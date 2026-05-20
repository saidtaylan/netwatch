package cluster

import (
	"reflect"
	"testing"
)

// stubProviderWith lets a test declare both pin and region constraints
// per-target via maps.
type stubProviderWith struct {
	ids       []string
	pinMap    map[string][]string
	regionMap map[string][]string
}

func (s stubProviderWith) LocalTargets() []string { return s.ids }
func (s stubProviderWith) ProbeFromConstraint(id string) []string {
	if s.pinMap == nil {
		return nil
	}
	return s.pinMap[id]
}
func (s stubProviderWith) ProbeFromRegionsConstraint(id string) []string {
	if s.regionMap == nil {
		return nil
	}
	return s.regionMap[id]
}

// ── OrphanedLocalTargets ──────────────────────────────────────────────────────

func TestOrphanedLocalTargets_NoProvider(t *testing.T) {
	m := NewTestManager("n1", []string{"n1"})
	if got := m.OrphanedLocalTargets(); got != nil {
		t.Errorf("expected nil with no provider, got %v", got)
	}
}

func TestOrphanedLocalTargets_AllAssigned(t *testing.T) {
	m := makeMgr("n1", "", 3)
	m.SetTestAliveSet("n1", "n2")
	seed(m, "n2", "t1")
	m.SetLocalTargetProvider(stubProvider{ids: []string{"t1"}})

	if got := m.OrphanedLocalTargets(); len(got) != 0 {
		t.Errorf("expected no orphans, got %v", got)
	}
}

func TestOrphanedLocalTargets_PinToDeadNode(t *testing.T) {
	// Pin t1 to n9 — a node that never joined → orphan.
	m := makeMgr("n1", "", 3)
	m.SetTestAliveSet("n1", "n2")
	seed(m, "n2", "t1")
	m.SetLocalTargetProvider(stubProviderWith{
		ids:    []string{"t1", "t2"},
		pinMap: map[string][]string{"t1": {"n9"}},
	})
	seed(m, "n1", "t2")

	got := m.OrphanedLocalTargets()
	want := []string{"t1"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestOrphanedLocalTargets_RegionFilterEmpty(t *testing.T) {
	// probe_from_regions: ["typo"] → no match → orphan.
	m := makeMgr("n1", "eu-west", 3)
	m.SetTestAliveSet("n1", "n2")
	m.SetTestRegions(map[string]string{"n1": "eu-west", "n2": "us-east"})
	seed(m, "n1", "t1")
	seed(m, "n2", "t1")

	m.SetLocalTargetProvider(stubProviderWith{
		ids:       []string{"t1"},
		regionMap: map[string][]string{"t1": {"typo-region"}},
	})

	got := m.OrphanedLocalTargets()
	if !reflect.DeepEqual(got, []string{"t1"}) {
		t.Errorf("expected [t1] orphan, got %v", got)
	}
}

func TestOrphanedLocalTargets_Recovers_WhenPinNodeJoins(t *testing.T) {
	// Initially n9 not alive → orphan. Then n9 joins → not orphan.
	m := makeMgr("n1", "", 3)
	m.SetTestAliveSet("n1")
	seed(m, "n1", "t1")
	m.SetLocalTargetProvider(stubProviderWith{
		ids:    []string{"t1"},
		pinMap: map[string][]string{"t1": {"n9"}},
	})

	if got := m.OrphanedLocalTargets(); !reflect.DeepEqual(got, []string{"t1"}) {
		t.Fatalf("expected orphan initially, got %v", got)
	}

	// n9 joins and broadcasts state for t1.
	m.SetTestAliveSet("n1", "n9")
	seed(m, "n9", "t1")

	if got := m.OrphanedLocalTargets(); len(got) != 0 {
		t.Errorf("expected orphan to recover, got %v", got)
	}
}

// ── UpdateNodeMeta (test-mode behaviour) ─────────────────────────────────────

func TestUpdateNodeMeta_NoListIsSafe(t *testing.T) {
	// Manager with m.list == nil → UpdateNodeMeta is a no-op and must not panic.
	m := NewTestManager("n1", []string{"n1"})
	if err := m.UpdateNodeMeta("eu-west", "europe"); err != nil {
		t.Errorf("expected nil error with no list, got %v", err)
	}
}
