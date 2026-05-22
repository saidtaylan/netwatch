package engine

// storage.go — Engine wiring for the StorageBackend layer (B24.1).
//
// This file is the engine-side glue between:
//   - cluster.Manager (gossip transport)
//   - internal/storage (interface + version semantics)
//   - internal/storage/sqlite (durable backend)
//   - internal/storage/gossip (cluster-aware wrapper)
//   - internal/storage/migrate (one-shot JSON → DB migration)
//
// Behavior contract for B24.1 (this commit):
//   - initStorage() builds a *gossipstore.Storage and stores it as e.storage
//   - In standalone mode, the wrapper uses NoopBroadcaster + AlwaysHealthy
//     so writes persist but don't replicate
//   - JSON migration is RUN at startup, but the migrated records are NOT
//     yet consumed by the engine — subsequent B24.x commits switch each
//     entity to read from storage. For now state.json's .migrated archive
//     proves migration runs without breaking startup.
//   - Shutdown closes the SQLite handle cleanly.

import (
	"context"
	"fmt"
	"log/slog"
	"path/filepath"
	"strings"

	"github.com/saidtaylan/netwatch/internal/storage/gossip"
	"github.com/saidtaylan/netwatch/internal/storage/migrate"
	"github.com/saidtaylan/netwatch/internal/storage/sqlite"
)

// initStorage opens the per-node SQLite database, runs one-shot JSON
// migration, and wraps the result with a gossip layer (a NoopBroadcaster
// in standalone mode, the cluster.Manager when clustering is enabled).
//
// Called once during Init() after cluster.Manager setup so that — when
// clustering is enabled — the gossip wrapper can register itself as the
// cluster's StorageChangeHandler.
func (e *Engine) initStorage() error {
	e.mu.RLock()
	stateFile := e.cfg.StateFile
	nodeName := e.clusterNodeNameLocked()
	e.mu.RUnlock()

	// Determine data directory. Defaults next to state.json so existing
	// deployments don't need new config. If state_file is unset, fall
	// back to the working directory + "netwatch-data".
	dataDir := deriveDataDir(stateFile)
	if dataDir == "" {
		return fmt.Errorf("could not derive data_dir; set state_file or data_dir explicitly")
	}

	// Open SQLite. Path is "<dataDir>/netwatch.db".
	dbPath := filepath.Join(dataDir, "netwatch.db")
	sqlBackend, err := sqlite.Open(dbPath)
	if err != nil {
		return fmt.Errorf("open sqlite %s: %w", dbPath, err)
	}

	// Run one-shot JSON migration. Idempotent — re-run is a no-op once
	// JSON files have been archived as <name>.migrated.
	if _, err := migrate.RunAll(context.Background(), sqlBackend, dataDir, nodeName); err != nil {
		// Migration errors are logged but not fatal — operator can
		// investigate manually. The engine still has its in-memory
		// state from loadPersistedState() (loaded above in Init).
		slog.Warn("[STORAGE] migration completed with errors", "err", err)
	}

	// Wrap with the gossip layer. In standalone mode the cluster manager
	// is nil and we substitute no-op interfaces so writes still persist
	// (without replication).
	var (
		isolatedChecker gossip.IsolatedModeChecker = gossip.AlwaysHealthy{}
		broadcaster     gossip.ChangeBroadcaster   = gossip.NoopBroadcaster{}
	)
	if e.clusterMgr != nil {
		isolatedChecker = e.clusterMgr
		broadcaster = e.clusterMgr
	}

	wrapped := gossip.NewStorage(sqlBackend, isolatedChecker, broadcaster, nodeName)
	e.storage = wrapped

	// Register receive-side handler so peer broadcasts arrive at our
	// inner backend. Skipped in standalone mode.
	if e.clusterMgr != nil {
		e.clusterMgr.SetStorageChangeHandler(wrapped)
	}

	slog.Info("[STORAGE] initialized",
		"path", dbPath,
		"data_dir", dataDir,
		"replication", e.clusterMgr != nil,
		"node", nodeName,
	)
	return nil
}

// Storage returns the engine's StorageBackend. Returns nil if Init() has
// not yet run or failed at the storage step.
//
// Exposed for cmd/linux/main.go (HTTP handlers) and integration tests.
// Callers should treat the return value as the storage.StorageBackend
// interface unless they need NextVersion() / Stats() / NodeName().
func (e *Engine) Storage() *gossip.Storage {
	return e.storage
}

// closeStorage releases the SQLite connection. Safe to call multiple
// times — idempotent via gossip.Storage.Close which delegates to the
// inner backend's own idempotent Close.
func (e *Engine) closeStorage() {
	if e.storage == nil {
		return
	}
	if err := e.storage.Close(); err != nil {
		slog.Warn("[STORAGE] close error", "err", err)
	}
}

// deriveDataDir picks the directory that will hold netwatch.db and
// receive migrated .migrated archives. Order of preference:
//
//  1. dirname(state_file) — historical layout, zero-config for existing
//     installs.
//  2. Empty when state_file is unset (caller errors).
//
// In a later sprint we may add a dedicated config.data_dir field for
// operators who want to split DB and state.json across volumes.
func deriveDataDir(stateFile string) string {
	stateFile = strings.TrimSpace(stateFile)
	if stateFile == "" {
		return ""
	}
	return filepath.Dir(stateFile)
}

// clusterNodeNameLocked returns the cluster node name when configured,
// otherwise falls back to the engine's hostname. Caller must hold e.mu
// (read or write). Used by storage layer to stamp Version.UpdatedBy.
func (e *Engine) clusterNodeNameLocked() string {
	if e.cfg.Cluster.Enabled && e.cfg.Cluster.NodeName != "" {
		return e.cfg.Cluster.NodeName
	}
	return e.hostname
}
