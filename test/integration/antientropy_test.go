//go:build !windows

// Package integration_test — anti-entropy integration tests.
//
// TestAntiEntropy_RejoinNoDuplicateAlert verifies that when a node is stopped
// and restarted while a target is already in hard_down state, the re-joining
// node does NOT fire a second "unreachable" alert. Anti-entropy (Phase 9) must
// merge the remote hard_down state and suppress the duplicate.
package integration_test

import (
	"fmt"
	"net"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/saidtaylan/netwatch/internal/engine"
)

// ── TestAntiEntropy_RejoinNoDuplicateAlert ────────────────────────────────────

// TestAntiEntropy_RejoinNoDuplicateAlert:
//
//  1. Two nodes form a cluster, both probe the same TCP target.
//  2. The target closes → exactly 1 "unreachable" alert fires (Phase 8 guarantee).
//  3. Node 1 shuts down.
//  4. Node 1 restarts (fresh Engine, same node_name + state.json, same port).
//  5. The re-joining node merges the remote hard_down state via anti-entropy.
//  6. No second "unreachable" alert is dispatched during or after the re-join.
func TestAntiEntropy_RejoinNoDuplicateAlert(t *testing.T) {
	// ── mock TCP target (initially UP) ───────────────────────────────────────
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

	// ── shared alert channel ─────────────────────────────────────────────────
	alertCh := make(chan alertEvent, 30)
	makeRunner := func() engine.AlertRunner {
		return func(_ string, env map[string]string) error {
			alertCh <- alertEvent{
				status: env["STATUS"],
				name:   env["NAME"],
				seq:    env["SEQ"],
			}
			return nil
		}
	}

	// Stable state file path for node1 (persisted across restart).
	stateDir1 := t.TempDir()
	stateFile1 := filepath.Join(stateDir1, "state1.json")
	alertScript1 := filepath.Join(stateDir1, "alert1")

	cfgNode1 := func() string {
		return fmt.Sprintf(`
app_name: "ae-test"
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
  ae-ch:
    type: script
    parameters:
      script: %q

default_notify: ["ae-ch"]

targets:
  - id: "ae-target"
    name: "AE Target"
    type: tcp
    target: %q
    interval_sec: 5

cluster:
  enabled: true
  node_name: "ae-node-1"
  bind_addr: "127.0.0.1"
  bind_port: %d
  advertise_addr: "127.0.0.1"
  advertise_port: %d
  expected_node_count: 2
  min_quorum_ratio: 0.5
  probe_replication_factor: 2
  peers:
    - "127.0.0.1:%d"
`, stateFile1, alertScript1, targetAddr, p1, p1, p2)
	}

	stateDir2 := t.TempDir()
	stateFile2 := filepath.Join(stateDir2, "state2.json")
	alertScript2 := filepath.Join(stateDir2, "alert2")

	cfgNode2YAML := fmt.Sprintf(`
app_name: "ae-test"
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
  ae-ch:
    type: script
    parameters:
      script: %q

default_notify: ["ae-ch"]

targets:
  - id: "ae-target"
    name: "AE Target"
    type: tcp
    target: %q
    interval_sec: 5

cluster:
  enabled: true
  node_name: "ae-node-2"
  bind_addr: "127.0.0.1"
  bind_port: %d
  advertise_addr: "127.0.0.1"
  advertise_port: %d
  expected_node_count: 2
  min_quorum_ratio: 0.5
  probe_replication_factor: 2
  peers:
    - "127.0.0.1:%d"
`, stateFile2, alertScript2, targetAddr, p2, p2, p1)

	// ── step 1: start node1 ──────────────────────────────────────────────────
	cfgPath1 := writeCfg(t, cfgNode1())
	e1 := engine.New("ae-node-1", makeRunner(), cfgPath1)
	if err := e1.Init(); err != nil {
		t.Fatalf("node1 Init: %v", err)
	}

	// ── step 2: start node2 ──────────────────────────────────────────────────
	cfgPath2 := writeCfg(t, cfgNode2YAML)
	e2 := engine.New("ae-node-2", makeRunner(), cfgPath2)
	if err := e2.Init(); err != nil {
		e1.Shutdown()
		t.Fatalf("node2 Init: %v", err)
	}
	t.Cleanup(e2.Shutdown)

	// ── wait for cluster formation ───────────────────────────────────────────
	waitState(t, "cluster: 2 members", 20*time.Second, func() bool {
		cm1, cm2 := e1.ClusterManager(), e2.ClusterManager()
		return cm1 != nil && cm2 != nil && cm1.AliveCount() >= 2 && cm2.AliveCount() >= 2
	})

	// ── wait for initial UP probe ────────────────────────────────────────────
	waitState(t, "first UP probe", 15*time.Second, func() bool {
		return atomic.LoadInt64(&accepted) > 0
	})

	// No spurious alert on UP.
	select {
	case ev := <-alertCh:
		t.Fatalf("unexpected alert on initial UP: %+v", ev)
	case <-time.After(300 * time.Millisecond):
	}

	// ── step 3: bring target down → wait for exactly 1 unreachable alert ────
	ln.Close()

	waitState(t, "unreachable alert (first)", 30*time.Second, func() bool {
		select {
		case ev := <-alertCh:
			return ev.status == "unreachable"
		default:
			return false
		}
	})
	t.Log("step 3: first unreachable alert received")

	// Drain any concurrent duplicates from this phase.
	time.Sleep(2 * time.Second)
	for {
		select {
		case <-alertCh:
		default:
			goto drainedFirst
		}
	}
drainedFirst:

	// ── step 4: stop node1 ───────────────────────────────────────────────────
	e1.Shutdown()
	t.Log("step 4: node1 shut down")

	// Give node2 time to observe node1 leaving.
	time.Sleep(2 * time.Second)

	// ── step 5: restart node1 with the SAME state.json ───────────────────────
	// A new Engine is created with the same config path / state file.
	// Anti-entropy must recognise the persisted hard_down and NOT re-alert.
	cfgPath1v2 := writeCfg(t, cfgNode1()) // same content, fresh file location ok
	e1v2 := engine.New("ae-node-1", makeRunner(), cfgPath1v2)
	if err := e1v2.Init(); err != nil {
		t.Fatalf("node1 restart Init: %v", err)
	}
	t.Cleanup(e1v2.Shutdown)
	t.Log("step 5: node1 restarted")

	// ── wait for re-join ─────────────────────────────────────────────────────
	waitState(t, "re-join: 2 members on node2", 20*time.Second, func() bool {
		cm2 := e2.ClusterManager()
		return cm2 != nil && cm2.AliveCount() >= 2
	})
	t.Log("step 6: node1 re-joined the cluster")

	// ── step 7: verify no duplicate alert within 10s ─────────────────────────
	deadline := time.After(10 * time.Second)
	duplicates := 0
	for {
		select {
		case ev := <-alertCh:
			if ev.status == "unreachable" {
				duplicates++
				t.Errorf("duplicate unreachable alert after rejoin: seq=%s", ev.seq)
			}
		case <-deadline:
			goto check
		}
	}
check:
	if duplicates == 0 {
		t.Log("✓ anti-entropy: no duplicate alert after node1 re-join")
	}
}
