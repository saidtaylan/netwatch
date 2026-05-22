package engine

// apps_manager.go — storage-backed App registry (B24.3).
//
// Apps are logical applications that reference one or more targets. They
// enrich alerts with AFFECTED_APPS and OWNER_TEAMS context.
//
// Before B24.3, apps were defined exclusively in config.yaml and rebuilt
// on every hot-reload. Now:
//
//   - Apps live in storage.TableApps (cluster-replicated via gossip).
//   - On first boot, config.yaml's apps section seeds the table once.
//     Subsequent edits to config.yaml's apps section are ignored — DB
//     is authoritative going forward.
//   - The engine's appIndex is rebuilt from the storage-backed registry
//     whenever (a) apps change in storage, or (b) target keys change on
//     hot-reload (so newly-added targets pick up their app references).
//
// Hot path: appIndex lookups on alert dispatch (notify.go) and fleet
// status (fleet.go) read e.appIndex under e.mu.RLock(). The manager
// publishes index updates by acquiring e.mu and replacing the snapshot.

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sync"

	"github.com/saidtaylan/netwatch/internal/storage"
	"github.com/saidtaylan/netwatch/internal/storage/gossip"
)

// appsManager owns the storage-backed apps registry and pushes a rebuilt
// AppTargetIndex into the engine whenever the registry changes.
//
// Concurrency model:
//   - mu protects the apps map and the indexPublisher closure.
//   - Storage Watch goroutine receives peer changes and triggers
//     publishIndex which acquires e.mu (passed via the closure) to swap
//     the engine's snapshot atomically.
type appsManager struct {
	mu sync.RWMutex

	storage  *gossip.Storage
	nodeName string

	// apps holds the live registry; key = app name.
	apps map[string]App

	// indexPublisher is the engine-supplied callback that swaps the
	// engine's appIndex snapshot. nil-safe — used only when set.
	indexPublisher func(idx AppTargetIndex)

	// targetKeysFn returns the current set of active target keys, used
	// to build the index. Provided by the engine so we don't import the
	// engine type into this file.
	targetKeysFn func() []string

	watchCancel context.CancelFunc
}

// newAppsManager constructs a storage-backed apps manager.
//
// seedApps: if the storage table is empty AND this slice is non-empty, the
// manager performs a one-time seed (typical first-boot path from config.yaml).
//
// indexPublisher: invoked with the freshly-built AppTargetIndex whenever
// the apps registry changes. The engine uses this to keep e.appIndex in
// sync with peer broadcasts.
//
// targetKeysFn: returns the current active target keys; needed to build
// the index. Apps may reference targets by either ID or Name — the index
// stores both spellings keyed by Target.key().
func newAppsManager(
	parent context.Context,
	gs *gossip.Storage,
	nodeName string,
	seedApps []App,
	targetKeysFn func() []string,
	indexPublisher func(idx AppTargetIndex),
) (*appsManager, error) {
	if gs == nil {
		return nil, fmt.Errorf("apps: nil storage")
	}
	if targetKeysFn == nil {
		targetKeysFn = func() []string { return nil }
	}
	ctx, cancel := context.WithCancel(parent)
	m := &appsManager{
		storage:        gs,
		nodeName:       nodeName,
		apps:           make(map[string]App),
		indexPublisher: indexPublisher,
		targetKeysFn:   targetKeysFn,
		watchCancel:    cancel,
	}

	if err := m.loadFromStorage(ctx); err != nil {
		cancel()
		return nil, fmt.Errorf("apps: initial load: %w", err)
	}

	// First-boot seed: if storage is empty AND config.yaml had apps, write
	// them once so operators don't need to migrate manually.
	if len(m.apps) == 0 && len(seedApps) > 0 {
		slog.Info("apps: seeding from config.yaml", "count", len(seedApps))
		for _, a := range seedApps {
			if err := m.Upsert(a); err != nil {
				slog.Warn("apps: seed upsert failed", "name", a.Name, "err", err)
			}
		}
	}

	// Push the initial index into the engine.
	m.publishIndex()

	go m.watchLoop(ctx)
	return m, nil
}

// Close stops the Watch goroutine. Safe to call multiple times.
func (m *appsManager) Close() {
	if m.watchCancel != nil {
		m.watchCancel()
	}
}

// Apps returns a snapshot of all currently-known apps.
func (m *appsManager) Apps() []App {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]App, 0, len(m.apps))
	for _, a := range m.apps {
		out = append(out, a)
	}
	return out
}

// Get returns the app by name, or (zero, false).
func (m *appsManager) Get(name string) (App, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	a, ok := m.apps[name]
	return a, ok
}

// Upsert adds or replaces an app. Writes to storage (gossip-replicated),
// updates the local cache, then republishes the index.
func (m *appsManager) Upsert(a App) error {
	if a.Name == "" {
		return fmt.Errorf("apps: empty name")
	}
	payload, err := json.Marshal(a)
	if err != nil {
		return fmt.Errorf("apps: marshal: %w", err)
	}
	ver := m.storage.NextVersion()
	if err := m.storage.Upsert(context.Background(),
		storage.TableApps, a.Name, payload, ver); err != nil {
		return fmt.Errorf("apps: storage upsert: %w", err)
	}
	m.mu.Lock()
	m.apps[a.Name] = a
	m.mu.Unlock()
	m.publishIndex()
	return nil
}

// Delete removes an app. Writes a tombstone (gossip-replicated).
// Returns false when the app did not exist.
func (m *appsManager) Delete(name string) (bool, error) {
	m.mu.RLock()
	_, exists := m.apps[name]
	m.mu.RUnlock()
	if !exists {
		return false, nil
	}
	ver := m.storage.NextVersion()
	if err := m.storage.Delete(context.Background(),
		storage.TableApps, name, ver); err != nil {
		return false, fmt.Errorf("apps: storage delete: %w", err)
	}
	m.mu.Lock()
	delete(m.apps, name)
	m.mu.Unlock()
	m.publishIndex()
	return true, nil
}

// RebuildIndex re-runs publishIndex. The engine calls this on hot-reload
// when target keys change — apps may now reference new targets.
func (m *appsManager) RebuildIndex() {
	m.publishIndex()
}

// ── internal: storage interaction ──────────────────────────────────────

// loadFromStorage rehydrates the apps map from the storage table.
func (m *appsManager) loadFromStorage(ctx context.Context) error {
	recs, err := m.storage.List(ctx, storage.TableApps, storage.Filter{})
	if err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, rec := range recs {
		if rec.Tombstone {
			continue
		}
		var a App
		if err := json.Unmarshal(rec.Payload, &a); err != nil {
			slog.Warn("apps: malformed record in storage", "id", rec.ID, "err", err)
			continue
		}
		m.apps[a.Name] = a
	}
	if n := len(m.apps); n > 0 {
		slog.Info("apps: loaded from storage", "count", n)
	}
	return nil
}

// watchLoop applies peer changes from storage to the local registry,
// then republishes the index so the engine sees the new state.
func (m *appsManager) watchLoop(ctx context.Context) {
	ch, err := m.storage.Watch(ctx, storage.TableApps)
	if err != nil {
		slog.Warn("apps: watch failed", "err", err)
		return
	}
	for evt := range ch {
		switch evt.Type {
		case storage.EventUpsert:
			var a App
			if err := json.Unmarshal(evt.Record.Payload, &a); err != nil {
				slog.Warn("apps: watch unmarshal failed", "id", evt.Record.ID, "err", err)
				continue
			}
			m.mu.Lock()
			m.apps[a.Name] = a
			m.mu.Unlock()
		case storage.EventDelete:
			m.mu.Lock()
			delete(m.apps, evt.Record.ID)
			m.mu.Unlock()
		}
		m.publishIndex()
	}
}

// publishIndex builds an AppTargetIndex from the current apps registry
// and hands it to the engine via the indexPublisher callback.
//
// The index is built defensively: app references that don't match any
// current target are silently dropped. validateApps (config-time) is
// stricter; runtime keeps going so an orphaned reference doesn't disable
// alerting altogether.
func (m *appsManager) publishIndex() {
	if m.indexPublisher == nil {
		return
	}
	m.mu.RLock()
	// Snapshot the apps map; we'll release mu before doing the (potentially
	// slow) target-key lookup and index publish.
	appsCopy := make([]App, 0, len(m.apps))
	for _, a := range m.apps {
		appsCopy = append(appsCopy, a)
	}
	m.mu.RUnlock()

	// validKeys is used to silently drop dangling references at runtime.
	// nil means "accept any key" — used by tests that don't wire targets.
	var validKeys map[string]bool
	if keys := m.targetKeysFn(); keys != nil {
		validKeys = make(map[string]bool, len(keys))
		for _, k := range keys {
			validKeys[k] = true
		}
	}

	idx := make(AppTargetIndex, len(appsCopy))
	for i := range appsCopy {
		a := &appsCopy[i]
		for _, ref := range a.Uses {
			if validKeys != nil && !validKeys[ref] {
				continue
			}
			idx[ref] = append(idx[ref], a)
		}
	}
	m.indexPublisher(idx)
}
