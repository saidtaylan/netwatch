package cluster

import (
	"testing"
	"time"
)

// ── detectLatencyAnomaly ──────────────────────────────────────────────────────

func TestDetectLatencyAnomaly_Empty(t *testing.T) {
	if detectLatencyAnomaly(nil) {
		t.Error("empty slice should not be anomalous")
	}
}

func TestDetectLatencyAnomaly_SingleEntry(t *testing.T) {
	entries := []GeoLatencyEntry{{NodeName: "n1", Latency: 0.1}}
	if detectLatencyAnomaly(entries) {
		t.Error("single entry should not trigger anomaly (need ≥2 non-zero)")
	}
}

func TestDetectLatencyAnomaly_AllZero(t *testing.T) {
	entries := []GeoLatencyEntry{
		{NodeName: "n1", Latency: 0},
		{NodeName: "n2", Latency: 0},
	}
	if detectLatencyAnomaly(entries) {
		t.Error("all-zero latencies should not be anomalous")
	}
}

func TestDetectLatencyAnomaly_Normal(t *testing.T) {
	// max = 0.2, min = 0.1 → ratio 2× — below threshold of 3×
	entries := []GeoLatencyEntry{
		{NodeName: "n1", Latency: 0.1},
		{NodeName: "n2", Latency: 0.2},
	}
	if detectLatencyAnomaly(entries) {
		t.Error("2× ratio should not trigger anomaly")
	}
}

func TestDetectLatencyAnomaly_Triggered(t *testing.T) {
	// max = 0.4, min = 0.1 → 4× — exceeds threshold
	entries := []GeoLatencyEntry{
		{NodeName: "n1", Latency: 0.1},
		{NodeName: "n2", Latency: 0.4},
	}
	if !detectLatencyAnomaly(entries) {
		t.Error("4× ratio should trigger anomaly")
	}
}

func TestDetectLatencyAnomaly_OneZeroOneNonZero(t *testing.T) {
	// Only one non-zero value — not enough to compare.
	entries := []GeoLatencyEntry{
		{NodeName: "n1", Latency: 0},
		{NodeName: "n2", Latency: 0.5},
	}
	if detectLatencyAnomaly(entries) {
		t.Error("only one non-zero value — should not trigger anomaly")
	}
}

// ── regionOf ─────────────────────────────────────────────────────────────────

func TestRegionOf_TestOverride(t *testing.T) {
	m := NewTestManager("node1", []string{"node1", "node2"})
	m.SetTestRegions(map[string]string{
		"node1": "eu-west",
		"node2": "us-east",
	})

	if got := m.regionOf("node1"); got != "eu-west" {
		t.Errorf("regionOf(node1) = %q, want eu-west", got)
	}
	if got := m.regionOf("node2"); got != "us-east" {
		t.Errorf("regionOf(node2) = %q, want us-east", got)
	}
}

func TestRegionOf_UnknownNode(t *testing.T) {
	m := NewTestManager("node1", []string{"node1"})
	if got := m.regionOf("ghost"); got != "" {
		t.Errorf("regionOf(unknown) = %q, want empty", got)
	}
}

func TestRegionOf_LocalNode_FromConfig(t *testing.T) {
	m := NewTestManager("node1", []string{"node1"})
	m.cfg.Region = "ap-south"
	if got := m.regionOf("node1"); got != "ap-south" {
		t.Errorf("regionOf(local) = %q, want ap-south", got)
	}
}

func TestRegionOf_ClearOverride(t *testing.T) {
	m := NewTestManager("node1", []string{"node1"})
	m.SetTestRegions(map[string]string{"node1": "eu-west"})
	m.SetTestRegions(nil) // clear
	m.cfg.Region = "ap-south"
	if got := m.regionOf("node1"); got != "ap-south" {
		t.Errorf("after clearing override, regionOf should use cfg.Region; got %q", got)
	}
}

// ── GeoLatencyForTarget ───────────────────────────────────────────────────────

func TestGeoLatencyForTarget_Empty(t *testing.T) {
	m := NewTestManager("node1", []string{"node1"})
	snap := m.GeoLatencyForTarget("target-a")
	if snap.TargetID != "target-a" {
		t.Errorf("TargetID = %q", snap.TargetID)
	}
	if len(snap.ByNode) != 0 {
		t.Errorf("expected empty ByNode, got %d entries", len(snap.ByNode))
	}
	if snap.Anomaly {
		t.Error("empty snapshot should not be anomalous")
	}
	if snap.ComputedAt.IsZero() {
		t.Error("ComputedAt should be set")
	}
}

func TestGeoLatencyForTarget_PopulatedNoAnomaly(t *testing.T) {
	m := NewTestManager("node1", []string{"node1", "node2"})
	m.SetTestRegions(map[string]string{"node1": "eu-west", "node2": "eu-west"})

	m.SetPeerState("node1", "svc-a", GossipPayload{
		TargetID: "svc-a", State: "up", Seq: 1, NodeName: "node1",
		Latency: 0.05, Timestamp: time.Now(),
	})
	m.SetPeerState("node2", "svc-a", GossipPayload{
		TargetID: "svc-a", State: "up", Seq: 1, NodeName: "node2",
		Latency: 0.10, Timestamp: time.Now(),
	})

	snap := m.GeoLatencyForTarget("svc-a")
	if len(snap.ByNode) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(snap.ByNode))
	}
	if snap.Anomaly {
		t.Error("2× ratio should not trigger anomaly")
	}
}

func TestGeoLatencyForTarget_Anomaly(t *testing.T) {
	m := NewTestManager("node1", []string{"node1", "node2"})
	m.SetTestRegions(map[string]string{"node1": "eu-west", "node2": "us-east"})

	m.SetPeerState("node1", "svc-b", GossipPayload{
		TargetID: "svc-b", State: "up", Seq: 1, NodeName: "node1",
		Latency: 0.05, Timestamp: time.Now(),
	})
	m.SetPeerState("node2", "svc-b", GossipPayload{
		TargetID: "svc-b", State: "up", Seq: 1, NodeName: "node2",
		Latency: 0.25, Timestamp: time.Now(), // 5× min
	})

	snap := m.GeoLatencyForTarget("svc-b")
	if !snap.Anomaly {
		t.Error("5× ratio should trigger anomaly")
	}
	// Verify region labels are populated.
	for _, e := range snap.ByNode {
		if e.Region == "" {
			t.Errorf("node %q missing region label", e.NodeName)
		}
	}
}

func TestGeoLatencyForTarget_SortedByNodeName(t *testing.T) {
	m := NewTestManager("node1", []string{"node1", "node2", "node3"})
	m.SetPeerState("node3", "svc-c", GossipPayload{TargetID: "svc-c", State: "up", Seq: 1, NodeName: "node3", Latency: 0.1})
	m.SetPeerState("node1", "svc-c", GossipPayload{TargetID: "svc-c", State: "up", Seq: 1, NodeName: "node1", Latency: 0.2})
	m.SetPeerState("node2", "svc-c", GossipPayload{TargetID: "svc-c", State: "up", Seq: 1, NodeName: "node2", Latency: 0.3})

	snap := m.GeoLatencyForTarget("svc-c")
	names := make([]string, len(snap.ByNode))
	for i, e := range snap.ByNode {
		names[i] = e.NodeName
	}
	for i := 1; i < len(names); i++ {
		if names[i] < names[i-1] {
			t.Errorf("ByNode not sorted: %v", names)
			break
		}
	}
}

// ── UpdateGeoMetrics ──────────────────────────────────────────────────────────

func TestUpdateGeoMetrics_NoPanic(t *testing.T) {
	m := NewTestManager("node1", []string{"node1", "node2"})
	m.SetTestRegions(map[string]string{"node1": "eu-west", "node2": "us-east"})
	m.SetPeerState("node1", "db-1", GossipPayload{TargetID: "db-1", State: "up", Seq: 1, NodeName: "node1", Latency: 0.02})
	m.SetPeerState("node2", "db-1", GossipPayload{TargetID: "db-1", State: "up", Seq: 1, NodeName: "node2", Latency: 0.08})

	// UpdateGeoMetrics must not panic even without a real registry.
	m.UpdateGeoMetrics(map[string][3]string{
		"db-1": {"db-primary", "10.0.0.1:5432", "sql"},
	})
}

// ── probe_from_regions filter (CandidatesFor) ─────────────────────────────────

type testRegionProvider struct {
	targets []string
	pin     []string
	regions []string
}

func (p *testRegionProvider) LocalTargets() []string                          { return p.targets }
func (p *testRegionProvider) ProbeFromConstraint(_ string) []string           { return p.pin }
func (p *testRegionProvider) ProbeFromRegionsConstraint(_ string) []string    { return p.regions }

func TestCandidatesFor_ProbeFromRegions(t *testing.T) {
	m := NewTestManager("node1", []string{"node1", "node2", "node3"})
	m.SetTestAliveSet("node1", "node2", "node3")
	m.SetTestRegions(map[string]string{
		"node1": "eu-west",
		"node2": "us-east",
		"node3": "eu-west",
	})

	// All three have the target in their peer states.
	m.SetPeerState("node1", "svc", GossipPayload{TargetID: "svc", State: "up", NodeName: "node1", Seq: 1})
	m.SetPeerState("node2", "svc", GossipPayload{TargetID: "svc", State: "up", NodeName: "node2", Seq: 1})
	m.SetPeerState("node3", "svc", GossipPayload{TargetID: "svc", State: "up", NodeName: "node3", Seq: 1})

	// Restrict probing to eu-west region only.
	prov := &testRegionProvider{
		targets: []string{"svc"},
		regions: []string{"eu-west"},
	}
	m.SetLocalTargetProvider(prov)

	candidates := m.CandidatesFor("svc")
	// node2 is us-east → must be excluded.
	for _, c := range candidates {
		if c == "node2" {
			t.Error("node2 (us-east) should be excluded by probe_from_regions=eu-west")
		}
	}
	if len(candidates) != 2 {
		t.Errorf("expected 2 candidates (eu-west nodes), got %d: %v", len(candidates), candidates)
	}
}

func TestCandidatesFor_ProbeFromRegions_NoMatch(t *testing.T) {
	m := NewTestManager("node1", []string{"node1", "node2"})
	m.SetTestAliveSet("node1", "node2")
	m.SetTestRegions(map[string]string{
		"node1": "eu-west",
		"node2": "us-east",
	})
	m.SetPeerState("node1", "svc", GossipPayload{TargetID: "svc", State: "up", NodeName: "node1", Seq: 1})
	m.SetPeerState("node2", "svc", GossipPayload{TargetID: "svc", State: "up", NodeName: "node2", Seq: 1})

	prov := &testRegionProvider{
		targets: []string{"svc"},
		regions: []string{"ap-south"}, // no node is in ap-south
	}
	m.SetLocalTargetProvider(prov)

	candidates := m.CandidatesFor("svc")
	if len(candidates) != 0 {
		t.Errorf("expected 0 candidates when region filter matches nobody, got %d: %v", len(candidates), candidates)
	}
}

func TestCandidatesFor_ProbeFromRegions_Empty(t *testing.T) {
	// Empty regions list → no filtering.
	m := NewTestManager("node1", []string{"node1", "node2"})
	m.SetTestAliveSet("node1", "node2")
	m.SetTestRegions(map[string]string{
		"node1": "eu-west",
		"node2": "us-east",
	})
	m.SetPeerState("node1", "svc", GossipPayload{TargetID: "svc", State: "up", NodeName: "node1", Seq: 1})
	m.SetPeerState("node2", "svc", GossipPayload{TargetID: "svc", State: "up", NodeName: "node2", Seq: 1})

	prov := &testRegionProvider{
		targets: []string{"svc"},
		regions: nil, // no constraint
	}
	m.SetLocalTargetProvider(prov)

	candidates := m.CandidatesFor("svc")
	if len(candidates) != 2 {
		t.Errorf("expected 2 candidates with no region filter, got %d: %v", len(candidates), candidates)
	}
}
