package cluster

import (
	"reflect"
	"sort"
	"testing"
	"time"
)

// ── Test helpers ─────────────────────────────────────────────────────────────

// stubProvider implements LocalTargetProvider for tests.
//
// pinPerTarget lets tests simulate `probe_from` constraints on a per-target
// basis. nil / missing entry → no constraint (current default).
type stubProvider struct {
	ids          []string
	pinPerTarget map[string][]string
}

func (s stubProvider) LocalTargets() []string { return s.ids }

func (s stubProvider) ProbeFromConstraint(targetID string) []string {
	if s.pinPerTarget == nil {
		return nil
	}
	return s.pinPerTarget[targetID]
}

func (s stubProvider) ProbeFromRegionsConstraint(_ string) []string { return nil }

// makeMgr builds a Manager with peerStates and (optionally) a local-target
// inventory, then forces the ring to "alive". m.list stays nil — aliveSet
// falls back to {cfg.NodeName} which we override below for multi-node tests.
func makeMgr(nodeName, zone string, factor int) *Manager {
	return &Manager{
		cfg:        Config{NodeName: nodeName, Zone: zone, ProbeReplicationFactor: factor},
		peerStates: make(map[string]map[string]GossipPayload),
	}
}

// seed adds a state broadcast for (nodeName, targetID) so CandidatesFor
// counts that node.
func seed(m *Manager, nodeName, targetID string) {
	m.SetPeerState(nodeName, targetID, GossipPayload{
		TargetID:  targetID,
		NodeName:  nodeName,
		State:     "up",
		Seq:       1,
		Timestamp: time.Now(),
	})
}

// setAliveForTest is a thin wrapper around SetTestAliveSet kept for
// readability in this file's table-driven tests.
func setAliveForTest(m *Manager, names ...string) { m.SetTestAliveSet(names...) }

// ── CandidatesFor ────────────────────────────────────────────────────────────

func TestCandidatesFor_EmptyWhenTargetUnknown(t *testing.T) {
	m := makeMgr("n1", "", 0)
	if got := m.CandidatesFor("ghost"); len(got) != 0 {
		t.Errorf("expected empty, got %v", got)
	}
}

func TestCandidatesFor_LocalNodeFromProvider(t *testing.T) {
	m := makeMgr("n1", "", 0)
	m.SetLocalTargetProvider(stubProvider{ids: []string{"t1"}})

	got := m.CandidatesFor("t1")
	if !reflect.DeepEqual(got, []string{"n1"}) {
		t.Errorf("want [n1], got %v", got)
	}
}

func TestCandidatesFor_FromPeerStates(t *testing.T) {
	m := makeMgr("n1", "", 0)
	setAliveForTest(m, "n1", "n2", "n3")
	seed(m, "n2", "t1")
	seed(m, "n3", "t1")

	got := m.CandidatesFor("t1")
	sort.Strings(got)
	want := []string{"n2", "n3"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("want %v, got %v", want, got)
	}
}

func TestCandidatesFor_DedupesLocalAndPeer(t *testing.T) {
	// Local node has the target in config AND has already broadcast a state.
	// The result must contain it only once.
	m := makeMgr("n1", "", 0)
	setAliveForTest(m, "n1", "n2")
	seed(m, "n1", "t1")
	seed(m, "n2", "t1")
	m.SetLocalTargetProvider(stubProvider{ids: []string{"t1"}})

	got := m.CandidatesFor("t1")
	want := []string{"n1", "n2"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("want %v, got %v", want, got)
	}
}

func TestCandidatesFor_FiltersDeadPeers(t *testing.T) {
	m := makeMgr("n1", "", 0)
	setAliveForTest(m, "n1", "n2") // n3 absent → considered dead
	seed(m, "n2", "t1")
	seed(m, "n3", "t1")

	got := m.CandidatesFor("t1")
	if !reflect.DeepEqual(got, []string{"n2"}) {
		t.Errorf("dead n3 should be filtered, got %v", got)
	}
}

func TestCandidatesFor_SortedDeterministically(t *testing.T) {
	m := makeMgr("nb", "", 0)
	setAliveForTest(m, "na", "nb", "nc", "nd")
	seed(m, "nd", "t1")
	seed(m, "na", "t1")
	seed(m, "nc", "t1")

	got := m.CandidatesFor("t1")
	want := []string{"na", "nc", "nd"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("want sorted %v, got %v", want, got)
	}
}

// ── hashCandidateOrder ───────────────────────────────────────────────────────

func TestHashCandidateOrder_Deterministic(t *testing.T) {
	candidates := []string{"a", "b", "c", "d", "e"}
	first := hashCandidateOrder(candidates, "target-xyz")
	for i := 0; i < 50; i++ {
		again := hashCandidateOrder(candidates, "target-xyz")
		if !reflect.DeepEqual(first, again) {
			t.Fatalf("non-deterministic: %v vs %v", first, again)
		}
	}
}

func TestHashCandidateOrder_StartsAtHashPosition(t *testing.T) {
	candidates := []string{"a", "b", "c", "d", "e"}
	out := hashCandidateOrder(candidates, "target-1")
	if len(out) != len(candidates) {
		t.Fatalf("length mismatch: %d vs %d", len(out), len(candidates))
	}
	// Every input must appear exactly once.
	seen := map[string]bool{}
	for _, n := range out {
		if seen[n] {
			t.Fatalf("duplicate %q in %v", n, out)
		}
		seen[n] = true
	}
}

func TestHashCandidateOrder_EmptyInput(t *testing.T) {
	if got := hashCandidateOrder(nil, "x"); got != nil {
		t.Errorf("expected nil, got %v", got)
	}
}

// ── zoneAwarePick (3-tier) ───────────────────────────────────────────────────

func zoneFunc(m map[string]string) func(string) string {
	return func(n string) string { return m[n] }
}

func TestZoneAwarePick_FullDiversity(t *testing.T) {
	// 6 nodes, 3 zones × 2 each, factor=3 → must pick 3 distinct zones.
	zones := map[string]string{
		"a1": "A", "a2": "A",
		"b1": "B", "b2": "B",
		"c1": "C", "c2": "C",
	}
	sorted := []string{"a1", "a2", "b1", "b2", "c1", "c2"}
	picked := zoneAwarePick(sorted, 3, zoneFunc(zones))
	if len(picked) != 3 {
		t.Fatalf("want 3 picks, got %d (%v)", len(picked), picked)
	}
	seen := map[string]bool{}
	for _, n := range picked {
		seen[zones[n]] = true
	}
	if len(seen) != 3 {
		t.Errorf("expected 3 distinct zones, got %v (picks=%v)", seen, picked)
	}
}

func TestZoneAwarePick_ZoneRepeatBeatsZoneless(t *testing.T) {
	// 2 zones × 3 nodes each, factor=3.
	// Expected: tier-1 picks one from each zone (2 picks).
	// Tier-2 fills with another zone-tagged node.
	// Zero zone-less nodes here so we only check zone repeat.
	zones := map[string]string{
		"a1": "A", "a2": "A", "a3": "A",
		"b1": "B", "b2": "B", "b3": "B",
	}
	sorted := []string{"a1", "a2", "a3", "b1", "b2", "b3"}
	picked := zoneAwarePick(sorted, 3, zoneFunc(zones))
	if len(picked) != 3 {
		t.Fatalf("want 3 picks, got %v", picked)
	}
	// First two must be distinct zones (tier 1).
	if zones[picked[0]] == zones[picked[1]] {
		t.Errorf("tier 1 should yield distinct zones, got %v (zones %s,%s)",
			picked, zones[picked[0]], zones[picked[1]])
	}
}

func TestZoneAwarePick_ZonelessSkippedWhenZonedAvailable(t *testing.T) {
	// 5 zone-tagged + 5 zone-less, factor=3 → all picks must be zone-tagged.
	zones := map[string]string{
		"a1": "A", "a2": "A", "b1": "B", "c1": "C", "d1": "D",
		"x1": "", "x2": "", "x3": "", "x4": "", "x5": "",
	}
	sorted := []string{"a1", "a2", "b1", "c1", "d1", "x1", "x2", "x3", "x4", "x5"}
	picked := zoneAwarePick(sorted, 3, zoneFunc(zones))
	if len(picked) != 3 {
		t.Fatalf("want 3, got %v", picked)
	}
	for _, n := range picked {
		if zones[n] == "" {
			t.Errorf("zone-less %q should not be picked when zoned available: %v", n, picked)
		}
	}
}

func TestZoneAwarePick_ZonelessFallbackWhenMustFill(t *testing.T) {
	// 2 zone-tagged + 5 zone-less, factor=3.
	// Tier 1 picks both zoned (2). Tier 2 has nothing more zoned.
	// Tier 3 must contribute the 3rd from the zone-less pool.
	zones := map[string]string{
		"a1": "A", "b1": "B",
		"x1": "", "x2": "", "x3": "", "x4": "", "x5": "",
	}
	sorted := []string{"a1", "b1", "x1", "x2", "x3", "x4", "x5"}
	picked := zoneAwarePick(sorted, 3, zoneFunc(zones))
	if len(picked) != 3 {
		t.Fatalf("want 3, got %v", picked)
	}
	zoned, zoneless := 0, 0
	for _, n := range picked {
		if zones[n] == "" {
			zoneless++
		} else {
			zoned++
		}
	}
	if zoned != 2 || zoneless != 1 {
		t.Errorf("want 2 zoned + 1 zoneless, got zoned=%d zoneless=%d (%v)",
			zoned, zoneless, picked)
	}
}

func TestZoneAwarePick_AllZoneless_LegacyMode(t *testing.T) {
	// No zones declared anywhere → behave exactly like hash order.
	zones := map[string]string{"x1": "", "x2": "", "x3": "", "x4": "", "x5": ""}
	sorted := []string{"x1", "x2", "x3", "x4", "x5"}
	picked := zoneAwarePick(sorted, 3, zoneFunc(zones))
	if !reflect.DeepEqual(picked, []string{"x1", "x2", "x3"}) {
		t.Errorf("legacy mode should follow hash order, got %v", picked)
	}
}

func TestZoneAwarePick_FactorLargerThanCandidates(t *testing.T) {
	zones := map[string]string{"a1": "A", "b1": "B"}
	sorted := []string{"a1", "b1"}
	picked := zoneAwarePick(sorted, 5, zoneFunc(zones))
	if len(picked) != 2 {
		t.Errorf("want 2 (all candidates), got %v", picked)
	}
}

func TestZoneAwarePick_ZeroFactorReturnsNil(t *testing.T) {
	if got := zoneAwarePick([]string{"a"}, 0, func(string) string { return "" }); got != nil {
		t.Errorf("factor=0 must return nil, got %v", got)
	}
}

func TestZoneAwarePick_EmptyInputReturnsNil(t *testing.T) {
	if got := zoneAwarePick(nil, 3, func(string) string { return "" }); got != nil {
		t.Errorf("empty input must return nil, got %v", got)
	}
}

// ── SelectProbers integration ────────────────────────────────────────────────

func TestSelectProbers_AllCandidatesWhenBelowFactor(t *testing.T) {
	m := makeMgr("n1", "", 5) // factor=5
	setAliveForTest(m, "n1", "n2", "n3")
	seed(m, "n1", "t1")
	seed(m, "n2", "t1")
	seed(m, "n3", "t1")

	got := m.SelectProbers("t1")
	if len(got) != 3 {
		t.Fatalf("want all 3, got %v", got)
	}
}

func TestSelectProbers_DeterministicWith100Iterations(t *testing.T) {
	m := makeMgr("n3", "", 3)
	setAliveForTest(m, "n1", "n2", "n3", "n4", "n5")
	for _, n := range []string{"n1", "n2", "n3", "n4", "n5"} {
		seed(m, n, "t-stable")
	}

	first := m.SelectProbers("t-stable")
	for i := 0; i < 100; i++ {
		next := m.SelectProbers("t-stable")
		if !reflect.DeepEqual(first, next) {
			t.Fatalf("non-deterministic on iter %d: %v vs %v", i, first, next)
		}
	}
	if len(first) != 3 {
		t.Errorf("want 3 probers, got %v", first)
	}
}

func TestSelectProbers_RespectsFactor(t *testing.T) {
	m := makeMgr("n1", "", 2)
	setAliveForTest(m, "n1", "n2", "n3", "n4")
	for _, n := range []string{"n1", "n2", "n3", "n4"} {
		seed(m, n, "t1")
	}

	got := m.SelectProbers("t1")
	if len(got) != 2 {
		t.Errorf("want 2 (factor), got %v", got)
	}
}

func TestSelectProbers_EmptyWhenNoCandidates(t *testing.T) {
	m := makeMgr("n1", "", 3)
	if got := m.SelectProbers("nobody-knows"); len(got) != 0 {
		t.Errorf("want empty, got %v", got)
	}
}

func TestSelectProbers_ZoneAwareIntegration(t *testing.T) {
	// 4 candidates, 2 zones, 1 zone-less. Factor=2.
	// zoneAwarePick must pick the 2 zoned nodes (one per zone), not zone-less.
	m := makeMgr("n-a", "Z1", 2)
	setAliveForTest(m, "n-a", "n-b", "n-c", "n-d")
	for _, n := range []string{"n-a", "n-b", "n-c", "n-d"} {
		seed(m, n, "t1")
	}
	// Inject zones — zoneOf consults testZoneOverride first.
	zoneMap := map[string]string{
		"n-a": "Z1",
		"n-b": "Z2",
		// n-c, n-d zone-less
	}
	m.SetTestZones(zoneMap)

	got := m.SelectProbers("t1")
	if len(got) != 2 {
		t.Fatalf("want 2 picks, got %v", got)
	}
	zones := map[string]bool{}
	for _, n := range got {
		z := zoneMap[n]
		if z == "" {
			t.Errorf("zone-less %q should not be in pick when zoned alternatives exist: %v", n, got)
		}
		zones[z] = true
	}
	if len(zones) != 2 {
		t.Errorf("want 2 distinct zones, got %v (picks=%v)", zones, got)
	}
}

// ── IsLocalProber ────────────────────────────────────────────────────────────

func TestIsLocalProber_TrueWhenSelected(t *testing.T) {
	m := makeMgr("n2", "", 3)
	setAliveForTest(m, "n1", "n2", "n3")
	for _, n := range []string{"n1", "n2", "n3"} {
		seed(m, n, "t1")
	}
	if !m.IsLocalProber("t1") {
		t.Errorf("want true (3 candidates, factor=3 → all included)")
	}
}

func TestIsLocalProber_FalseWhenNotSelected(t *testing.T) {
	m := makeMgr("n5", "", 2)
	setAliveForTest(m, "n1", "n2", "n3", "n4", "n5")
	for _, n := range []string{"n1", "n2", "n3", "n4", "n5"} {
		seed(m, n, "t1")
	}
	// We don't know in advance which 2 of 5 the hash picks, so just check
	// that exactly 2 nodes report true (consistency property).
	trueCount := 0
	for _, n := range []string{"n1", "n2", "n3", "n4", "n5"} {
		mm := makeMgr(n, "", 2)
		// Copy state to a fresh manager from this node's perspective.
		setAliveForTest(mm, "n1", "n2", "n3", "n4", "n5")
		for _, peer := range []string{"n1", "n2", "n3", "n4", "n5"} {
			seed(mm, peer, "t1")
		}
		if mm.IsLocalProber("t1") {
			trueCount++
		}
	}
	if trueCount != 2 {
		t.Errorf("exactly 2 nodes should self-identify as probers, got %d", trueCount)
	}
}

func TestIsLocalProber_FalseForUnknownTarget(t *testing.T) {
	m := makeMgr("n1", "", 3)
	if m.IsLocalProber("ghost") {
		t.Error("unknown target should not select any prober")
	}
}

// ── ProbeFrom constraint (Active Probe Delegation) ───────────────────────────

func TestProbeFrom_FiltersCandidatesToAllowedList(t *testing.T) {
	// 4 nodes know the target, but probe_from pins it to {n2, n3}.
	// Candidates must shrink to those two regardless of factor.
	m := makeMgr("n1", "", 5)
	setAliveForTest(m, "n1", "n2", "n3", "n4")
	for _, n := range []string{"n1", "n2", "n3", "n4"} {
		seed(m, n, "t-pinned")
	}
	m.SetLocalTargetProvider(stubProvider{
		ids: []string{"t-pinned"},
		pinPerTarget: map[string][]string{
			"t-pinned": {"n2", "n3"},
		},
	})

	got := m.CandidatesFor("t-pinned")
	want := []string{"n2", "n3"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("constraint should restrict candidates to %v, got %v", want, got)
	}
}

func TestProbeFrom_EmptyConstraintMeansNoFilter(t *testing.T) {
	m := makeMgr("n1", "", 5)
	setAliveForTest(m, "n1", "n2", "n3")
	for _, n := range []string{"n1", "n2", "n3"} {
		seed(m, n, "t1")
	}
	// Empty pin list → behaves as if not set.
	m.SetLocalTargetProvider(stubProvider{
		ids: []string{"t1"},
		pinPerTarget: map[string][]string{
			"t1": nil,
		},
	})
	got := m.CandidatesFor("t1")
	if len(got) != 3 {
		t.Errorf("empty constraint should preserve all candidates, got %v", got)
	}
}

func TestProbeFrom_AllowsUnknownNodesToFilterAway(t *testing.T) {
	// probe_from references a node that does not currently know the target.
	// Result: candidates contain only the intersection — possibly empty.
	m := makeMgr("n1", "", 3)
	setAliveForTest(m, "n1", "n2")
	seed(m, "n1", "t1")
	seed(m, "n2", "t1")
	m.SetLocalTargetProvider(stubProvider{
		ids: []string{"t1"},
		pinPerTarget: map[string][]string{
			"t1": {"ghost"},
		},
	})

	got := m.CandidatesFor("t1")
	if len(got) != 0 {
		t.Errorf("non-existent pin should yield empty candidates, got %v", got)
	}
}

func TestProbeFrom_DeadPinnedNodeStillFiltered(t *testing.T) {
	// Pin allows n2 but n2 is dead → result excludes n2.
	m := makeMgr("n1", "", 3)
	setAliveForTest(m, "n1", "n3") // n2 absent
	seed(m, "n1", "t1")
	seed(m, "n2", "t1")
	seed(m, "n3", "t1")
	m.SetLocalTargetProvider(stubProvider{
		ids: []string{"t1"},
		pinPerTarget: map[string][]string{
			"t1": {"n1", "n2", "n3"},
		},
	})

	got := m.CandidatesFor("t1")
	want := []string{"n1", "n3"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("dead pinned n2 should be excluded; want %v got %v", want, got)
	}
}

func TestProbeFrom_OverridesZoneSpread(t *testing.T) {
	// Even with zones declared, probe_from takes precedence — only pinned
	// nodes are eligible, regardless of which zones they fall in.
	m := makeMgr("n1", "Z1", 3)
	setAliveForTest(m, "n1", "n2", "n3", "n4")
	for _, n := range []string{"n1", "n2", "n3", "n4"} {
		seed(m, n, "t1")
	}
	m.SetTestZones(map[string]string{
		"n1": "Z1", "n2": "Z2", "n3": "Z3", "n4": "Z4",
	})
	m.SetLocalTargetProvider(stubProvider{
		ids: []string{"t1"},
		pinPerTarget: map[string][]string{
			"t1": {"n1", "n2"}, // only two pinned, even though 4 zones exist
		},
	})

	got := m.SelectProbers("t1")
	want := []string{"n1", "n2"}
	sort.Strings(got)
	if !reflect.DeepEqual(got, want) {
		t.Errorf("pin should override zone spread; want %v got %v", want, got)
	}
}
