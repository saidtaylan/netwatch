package cluster

import (
	"testing"
	"time"
)

func TestConfigHashOf(t *testing.T) {
	h := ConfigHashOf([]byte("hello world"))
	if len(h) != 16 {
		t.Fatalf("expected 16-char hash, got %d: %q", len(h), h)
	}
	// Same input → same hash.
	if ConfigHashOf([]byte("hello world")) != h {
		t.Fatal("ConfigHashOf is not deterministic")
	}
	// Different input → different hash.
	if ConfigHashOf([]byte("other content")) == h {
		t.Fatal("ConfigHashOf produced collision on different input")
	}
}

func TestSetLocalConfigInfo_NoCluster(t *testing.T) {
	m := NewTestManager("node1", []string{"node1"})
	// Should be a no-op (no delegate, no broadcast queue) but must not panic.
	m.SetLocalConfigInfo("abc123", 512, time.Now())

	m.cfgMu.RLock()
	hash := m.localCfgHash
	size := m.localCfgSize
	m.cfgMu.RUnlock()

	if hash != "abc123" {
		t.Errorf("localCfgHash = %q, want abc123", hash)
	}
	if size != 512 {
		t.Errorf("localCfgSize = %d, want 512", size)
	}
}

func TestHandleConfigBroadcast_SameHash(t *testing.T) {
	m := NewTestManager("node1", []string{"node1", "node2"})
	m.SetLocalConfigInfo("aaaa1111bbbb2222", 100, time.Now())

	cb := ConfigBroadcast{
		MsgType:    msgTypeConfig,
		NodeName:   "node2",
		ConfigHash: "aaaa1111bbbb2222", // same as local
		ConfigSize: 100,
		LoadedAt:   time.Now(),
	}
	m.handleConfigBroadcast(cb)

	drifts := m.ConfigDriftDetected()
	if len(drifts) != 0 {
		t.Errorf("expected no drift, got %d", len(drifts))
	}
}

func TestHandleConfigBroadcast_DifferentHash(t *testing.T) {
	m := NewTestManager("node1", []string{"node1", "node2"})
	m.SetLocalConfigInfo("aaaa1111bbbb2222", 100, time.Now())

	cb := ConfigBroadcast{
		MsgType:    msgTypeConfig,
		NodeName:   "node2",
		ConfigHash: "zzzz9999xxxx8888", // different
		ConfigSize: 150,
		LoadedAt:   time.Now(),
	}
	m.handleConfigBroadcast(cb)

	drifts := m.ConfigDriftDetected()
	if len(drifts) != 1 {
		t.Fatalf("expected 1 drift, got %d", len(drifts))
	}
	if drifts[0].NodeName != "node2" {
		t.Errorf("drift NodeName = %q, want node2", drifts[0].NodeName)
	}
	if drifts[0].LocalHash != "aaaa1111bbbb2222" {
		t.Errorf("drift LocalHash = %q", drifts[0].LocalHash)
	}
	if drifts[0].RemoteHash != "zzzz9999xxxx8888" {
		t.Errorf("drift RemoteHash = %q", drifts[0].RemoteHash)
	}
}

func TestHandleConfigBroadcast_IgnoreSelf(t *testing.T) {
	m := NewTestManager("node1", []string{"node1"})
	m.SetLocalConfigInfo("aaaa1111bbbb2222", 100, time.Now())

	// Message from self — must be ignored.
	cb := ConfigBroadcast{
		MsgType:    msgTypeConfig,
		NodeName:   "node1",
		ConfigHash: "different_hash_xx",
	}
	m.handleConfigBroadcast(cb)

	if len(m.ConfigDriftDetected()) != 0 {
		t.Error("self-message should be ignored; drift detected unexpectedly")
	}
}

func TestConfigSyncSnapshot(t *testing.T) {
	m := NewTestManager("node1", []string{"node1", "node2", "node3"})
	loadedAt := time.Now()
	m.SetLocalConfigInfo("localHash12345678", 200, loadedAt)

	// Two peers: one in sync, one drifted.
	m.handleConfigBroadcast(ConfigBroadcast{
		MsgType:    msgTypeConfig,
		NodeName:   "node2",
		ConfigHash: "localHash12345678",
		LoadedAt:   loadedAt,
	})
	m.handleConfigBroadcast(ConfigBroadcast{
		MsgType:    msgTypeConfig,
		NodeName:   "node3",
		ConfigHash: "differentHash5678",
		LoadedAt:   loadedAt,
	})

	snap := m.ConfigSyncSnapshot()

	if snap.Self.ConfigHash != "localHash12345678" {
		t.Errorf("self hash = %q", snap.Self.ConfigHash)
	}
	if len(snap.Peers) != 2 {
		t.Fatalf("expected 2 peers, got %d", len(snap.Peers))
	}
	if snap.DriftCount != 1 {
		t.Errorf("DriftCount = %d, want 1", snap.DriftCount)
	}

	for _, p := range snap.Peers {
		switch p.NodeName {
		case "node2":
			if !p.InSync {
				t.Error("node2 should be in sync")
			}
		case "node3":
			if p.InSync {
				t.Error("node3 should not be in sync")
			}
		default:
			t.Errorf("unexpected peer %q", p.NodeName)
		}
	}
}

func TestConfigDriftDetected_NoLocalHash(t *testing.T) {
	m := NewTestManager("node1", []string{"node1", "node2"})
	// No local hash set — ConfigDriftDetected must return nil.
	m.handleConfigBroadcast(ConfigBroadcast{
		MsgType:    msgTypeConfig,
		NodeName:   "node2",
		ConfigHash: "someHash",
	})
	if m.ConfigDriftDetected() != nil {
		t.Error("without a local hash, ConfigDriftDetected should return nil")
	}
}

func TestUpdateConfigDriftMetric_NoPanic(t *testing.T) {
	m := NewTestManager("node1", []string{"node1"})
	// Must not panic even with no local config info.
	m.UpdateConfigDriftMetric()
}
