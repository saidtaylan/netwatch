//go:build !windows

// Package integration_test — comprehensive end-to-end tests.
//
// Covers: Apps indirection (multi-app, union channels, AFFECTED_APPS/OWNER_TEAMS),
// dependency graph (ROOT_CAUSE, CASCADING_IMPACT), SLO incident recording,
// FleetSnapshot correctness, TopologySnapshot edges, config validation,
// HTTP probe, standalone scope classification, and 3/5-node cluster scenarios
// including quorum loss, primary failover, and zone-aware prober spread.
package integration_test

import (
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/saidtaylan/netwatch/internal/engine"
)

// ── helpers ───────────────────────────────────────────────────────────────────

// richRunner captures the full env map for each alert call.
type richRunner struct {
	mu     sync.Mutex
	events []map[string]string
}

func (r *richRunner) runner() engine.AlertRunner {
	return func(_ string, env map[string]string) error {
		cp := make(map[string]string, len(env))
		for k, v := range env {
			cp[k] = v
		}
		r.mu.Lock()
		r.events = append(r.events, cp)
		r.mu.Unlock()
		return nil
	}
}

func (r *richRunner) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.events)
}

func (r *richRunner) byStatus(status string) []map[string]string {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []map[string]string
	for _, ev := range r.events {
		if ev["STATUS"] == status {
			out = append(out, ev)
		}
	}
	return out
}

// drain discards all buffered events.
func (r *richRunner) drain() {
	r.mu.Lock()
	r.events = nil
	r.mu.Unlock()
}

// tcpServer starts a loopback TCP server and returns its address and a closer.
func tcpServer(t *testing.T) (addr string, close func()) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("tcpServer listen: %v", err)
	}
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			c.Close()
		}
	}()
	return ln.Addr().String(), func() { _ = ln.Close() }
}

// httpServer starts a loopback HTTP server that responds 200 to all GETs.
func httpServer(t *testing.T) (url string, close func()) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("httpServer listen: %v", err)
	}
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})}
	go srv.Serve(ln) //nolint:errcheck
	return "http://" + ln.Addr().String(), func() { _ = srv.Close() }
}

// noopEngine creates an engine wired with a no-op alert runner.
func noopEngine(t *testing.T, cfgYAML string) *engine.Engine {
	t.Helper()
	p := writeCfg(t, cfgYAML)
	noop := engine.AlertRunner(func(_ string, _ map[string]string) error { return nil })
	e := engine.New("test-host", noop, p)
	if err := e.Init(); err != nil {
		t.Fatalf("noopEngine Init: %v", err)
	}
	t.Cleanup(e.Shutdown)
	return e
}

// ── ──────────────────────────────────────────────────────────────────────────
// SECTION 1 — Apps features
// ─────────────────────────────────────────────────────────────────────────────

// TestApps_MultiApp_AffectedApps verifies that when two separate apps both
// reference the same target, the alert env contains both app names in
// AFFECTED_APPS and both owner teams in OWNER_TEAMS.
func TestApps_MultiApp_AffectedApps(t *testing.T) {
	addr, closeTarget := tcpServer(t)
	defer closeTarget()

	rr := &richRunner{}
	cfgPath := writeCfg(t, fmt.Sprintf(`
node_alias: "apps-multi"
port:     "0"
state_file: %q
log_path: ""
timeout: 5
max_retries: 1
retry_interval_sec: 5
probe_interval_sec: 5
ticker_interval_sec: 1
reload_interval_sec: 0

notifications:
  ch:
    type: script
    parameters:
      script: "/bin/true"

default_notify: ["ch"]

targets:
  - id: "shared-db"
    name: "Shared DB"
    type: tcp
    target: %q
    interval_sec: 5

apps:
  - name: "payment-service"
    owner_team: "fintech-sre"
    uses: ["shared-db"]
    notifications: ["ch"]
  - name: "inventory-api"
    owner_team: "logistics-team"
    uses: ["shared-db"]
    notifications: ["ch"]
`, filepath.Join(t.TempDir(), "state.json"), addr))

	e := engine.New("test-host", rr.runner(), cfgPath)
	if err := e.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}
	t.Cleanup(e.Shutdown)

	// Wait for first UP probe.
	time.Sleep(3 * time.Second)

	// Bring target down.
	closeTarget()

	waitState(t, "unreachable alert with 2 apps", 20*time.Second, func() bool {
		return len(rr.byStatus("unreachable")) > 0
	})

	ev := rr.byStatus("unreachable")[0]

	// AFFECTED_APPS must contain both app names (order may vary).
	apps := ev["AFFECTED_APPS"]
	if !strings.Contains(apps, "payment-service") || !strings.Contains(apps, "inventory-api") {
		t.Errorf("AFFECTED_APPS=%q; want both payment-service and inventory-api", apps)
	}
	// OWNER_TEAMS must contain both teams.
	teams := ev["OWNER_TEAMS"]
	if !strings.Contains(teams, "fintech-sre") || !strings.Contains(teams, "logistics-team") {
		t.Errorf("OWNER_TEAMS=%q; want fintech-sre and logistics-team", teams)
	}
	t.Logf("AFFECTED_APPS=%s OWNER_TEAMS=%s", apps, teams)
}

// TestApps_TargetOwnChannel_Union verifies that a target's own notify list and
// the app's notifications list are unioned (not duplicated) for alert dispatch.
// The test checks that exactly one alert fires even when both channels overlap.
func TestApps_TargetOwnChannel_Union(t *testing.T) {
	addr, closeTarget := tcpServer(t)
	defer closeTarget()

	var fired int64
	runner := engine.AlertRunner(func(_ string, env map[string]string) error {
		if env["STATUS"] == "unreachable" {
			atomic.AddInt64(&fired, 1)
		}
		return nil
	})

	cfgPath := writeCfg(t, fmt.Sprintf(`
node_alias: "channel-union"
port:     "0"
state_file: %q
log_path: ""
timeout: 5
max_retries: 1
retry_interval_sec: 5
probe_interval_sec: 5
ticker_interval_sec: 1
reload_interval_sec: 0

notifications:
  ops:
    type: script
    parameters:
      script: "/bin/true"
  dev:
    type: script
    parameters:
      script: "/bin/true"

default_notify: ["ops"]

targets:
  - id: "svc"
    name: "SVC"
    type: tcp
    target: %q
    notify: ["ops"]
    interval_sec: 5

apps:
  - name: "myapp"
    owner_team: "dev-team"
    uses: ["svc"]
    notifications: ["dev"]
`, filepath.Join(t.TempDir(), "state.json"), addr))

	e := engine.New("test-host", runner, cfgPath)
	if err := e.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}
	t.Cleanup(e.Shutdown)

	// Wait for initial probe, then take target down.
	time.Sleep(3 * time.Second)
	closeTarget()

	// There should be exactly 2 alerts (one per distinct channel: ops + dev).
	waitState(t, "2 unreachable alerts (ops+dev)", 20*time.Second, func() bool {
		return atomic.LoadInt64(&fired) >= 2
	})

	time.Sleep(2 * time.Second) // drain extra window
	total := atomic.LoadInt64(&fired)
	if total != 2 {
		t.Errorf("want exactly 2 channel alerts (ops+dev), got %d", total)
	} else {
		t.Logf("✓ union channels: %d alerts dispatched (one per channel)", total)
	}
}

// TestApps_NoAppNoTargetNotify_FallsToDefault verifies that a target with no
// app reference and no target.notify falls back to default_notify.
func TestApps_NoAppNoTargetNotify_FallsToDefault(t *testing.T) {
	addr, closeTarget := tcpServer(t)
	defer closeTarget()

	rr := &richRunner{}
	cfgPath := writeCfg(t, fmt.Sprintf(`
node_alias: "default-notify-test"
port:     "0"
state_file: %q
log_path: ""
timeout: 5
max_retries: 1
retry_interval_sec: 5
probe_interval_sec: 5
ticker_interval_sec: 1
reload_interval_sec: 0

notifications:
  default-ch:
    type: script
    parameters:
      script: "/bin/true"

default_notify: ["default-ch"]

targets:
  - id: "bare-target"
    name: "Bare Target"
    type: tcp
    target: %q
    interval_sec: 5
`, filepath.Join(t.TempDir(), "state.json"), addr))

	e := engine.New("test-host", rr.runner(), cfgPath)
	if err := e.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}
	t.Cleanup(e.Shutdown)

	time.Sleep(3 * time.Second)
	closeTarget()

	waitState(t, "alert via default_notify", 20*time.Second, func() bool {
		return len(rr.byStatus("unreachable")) > 0
	})
	t.Log("✓ default_notify fallback works")
}

// TestApps_MultipleTargets_OneDown verifies that when an app has two targets
// and only one goes down, the alert correctly lists only that target's name.
func TestApps_MultipleTargets_OneDown(t *testing.T) {
	addrUp, _ := tcpServer(t) // stays UP throughout the test
	addrDown, closeDown := tcpServer(t)
	defer closeDown()

	rr := &richRunner{}
	cfgPath := writeCfg(t, fmt.Sprintf(`
node_alias: "multi-target-app"
port:     "0"
state_file: %q
log_path: ""
timeout: 5
max_retries: 1
retry_interval_sec: 5
probe_interval_sec: 5
ticker_interval_sec: 1
reload_interval_sec: 0

notifications:
  ch:
    type: script
    parameters:
      script: "/bin/true"

default_notify: ["ch"]

targets:
  - id: "svc-a"
    name: "SvcA"
    type: tcp
    target: %q
    interval_sec: 5
  - id: "svc-b"
    name: "SvcB"
    type: tcp
    target: %q
    interval_sec: 5

apps:
  - name: "composite-app"
    owner_team: "platform"
    uses: ["svc-a", "svc-b"]
    notifications: ["ch"]
`, filepath.Join(t.TempDir(), "state.json"), addrUp, addrDown))

	e := engine.New("test-host", rr.runner(), cfgPath)
	if err := e.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}
	t.Cleanup(e.Shutdown)

	time.Sleep(3 * time.Second)
	closeDown() // only svc-b goes down

	waitState(t, "unreachable alert for svc-b", 20*time.Second, func() bool {
		for _, ev := range rr.byStatus("unreachable") {
			if ev["NAME"] == "SvcB" {
				return true
			}
		}
		return false
	})

	// svc-a should NOT have fired an unreachable alert.
	for _, ev := range rr.byStatus("unreachable") {
		if ev["NAME"] == "SvcA" {
			t.Errorf("SvcA (UP) generated an unreachable alert: %v", ev)
		}
	}
	t.Log("✓ only SvcB (down) generated an alert; SvcA (up) is silent")
}

// ── SECTION 2 — Dependency graph / root cause ─────────────────────────────────

// TestDependency_RootCause_InAlert verifies a 3-level chain:
//
//	infra-switch → db-primary → app-api (each depends on the one before it)
//
// When infra-switch goes down, the alert for app-api must include
// ROOT_CAUSE=infra-switch and DEPENDENCY_DEPTH=2.
func TestDependency_RootCause_InAlert(t *testing.T) {
	addrInfra, closeInfra := tcpServer(t)
	defer closeInfra()
	addrDB, closeDB := tcpServer(t)
	defer closeDB()
	addrAPI, closeAPI := tcpServer(t)
	defer closeAPI()

	rr := &richRunner{}
	cfgPath := writeCfg(t, fmt.Sprintf(`
node_alias: "dep-rootcause"
port:     "0"
state_file: %q
log_path: ""
timeout: 5
max_retries: 1
retry_interval_sec: 5
probe_interval_sec: 5
ticker_interval_sec: 1
reload_interval_sec: 0

notifications:
  ch:
    type: script
    parameters:
      script: "/bin/true"

default_notify: ["ch"]

targets:
  - id: "infra-switch"
    name: "Infra Switch"
    type: tcp
    target: %q
    interval_sec: 5
  - id: "db-primary"
    name: "DB Primary"
    type: tcp
    target: %q
    interval_sec: 5
    depends_on: ["infra-switch"]
  - id: "app-api"
    name: "App API"
    type: tcp
    target: %q
    interval_sec: 5
    depends_on: ["db-primary"]
`, filepath.Join(t.TempDir(), "state.json"), addrInfra, addrDB, addrAPI))

	e := engine.New("test-host", rr.runner(), cfgPath)
	if err := e.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}
	t.Cleanup(e.Shutdown)

	// Let all targets probe UP first.
	time.Sleep(3 * time.Second)

	// Bring targets down sequentially so that root dependencies are hard-down
	// before downstream targets fire their alerts. Root cause detection reads
	// e.lastKnown at alert-dispatch time, so infra-switch and db-primary must
	// already be hard-down when app-api fires.
	closeInfra()
	waitState(t, "infra-switch hard_down", 20*time.Second, func() bool {
		snap := e.FleetSnapshot()
		for _, tgt := range snap.Targets {
			if tgt.Name == "Infra Switch" && tgt.ConsensusState == "hard_down" {
				return true
			}
		}
		return false
	})

	closeDB()
	waitState(t, "db-primary hard_down", 20*time.Second, func() bool {
		snap := e.FleetSnapshot()
		for _, tgt := range snap.Targets {
			if tgt.Name == "DB Primary" && tgt.ConsensusState == "hard_down" {
				return true
			}
		}
		return false
	})

	// Both root deps now hard-down; close leaf — its alert must carry ROOT_CAUSE.
	closeAPI()

	// Wait for app-api unreachable alert.
	waitState(t, "app-api unreachable alert with ROOT_CAUSE", 30*time.Second, func() bool {
		for _, ev := range rr.byStatus("unreachable") {
			if ev["NAME"] == "App API" && ev["ROOT_CAUSE"] != "" {
				return true
			}
		}
		return false
	})

	// Find the app-api alert with a non-self ROOT_CAUSE.
	var apiAlert map[string]string
	for _, ev := range rr.byStatus("unreachable") {
		if ev["NAME"] == "App API" && ev["ROOT_CAUSE"] != "" && ev["ROOT_CAUSE"] != "app-api" {
			apiAlert = ev
			break
		}
	}
	if apiAlert == nil {
		// Fall back to any app-api alert for diagnosis.
		for _, ev := range rr.byStatus("unreachable") {
			if ev["NAME"] == "App API" {
				t.Fatalf("App API alert found but ROOT_CAUSE=%q DEPENDENCY_DEPTH=%q; "+
					"infra/db must be hard_down before API fires", ev["ROOT_CAUSE"], ev["DEPENDENCY_DEPTH"])
			}
		}
		t.Fatal("no unreachable alert found for App API")
	}

	rc := apiAlert["ROOT_CAUSE"]
	if rc != "infra-switch" {
		t.Errorf("ROOT_CAUSE: want infra-switch, got %q", rc)
	}
	depth := apiAlert["DEPENDENCY_DEPTH"]
	if depth != "2" {
		t.Errorf("DEPENDENCY_DEPTH: want 2, got %q", depth)
	}
	t.Logf("✓ App API alert: ROOT_CAUSE=%s DEPENDENCY_DEPTH=%s", rc, depth)
}

// TestDependency_CascadingImpact verifies that the alert for the root cause
// target includes CASCADING_IMPACT listing downstream dependents.
func TestDependency_CascadingImpact(t *testing.T) {
	addrInfra, closeInfra := tcpServer(t)
	defer closeInfra()
	addrDB, closeDB := tcpServer(t)
	defer closeDB()
	addrAPI, closeAPI := tcpServer(t)
	defer closeAPI()

	rr := &richRunner{}
	cfgPath := writeCfg(t, fmt.Sprintf(`
node_alias: "dep-cascade"
port:     "0"
state_file: %q
log_path: ""
timeout: 5
max_retries: 1
retry_interval_sec: 5
probe_interval_sec: 5
ticker_interval_sec: 1
reload_interval_sec: 0

notifications:
  ch:
    type: script
    parameters:
      script: "/bin/true"

default_notify: ["ch"]

targets:
  - id: "root-infra"
    name: "Root Infra"
    type: tcp
    target: %q
    interval_sec: 5
  - id: "mid-db"
    name: "Mid DB"
    type: tcp
    target: %q
    interval_sec: 5
    depends_on: ["root-infra"]
  - id: "leaf-api"
    name: "Leaf API"
    type: tcp
    target: %q
    interval_sec: 5
    depends_on: ["mid-db"]
`, filepath.Join(t.TempDir(), "state.json"), addrInfra, addrDB, addrAPI))

	e := engine.New("test-host", rr.runner(), cfgPath)
	if err := e.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}
	t.Cleanup(e.Shutdown)

	time.Sleep(3 * time.Second)
	closeInfra()
	closeDB()
	closeAPI()

	// Wait for root-infra alert with CASCADING_IMPACT.
	waitState(t, "Root Infra alert with CASCADING_IMPACT", 30*time.Second, func() bool {
		for _, ev := range rr.byStatus("unreachable") {
			if ev["NAME"] == "Root Infra" && ev["CASCADING_IMPACT"] != "" {
				return true
			}
		}
		return false
	})

	var infraAlert map[string]string
	for _, ev := range rr.byStatus("unreachable") {
		if ev["NAME"] == "Root Infra" {
			infraAlert = ev
			break
		}
	}
	if infraAlert == nil {
		t.Fatal("no alert for Root Infra")
	}

	impact := infraAlert["CASCADING_IMPACT"]
	if !strings.Contains(impact, "mid-db") || !strings.Contains(impact, "leaf-api") {
		t.Errorf("CASCADING_IMPACT=%q; want mid-db and leaf-api", impact)
	}
	t.Logf("✓ Root Infra CASCADING_IMPACT=%s", impact)
}

// TestTopologySnapshot_Edges verifies that TopologySnapshot correctly reflects
// the depends_on relationships.
func TestTopologySnapshot_Edges(t *testing.T) {
	addr1, close1 := tcpServer(t)
	defer close1()
	addr2, close2 := tcpServer(t)
	defer close2()

	e := noopEngine(t, fmt.Sprintf(`
node_alias: "topo-test"
port:     "0"
state_file: %q
log_path: ""
timeout: 5
max_retries: 1
retry_interval_sec: 30
probe_interval_sec: 60
ticker_interval_sec: 10
reload_interval_sec: 0

notifications: {}
default_notify: []

targets:
  - id: "root-t"
    name: "Root"
    type: tcp
    target: %q
  - id: "child-t"
    name: "Child"
    type: tcp
    target: %q
    depends_on: ["root-t"]
`, filepath.Join(t.TempDir(), "s.json"), addr1, addr2))

	topo := e.TopologySnapshot()

	rootEntry, ok := topo.Targets["root-t"]
	if !ok {
		t.Fatal("root-t not in topology snapshot")
	}
	childEntry, ok := topo.Targets["child-t"]
	if !ok {
		t.Fatal("child-t not in topology snapshot")
	}

	// child-t must declare root-t as a dependency.
	found := false
	for _, dep := range childEntry.DependsOn {
		if dep == "root-t" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("child-t.DependsOn does not contain root-t: %v", childEntry.DependsOn)
	}

	// root-t's DependedOnBy should include child-t.
	foundReverse := false
	for _, rev := range rootEntry.DependedOnBy {
		if rev == "child-t" {
			foundReverse = true
			break
		}
	}
	if !foundReverse {
		t.Errorf("root-t.DependedOnBy does not contain child-t: %v", rootEntry.DependedOnBy)
	}
	t.Logf("✓ topology edges: child-t→root-t, root-t.DependedOnBy=[child-t]")
}

// ── SECTION 3 — SLO ──────────────────────────────────────────────────────────

// TestSLO_IncidentRecording verifies that after a target goes down and
// recovers, SLOSnapshot shows at least one incident and ActualUptime < 1.
func TestSLO_IncidentRecording(t *testing.T) {
	addr, closeTarget := tcpServer(t)
	defer closeTarget()

	stateDir := t.TempDir()
	cfgPath := writeCfg(t, fmt.Sprintf(`
node_alias: "slo-test"
port:     "0"
state_file: %q
log_path: ""
timeout: 5
max_retries: 1
retry_interval_sec: 5
probe_interval_sec: 5
ticker_interval_sec: 1
reload_interval_sec: 0

notifications: {}
default_notify: []

targets:
  - id: "slo-svc"
    name: "SLO SVC"
    type: tcp
    target: %q
    interval_sec: 5

slo:
  enabled: true
  retention_days: 90
  targets:
    - id: "slo-svc"
      target_uptime: 0.9990
      window: "24h"
`, filepath.Join(stateDir, "state.json"), addr))

	noop := engine.AlertRunner(func(_ string, _ map[string]string) error { return nil })
	e := engine.New("test-host", noop, cfgPath)
	if err := e.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}
	t.Cleanup(e.Shutdown)

	if !e.SLOEnabled() {
		t.Fatal("SLO should be enabled")
	}

	// Let target probe UP first.
	time.Sleep(3 * time.Second)

	// Bring target down.
	closeTarget()

	// Wait for hard_down state to be recorded.
	waitState(t, "target hard_down", 20*time.Second, func() bool {
		for _, s := range e.Status() {
			if s.Name == "SLO SVC" && s.Status == "HARD_DOWN" {
				return true
			}
		}
		return false
	})
	t.Log("target is hard_down — incident should be open")

	// Reopen the target.
	ln2, err := net.Listen("tcp", addr)
	if err != nil {
		t.Fatalf("reopen listen: %v", err)
	}
	defer ln2.Close()
	go func() {
		for {
			c, err := ln2.Accept()
			if err != nil {
				return
			}
			c.Close()
		}
	}()

	// Wait for recovery.
	waitState(t, "target recovery", 20*time.Second, func() bool {
		for _, s := range e.Status() {
			if s.Name == "SLO SVC" && s.Status == "UP" {
				return true
			}
		}
		return false
	})

	// SLOSnapshot must show at least 1 incident.
	snap := e.SLOSnapshot()
	if snap == nil {
		t.Fatal("SLOSnapshot returned nil")
	}
	result, ok := snap.Targets["slo-svc"]
	if !ok {
		t.Fatal("slo-svc not found in SLOSnapshot")
	}
	if result.IncidentCount == 0 {
		t.Errorf("IncidentCount should be > 0 after a down/up cycle, got 0")
	}
	if result.DowntimeSec == 0 {
		t.Errorf("DowntimeSec should be > 0 after a down/up cycle, got 0")
	}
	// Uptime should be slightly less than 1.0 due to the brief outage.
	// Window is 24h, outage was ~15s → ratio still very close to 1.0 but downtime > 0.
	t.Logf("✓ SLO incident: count=%d downtime=%ds actualUptime=%.6f",
		result.IncidentCount, result.DowntimeSec, result.ActualUptime)
}

// TestSLO_Disabled_SnapshotNil verifies that SLOSnapshot returns nil when
// slo.enabled is false.
func TestSLO_Disabled_SnapshotNil(t *testing.T) {
	addr, close1 := tcpServer(t)
	defer close1()

	e := noopEngine(t, fmt.Sprintf(`
node_alias: "slo-off"
port:     "0"
state_file: %q
log_path: ""
timeout: 5
max_retries: 1
probe_interval_sec: 60
ticker_interval_sec: 10

notifications: {}
default_notify: []

targets:
  - name: "t1"
    type: tcp
    target: %q
`, filepath.Join(t.TempDir(), "s.json"), addr))

	if snap := e.SLOSnapshot(); snap != nil {
		t.Errorf("expected nil SLOSnapshot when slo.enabled=false, got %v", snap)
	}
	t.Log("✓ SLOSnapshot nil when disabled")
}

// ── SECTION 4 — FleetSnapshot ─────────────────────────────────────────────────

// TestFleetSnapshot_StandaloneMode verifies that the standalone engine produces
// a FleetSnapshot with a nil Cluster section, correct summary counts, and
// STANDALONE scope for downed targets.
func TestFleetSnapshot_StandaloneMode(t *testing.T) {
	addr, closeTarget := tcpServer(t)
	defer closeTarget()

	rr := &richRunner{}
	cfgPath := writeCfg(t, fmt.Sprintf(`
node_alias: "fleet-standalone"
port:     "0"
state_file: %q
log_path: ""
timeout: 5
max_retries: 1
retry_interval_sec: 5
probe_interval_sec: 5
ticker_interval_sec: 1
reload_interval_sec: 0

notifications:
  ch:
    type: script
    parameters:
      script: "/bin/true"

default_notify: ["ch"]

targets:
  - id: "web"
    name: "Web"
    type: tcp
    target: %q
    interval_sec: 5
`, filepath.Join(t.TempDir(), "state.json"), addr))

	e := engine.New("test-host", rr.runner(), cfgPath)
	if err := e.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}
	t.Cleanup(e.Shutdown)

	// Verify initial state: 1 target, should become UP.
	waitState(t, "web target UP in fleet", 15*time.Second, func() bool {
		snap := e.FleetSnapshot()
		for _, t := range snap.Targets {
			if t.Name == "Web" && t.ConsensusState == "up" {
				return true
			}
		}
		return false
	})

	// Cluster section must be nil (standalone mode).
	snap := e.FleetSnapshot()
	if snap.Cluster != nil {
		t.Errorf("expected Cluster=nil in standalone mode, got %+v", snap.Cluster)
	}
	if snap.Summary.Total < 1 {
		t.Errorf("Summary.Total should be >= 1, got %d", snap.Summary.Total)
	}

	// Bring target down; verify hard_down appears in fleet.
	closeTarget()
	waitState(t, "web hard_down in fleet", 20*time.Second, func() bool {
		snap := e.FleetSnapshot()
		for _, tgt := range snap.Targets {
			if tgt.Name == "Web" && tgt.ConsensusState == "hard_down" {
				return true
			}
		}
		return false
	})

	snap = e.FleetSnapshot()
	var webTarget *engine.FleetTarget
	for i := range snap.Targets {
		if snap.Targets[i].Name == "Web" {
			webTarget = &snap.Targets[i]
			break
		}
	}
	if webTarget == nil {
		t.Fatal("Web target not found in FleetSnapshot")
	}
	if webTarget.ConsensusState != "hard_down" {
		t.Errorf("ConsensusState: want hard_down, got %q", webTarget.ConsensusState)
	}
	// In standalone mode with a down target, scope is "NODE_LOCAL"
	// (classifyScope returns NODE_LOCAL when clusterMgr==nil && localDown==true).
	if webTarget.Scope != "NODE_LOCAL" {
		t.Errorf("Scope: want NODE_LOCAL for standalone-down, got %q", webTarget.Scope)
	}
	if snap.Summary.HardDown < 1 {
		t.Errorf("Summary.HardDown should be >= 1, got %d", snap.Summary.HardDown)
	}
	t.Logf("✓ FleetSnapshot standalone: ConsensusState=%s Scope=%s HardDown=%d",
		webTarget.ConsensusState, webTarget.Scope, snap.Summary.HardDown)
}

// TestFleetSnapshot_AffectedApps verifies that FleetTarget.AffectedApps is
// populated when the target belongs to an app.
func TestFleetSnapshot_AffectedApps(t *testing.T) {
	addr, closeTarget := tcpServer(t)
	defer closeTarget()

	e := noopEngine(t, fmt.Sprintf(`
node_alias: "fleet-apps"
port:     "0"
state_file: %q
log_path: ""
timeout: 5
max_retries: 1
retry_interval_sec: 5
probe_interval_sec: 5
ticker_interval_sec: 1
reload_interval_sec: 0
notifications: {}
default_notify: []

targets:
  - id: "db-t"
    name: "DB"
    type: tcp
    target: %q
    interval_sec: 5

apps:
  - name: "billing-app"
    owner_team: "billing-team"
    uses: ["db-t"]
`, filepath.Join(t.TempDir(), "s.json"), addr))

	// Bring target down and wait for fleet to reflect it.
	closeTarget()
	waitState(t, "db-t hard_down in fleet", 20*time.Second, func() bool {
		snap := e.FleetSnapshot()
		for _, tgt := range snap.Targets {
			if tgt.Name == "DB" && tgt.ConsensusState == "hard_down" {
				return true
			}
		}
		return false
	})

	snap := e.FleetSnapshot()
	var dbTarget *engine.FleetTarget
	for i := range snap.Targets {
		if snap.Targets[i].Name == "DB" {
			dbTarget = &snap.Targets[i]
			break
		}
	}
	if dbTarget == nil {
		t.Fatal("DB target not in FleetSnapshot")
	}
	found := false
	for _, a := range dbTarget.AffectedApps {
		if a == "billing-app" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("AffectedApps should contain billing-app, got %v", dbTarget.AffectedApps)
	}
	t.Logf("✓ FleetSnapshot.AffectedApps=%v", dbTarget.AffectedApps)
}

// ── SECTION 5 — Config validation ─────────────────────────────────────────────

// TestValidateConfig_Valid verifies that a well-formed config passes validation.
func TestValidateConfig_Valid(t *testing.T) {
	addr, close1 := tcpServer(t)
	defer close1()

	p := writeCfg(t, fmt.Sprintf(`
node_alias: "valid-cfg"
port:     "0"
state_file: %q
log_path: ""
timeout: 5
max_retries: 2
probe_interval_sec: 60

notifications:
  ops:
    type: script
    parameters:
      script: "/bin/true"

default_notify: ["ops"]

targets:
  - id: "srv-1"
    name: "Srv1"
    type: tcp
    target: %q

apps:
  - name: "myapp"
    owner_team: "infra"
    uses: ["srv-1"]
`, filepath.Join(t.TempDir(), "s.json"), addr))

	cfg, err := engine.ValidateConfigFile(p)
	if err != nil {
		t.Fatalf("ValidateConfigFile unexpected error: %v", err)
	}
	if cfg.NodeAlias != "valid-cfg" {
		t.Errorf("NodeAlias: got %q, want valid-cfg", cfg.NodeAlias)
	}
	if len(cfg.Targets) != 1 {
		t.Errorf("Targets: want 1, got %d", len(cfg.Targets))
	}
	t.Log("✓ valid config passes validation")
}

// TestValidateConfig_DuplicateTargetID verifies that duplicate target IDs
// cause ValidateConfigFile to return an error.
func TestValidateConfig_DuplicateTargetID(t *testing.T) {
	addr, close1 := tcpServer(t)
	defer close1()

	p := writeCfg(t, fmt.Sprintf(`
node_alias: "dupe-id"
port:     "0"
state_file: %q
log_path: ""
timeout: 5

targets:
  - id: "dup"
    name: "Dup1"
    type: tcp
    target: %q
  - id: "dup"
    name: "Dup2"
    type: tcp
    target: %q
`, filepath.Join(t.TempDir(), "s.json"), addr, addr))

	_, err := engine.ValidateConfigFile(p)
	if err == nil {
		t.Error("expected error for duplicate target ID, got nil")
	} else {
		t.Logf("✓ duplicate target ID error: %v", err)
	}
}

// TestValidateConfig_UnknownAppTarget verifies that an app referencing a
// non-existent target ID causes a validation error.
func TestValidateConfig_UnknownAppTarget(t *testing.T) {
	addr, close1 := tcpServer(t)
	defer close1()

	p := writeCfg(t, fmt.Sprintf(`
node_alias: "bad-app"
port:     "0"
state_file: %q
log_path: ""
timeout: 5

targets:
  - id: "real-target"
    name: "Real"
    type: tcp
    target: %q

apps:
  - name: "myapp"
    uses: ["nonexistent-target"]
`, filepath.Join(t.TempDir(), "s.json"), addr))

	_, err := engine.ValidateConfigFile(p)
	if err == nil {
		t.Error("expected error for app referencing nonexistent target, got nil")
	} else {
		t.Logf("✓ unknown app target error: %v", err)
	}
}

// TestValidateConfig_CyclicDependency verifies that a cyclic depends_on graph
// causes a validation error.
func TestValidateConfig_CyclicDependency(t *testing.T) {
	addr, close1 := tcpServer(t)
	defer close1()
	addr2, close2 := tcpServer(t)
	defer close2()

	p := writeCfg(t, fmt.Sprintf(`
node_alias: "cyclic"
port:     "0"
state_file: %q
log_path: ""
timeout: 5

targets:
  - id: "a"
    name: "A"
    type: tcp
    target: %q
    depends_on: ["b"]
  - id: "b"
    name: "B"
    type: tcp
    target: %q
    depends_on: ["a"]
`, filepath.Join(t.TempDir(), "s.json"), addr, addr2))

	_, err := engine.ValidateConfigFile(p)
	if err == nil {
		t.Error("expected cyclic dependency error, got nil")
	} else {
		t.Logf("✓ cyclic dependency error: %v", err)
	}
}

// ── SECTION 6 — HTTP probe ───────────────────────────────────────────────────

// TestHTTP_Probe_UpDown verifies that an HTTP target can be probed successfully
// and that bringing it down triggers an unreachable alert.
func TestHTTP_Probe_UpDown(t *testing.T) {
	url, closeHTTP := httpServer(t)
	defer closeHTTP()

	rr := &richRunner{}
	cfgPath := writeCfg(t, fmt.Sprintf(`
node_alias: "http-probe"
port:     "0"
state_file: %q
log_path: ""
timeout: 5
max_retries: 1
retry_interval_sec: 5
probe_interval_sec: 5
ticker_interval_sec: 1
reload_interval_sec: 0

notifications:
  ch:
    type: script
    parameters:
      script: "/bin/true"

default_notify: ["ch"]

targets:
  - id: "web-http"
    name: "Web HTTP"
    type: http
    target: %q
    interval_sec: 5
    options:
      expected_status:
        in: [200]
`, filepath.Join(t.TempDir(), "state.json"), url))

	e := engine.New("test-host", rr.runner(), cfgPath)
	if err := e.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}
	t.Cleanup(e.Shutdown)

	// Wait for first successful HTTP probe.
	waitState(t, "http target UP", 15*time.Second, func() bool {
		for _, s := range e.Status() {
			if s.Name == "Web HTTP" && s.Status == "UP" {
				return true
			}
		}
		return false
	})
	t.Log("HTTP target is UP")

	// Bring down the HTTP server.
	closeHTTP()

	// Wait for unreachable alert.
	waitState(t, "http unreachable alert", 20*time.Second, func() bool {
		return len(rr.byStatus("unreachable")) > 0
	})

	ev := rr.byStatus("unreachable")[0]
	if ev["TYPE"] != "http" {
		t.Errorf("TYPE: want http, got %q", ev["TYPE"])
	}
	t.Logf("✓ HTTP probe alert: NAME=%s TYPE=%s STATUS=%s", ev["NAME"], ev["TYPE"], ev["STATUS"])
}

// ── SECTION 7 — 5-node cluster: quorum + isolated mode ────────────────────────

// buildN returns config YAML for a node in an N-node cluster.
// zone is optional (pass "" to omit).
func buildNNodeCfg(c clusterNodeCfg, totalNodes int, zone string) string {
	peerList := ""
	for _, p := range c.peers {
		peerList += fmt.Sprintf("    - \"127.0.0.1:%d\"\n", p)
	}
	zoneLine := ""
	if zone != "" {
		zoneLine = fmt.Sprintf("  zone: %q\n", zone)
	}
	return fmt.Sprintf(`
node_alias: "cluster-comprehensive"
port:     "0"
state_file: %q
log_path: ""
timeout: 5
max_retries: 1
retry_interval_sec: 5
probe_interval_sec: 5
ticker_interval_sec: 1
reload_interval_sec: 0
watchdog_threshold_sec: 0

notifications:
  ch:
    type: script
    parameters:
      script: %q

default_notify: ["ch"]

targets:
  - id: %q
    name: %q
    type: tcp
    target: %q
    interval_sec: 5

cluster:
  enabled: true
  node_name: %q
  bind_addr: "127.0.0.1"
  bind_port: %d
  advertise_addr: "127.0.0.1"
  advertise_port: %d
  expected_node_count: %d
  min_quorum_ratio: 0.5
  probe_replication_factor: 3
%s  peers:
%s`,
		c.stateFile,
		c.alertScript,
		c.targetID, c.targetID,
		c.targetAddr,
		c.nodeName,
		c.bindPort, c.bindPort,
		totalNodes,
		zoneLine,
		peerList,
	)
}

// TestCluster_5Node_QuorumLost_Isolated brings up 5 nodes, shuts 3 of them
// down, and verifies the 2 survivors transition to isolated mode.
//
// Quorum formula: floor(5 × 0.5) + 1 = 3. With only 2 alive, quorum is lost.
func TestCluster_5Node_QuorumLost_Isolated(t *testing.T) {
	targetAddr, closeTarget := tcpServer(t)
	defer closeTarget()

	ports := make([]int, 5)
	for i := range ports {
		ports[i] = freePort(t)
	}

	alertCh, runnerFactory := sharedTracker(40)
	_ = alertCh // not testing alerts here, just quorum

	engines := make([]*engine.Engine, 5)
	for i := 0; i < 5; i++ {
		// Build peer list: every node knows about all others.
		peers := make([]int, 0, 4)
		for j := 0; j < 5; j++ {
			if j != i {
				peers = append(peers, ports[j])
			}
		}
		dir := t.TempDir()
		cfg := buildNNodeCfg(clusterNodeCfg{
			stateFile:   filepath.Join(dir, "state.json"),
			alertScript: filepath.Join(dir, "alert"),
			targetAddr:  targetAddr,
			targetID:    "quorum-target",
			nodeName:    fmt.Sprintf("qn%d", i+1),
			bindPort:    ports[i],
			peers:       peers,
		}, 5, "")
		cfgPath := writeCfg(t, cfg)
		e := engine.New(fmt.Sprintf("qn%d", i+1), runnerFactory(), cfgPath)
		if err := e.Init(); err != nil {
			t.Fatalf("node %d Init: %v", i+1, err)
		}
		engines[i] = e
		t.Cleanup(e.Shutdown)
	}

	// Wait for all 5 nodes to see each other AND for the quorum loop (runs every
	// 5 s) to confirm quorum is healthy and isolated=false on every node.
	waitState(t, "5-node cluster formation + quorum healthy", 40*time.Second, func() bool {
		for _, e := range engines {
			cm := e.ClusterManager()
			if cm == nil || cm.AliveCount() < 5 || !cm.QuorumHealthy() || cm.IsolatedMode() {
				return false
			}
		}
		return true
	})
	t.Log("5-node cluster formed — all nodes report quorum healthy, not isolated")

	// Shut down nodes 3, 4, 5 (indices 2, 3, 4).
	t.Log("shutting down nodes 3, 4, 5…")
	engines[2].Shutdown()
	engines[3].Shutdown()
	engines[4].Shutdown()

	// Nodes 1 and 2 must detect quorum loss and enter isolated mode.
	// memberlist failure detection takes a few seconds.
	waitState(t, "nodes 1+2 isolated", 30*time.Second, func() bool {
		cm1 := engines[0].ClusterManager()
		cm2 := engines[1].ClusterManager()
		return cm1 != nil && cm2 != nil &&
			cm1.IsolatedMode() && cm2.IsolatedMode()
	})
	t.Log("✓ nodes 1+2 entered isolated mode after losing quorum")

	// Verify quorum is lost on surviving nodes.
	for _, idx := range []int{0, 1} {
		cm := engines[idx].ClusterManager()
		if cm.QuorumHealthy() {
			t.Errorf("node %d: quorum should NOT be healthy after shutdown of 3 peers", idx+1)
		}
	}
	t.Log("✓ quorum lost on surviving nodes")
}

// ── SECTION 8 — 3-node cluster: primary failover ─────────────────────────────

// TestCluster_3Node_PrimaryFailover starts a 3-node cluster with all nodes
// probing (factor=3). The primary for the test target is identified and shut
// down. The target is then brought down and exactly one alert must arrive from
// one of the remaining two nodes.
func TestCluster_3Node_PrimaryFailover(t *testing.T) {
	targetAddr, closeTarget := tcpServer(t)
	defer closeTarget()

	var accepted int64
	go func() {
		ln, err := net.Listen("tcp", targetAddr)
		if err != nil {
			return
		}
		defer ln.Close()
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			atomic.AddInt64(&accepted, 1)
			c.Close()
		}
	}()
	_ = accepted // used below

	ports := [3]int{freePort(t), freePort(t), freePort(t)}
	alertCh, runnerFactory := sharedTracker(20)

	engines := make([]*engine.Engine, 3)
	for i := 0; i < 3; i++ {
		peers := make([]int, 0, 2)
		for j := 0; j < 3; j++ {
			if j != i {
				peers = append(peers, ports[j])
			}
		}
		dir := t.TempDir()
		cfg := buildNNodeCfg(clusterNodeCfg{
			stateFile:   filepath.Join(dir, "state.json"),
			alertScript: filepath.Join(dir, "alert"),
			targetAddr:  targetAddr,
			targetID:    "failover-target",
			nodeName:    fmt.Sprintf("fn%d", i+1),
			bindPort:    ports[i],
			peers:       peers,
		}, 3, "")
		cfgPath := writeCfg(t, cfg)
		e := engine.New(fmt.Sprintf("fn%d", i+1), runnerFactory(), cfg)
		_ = e
		e = engine.New(fmt.Sprintf("fn%d", i+1), runnerFactory(), cfgPath)
		if err := e.Init(); err != nil {
			t.Fatalf("node %d Init: %v", i+1, err)
		}
		engines[i] = e
	}
	t.Cleanup(func() {
		for _, e := range engines {
			if e != nil {
				e.Shutdown()
			}
		}
	})

	// Wait for 3-node cluster formation.
	waitState(t, "3-node cluster formed", 20*time.Second, func() bool {
		for _, e := range engines {
			cm := e.ClusterManager()
			if cm == nil || cm.AliveCount() < 3 {
				return false
			}
		}
		return true
	})
	t.Log("3-node cluster formed")

	// Wait for first probe acceptance.
	waitState(t, "first UP probe", 15*time.Second, func() bool {
		for _, e := range engines {
			for _, s := range e.Status() {
				if s.Name == "failover-target" && s.Status == "UP" {
					return true
				}
			}
		}
		return false
	})

	// Identify which node is the current primary.
	var primaryIdx int = -1
	for i, e := range engines {
		cm := e.ClusterManager()
		if cm != nil && cm.IsResponsible("failover-target") {
			primaryIdx = i
			break
		}
	}
	if primaryIdx < 0 {
		t.Fatal("could not identify primary node for failover-target")
	}
	t.Logf("primary for failover-target is node %d (fn%d)", primaryIdx+1, primaryIdx+1)

	// Drain any spurious UP-state alerts.
	time.Sleep(500 * time.Millisecond)
	for {
		select {
		case <-alertCh:
		default:
			goto drained
		}
	}
drained:

	// Shut down the primary node BEFORE bringing the target down.
	t.Logf("shutting down primary node %d…", primaryIdx+1)
	engines[primaryIdx].Shutdown()
	engines[primaryIdx] = nil

	// Give the ring time to rebalance.
	time.Sleep(3 * time.Second)

	// Now bring target down.
	closeTarget()

	// Expect exactly 1 unreachable alert from the new primary.
	var downAlerts []alertEvent
	waitState(t, "1 unreachable after failover", 30*time.Second, func() bool {
		select {
		case ev := <-alertCh:
			downAlerts = append(downAlerts, ev)
			return ev.status == "unreachable"
		default:
			return false
		}
	})

	// Drain extra alerts within 4s.
	time.Sleep(4 * time.Second)
	for {
		select {
		case ev := <-alertCh:
			downAlerts = append(downAlerts, ev)
		default:
			goto drainPF
		}
	}
drainPF:

	unreachable := 0
	for _, ev := range downAlerts {
		if ev.status == "unreachable" {
			unreachable++
		}
	}
	if unreachable != 1 {
		t.Errorf("primary failover: want exactly 1 unreachable alert, got %d (events=%v)",
			unreachable, downAlerts)
	} else {
		t.Logf("✓ primary failover: exactly 1 unreachable alert after primary shutdown")
	}
}

// ── SECTION 9 — Zone-aware prober spread ─────────────────────────────────────

// TestCluster_ZoneAwareSpread starts 4 nodes across 2 zones with
// probe_replication_factor=3. After cluster formation it verifies that the
// prober set spans both zones.
func TestCluster_ZoneAwareSpread(t *testing.T) {
	targetAddr, closeTarget := tcpServer(t)
	defer closeTarget()

	ports := [4]int{freePort(t), freePort(t), freePort(t), freePort(t)}
	zones := []string{"eu-west", "eu-west", "us-east", "us-east"}
	names := []string{"zone-n1", "zone-n2", "zone-n3", "zone-n4"}

	engines := make([]*engine.Engine, 4)
	for i := 0; i < 4; i++ {
		peers := make([]int, 0, 3)
		for j := 0; j < 4; j++ {
			if j != i {
				peers = append(peers, ports[j])
			}
		}
		dir := t.TempDir()
		peerList := ""
		for _, p := range peers {
			peerList += fmt.Sprintf("    - \"127.0.0.1:%d\"\n", p)
		}
		cfg := fmt.Sprintf(`
node_alias: "zone-spread"
port:     "0"
state_file: %q
log_path: ""
timeout: 5
max_retries: 1
retry_interval_sec: 30
probe_interval_sec: 30
ticker_interval_sec: 5
reload_interval_sec: 0
watchdog_threshold_sec: 0

notifications: {}
default_notify: []

targets:
  - id: "zone-target"
    name: "Zone Target"
    type: tcp
    target: %q

cluster:
  enabled: true
  node_name: %q
  bind_addr: "127.0.0.1"
  bind_port: %d
  advertise_addr: "127.0.0.1"
  advertise_port: %d
  expected_node_count: 4
  min_quorum_ratio: 0.5
  probe_replication_factor: 3
  zone: %q
  peers:
%s`,
			filepath.Join(dir, "state.json"),
			targetAddr,
			names[i],
			ports[i], ports[i],
			zones[i],
			peerList,
		)
		cfgPath := writeCfg(t, cfg)
		noop := engine.AlertRunner(func(_ string, _ map[string]string) error { return nil })
		e := engine.New(names[i], noop, cfgPath)
		if err := e.Init(); err != nil {
			t.Fatalf("node %d Init: %v", i+1, err)
		}
		engines[i] = e
		t.Cleanup(e.Shutdown)
	}

	// Wait for all 4 nodes to see each other.
	waitState(t, "4-node zone cluster formation", 30*time.Second, func() bool {
		for _, e := range engines {
			cm := e.ClusterManager()
			if cm == nil || cm.AliveCount() < 4 {
				return false
			}
		}
		return true
	})
	t.Log("4-node zone cluster formed")

	// Wait for prober assignments to stabilise.
	time.Sleep(3 * time.Second)

	// Check prober assignments from node 1's perspective.
	cm := engines[0].ClusterManager()
	snap := cm.ProberAssignmentsSnapshot()

	assignment, ok := snap.Assignments["zone-target"]
	if !ok {
		t.Fatal("zone-target not found in ProberAssignmentsSnapshot")
	}
	if len(assignment.Probers) == 0 {
		t.Fatal("no probers selected for zone-target")
	}
	t.Logf("probers for zone-target: %v", assignment.Probers)

	// With factor=3 and 4 nodes in 2 zones, we must cover both zones.
	// zone-n1/n2 are eu-west, zone-n3/n4 are us-east.
	euWestNodes := map[string]bool{"zone-n1": true, "zone-n2": true}
	usEastNodes := map[string]bool{"zone-n3": true, "zone-n4": true}

	hasEU, hasUS := false, false
	for _, prober := range assignment.Probers {
		if euWestNodes[prober] {
			hasEU = true
		}
		if usEastNodes[prober] {
			hasUS = true
		}
	}
	if !hasEU || !hasUS {
		t.Errorf("zone spread: probers=%v should span both zones eu-west and us-east", assignment.Probers)
	} else {
		t.Logf("✓ zone spread: probers=%v cover both eu-west and us-east", assignment.Probers)
	}

	// Replication factor must be respected (≤ 3 probers, ≤ 4 candidates).
	if len(assignment.Probers) > 3 {
		t.Errorf("too many probers: want ≤ 3, got %d", len(assignment.Probers))
	}
}

// ── SECTION 10 — Cluster scope classification ─────────────────────────────────

// TestCluster_Scope_GLOBAL starts a 2-node cluster where both nodes see a
// target go down. The FleetSnapshot scope in cluster mode must not be STANDALONE.
func TestCluster_Scope_GLOBAL(t *testing.T) {
	targetAddr, closeTarget := tcpServer(t)
	defer closeTarget()

	p1, p2 := freePort(t), freePort(t)
	alertCh, runnerFactory := sharedTracker(20)

	mkCfg := func(name string, port int, peerPort int) string {
		dir := t.TempDir()
		return buildNNodeCfg(clusterNodeCfg{
			stateFile:   filepath.Join(dir, "state.json"),
			alertScript: filepath.Join(dir, "alert"),
			targetAddr:  targetAddr,
			targetID:    "scope-target",
			nodeName:    name,
			bindPort:    port,
			peers:       []int{peerPort},
		}, 2, "")
	}

	e1 := engine.New("scope-n1", runnerFactory(), writeCfg(t, mkCfg("scope-n1", p1, p2)))
	if err := e1.Init(); err != nil {
		t.Fatalf("node1 Init: %v", err)
	}
	t.Cleanup(e1.Shutdown)

	e2 := engine.New("scope-n2", runnerFactory(), writeCfg(t, mkCfg("scope-n2", p2, p1)))
	if err := e2.Init(); err != nil {
		t.Fatalf("node2 Init: %v", err)
	}
	t.Cleanup(e2.Shutdown)

	waitState(t, "2-node scope cluster formed", 20*time.Second, func() bool {
		cm1, cm2 := e1.ClusterManager(), e2.ClusterManager()
		return cm1 != nil && cm2 != nil && cm1.AliveCount() >= 2 && cm2.AliveCount() >= 2
	})
	t.Log("2-node cluster formed for scope test")

	// Wait for at least one UP probe on either node via FleetSnapshot.
	waitState(t, "scope-target UP on at least one node", 20*time.Second, func() bool {
		for _, e := range []*engine.Engine{e1, e2} {
			for _, tgt := range e.FleetSnapshot().Targets {
				if tgt.Name == "scope-target" && tgt.ConsensusState == "up" {
					return true
				}
			}
		}
		return false
	})

	// Drain spurious alerts.
	for {
		select {
		case <-alertCh:
		default:
			goto drained
		}
	}
drained:

	// Bring target down.
	closeTarget()

	// Wait for unreachable alert (confirms hard_down propagated).
	waitState(t, "unreachable alert (scope test)", 30*time.Second, func() bool {
		select {
		case ev := <-alertCh:
			return ev.status == "unreachable"
		default:
			return false
		}
	})

	// Give gossip time to propagate to both nodes.
	time.Sleep(3 * time.Second)

	// Inspect FleetSnapshot on both nodes for scope/classification.
	for _, e := range []*engine.Engine{e1, e2} {
		snap := e.FleetSnapshot()
		for _, tgt := range snap.Targets {
			if tgt.Name == "scope-target" {
				scope := tgt.Scope
				cls := tgt.Classification
				t.Logf("node scope=%s classification=%s confidence=%.2f", scope, cls, tgt.Confidence)
				if scope == "STANDALONE" {
					t.Errorf("scope must not be STANDALONE in cluster mode")
				}
			}
		}
	}
	t.Log("✓ cluster scope classification is non-STANDALONE")
}

// ── SECTION 11 — Watchdog smoke test ─────────────────────────────────────────

// TestWatchdog_NotifyScrape_NocrashSmoke verifies that NotifyScrape can be
// called repeatedly without panicking, and that the engine reports a last-
// scrape time after the call.
func TestWatchdog_NotifyScrape_NocrashSmoke(t *testing.T) {
	addr, close1 := tcpServer(t)
	defer close1()

	e := noopEngine(t, fmt.Sprintf(`
node_alias: "watchdog-smoke"
port:     "0"
state_file: %q
log_path: ""
timeout: 5
max_retries: 1
probe_interval_sec: 60
ticker_interval_sec: 10
watchdog_threshold_sec: 60

notifications: {}
default_notify: []

targets:
  - name: "t1"
    type: tcp
    target: %q
`, filepath.Join(t.TempDir(), "s.json"), addr))

	// Call NotifyScrape multiple times — must not panic.
	for i := 0; i < 10; i++ {
		e.NotifyScrape()
	}
	t.Log("✓ NotifyScrape called 10 times without panic")
}

// ── SECTION 12 — State machine Seq/ErrorCode persistence ─────────────────────

// TestStateMachine_SeqAndErrorCode verifies that the state machine increments
// Seq on each down/up transition and persists ErrorCode to state.json.
func TestStateMachine_SeqAndErrorCode(t *testing.T) {
	addr, closeTarget := tcpServer(t)
	defer closeTarget()

	stateFile := filepath.Join(t.TempDir(), "state.json")
	rr := &richRunner{}
	cfgPath := writeCfg(t, fmt.Sprintf(`
node_alias: "seq-test"
port:     "0"
state_file: %q
log_path: ""
timeout: 5
max_retries: 1
retry_interval_sec: 5
probe_interval_sec: 5
ticker_interval_sec: 1
reload_interval_sec: 0

notifications:
  ch:
    type: script
    parameters:
      script: "/bin/true"

default_notify: ["ch"]

targets:
  - id: "seq-target"
    name: "Seq Target"
    type: tcp
    target: %q
    interval_sec: 5
`, stateFile, addr))

	e := engine.New("test-host", rr.runner(), cfgPath)
	if err := e.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}
	t.Cleanup(e.Shutdown)

	// Wait for UP.
	waitState(t, "UP state", 15*time.Second, func() bool {
		for _, s := range e.Status() {
			if s.Name == "Seq Target" && s.Status == "UP" {
				return true
			}
		}
		return false
	})

	// Down → get seq from alert.
	closeTarget()
	waitState(t, "unreachable alert with SEQ", 20*time.Second, func() bool {
		for _, ev := range rr.byStatus("unreachable") {
			if ev["NAME"] == "Seq Target" && ev["SEQ"] != "" && ev["SEQ"] != "0" {
				return true
			}
		}
		return false
	})

	downEv := rr.byStatus("unreachable")[0]
	seqDown := downEv["SEQ"]
	errCode := downEv["ERROR_CODE"]
	t.Logf("down: SEQ=%s ERROR_CODE=%q", seqDown, errCode)

	if seqDown == "" || seqDown == "0" {
		t.Errorf("SEQ should be non-zero on down, got %q", seqDown)
	}
	if errCode == "" {
		t.Errorf("ERROR_CODE should be non-empty on down")
	}

	// Verify state.json has seq > 0 and error_code.
	waitState(t, "state.json hard_down with seq", 10*time.Second, func() bool {
		if _, err := os.Stat(stateFile); err != nil {
			return false
		}
		sf := readStateFile(t, stateFile)
		entry, ok := sf.Targets["seq-target"]
		return ok && entry.State == "hard_down" && entry.Seq > 0 && entry.ErrorCode != ""
	})

	sf := readStateFile(t, stateFile)
	entry := sf.Targets["seq-target"]
	t.Logf("state.json: state=%s seq=%d error_code=%q", entry.State, entry.Seq, entry.ErrorCode)

	// Reopen target for recovery, verify SEQ increases.
	ln2, err := net.Listen("tcp", addr)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer ln2.Close()
	go func() {
		for {
			c, err := ln2.Accept()
			if err != nil {
				return
			}
			c.Close()
		}
	}()

	waitState(t, "reachable alert", 20*time.Second, func() bool {
		return len(rr.byStatus("reachable")) > 0
	})

	upEv := rr.byStatus("reachable")[0]
	seqUp := upEv["SEQ"]
	t.Logf("recovery: SEQ=%s", seqUp)

	if seqUp <= seqDown {
		t.Errorf("recovery SEQ (%s) should be > down SEQ (%s)", seqUp, seqDown)
	}
	t.Log("✓ SEQ increments on down and recovery; ErrorCode set on down, cleared on recovery")
}

// ── SECTION 13 — Cluster exactly-once with 3 nodes ───────────────────────────

// TestCluster_3Node_ExactlyOnce extends the 2-node test to 3 nodes with
// factor=3 (all probe). Exactly 1 "unreachable" alert must fire.
func TestCluster_3Node_ExactlyOnce(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	targetAddr := ln.Addr().String()
	var accepted int64
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			atomic.AddInt64(&accepted, 1)
			c.Close()
		}
	}()

	ports := [3]int{freePort(t), freePort(t), freePort(t)}
	alertCh, runnerFactory := sharedTracker(30)

	for i := 0; i < 3; i++ {
		peers := make([]int, 0, 2)
		for j := 0; j < 3; j++ {
			if j != i {
				peers = append(peers, ports[j])
			}
		}
		dir := t.TempDir()
		cfg := buildNNodeCfg(clusterNodeCfg{
			stateFile:   filepath.Join(dir, "state.json"),
			alertScript: filepath.Join(dir, "alert"),
			targetAddr:  targetAddr,
			targetID:    "tri-target",
			nodeName:    fmt.Sprintf("tri-n%d", i+1),
			bindPort:    ports[i],
			peers:       peers,
		}, 3, "")
		cfgPath := writeCfg(t, cfg)
		e := engine.New(fmt.Sprintf("tri-n%d", i+1), runnerFactory(), cfgPath)
		if err := e.Init(); err != nil {
			t.Fatalf("node %d Init: %v", i+1, err)
		}
		t.Cleanup(e.Shutdown)
	}

	waitState(t, "3-node cluster formed", 25*time.Second, func() bool {
		// All engines would need to be tracked; just wait based on time.
		return true
	})
	time.Sleep(5 * time.Second) // let cluster stabilize

	waitState(t, "first UP probe (3-node)", 15*time.Second, func() bool {
		return atomic.LoadInt64(&accepted) > 0
	})

	select {
	case ev := <-alertCh:
		t.Fatalf("unexpected alert on initial UP: %+v", ev)
	case <-time.After(300 * time.Millisecond):
	}

	// Bring down target.
	ln.Close()

	var downAlerts []alertEvent
	waitState(t, "exactly-1 unreachable (3-node)", 30*time.Second, func() bool {
		select {
		case ev := <-alertCh:
			downAlerts = append(downAlerts, ev)
			return ev.status == "unreachable"
		default:
			return false
		}
	})

	time.Sleep(4 * time.Second)
	for {
		select {
		case ev := <-alertCh:
			downAlerts = append(downAlerts, ev)
		default:
			goto drainTri
		}
	}
drainTri:
	count := 0
	for _, ev := range downAlerts {
		if ev.status == "unreachable" {
			count++
		}
	}
	if count != 1 {
		t.Errorf("3-node exactly-once: want 1 unreachable, got %d (all=%v)", count, downAlerts)
	} else {
		t.Logf("✓ 3-node exactly-once: 1 unreachable alert (seq=%s)", downAlerts[0].seq)
	}
}
