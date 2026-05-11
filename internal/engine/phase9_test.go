package engine

import (
	"context"
	"encoding/json"
	"sync/atomic"
	"testing"
)

// ── FullState ─────────────────────────────────────────────────────────────────

func TestFullState_Empty(t *testing.T) {
	e := &Engine{lastKnown: make(map[string]PersistedState)}
	data := e.FullState()
	var got map[string]PersistedState
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("want empty map, got %v", got)
	}
}

func TestFullState_Serializes(t *testing.T) {
	e := &Engine{
		lastKnown: map[string]PersistedState{
			"db":    {State: "hard_down", Seq: 3, ErrorCode: "refused"},
			"cache": {State: "up", Seq: 1},
		},
	}
	data := e.FullState()
	var got map[string]PersistedState
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("want 2 entries, got %d", len(got))
	}
	if got["db"].Seq != 3 || got["db"].State != "hard_down" {
		t.Errorf("db: got %+v", got["db"])
	}
	if got["cache"].State != "up" {
		t.Errorf("cache: got %+v", got["cache"])
	}
}

// FullState must not expose the internal map — mutating the result must not
// affect lastKnown.
func TestFullState_IsolatedCopy(t *testing.T) {
	e := &Engine{
		lastKnown: map[string]PersistedState{
			"svc": {State: "up", Seq: 1},
		},
	}
	data := e.FullState()
	var got map[string]PersistedState
	_ = json.Unmarshal(data, &got)
	got["svc"] = PersistedState{State: "hard_down", Seq: 99}

	e.stateMu.RLock()
	actual := e.lastKnown["svc"]
	e.stateMu.RUnlock()
	if actual.State != "up" {
		t.Error("FullState result mutation leaked into internal map")
	}
}

// ── ApplyRemoteState ──────────────────────────────────────────────────────────

func mustMarshal(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return b
}

func TestApplyRemoteState_AcceptsNewer(t *testing.T) {
	// Remote has higher Seq → local state must be overwritten.
	e := &Engine{
		lastKnown: map[string]PersistedState{
			"db": {State: "up", Seq: 1},
		},
	}
	remote := map[string]PersistedState{
		"db": {State: "hard_down", Seq: 5, ErrorCode: "refused"},
	}
	e.ApplyRemoteState(mustMarshal(t, remote))

	e.stateMu.RLock()
	ps := e.lastKnown["db"]
	e.stateMu.RUnlock()

	if ps.State != "hard_down" || ps.Seq != 5 {
		t.Errorf("want hard_down/seq=5, got %+v", ps)
	}
}

func TestApplyRemoteState_KeepsLocal(t *testing.T) {
	// Local has higher Seq → local state must be preserved (no overwrite).
	e := &Engine{
		lastKnown: map[string]PersistedState{
			"db": {State: "up", Seq: 10},
		},
	}
	remote := map[string]PersistedState{
		"db": {State: "hard_down", Seq: 3},
	}
	e.ApplyRemoteState(mustMarshal(t, remote))

	e.stateMu.RLock()
	ps := e.lastKnown["db"]
	e.stateMu.RUnlock()

	if ps.State != "up" || ps.Seq != 10 {
		t.Errorf("want up/seq=10, got %+v", ps)
	}
}

func TestApplyRemoteState_NewTarget(t *testing.T) {
	// Target unknown locally → accept remote state.
	e := &Engine{
		lastKnown: make(map[string]PersistedState),
	}
	remote := map[string]PersistedState{
		"new-svc": {State: "hard_down", Seq: 2, ErrorCode: "timeout"},
	}
	e.ApplyRemoteState(mustMarshal(t, remote))

	e.stateMu.RLock()
	ps, ok := e.lastKnown["new-svc"]
	e.stateMu.RUnlock()

	if !ok {
		t.Fatal("new-svc not inserted")
	}
	if ps.State != "hard_down" || ps.Seq != 2 {
		t.Errorf("want hard_down/seq=2, got %+v", ps)
	}
}

func TestApplyRemoteState_LamportTieBreak(t *testing.T) {
	// Equal Seq — higher OwnerNode wins.
	e := &Engine{
		lastKnown: map[string]PersistedState{
			"db": {State: "up", Seq: 5, OwnerNode: "node-1"},
		},
	}
	remote := map[string]PersistedState{
		"db": {State: "hard_down", Seq: 5, OwnerNode: "node-3"}, // node-3 > node-1
	}
	e.ApplyRemoteState(mustMarshal(t, remote))

	e.stateMu.RLock()
	ps := e.lastKnown["db"]
	e.stateMu.RUnlock()

	if ps.State != "hard_down" || ps.OwnerNode != "node-3" {
		t.Errorf("want hard_down/node-3, got %+v", ps)
	}
}

func TestApplyRemoteState_LamportTieBreak_LocalWins(t *testing.T) {
	// Equal Seq — local OwnerNode is higher, so local wins.
	e := &Engine{
		lastKnown: map[string]PersistedState{
			"db": {State: "up", Seq: 5, OwnerNode: "node-3"},
		},
	}
	remote := map[string]PersistedState{
		"db": {State: "hard_down", Seq: 5, OwnerNode: "node-1"}, // node-1 < node-3
	}
	e.ApplyRemoteState(mustMarshal(t, remote))

	e.stateMu.RLock()
	ps := e.lastKnown["db"]
	e.stateMu.RUnlock()

	if ps.State != "up" || ps.OwnerNode != "node-3" {
		t.Errorf("want up/node-3, got %+v", ps)
	}
}

func TestApplyRemoteState_MalformedJSON(t *testing.T) {
	// Bad JSON → no crash, state unchanged.
	e := &Engine{
		lastKnown: map[string]PersistedState{
			"db": {State: "up", Seq: 1},
		},
	}
	e.ApplyRemoteState([]byte("{not-valid-json"))

	e.stateMu.RLock()
	ps := e.lastKnown["db"]
	e.stateMu.RUnlock()

	if ps.State != "up" || ps.Seq != 1 {
		t.Errorf("state was corrupted by bad input: %+v", ps)
	}
}

func TestApplyRemoteState_EmptyRemote(t *testing.T) {
	// Empty remote map → nothing changes.
	e := &Engine{
		lastKnown: map[string]PersistedState{
			"db": {State: "up", Seq: 2},
		},
	}
	e.ApplyRemoteState(mustMarshal(t, map[string]PersistedState{}))

	e.stateMu.RLock()
	ps := e.lastKnown["db"]
	e.stateMu.RUnlock()

	if ps.Seq != 2 {
		t.Errorf("state changed unexpectedly: %+v", ps)
	}
}

// ── SetSyncing / syncing flag ─────────────────────────────────────────────────

func TestSetSyncing_FlipsFlag(t *testing.T) {
	e := &Engine{}
	if e.syncing.Load() {
		t.Error("syncing should be false initially")
	}
	e.SetSyncing(true)
	if !e.syncing.Load() {
		t.Error("syncing should be true after SetSyncing(true)")
	}
	e.SetSyncing(false)
	if e.syncing.Load() {
		t.Error("syncing should be false after SetSyncing(false)")
	}
}

// Verify runCheck exits early when syncing=true by counting probe executions.
// We inject a test target with a no-op checker type to avoid real network calls.
func TestRunCheck_SkipsWhenSyncing(t *testing.T) {
	var probeCount atomic.Int32
	e := &Engine{
		lastKnown: make(map[string]PersistedState),
		pending:   make(map[string]PendingEntry),
		hostname:  "test-host",
		checkers: map[string]Checker{
			"noop": &noopChecker{probe: func() (bool, error) {
				probeCount.Add(1)
				return true, nil
			}},
		},
		cfg: Config{AppName: "test"},
	}
	e.syncing.Store(true)

	t.Context().Done() // just use a background context
	ctx := t.Context()
	e.runCheck(ctx, Target{Type: "noop", Name: "svc", Target: "host:1"})

	if probeCount.Load() != 0 {
		t.Errorf("probe ran %d time(s) during syncing — expected 0", probeCount.Load())
	}
}

// noopChecker satisfies the Checker interface for tests.
type noopChecker struct {
	probe func() (bool, error)
}

func (n *noopChecker) Run(_ context.Context, _ string, _ json.RawMessage) (bool, error) {
	return n.probe()
}
func (n *noopChecker) ValidateOptions(_ json.RawMessage) error { return nil }
func (n *noopChecker) ParseAddr(addr string) (string, string, error) {
	return addr, "", nil
}
