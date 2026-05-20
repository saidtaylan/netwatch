package cluster

import (
	"encoding/json"
	"sync"
	"testing"
)

// ── mockProvider ──────────────────────────────────────────────────────────────

// mockProvider implements AntiEntropyProvider for unit testing.
type mockProvider struct {
	mu sync.Mutex

	fullStateReturn  []byte
	fullStateCalls   int
	applyBufs        [][]byte
	syncingValues    []bool
}

func (m *mockProvider) FullState() []byte {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.fullStateCalls++
	return m.fullStateReturn
}

func (m *mockProvider) ApplyRemoteState(buf []byte) {
	m.mu.Lock()
	defer m.mu.Unlock()
	cp := make([]byte, len(buf))
	copy(cp, buf)
	m.applyBufs = append(m.applyBufs, cp)
}

func (m *mockProvider) SetSyncing(v bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.syncingValues = append(m.syncingValues, v)
}

// ── LocalState dispatch ───────────────────────────────────────────────────────

func TestLocalState_JoinTrue_UsesProvider(t *testing.T) {
	payload := []byte(`{"target":"up"}`)
	prov := &mockProvider{fullStateReturn: payload}

	mgr := NewTestManager("node-1", nil)
	mgr.SetStateProvider(prov)

	del := &gossipDelegate{mgr: mgr}
	got := del.LocalState(true)

	if string(got) != string(payload) {
		t.Errorf("LocalState(join=true): want %q got %q", payload, got)
	}
	if prov.fullStateCalls != 1 {
		t.Errorf("FullState() called %d times, want 1", prov.fullStateCalls)
	}
}

func TestLocalState_JoinFalse_UsesPeerStates(t *testing.T) {
	prov := &mockProvider{}
	mgr := NewTestManager("node-1", nil)
	mgr.SetStateProvider(prov)
	mgr.SetPeerState("node-2", "db", GossipPayload{TargetID: "db", State: "up", NodeName: "node-2"})

	del := &gossipDelegate{mgr: mgr}
	got := del.LocalState(false)

	// FullState must NOT be called.
	if prov.fullStateCalls != 0 {
		t.Errorf("FullState() must not be called for join=false, called %d times", prov.fullStateCalls)
	}
	// Result must be valid JSON containing peer state data.
	var parsed map[string]map[string]GossipPayload
	if err := json.Unmarshal(got, &parsed); err != nil {
		t.Fatalf("LocalState(join=false) returned invalid JSON: %v", err)
	}
	if _, ok := parsed["node-2"]["db"]; !ok {
		t.Error("expected node-2 db state in peer-states payload")
	}
}

func TestLocalState_JoinTrue_NoProvider_FallsBackToPeerStates(t *testing.T) {
	mgr := NewTestManager("node-1", nil)
	// No provider registered — should fall back to peerStates (not panic).
	mgr.SetPeerState("node-2", "db", GossipPayload{TargetID: "db", State: "up"})

	del := &gossipDelegate{mgr: mgr}
	got := del.LocalState(true)

	// Should return peerStates JSON, not panic.
	var parsed map[string]map[string]GossipPayload
	if err := json.Unmarshal(got, &parsed); err != nil {
		t.Fatalf("fallback localState: invalid JSON: %v", err)
	}
}

// ── MergeRemoteState dispatch ─────────────────────────────────────────────────

func TestMergeRemoteState_JoinTrue_CallsProvider(t *testing.T) {
	prov := &mockProvider{}
	mgr := NewTestManager("node-1", nil)
	mgr.SetStateProvider(prov)

	del := &gossipDelegate{mgr: mgr}
	buf := []byte(`{"db":{"state":"hard_down","seq":3}}`)
	del.MergeRemoteState(buf, true)

	// ApplyRemoteState must be called with the correct buf.
	if len(prov.applyBufs) != 1 {
		t.Fatalf("ApplyRemoteState called %d times, want 1", len(prov.applyBufs))
	}
	if string(prov.applyBufs[0]) != string(buf) {
		t.Errorf("ApplyRemoteState buf mismatch: got %q", prov.applyBufs[0])
	}
	// SetSyncing must be called: true then false.
	if len(prov.syncingValues) != 2 {
		t.Fatalf("SetSyncing called %d times, want 2", len(prov.syncingValues))
	}
	if prov.syncingValues[0] != true || prov.syncingValues[1] != false {
		t.Errorf("SetSyncing sequence: want [true false], got %v", prov.syncingValues)
	}
}

func TestMergeRemoteState_JoinFalse_UsesOnStateReceived(t *testing.T) {
	prov := &mockProvider{}
	mgr := NewTestManager("node-1", nil)
	mgr.SetStateProvider(prov)

	del := &gossipDelegate{mgr: mgr}
	payload := GossipPayload{TargetID: "cache", State: "up", NodeName: "node-2", Seq: 1}
	data, _ := json.Marshal(map[string]map[string]GossipPayload{
		"node-2": {"cache": payload},
	})
	del.MergeRemoteState(data, false)

	// Provider must NOT be involved.
	if len(prov.applyBufs) != 0 {
		t.Error("ApplyRemoteState must not be called for join=false")
	}
	if len(prov.syncingValues) != 0 {
		t.Error("SetSyncing must not be called for join=false")
	}
	// State must be merged into peerStates.
	states := mgr.PeerStatesForTarget("cache")
	if len(states) != 1 || states[0].State != "up" {
		t.Errorf("cache state not merged: %v", states)
	}
}

func TestMergeRemoteState_EmptyBuf_NoOp(t *testing.T) {
	prov := &mockProvider{}
	mgr := NewTestManager("node-1", nil)
	mgr.SetStateProvider(prov)

	del := &gossipDelegate{mgr: mgr}
	del.MergeRemoteState([]byte{}, true)  // empty — must be a no-op
	del.MergeRemoteState(nil, true)        // nil — must be a no-op

	if len(prov.applyBufs) != 0 {
		t.Error("ApplyRemoteState should not be called for empty buf")
	}
}

func TestMergeRemoteState_JoinTrue_NoProvider_FallsBackToOnStateReceived(t *testing.T) {
	mgr := NewTestManager("node-1", nil)
	// No provider registered — join=true should fall back to peer-states merge.

	del := &gossipDelegate{mgr: mgr}
	payload := GossipPayload{TargetID: "svc", State: "hard_down", NodeName: "node-2", Seq: 2}
	data, _ := json.Marshal(map[string]map[string]GossipPayload{
		"node-2": {"svc": payload},
	})
	del.MergeRemoteState(data, true) // no provider → treated as periodic

	states := mgr.PeerStatesForTarget("svc")
	if len(states) != 1 {
		t.Errorf("fallback merge: want 1 state, got %d", len(states))
	}
}
