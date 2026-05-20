package engine

// maintenance.go — API-driven maintenance window manager.
//
// Maintenance windows suppress alerts for specific targets for a given
// duration. They are:
//   - Set via PUT  /cluster/maintenance (gossip-replicated to all nodes)
//   - Listed via GET /cluster/maintenance
//   - Cancelled via DELETE /cluster/maintenance/{id}
//   - Persisted to maintenance.json so they survive agent restarts
//   - Never written to config.yaml (runtime state, not config)
//
// shouldAlert() consults IsInMaintenance() before dispatching any alert.
// Probes continue to run normally — only alert dispatch is suppressed.

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// MaintenanceWindow describes a single ad-hoc maintenance period.
type MaintenanceWindow struct {
	// ID uniquely identifies this window for cancellation.
	// Format: "mw-<RFC3339>-<random4>" e.g. "mw-20260516T163000Z-a3b1"
	ID string `json:"id"`

	// TargetIDs is the list of target keys (IDs or names) under maintenance.
	TargetIDs []string `json:"target_ids"`

	StartedAt time.Time `json:"started_at"`
	ExpiresAt time.Time `json:"expires_at"`

	// Reason is optional free-text for audit.
	Reason string `json:"reason,omitempty"`

	// StartedBy is an optional human/service identifier.
	StartedBy string `json:"started_by,omitempty"`
}

// maintenanceFile is the on-disk format.
type maintenanceFile struct {
	Version int                  `json:"version"`
	Windows []MaintenanceWindow  `json:"windows"`
}

// maintenanceManager manages active maintenance windows in memory + on disk.
// All methods are safe for concurrent use.
type maintenanceManager struct {
	mu       sync.RWMutex
	windows  map[string]MaintenanceWindow // id → window
	byTarget map[string][]string          // targetKey → []windowID (for fast lookup)
	path     string                       // absolute path to maintenance.json
}

func newMaintenanceManager(stateFilePath string) *maintenanceManager {
	// Place maintenance.json next to state.json.
	dir := filepath.Dir(stateFilePath)
	m := &maintenanceManager{
		windows:  make(map[string]MaintenanceWindow),
		byTarget: make(map[string][]string),
		path:     filepath.Join(dir, "maintenance.json"),
	}
	m.load()
	return m
}

// IsInMaintenance returns true when targetKey has at least one active
// (non-expired) maintenance window at the current time.
func (m *maintenanceManager) IsInMaintenance(targetKey string) bool {
	now := time.Now()
	m.mu.RLock()
	defer m.mu.RUnlock()
	for _, wid := range m.byTarget[targetKey] {
		if w, ok := m.windows[wid]; ok && now.Before(w.ExpiresAt) {
			return true
		}
	}
	return false
}

// Set adds or replaces a maintenance window and persists to disk.
// It is idempotent: setting the same ID again updates ExpiresAt / Reason.
func (m *maintenanceManager) Set(w MaintenanceWindow) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Remove old index entries if this is an update.
	if old, ok := m.windows[w.ID]; ok {
		for _, tID := range old.TargetIDs {
			m.removeFromIndex(tID, w.ID)
		}
	}

	m.windows[w.ID] = w
	for _, tID := range w.TargetIDs {
		m.byTarget[tID] = append(m.byTarget[tID], w.ID)
	}

	return m.save()
}

// Cancel removes a maintenance window by ID and persists.
// Returns an error only on save failure; a missing ID is silently ignored.
func (m *maintenanceManager) Cancel(id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	w, ok := m.windows[id]
	if !ok {
		return nil // already gone
	}
	for _, tID := range w.TargetIDs {
		m.removeFromIndex(tID, id)
	}
	delete(m.windows, id)
	return m.save()
}

// List returns a snapshot of all non-expired maintenance windows.
func (m *maintenanceManager) List() []MaintenanceWindow {
	now := time.Now()
	m.mu.RLock()
	defer m.mu.RUnlock()

	out := make([]MaintenanceWindow, 0, len(m.windows))
	for _, w := range m.windows {
		if now.Before(w.ExpiresAt) {
			out = append(out, w)
		}
	}
	return out
}

// PruneExpired removes windows that have already expired.
// Called periodically and on startup.
func (m *maintenanceManager) PruneExpired() {
	now := time.Now()
	m.mu.Lock()
	defer m.mu.Unlock()

	changed := false
	for id, w := range m.windows {
		if !now.Before(w.ExpiresAt) {
			for _, tID := range w.TargetIDs {
				m.removeFromIndex(tID, id)
			}
			delete(m.windows, id)
			changed = true
		}
	}
	if changed {
		_ = m.save()
	}
}

// GenerateWindowID returns a unique maintenance window ID.
func GenerateWindowID() string {
	b := make([]byte, 4)
	_, _ = rand.Read(b)
	return fmt.Sprintf("mw-%s-%s",
		time.Now().UTC().Format("20060102T150405Z"),
		hex.EncodeToString(b)[:6])
}

// ── internal helpers ──────────────────────────────────────────────────────────

func (m *maintenanceManager) removeFromIndex(targetKey, windowID string) {
	ids := m.byTarget[targetKey]
	out := ids[:0]
	for _, id := range ids {
		if id != windowID {
			out = append(out, id)
		}
	}
	if len(out) == 0 {
		delete(m.byTarget, targetKey)
	} else {
		m.byTarget[targetKey] = out
	}
}

func (m *maintenanceManager) load() {
	data, err := os.ReadFile(m.path)
	if os.IsNotExist(err) {
		return // first run, no file yet
	}
	if err != nil {
		slog.Warn("maintenance: cannot read file, starting empty", "path", m.path, "err", err)
		return
	}

	var f maintenanceFile
	if err := json.Unmarshal(data, &f); err != nil {
		slog.Warn("maintenance: malformed file, starting empty", "path", m.path, "err", err)
		return
	}

	now := time.Now()
	loaded := 0
	for _, w := range f.Windows {
		if !now.Before(w.ExpiresAt) {
			continue // already expired, skip
		}
		m.windows[w.ID] = w
		for _, tID := range w.TargetIDs {
			m.byTarget[tID] = append(m.byTarget[tID], w.ID)
		}
		loaded++
	}
	if loaded > 0 {
		slog.Info("maintenance: loaded windows", "count", loaded, "path", m.path)
	}
}

func (m *maintenanceManager) save() error {
	now := time.Now()
	var active []MaintenanceWindow
	for _, w := range m.windows {
		if now.Before(w.ExpiresAt) {
			active = append(active, w)
		}
	}

	f := maintenanceFile{Version: 1, Windows: active}
	data, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return fmt.Errorf("maintenance: marshal: %w", err)
	}

	tmp := m.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0644); err != nil {
		return fmt.Errorf("maintenance: write tmp: %w", err)
	}
	if err := os.Rename(tmp, m.path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("maintenance: rename: %w", err)
	}
	return nil
}
