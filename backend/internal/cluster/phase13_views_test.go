package cluster

import (
	"sort"
	"testing"
	"time"
)

// ── ProberAssignmentsSnapshot ────────────────────────────────────────────────

func TestProberSnapshot_ContainsAllKnownTargets(t *testing.T) {
	m := makeMgr("n1", "Z1", 2)
	setAliveForTest(m, "n1", "n2")
	seed(m, "n1", "t-local")
	seed(m, "n2", "t-peer")
	m.SetLocalTargetProvider(stubProvider{ids: []string{"t-local"}})

	snap := m.ProberAssignmentsSnapshot()
	if _, ok := snap.Assignments["t-local"]; !ok {
		t.Errorf("expected t-local in assignments, got %v", keysOf(snap.Assignments))
	}
	if _, ok := snap.Assignments["t-peer"]; !ok {
		t.Errorf("expected t-peer in assignments (gossiped), got %v", keysOf(snap.Assignments))
	}
	if snap.LocalNode != "n1" {
		t.Errorf("LocalNode: want n1, got %q", snap.LocalNode)
	}
	if snap.ReplicationFactor != 2 {
		t.Errorf("ReplicationFactor: want 2, got %d", snap.ReplicationFactor)
	}
}

func TestProberSnapshot_PrimaryIsFirstProber(t *testing.T) {
	m := makeMgr("n1", "", 3)
	setAliveForTest(m, "n1", "n2", "n3")
	for _, n := range []string{"n1", "n2", "n3"} {
		seed(m, n, "t1")
	}
	m.SetLocalTargetProvider(stubProvider{ids: []string{"t1"}})

	snap := m.ProberAssignmentsSnapshot()
	a := snap.Assignments["t1"]
	if len(a.Probers) == 0 {
		t.Fatal("no probers selected")
	}
	if a.Primary != a.Probers[0] {
		t.Errorf("Primary must be first prober; got primary=%q probers=%v", a.Primary, a.Probers)
	}
}

func TestProberSnapshot_IProbeFlagMatchesLocalIdentity(t *testing.T) {
	// 3 candidates, factor=3, so n1 is always among the picks.
	m := makeMgr("n1", "", 3)
	setAliveForTest(m, "n1", "n2", "n3")
	for _, n := range []string{"n1", "n2", "n3"} {
		seed(m, n, "t1")
	}
	m.SetLocalTargetProvider(stubProvider{ids: []string{"t1"}})

	snap := m.ProberAssignmentsSnapshot()
	a := snap.Assignments["t1"]
	if !a.IsLocalProber {
		t.Errorf("n1 should self-identify as prober when all candidates are picked")
	}
}

func TestProberSnapshot_ExposesProbeFromConstraint(t *testing.T) {
	m := makeMgr("n1", "", 3)
	setAliveForTest(m, "n1", "n2")
	seed(m, "n1", "pinned")
	seed(m, "n2", "pinned")
	m.SetLocalTargetProvider(stubProvider{
		ids:          []string{"pinned"},
		pinPerTarget: map[string][]string{"pinned": {"n1"}},
	})

	snap := m.ProberAssignmentsSnapshot()
	a := snap.Assignments["pinned"]
	if len(a.Constraint) != 1 || a.Constraint[0] != "n1" {
		t.Errorf("Constraint not surfaced; got %v", a.Constraint)
	}
	// And the actual selection respected the pin.
	if !contains(a.Probers, "n1") || len(a.Probers) != 1 {
		t.Errorf("pin should restrict probers to [n1]; got %v", a.Probers)
	}
}

// ── FleetSummarySnapshot ────────────────────────────────────────────────────

func TestFleetSummary_CountsTargetStates(t *testing.T) {
	m := makeMgr("n1", "", 3)
	setAliveForTest(m, "n1", "n2", "n3")

	// up by 3-way consensus
	for _, n := range []string{"n1", "n2", "n3"} {
		m.SetPeerState(n, "t-up", GossipPayload{
			TargetID: "t-up", NodeName: n, State: "up", Seq: 1, Timestamp: time.Now(),
		})
	}
	// hard_down by one peer
	m.SetPeerState("n2", "t-down", GossipPayload{
		TargetID: "t-down", NodeName: "n2", State: "hard_down", Seq: 1, Timestamp: time.Now(),
	})
	// bootstrap unknown only — should count as Unknown
	m.SetPeerState("n3", "t-boot", GossipPayload{
		TargetID: "t-boot", NodeName: "n3", State: "unknown", Seq: 0, Timestamp: time.Now(),
	})

	sum := m.FleetSummarySnapshot()
	if sum.Targets.Total != 3 {
		t.Errorf("Total: want 3, got %d", sum.Targets.Total)
	}
	if sum.Targets.Up != 1 {
		t.Errorf("Up: want 1, got %d", sum.Targets.Up)
	}
	if sum.Targets.HardDown != 1 {
		t.Errorf("HardDown: want 1, got %d", sum.Targets.HardDown)
	}
	if sum.Targets.Unknown != 1 {
		t.Errorf("Unknown: want 1, got %d", sum.Targets.Unknown)
	}
}

func TestFleetSummary_DownTargetsListIsCapped(t *testing.T) {
	m := makeMgr("n1", "", 3)
	setAliveForTest(m, "n1")
	// Inject more than FleetDownTargetsCap down targets.
	for i := 0; i < FleetDownTargetsCap+50; i++ {
		tid := "t-" + itoa(i)
		m.SetPeerState("n1", tid, GossipPayload{
			TargetID: tid, NodeName: "n1", State: "hard_down", Seq: 1,
			Timestamp: time.Now(),
		})
	}

	sum := m.FleetSummarySnapshot()
	if sum.Targets.HardDown != FleetDownTargetsCap+50 {
		t.Errorf("HardDown count should be exact; got %d", sum.Targets.HardDown)
	}
	if len(sum.DownTargets) != FleetDownTargetsCap {
		t.Errorf("DownTargets list should be capped at %d; got %d",
			FleetDownTargetsCap, len(sum.DownTargets))
	}
}

func TestFleetSummary_IncludesLocalProviderTargets(t *testing.T) {
	// Local node has a target nobody else has broadcast yet (bootstrap path).
	// It must still appear in the Total count.
	m := makeMgr("n1", "", 3)
	setAliveForTest(m, "n1")
	m.SetLocalTargetProvider(stubProvider{ids: []string{"t-only-local"}})

	sum := m.FleetSummarySnapshot()
	if sum.Targets.Total != 1 || sum.Targets.Unknown != 1 {
		t.Errorf("local-only target should count as Unknown; got total=%d unknown=%d",
			sum.Targets.Total, sum.Targets.Unknown)
	}
}

func TestFleetSummary_ClusterInfoReflectsConfig(t *testing.T) {
	m := &Manager{
		cfg: Config{
			NodeName:               "n1",
			Zone:                   "ist",
			ProbeReplicationFactor: 5,
			ExpectedNodeCount:      7,
			MinQuorumRatio:         0.6,
		},
		peerStates: map[string]map[string]GossipPayload{},
	}

	sum := m.FleetSummarySnapshot()
	if sum.Cluster.ReplicationFactor != 5 {
		t.Errorf("factor: want 5, got %d", sum.Cluster.ReplicationFactor)
	}
	if sum.Cluster.ExpectedNodeCount != 7 {
		t.Errorf("expected: want 7, got %d", sum.Cluster.ExpectedNodeCount)
	}
	if sum.Cluster.MinQuorumRatio != 0.6 {
		t.Errorf("ratio: want 0.6, got %v", sum.Cluster.MinQuorumRatio)
	}
}

// ── helpers ─────────────────────────────────────────────────────────────────

func keysOf(m map[string]ProberAssignment) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// Tiny itoa to avoid importing strconv just for one call.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}
