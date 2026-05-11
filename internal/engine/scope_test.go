package engine

import (
	"testing"
)

// ── classifyScope — standalone mode ──────────────────────────────────────────

func TestClassifyScope_Standalone_LocalDown(t *testing.T) {
	e := newStandaloneEngine([]Target{
		{ID: "db", Name: "DB", Type: "tcp", Target: "127.0.0.1:5432"},
	}, nil, map[string]PersistedState{
		"db": {State: "hard_down", Seq: 1},
	})

	ds := e.classifyScope("db")
	if ds.Scope != "NODE_LOCAL" {
		t.Errorf("scope: want NODE_LOCAL, got %q", ds.Scope)
	}
	if ds.Classification != "LOCAL_FAILURE" {
		t.Errorf("classification: want LOCAL_FAILURE, got %q", ds.Classification)
	}
	if ds.Confidence != 1.0 {
		t.Errorf("confidence: want 1.0, got %.2f", ds.Confidence)
	}
	if len(ds.DownNodes) != 1 {
		t.Errorf("down_nodes: want 1, got %d", len(ds.DownNodes))
	}
}

func TestClassifyScope_Standalone_Up(t *testing.T) {
	e := newStandaloneEngine([]Target{
		{ID: "db", Name: "DB", Type: "tcp", Target: "127.0.0.1:5432"},
	}, nil, map[string]PersistedState{
		"db": {State: "up", Seq: 1},
	})

	ds := e.classifyScope("db")
	if ds.Scope != "STANDALONE" {
		t.Errorf("scope: want STANDALONE, got %q", ds.Scope)
	}
	if ds.Classification != "LOCAL_FAILURE" {
		t.Errorf("classification: want LOCAL_FAILURE, got %q", ds.Classification)
	}
}

func TestClassifyScope_Standalone_Unknown(t *testing.T) {
	// Target exists in config but has no state yet.
	e := newStandaloneEngine([]Target{
		{ID: "db", Name: "DB", Type: "tcp", Target: "127.0.0.1:5432"},
	}, nil, nil)

	ds := e.classifyScope("db")
	// No local state → STANDALONE scope, AMBIGUOUS.
	if ds.Classification != "AMBIGUOUS" {
		t.Errorf("classification: want AMBIGUOUS, got %q", ds.Classification)
	}
}

// ── ScopeEnv ──────────────────────────────────────────────────────────────────

func TestDetailedScope_ScopeEnv_AllFields(t *testing.T) {
	ds := DetailedScope{
		Scope:          "GLOBAL",
		Classification: "REAL_OUTAGE",
		DownNodes:      []string{"node-1", "node-2"},
		UpNodes:        nil,
		OfflineNodes:   []string{"node-3"},
		Confidence:     0.90,
	}
	env := ds.ScopeEnv()

	if env["SCOPE"] != "GLOBAL" {
		t.Errorf("SCOPE: want GLOBAL, got %q", env["SCOPE"])
	}
	if env["CLASSIFICATION"] != "REAL_OUTAGE" {
		t.Errorf("CLASSIFICATION: want REAL_OUTAGE, got %q", env["CLASSIFICATION"])
	}
	if env["CONFIDENCE"] != "0.90" {
		t.Errorf("CONFIDENCE: want 0.90, got %q", env["CONFIDENCE"])
	}
	if env["DOWN_NODES"] != "node-1,node-2" {
		t.Errorf("DOWN_NODES: want node-1,node-2, got %q", env["DOWN_NODES"])
	}
	if _, ok := env["UP_NODES"]; ok {
		t.Error("UP_NODES should not be present when empty")
	}
	if env["OFFLINE_NODES"] != "node-3" {
		t.Errorf("OFFLINE_NODES: want node-3, got %q", env["OFFLINE_NODES"])
	}
}

func TestDetailedScope_ScopeEnv_NetworkPartition(t *testing.T) {
	ds := DetailedScope{
		Scope:           "PARTIAL",
		Classification:  "NETWORK_PARTITION",
		DownNodes:       []string{"node-1"},
		UpNodes:         []string{"node-2", "node-3"},
		PartitionGroups: [][]string{{"node-1"}, {"node-2", "node-3"}},
		Confidence:      0.75,
	}
	env := ds.ScopeEnv()
	if env["CLASSIFICATION"] != "NETWORK_PARTITION" {
		t.Errorf("CLASSIFICATION: want NETWORK_PARTITION, got %q", env["CLASSIFICATION"])
	}
	if env["UP_NODES"] != "node-2,node-3" {
		t.Errorf("UP_NODES: want node-2,node-3, got %q", env["UP_NODES"])
	}
}

// ── absFloat helper ───────────────────────────────────────────────────────────

func TestAbsFloat(t *testing.T) {
	cases := []struct{ in, want float64 }{
		{-1.5, 1.5},
		{0.0, 0.0},
		{2.3, 2.3},
	}
	for _, c := range cases {
		if got := absFloat(c.in); got != c.want {
			t.Errorf("absFloat(%v) = %v, want %v", c.in, got, c.want)
		}
	}
}

// ── condSlice helper ──────────────────────────────────────────────────────────

func TestCondSlice(t *testing.T) {
	if s := condSlice(true, "node-1"); len(s) != 1 || s[0] != "node-1" {
		t.Errorf("condSlice(true, node-1) = %v, want [node-1]", s)
	}
	if s := condSlice(false, "node-1"); s != nil {
		t.Errorf("condSlice(false, node-1) = %v, want nil", s)
	}
}
