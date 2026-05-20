package cluster

import (
	"math"
	"testing"
)

// ── Hash ring ─────────────────────────────────────────────────────────────────

func TestGetResponsibleNode_Deterministic(t *testing.T) {
	m := &Manager{cfg: Config{NodeName: "node-1"}}
	m.ring = []string{"node-1", "node-2", "node-3"}

	p1, s1 := m.GetResponsibleNode("test-target")
	p2, s2 := m.GetResponsibleNode("test-target")
	if p1 != p2 || s1 != s2 {
		t.Fatalf("non-deterministic: (%s,%s) then (%s,%s)", p1, s1, p2, s2)
	}
}

func TestGetResponsibleNode_EmptyRing(t *testing.T) {
	m := &Manager{}
	p, s := m.GetResponsibleNode("any")
	if p != "" || s != "" {
		t.Errorf("empty ring: want (\"\",\"\"), got (%q,%q)", p, s)
	}
}

func TestGetResponsibleNode_SingleNode(t *testing.T) {
	m := &Manager{cfg: Config{NodeName: "solo"}}
	m.ring = []string{"solo"}

	p, s := m.GetResponsibleNode("target")
	if p != "solo" {
		t.Errorf("single-node: primary want=solo got=%q", p)
	}
	if s != "" {
		t.Errorf("single-node: secondary want=\"\" got=%q", s)
	}
}

func TestGetResponsibleNode_PrimarySecondaryDiffer(t *testing.T) {
	m := &Manager{}
	m.ring = []string{"node-1", "node-2", "node-3"}

	p, s := m.GetResponsibleNode("my-service")
	if p == s {
		t.Errorf("primary == secondary (%q) — must differ", p)
	}
}

func TestGetResponsibleNode_Distribution(t *testing.T) {
	// 8 targets should hit at least 2 different primaries with 3 nodes.
	m := &Manager{}
	m.ring = []string{"node-1", "node-2", "node-3"}
	seen := map[string]int{}
	for _, tid := range []string{"db", "cache", "api", "auth", "search", "queue", "payments", "orders"} {
		p, _ := m.GetResponsibleNode(tid)
		seen[p]++
	}
	if len(seen) < 2 {
		t.Errorf("all 8 targets hashed to 1 node — distribution broken: %v", seen)
	}
}

func TestIsResponsible_Primary(t *testing.T) {
	m := &Manager{cfg: Config{NodeName: "node-1"}}
	m.ring = []string{"node-1", "node-2", "node-3"}

	found := false
	for _, tid := range []string{"db", "cache", "api", "auth", "search", "queue", "payments", "orders", "x", "y", "z"} {
		p, _ := m.GetResponsibleNode(tid)
		if p == "node-1" {
			if !m.IsResponsible(tid) {
				t.Errorf("IsResponsible(%q) false even though node-1 is primary", tid)
			}
			found = true
			break
		}
	}
	if !found {
		t.Skip("no target mapped to node-1 as primary in test set")
	}
}

func TestIsResponsible_NotAssigned(t *testing.T) {
	// node-3 should NOT be responsible when it's neither primary nor secondary.
	m := &Manager{cfg: Config{NodeName: "node-3"}}
	m.ring = []string{"node-1", "node-2", "node-3"}

	found := false
	for _, tid := range []string{"db", "cache", "api", "auth", "search", "queue", "payments", "orders", "x", "y", "z", "1", "2", "3"} {
		p, s := m.GetResponsibleNode(tid)
		if p != "node-3" && s != "node-3" {
			if m.IsResponsible(tid) {
				t.Errorf("IsResponsible(%q) true but node-3 is neither primary(%s) nor secondary(%s)", tid, p, s)
			}
			found = true
			break
		}
	}
	if !found {
		t.Skip("all test targets assigned to node-3 — extend test set")
	}
}

// ── Quorum formula ────────────────────────────────────────────────────────────

// quorumNeeded extracts the formula from checkQuorum for unit testing.
func quorumNeeded(expected int, ratio float64) int {
	if ratio <= 0 {
		ratio = 0.5
	}
	return int(math.Floor(float64(expected)*ratio)) + 1
}

func TestQuorumFormula(t *testing.T) {
	cases := []struct {
		expected, alive int
		ratio           float64
		want            bool
		label           string
	}{
		{3, 2, 0.5, true, "3-node cluster, 2 alive → quorum (2≥2)"},
		{3, 1, 0.5, false, "3-node cluster, 1 alive → no quorum (1<2)"},
		{5, 3, 0.5, true, "5-node cluster, 3 alive → quorum (3≥3)"},
		{5, 2, 0.5, false, "5-node cluster, 2 alive → no quorum (2<3)"},
		{3, 3, 0.67, true, "3-node cluster, ratio 0.67, 3 alive → quorum (3≥3)"},
		{3, 2, 0.67, false, "3-node cluster, ratio 0.67, 2 alive → no quorum (2<3)"},
		{1, 1, 0.5, true, "single-node cluster always has quorum"},
	}
	for _, tc := range cases {
		t.Run(tc.label, func(t *testing.T) {
			got := tc.alive >= quorumNeeded(tc.expected, tc.ratio)
			if got != tc.want {
				t.Errorf("alive=%d needed=%d → %v, want %v",
					tc.alive, quorumNeeded(tc.expected, tc.ratio), got, tc.want)
			}
		})
	}
}

func TestCheckQuorum_NoExpectedCount(t *testing.T) {
	// expected_node_count=0 → quorum always healthy (standalone/unconfigured)
	m := &Manager{cfg: Config{ExpectedNodeCount: 0}}
	if !m.checkQuorum() {
		t.Error("expected always-true quorum when ExpectedNodeCount=0")
	}
}

// ── IsolatedMode ──────────────────────────────────────────────────────────────

func TestIsolatedMode_Default(t *testing.T) {
	m := &Manager{}
	if m.IsolatedMode() {
		t.Error("IsolatedMode should be false by default")
	}
}

func TestIsolatedMode_SetAndClear(t *testing.T) {
	m := &Manager{}
	m.isolated.Store(true)
	if !m.IsolatedMode() {
		t.Error("IsolatedMode should be true after Store(true)")
	}
	m.isolated.Store(false)
	if m.IsolatedMode() {
		t.Error("IsolatedMode should be false after Store(false)")
	}
}

// ── PeerStatesForTarget ───────────────────────────────────────────────────────

func TestPeerStatesForTarget(t *testing.T) {
	m := &Manager{
		peerStates: map[string]map[string]GossipPayload{
			"node-2": {"db": {TargetID: "db", State: "hard_down", NodeName: "node-2"}},
			"node-3": {"db": {TargetID: "db", State: "up", NodeName: "node-3"}},
			"node-4": {"cache": {TargetID: "cache", State: "up", NodeName: "node-4"}},
		},
	}

	got := m.PeerStatesForTarget("db")
	if len(got) != 2 {
		t.Fatalf("want 2 peer states for 'db', got %d", len(got))
	}

	stateMap := map[string]string{}
	for _, p := range got {
		stateMap[p.NodeName] = p.State
	}
	if stateMap["node-2"] != "hard_down" {
		t.Errorf("node-2 want hard_down got %q", stateMap["node-2"])
	}
	if stateMap["node-3"] != "up" {
		t.Errorf("node-3 want up got %q", stateMap["node-3"])
	}

	// 'cache' only known by node-4
	got2 := m.PeerStatesForTarget("cache")
	if len(got2) != 1 {
		t.Fatalf("want 1 peer state for 'cache', got %d", len(got2))
	}

	// unknown target → empty
	got3 := m.PeerStatesForTarget("unknown")
	if len(got3) != 0 {
		t.Errorf("want 0 peer states for unknown target, got %d", len(got3))
	}
}
