package engine

// maintenance.go — API-driven maintenance window manager (storage-backed, B24).
//
// Maintenance windows suppress alerts for specific targets for a given
// duration. They are:
//   - Set via PUT  /cluster/maintenance (persisted to DB, gossip-replicated)
//   - Listed via GET /cluster/maintenance
//   - Cancelled via DELETE /cluster/maintenance/{id} (tombstone in DB)
//   - Persistence: SQLite table maintenance_windows (was maintenance.json
//     before B24; existing JSON files migrated to DB by storage/migrate)
//
// shouldAlert() consults IsInMaintenance() before dispatching any alert.
// Probes continue to run normally — only alert dispatch is suppressed.
//
// Architecture:
//   - The manager holds an in-memory cache (windows/byTarget maps) for
//     fast O(1) IsInMaintenance lookups on the alert hot path.
//   - Set/Cancel write to storage; storage broadcasts to peers via gossip.
//   - Storage Watch subscriptions keep the cache in sync with peer changes.
//   - PruneExpired writes tombstones to storage so peers reconcile.

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/saidtaylan/netwatch/internal/storage"
	"github.com/saidtaylan/netwatch/internal/storage/gossip"
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

// maintenanceManager manages active maintenance windows in memory + storage.
// All methods are safe for concurrent use.
//
// The in-memory cache is the authoritative read path for IsInMaintenance.
// It is updated from two sources:
//   - Local Set/Cancel calls (write to storage, also update cache directly)
//   - Storage Watch events (changes from peer broadcasts)
//
// Both paths converge on applyWindow/removeWindow which are mutex-protected.
type maintenanceManager struct {
	mu       sync.RWMutex
	windows  map[string]MaintenanceWindow // id → window (active only, cache)
	byTarget map[string][]string          // targetKey → []windowID

	storage *gossip.Storage // backing store; persists + broadcasts

	// watchCancel stops the Watch goroutine on Shutdown. The Watch
	// goroutine is best-effort; missed events are recoverable via the
	// next periodic anti-entropy sync.
	watchCancel context.CancelFunc
}

// newMaintenanceManager constructs a manager backed by the storage layer.
// Loads the current window set from storage into the in-memory cache,
// then starts a Watch goroutine to keep the cache in sync with peer
// broadcasts.
//
// Returns an error only when the initial storage List call fails. A nil
// storage argument is rejected — callers must ensure the engine's storage
// is initialized before creating the maintenance manager.
func newMaintenanceManager(parent context.Context, gs *gossip.Storage) (*maintenanceManager, error) {
	if gs == nil {
		return nil, fmt.Errorf("maintenance: nil storage")
	}
	ctx, cancel := context.WithCancel(parent)
	m := &maintenanceManager{
		windows:     make(map[string]MaintenanceWindow),
		byTarget:    make(map[string][]string),
		storage:     gs,
		watchCancel: cancel,
	}

	// Initial load from storage. This populates the cache with any
	// windows that survived restart (including those just migrated from
	// the old maintenance.json by the storage/migrate package).
	if err := m.loadFromStorage(ctx); err != nil {
		cancel()
		return nil, fmt.Errorf("maintenance: initial load: %w", err)
	}

	// Watch goroutine — keeps the cache in sync with peer changes.
	go m.watchStorageLoop(ctx)

	return m, nil
}

// Close stops the Watch goroutine. Safe to call multiple times.
func (m *maintenanceManager) Close() {
	if m.watchCancel != nil {
		m.watchCancel()
	}
}

// IsInMaintenance returns true when targetKey has at least one active
// (non-expired) maintenance window at the current time.
//
// Hot path: called for every alert dispatch decision. Must be O(1) and
// lock-light. The bounded-size byTarget cache makes this trivial.
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

// Set adds or replaces a maintenance window. Writes to storage (which
// broadcasts to peers automatically via the gossip layer) and updates
// the in-memory cache for immediate local visibility.
//
// Idempotent: setting the same ID again updates the existing window.
func (m *maintenanceManager) Set(w MaintenanceWindow) error {
	payload, err := json.Marshal(w)
	if err != nil {
		return fmt.Errorf("marshal window: %w", err)
	}

	// Storage write — broadcasts to peers, respects IsolatedMode (split-brain
	// protection: write fails fast with ErrSplitBrain).
	ver := m.storage.NextVersion()
	if err := m.storage.Upsert(context.Background(),
		storage.TableMaintenance, w.ID, payload, ver); err != nil {
		return fmt.Errorf("storage upsert: %w", err)
	}

	// Update local cache immediately so the next IsInMaintenance call
	// sees the change without waiting for the Watch event.
	m.mu.Lock()
	m.applyWindow(w)
	m.mu.Unlock()
	return nil
}

// Cancel removes a maintenance window by ID. Writes a tombstone to storage
// (replicated to peers) and removes from the in-memory cache.
//
// Missing IDs are silently ignored.
func (m *maintenanceManager) Cancel(id string) error {
	m.mu.RLock()
	_, exists := m.windows[id]
	m.mu.RUnlock()
	if !exists {
		return nil // already gone
	}

	ver := m.storage.NextVersion()
	if err := m.storage.Delete(context.Background(),
		storage.TableMaintenance, id, ver); err != nil {
		return fmt.Errorf("storage delete: %w", err)
	}

	m.mu.Lock()
	m.removeWindow(id)
	m.mu.Unlock()
	return nil
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

// PruneExpired removes windows that have already expired by writing
// tombstones to storage. Called periodically by runMaintenancePruner.
//
// Tombstones (rather than hard deletes) ensure that a peer who missed
// the original Cancel won't resurrect the window via an old upsert.
func (m *maintenanceManager) PruneExpired() {
	now := time.Now()

	m.mu.RLock()
	var expired []string
	for id, w := range m.windows {
		if !now.Before(w.ExpiresAt) {
			expired = append(expired, id)
		}
	}
	m.mu.RUnlock()

	for _, id := range expired {
		ver := m.storage.NextVersion()
		if err := m.storage.Delete(context.Background(),
			storage.TableMaintenance, id, ver); err != nil {
			slog.Warn("[MAINTENANCE] prune storage delete failed", "id", id, "err", err)
			continue
		}
		m.mu.Lock()
		m.removeWindow(id)
		m.mu.Unlock()
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

// ── internal: storage interaction ──────────────────────────────────────────

// loadFromStorage repopulates the in-memory cache from storage. Called
// once at startup. Expired windows are not added — they'll be tombstoned
// by the first PruneExpired tick.
func (m *maintenanceManager) loadFromStorage(ctx context.Context) error {
	recs, err := m.storage.List(ctx, storage.TableMaintenance, storage.Filter{})
	if err != nil {
		return err
	}
	now := time.Now()

	m.mu.Lock()
	defer m.mu.Unlock()

	for _, rec := range recs {
		if rec.Tombstone {
			continue
		}
		var w MaintenanceWindow
		if err := json.Unmarshal(rec.Payload, &w); err != nil {
			slog.Warn("[MAINTENANCE] malformed window in storage", "id", rec.ID, "err", err)
			continue
		}
		if !now.Before(w.ExpiresAt) {
			continue // expired — let PruneExpired tombstone it later
		}
		m.applyWindow(w)
	}
	if n := len(m.windows); n > 0 {
		slog.Info("[MAINTENANCE] loaded from storage", "count", n)
	}
	return nil
}

// watchStorageLoop receives change events from storage and applies them
// to the in-memory cache. This is how peer broadcasts reach the
// maintenance manager: storage layer applies the remote upsert/delete,
// emits an Event, this loop translates it into a cache update.
//
// Stops when ctx is cancelled (engine shutdown).
func (m *maintenanceManager) watchStorageLoop(ctx context.Context) {
	ch, err := m.storage.Watch(ctx, storage.TableMaintenance)
	if err != nil {
		slog.Warn("[MAINTENANCE] watch failed", "err", err)
		return
	}
	for evt := range ch {
		m.applyStorageEvent(evt)
	}
}

// applyStorageEvent updates the cache for one storage change event.
// Handles both upserts (apply) and deletes (remove). Idempotent for
// our own writes — applyWindow is a set, removeWindow is a no-op when
// missing.
func (m *maintenanceManager) applyStorageEvent(evt storage.Event) {
	switch evt.Type {
	case storage.EventUpsert:
		var w MaintenanceWindow
		if err := json.Unmarshal(evt.Record.Payload, &w); err != nil {
			slog.Warn("[MAINTENANCE] watch unmarshal failed", "id", evt.Record.ID, "err", err)
			return
		}
		m.mu.Lock()
		m.applyWindow(w)
		m.mu.Unlock()
	case storage.EventDelete:
		m.mu.Lock()
		m.removeWindow(evt.Record.ID)
		m.mu.Unlock()
	}
}

// ── internal: cache mutators (must hold mu) ────────────────────────────────

// applyWindow inserts or replaces a window in both maps. Caller must
// hold m.mu. Handles re-targeting cleanly: if the window already exists
// with different TargetIDs, the old byTarget entries are removed before
// the new ones are added.
func (m *maintenanceManager) applyWindow(w MaintenanceWindow) {
	if old, ok := m.windows[w.ID]; ok {
		for _, tID := range old.TargetIDs {
			m.removeFromIndex(tID, w.ID)
		}
	}
	m.windows[w.ID] = w
	for _, tID := range w.TargetIDs {
		m.byTarget[tID] = append(m.byTarget[tID], w.ID)
	}
}

// removeWindow deletes a window from both maps. Caller must hold m.mu.
func (m *maintenanceManager) removeWindow(id string) {
	w, ok := m.windows[id]
	if !ok {
		return
	}
	for _, tID := range w.TargetIDs {
		m.removeFromIndex(tID, id)
	}
	delete(m.windows, id)
}

// removeFromIndex removes a single windowID from a byTarget slice.
// Caller must hold m.mu.
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
