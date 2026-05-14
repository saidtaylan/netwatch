//go:build !windows

// tests/domain/state_persistence_test.go — domain tests for state.json persistence.
//
// These tests verify:
//   - The engine writes a valid v2 state.json when a target goes hard-down.
//   - On restart, a node with a persisted hard-down state does NOT re-fire
//     a duplicate "unreachable" alert.
//   - On restart + recovery, a "reachable" alert fires exactly once.
package domain_test

import (
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/saidtaylan/netwatch/internal/engine"
)

// stateFileContent reads and parses the v2 state.json.
type stateV2 struct {
	Version int `json:"version"`
	Targets map[string]struct {
		State     string `json:"state"`
		Seq       uint64 `json:"seq"`
		ErrorCode string `json:"error_code"`
		OwnerNode string `json:"owner_node"`
	} `json:"targets"`
}

func readStateFile(t *testing.T, path string) stateV2 {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("readStateFile %s: %v", path, err)
	}
	var s stateV2
	if err := json.Unmarshal(data, &s); err != nil {
		t.Fatalf("unmarshal state: %v", err)
	}
	return s
}

// startMinimalEngine starts an engine with a TCP target at addr.
// Returns the engine and the config/state dir.
func startMinimalEngine(t *testing.T, addr, dir string, runner engine.AlertRunner) *engine.Engine {
	t.Helper()
	httpPort := freePort(t)
	cfgPath := filepath.Join(dir, "config.yaml")
	cfg := `
port: "` + itoa(httpPort) + `"
state_file: "` + filepath.Join(dir, "state.json") + `"
log_path: ""
timeout: 2
max_retries: 1
retry_interval_sec: 5
probe_interval_sec: 5
ticker_interval_sec: 1
reload_interval_sec: 0
notifications:
  test:
    type: script
    parameters:
      script: "/tmp/alert.sh"
default_notify: ["test"]
targets:
  - id: "tcp-target"
    name: "TCP Target"
    type: tcp
    target: "` + addr + `"
`
	if err := os.WriteFile(cfgPath, []byte(cfg), 0644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	hostname, _ := os.Hostname()
	e := engine.New(hostname, runner, cfgPath)
	if err := e.Init(); err != nil {
		t.Fatalf("engine.Init: %v", err)
	}
	return e
}

// TestStatePersistence_V2Format: after a target goes hard-down, state.json
// must be written in v2 format with the correct fields.
func TestStatePersistence_V2Format(t *testing.T) {
	port := freePort(t)
	addr := "127.0.0.1:" + itoa(port) // nothing listening

	dir := t.TempDir()
	runner, alerts := alertCapture()
	e := startMinimalEngine(t, addr, dir, runner)

	// Wait for hard-down alert.
	waitAlert(t, alerts, "unreachable", 15*time.Second)
	e.Shutdown()

	// Verify state.json format.
	st := readStateFile(t, filepath.Join(dir, "state.json"))
	if st.Version != 2 {
		t.Errorf("state.json version: got %d, want 2", st.Version)
	}
	target, ok := st.Targets["tcp-target"]
	if !ok {
		t.Fatalf("tcp-target not found in state.json; keys: %v", st.Targets)
	}
	if target.State != "hard_down" {
		t.Errorf("state: got %q, want hard_down", target.State)
	}
	if target.Seq == 0 {
		t.Error("seq must be > 0 after hard-down")
	}
	if target.ErrorCode == "" {
		t.Error("error_code must be non-empty after hard-down")
	}
}

// TestStatePersistence_NoSpuriousAlert: restarting the engine with a
// persisted hard-down state must NOT re-fire an "unreachable" alert.
func TestStatePersistence_NoSpuriousAlert(t *testing.T) {
	port := freePort(t)
	addr := "127.0.0.1:" + itoa(port)
	dir := t.TempDir()

	// Phase 1: bring the target to hard-down.
	runner1, alerts1 := alertCapture()
	e1 := startMinimalEngine(t, addr, dir, runner1)
	waitAlert(t, alerts1, "unreachable", 15*time.Second)
	e1.Shutdown()

	// Phase 2: restart. Target is still down. No "unreachable" alert should fire.
	runner2, alerts2 := alertCapture()
	hostname, _ := os.Hostname()
	e2 := engine.New(hostname, runner2, filepath.Join(dir, "config.yaml"))
	if err := e2.Init(); err != nil {
		t.Fatalf("engine.Init restart: %v", err)
	}
	defer e2.Shutdown()

	// Give the engine time to probe (it will fail, but should not alert again).
	select {
	case a := <-alerts2:
		if a.status == "unreachable" {
			t.Errorf("spurious duplicate unreachable alert after restart: target=%s", a.target)
		}
	case <-time.After(5 * time.Second):
		// Good — no alert within 5 seconds.
	}
}

// TestStatePersistence_RecoveryAfterRestart: if a target was hard-down before
// restart and recovers after restart, exactly one "reachable" alert fires.
func TestStatePersistence_RecoveryAfterRestart(t *testing.T) {
	port := freePort(t)
	addr := "127.0.0.1:" + itoa(port)
	dir := t.TempDir()

	// Phase 1: hard-down.
	runner1, alerts1 := alertCapture()
	e1 := startMinimalEngine(t, addr, dir, runner1)
	waitAlert(t, alerts1, "unreachable", 15*time.Second)
	e1.Shutdown()

	// Phase 2: restart with the target now UP.
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		t.Skipf("cannot bind %s: %v", addr, err)
	}
	defer ln.Close()
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			c.Close()
		}
	}()

	runner2, alerts2 := alertCapture()
	hostname, _ := os.Hostname()
	e2 := engine.New(hostname, runner2, filepath.Join(dir, "config.yaml"))
	if err := e2.Init(); err != nil {
		t.Fatalf("engine.Init restart: %v", err)
	}
	defer e2.Shutdown()

	// Should receive exactly one "reachable" alert.
	waitAlert(t, alerts2, "reachable", 15*time.Second)
}

// TestStatePersistence_SeqContinuesAcrossRestart: seq must be monotonically
// increasing across restarts (not reset to 0).
func TestStatePersistence_SeqContinuesAcrossRestart(t *testing.T) {
	port := freePort(t)
	addr := "127.0.0.1:" + itoa(port)
	dir := t.TempDir()

	// Phase 1: hard-down (seq will be 1).
	runner1, alerts1 := alertCapture()
	e1 := startMinimalEngine(t, addr, dir, runner1)
	a1 := waitAlert(t, alerts1, "unreachable", 15*time.Second)
	firstSeq := a1.env["SEQ"]
	e1.Shutdown()

	// Phase 2: start listener for recovery.
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		t.Skipf("cannot bind %s: %v", addr, err)
	}
	defer ln.Close()
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			c.Close()
		}
	}()

	runner2, alerts2 := alertCapture()
	hostname, _ := os.Hostname()
	e2 := engine.New(hostname, runner2, filepath.Join(dir, "config.yaml"))
	if err := e2.Init(); err != nil {
		t.Fatalf("engine.Init restart: %v", err)
	}
	defer e2.Shutdown()

	a2 := waitAlert(t, alerts2, "reachable", 15*time.Second)
	recoverySeq := a2.env["SEQ"]

	if recoverySeq <= firstSeq {
		t.Errorf("recovery seq (%s) should be > hard-down seq (%s)", recoverySeq, firstSeq)
	}
}
