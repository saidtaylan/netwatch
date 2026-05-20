//go:build !windows

package cluster_test

import (
	"testing"

	"github.com/saidtaylan/netwatch/internal/cluster"
)

// TestClusterConfig_DisabledAlwaysValid verifies that a disabled cluster config
// does not produce an error regardless of other field values.
func TestClusterConfig_DisabledAlwaysValid(t *testing.T) {
	cfg := cluster.Config{
		Enabled: false,
	}
	if err := cfg.Validate(); err != nil {
		t.Errorf("disabled cluster should always be valid, got: %v", err)
	}
}

// TestClusterConfig_DisabledIgnoresNodeName verifies that node_name absence is not
// an error when cluster is disabled.
func TestClusterConfig_DisabledIgnoresNodeName(t *testing.T) {
	cfg := cluster.Config{
		Enabled:  false,
		NodeName: "",
	}
	if err := cfg.Validate(); err != nil {
		t.Errorf("disabled cluster with empty node_name should be valid: %v", err)
	}
}

// TestClusterConfig_EnabledMissingNodeName verifies error when enabled but node_name is absent.
func TestClusterConfig_EnabledMissingNodeName(t *testing.T) {
	cfg := cluster.Config{
		Enabled:  true,
		NodeName: "",
		BindPort: 17960,
	}
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected error for enabled cluster without node_name")
	}
}

// TestClusterConfig_EnabledWithNodeName verifies no error with all required fields set.
func TestClusterConfig_EnabledWithNodeName(t *testing.T) {
	cfg := cluster.Config{
		Enabled:  true,
		NodeName: "node-alpha",
		BindPort: 17961,
	}
	if err := cfg.Validate(); err != nil {
		t.Errorf("expected no error, got: %v", err)
	}
}

// TestClusterConfig_BindPortZeroOK verifies that bind_port=0 (use default) is valid.
func TestClusterConfig_BindPortZeroOK(t *testing.T) {
	cfg := cluster.Config{
		Enabled:  true,
		NodeName: "node-beta",
		BindPort: 0,
	}
	if err := cfg.Validate(); err != nil {
		t.Errorf("bind_port=0 should be valid (use default): %v", err)
	}
}

// TestClusterConfig_BindPortNegative verifies error for negative bind port.
func TestClusterConfig_BindPortNegative(t *testing.T) {
	cfg := cluster.Config{
		Enabled:  true,
		NodeName: "node-gamma",
		BindPort: -1,
	}
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected error for bind_port=-1")
	}
}

// TestClusterConfig_BindPortTooLarge verifies error for port > 65535.
func TestClusterConfig_BindPortTooLarge(t *testing.T) {
	cfg := cluster.Config{
		Enabled:  true,
		NodeName: "node-delta",
		BindPort: 65536,
	}
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected error for bind_port=65536")
	}
}

// TestClusterConfig_BindPortMaxValid verifies bind_port=65535 is valid.
func TestClusterConfig_BindPortMaxValid(t *testing.T) {
	cfg := cluster.Config{
		Enabled:  true,
		NodeName: "node-epsilon",
		BindPort: 65535,
	}
	if err := cfg.Validate(); err != nil {
		t.Errorf("bind_port=65535 should be valid: %v", err)
	}
}

// TestClusterConfig_ProbeReplicationFactorNegative verifies error for negative replication factor.
func TestClusterConfig_ProbeReplicationFactorNegative(t *testing.T) {
	cfg := cluster.Config{
		Enabled:                true,
		NodeName:               "node-zeta",
		BindPort:               17962,
		ProbeReplicationFactor: -1,
	}
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected error for probe_replication_factor=-1")
	}
}

// TestClusterConfig_ProbeReplicationFactorZeroOK verifies 0 is valid (means unlimited/default).
func TestClusterConfig_ProbeReplicationFactorZeroOK(t *testing.T) {
	cfg := cluster.Config{
		Enabled:                true,
		NodeName:               "node-eta",
		BindPort:               17963,
		ProbeReplicationFactor: 0,
	}
	if err := cfg.Validate(); err != nil {
		t.Errorf("probe_replication_factor=0 should be valid: %v", err)
	}
}

// TestClusterConfig_ProbeReplicationFactorPositive verifies positive values pass.
func TestClusterConfig_ProbeReplicationFactorPositive(t *testing.T) {
	cfg := cluster.Config{
		Enabled:                true,
		NodeName:               "node-theta",
		BindPort:               17964,
		ProbeReplicationFactor: 3,
	}
	if err := cfg.Validate(); err != nil {
		t.Errorf("probe_replication_factor=3 should be valid: %v", err)
	}
}

// TestClusterConfig_ZoneEmptyOK verifies empty zone is valid.
func TestClusterConfig_ZoneEmptyOK(t *testing.T) {
	cfg := cluster.Config{
		Enabled:  true,
		NodeName: "node-iota",
		BindPort: 17965,
		Zone:     "",
	}
	if err := cfg.Validate(); err != nil {
		t.Errorf("empty zone should be valid: %v", err)
	}
}

// TestClusterConfig_ZoneAnyStringOK verifies any zone string is valid.
func TestClusterConfig_ZoneAnyStringOK(t *testing.T) {
	zones := []string{"us-east-1a", "istanbul", "eu-central", "dc1", "rack-42"}
	for _, z := range zones {
		cfg := cluster.Config{
			Enabled:  true,
			NodeName: "node-kappa",
			BindPort: 17966,
			Zone:     z,
		}
		if err := cfg.Validate(); err != nil {
			t.Errorf("zone=%q should be valid: %v", z, err)
		}
	}
}

// TestClusterConfig_InvalidKeyring verifies error for invalid base64 keyring entry.
func TestClusterConfig_InvalidKeyring(t *testing.T) {
	cfg := cluster.Config{
		Enabled:  true,
		NodeName: "node-lambda",
		BindPort: 17967,
		Keyring:  []string{"not-valid-base64!!!"},
	}
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected error for invalid base64 keyring entry")
	}
}

// TestClusterConfig_KeyringWrongLength verifies error for decoded keyring of wrong length.
func TestClusterConfig_KeyringWrongLength(t *testing.T) {
	import_base64 := "dGVzdA==" // base64("test") = 4 bytes, not 16/24/32
	cfg := cluster.Config{
		Enabled:  true,
		NodeName: "node-mu",
		BindPort: 17968,
		Keyring:  []string{import_base64},
	}
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected error for keyring with decoded length != 16/24/32")
	}
}

// TestClusterConfig_ValidKeyring16Bytes verifies AES-128 key (16 bytes) is valid.
func TestClusterConfig_ValidKeyring16Bytes(t *testing.T) {
	// 16 zero bytes in base64 = "AAAAAAAAAAAAAAAAAAAAAA=="
	import_base64 := "AAAAAAAAAAAAAAAAAAAAAA=="
	cfg := cluster.Config{
		Enabled:  true,
		NodeName: "node-nu",
		BindPort: 17969,
		Keyring:  []string{import_base64},
	}
	if err := cfg.Validate(); err != nil {
		t.Errorf("16-byte AES key should be valid: %v", err)
	}
}

// TestClusterConfig_ValidKeyring32Bytes verifies AES-256 key (32 bytes) is valid.
func TestClusterConfig_ValidKeyring32Bytes(t *testing.T) {
	// 32 zero bytes in base64
	import_base64 := "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA="
	cfg := cluster.Config{
		Enabled:  true,
		NodeName: "node-xi",
		BindPort: 17970,
		Keyring:  []string{import_base64},
	}
	if err := cfg.Validate(); err != nil {
		t.Errorf("32-byte AES key should be valid: %v", err)
	}
}

// TestClusterConfig_MinQuorumRatioField verifies the field is stored correctly (no validation in Validate).
func TestClusterConfig_FieldsStoredCorrectly(t *testing.T) {
	cfg := cluster.Config{
		Enabled:           true,
		NodeName:          "node-omicron",
		BindPort:          17971,
		Zone:              "asia-pacific",
		Region:            "ap-south",
		ExpectedNodeCount: 5,
		MinQuorumRatio:    0.6,
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("valid config returned error: %v", err)
	}
	if cfg.Zone != "asia-pacific" {
		t.Errorf("Zone: want asia-pacific, got %q", cfg.Zone)
	}
	if cfg.Region != "ap-south" {
		t.Errorf("Region: want ap-south, got %q", cfg.Region)
	}
	if cfg.ExpectedNodeCount != 5 {
		t.Errorf("ExpectedNodeCount: want 5, got %d", cfg.ExpectedNodeCount)
	}
	if cfg.MinQuorumRatio != 0.6 {
		t.Errorf("MinQuorumRatio: want 0.6, got %f", cfg.MinQuorumRatio)
	}
}
