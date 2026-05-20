package engine

import (
	"testing"
)

// makeTarget is a test helper that builds a minimal Target with DependsOn set.
func makeTarget(id string, deps ...string) Target {
	return Target{ID: id, Name: id, Type: "tcp", Target: "127.0.0.1:1", DependsOn: deps}
}

// ── buildDependencyGraph ──────────────────────────────────────────────────────

func TestBuildDependencyGraph_NoDeps(t *testing.T) {
	targets := []Target{makeTarget("a"), makeTarget("b")}
	g, err := buildDependencyGraph(targets)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(g.edges) != 0 {
		t.Errorf("expected no edges, got %v", g.edges)
	}
}

func TestBuildDependencyGraph_SimpleDep(t *testing.T) {
	// payment-service depends on db-primary
	targets := []Target{
		makeTarget("payment-service", "db-primary"),
		makeTarget("db-primary"),
	}
	g, err := buildDependencyGraph(targets)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(g.edges["payment-service"]) != 1 || g.edges["payment-service"][0] != "db-primary" {
		t.Errorf("edges: got %v", g.edges)
	}
	if len(g.reverse["db-primary"]) != 1 || g.reverse["db-primary"][0] != "payment-service" {
		t.Errorf("reverse: got %v", g.reverse)
	}
}

func TestBuildDependencyGraph_UnknownRef(t *testing.T) {
	targets := []Target{makeTarget("svc", "nonexistent")}
	_, err := buildDependencyGraph(targets)
	if err == nil {
		t.Fatal("expected error for unknown depends_on reference")
	}
}

func TestBuildDependencyGraph_CycleDetected(t *testing.T) {
	// a → b → a
	targets := []Target{
		makeTarget("a", "b"),
		makeTarget("b", "a"),
	}
	_, err := buildDependencyGraph(targets)
	if err == nil {
		t.Fatal("expected cycle error")
	}
}

func TestBuildDependencyGraph_SelfCycle(t *testing.T) {
	targets := []Target{makeTarget("a", "a")}
	_, err := buildDependencyGraph(targets)
	if err == nil {
		t.Fatal("expected self-cycle error")
	}
}

// ── FindRootCause ─────────────────────────────────────────────────────────────

func TestFindRootCause_NoDeps(t *testing.T) {
	g, _ := buildDependencyGraph([]Target{makeTarget("a")})
	states := map[string]PersistedState{"a": {State: "hard_down"}}
	if rc := g.FindRootCause("a", states); rc != "a" {
		t.Errorf("want a, got %q", rc)
	}
}

func TestFindRootCause_DirectDep(t *testing.T) {
	// payment-service → db-primary; db-primary is also down → root cause
	targets := []Target{
		makeTarget("payment-service", "db-primary"),
		makeTarget("db-primary"),
	}
	g, _ := buildDependencyGraph(targets)
	states := map[string]PersistedState{
		"payment-service": {State: "hard_down"},
		"db-primary":      {State: "hard_down"},
	}
	rc := g.FindRootCause("payment-service", states)
	if rc != "db-primary" {
		t.Errorf("want db-primary, got %q", rc)
	}
}

func TestFindRootCause_DepIsUp(t *testing.T) {
	// payment-service → db-primary; db-primary UP → payment-service is own root
	targets := []Target{
		makeTarget("payment-service", "db-primary"),
		makeTarget("db-primary"),
	}
	g, _ := buildDependencyGraph(targets)
	states := map[string]PersistedState{
		"payment-service": {State: "hard_down"},
		"db-primary":      {State: "up"},
	}
	rc := g.FindRootCause("payment-service", states)
	if rc != "payment-service" {
		t.Errorf("want payment-service, got %q", rc)
	}
}

func TestFindRootCause_Chain(t *testing.T) {
	// checkout → payment-service → db-primary; all down → db-primary is root
	targets := []Target{
		makeTarget("checkout", "payment-service"),
		makeTarget("payment-service", "db-primary"),
		makeTarget("db-primary"),
	}
	g, _ := buildDependencyGraph(targets)
	states := map[string]PersistedState{
		"checkout":        {State: "hard_down"},
		"payment-service": {State: "hard_down"},
		"db-primary":      {State: "hard_down"},
	}
	rc := g.FindRootCause("checkout", states)
	if rc != "db-primary" {
		t.Errorf("want db-primary, got %q", rc)
	}
}

func TestFindRootCause_NilGraph(t *testing.T) {
	var g *DependencyGraph
	states := map[string]PersistedState{"a": {State: "hard_down"}}
	if rc := g.FindRootCause("a", states); rc != "a" {
		t.Errorf("nil graph should return self, got %q", rc)
	}
}

// ── CascadingImpact ───────────────────────────────────────────────────────────

func TestCascadingImpact_NoDeps(t *testing.T) {
	g, _ := buildDependencyGraph([]Target{makeTarget("db")})
	if impact := g.CascadingImpact("db"); len(impact) != 0 {
		t.Errorf("expected no impact, got %v", impact)
	}
}

func TestCascadingImpact_Single(t *testing.T) {
	targets := []Target{
		makeTarget("payment-service", "db-primary"),
		makeTarget("db-primary"),
	}
	g, _ := buildDependencyGraph(targets)
	impact := g.CascadingImpact("db-primary")
	if len(impact) != 1 || impact[0] != "payment-service" {
		t.Errorf("want [payment-service], got %v", impact)
	}
}

func TestCascadingImpact_Tree(t *testing.T) {
	// db-primary ← payment-service ← checkout
	//            ← inventory-api
	targets := []Target{
		makeTarget("checkout", "payment-service"),
		makeTarget("payment-service", "db-primary"),
		makeTarget("inventory-api", "db-primary"),
		makeTarget("db-primary"),
	}
	g, _ := buildDependencyGraph(targets)
	impact := g.CascadingImpact("db-primary")
	want := map[string]bool{
		"payment-service": true,
		"inventory-api":   true,
		"checkout":        true,
	}
	if len(impact) != len(want) {
		t.Fatalf("want 3 items, got %v", impact)
	}
	for _, id := range impact {
		if !want[id] {
			t.Errorf("unexpected impact: %q", id)
		}
	}
}

// ── DependencyDepth ───────────────────────────────────────────────────────────

func TestDependencyDepth_Same(t *testing.T) {
	g, _ := buildDependencyGraph([]Target{makeTarget("a")})
	if d := g.DependencyDepth("a", "a"); d != 0 {
		t.Errorf("want 0, got %d", d)
	}
}

func TestDependencyDepth_Direct(t *testing.T) {
	targets := []Target{
		makeTarget("checkout", "payment-service"),
		makeTarget("payment-service", "db-primary"),
		makeTarget("db-primary"),
	}
	g, _ := buildDependencyGraph(targets)
	// db-primary → payment-service depth = 1
	if d := g.DependencyDepth("payment-service", "db-primary"); d != 1 {
		t.Errorf("want 1, got %d", d)
	}
	// db-primary → checkout depth = 2
	if d := g.DependencyDepth("checkout", "db-primary"); d != 2 {
		t.Errorf("want 2, got %d", d)
	}
}
