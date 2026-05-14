//go:build !windows

package engine_test

import (
	"testing"

	"github.com/saidtaylan/netwatch/internal/engine"
)

// ptr helpers for pointer-typed fields.
func ptrInt(v int) *int { return &v }

// TestSharedConfig_AppliedFieldsEmpty verifies zero-value SharedConfig returns empty slice.
func TestSharedConfig_AppliedFieldsEmpty(t *testing.T) {
	sc := engine.SharedConfig{}
	fields := engine.AppliedFields(sc)
	if len(fields) != 0 {
		t.Errorf("expected empty fields for zero-value SharedConfig, got %v", fields)
	}
}

// TestSharedConfig_AppliedFieldsTimeout verifies Timeout field is reported.
func TestSharedConfig_AppliedFieldsTimeout(t *testing.T) {
	sc := engine.SharedConfig{Timeout: 10}
	fields := engine.AppliedFields(sc)
	if !containsField(fields, "timeout") {
		t.Errorf("expected timeout in fields, got %v", fields)
	}
}

// TestSharedConfig_AppliedFieldsMaxRetries verifies MaxRetries field is reported.
func TestSharedConfig_AppliedFieldsMaxRetries(t *testing.T) {
	v := 3
	sc := engine.SharedConfig{MaxRetries: &v}
	fields := engine.AppliedFields(sc)
	if !containsField(fields, "max_retries") {
		t.Errorf("expected max_retries in fields, got %v", fields)
	}
}

// TestSharedConfig_AppliedFieldsRetryInterval verifies RetryIntervalSec field is reported.
func TestSharedConfig_AppliedFieldsRetryInterval(t *testing.T) {
	v := 30
	sc := engine.SharedConfig{RetryIntervalSec: &v}
	fields := engine.AppliedFields(sc)
	if !containsField(fields, "retry_interval_sec") {
		t.Errorf("expected retry_interval_sec in fields, got %v", fields)
	}
}

// TestSharedConfig_AppliedFieldsTickerInterval verifies TickerIntervalSec field is reported.
func TestSharedConfig_AppliedFieldsTickerInterval(t *testing.T) {
	v := 5
	sc := engine.SharedConfig{TickerIntervalSec: &v}
	fields := engine.AppliedFields(sc)
	if !containsField(fields, "ticker_interval_sec") {
		t.Errorf("expected ticker_interval_sec in fields, got %v", fields)
	}
}

// TestSharedConfig_AppliedFieldsProbeInterval verifies ProbeIntervalSec field is reported.
func TestSharedConfig_AppliedFieldsProbeInterval(t *testing.T) {
	v := 60
	sc := engine.SharedConfig{ProbeIntervalSec: &v}
	fields := engine.AppliedFields(sc)
	if !containsField(fields, "probe_interval_sec") {
		t.Errorf("expected probe_interval_sec in fields, got %v", fields)
	}
}

// TestSharedConfig_AppliedFieldsReloadInterval verifies ReloadIntervalSec field is reported.
func TestSharedConfig_AppliedFieldsReloadInterval(t *testing.T) {
	v := 30
	sc := engine.SharedConfig{ReloadIntervalSec: &v}
	fields := engine.AppliedFields(sc)
	if !containsField(fields, "reload_interval_sec") {
		t.Errorf("expected reload_interval_sec in fields, got %v", fields)
	}
}

// TestSharedConfig_AppliedFieldsWatchdog verifies WatchdogThresholdSec field is reported.
func TestSharedConfig_AppliedFieldsWatchdog(t *testing.T) {
	v := 120
	sc := engine.SharedConfig{WatchdogThresholdSec: &v}
	fields := engine.AppliedFields(sc)
	if !containsField(fields, "watchdog_threshold_sec") {
		t.Errorf("expected watchdog_threshold_sec in fields, got %v", fields)
	}
}

// TestSharedConfig_AppliedFieldsNotifications verifies Notifications field is reported.
func TestSharedConfig_AppliedFieldsNotifications(t *testing.T) {
	sc := engine.SharedConfig{
		Notifications: map[string]engine.AlertChannelConfig{
			"ops": {Type: "script"},
		},
	}
	fields := engine.AppliedFields(sc)
	if !containsField(fields, "notifications") {
		t.Errorf("expected notifications in fields, got %v", fields)
	}
}

// TestSharedConfig_AppliedFieldsDefaultNotify verifies DefaultNotify field is reported.
func TestSharedConfig_AppliedFieldsDefaultNotify(t *testing.T) {
	sc := engine.SharedConfig{DefaultNotify: []string{"ops"}}
	fields := engine.AppliedFields(sc)
	if !containsField(fields, "default_notify") {
		t.Errorf("expected default_notify in fields, got %v", fields)
	}
}

// TestSharedConfig_AppliedFieldsCluster verifies Cluster field is reported as "cluster.*".
func TestSharedConfig_AppliedFieldsCluster(t *testing.T) {
	sc := engine.SharedConfig{
		Cluster: &engine.SharedClusterConfig{
			ExpectedNodeCount: 3,
		},
	}
	fields := engine.AppliedFields(sc)
	if !containsField(fields, "cluster.*") {
		t.Errorf("expected cluster.* in fields, got %v", fields)
	}
}

// TestSharedConfig_AppliedFieldsAllSet verifies all fields reported when fully populated.
func TestSharedConfig_AppliedFieldsAllSet(t *testing.T) {
	maxR := 2
	retryI := 10
	tickerI := 5
	probeI := 30
	reloadI := 60
	watchdog := 120
	sc := engine.SharedConfig{
		Timeout:              5,
		MaxRetries:           &maxR,
		RetryIntervalSec:     &retryI,
		TickerIntervalSec:    &tickerI,
		ProbeIntervalSec:     &probeI,
		ReloadIntervalSec:    &reloadI,
		WatchdogThresholdSec: &watchdog,
		Notifications:        map[string]engine.AlertChannelConfig{"ops": {Type: "script"}},
		DefaultNotify:        []string{"ops"},
		Cluster: &engine.SharedClusterConfig{
			ExpectedNodeCount: 3,
			MinQuorumRatio:    0.5,
		},
	}
	fields := engine.AppliedFields(sc)
	expected := []string{
		"timeout", "max_retries", "retry_interval_sec", "ticker_interval_sec",
		"probe_interval_sec", "reload_interval_sec", "watchdog_threshold_sec",
		"notifications", "default_notify", "cluster.*",
	}
	for _, want := range expected {
		if !containsField(fields, want) {
			t.Errorf("expected %q in fields; got %v", want, fields)
		}
	}
	if len(fields) != len(expected) {
		t.Errorf("field count: want %d, got %d: %v", len(expected), len(fields), fields)
	}
}

// TestSharedClusterConfig_Fields verifies SharedClusterConfig struct has correct fields.
func TestSharedClusterConfig_Fields(t *testing.T) {
	scc := engine.SharedClusterConfig{
		Keyring:                []string{"key1", "key2"},
		Peers:                  []string{"10.0.0.1:7946", "10.0.0.2:7946"},
		ExpectedNodeCount:      5,
		MinQuorumRatio:         0.6,
		ProbeReplicationFactor: 3,
		MinProbeConfirmations:  2,
	}
	if len(scc.Keyring) != 2 {
		t.Errorf("Keyring: want 2, got %d", len(scc.Keyring))
	}
	if len(scc.Peers) != 2 {
		t.Errorf("Peers: want 2, got %d", len(scc.Peers))
	}
	if scc.ExpectedNodeCount != 5 {
		t.Errorf("ExpectedNodeCount: want 5, got %d", scc.ExpectedNodeCount)
	}
	if scc.MinQuorumRatio != 0.6 {
		t.Errorf("MinQuorumRatio: want 0.6, got %f", scc.MinQuorumRatio)
	}
	if scc.ProbeReplicationFactor != 3 {
		t.Errorf("ProbeReplicationFactor: want 3, got %d", scc.ProbeReplicationFactor)
	}
	if scc.MinProbeConfirmations != 2 {
		t.Errorf("MinProbeConfirmations: want 2, got %d", scc.MinProbeConfirmations)
	}
}

// TestSharedConfig_ClusterNilMeansNoClusterField verifies nil Cluster → no cluster.* field.
func TestSharedConfig_ClusterNilMeansNoClusterField(t *testing.T) {
	sc := engine.SharedConfig{
		Timeout: 5,
	}
	fields := engine.AppliedFields(sc)
	if containsField(fields, "cluster.*") {
		t.Errorf("unexpected cluster.* in fields when Cluster is nil: %v", fields)
	}
}

// TestSharedConfig_AlertChannelConfig verifies AlertChannelConfig struct.
func TestSharedConfig_AlertChannelConfig(t *testing.T) {
	acc := engine.AlertChannelConfig{
		Type:       "webhook",
		Parameters: map[string]string{"url": "https://example.com/hook"},
	}
	if acc.Type != "webhook" {
		t.Errorf("Type: want webhook, got %q", acc.Type)
	}
	if acc.Parameters["url"] != "https://example.com/hook" {
		t.Errorf("Parameters url: want https://example.com/hook, got %q", acc.Parameters["url"])
	}
}

// containsField is a helper to check if a slice contains a specific string.
func containsField(fields []string, want string) bool {
	for _, f := range fields {
		if f == want {
			return true
		}
	}
	return false
}
