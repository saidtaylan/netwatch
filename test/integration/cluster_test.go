//go:build !windows

// Package integration_test — cluster integration tests.
//
// These tests spin up two in-process Engine instances with cluster.enabled=true
// and a real memberlist gossip layer on localhost. They verify:
//   - Exactly-once alerting: a target going hard_down triggers exactly 1 alert
//     even though both nodes probe it.
//   - Recovery propagation: after the target comes back up exactly 1 "reachable"
//     alert is dispatched.
package integration_test

import (
	"fmt"
	"net"
	"sync/atomic"
	"testing"
	"time"

	"github.com/saidtaylan/netwatch/internal/engine"
)

// ── port helpers ──────────────────────────────────────────────────────────────

// freePort asks the OS for a free TCP port, closes the listener, and returns
// the port number. The port may be reused by memberlist before the test starts,
// but in practice the window is small enough for local tests.
func freePort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("freePort: %v", err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	ln.Close()
	return port
}

// ── cluster config builder ────────────────────────────────────────────────────

type clusterNodeCfg struct {
	stateFile   string
	alertScript string
	targetAddr  string
	targetID    string // target id: in config
	nodeName    string // cluster node_name (must be unique per node)
	bindPort    int
	peers       []int
}

func buildClusterCfg(c clusterNodeCfg) string {
	peerList := ""
	for _, p := range c.peers {
		peerList += fmt.Sprintf("    - \"127.0.0.1:%d\"\n", p)
	}
	return fmt.Sprintf(`
app_name: "cluster-integration"
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
  expected_node_count: 2
  min_quorum_ratio: 0.5
  probe_replication_factor: 2
  peers:
%s`,
		c.stateFile,
		c.alertScript,
		c.targetID, c.targetID,
		c.targetAddr,
		c.nodeName,
		c.bindPort, c.bindPort,
		peerList,
	)
}

// ── shared alert channel helper ───────────────────────────────────────────────

// sharedTracker wraps multiple alertTrackers into one combined channel.
// All runners send to the same buffered channel so the test can assert
// total alert count without knowing which node fired.
func sharedTracker(buf int) (chan alertEvent, func() engine.AlertRunner) {
	ch := make(chan alertEvent, buf)
	factory := func() engine.AlertRunner {
		return func(scriptBase string, env map[string]string) error {
			ch <- alertEvent{
				scriptBase: scriptBase,
				status:     env["STATUS"],
				name:       env["NAME"],
				seq:        env["SEQ"],
			}
			return nil
		}
	}
	return ch, factory
}

// ── TestCluster_ExactlyOnceAlert ──────────────────────────────────────────────

// TestCluster_ExactlyOnceAlert verifies that two engines watching the same
// target only produce a single "unreachable" alert when the target goes down.
// Both nodes probe the target (probe_replication_factor=2), but the consistent-
// hash primary is responsible for dispatching the alert — the secondary suppresses.
func TestCluster_ExactlyOnceAlert(t *testing.T) {
	// ── mock target ──────────────────────────────────────────────────────────
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

	// ── free gossip ports ────────────────────────────────────────────────────
	p1 := freePort(t)
	p2 := freePort(t)

	// ── shared alert channel — both nodes send here ──────────────────────────
	alertCh, runnerFactory := sharedTracker(20)
	reg1 := newRegistry(t)
	_ = reg1

	// ── node 1 ──────────────────────────────────────────────────────────────
	sf1 := writeCfg(t, buildClusterCfg(clusterNodeCfg{
		stateFile:   t.TempDir() + "/state1.json",
		alertScript: t.TempDir() + "/alert1",
		targetAddr:  targetAddr,
		targetID:    "mock-tcp",
		nodeName:    "cluster-n1",
		bindPort:    p1,
		peers:       []int{p2},
	}))
	e1 := engine.New("cluster-n1", runnerFactory(), sf1)
	if err := e1.Init(); err != nil {
		t.Fatalf("node1 Init: %v", err)
	}
	t.Cleanup(e1.Shutdown)

	// ── node 2 ──────────────────────────────────────────────────────────────
	sf2 := writeCfg(t, buildClusterCfg(clusterNodeCfg{
		stateFile:   t.TempDir() + "/state2.json",
		alertScript: t.TempDir() + "/alert2",
		targetAddr:  targetAddr,
		targetID:    "mock-tcp",
		nodeName:    "cluster-n2",
		bindPort:    p2,
		peers:       []int{p1},
	}))
	e2 := engine.New("cluster-n2", runnerFactory(), sf2)
	if err := e2.Init(); err != nil {
		t.Fatalf("node2 Init: %v", err)
	}
	t.Cleanup(e2.Shutdown)

	// ── wait for cluster to form (both nodes see each other) ─────────────────
	waitState(t, "cluster formation: 2 members", 20*time.Second, func() bool {
		cm1 := e1.ClusterManager()
		cm2 := e2.ClusterManager()
		if cm1 == nil || cm2 == nil {
			return false
		}
		return cm1.AliveCount() >= 2 && cm2.AliveCount() >= 2
	})
	t.Log("cluster formed — 2 members visible on both nodes")

	// ── wait for first successful probe ──────────────────────────────────────
	waitState(t, "first UP probe", 15*time.Second, func() bool {
		return atomic.LoadInt64(&accepted) > 0
	})

	// Confirm no spurious alert on initial UP.
	select {
	case ev := <-alertCh:
		t.Fatalf("unexpected alert on initial UP: %+v", ev)
	case <-time.After(300 * time.Millisecond):
	}

	// ── bring target down ────────────────────────────────────────────────────
	ln.Close()

	// ── wait for exactly 1 "unreachable" alert ───────────────────────────────
	var downAlerts []alertEvent
	waitState(t, "unreachable alert (exactly 1)", 30*time.Second, func() bool {
		select {
		case ev := <-alertCh:
			downAlerts = append(downAlerts, ev)
			if ev.status == "unreachable" {
				return true
			}
		default:
		}
		return false
	})

	// Drain any extra alerts that arrive within 3 s.
	time.Sleep(3 * time.Second)
	for {
		select {
		case ev := <-alertCh:
			downAlerts = append(downAlerts, ev)
		default:
			goto drained
		}
	}
drained:

	unreachableCount := 0
	for _, ev := range downAlerts {
		if ev.status == "unreachable" {
			unreachableCount++
		}
	}
	if unreachableCount != 1 {
		t.Errorf("exactly-once: want 1 unreachable alert, got %d (all: %v)", unreachableCount, downAlerts)
	} else {
		t.Logf("✓ exactly-once: 1 unreachable alert received (seq=%s)", downAlerts[0].seq)
	}
}

// ── TestCluster_RecoveryAlert ─────────────────────────────────────────────────

// TestCluster_RecoveryAlert verifies that after a cluster-detected down target
// recovers, exactly one "reachable" alert is dispatched.
func TestCluster_RecoveryAlert(t *testing.T) {
	// ── mock target ──────────────────────────────────────────────────────────
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

	p1 := freePort(t)
	p2 := freePort(t)
	alertCh, runnerFactory := sharedTracker(20)

	sf1 := writeCfg(t, buildClusterCfg(clusterNodeCfg{
		stateFile:   t.TempDir() + "/state1.json",
		alertScript: t.TempDir() + "/alert1",
		targetAddr:  targetAddr,
		targetID:    "svc-recovery",
		nodeName:    "recovery-n1",
		bindPort:    p1,
		peers:       []int{p2},
	}))
	e1 := engine.New("recovery-n1", runnerFactory(), sf1)
	if err := e1.Init(); err != nil {
		t.Fatalf("node1 Init: %v", err)
	}
	t.Cleanup(e1.Shutdown)

	sf2 := writeCfg(t, buildClusterCfg(clusterNodeCfg{
		stateFile:   t.TempDir() + "/state2.json",
		alertScript: t.TempDir() + "/alert2",
		targetAddr:  targetAddr,
		targetID:    "svc-recovery",
		nodeName:    "recovery-n2",
		bindPort:    p2,
		peers:       []int{p1},
	}))
	e2 := engine.New("recovery-n2", runnerFactory(), sf2)
	if err := e2.Init(); err != nil {
		t.Fatalf("node2 Init: %v", err)
	}
	t.Cleanup(e2.Shutdown)

	waitState(t, "cluster formation", 20*time.Second, func() bool {
		cm1, cm2 := e1.ClusterManager(), e2.ClusterManager()
		return cm1 != nil && cm2 != nil && cm1.AliveCount() >= 2 && cm2.AliveCount() >= 2
	})

	// Wait for initial UP probe.
	waitState(t, "first UP probe", 15*time.Second, func() bool {
		return atomic.LoadInt64(&accepted) > 0
	})

	// Bring target down → wait for unreachable alert.
	ln.Close()
	waitState(t, "unreachable alert", 30*time.Second, func() bool {
		select {
		case ev := <-alertCh:
			return ev.status == "unreachable"
		default:
			return false
		}
	})
	t.Log("target down confirmed")

	// Bring target back up.
	ln2, err := net.Listen("tcp", targetAddr)
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

	// Wait for exactly 1 "reachable" alert.
	var upAlerts []alertEvent
	waitState(t, "reachable alert (exactly 1)", 30*time.Second, func() bool {
		select {
		case ev := <-alertCh:
			upAlerts = append(upAlerts, ev)
			return ev.status == "reachable"
		default:
			return false
		}
	})

	// Drain any extra recovery alerts within 3s.
	time.Sleep(3 * time.Second)
	for {
		select {
		case ev := <-alertCh:
			if ev.status == "reachable" {
				upAlerts = append(upAlerts, ev)
			}
		default:
			goto done
		}
	}
done:

	reachableCount := 0
	for _, ev := range upAlerts {
		if ev.status == "reachable" {
			reachableCount++
		}
	}
	if reachableCount != 1 {
		t.Errorf("recovery exactly-once: want 1 reachable alert, got %d", reachableCount)
	} else {
		t.Logf("✓ recovery exactly-once: 1 reachable alert received")
	}
}
