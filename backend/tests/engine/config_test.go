//go:build !windows

package engine_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/saidtaylan/netwatch/internal/engine"
)

// writeConfig writes a YAML config to a temp file and returns the path.
func writeConfig(t *testing.T, yaml string) string {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(p, []byte(yaml), 0o644); err != nil {
		t.Fatalf("writeConfig: %v", err)
	}
	return p
}

// TestConfig_MinimalValid verifies that a minimal config with no targets passes validation.
func TestConfig_MinimalValid(t *testing.T) {
	p := writeConfig(t, `
port: "19001"
timeout: 5
state_file: "/tmp/state-test.json"
log_path: ""
`)
	cfg, err := engine.ValidateConfigFile(p)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Port != "19001" {
		t.Errorf("port: want 19001, got %q", cfg.Port)
	}
}

// TestConfig_NodeAliasOptional confirms node_alias absence is not an error.
func TestConfig_NodeAliasOptional(t *testing.T) {
	p := writeConfig(t, `
port: "19002"
timeout: 5
`)
	cfg, err := engine.ValidateConfigFile(p)
	if err != nil {
		t.Fatalf("unexpected error for missing node_alias: %v", err)
	}
	if cfg.NodeAlias != "" {
		t.Errorf("expected empty NodeAlias, got %q", cfg.NodeAlias)
	}
}

// TestConfig_NodeAliasPresent verifies node_alias is parsed correctly when present.
func TestConfig_NodeAliasPresent(t *testing.T) {
	p := writeConfig(t, `
port: "19003"
timeout: 5
node_alias: "my-prober-node"
`)
	cfg, err := engine.ValidateConfigFile(p)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.NodeAlias != "my-prober-node" {
		t.Errorf("NodeAlias: want my-prober-node, got %q", cfg.NodeAlias)
	}
}

// TestConfig_SetupToken verifies admin.setup_token field is recognized.
func TestConfig_SetupToken(t *testing.T) {
	p := writeConfig(t, `
port: "19004"
timeout: 5
admin:
  setup_token: "secret-token-abc"
`)
	cfg, err := engine.ValidateConfigFile(p)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Admin == nil {
		t.Fatal("expected Admin to be non-nil")
	}
	if cfg.Admin.SetupToken != "secret-token-abc" {
		t.Errorf("Admin.SetupToken: want secret-token-abc, got %q", cfg.Admin.SetupToken)
	}
}

// TestConfig_SetupTokenEmpty verifies admin section works when setup_token is empty.
func TestConfig_SetupTokenEmpty(t *testing.T) {
	p := writeConfig(t, `
port: "19005"
timeout: 5
admin:
  setup_token: ""
`)
	cfg, err := engine.ValidateConfigFile(p)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Admin == nil {
		t.Fatal("expected Admin section to be parsed")
	}
	if cfg.Admin.SetupToken != "" {
		t.Errorf("expected empty setup_token, got %q", cfg.Admin.SetupToken)
	}
}

// TestConfig_ClusterEnabledRequiresNodeName verifies error when cluster.enabled=true but node_name is absent.
func TestConfig_ClusterEnabledRequiresNodeName(t *testing.T) {
	p := writeConfig(t, `
port: "19006"
timeout: 5
cluster:
  enabled: true
`)
	_, err := engine.ValidateConfigFile(p)
	if err == nil {
		t.Fatal("expected error for cluster.enabled=true without node_name")
	}
}

// TestConfig_ClusterEnabledWithNodeName verifies no error when node_name is set.
func TestConfig_ClusterEnabledWithNodeName(t *testing.T) {
	p := writeConfig(t, `
port: "19007"
timeout: 5
cluster:
  enabled: true
  node_name: "node-1"
  bind_port: 17946
`)
	_, err := engine.ValidateConfigFile(p)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// TestConfig_ClusterDisabledIgnoresFields verifies that cluster fields are not validated when disabled.
func TestConfig_ClusterDisabledIgnoresFields(t *testing.T) {
	p := writeConfig(t, `
port: "19008"
timeout: 5
cluster:
  enabled: false
  node_name: ""
`)
	_, err := engine.ValidateConfigFile(p)
	if err != nil {
		t.Fatalf("unexpected error for disabled cluster: %v", err)
	}
}

// TestConfig_MaxRetriesNegative verifies validation error for negative max_retries.
func TestConfig_MaxRetriesNegative(t *testing.T) {
	p := writeConfig(t, `
port: "19009"
timeout: 5
max_retries: -1
`)
	_, err := engine.ValidateConfigFile(p)
	if err == nil {
		t.Fatal("expected error for max_retries=-1")
	}
}

// TestConfig_MaxRetriesZero verifies zero max_retries is valid.
func TestConfig_MaxRetriesZero(t *testing.T) {
	p := writeConfig(t, `
port: "19010"
timeout: 5
max_retries: 0
`)
	_, err := engine.ValidateConfigFile(p)
	if err != nil {
		t.Fatalf("unexpected error for max_retries=0: %v", err)
	}
}

// TestConfig_RetryIntervalTooSmall verifies validation error for retry_interval_sec < 5.
func TestConfig_RetryIntervalTooSmall(t *testing.T) {
	p := writeConfig(t, `
port: "19011"
timeout: 5
retry_interval_sec: 3
`)
	_, err := engine.ValidateConfigFile(p)
	if err == nil {
		t.Fatal("expected error for retry_interval_sec=3 (< 5)")
	}
}

// TestConfig_RetryIntervalAtMinimum verifies retry_interval_sec=5 passes.
func TestConfig_RetryIntervalAtMinimum(t *testing.T) {
	p := writeConfig(t, `
port: "19012"
timeout: 5
retry_interval_sec: 5
`)
	_, err := engine.ValidateConfigFile(p)
	if err != nil {
		t.Fatalf("unexpected error for retry_interval_sec=5: %v", err)
	}
}

// TestConfig_UnresolvedVariable verifies that ${UNRESOLVED_VAR} causes an error.
func TestConfig_UnresolvedVariable(t *testing.T) {
	p := writeConfig(t, `
port: "19013"
timeout: 5
node_alias: "${UNRESOLVED_NETWATCH_VAR_XXXX}"
`)
	_, err := engine.ValidateConfigFile(p)
	if err == nil {
		t.Fatal("expected error for unresolved ${UNRESOLVED_NETWATCH_VAR_XXXX}")
	}
}

// TestConfig_InvalidYAML verifies that malformed YAML causes an error.
func TestConfig_InvalidYAML(t *testing.T) {
	p := writeConfig(t, `
port: "19014"
timeout: [bad yaml
  - this is not valid
`)
	_, err := engine.ValidateConfigFile(p)
	if err == nil {
		t.Fatal("expected error for invalid YAML")
	}
}

// TestConfig_WithTargets verifies that targets are parsed correctly.
func TestConfig_WithTargets(t *testing.T) {
	p := writeConfig(t, `
port: "19015"
timeout: 5
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
  - id: "test-tcp"
    name: "Test TCP"
    type: tcp
    target: "127.0.0.1:9999"
`)
	cfg, err := engine.ValidateConfigFile(p)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(cfg.Targets) != 1 {
		t.Fatalf("expected 1 target, got %d", len(cfg.Targets))
	}
	if cfg.Targets[0].Name != "Test TCP" {
		t.Errorf("target name: want Test TCP, got %q", cfg.Targets[0].Name)
	}
}

// TestConfig_DefaultNotifyUndefined verifies error when default_notify references undefined channel.
func TestConfig_DefaultNotifyUndefined(t *testing.T) {
	p := writeConfig(t, `
port: "19016"
timeout: 5
default_notify: ["nonexistent-channel"]
`)
	_, err := engine.ValidateConfigFile(p)
	if err == nil {
		t.Fatal("expected error: default_notify references undefined channel")
	}
}

// TestConfig_ProbeReplicationFactorNegative verifies error for negative probe_replication_factor.
func TestConfig_ProbeReplicationFactorNegative(t *testing.T) {
	p := writeConfig(t, `
port: "19017"
timeout: 5
cluster:
  enabled: true
  node_name: "n1"
  bind_port: 17947
  probe_replication_factor: -1
`)
	_, err := engine.ValidateConfigFile(p)
	if err == nil {
		t.Fatal("expected error for probe_replication_factor=-1")
	}
}

// TestConfig_ProbeReplicationFactorZero verifies zero is valid (means no limit).
func TestConfig_ProbeReplicationFactorZero(t *testing.T) {
	p := writeConfig(t, `
port: "19018"
timeout: 5
cluster:
  enabled: true
  node_name: "n1"
  bind_port: 17948
  probe_replication_factor: 0
`)
	_, err := engine.ValidateConfigFile(p)
	if err != nil {
		t.Fatalf("unexpected error for probe_replication_factor=0: %v", err)
	}
}

// TestConfig_ClusterMinQuorumRatioInvalid verifies error for invalid min_quorum_ratio.
// Note: cluster.Validate() does not check min_quorum_ratio range (it checks node_name,
// bind_port, and keyring). So this just ensures the field is parsed.
func TestConfig_ClusterFieldsParsed(t *testing.T) {
	p := writeConfig(t, `
port: "19019"
timeout: 5
cluster:
  enabled: true
  node_name: "n1"
  bind_port: 17949
  min_quorum_ratio: 0.6
  expected_node_count: 3
  zone: "eu-west"
`)
	cfg, err := engine.ValidateConfigFile(p)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Cluster.Zone != "eu-west" {
		t.Errorf("cluster zone: want eu-west, got %q", cfg.Cluster.Zone)
	}
	if cfg.Cluster.ExpectedNodeCount != 3 {
		t.Errorf("expected_node_count: want 3, got %d", cfg.Cluster.ExpectedNodeCount)
	}
}
