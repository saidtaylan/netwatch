package engine

// targets_manager.go — storage-backed Target registry (B24.6).
//
// Targets are the probes themselves — the most operationally sensitive
// data in netwatch. Migrating them to storage carries the highest risk
// of the B24 sprint because they drive:
//   - probe goroutine lifecycle (each target = its own loop)
//   - cluster prober assignments (hash ring + zone-aware spread)
//   - state machine persistence (state.json key = target.key())
//   - dependency graph (depends_on cycles caught at validation time)
//   - app index (apps reference targets)
//
// Storage model: storage.TableTargets, cluster-replicated via gossip.
// First-boot seed: cfg.Targets migrated to DB once when the table is
// empty. After that, config.yaml's targets: section is IGNORED — DB is
// authoritative. CRUD via engine.UpsertTarget / engine.DeleteTarget.
//
// On every target-set change (local CRUD or peer broadcast), the
// reconcileFn callback is invoked with the new full target slice. The
// engine implements reconcileFn as a wrapper around the same reconciliation
// pipeline that hot-reload of config.yaml uses — keeping behaviour
// consistent between the two paths.

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sort"
	"sync"

	"github.com/saidtaylan/netwatch/internal/storage"
	"github.com/saidtaylan/netwatch/internal/storage/gossip"
)

// targetsManager owns the storage-backed target registry.
//
// reconcileFn is the engine-supplied callback that applies a new target
// set to the live engine (validates, purges stale state, rebuilds the
// dependency graph, restarts prober assignments, etc). It must be
// idempotent — Watch events may deliver duplicate snapshots.
type targetsManager struct {
	mu sync.RWMutex

	storage  *gossip.Storage
	nodeName string

	// targets holds the live registry; key = target.key() (ID or Name).
	targets map[string]Target

	// reconcileFn is invoked with the full target slice on every change.
	// nil callback → manager still persists changes but doesn't notify.
	reconcileFn func(targets []Target) error

	watchCancel context.CancelFunc
}

// newTargetsManager constructs a storage-backed target manager.
//
// seedTargets: first-boot migration from config.yaml. Written to DB once
// when the table is empty. Subsequent edits to config.yaml's targets:
// section are ignored — DB is authoritative.
//
// reconcileFn: engine callback that applies a new target set to the live
// runtime (state purge, probe assignments, dependency graph). Errors from
// reconcileFn are logged but never block storage Watch progress — a bad
// remote write shouldn't stall the local Watch loop.
func newTargetsManager(
	parent context.Context,
	gs *gossip.Storage,
	nodeName string,
	seedTargets []Target,
	reconcileFn func([]Target) error,
) (*targetsManager, error) {
	if gs == nil {
		return nil, fmt.Errorf("targets: nil storage")
	}
	ctx, cancel := context.WithCancel(parent)
	m := &targetsManager{
		storage:     gs,
		nodeName:    nodeName,
		targets:     make(map[string]Target),
		reconcileFn: reconcileFn,
		watchCancel: cancel,
	}

	if err := m.loadFromStorage(ctx); err != nil {
		cancel()
		return nil, fmt.Errorf("targets: initial load: %w", err)
	}

	// First-boot seed: write cfg.Targets to DB once. We do NOT call the
	// reconcile callback during seed — the engine is already initialized
	// with these exact targets via cfg.Targets, so reconcile would be a
	// no-op. Calling it would also race against the engine's normal Init
	// sequence (probe goroutines being launched concurrently).
	if len(m.targets) == 0 && len(seedTargets) > 0 {
		slog.Info("targets: seeding from config.yaml", "count", len(seedTargets))
		for _, t := range seedTargets {
			if err := m.persistTarget(t); err != nil {
				slog.Warn("targets: seed upsert failed", "id", t.key(), "err", err)
				continue
			}
			m.mu.Lock()
			m.targets[t.key()] = t
			m.mu.Unlock()
		}
	}

	go m.watchLoop(ctx)
	return m, nil
}

// Close stops the Watch goroutine. Safe to call multiple times.
func (m *targetsManager) Close() {
	if m.watchCancel != nil {
		m.watchCancel()
	}
}

// Targets returns a deterministic snapshot of the current target list.
// Sorted by target.key() so callers iterating get stable ordering
// across calls (matters for cluster prober assignment determinism —
// any subtle ordering drift would push targets to different probers).
func (m *targetsManager) Targets() []Target {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]Target, 0, len(m.targets))
	for _, t := range m.targets {
		out = append(out, t)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].key() < out[j].key() })
	return out
}

// Get returns the target by key (ID or Name), or (zero, false).
func (m *targetsManager) Get(key string) (Target, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	t, ok := m.targets[key]
	return t, ok
}

// Upsert adds or replaces a target. Cluster-replicated via gossip.
// After persisting, invokes the engine's reconcileFn with the new full
// target slice — this is where probe goroutines start/stop, state purges,
// and prober reassignments happen.
//
// If reconcileFn returns an error, the storage write is NOT rolled back
// (gossip already broadcast it). The error is returned so the caller
// can surface it via HTTP. Operators should fix the underlying problem
// (e.g. bad regex in target name) and re-issue.
func (m *targetsManager) Upsert(t Target) error {
	if t.key() == "" {
		return fmt.Errorf("targets: empty id and name")
	}
	if err := m.persistTarget(t); err != nil {
		return err
	}
	m.mu.Lock()
	m.targets[t.key()] = t
	snapshot := m.snapshotLocked()
	m.mu.Unlock()
	return m.reconcile(snapshot)
}

// Delete removes a target by key (ID or Name). Returns false when the
// target did not exist. Tombstone is gossip-replicated; reconcileFn
// then tears down the probe goroutine for the removed target.
func (m *targetsManager) Delete(key string) (bool, error) {
	m.mu.RLock()
	_, exists := m.targets[key]
	m.mu.RUnlock()
	if !exists {
		return false, nil
	}
	ver := m.storage.NextVersion()
	if err := m.storage.Delete(context.Background(),
		storage.TableTargets, key, ver); err != nil {
		return false, fmt.Errorf("targets: storage delete: %w", err)
	}
	m.mu.Lock()
	delete(m.targets, key)
	snapshot := m.snapshotLocked()
	m.mu.Unlock()
	if err := m.reconcile(snapshot); err != nil {
		// Storage tombstone already broadcast; surface the reconcile error
		// but don't fail the delete (the target IS gone from registry).
		slog.Warn("targets: reconcile after delete failed", "id", key, "err", err)
	}
	return true, nil
}

// persistTarget writes a target to storage. Separate from Upsert so the
// seed path can reuse it without triggering reconciliation.
func (m *targetsManager) persistTarget(t Target) error {
	payload, err := json.Marshal(t)
	if err != nil {
		return fmt.Errorf("targets: marshal: %w", err)
	}
	ver := m.storage.NextVersion()
	if err := m.storage.Upsert(context.Background(),
		storage.TableTargets, t.key(), payload, ver); err != nil {
		return fmt.Errorf("targets: storage upsert: %w", err)
	}
	return nil
}

// reconcile invokes the engine callback. nil-safe.
func (m *targetsManager) reconcile(targets []Target) error {
	if m.reconcileFn == nil {
		return nil
	}
	return m.reconcileFn(targets)
}

// snapshotLocked returns a sorted copy of m.targets. Caller must hold m.mu.
func (m *targetsManager) snapshotLocked() []Target {
	out := make([]Target, 0, len(m.targets))
	for _, t := range m.targets {
		out = append(out, t)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].key() < out[j].key() })
	return out
}

// ── internal: storage interaction ──────────────────────────────────────

// loadFromStorage repopulates the in-memory map from storage. Does NOT
// trigger reconcileFn — the engine handles initial wiring separately
// via Init.
func (m *targetsManager) loadFromStorage(ctx context.Context) error {
	recs, err := m.storage.List(ctx, storage.TableTargets, storage.Filter{})
	if err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, rec := range recs {
		if rec.Tombstone {
			continue
		}
		var t Target
		if err := json.Unmarshal(rec.Payload, &t); err != nil {
			slog.Warn("targets: malformed record in storage", "id", rec.ID, "err", err)
			continue
		}
		m.targets[t.key()] = t
	}
	if n := len(m.targets); n > 0 {
		slog.Info("targets: loaded from storage", "count", n)
	}
	return nil
}

// watchLoop receives change events for the targets table and triggers
// reconciliation. Peer Upsert/Delete arrives here via the gossip storage
// layer. The reconcileFn callback handles probe goroutine lifecycle.
func (m *targetsManager) watchLoop(ctx context.Context) {
	ch, err := m.storage.Watch(ctx, storage.TableTargets)
	if err != nil {
		slog.Warn("targets: watch failed", "err", err)
		return
	}
	for evt := range ch {
		switch evt.Type {
		case storage.EventUpsert:
			var t Target
			if err := json.Unmarshal(evt.Record.Payload, &t); err != nil {
				slog.Warn("targets: watch unmarshal failed", "id", evt.Record.ID, "err", err)
				continue
			}
			m.mu.Lock()
			m.targets[t.key()] = t
			snap := m.snapshotLocked()
			m.mu.Unlock()
			if err := m.reconcile(snap); err != nil {
				slog.Warn("targets: reconcile from peer upsert failed", "id", t.key(), "err", err)
			}
		case storage.EventDelete:
			m.mu.Lock()
			delete(m.targets, evt.Record.ID)
			snap := m.snapshotLocked()
			m.mu.Unlock()
			if err := m.reconcile(snap); err != nil {
				slog.Warn("targets: reconcile from peer delete failed", "id", evt.Record.ID, "err", err)
			}
		}
	}
}
