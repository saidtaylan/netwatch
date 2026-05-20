//go:build !windows

// tests/domain/alert_flow_test.go — domain-driven tests for the full alert flow.
//
// These tests start a real Engine against test TCP listeners and observe
// that alerts fire correctly through the state machine:
//
//   probe fails → SOFT_DOWN → retries exhaust → HARD_DOWN → alert("unreachable")
//   probe recovers → UP → alert("reachable")
//
// No cluster layer is involved — these are standalone-mode domain tests.
package domain_test

import (
	"net"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/saidtaylan/netwatch/internal/engine"
)

// capturedAlert holds one call to the AlertRunner (script channel).
type capturedAlert struct {
	status    string
	target    string
	nodeAlias string
	scope     string
	env       map[string]string
}

// alertCapture returns an AlertRunner that sends every invocation to the
// returned channel (buffered so the runner never blocks the probe loop).
func alertCapture() (engine.AlertRunner, <-chan capturedAlert) {
	ch := make(chan capturedAlert, 16)
	runner := func(_ string, env map[string]string) error {
		ch <- capturedAlert{
			status:    env["STATUS"],
			target:    env["TARGET"],
			nodeAlias: env["NODE_ALIAS"],
			scope:     env["SCOPE"],
			env:       env,
		}
		return nil
	}
	return runner, ch
}

// freePort returns an OS-chosen free TCP port.
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

// startEngine creates an engine from yamlConfig, starts it (Init), and
// registers t.Cleanup to shut it down.
func startEngine(t *testing.T, yamlConfig string, runner engine.AlertRunner) *engine.Engine {
	t.Helper()
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(cfgPath, []byte(yamlConfig), 0644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	hostname, _ := os.Hostname()
	e := engine.New(hostname, runner, cfgPath)
	if err := e.Init(); err != nil {
		t.Fatalf("engine.Init: %v", err)
	}
	t.Cleanup(func() { e.Shutdown() })
	return e
}

// waitAlert waits up to timeout for an alert matching the given status.
func waitAlert(t *testing.T, ch <-chan capturedAlert, status string, timeout time.Duration) capturedAlert {
	t.Helper()
	deadline := time.After(timeout)
	for {
		select {
		case a := <-ch:
			if a.status == status {
				return a
			}
		case <-deadline:
			t.Fatalf("timed out waiting for alert with status=%q", status)
		}
	}
}

// domainConfig builds a minimal test config yaml. port must be unique per test.
func domainConfig(t *testing.T, targetAddr string, extra ...string) string {
	t.Helper()
	dir := t.TempDir()
	port := freePort(t)
	cfg := `
port: "` + itoa(port) + `"
state_file: "` + filepath.Join(dir, "state.json") + `"
log_path: ""
timeout: 2
max_retries: 1
retry_interval_sec: 5
probe_interval_sec: 5
ticker_interval_sec: 1
reload_interval_sec: 0
notifications:
  test-chan:
    type: script
    parameters:
      script: "/tmp/alert.sh"
default_notify: ["test-chan"]
targets:
  - name: "test-target"
    type: tcp
    target: "` + targetAddr + `"
`
	for _, x := range extra {
		cfg += x + "\n"
	}
	return cfg
}

func itoa(n int) string {
	return strconv.Itoa(n)
}

// ── Tests ─────────────────────────────────────────────────────────────────────

// TestAlertFlow_TCPDown_TriggersUnreachable: a port nobody is listening on
// causes a hard-down after retries exhaust, and an "unreachable" alert fires.
func TestAlertFlow_TCPDown_TriggersUnreachable(t *testing.T) {
	port := freePort(t)
	addr := "127.0.0.1:" + itoa(port) // nothing listening here

	runner, alerts := alertCapture()
	startEngine(t, domainConfig(t, addr), runner)

	a := waitAlert(t, alerts, "unreachable", 15*time.Second)
	if a.target != addr {
		t.Errorf("alert target: got %q, want %q", a.target, addr)
	}
}

// TestAlertFlow_TCPRecovery: target starts down, comes back up → recovery alert.
func TestAlertFlow_TCPRecovery(t *testing.T) {
	port := freePort(t)
	addr := "127.0.0.1:" + itoa(port)

	runner, alerts := alertCapture()
	startEngine(t, domainConfig(t, addr), runner)

	// Wait for the down alert first.
	waitAlert(t, alerts, "unreachable", 15*time.Second)

	// Now start a real listener to simulate recovery.
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		t.Skipf("cannot re-bind port %d: %v", port, err)
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

	waitAlert(t, alerts, "reachable", 20*time.Second)
}

// TestAlertFlow_NodeAlias_InEnv: NODE_ALIAS env var is injected into alert.
func TestAlertFlow_NodeAlias_InEnv(t *testing.T) {
	port := freePort(t)
	addr := "127.0.0.1:" + itoa(port)

	cfg := domainConfig(t, addr, `node_alias: "my-test-node"`)
	runner, alerts := alertCapture()
	startEngine(t, cfg, runner)

	a := waitAlert(t, alerts, "unreachable", 15*time.Second)
	if a.nodeAlias != "my-test-node" {
		t.Errorf("NODE_ALIAS: got %q, want my-test-node", a.nodeAlias)
	}
}

// TestAlertFlow_AppName_Backward: APP_NAME env var equals NODE_ALIAS (backward compat).
func TestAlertFlow_AppName_Backward(t *testing.T) {
	port := freePort(t)
	addr := "127.0.0.1:" + itoa(port)

	cfg := domainConfig(t, addr, `node_alias: "compat-check"`)
	runner, alerts := alertCapture()
	startEngine(t, cfg, runner)

	a := waitAlert(t, alerts, "unreachable", 15*time.Second)
	if a.env["APP_NAME"] != "compat-check" {
		t.Errorf("APP_NAME: got %q, want compat-check (backward compat)", a.env["APP_NAME"])
	}
}

// TestAlertFlow_Scope_Set: alerts in standalone mode carry a SCOPE env var.
// (In standalone mode the scope is NODE_LOCAL — cluster is nil so classifyScope
// returns that value because it checks local state only.)
func TestAlertFlow_Scope_Set(t *testing.T) {
	port := freePort(t)
	addr := "127.0.0.1:" + itoa(port)

	runner, alerts := alertCapture()
	startEngine(t, domainConfig(t, addr), runner)

	a := waitAlert(t, alerts, "unreachable", 15*time.Second)
	if a.scope == "" {
		t.Error("SCOPE env var must be non-empty in alert")
	}
}

// TestAlertFlow_SeqIncrementsOnRecovery: seq=1 on hard-down, seq=2 on recovery.
func TestAlertFlow_SeqIncrementsOnRecovery(t *testing.T) {
	port := freePort(t)
	addr := "127.0.0.1:" + itoa(port)

	runner, alerts := alertCapture()
	startEngine(t, domainConfig(t, addr), runner)

	down := waitAlert(t, alerts, "unreachable", 15*time.Second)
	if down.env["SEQ"] == "" {
		t.Error("SEQ env var must be present in unreachable alert")
	}

	// Start listener for recovery.
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		t.Skipf("cannot re-bind port %d: %v", port, err)
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

	up := waitAlert(t, alerts, "reachable", 20*time.Second)
	if up.env["SEQ"] == "" {
		t.Error("SEQ env var must be present in reachable alert")
	}
	// Seq on recovery should be > seq on hard-down.
	if up.env["SEQ"] <= down.env["SEQ"] {
		t.Errorf("seq should increase on recovery: down=%s recovery=%s",
			down.env["SEQ"], up.env["SEQ"])
	}
}

// TestAlertFlow_ErrorCode_InAlert: ERROR_CODE env var is set on down alert.
func TestAlertFlow_ErrorCode_InAlert(t *testing.T) {
	port := freePort(t)
	addr := "127.0.0.1:" + itoa(port)

	runner, alerts := alertCapture()
	startEngine(t, domainConfig(t, addr), runner)

	a := waitAlert(t, alerts, "unreachable", 15*time.Second)
	if a.env["ERROR_CODE"] == "" {
		t.Error("ERROR_CODE must be non-empty in an unreachable alert")
	}
}

// TestAlertFlow_ErrorCodeClearedOnRecovery: ERROR_CODE is empty on recovery alert.
func TestAlertFlow_ErrorCodeClearedOnRecovery(t *testing.T) {
	port := freePort(t)
	addr := "127.0.0.1:" + itoa(port)

	runner, alerts := alertCapture()
	startEngine(t, domainConfig(t, addr), runner)

	waitAlert(t, alerts, "unreachable", 15*time.Second)

	ln, err := net.Listen("tcp", addr)
	if err != nil {
		t.Skipf("cannot re-bind port %d: %v", port, err)
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

	up := waitAlert(t, alerts, "reachable", 20*time.Second)
	if up.env["ERROR_CODE"] != "" {
		t.Errorf("ERROR_CODE should be empty on recovery, got %q", up.env["ERROR_CODE"])
	}
}

// TestAlertFlow_AffectedApps_InAlert: AFFECTED_APPS and OWNER_TEAMS are injected when apps are defined.
func TestAlertFlow_AffectedApps_InAlert(t *testing.T) {
	port := freePort(t)
	addr := "127.0.0.1:" + itoa(port)
	dir := t.TempDir()

	appCfg := `
port: "` + itoa(freePort(t)) + `"
state_file: "` + filepath.Join(dir, "state.json") + `"
log_path: ""
timeout: 2
max_retries: 1
retry_interval_sec: 5
probe_interval_sec: 5
ticker_interval_sec: 1
reload_interval_sec: 0
notifications:
  test-chan:
    type: script
    parameters:
      script: "/tmp/alert.sh"
default_notify: ["test-chan"]
targets:
  - id: "db-target"
    name: "DB"
    type: tcp
    target: "` + addr + `"
apps:
  - name: "my-service"
    owner_team: "platform-sre"
    uses: ["db-target"]
`
	cfgPath := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(cfgPath, []byte(appCfg), 0644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	runner, alerts := alertCapture()
	hostname, _ := os.Hostname()
	e := engine.New(hostname, runner, cfgPath)
	if err := e.Init(); err != nil {
		t.Fatalf("engine.Init: %v", err)
	}
	t.Cleanup(func() { e.Shutdown() })

	a := waitAlert(t, alerts, "unreachable", 15*time.Second)
	if a.env["AFFECTED_APPS"] != "my-service" {
		t.Errorf("AFFECTED_APPS: got %q, want my-service", a.env["AFFECTED_APPS"])
	}
	if a.env["OWNER_TEAMS"] != "platform-sre" {
		t.Errorf("OWNER_TEAMS: got %q, want platform-sre", a.env["OWNER_TEAMS"])
	}
}
