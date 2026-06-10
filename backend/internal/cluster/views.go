// Cluster view types and aggregations exposed via HTTP endpoints.
//
// Two aggregated views are exported here:
//
//   - ProberSnapshot — per-target prober assignment view, surfaced via
//     /cluster/probers. Answers "for each target, who probes it and why?"
//   - FleetSummary  — cluster-wide summary, surfaced via /fleet/status.
//     Counts only; intentionally no per-target detail (consumers wanting
//     per-target info use /cluster/state or /status).
//
// Both methods are read-only and lock the manager briefly while snapshotting.
// They are safe to call from HTTP handlers.
package cluster

import (
	"sort"
)

// ── /cluster/probers ────────────────────────────────────────────────────────

// ProberAssignment captures the prober selection for a single target.
//
//   - Probers are the nodes that will actually run probes (≤ replication
//     factor; ordered by hash position so the head is the alerting primary).
//   - Candidates is the unfiltered set before factor capping — useful for
//     debugging "why is my node not in Probers?" questions.
//   - Constraint surfaces the active ProbeFrom pin, if any.
type ProberAssignment struct {
	TargetID      string   `json:"target_id"`
	Probers       []string `json:"probers"`
	Primary       string   `json:"primary,omitempty"`
	Candidates    []string `json:"candidates"`
	IsLocalProber bool     `json:"i_probe"`
	Constraint    []string `json:"probe_from,omitempty"`
}

// ProberSnapshot is the response payload of GET /cluster/probers.
type ProberSnapshot struct {
	LocalNode         string                      `json:"local_node"`
	ReplicationFactor int                         `json:"replication_factor"`
	Members           []MemberInfo                `json:"members"` // includes zone
	Assignments       map[string]ProberAssignment `json:"assignments"`
}

// ProberAssignmentsSnapshot returns the current prober assignment view for
// every target this node knows about (locally or via gossip). Used by the
// /cluster/probers HTTP handler.
//
// The result is a defensive copy — callers may mutate freely.
func (m *Manager) ProberAssignmentsSnapshot() ProberSnapshot {
	// Gather the union of all known target IDs: local config + peer broadcasts.
	known := map[string]struct{}{}
	m.mu.RLock()
	provider := m.localTargetProvider
	for _, targets := range m.peerStates {
		for tid := range targets {
			known[tid] = struct{}{}
		}
	}
	m.mu.RUnlock()
	if provider != nil {
		for _, id := range provider.LocalTargets() {
			known[id] = struct{}{}
		}
	}

	assignments := make(map[string]ProberAssignment, len(known))
	for tid := range known {
		candidates := m.CandidatesFor(tid)
		probers := m.SelectProbers(tid)

		var primary string
		if len(probers) > 0 {
			primary = probers[0]
		}

		var constraint []string
		if provider != nil {
			if pin := provider.ProbeFromConstraint(tid); len(pin) > 0 {
				constraint = append([]string(nil), pin...)
			}
		}

		assignments[tid] = ProberAssignment{
			TargetID:      tid,
			Probers:       probers,
			Primary:       primary,
			Candidates:    candidates,
			IsLocalProber: contains(probers, m.cfg.NodeName),
			Constraint:    constraint,
		}
	}

	return ProberSnapshot{
		LocalNode:         m.cfg.NodeName,
		ReplicationFactor: m.ReplicationFactor(),
		Members:           m.Members(),
		Assignments:       assignments,
	}
}

// ── /fleet/status ────────────────────────────────────────────────────────────

// FleetClusterInfo is the cluster-wide section of FleetSummary.
type FleetClusterInfo struct {
	Size              int     `json:"size"`               // total known members
	AliveCount        int     `json:"alive_count"`        // members in StateAlive
	QuorumHealthy     bool    `json:"quorum_healthy"`     // checkQuorum() result
	Isolated          bool    `json:"isolated"`           // IsolatedMode()
	ExpectedNodeCount int     `json:"expected_node_count,omitempty"`
	MinQuorumRatio    float64 `json:"min_quorum_ratio,omitempty"`
	ReplicationFactor int     `json:"replication_factor"`
}

// FleetTargetCounts is the target-rollup section of FleetSummary.
//
// "Consensus" is defined permissively for HardDown — any node reporting
// hard_down counts the target as down (alignment with exactly-once alerting:
// if some node sees down and primary alerted, fleet is down). Up requires
// every reporting node agrees. Unknown covers targets only seen at bootstrap.
type FleetTargetCounts struct {
	Total    int `json:"total"`     // unique target IDs across the cluster
	Up       int `json:"up"`        // all reporting nodes say "up"
	HardDown int `json:"hard_down"` // any reporting node says "hard_down"
	Unknown  int `json:"unknown"`   // no node has a definitive state yet
}

// FleetSummary is the response payload of GET /fleet/status.
//
// Intentionally summary-only — per-target detail lives in /cluster/state
// (raw peer payloads) and /cluster/probers (prober assignments).
//
// DownTargets is a short list of the names (target IDs) currently in
// hard_down by cluster consensus. Capped at FleetDownTargetsCap to keep
// the payload bounded; the Targets.HardDown count is always exact.
type FleetSummary struct {
	LocalNode   string            `json:"local_node"`
	Cluster     FleetClusterInfo  `json:"cluster"`
	Members     []MemberInfo      `json:"members"` // with zones
	Targets     FleetTargetCounts `json:"targets"`
	DownTargets []string          `json:"down_targets,omitempty"`
}

// FleetDownTargetsCap caps the DownTargets list to keep responses bounded
// on large clusters with outages. The Targets.HardDown count remains exact.
const FleetDownTargetsCap = 100

// FleetSummarySnapshot computes the aggregated fleet view used by /fleet/status.
//
// Aggregation rules:
//   - A target is counted under HardDown when any peer (or local) reports
//     it as hard_down — mirrors the exactly-once alerting model where one
//     primary firing means the cluster considers it down.
//   - Up requires all reporting peers to say "up". Disagreement degrades the
//     target into Unknown.
//   - Bootstrap "unknown" payloads do not move a target into Up or HardDown
//     and contribute only to the Total count.
//
// Callers receive a defensive copy.
func (m *Manager) FleetSummarySnapshot() FleetSummary {
	// Index targets by ID, recording the "down anywhere?" flag and whether
	// every reporting node agreed on "up".
	type roll struct {
		anyDown bool
		anyUp   bool
		anyKnown bool // up or hard_down (i.e. not unknown / bootstrap)
	}
	rolled := map[string]*roll{}

	addObservation := func(targetID, state string) {
		r := rolled[targetID]
		if r == nil {
			r = &roll{}
			rolled[targetID] = r
		}
		switch state {
		case "hard_down":
			r.anyDown = true
			r.anyKnown = true
		case "up":
			r.anyUp = true
			r.anyKnown = true
		}
	}

	m.mu.RLock()
	for _, targets := range m.peerStates {
		for tid, p := range targets {
			addObservation(tid, p.State)
		}
	}
	m.mu.RUnlock()

	// Include local targets so a node that has just started — and hasn't
	// received any peer broadcasts yet — still reports its inventory size.
	if p := m.localTargetProvider; p != nil {
		for _, id := range p.LocalTargets() {
			if _, ok := rolled[id]; !ok {
				rolled[id] = &roll{}
			}
		}
	}

	var counts FleetTargetCounts
	var down []string
	for tid, r := range rolled {
		counts.Total++
		switch {
		case r.anyDown:
			counts.HardDown++
			if len(down) < FleetDownTargetsCap {
				down = append(down, tid)
			}
		case r.anyUp && !r.anyDown:
			counts.Up++
		default:
			counts.Unknown++
		}
	}
	sort.Strings(down)

	members := m.Members()
	alive := 0
	for _, mem := range members {
		if mem.Status == "alive" {
			alive++
		}
	}

	return FleetSummary{
		LocalNode: m.cfg.NodeName,
		Cluster: FleetClusterInfo{
			Size:              len(members),
			AliveCount:        alive,
			QuorumHealthy:     m.checkQuorum(),
			Isolated:          m.isolated.Load(),
			ExpectedNodeCount: m.cfg.ExpectedNodeCount,
			MinQuorumRatio:    m.cfg.MinQuorumRatio,
			ReplicationFactor: m.ReplicationFactor(),
		},
		Members:     members,
		Targets:     counts,
		DownTargets: down,
	}
}

// contains reports whether s is in haystack. Tiny utility kept local to
// avoid importing slices for a single call.
func contains(haystack []string, s string) bool {
	for _, h := range haystack {
		if h == s {
			return true
		}
	}
	return false
}
