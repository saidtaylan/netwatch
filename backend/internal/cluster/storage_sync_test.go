package cluster

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/saidtaylan/netwatch/internal/storage"
	"github.com/saidtaylan/netwatch/internal/storage/gossip"
)

// fakeStorageHandler records calls to ApplyRemoteChange.
type fakeStorageHandler struct {
	mu      sync.Mutex
	calls   []gossip.StorageChange
	failErr error
}

func (f *fakeStorageHandler) ApplyRemoteChange(_ context.Context, c gossip.StorageChange) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, c)
	return f.failErr
}

func (f *fakeStorageHandler) list() []gossip.StorageChange {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]gossip.StorageChange, len(f.calls))
	copy(out, f.calls)
	return out
}

func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return b
}

// ── Interface compliance ────────────────────────────────────────────────────

func TestManager_SatisfiesGossipInterfaces(t *testing.T) {
	var _ gossip.ChangeBroadcaster = (*Manager)(nil)
	var _ gossip.IsolatedModeChecker = (*Manager)(nil)
}

// ── SetStorageChangeHandler ─────────────────────────────────────────────────

func TestSetStorageChangeHandler(t *testing.T) {
	m := NewTestManager("node-local", nil)
	h := &fakeStorageHandler{}
	m.SetStorageChangeHandler(h)

	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.storageChangeHandler == nil {
		t.Error("handler not stored")
	}
}

func TestSetStorageChangeHandler_NilDisables(t *testing.T) {
	m := NewTestManager("node-local", nil)
	m.SetStorageChangeHandler(&fakeStorageHandler{})
	m.SetStorageChangeHandler(nil)

	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.storageChangeHandler != nil {
		t.Error("nil should disable handler")
	}
}

// ── handleStorageChange dispatch ────────────────────────────────────────────

func TestHandleStorageChange_AppliesValidMessage(t *testing.T) {
	m := NewTestManager("node-local", nil)
	h := &fakeStorageHandler{}
	m.SetStorageChangeHandler(h)

	change := gossip.StorageChange{
		MsgType: msgTypeStorageChange,
		Table:   storage.TableSLOTargets,
		ID:      "db-primary",
		Payload: []byte(`{"target_uptime":0.999}`),
		Version: storage.Version{Seq: 5, UpdatedAt: time.Now().UTC(), UpdatedBy: "node-peer"},
	}
	m.handleStorageChange(mustJSON(t, change))

	calls := h.list()
	if len(calls) != 1 {
		t.Fatalf("expected 1 apply call, got %d", len(calls))
	}
	if calls[0].ID != "db-primary" {
		t.Errorf("wrong record ID: %q", calls[0].ID)
	}
	if calls[0].Table != storage.TableSLOTargets {
		t.Errorf("wrong table: %q", calls[0].Table)
	}
}

func TestHandleStorageChange_IgnoresOwnBroadcasts(t *testing.T) {
	m := NewTestManager("node-local", nil)
	h := &fakeStorageHandler{}
	m.SetStorageChangeHandler(h)

	// Origin matches our node name — self-echo
	change := gossip.StorageChange{
		MsgType: msgTypeStorageChange,
		Table:   storage.TableSLOTargets,
		ID:      "x",
		Version: storage.Version{Seq: 1, UpdatedBy: "node-local"},
	}
	m.handleStorageChange(mustJSON(t, change))

	if len(h.list()) > 0 {
		t.Error("self-originating broadcasts should be dropped")
	}
}

func TestHandleStorageChange_NoHandlerSilentDrop(t *testing.T) {
	m := NewTestManager("node-local", nil)
	// No handler set — should not panic
	change := gossip.StorageChange{
		MsgType: msgTypeStorageChange,
		Table:   storage.TableSLOTargets,
		ID:      "x",
		Version: storage.Version{Seq: 1, UpdatedBy: "node-peer"},
	}
	m.handleStorageChange(mustJSON(t, change))
}

func TestHandleStorageChange_MalformedJSONIgnored(t *testing.T) {
	m := NewTestManager("node-local", nil)
	h := &fakeStorageHandler{}
	m.SetStorageChangeHandler(h)

	m.handleStorageChange([]byte(`not valid json {{{`))
	if len(h.list()) > 0 {
		t.Error("malformed JSON should not reach handler")
	}
}

func TestHandleStorageChange_HandlerErrorLoggedNotPropagated(t *testing.T) {
	m := NewTestManager("node-local", nil)
	h := &fakeStorageHandler{failErr: storage.ErrNotFound}
	m.SetStorageChangeHandler(h)

	change := gossip.StorageChange{
		MsgType: msgTypeStorageChange,
		Table:   storage.TableSLOTargets,
		ID:      "x",
		Version: storage.Version{Seq: 1, UpdatedBy: "node-peer"},
	}
	// Should not panic; handler error is swallowed (logged)
	m.handleStorageChange(mustJSON(t, change))

	if len(h.list()) != 1 {
		t.Error("handler should have been called once")
	}
}

func TestHandleStorageChange_DeleteVariant(t *testing.T) {
	m := NewTestManager("node-local", nil)
	h := &fakeStorageHandler{}
	m.SetStorageChangeHandler(h)

	change := gossip.StorageChange{
		MsgType:   msgTypeStorageChange,
		Table:     storage.TableSLOTargets,
		ID:        "x",
		Tombstone: true,
		Version:   storage.Version{Seq: 10, UpdatedBy: "node-peer"},
	}
	m.handleStorageChange(mustJSON(t, change))

	calls := h.list()
	if len(calls) != 1 {
		t.Fatalf("expected 1 call, got %d", len(calls))
	}
	if !calls[0].Tombstone {
		t.Error("tombstone flag lost in dispatch")
	}
}

// ── BroadcastStorageChange (standalone path) ────────────────────────────────

func TestBroadcastStorageChange_StandaloneNoOp(t *testing.T) {
	// NewTestManager produces a Manager with list == nil (standalone).
	// Broadcast should be a no-op that never errors.
	m := NewTestManager("node-1", nil)
	err := m.BroadcastStorageChange(gossip.StorageChange{
		Table:   storage.TableSLOTargets,
		ID:      "x",
		Version: storage.Version{Seq: 1, UpdatedBy: "node-1"},
	})
	if err != nil {
		t.Errorf("standalone broadcast should not error, got %v", err)
	}
}

func TestBroadcastStorageChange_ContractAlwaysNil(t *testing.T) {
	// gossip.ChangeBroadcaster contract: never return error to caller.
	// Local write should never fail because of a broadcast issue.
	m := NewTestManager("n", nil)
	for i := 0; i < 10; i++ {
		err := m.BroadcastStorageChange(gossip.StorageChange{
			Table:   storage.TableSLOTargets,
			ID:      "x",
			Version: storage.Version{Seq: uint64(i + 1), UpdatedBy: "n"},
		})
		if err != nil {
			t.Errorf("iteration %d: %v", i, err)
		}
	}
}
