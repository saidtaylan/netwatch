package engine

import (
	"reflect"
	"sort"
	"sync"
	"testing"
)

// ── LocalTargets ─────────────────────────────────────────────────────────────

func TestLocalTargets_ReturnsAllConfiguredKeys(t *testing.T) {
	enabled, disabled := true, false
	e := &Engine{mu: sync.RWMutex{}}
	e.cfg.Targets = []Target{
		{Name: "alpha", Type: "tcp", Target: "a:1", Enabled: &enabled},
		{Name: "beta", Type: "tcp", Target: "b:2", Enabled: &disabled},
		{ID: "gamma-id", Name: "gamma", Type: "tcp", Target: "c:3"},
	}

	got := e.LocalTargets()
	sort.Strings(got)
	// LocalTargets intentionally includes disabled targets so prober
	// assignment stays stable across enable/disable toggles.
	want := []string{"alpha", "beta", "gamma-id"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("want %v, got %v", want, got)
	}
}

func TestLocalTargets_EmptyConfig(t *testing.T) {
	e := &Engine{mu: sync.RWMutex{}}
	if got := e.LocalTargets(); len(got) != 0 {
		t.Errorf("empty config should yield empty slice, got %v", got)
	}
}

// ── ProbeFromConstraint ──────────────────────────────────────────────────────

func TestProbeFromConstraint_ReturnsNilForUnknownTarget(t *testing.T) {
	e := &Engine{mu: sync.RWMutex{}}
	e.cfg.Targets = []Target{{Name: "alpha", Type: "tcp", Target: "a:1"}}
	if got := e.ProbeFromConstraint("missing"); got != nil {
		t.Errorf("unknown target must return nil, got %v", got)
	}
}

func TestProbeFromConstraint_ReturnsNilWhenUnset(t *testing.T) {
	e := &Engine{mu: sync.RWMutex{}}
	e.cfg.Targets = []Target{{Name: "alpha", Type: "tcp", Target: "a:1"}}
	if got := e.ProbeFromConstraint("alpha"); got != nil {
		t.Errorf("absent probe_from must return nil, got %v", got)
	}
}

func TestProbeFromConstraint_ReturnsCopiedList(t *testing.T) {
	pin := []string{"n2", "n3"}
	e := &Engine{mu: sync.RWMutex{}}
	e.cfg.Targets = []Target{{
		Name:      "alpha",
		Type:      "tcp",
		Target:    "a:1",
		ProbeFrom: pin,
	}}

	got := e.ProbeFromConstraint("alpha")
	if !reflect.DeepEqual(got, pin) {
		t.Errorf("want %v, got %v", pin, got)
	}
	// Defensive copy: mutating the returned slice must not affect the
	// stored target config (otherwise concurrent callers could corrupt state).
	got[0] = "MUTATED"
	again := e.ProbeFromConstraint("alpha")
	if again[0] != "n2" {
		t.Errorf("internal state mutated through returned slice: %v", again)
	}
}

func TestProbeFromConstraint_TargetIDOverridesName(t *testing.T) {
	// Target.key() prefers ID. Constraint lookup must use the same key.
	e := &Engine{mu: sync.RWMutex{}}
	e.cfg.Targets = []Target{{
		ID:        "stable-id",
		Name:      "display-name",
		Type:      "tcp",
		Target:    "a:1",
		ProbeFrom: []string{"n1"},
	}}
	if got := e.ProbeFromConstraint("stable-id"); !reflect.DeepEqual(got, []string{"n1"}) {
		t.Errorf("ID-keyed lookup failed: got %v", got)
	}
	if got := e.ProbeFromConstraint("display-name"); got != nil {
		t.Errorf("Name lookup should miss when ID is set, got %v", got)
	}
}

// ── bootstrapInventoryBroadcast ──────────────────────────────────────────────

func TestBootstrapInventoryBroadcast_NoopWhenClusterDisabled(t *testing.T) {
	// clusterMgr is nil — must return without panic and without doing work.
	e := &Engine{mu: sync.RWMutex{}, stateMu: sync.RWMutex{}}
	e.cfg.Targets = []Target{{Name: "alpha", Type: "tcp", Target: "a:1"}}
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("panic on standalone bootstrap: %v", r)
		}
	}()
	e.bootstrapInventoryBroadcast()
}

func TestBootstrapInventoryBroadcast_SkipsDisabledTargets(t *testing.T) {
	// This test verifies the iteration logic — clusterMgr nil so no real
	// broadcasts are emitted, but the function must walk to completion
	// without indexing into a non-existent target.
	disabled := false
	e := &Engine{mu: sync.RWMutex{}, stateMu: sync.RWMutex{}}
	e.cfg.Targets = []Target{
		{Name: "alpha", Type: "tcp", Target: "a:1"},
		{Name: "beta", Type: "tcp", Target: "b:2", Enabled: &disabled},
	}
	e.lastKnown = map[string]PersistedState{
		"alpha": {State: "up", Seq: 5},
	}
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("panic walking targets: %v", r)
		}
	}()
	e.bootstrapInventoryBroadcast()
}
