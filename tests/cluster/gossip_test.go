//go:build !windows

package cluster_test

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/saidtaylan/netwatch/internal/cluster"
)

// TestGossipPayload_MarshalUnmarshalRoundtrip verifies GossipPayload survives JSON round-trip.
func TestGossipPayload_MarshalUnmarshalRoundtrip(t *testing.T) {
	original := cluster.GossipPayload{
		NodeName:   "node-1",
		TargetID:   "db-primary",
		TargetName: "DB Primary",
		TargetType: "tcp",
		State:      "hard_down",
		Seq:        7,
		ErrorCode:  "dial tcp: connection refused",
		Latency:    0.042,
	}

	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var decoded cluster.GossipPayload
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if decoded.NodeName != original.NodeName {
		t.Errorf("NodeName: want %q, got %q", original.NodeName, decoded.NodeName)
	}
	if decoded.TargetID != original.TargetID {
		t.Errorf("TargetID: want %q, got %q", original.TargetID, decoded.TargetID)
	}
	if decoded.TargetName != original.TargetName {
		t.Errorf("TargetName: want %q, got %q", original.TargetName, decoded.TargetName)
	}
	if decoded.TargetType != original.TargetType {
		t.Errorf("TargetType: want %q, got %q", original.TargetType, decoded.TargetType)
	}
	if decoded.State != original.State {
		t.Errorf("State: want %q, got %q", original.State, decoded.State)
	}
	if decoded.Seq != original.Seq {
		t.Errorf("Seq: want %d, got %d", original.Seq, decoded.Seq)
	}
	if decoded.ErrorCode != original.ErrorCode {
		t.Errorf("ErrorCode: want %q, got %q", original.ErrorCode, decoded.ErrorCode)
	}
	if decoded.Latency != original.Latency {
		t.Errorf("Latency: want %f, got %f", original.Latency, decoded.Latency)
	}
}

// TestGossipPayload_SoftDownState verifies soft_down state and RetryNum fields.
func TestGossipPayload_SoftDownState(t *testing.T) {
	p := cluster.GossipPayload{
		NodeName:   "node-2",
		TargetID:   "api-gw",
		TargetName: "API Gateway",
		TargetType: "http",
		State:      "soft_down",
		Seq:        2,
		RetryNum:   3,
		ErrorCode:  "context deadline exceeded",
	}

	data, err := json.Marshal(p)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var decoded cluster.GossipPayload
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if decoded.State != "soft_down" {
		t.Errorf("State: want soft_down, got %q", decoded.State)
	}
	if decoded.RetryNum != 3 {
		t.Errorf("RetryNum: want 3, got %d", decoded.RetryNum)
	}
}

// TestGossipPayload_ZeroValues verifies zero-value payload marshals/unmarshals cleanly.
func TestGossipPayload_ZeroValues(t *testing.T) {
	p := cluster.GossipPayload{}
	data, err := json.Marshal(p)
	if err != nil {
		t.Fatalf("marshal zero payload: %v", err)
	}
	var decoded cluster.GossipPayload
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal zero payload: %v", err)
	}
	if decoded.Seq != 0 {
		t.Errorf("Seq: want 0, got %d", decoded.Seq)
	}
}

// TestConfigPushPayload_MsgType verifies MsgType is "config_push".
func TestConfigPushPayload_MsgType(t *testing.T) {
	p := cluster.ConfigPushPayload{
		MsgType:      "config_push",
		FromNode:     "node-src",
		SharedConfig: json.RawMessage(`{"timeout":10}`),
		PushedAt:     time.Now(),
	}
	if p.MsgType != "config_push" {
		t.Errorf("MsgType: want config_push, got %q", p.MsgType)
	}
}

// TestConfigPushPayload_MarshalRoundtrip verifies ConfigPushPayload round-trips through JSON.
func TestConfigPushPayload_MarshalRoundtrip(t *testing.T) {
	now := time.Now().Truncate(time.Second).UTC()
	original := cluster.ConfigPushPayload{
		MsgType:      "config_push",
		FromNode:     "node-src",
		SharedConfig: json.RawMessage(`{"timeout":15,"max_retries":3}`),
		PushedAt:     now,
	}

	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var decoded cluster.ConfigPushPayload
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if decoded.MsgType != "config_push" {
		t.Errorf("MsgType: want config_push, got %q", decoded.MsgType)
	}
	if decoded.FromNode != "node-src" {
		t.Errorf("FromNode: want node-src, got %q", decoded.FromNode)
	}
	if string(decoded.SharedConfig) != `{"timeout":15,"max_retries":3}` {
		t.Errorf("SharedConfig: want %q, got %q", `{"timeout":15,"max_retries":3}`, string(decoded.SharedConfig))
	}
}

// TestConfigPushPayload_SharedConfigAsRawMessage verifies SharedConfig is treated as json.RawMessage.
func TestConfigPushPayload_SharedConfigAsRawMessage(t *testing.T) {
	nested := map[string]interface{}{
		"timeout":    20,
		"max_retries": 2,
		"cluster": map[string]interface{}{
			"expected_node_count": 3,
		},
	}
	rawBytes, err := json.Marshal(nested)
	if err != nil {
		t.Fatalf("marshal nested: %v", err)
	}

	p := cluster.ConfigPushPayload{
		MsgType:      "config_push",
		FromNode:     "node-x",
		SharedConfig: json.RawMessage(rawBytes),
		PushedAt:     time.Now(),
	}

	// Re-marshal and unmarshal to verify nested JSON preserved
	outer, err := json.Marshal(p)
	if err != nil {
		t.Fatalf("marshal outer: %v", err)
	}
	var decoded cluster.ConfigPushPayload
	if err := json.Unmarshal(outer, &decoded); err != nil {
		t.Fatalf("unmarshal outer: %v", err)
	}

	var recovered map[string]interface{}
	if err := json.Unmarshal(decoded.SharedConfig, &recovered); err != nil {
		t.Fatalf("unmarshal SharedConfig: %v", err)
	}
	if recovered["timeout"] != float64(20) {
		t.Errorf("timeout in recovered SharedConfig: want 20, got %v", recovered["timeout"])
	}
}

// TestGossipPayload_UnknownFieldsIgnored verifies forward-compatibility: extra JSON fields
// in a GossipPayload are silently ignored during unmarshal.
func TestGossipPayload_UnknownFieldsIgnored(t *testing.T) {
	data := `{
		"node_name": "node-future",
		"target_id": "svc",
		"state": "up",
		"seq": 1,
		"unknown_future_field": "some_value",
		"another_new_field": 42
	}`
	var p cluster.GossipPayload
	if err := json.Unmarshal([]byte(data), &p); err != nil {
		t.Fatalf("unmarshal with unknown fields: %v", err)
	}
	if p.NodeName != "node-future" {
		t.Errorf("NodeName: want node-future, got %q", p.NodeName)
	}
	if p.State != "up" {
		t.Errorf("State: want up, got %q", p.State)
	}
	if p.Seq != 1 {
		t.Errorf("Seq: want 1, got %d", p.Seq)
	}
}

// TestConfigPushPayload_UnknownFieldsIgnored verifies forward-compat for ConfigPushPayload.
func TestConfigPushPayload_UnknownFieldsIgnored(t *testing.T) {
	data := `{
		"msg_type": "config_push",
		"from_node": "node-src",
		"shared_config": {"timeout": 5},
		"pushed_at": "2026-01-01T00:00:00Z",
		"extra_field_for_future": "ignored"
	}`
	var p cluster.ConfigPushPayload
	if err := json.Unmarshal([]byte(data), &p); err != nil {
		t.Fatalf("unmarshal with unknown fields: %v", err)
	}
	if p.MsgType != "config_push" {
		t.Errorf("MsgType: want config_push, got %q", p.MsgType)
	}
	if p.FromNode != "node-src" {
		t.Errorf("FromNode: want node-src, got %q", p.FromNode)
	}
}

// TestGossipPayload_HighSeqValue verifies large sequence numbers are handled correctly.
func TestGossipPayload_HighSeqValue(t *testing.T) {
	const maxSeq = uint64(^uint64(0)) // max uint64
	p := cluster.GossipPayload{
		NodeName: "node-seq",
		TargetID: "target",
		State:    "hard_down",
		Seq:      maxSeq,
	}
	data, _ := json.Marshal(p)
	var decoded cluster.GossipPayload
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if decoded.Seq != maxSeq {
		t.Errorf("Seq: want %d, got %d", maxSeq, decoded.Seq)
	}
}
