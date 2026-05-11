//go:build !windows

// Package integration provides end-to-end tests for the netwatch engine.
// Each test spins up a real Engine instance against in-process TCP/mock targets
// without requiring a running binary or external processes.
package integration_test

import (
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/saidtaylan/netwatch/internal/engine"
)

// ── helpers ───────────────────────────────────────────────────────────────────

// newRegistry returns a fresh Prometheus registry with engine core metrics.
// Using a fresh registry per test avoids "already registered" panics from
// the global GaugeUp / GaugeDuration singletons.
func newRegistry(t *testing.T) *prometheus.Registry {
	t.Helper()
	reg := prometheus.NewRegistry()
	engine.RegisterMetrics(reg)
	return reg
}

// writeCfg writes a YAML config file to a temp dir and returns its path.
func writeCfg(t *testing.T, yaml string) string {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(p, []byte(yaml), 0o644); err != nil {
		t.Fatalf("writeCfg: %v", err)
	}
	return p
}

// alertTracker is an AlertRunner that records each call in a channel.
type alertTracker struct {
	ch chan alertEvent
}

type alertEvent struct {
	scriptBase string
	status     string
	name       string
	seq        string
}

func newAlertTracker(buf int) *alertTracker {
	return &alertTracker{ch: make(chan alertEvent, buf)}
}

func (a *alertTracker) runner() engine.AlertRunner {
	return func(scriptBase string, env map[string]string) error {
		a.ch <- alertEvent{
			scriptBase: scriptBase,
			status:     env["STATUS"],
			name:       env["NAME"],
			seq:        env["SEQ"],
		}
		return nil
	}
}

// waitState polls fn every 200ms until it returns true or deadline passes.
func waitState(t *testing.T, desc string, deadline time.Duration, fn func() bool) {
	t.Helper()
	end := time.Now().Add(deadline)
	for time.Now().Before(end) {
		if fn() {
			return
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatalf("timeout waiting for: %s", desc)
}

// ── stateFileV2 ────────────────────────────────────────────────────────────────
// Mirrors the on-disk format so tests can inspect state.json without importing
// unexported types.
type stateFileV2 struct {
	Version int                            `json:"version"`
	Targets map[string]persistedStateEntry `json:"targets"`
}
type persistedStateEntry struct {
	State     string `json:"state"`
	Seq       uint64 `json:"seq"`
	ErrorCode string `json:"error_code"`
}

func readStateFile(t *testing.T, path string) stateFileV2 {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("readStateFile: %v", err)
	}
	var sf stateFileV2
	if err := json.Unmarshal(b, &sf); err != nil {
		t.Fatalf("readStateFile unmarshal: %v", err)
	}
	return sf
}

// ── TestStandalone_ProbeAndAlertCycle ─────────────────────────────────────────

// TestStandalone_ProbeAndAlertCycle exercises the full standalone engine cycle:
//
//  1. TCP target UP → probe succeeds, no alert.
//  2. Target closes → soft-down → hard-down → "unreachable" alert.
//  3. state.json carries hard_down + non-zero Seq.
//  4. Target reopens → recovery → "reachable" alert with higher Seq.
//  5. state.json reverts to up + seq bumped.
func TestStandalone_ProbeAndAlertCycle(t *testing.T) {
	// ── step 0: start a mock TCP server ──────────────────────────────────────
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	addr := ln.Addr().String()
	// Accept connections in background so probes don't stall.
	var accepted int64
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return // closed
			}
			atomic.AddInt64(&accepted, 1)
			c.Close()
		}
	}()

	// ── step 1: write config ─────────────────────────────────────────────────
	stateFile := filepath.Join(t.TempDir(), "state.json")
	alertScript := filepath.Join(t.TempDir(), "alert") // runner ignores extension

	cfg := fmt.Sprintf(`
app_name: "integration-standalone"
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
  test-channel:
    type: script
    parameters:
      script: %q

default_notify: ["test-channel"]

targets:
  - id: "mock-tcp"
    name: "Mock TCP"
    type: tcp
    target: %q
    interval_sec: 5
`, stateFile, alertScript, addr)

	cfgPath := writeCfg(t, cfg)
	tracker := newAlertTracker(10)
	reg := newRegistry(t)
	_ = reg

	e := engine.New("test-host", tracker.runner(), cfgPath)
	if err := e.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}
	t.Cleanup(e.Shutdown)

	// ── step 2: verify first probe succeeds ──────────────────────────────────
	waitState(t, "first UP probe accepted", 10*time.Second, func() bool {
		return atomic.LoadInt64(&accepted) > 0
	})
	// No alert on first UP.
	select {
	case ev := <-tracker.ch:
		t.Fatalf("unexpected alert on first probe: %+v", ev)
	case <-time.After(500 * time.Millisecond):
	}

	// ── step 3: close target → expect unreachable alert ──────────────────────
	ln.Close() // connection refused from now on

	var downAlert alertEvent
	waitState(t, "unreachable alert", 20*time.Second, func() bool {
		select {
		case ev := <-tracker.ch:
			if ev.status == "unreachable" {
				downAlert = ev
				return true
			}
		default:
		}
		return false
	})
	if downAlert.name != "Mock TCP" {
		t.Errorf("alert name: want Mock TCP, got %q", downAlert.name)
	}
	if downAlert.seq == "" || downAlert.seq == "0" {
		t.Errorf("alert seq should be non-zero, got %q", downAlert.seq)
	}

	// ── step 4: verify state.json reflects hard_down ──────────────────────────
	waitState(t, "state.json hard_down", 10*time.Second, func() bool {
		if _, err := os.Stat(stateFile); err != nil {
			return false
		}
		sf := readStateFile(t, stateFile)
		entry, ok := sf.Targets["mock-tcp"]
		return ok && entry.State == "hard_down" && entry.Seq > 0
	})

	sfDown := readStateFile(t, stateFile)
	t.Logf("state.json after down: %+v", sfDown.Targets["mock-tcp"])

	// ── step 5: reopen target → expect reachable alert ───────────────────────
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

	var upAlert alertEvent
	waitState(t, "reachable alert", 20*time.Second, func() bool {
		select {
		case ev := <-tracker.ch:
			if ev.status == "reachable" {
				upAlert = ev
				return true
			}
		default:
		}
		return false
	})
	if upAlert.name != "Mock TCP" {
		t.Errorf("recovery alert name: want Mock TCP, got %q", upAlert.name)
	}

	// ── step 6: verify state.json reflects recovery ───────────────────────────
	waitState(t, "state.json up", 5*time.Second, func() bool {
		sf := readStateFile(t, stateFile)
		entry, ok := sf.Targets["mock-tcp"]
		return ok && entry.State == "up"
	})
	sfUp := readStateFile(t, stateFile)
	downSeq := sfDown.Targets["mock-tcp"].Seq
	upSeq := sfUp.Targets["mock-tcp"].Seq
	if upSeq <= downSeq {
		t.Errorf("recovery seq (%d) should be > down seq (%d)", upSeq, downSeq)
	}
	t.Logf("state.json after recovery: %+v", sfUp.Targets["mock-tcp"])
}

// ── TestStandalone_AppEnrichment ──────────────────────────────────────────────

// TestStandalone_AppEnrichment verifies that when a target belongs to an app,
// the alert env contains AFFECTED_APPS and OWNER_TEAMS.
func TestStandalone_AppEnrichment(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	addr := ln.Addr().String()
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			c.Close()
		}
	}()

	stateFile := filepath.Join(t.TempDir(), "state.json")
	alertScript := filepath.Join(t.TempDir(), "alert")

	cfg := fmt.Sprintf(`
app_name: "integration-apps"
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
  app-channel:
    type: script
    parameters:
      script: %q

default_notify: ["app-channel"]

targets:
  - id: "svc-db"
    name: "Service DB"
    type: tcp
    target: %q
    interval_sec: 5

apps:
  - name: "payment-service"
    owner_team: "fintech-sre"
    uses: ["svc-db"]
    notifications: ["app-channel"]
`, stateFile, alertScript, addr)

	cfgPath := writeCfg(t, cfg)

	// Use a richer tracker that captures full env map.
	type richEvent struct {
		env map[string]string
	}
	ch := make(chan richEvent, 10)
	runner := engine.AlertRunner(func(scriptBase string, env map[string]string) error {
		cp := make(map[string]string, len(env))
		for k, v := range env {
			cp[k] = v
		}
		ch <- richEvent{env: cp}
		return nil
	})

	_ = newRegistry(t)
	e := engine.New("test-host", runner, cfgPath)
	if err := e.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}
	t.Cleanup(e.Shutdown)

	// Wait for first UP probe, then close to trigger alert.
	time.Sleep(3 * time.Second)
	ln.Close()

	var ev richEvent
	waitState(t, "unreachable alert with apps", 20*time.Second, func() bool {
		select {
		case e := <-ch:
			if e.env["STATUS"] == "unreachable" {
				ev = e
				return true
			}
		default:
		}
		return false
	})

	if ev.env["AFFECTED_APPS"] != "payment-service" {
		t.Errorf("AFFECTED_APPS: want payment-service, got %q", ev.env["AFFECTED_APPS"])
	}
	if ev.env["OWNER_TEAMS"] != "fintech-sre" {
		t.Errorf("OWNER_TEAMS: want fintech-sre, got %q", ev.env["OWNER_TEAMS"])
	}
	if ev.env["SEQ"] == "" || ev.env["SEQ"] == "0" {
		t.Errorf("SEQ should be non-zero, got %q", ev.env["SEQ"])
	}
	t.Logf("alert env: AFFECTED_APPS=%s OWNER_TEAMS=%s SEQ=%s STATUS=%s",
		ev.env["AFFECTED_APPS"], ev.env["OWNER_TEAMS"], ev.env["SEQ"], ev.env["STATUS"])
}

// ── TestStandalone_StateV2Migration ──────────────────────────────────────────

// TestStandalone_StateV2Migration verifies that a legacy v1 state.json
// (plain map[string]bool) is migrated to v2 format automatically on startup.
func TestStandalone_StateV2Migration(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
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
	addr := ln.Addr().String()

	// Write a v1 state.json (plain bool map, as the old format used).
	stateFile := filepath.Join(t.TempDir(), "state.json")
	v1data := `{"mock-tcp":true,"other-dead":false}`
	if err := os.WriteFile(stateFile, []byte(v1data), 0o644); err != nil {
		t.Fatalf("write v1 state: %v", err)
	}

	cfg := fmt.Sprintf(`
app_name: "migration-test"
port:     "0"
state_file: %q
log_path: ""
timeout: 2
max_retries: 1
retry_interval_sec: 5
probe_interval_sec: 30
ticker_interval_sec: 1
reload_interval_sec: 0

notifications:
  noop:
    type: script
    parameters:
      script: "/bin/true"

default_notify: ["noop"]

targets:
  - id: "mock-tcp"
    name: "Mock TCP"
    type: tcp
    target: %q
`, stateFile, addr)

	cfgPath := writeCfg(t, cfg)
	_ = newRegistry(t)
	noop := engine.AlertRunner(func(_ string, _ map[string]string) error { return nil })
	e := engine.New("test-host", noop, cfgPath)
	if err := e.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}
	e.Shutdown()

	// After Init+Shutdown, state.json should be in v2 format.
	sf := readStateFile(t, stateFile)
	if sf.Version != 2 {
		t.Errorf("want state version 2, got %d", sf.Version)
	}
	// mock-tcp was true in v1 → "up" in v2
	entry, ok := sf.Targets["mock-tcp"]
	if !ok {
		t.Fatal("mock-tcp not found in migrated state")
	}
	if entry.State != "up" {
		t.Errorf("mock-tcp state: want up, got %q", entry.State)
	}
	t.Logf("migrated state: %+v", sf.Targets)
}
