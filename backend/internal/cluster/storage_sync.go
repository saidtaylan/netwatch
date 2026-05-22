package cluster

// storage_sync.go — Gossip propagation for StorageBackend changes (B23).
//
// This file mirrors the maintenance.go pattern:
//   1. A msgType constant ("storage_change") identifies the wire format
//   2. A Payload struct describes the JSON layload
//   3. A Handler interface lets the storage layer apply incoming changes
//   4. Broadcast methods push to memberlist's broadcast queue (UDP, fire-and-forget)
//   5. A handle* function in NotifyMsg dispatches inbound messages
//
// Architecture: cluster → storage/gossip. The storage layer does NOT
// import cluster — it defines the interfaces (gossip.ChangeBroadcaster,
// gossip.IsolatedModeChecker) and cluster.Manager satisfies them.
//
// This file adds:
//   - Manager.BroadcastStorageChange(change) → satisfies gossip.ChangeBroadcaster
//   - StorageChangeHandler interface → satisfied by gossip.Storage.ApplyRemoteChange
//   - Manager.SetStorageChangeHandler(h) / handleStorageChange(data)
//
// IsolatedMode is already satisfied by Manager.IsolatedMode() — no change
// needed there.

import (
	"context"
	"encoding/json"
	"log/slog"
	"time"

	"github.com/saidtaylan/netwatch/internal/storage"
	"github.com/saidtaylan/netwatch/internal/storage/gossip"
)

const msgTypeStorageChange = "storage_change"

// StorageChangeHandler is satisfied by gossip.Storage.ApplyRemoteChange.
// The cluster package does not import storage/gossip at the call site
// (it does import it for the type signature only) — the handler is
// registered via SetStorageChangeHandler from outside.
type StorageChangeHandler interface {
	ApplyRemoteChange(ctx context.Context, change gossip.StorageChange) error
}

// SetStorageChangeHandler registers the callback invoked when a peer
// broadcasts a storage change. Typically called by the engine during
// Init after the GossipLWWStorage is constructed.
//
// Calling with nil disables remote-change handling (useful for graceful
// shutdown sequencing).
func (m *Manager) SetStorageChangeHandler(h StorageChangeHandler) {
	m.mu.Lock()
	m.storageChangeHandler = h
	m.mu.Unlock()
}

// BroadcastStorageChange ships a storage change to all peers via UDP
// best-effort. Failures are logged but do NOT fail the caller — the
// gossip wrapper expects fire-and-forget semantics.
//
// This method satisfies gossip.ChangeBroadcaster:
//
//	type ChangeBroadcaster interface {
//	    BroadcastStorageChange(change StorageChange) error
//	}
//
// We always return nil error to match the contract; failures surface in
// logs and Prometheus metrics (when added in B25).
func (m *Manager) BroadcastStorageChange(change gossip.StorageChange) error {
	// Stamp msg_type so NotifyMsg can route. (gossip.Storage already
	// sets this but defensive overwrite makes the wire format authoritative.)
	change.MsgType = msgTypeStorageChange

	data, err := json.Marshal(change)
	if err != nil {
		slog.Error("[STORAGE-SYNC] marshal failed", "err", err)
		return nil // never fail the caller
	}

	// Skip when cluster is disabled / standalone.
	if m.list == nil {
		return nil
	}

	local := m.list.LocalNode()
	for _, member := range m.list.Members() {
		if member.Name == local.Name {
			continue
		}
		// Use UDP (SendBestEffort) — storage changes are not financially
		// critical and anti-entropy will reconcile drops. TCP would add
		// head-of-line blocking under heavy write bursts.
		if err := m.list.SendBestEffort(member, data); err != nil {
			slog.Warn("[STORAGE-SYNC] delivery failed",
				"to", member.Name, "table", change.Table, "id", change.ID, "err", err)
		}
	}
	return nil
}

// handleStorageChange is called from NotifyMsg when a UDP message with
// msg_type=="storage_change" arrives. Parses, dedupes own broadcasts,
// and forwards to the registered handler.
func (m *Manager) handleStorageChange(data []byte) {
	var change gossip.StorageChange
	if err := json.Unmarshal(data, &change); err != nil {
		slog.Warn("[STORAGE-SYNC] unmarshal failed", "err", err)
		return
	}

	// Ignore broadcasts originating from this node. The gossip.Storage
	// already wrote the change locally before broadcasting; receiving our
	// own UDP echo would be a no-op (stale write) but log noise.
	if change.Version.UpdatedBy == m.cfg.NodeName {
		return
	}

	m.mu.RLock()
	h := m.storageChangeHandler
	m.mu.RUnlock()

	if h == nil {
		// No handler registered → silently drop. This happens during
		// startup window between cluster.New() and engine.Init() wiring.
		return
	}

	// Run on a fresh context with a sane timeout so a stuck handler
	// can't block memberlist's NotifyMsg goroutine.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := h.ApplyRemoteChange(ctx, change); err != nil {
		// ApplyRemoteChange normally swallows ErrStaleWrite. Other
		// errors mean the underlying storage layer had a real problem
		// (e.g. disk full) — log and move on; anti-entropy will retry.
		slog.Warn("[STORAGE-SYNC] apply failed",
			"table", change.Table, "id", change.ID,
			"seq", change.Version.Seq, "from", change.Version.UpdatedBy,
			"err", err)
		return
	}
}

// Compile-time interface compliance — if gossip.ChangeBroadcaster ever
// changes signature, this won't compile.
var _ gossip.ChangeBroadcaster = (*Manager)(nil)

// gossip.IsolatedModeChecker is already satisfied by Manager.IsolatedMode()
// declared in cluster.go — no extra declaration needed here.
var _ gossip.IsolatedModeChecker = (*Manager)(nil)

// Ensure storage package is referenced (used by handleStorageChange's
// indirect path through gossip.StorageChange.Version.UpdatedBy logic).
// This blank import is kept in case future logic needs storage types
// directly; remove if unused.
var _ = storage.ErrStaleWrite
