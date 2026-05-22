// Package migrate copies legacy JSON-file persistence (state.json,
// incidents.json, maintenance.json) into the new StorageBackend tables.
//
// Each migrator:
//  1. Reads the JSON file (if present — returns no-op cleanly if absent)
//  2. Parses it according to its versioned envelope
//  3. Writes each record to the corresponding StorageBackend table
//  4. Renames the JSON file to <name>.migrated so future boots skip it
//
// This package is intentionally **read-only** with respect to the engine's
// runtime state — the engine continues to use its own in-memory structures.
// Migration is one-shot at boot time: storage now becomes the source of
// truth, but the existing engine code path is unchanged in this sprint.
// The engine→storage rewire happens in B24.
//
// Why archive instead of delete?
//   - Bug recovery: if migration corrupts something, the operator has the
//     original file to restore from.
//   - Re-run safety: future boots see the .migrated suffix and skip.
//   - Audit: the original timestamps + content remain on disk.
package migrate

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/saidtaylan/netwatch/internal/storage"
)

// migratedSuffix is appended to JSON files after a successful migration so
// subsequent boots ignore them. Operators can rename them back if they
// need to re-run migration after wiping the storage backend.
const migratedSuffix = ".migrated"

// Result summarizes what one migrator did. Returned even on no-op so the
// caller can log a clear "1 record migrated, 2 files skipped" message.
type Result struct {
	Source        string // original file path (e.g. /var/lib/netwatch/state.json)
	Records       int    // number of records written to storage
	Skipped       bool   // true if file didn't exist or was already migrated
	ArchivedAs    string // path of the .migrated archive (empty when skipped)
	ParseError    error  // non-nil when file existed but parse failed (no archive)
}

// RunAll executes every migrator in sequence against the provided
// StorageBackend. `dataDir` is the directory that historically held the
// JSON files (e.g. dirname(state.json)). `nodeName` is used as the
// Version.UpdatedBy for migrated records so anti-entropy can later
// determine origin.
//
// RunAll is idempotent — running it twice on the same dataDir is a no-op
// after the first run (the files are archived). Errors in individual
// migrators don't stop the others; they're collected into the returned
// joined error.
func RunAll(ctx context.Context, backend storage.StorageBackend, dataDir, nodeName string) ([]Result, error) {
	if dataDir == "" {
		return nil, fmt.Errorf("migrate: empty data_dir")
	}

	migrators := []struct {
		name string
		fn   func(context.Context, storage.StorageBackend, string, string) (Result, error)
	}{
		{"state.json", MigrateState},
		{"incidents.json", MigrateIncidents},
		{"maintenance.json", MigrateMaintenance},
	}

	results := make([]Result, 0, len(migrators))
	var errs []error
	for _, m := range migrators {
		res, err := m.fn(ctx, backend, dataDir, nodeName)
		results = append(results, res)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", m.name, err))
		} else if !res.Skipped {
			slog.Info("[STORAGE-MIGRATE] migrated",
				"file", res.Source, "records", res.Records, "archive", res.ArchivedAs)
		}
	}
	return results, errors.Join(errs...)
}

// archiveFile renames `src` to `src + migratedSuffix`. Atomic on local
// filesystems. If the .migrated file already exists, it's overwritten —
// this is fine because both should contain the same data (re-runs are
// idempotent).
func archiveFile(src string) (string, error) {
	dst := src + migratedSuffix
	if err := os.Rename(src, dst); err != nil {
		return "", err
	}
	return dst, nil
}

// fileExistsAndReadable returns the file content and true when readable,
// or nil/false (with no error) when the file simply doesn't exist.
// Other errors (permission, etc.) are returned as the second return.
func fileExistsAndReadable(path string) ([]byte, bool, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, false, nil
		}
		return nil, false, err
	}
	return data, true, nil
}

// ── state.json → target_states ──────────────────────────────────────────────

// persistedState mirrors engine.PersistedState. Duplicated here so the
// migrate package has no dependency on internal/engine (which would
// create an import cycle once engine starts using internal/storage).
type persistedState struct {
	State     string `json:"state"`
	Seq       uint64 `json:"seq"`
	ErrorCode string `json:"error_code,omitempty"`
	OwnerNode string `json:"owner_node,omitempty"`
}

type stateFileV2 struct {
	Version int                       `json:"version"`
	Targets map[string]persistedState `json:"targets"`
}

// MigrateState copies state.json → target_states. The Seq field of each
// PersistedState becomes the storage.Version.Seq directly — this preserves
// causal ordering with the existing in-memory state machine.
//
// V1 (plain map[string]bool) is also supported via a fallback parse.
func MigrateState(ctx context.Context, backend storage.StorageBackend, dataDir, nodeName string) (Result, error) {
	path := filepath.Join(dataDir, "state.json")
	res := Result{Source: path}

	data, ok, err := fileExistsAndReadable(path)
	if err != nil {
		return res, err
	}
	if !ok {
		res.Skipped = true
		return res, nil
	}

	// Try v2 first
	var v2 stateFileV2
	targets := map[string]persistedState{}
	if err := json.Unmarshal(data, &v2); err == nil && v2.Version == 2 && v2.Targets != nil {
		targets = v2.Targets
	} else {
		// Fall back to v1: plain map[string]bool
		var v1 map[string]bool
		if err := json.Unmarshal(data, &v1); err != nil {
			res.ParseError = err
			return res, fmt.Errorf("parse state.json: %w", err)
		}
		for id, up := range v1 {
			state := "up"
			if !up {
				state = "hard_down"
			}
			targets[id] = persistedState{State: state, Seq: 1}
		}
	}

	now := time.Now().UTC()
	for id, ps := range targets {
		payload, err := json.Marshal(ps)
		if err != nil {
			return res, fmt.Errorf("marshal %s: %w", id, err)
		}
		ver := storage.Version{
			Seq:       ps.Seq,
			UpdatedAt: now,
			UpdatedBy: nodeName,
		}
		// Migration writes are always new — but if a previous partial
		// migration left some rows, the staleness check rejects with
		// ErrStaleWrite. Treat that as a benign skip.
		err = backend.Upsert(ctx, storage.TableTargetStates, id, payload, ver)
		if err != nil && !errors.Is(err, storage.ErrStaleWrite) {
			return res, fmt.Errorf("upsert %s: %w", id, err)
		}
		res.Records++
	}

	archive, err := archiveFile(path)
	if err != nil {
		return res, fmt.Errorf("archive: %w", err)
	}
	res.ArchivedAs = archive
	return res, nil
}

// ── incidents.json → slo_incidents ──────────────────────────────────────────

type incidentRecord struct {
	TargetID    string     `json:"target_id"`
	StartedAt   time.Time  `json:"started_at"`
	EndedAt     *time.Time `json:"ended_at,omitempty"`
	DurationSec int64      `json:"duration_sec,omitempty"`
	Scope       string     `json:"scope,omitempty"`
	ErrorCode   string     `json:"error_code,omitempty"`
}

type incidentFileV1 struct {
	Version   int              `json:"version"`
	Incidents []incidentRecord `json:"incidents"`
}

// MigrateIncidents copies incidents.json → slo_incidents. Each incident
// gets a synthesized ID of `<target_id>-<unix_started_at>` for stable
// deduplication across re-runs.
func MigrateIncidents(ctx context.Context, backend storage.StorageBackend, dataDir, nodeName string) (Result, error) {
	path := filepath.Join(dataDir, "incidents.json")
	res := Result{Source: path}

	data, ok, err := fileExistsAndReadable(path)
	if err != nil {
		return res, err
	}
	if !ok {
		res.Skipped = true
		return res, nil
	}

	var envelope incidentFileV1
	if err := json.Unmarshal(data, &envelope); err != nil {
		// Try plain array (some older formats)
		var arr []incidentRecord
		if err2 := json.Unmarshal(data, &arr); err2 != nil {
			res.ParseError = err
			return res, fmt.Errorf("parse incidents.json: %w", err)
		}
		envelope.Incidents = arr
	}

	now := time.Now().UTC()
	for _, inc := range envelope.Incidents {
		id := fmt.Sprintf("%s-%d", inc.TargetID, inc.StartedAt.UTC().Unix())
		payload, err := json.Marshal(inc)
		if err != nil {
			return res, fmt.Errorf("marshal %s: %w", id, err)
		}
		ver := storage.Version{
			Seq:       uint64(inc.StartedAt.UTC().Unix()), // monotonic per target
			UpdatedAt: now,
			UpdatedBy: nodeName,
		}
		err = backend.Upsert(ctx, storage.TableSLOIncidents, id, payload, ver)
		if err != nil && !errors.Is(err, storage.ErrStaleWrite) {
			return res, fmt.Errorf("upsert %s: %w", id, err)
		}
		res.Records++
	}

	archive, err := archiveFile(path)
	if err != nil {
		return res, fmt.Errorf("archive: %w", err)
	}
	res.ArchivedAs = archive
	return res, nil
}

// ── maintenance.json → maintenance_windows ──────────────────────────────────

type maintenanceWindow struct {
	ID        string    `json:"id"`
	TargetIDs []string  `json:"target_ids"`
	StartedAt time.Time `json:"started_at"`
	ExpiresAt time.Time `json:"expires_at"`
	Reason    string    `json:"reason,omitempty"`
	StartedBy string    `json:"started_by,omitempty"`
}

type maintenanceFile struct {
	Version int                 `json:"version"`
	Windows []maintenanceWindow `json:"windows"`
}

// MigrateMaintenance copies maintenance.json → maintenance_windows. The
// `id` field already exists in the source so no synthesis is needed.
func MigrateMaintenance(ctx context.Context, backend storage.StorageBackend, dataDir, nodeName string) (Result, error) {
	path := filepath.Join(dataDir, "maintenance.json")
	res := Result{Source: path}

	data, ok, err := fileExistsAndReadable(path)
	if err != nil {
		return res, err
	}
	if !ok {
		res.Skipped = true
		return res, nil
	}

	var envelope maintenanceFile
	if err := json.Unmarshal(data, &envelope); err != nil {
		res.ParseError = err
		return res, fmt.Errorf("parse maintenance.json: %w", err)
	}

	now := time.Now().UTC()
	for _, w := range envelope.Windows {
		if w.ID == "" {
			continue // skip malformed
		}
		payload, err := json.Marshal(w)
		if err != nil {
			return res, fmt.Errorf("marshal %s: %w", w.ID, err)
		}
		ver := storage.Version{
			Seq:       uint64(w.StartedAt.UTC().Unix()),
			UpdatedAt: now,
			UpdatedBy: nodeName,
		}
		err = backend.Upsert(ctx, storage.TableMaintenance, w.ID, payload, ver)
		if err != nil && !errors.Is(err, storage.ErrStaleWrite) {
			return res, fmt.Errorf("upsert %s: %w", w.ID, err)
		}
		res.Records++
	}

	archive, err := archiveFile(path)
	if err != nil {
		return res, fmt.Errorf("archive: %w", err)
	}
	res.ArchivedAs = archive
	return res, nil
}
