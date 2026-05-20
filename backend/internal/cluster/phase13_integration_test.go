package cluster

import (
	"fmt"
	"reflect"
	"sort"
	"sync"
	"testing"
	"time"
)

// Phase 13 integration tests — multi-manager scenarios that exercise the
// invariants Phase 13 promises (exactly-once probing, consistent assignment
// across nodes, deterministic failover) without standing up real memberlist
// network sockets. Each "node" is its own Manager configured with the same
// peerStates / aliveSet view, simulating perfect gossip convergence.

// fakeCluster builds N Managers that share peerStates, aliveSet, and zones —
// equivalent to a cluster where gossip has fully converged. Each Manager has
// its own NodeName so IsLocalProber answers from that node's POV.
type fakeCluster struct {
	managers map[string]*Manager
}

// newFakeCluster wires N managers that all agree on:
//   - the set of alive nodes (== all names),
//   - per-node zones (when provided),
//   - the inventory of every target each node "carries" in config.
//
// localTargets maps nodeName → []targetID. After construction every node sees
// the same candidate set for every targetID.
func newFakeCluster(
	t *testing.T,
	nodeNames []string,
	zones map[string]string,
	localTargets map[string][]string,
	factor int,
) *fakeCluster {
	t.Helper()
	c := &fakeCluster{managers: make(map[string]*Manager, len(nodeNames))}

	for _, name := range nodeNames {
		m := &Manager{
			cfg: Config{
				NodeName:               name,
				Zone:                   zones[name],
				ProbeReplicationFactor: factor,
			},
			peerStates: make(map[string]map[string]GossipPayload),
		}
		// Every node knows every node is alive.
		m.SetTestAliveSet(nodeNames...)
		if len(zones) > 0 {
			m.SetTestZones(zones)
		}
		// Each node's local provider reflects its own config slice of targets.
		m.SetLocalTargetProvider(stubProvider{ids: localTargets[name]})
		c.managers[name] = m
	}

	// Simulate gossip convergence: every node has received state broadcasts
	// from every other node for that node's local targets. Each entry uses
	// seq=1 / state="up" — content doesn't matter for prober selection.
	for owner, ids := range localTargets {
		for _, tid := range ids {
			for _, receiver := range nodeNames {
				if receiver == owner {
					continue // peerStates intentionally excludes self
				}
				c.managers[receiver].SetPeerState(owner, tid, GossipPayload{
					TargetID:  tid,
					NodeName:  owner,
					State:     "up",
					Seq:       1,
					Timestamp: time.Now(),
				})
			}
		}
	}

	return c
}

// allTargets returns the union of every node's local target inventory.
func allTargets(localTargets map[string][]string) []string {
	seen := map[string]bool{}
	var out []string
	for _, ids := range localTargets {
		for _, id := range ids {
			if !seen[id] {
				seen[id] = true
				out = append(out, id)
			}
		}
	}
	sort.Strings(out)
	return out
}

// ── Invariant: exactly N probers identify as IsLocalProber ──────────────────

func TestIntegration_ExactlyFactorProbersSelfIdentify(t *testing.T) {
	// 5 nodes, all carry target t1, factor=3. Across the cluster exactly 3
	// nodes must answer IsLocalProber=true. Anyone else is a duplicate or
	// missing prober — both break exactly-once alerting.
	names := []string{"a", "b", "c", "d", "e"}
	locals := map[string][]string{}
	for _, n := range names {
		locals[n] = []string{"t1"}
	}
	fc := newFakeCluster(t, names, nil, locals, 3)

	trueCount := 0
	for _, n := range names {
		if fc.managers[n].IsLocalProber("t1") {
			trueCount++
		}
	}
	if trueCount != 3 {
		t.Errorf("want exactly 3 self-identifying probers (factor=3), got %d", trueCount)
	}
}

// ── Invariant: every node computes the same prober set ─────────────────────

func TestIntegration_AllNodesAgreeOnProberSet(t *testing.T) {
	names := []string{"a", "b", "c", "d", "e", "f"}
	locals := map[string][]string{}
	for _, n := range names {
		locals[n] = []string{"shared-target"}
	}
	fc := newFakeCluster(t, names, nil, locals, 3)

	first := fc.managers["a"].SelectProbers("shared-target")
	for _, n := range names[1:] {
		got := fc.managers[n].SelectProbers("shared-target")
		if !reflect.DeepEqual(got, first) {
			t.Errorf("node %s disagrees: %v vs reference %v from a", n, got, first)
		}
	}
}

// ── Zone-aware spread is reproducible across nodes ──────────────────────────

func TestIntegration_ZoneSpreadConsistentAcrossNodes(t *testing.T) {
	// 6 nodes, 3 zones × 2 each. Every node must compute the same 3-zone
	// prober set for any given target.
	names := []string{"n1", "n2", "n3", "n4", "n5", "n6"}
	zones := map[string]string{
		"n1": "A", "n2": "A",
		"n3": "B", "n4": "B",
		"n5": "C", "n6": "C",
	}
	locals := map[string][]string{}
	for _, n := range names {
		locals[n] = []string{"zone-aware"}
	}
	fc := newFakeCluster(t, names, zones, locals, 3)

	first := fc.managers["n1"].SelectProbers("zone-aware")
	if len(first) != 3 {
		t.Fatalf("want 3 probers (one per zone), got %v", first)
	}
	// Verify zone diversity in n1's answer.
	seenZones := map[string]bool{}
	for _, n := range first {
		seenZones[zones[n]] = true
	}
	if len(seenZones) != 3 {
		t.Errorf("zone diversity broken; picks=%v zones=%v", first, seenZones)
	}
	// And every other node agrees on the exact same picks.
	for _, n := range names[1:] {
		got := fc.managers[n].SelectProbers("zone-aware")
		if !reflect.DeepEqual(got, first) {
			t.Errorf("node %s disagrees on zone-aware pick: %v vs reference %v", n, got, first)
		}
	}
}

// ── ProbeFrom (Feature 6) pin honored by every node ─────────────────────────

func TestIntegration_ProbeFromHonoredClusterWide(t *testing.T) {
	// 5 nodes know the target. Pin = {b, d}. Every node must independently
	// arrive at probers == {b, d}.
	names := []string{"a", "b", "c", "d", "e"}
	locals := map[string][]string{}
	for _, n := range names {
		locals[n] = []string{"pinned"}
	}
	fc := newFakeCluster(t, names, nil, locals, 3)

	// Install the same pin on every node's provider — operator contract:
	// probe_from must be identical across nodes that carry the target.
	for _, n := range names {
		fc.managers[n].SetLocalTargetProvider(stubProvider{
			ids:          []string{"pinned"},
			pinPerTarget: map[string][]string{"pinned": {"b", "d"}},
		})
	}

	want := []string{"b", "d"}
	for _, n := range names {
		got := fc.managers[n].SelectProbers("pinned")
		sort.Strings(got)
		if !reflect.DeepEqual(got, want) {
			t.Errorf("node %s: want pin-restricted %v, got %v", n, want, got)
		}
	}
}

// ── Failover: removing a prober promotes a fresh primary ────────────────────

func TestIntegration_PrimaryFailoverWhenNodeLeaves(t *testing.T) {
	names := []string{"a", "b", "c", "d", "e"}
	locals := map[string][]string{}
	for _, n := range names {
		locals[n] = []string{"t1"}
	}
	fc := newFakeCluster(t, names, nil, locals, 3)

	first := fc.managers["a"].SelectProbers("t1")
	if len(first) == 0 {
		t.Fatal("no probers selected initially")
	}
	originalPrimary := first[0]

	// Simulate the primary leaving on every remaining node.
	remaining := make([]string, 0, len(names)-1)
	for _, n := range names {
		if n != originalPrimary {
			remaining = append(remaining, n)
		}
	}
	for _, n := range remaining {
		fc.managers[n].SetTestAliveSet(remaining...)
		// Also clean up peerStates for the departed node — simulates
		// NotifyLeave's delete(peerStates, leaver).
		fc.managers[n].mu.Lock()
		delete(fc.managers[n].peerStates, originalPrimary)
		fc.managers[n].mu.Unlock()
	}

	// Now every remaining node must compute the same new prober set, and the
	// new primary must NOT be the departed node.
	newFirst := fc.managers[remaining[0]].SelectProbers("t1")
	for _, n := range remaining[1:] {
		got := fc.managers[n].SelectProbers("t1")
		if !reflect.DeepEqual(got, newFirst) {
			t.Errorf("node %s disagrees after failover: %v vs %v", n, got, newFirst)
		}
	}
	if len(newFirst) == 0 || newFirst[0] == originalPrimary {
		t.Errorf("departed primary %q still leads; new probers=%v", originalPrimary, newFirst)
	}
}

// ── Membership churn does not split prober set ──────────────────────────────

func TestIntegration_AddingNodeReshufflesConsistently(t *testing.T) {
	// Start with 3 nodes (all probe), then add a 4th — every existing node
	// must end up with the same new prober set (which now drops one of the
	// originals because factor=3).
	names := []string{"a", "b", "c"}
	locals := map[string][]string{
		"a": {"t1"}, "b": {"t1"}, "c": {"t1"},
	}
	fc := newFakeCluster(t, names, nil, locals, 3)

	before := fc.managers["a"].SelectProbers("t1")
	if len(before) != 3 {
		t.Fatalf("want all 3 (candidates ≤ factor), got %v", before)
	}

	// Add node d.
	expanded := []string{"a", "b", "c", "d"}
	for _, n := range expanded {
		var mgr *Manager
		if existing, ok := fc.managers[n]; ok {
			mgr = existing
		} else {
			mgr = &Manager{
				cfg:        Config{NodeName: n, ProbeReplicationFactor: 3},
				peerStates: make(map[string]map[string]GossipPayload),
			}
			mgr.SetLocalTargetProvider(stubProvider{ids: []string{"t1"}})
			fc.managers[n] = mgr
		}
		mgr.SetTestAliveSet(expanded...)
	}
	// d's state broadcast reaches a, b, c.
	for _, receiver := range []string{"a", "b", "c"} {
		fc.managers[receiver].SetPeerState("d", "t1", GossipPayload{
			TargetID: "t1", NodeName: "d", State: "up", Seq: 1, Timestamp: time.Now(),
		})
	}
	// And d learns about a, b, c.
	for _, owner := range []string{"a", "b", "c"} {
		fc.managers["d"].SetPeerState(owner, "t1", GossipPayload{
			TargetID: "t1", NodeName: owner, State: "up", Seq: 1, Timestamp: time.Now(),
		})
	}

	after := fc.managers["a"].SelectProbers("t1")
	if len(after) != 3 {
		t.Errorf("factor cap should hold post-expansion; got %v", after)
	}
	// All four nodes must compute the same set.
	for _, n := range expanded[1:] {
		got := fc.managers[n].SelectProbers("t1")
		if !reflect.DeepEqual(got, after) {
			t.Errorf("node %s disagrees post-expansion: %v vs %v", n, got, after)
		}
	}
}

// ── Concurrent IsLocalProber stress test ────────────────────────────────────

func TestIntegration_ConcurrentReadsAreSafe(t *testing.T) {
	// Stress: many goroutines calling SelectProbers / CandidatesFor under -race.
	names := []string{"a", "b", "c", "d", "e"}
	locals := map[string][]string{}
	for _, n := range names {
		locals[n] = []string{"t1", "t2", "t3"}
	}
	fc := newFakeCluster(t, names, nil, locals, 3)

	var wg sync.WaitGroup
	for _, n := range names {
		for _, tid := range []string{"t1", "t2", "t3"} {
			wg.Add(1)
			go func(mgr *Manager, target string) {
				defer wg.Done()
				for i := 0; i < 200; i++ {
					_ = mgr.SelectProbers(target)
					_ = mgr.IsLocalProber(target)
					_ = mgr.CandidatesFor(target)
				}
			}(fc.managers[n], tid)
		}
	}
	wg.Wait()
}

// ── Many targets, replication factor invariant holds ────────────────────────

func TestIntegration_FactorHoldsAcrossManyTargets(t *testing.T) {
	// 7 nodes, factor=2. For 50 targets verify the sum of IsLocalProber
	// across all nodes is exactly 50 * 2 (every target gets 2 probers,
	// each prober self-identifies once).
	names := []string{"a", "b", "c", "d", "e", "f", "g"}
	locals := map[string][]string{}
	var targets []string
	for i := 0; i < 50; i++ {
		targets = append(targets, fmt.Sprintf("t%02d", i))
	}
	for _, n := range names {
		locals[n] = targets
	}
	fc := newFakeCluster(t, names, nil, locals, 2)

	total := 0
	for _, n := range names {
		for _, tid := range targets {
			if fc.managers[n].IsLocalProber(tid) {
				total++
			}
		}
	}
	want := len(targets) * 2
	if total != want {
		t.Errorf("global prober count: want %d (50 targets × factor 2), got %d", want, total)
	}
	_ = allTargets // silence unused-helper warning under partial builds
}
