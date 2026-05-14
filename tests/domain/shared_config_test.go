//go:build !windows

// tests/domain/shared_config_test.go — domain tests for the config push/sync lifecycle.
//
// Tests verify that SharedConfig merging:
//   - Overwrites only shared fields in the target config file.
//   - Preserves node-specific fields (port, node_alias, targets, etc.).
//   - The merged config remains valid and loadable.
package domain_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/saidtaylan/netwatch/internal/engine"
)

// buildEngineForPush creates a running engine in a temp dir.
// Returns the engine, the config path, and a cleanup func.
func buildEngineForPush(t *testing.T, yamlConfig string) (*engine.Engine, string) {
	t.Helper()
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(cfgPath, []byte(yamlConfig), 0644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	hostname, _ := os.Hostname()
	e := engine.New(hostname, func(_ string, _ map[string]string) error { return nil }, cfgPath)
	if err := e.Init(); err != nil {
		t.Fatalf("engine.Init: %v", err)
	}
	t.Cleanup(func() { e.Shutdown() })
	return e, cfgPath
}

const pushBaseConfig = `
port: "19800"
node_alias: "original-alias"
state_file: "/tmp/push-test-state.json"
log_path: ""
timeout: 5
max_retries: 1
retry_interval_sec: 5
probe_interval_sec: 30
ticker_interval_sec: 1
reload_interval_sec: 0
notifications: {}
default_notify: []
targets:
  - name: "my-target"
    type: tcp
    target: "127.0.0.1:9000"
`

// TestSharedConfig_ApplyPreservesNodeSpecificFields: applying a SharedConfig
// must NOT overwrite port, node_alias, targets, state_file, or log_path.
func TestSharedConfig_ApplyPreservesNodeSpecificFields(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(cfgPath, []byte(pushBaseConfig), 0644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	hostname, _ := os.Hostname()
	e := engine.New(hostname, func(_ string, _ map[string]string) error { return nil }, cfgPath)
	if err := e.Init(); err != nil {
		t.Fatalf("engine.Init: %v", err)
	}
	defer e.Shutdown()

	// Apply a SharedConfig that only updates timeout.
	maxRetries := 3
	sc := engine.SharedConfig{
		Timeout:    10,
		MaxRetries: &maxRetries,
	}
	raw, _ := json.Marshal(sc)
	if err := e.ApplySharedConfigJSON(raw); err != nil {
		t.Fatalf("ApplySharedConfigJSON: %v", err)
	}

	// Give hot-reload a moment.
	time.Sleep(200 * time.Millisecond)

	// Re-read the config file and verify node-specific fields are preserved.
	cfg, err := engine.ValidateConfigFile(cfgPath)
	if err != nil {
		t.Fatalf("validate after apply: %v", err)
	}
	if cfg.Port != "19800" {
		t.Errorf("port must be preserved: got %q, want 19800", cfg.Port)
	}
	if cfg.NodeAlias != "original-alias" {
		t.Errorf("node_alias must be preserved: got %q, want original-alias", cfg.NodeAlias)
	}
	if len(cfg.Targets) != 1 {
		t.Errorf("targets must be preserved: got %d, want 1", len(cfg.Targets))
	}
}

// TestSharedConfig_ApplyUpdatesSharedFields: applying a SharedConfig updates
// only the declared shared fields.
func TestSharedConfig_ApplyUpdatesSharedFields(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(cfgPath, []byte(pushBaseConfig), 0644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	hostname, _ := os.Hostname()
	e := engine.New(hostname, func(_ string, _ map[string]string) error { return nil }, cfgPath)
	if err := e.Init(); err != nil {
		t.Fatalf("engine.Init: %v", err)
	}
	defer e.Shutdown()

	maxRetries := 7
	sc := engine.SharedConfig{
		Timeout:    42,
		MaxRetries: &maxRetries,
		// DefaultNotify intentionally omitted — don't reference undefined channels.
	}
	raw, _ := json.Marshal(sc)
	if err := e.ApplySharedConfigJSON(raw); err != nil {
		t.Fatalf("ApplySharedConfigJSON: %v", err)
	}

	time.Sleep(300 * time.Millisecond)

	cfg, err := engine.ValidateConfigFile(cfgPath)
	if err != nil {
		t.Fatalf("validate after apply: %v", err)
	}
	if cfg.Timeout != 42 {
		t.Errorf("timeout: got %d, want 42", cfg.Timeout)
	}
}

// TestSharedConfig_ExtractDoesNotContainSecrets: ExtractSharedConfig reads from
// the raw pre-injection bytes so it never returns a resolved credential.
func TestSharedConfig_ExtractDoesNotContainSecrets(t *testing.T) {
	dir := t.TempDir()

	// Write a credentials file with a secret.
	credsPath := filepath.Join(dir, "credentials.env")
	if err := os.WriteFile(credsPath, []byte("MY_SECRET=verysecret\n"), 0600); err != nil {
		t.Fatalf("write creds: %v", err)
	}

	cfgYAML := `
port: "19801"
state_file: "` + filepath.Join(dir, "state.json") + `"
log_path: ""
timeout: 5
max_retries: 1
retry_interval_sec: 5
probe_interval_sec: 30
ticker_interval_sec: 1
reload_interval_sec: 0
credentials_file: "` + credsPath + `"
notifications:
  smtp-chan:
    type: mail
    parameters:
      smtp_host: "smtp.example.com"
      smtp_port: "587"
      from: "noreply@example.com"
      to: "ops@example.com"
      username: "user"
      password: "${MY_SECRET}"
default_notify: []
targets: []
`
	cfgPath := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(cfgPath, []byte(cfgYAML), 0644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	hostname, _ := os.Hostname()
	e := engine.New(hostname, func(_ string, _ map[string]string) error { return nil }, cfgPath)
	if err := e.Init(); err != nil {
		t.Fatalf("engine.Init: %v", err)
	}
	defer e.Shutdown()

	sc, err := e.ExtractSharedConfig()
	if err != nil {
		t.Fatalf("ExtractSharedConfig: %v", err)
	}

	// The extracted config must contain the raw ${MY_SECRET} placeholder,
	// not the resolved value "verysecret".
	raw, _ := json.Marshal(sc)
	if contains(string(raw), "verysecret") {
		t.Error("ExtractSharedConfig leaked a resolved credential (verysecret)")
	}
}

// TestSharedConfig_AppliedFields_Comprehensive: AppliedFields returns the
// correct set of field names for a fully-populated SharedConfig.
func TestSharedConfig_AppliedFields_Comprehensive(t *testing.T) {
	maxRetries := 3
	retryInterval := 10
	tickerInterval := 5
	probeInterval := 30
	reloadInterval := 60
	watchdog := 120
	sc := engine.SharedConfig{
		Timeout:              15,
		MaxRetries:           &maxRetries,
		RetryIntervalSec:     &retryInterval,
		TickerIntervalSec:    &tickerInterval,
		ProbeIntervalSec:     &probeInterval,
		ReloadIntervalSec:    &reloadInterval,
		WatchdogThresholdSec: &watchdog,
		Notifications: map[string]engine.AlertChannelConfig{
			"chan1": {Type: "script"},
		},
		DefaultNotify: []string{"chan1"},
		Cluster: &engine.SharedClusterConfig{
			ExpectedNodeCount:      3,
			MinQuorumRatio:         0.5,
			ProbeReplicationFactor: 3,
		},
	}
	fields := engine.AppliedFields(sc)
	fieldSet := make(map[string]bool, len(fields))
	for _, f := range fields {
		fieldSet[f] = true
	}

	mustHave := []string{
		"timeout", "max_retries", "retry_interval_sec", "ticker_interval_sec",
		"probe_interval_sec", "reload_interval_sec", "watchdog_threshold_sec",
		"notifications", "default_notify", "cluster.*",
	}
	for _, want := range mustHave {
		if !fieldSet[want] {
			t.Errorf("AppliedFields missing %q; got: %v", want, fields)
		}
	}
}

// contains is a simple substring check helper.
func contains(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || len(sub) == 0 || func() bool {
		for i := 0; i <= len(s)-len(sub); i++ {
			if s[i:i+len(sub)] == sub {
				return true
			}
		}
		return false
	}())
}

// toRaw marshals a SharedConfig to JSON for use in ApplySharedConfigJSON.
func toRaw(t *testing.T, sc engine.SharedConfig) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(sc)
	if err != nil {
		t.Fatalf("marshal SharedConfig: %v", err)
	}
	return raw
}
