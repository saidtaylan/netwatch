//go:build !windows

// tests/domain/crossnode_rootcause_test.go — verifies that ROOT_CAUSE resolution
// works correctly when prober sets are disjoint across cluster nodes.
//
// Scenario:
//   - 4 node cluster, probe_replication_factor=1 (each target gets 1 prober)
//   - target-db:  pinned to n1 via probe_from (probed by n1 only)
//   - target-api: pinned to n2 via probe_from (probed by n2 only; depends_on target-db)
//   - target-db fails first; once its hard_down has propagated to n2, target-api fails
//   - ROOT_CAUSE in the api-gateway alert must be "target-db", proving n2 resolved
//     the cause from a peer-observed state it never probed itself.
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
	// probe_from pins make prober assignment deterministic so the disjoint-set
	// scenario is exercised on every run (without the pins the hash ring may put
	// both targets on the same node, which cannot test cross-node resolution).
	// All nodes share this config, satisfying the "same probe_from on every node"
	// contract.
	sharedTargets := fmt.Sprintf(`
targets:
  - id: "target-db"
    name: "Database"
    type: tcp
    target: %q
    interval_sec: 5
    probe_from: ["n1"]
  - id: "target-api"
    name: "API Gateway"
    type: tcp
    target: %q
    interval_sec: 5
    depends_on: ["target-db"]
    probe_from: ["n2"]
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

	// Wait for prober assignments to converge to the pinned layout: target-db on
	// n1 (index 0), target-api on n2 (index 1). probe_from makes this deterministic,
	// but the recompute is debounced after cluster membership settles, so poll.
	dbProber, apiProber := -1, -1
	assignDeadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(assignDeadline) {
		dbProber, apiProber = -1, -1
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
		if dbProber == 0 && apiProber == 1 {
			break
		}
		time.Sleep(500 * time.Millisecond)
	}
	t.Logf("target-db prober: n%d, target-api prober: n%d", dbProber+1, apiProber+1)

	if dbProber == -1 || apiProber == -1 || dbProber == apiProber {
		t.Fatalf("probe_from pins did not produce disjoint probers (db=%d api=%d) — assignment failed to converge", dbProber, apiProber)
	}

	// Causal-order outage: the upstream (target-db) fails first. We wait until its
	// hard_down state has actually propagated via gossip to the api prober (n2)
	// before failing the downstream (target-api). This is the realistic scenario
	// root-cause-across-disjoint-probers exists for: when the dependent service's
	// alert fires, the upstream failure is already known cluster-wide, so n2 can
	// attribute the api outage to target-db. (Killing both simultaneously is a
	// race the eventually-consistent gossip layer cannot resolve deterministically:
	// the first api alert may legitimately fire before db's state arrives.)
	closeDB()
	t.Log("target-db killed — waiting for hard_down to propagate to api prober (n2)")

	apiMgr := nodes[apiProber].ClusterManager()
	propDeadline := time.Now().Add(40 * time.Second)
	dbDownSeen := false
	for time.Now().Before(propDeadline) {
		for _, p := range apiMgr.AllPeerStates() {
			if p.TargetID == "target-db" && p.State == "hard_down" {
				dbDownSeen = true
				break
			}
		}
		if dbDownSeen {
			break
		}
		time.Sleep(500 * time.Millisecond)
	}
	if !dbDownSeen {
		t.Fatal("target-db hard_down did not propagate to api prober within 40s — cross-node gossip not converging")
	}
	t.Log("target-db hard_down observed on n2 — now killing target-api")
	closeAPI()

	// Collect alerts. We need the api-gateway unreachable alert.
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
