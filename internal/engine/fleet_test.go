package engine

import (
	"testing"
)

// ── helpers ───────────────────────────────────────────────────────────────────

// newStandaloneEngine builds a bare-minimum Engine suitable for fleet tests
// without going through the full Init() path (no network, no cluster).
func newStandaloneEngine(targets []Target, apps []App, states map[string]PersistedState) *Engine {
	e := &Engine{
		cfg: Config{
			AppName: "fleet-test",
			Targets: targets,
			Apps:    apps,
		},
		lastKnown: make(map[string]PersistedState),
		pending:   make(map[string]PendingEntry),
		checkers:  map[string]Checker{"tcp": &tcpChecker{}},
		appIndex:  buildAppTargetIndex(Config{Targets: targets, Apps: apps}),
	}
	g, _ := buildDependencyGraph(targets)
	e.topoGraph = g
	for k, v := range states {
		e.lastKnown[k] = v
	}
	return e
}

// ── TestFleetSnapshot_Standalone ─────────────────────────────────────────────

func TestFleetSnapshot_Standalone_AllUp(t *testing.T) {
	targets := []Target{
		{ID: "db", Name: "DB", Type: "tcp", Target: "127.0.0.1:5432"},
		{ID: "api", Name: "API", Type: "tcp", Target: "127.0.0.1:8080"},
	}
	states := map[string]PersistedState{
		"db":  {State: "up", Seq: 1},
		"api": {State: "up", Seq: 2},
	}
	e := newStandaloneEngine(targets, nil, states)
	snap := e.FleetSnapshot()

	if snap.Cluster != nil {
		t.Error("standalone: cluster section should be nil")
	}
	if snap.Summary.Total != 2 {
		t.Errorf("total: want 2, got %d", snap.Summary.Total)
	}
	if snap.Summary.Up != 2 {
		t.Errorf("up: want 2, got %d", snap.Summary.Up)
	}
	if snap.Summary.HardDown != 0 {
		t.Errorf("hard_down: want 0, got %d", snap.Summary.HardDown)
	}
	if len(snap.Incidents) != 0 {
		t.Errorf("incidents: want 0, got %d", len(snap.Incidents))
	}
}

func TestFleetSnapshot_Standalone_HardDown(t *testing.T) {
	targets := []Target{
		{ID: "db", Name: "DB", Type: "tcp", Target: "127.0.0.1:5432"},
	}
	states := map[string]PersistedState{
		"db": {State: "hard_down", Seq: 3, ErrorCode: "connection refused"},
	}
	e := newStandaloneEngine(targets, nil, states)
	snap := e.FleetSnapshot()

	if snap.Summary.HardDown != 1 {
		t.Errorf("hard_down: want 1, got %d", snap.Summary.HardDown)
	}
	if len(snap.Incidents) != 1 {
		t.Errorf("incidents: want 1, got %d", len(snap.Incidents))
	}
	if snap.Incidents[0].TargetID != "db" {
		t.Errorf("incident target: want db, got %q", snap.Incidents[0].TargetID)
	}
	if snap.Incidents[0].ErrorCode != "connection refused" {
		t.Errorf("incident error: want 'connection refused', got %q", snap.Incidents[0].ErrorCode)
	}
}

func TestFleetSnapshot_Standalone_SoftDown(t *testing.T) {
	targets := []Target{
		{ID: "svc", Name: "SVC", Type: "tcp", Target: "127.0.0.1:9090"},
	}
	e := newStandaloneEngine(targets, nil, nil)
	// Inject a pending (soft-down) entry manually.
	e.stateMu.Lock()
	e.pending[targets[0].typeKey()] = PendingEntry{Target: targets[0], RetryCount: 1}
	e.stateMu.Unlock()

	snap := e.FleetSnapshot()
	if snap.Summary.SoftDown != 1 {
		t.Errorf("soft_down: want 1, got %d", snap.Summary.SoftDown)
	}
}

// ── TestFleetSnapshot_AppContext ──────────────────────────────────────────────

func TestFleetSnapshot_AppContext(t *testing.T) {
	targets := []Target{
		{ID: "db", Name: "DB", Type: "tcp", Target: "127.0.0.1:5432"},
	}
	apps := []App{
		{Name: "payment-service", OwnerTeam: "fintech-sre", Uses: []string{"db"}},
	}
	states := map[string]PersistedState{
		"db": {State: "hard_down", Seq: 1},
	}
	e := newStandaloneEngine(targets, apps, states)
	snap := e.FleetSnapshot()

	if len(snap.Targets) != 1 {
		t.Fatalf("want 1 target, got %d", len(snap.Targets))
	}
	ft := snap.Targets[0]
	if len(ft.AffectedApps) != 1 || ft.AffectedApps[0] != "payment-service" {
		t.Errorf("affected_apps: want [payment-service], got %v", ft.AffectedApps)
	}
	if len(ft.OwnerTeams) != 1 || ft.OwnerTeams[0] != "fintech-sre" {
		t.Errorf("owner_teams: want [fintech-sre], got %v", ft.OwnerTeams)
	}
}

// ── TestFleetSnapshot_RootCause ───────────────────────────────────────────────

func TestFleetSnapshot_RootCause(t *testing.T) {
	// checkout depends on payment-service depends on db-primary.
	// All hard_down → root cause should be db-primary for all.
	targets := []Target{
		{ID: "checkout", Name: "Checkout", Type: "tcp", Target: "127.0.0.1:1", DependsOn: []string{"payment-service"}},
		{ID: "payment-service", Name: "Payment", Type: "tcp", Target: "127.0.0.1:2", DependsOn: []string{"db-primary"}},
		{ID: "db-primary", Name: "DB Primary", Type: "tcp", Target: "127.0.0.1:3"},
	}
	states := map[string]PersistedState{
		"checkout":        {State: "hard_down", Seq: 2},
		"payment-service": {State: "hard_down", Seq: 2},
		"db-primary":      {State: "hard_down", Seq: 1},
	}
	e := newStandaloneEngine(targets, nil, states)
	snap := e.FleetSnapshot()

	byID := make(map[string]FleetTarget, len(snap.Targets))
	for _, ft := range snap.Targets {
		byID[ft.Name] = ft
	}

	if byID["DB Primary"].RootCause != "db-primary" {
		t.Errorf("db-primary root cause: want db-primary, got %q", byID["DB Primary"].RootCause)
	}
	if byID["Payment"].RootCause != "db-primary" {
		t.Errorf("payment root cause: want db-primary, got %q", byID["Payment"].RootCause)
	}
	if byID["Checkout"].RootCause != "db-primary" {
		t.Errorf("checkout root cause: want db-primary, got %q", byID["Checkout"].RootCause)
	}
}

func TestFleetSnapshot_CascadingImpact(t *testing.T) {
	// db-primary ← payment-service ← checkout
	targets := []Target{
		{ID: "checkout", Name: "Checkout", Type: "tcp", Target: "127.0.0.1:1", DependsOn: []string{"payment-service"}},
		{ID: "payment-service", Name: "Payment", Type: "tcp", Target: "127.0.0.1:2", DependsOn: []string{"db-primary"}},
		{ID: "db-primary", Name: "DB Primary", Type: "tcp", Target: "127.0.0.1:3"},
	}
	states := map[string]PersistedState{
		"db-primary": {State: "hard_down", Seq: 1},
	}
	e := newStandaloneEngine(targets, nil, states)
	snap := e.FleetSnapshot()

	byName := make(map[string]FleetTarget, len(snap.Targets))
	for _, ft := range snap.Targets {
		byName[ft.Name] = ft
	}

	dbPrimary := byName["DB Primary"]
	// Even though db-primary is only hard_down, its cascading impact (structural)
	// should list payment-service and checkout.
	impact := dbPrimary.CascadingImpact
	if len(impact) != 2 {
		t.Errorf("cascading impact: want 2, got %v", impact)
	}
}

// ── TestFleetSnapshot_TargetOrder ─────────────────────────────────────────────

func TestFleetSnapshot_TargetOrder(t *testing.T) {
	// Targets should be sorted by name in the output.
	targets := []Target{
		{ID: "z-svc", Name: "Z Service", Type: "tcp", Target: "127.0.0.1:1"},
		{ID: "a-svc", Name: "A Service", Type: "tcp", Target: "127.0.0.1:2"},
		{ID: "m-svc", Name: "M Service", Type: "tcp", Target: "127.0.0.1:3"},
	}
	e := newStandaloneEngine(targets, nil, nil)
	snap := e.FleetSnapshot()

	if len(snap.Targets) != 3 {
		t.Fatalf("want 3 targets, got %d", len(snap.Targets))
	}
	names := []string{snap.Targets[0].Name, snap.Targets[1].Name, snap.Targets[2].Name}
	if names[0] != "A Service" || names[1] != "M Service" || names[2] != "Z Service" {
		t.Errorf("targets not sorted: %v", names)
	}
}
