// Probe ownership selection — Phase 13.
//
// This file owns the question "for a given target, which nodes should probe it?"
// The answer is computed by every node independently and must agree across the
// cluster, so it is deterministic and depends only on:
//
//   - the candidate set (nodes that have the target in their config), and
//   - each candidate's zone label (from memberlist NodeMeta).
//
// No new gossip messages are introduced. Candidate membership is derived from
// existing GossipPayload state broadcasts plus a LocalTargetProvider that the
// engine registers so the local node is visible before its first probe runs.
package cluster

import (
	"sort"

	"github.com/hashicorp/memberlist"
)

// ── LocalTargetProvider ──────────────────────────────────────────────────────

// LocalTargetProvider is implemented by the engine to declare which target
// IDs exist in this node's config. The cluster layer cannot import the engine
// (it would create a cycle), so the engine pushes the inventory in via
// SetLocalTargetProvider.
//
// Both methods must be cheap to call — CandidatesFor may invoke them on every
// prober assignment recomputation. Returning fresh slices on each call is
// fine; callers iterate them and do not retain the reference.
type LocalTargetProvider interface {
	// LocalTargets returns the target IDs (Target.key()) that this node has
	// in its current config. Order is not important.
	LocalTargets() []string

	// ProbeFromConstraint returns the explicit list of node names allowed to
	// probe targetID, as declared in the target's `probe_from` config field.
	// An empty / nil return means "no constraint — let the cluster decide".
	//
	// When non-empty, CandidatesFor will intersect the candidate set with this
	// list, effectively pinning probe execution to the named nodes. All nodes
	// that have the target in their config should return the same list to
	// avoid disagreement on prober assignments.
	ProbeFromConstraint(targetID string) []string
}

// SetLocalTargetProvider registers the engine's local-target inventory source.
// Call it once during Engine.Init after cluster.New returns. Subsequent
// CandidatesFor calls will see the local node in candidate sets even before
// the first state broadcast goes out (closes the bootstrap chicken-and-egg).
func (m *Manager) SetLocalTargetProvider(p LocalTargetProvider) {
	m.mu.Lock()
	m.localTargetProvider = p
	m.mu.Unlock()
}

// ── Candidate set derivation ────────────────────────────────────────────────

// aliveSet returns the set of node names currently in StateAlive.
// In test contexts where m.list is nil we fall back to "this node only" so
// CandidatesFor still gives a meaningful answer; unit tests that need to
// simulate multi-node aliveness install a testAliveOverride.
func (m *Manager) aliveSet() map[string]bool {
	if m.testAliveOverride != nil {
		out := make(map[string]bool, len(m.testAliveOverride))
		for k, v := range m.testAliveOverride {
			if v {
				out[k] = true
			}
		}
		return out
	}
	out := map[string]bool{}
	if m.list == nil {
		out[m.cfg.NodeName] = true
		return out
	}
	for _, mem := range m.list.Members() {
		if mem.State == memberlist.StateAlive {
			out[mem.Name] = true
		}
	}
	return out
}

// CandidatesFor returns the sorted list of cluster nodes that could probe
// targetID. A node qualifies when either:
//
//   - it has broadcast a GossipPayload for targetID at some point (so we know
//     the target is in its config), or
//   - it is the local node and LocalTargetProvider reports targetID locally.
//
// Dead / left nodes are filtered out using the current memberlist alive set.
// When the target carries a non-empty ProbeFromConstraint the candidate set
// is intersected with that list — explicit pinning overrides automatic
// selection.
//
// The result is lexicographically sorted for deterministic downstream hashing.
func (m *Manager) CandidatesFor(targetID string) []string {
	alive := m.aliveSet()

	m.mu.RLock()
	provider := m.localTargetProvider
	seen := map[string]bool{}
	out := make([]string, 0, len(m.peerStates)+1)
	for nodeName, targets := range m.peerStates {
		if !alive[nodeName] {
			continue
		}
		if _, ok := targets[targetID]; ok && !seen[nodeName] {
			seen[nodeName] = true
			out = append(out, nodeName)
		}
	}
	m.mu.RUnlock()

	// Include this node when its config carries the target, even if no state
	// has been broadcast yet (bootstrap path).
	if provider != nil && alive[m.cfg.NodeName] && !seen[m.cfg.NodeName] {
		for _, id := range provider.LocalTargets() {
			if id == targetID {
				out = append(out, m.cfg.NodeName)
				break
			}
		}
	}

	// Apply ProbeFrom constraint when the operator pinned the target to a
	// specific node list. Intersect candidate set with the allowed names.
	if provider != nil {
		if pin := provider.ProbeFromConstraint(targetID); len(pin) > 0 {
			allowed := make(map[string]bool, len(pin))
			for _, n := range pin {
				allowed[n] = true
			}
			filtered := out[:0]
			for _, n := range out {
				if allowed[n] {
					filtered = append(filtered, n)
				}
			}
			out = filtered
		}
	}

	sort.Strings(out)
	return out
}

// ── Prober selection ────────────────────────────────────────────────────────

// hashCandidateOrder rotates a sorted candidate list so it starts at the hash
// position of targetID. Every node computes the same rotation, which is what
// makes prober selection consistent without coordination.
//
// `candidates` must already be sorted lexicographically.
func hashCandidateOrder(candidates []string, targetID string) []string {
	n := len(candidates)
	if n == 0 {
		return nil
	}
	start := int(hashTarget(targetID)) % n
	out := make([]string, n)
	for i := 0; i < n; i++ {
		out[i] = candidates[(start+i)%n]
	}
	return out
}

// zoneAwarePick implements the 3-tier probe selection rule.
//
// Tier 1 (zone diversity): walk the hash-ordered list and pick the first node
// of each unique zone. This guarantees redundancy across failure domains.
//
// Tier 2 (zone repeat): if Tier 1 ran out of unique zones before reaching the
// target factor, fill from the remaining zone-tagged nodes. Repeated zones
// are still strictly preferred over zone-less nodes.
//
// Tier 3 (zone-less fallback): only when no more zone-tagged candidates exist,
// pick zone-less nodes in hash order.
//
// The function never returns more than `factor` names, and never fewer than
// min(factor, len(sortedByHash)).
func zoneAwarePick(sortedByHash []string, factor int, zoneOf func(string) string) []string {
	if factor <= 0 || len(sortedByHash) == 0 {
		return nil
	}

	var withZone, withoutZone []string
	for _, n := range sortedByHash {
		if zoneOf(n) != "" {
			withZone = append(withZone, n)
		} else {
			withoutZone = append(withoutZone, n)
		}
	}

	picked := make([]string, 0, factor)
	seenZones := map[string]bool{}
	var tier2 []string

	// Tier 1 — first appearance of each zone (in hash order).
	for _, n := range withZone {
		if len(picked) >= factor {
			break
		}
		z := zoneOf(n)
		if !seenZones[z] {
			picked = append(picked, n)
			seenZones[z] = true
		} else {
			tier2 = append(tier2, n)
		}
	}

	// Tier 2 — additional zone-tagged nodes (zone repeat beats zone-less).
	for _, n := range tier2 {
		if len(picked) >= factor {
			break
		}
		picked = append(picked, n)
	}

	// Tier 3 — zone-less nodes are only chosen when nothing else is left.
	for _, n := range withoutZone {
		if len(picked) >= factor {
			break
		}
		picked = append(picked, n)
	}

	return picked
}

// SelectProbers returns the deterministic prober subset for targetID.
// At most ProbeReplicationFactor names are returned (default 3). The slice
// is empty when no node has the target in its config.
//
// When the candidate count is ≤ factor every candidate becomes a prober and
// zone preferences do not apply — small clusters keep the legacy "everyone
// probes" behaviour for free.
func (m *Manager) SelectProbers(targetID string) []string {
	candidates := m.CandidatesFor(targetID)
	if len(candidates) == 0 {
		return nil
	}
	factor := m.cfg.effectiveReplicationFactor()
	if len(candidates) <= factor {
		return candidates
	}
	ordered := hashCandidateOrder(candidates, targetID)
	return zoneAwarePick(ordered, factor, m.zoneOf)
}

// IsLocalProber reports whether this node belongs to the prober subset for
// targetID. Engine.startProbeLoop consults this to decide whether to launch
// a probe goroutine for the target on this node.
//
// Standalone mode (m == nil) is handled by the caller — engine treats a nil
// Manager as "always probe locally".
func (m *Manager) IsLocalProber(targetID string) bool {
	for _, n := range m.SelectProbers(targetID) {
		if n == m.cfg.NodeName {
			return true
		}
	}
	return false
}
