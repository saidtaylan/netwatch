//go:build !windows

// tests/domain/crossnode_rootcause_test.go — verifies that ROOT_CAUSE resolution
// works correctly when prober sets are disjoint across cluster nodes.
//
// Scenario:
//   - 4 node cluster, probe_replication_factor=1 (each target gets 1 prober)
//   - target-db: probed by node-A only
//   - target-api: probed by node-B only  (depends_on: target-db)
//   - Both services go down simultaneously
//   - ROOT_CAUSE in the api-gateway alert must be "target-db"
//
// This test exercises the AllPeerStates() merge in notify.go which cross-pollinates
// peer-observed states into the local allStates map before root-cause detection.
package domain_test

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/saidtaylan/netwatch/internal/cluster"
	"github.com/saidtaylan/netwatch/internal/engine"
)

// makeClusterNode creates a started engine for a cluster test node.
func makeClusterNode(t *testing.T, cfg string, runner engine.AlertRunner) (*engine.Engine, func()) {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(p, []byte(cfg), 0644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	h, _ := os.Hostname()
	e := engine.New(h, runner, p)
	if err := e.Init(); err != nil {
		t.Fatalf("engine.Init: %v", err)
	}
	stop := func() { e.Shutdown() }
	return e, stop
}

// listenTCP starts a TCP server that accepts and immediately closes connections.
func listenTCP(t *testing.T) (addr string, close func()) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
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
	return ln.Addr().String(), func() { ln.Close() }
}

func TestCrossNode_RootCause_DisjointProbers(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping cluster test in short mode")
	}

	// Generate a fresh keyring for this test.
	keyring, err := engine.GenerateKeyringKey()
	if err != nil {
		t.Fatalf("keyring: %v", err)
	}

	// Start mock services (both UP initially).
	dbAddr, closeDB := listenTCP(t)
	apiAddr, closeAPI := listenTCP(t)
	defer closeDB()
	defer closeAPI()

	// Shared config pieces.
	const (
		basePort    = 23800
		gossipBase  = 23900
		nodeCount   = 4
		factor      = 1  // each target gets exactly 1 prober → disjoint sets guaranteed
	)

	// Alert capture — shared across all nodes.
	type capture struct {
		name      string
		status    string
		rootCause string
		env       map[string]string
	}
	alertCh := make(chan capture, 32)
	makeRunner := func() engine.AlertRunner {
		return func(_ string, env map[string]string) error {
			alertCh <- capture{
				name:      env["NAME"],
				status:    env["STATUS"],
				rootCause: env["ROOT_CAUSE"],
				env:       env,
			}
			return nil
		}
	}

	// target-db depends_on nothing; target-api depends_on target-db.
	sharedTargets := fmt.Sprintf(`
targets:
  - id: "target-db"
    name: "Database"
    type: tcp
    target: %q
    interval_sec: 5
  - id: "target-api"
    name: "API Gateway"
    type: tcp
    target: %q
    interval_sec: 5
    depends_on: ["target-db"]
`, dbAddr, apiAddr)

	peers := fmt.Sprintf(`
  peers:
    - "127.0.0.1:%d"
`, gossipBase+1)

	nodes := make([]*engine.Engine, nodeCount)
	stops := make([]func(), nodeCount)

	for i := 0; i < nodeCount; i++ {
		httpPort := basePort + i + 1
		gossipPort := gossipBase + i + 1
		nodeNum := i + 1
		cfg := fmt.Sprintf(`
port: "%d"
state_file: %q
log_path: ""
timeout: 2
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
default_notify: ["ops"]
%s
cluster:
  enabled: true
  node_name: "n%d"
  bind_addr: "127.0.0.1"
  bind_port: %d
  advertise_addr: "127.0.0.1"
  advertise_port: %d
  keyring: [%q]
  expected_node_count: %d
  min_quorum_ratio: 0.5
  probe_replication_factor: %d
%s`,
			httpPort,
			filepath.Join(t.TempDir(), "state.json"),
			sharedTargets,
			nodeNum,
			gossipPort,
			gossipPort,
			keyring,
			nodeCount,
			factor,
			peers,
		)

		nodes[i], stops[i] = makeClusterNode(t, cfg, makeRunner())
	}
	defer func() {
		for _, s := range stops {
			s()
		}
	}()

	// Wait for cluster to form (all 4 members).
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		if nodes[0].ClusterMemberCount() >= nodeCount {
			break
		}
		time.Sleep(500 * time.Millisecond)
	}
	if nodes[0].ClusterMemberCount() < nodeCount {
		t.Logf("cluster only %d/%d members after 30s — continuing anyway", nodes[0].ClusterMemberCount(), nodeCount)
	}

	// Wait for assignments to stabilize (~probe_replication_factor * (probe_interval + buffer)).
	time.Sleep(10 * time.Second)

	// Identify which node probes which target.
	dbProber := -1
	apiProber := -1
	for i, e := range nodes {
		if e.ClusterManager() != nil {
			if e.ClusterManager().IsLocalProber("target-db") {
				dbProber = i
			}
			if e.ClusterManager().IsLocalProber("target-api") {
				apiProber = i
			}
		}
	}
	t.Logf("target-db prober: n%d, target-api prober: n%d", dbProber+1, apiProber+1)

	if dbProber == apiProber || dbProber == -1 || apiProber == -1 {
		t.Skipf("probers not disjoint (db=%d api=%d) — cannot test cross-node scenario with this ring assignment", dbProber, apiProber)
	}

	// Kill both services simultaneously.
	closeDB()
	closeAPI()
	t.Log("both services killed — waiting for alerts")

	// Collect alerts for up to 45 seconds. We need the api-gateway alert.
	var apiAlert *capture
	timeout := time.After(45 * time.Second)
outer:
	for {
		select {
		case a := <-alertCh:
			t.Logf("got alert: name=%s status=%s root_cause=%s", a.name, a.status, a.rootCause)
			if a.name == "API Gateway" && a.status == "unreachable" {
				apiAlert = &a
				break outer
			}
		case <-timeout:
			break outer
		}
	}

	if apiAlert == nil {
		t.Fatal("no unreachable alert for API Gateway within 45s")
	}

	// ROOT_CAUSE must be target-db, not target-api itself.
	// The merge via AllPeerStates() in notify.go should have provided db's hard_down state
	// even though it was probed by a different node.
	if apiAlert.rootCause == "" {
		t.Error("ROOT_CAUSE is empty — cross-node lookup not working")
	} else if apiAlert.rootCause != "target-db" {
		t.Errorf("ROOT_CAUSE: got %q, want target-db (cross-node resolution failed)", apiAlert.rootCause)
	} else {
		t.Logf("✓ ROOT_CAUSE=%s correctly resolved across disjoint prober sets", apiAlert.rootCause)
	}

	// DEPENDENCY_DEPTH should be 1 (api-gateway → db).
	if depth, ok := apiAlert.env["DEPENDENCY_DEPTH"]; ok && depth != "1" {
		t.Errorf("DEPENDENCY_DEPTH: got %q, want 1", depth)
	}
}

// TestCrossNode_RootCause_LocalProberStillWorks verifies standalone (no cluster)
// and same-node prober scenarios remain unaffected.
func TestCrossNode_RootCause_LocalProberStillWorks(t *testing.T) {
	dbAddr, closeDB := listenTCP(t)
	defer closeDB()
	apiAddr, closeAPI := listenTCP(t)
	defer closeAPI()

	type capture struct{ name, status, rootCause string }
	alertCh := make(chan capture, 8)
	runner := func(_ string, env map[string]string) error {
		alertCh <- capture{env["NAME"], env["STATUS"], env["ROOT_CAUSE"]}
		return nil
	}

	cfg := fmt.Sprintf(`
port: "0"
state_file: %q
log_path: ""
timeout: 2
max_retries: 1
retry_interval_sec: 5
probe_interval_sec: 5
ticker_interval_sec: 1
reload_interval_sec: 0
notifications:
  ops: {type: script, parameters: {script: "/bin/true"}}
default_notify: ["ops"]
targets:
  - id: "local-db"
    name: "Local DB"
    type: tcp
    target: %q
    interval_sec: 5
  - id: "local-api"
    name: "Local API"
    type: tcp
    target: %q
    interval_sec: 5
    depends_on: ["local-db"]
`, filepath.Join(t.TempDir(), "state.json"), dbAddr, apiAddr)

	dir := t.TempDir()
	p := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(p, []byte(cfg), 0644); err != nil {
		t.Fatalf("write: %v", err)
	}
	h, _ := os.Hostname()
	e := engine.New(h, runner, p)
	if err := e.Init(); err != nil {
		t.Fatalf("init: %v", err)
	}
	defer e.Shutdown()

	// Kill both.
	closeDB()
	closeAPI()

	var apiAlert *capture
	timeout := time.After(25 * time.Second)
outer:
	for {
		select {
		case a := <-alertCh:
			if a.name == "Local API" && a.status == "unreachable" {
				apiAlert = &a
				break outer
			}
		case <-timeout:
			break outer
		}
	}

	if apiAlert == nil {
		t.Fatal("no alert for Local API")
	}
	if apiAlert.rootCause != "local-db" {
		t.Errorf("ROOT_CAUSE: got %q, want local-db", apiAlert.rootCause)
	}

	// Suppress unused import warning for cluster package.
	_ = cluster.ConfigHashOf(nil)
}
