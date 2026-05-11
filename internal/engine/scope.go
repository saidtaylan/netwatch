package engine

import (
	"fmt"
	"sort"
	"strings"
)

// ── DetailedScope — enriched scope classification ─────────────────────────────
//
// Phase 8 established three raw scope values (GLOBAL / PARTIAL / NODE_LOCAL).
// DetailedScope builds on that foundation by naming the failure mode and
// attaching a confidence score so alert recipients know whether they should
// escalate immediately (REAL_OUTAGE, confidence ≥ 0.9) or investigate the
// network first (NETWORK_PARTITION, LOCAL_FAILURE).

// DetailedScope enriches the basic GLOBAL/PARTIAL/NODE_LOCAL scope tag with a
// human-readable classification and a confidence score.
//
// It is returned by classifyScope and is consumed in two places:
//   - sendAlert() — injects CLASSIFICATION, DOWN_NODES, UP_NODES, CONFIDENCE, etc. into alert env
//   - FleetSnapshot() — populates Classification and Confidence on each FleetTarget
type DetailedScope struct {
	// Scope is the raw scope tag produced by the existing computeScope logic:
	// GLOBAL | PARTIAL | NODE_LOCAL | STANDALONE.
	Scope string `json:"scope"`

	// Classification is the inferred failure mode.
	//
	//   REAL_OUTAGE       — all reporting nodes confirm the target is down;
	//                       no up-votes, no silent nodes → highest confidence.
	//   NETWORK_PARTITION — some nodes see the target as up, others as down;
	//                       indicates a network split between node groups.
	//   LOCAL_FAILURE     — only this node sees it down while peers confirm up;
	//                       suspect local connectivity or misconfiguration.
	//   AMBIGUOUS         — either no data yet (cluster bootstrap) or a global
	//                       scope observation with silent (unreporting) nodes,
	//                       so the picture is incomplete.
	Classification string `json:"classification"`

	// DownNodes is the set of nodes (including self) that reported hard_down.
	DownNodes []string `json:"down_nodes,omitempty"`

	// UpNodes is the set of nodes (including self) that reported up.
	UpNodes []string `json:"up_nodes,omitempty"`

	// OfflineNodes lists alive cluster members that have not reported on this
	// target at all. Non-empty only in cluster mode.
	OfflineNodes []string `json:"offline_nodes,omitempty"`

	// PartitionGroups is non-nil only when Classification = NETWORK_PARTITION.
	// Index 0 = down-seeing nodes, index 1 = up-seeing nodes.
	PartitionGroups [][]string `json:"partition_groups,omitempty"`

	// Confidence is a [0.0, 1.0] score indicating how certain the classification
	// is. 1.0 means all available evidence aligns; ≤ 0.5 indicates ambiguity.
	Confidence float64 `json:"confidence"`
}

// ScopeEnv converts a DetailedScope to a map of alert environment variables.
// The returned map is merged on top of the base env map in sendAlert() so
// script, mail, and webhook channels all receive the same enrichment.
//
// Variables produced:
//
//	SCOPE           — raw scope tag (GLOBAL | PARTIAL | NODE_LOCAL | STANDALONE)
//	CLASSIFICATION  — REAL_OUTAGE | NETWORK_PARTITION | LOCAL_FAILURE | AMBIGUOUS
//	CONFIDENCE      — "0.00"–"1.00" (two decimal places)
//	DOWN_NODES      — comma-separated node names that saw hard_down (optional)
//	UP_NODES        — comma-separated node names that saw up (optional)
//	OFFLINE_NODES   — comma-separated alive-but-silent nodes (optional)
func (d DetailedScope) ScopeEnv() map[string]string {
	env := map[string]string{
		"SCOPE":          d.Scope,
		"CLASSIFICATION": d.Classification,
		"CONFIDENCE":     fmt.Sprintf("%.2f", d.Confidence),
	}
	if len(d.DownNodes) > 0 {
		env["DOWN_NODES"] = strings.Join(d.DownNodes, ",")
	}
	if len(d.UpNodes) > 0 {
		env["UP_NODES"] = strings.Join(d.UpNodes, ",")
	}
	if len(d.OfflineNodes) > 0 {
		env["OFFLINE_NODES"] = strings.Join(d.OfflineNodes, ",")
	}
	return env
}

// classifyScope computes a DetailedScope for targetID.
//
// It merges the local state (from e.lastKnown) with peer observations from
// the cluster layer to infer whether the failure is a confirmed global outage,
// a network partition, or a local node issue.
//
// Standalone mode (clusterMgr nil): always returns LOCAL_FAILURE / confidence 1.0
// — there are no peers to contradict or confirm the observation.
func (e *Engine) classifyScope(targetID string) DetailedScope {
	// ── Read local state ───────────────────────────────────────────────────
	e.stateMu.RLock()
	localPS, localKnown := e.lastKnown[targetID]
	e.stateMu.RUnlock()

	localDown := localKnown && localPS.State == "hard_down"
	localNode := e.clusterNodeName()

	// ── Standalone mode ────────────────────────────────────────────────────
	if e.clusterMgr == nil {
		if !localKnown {
			// Target hasn't been probed yet — no state to classify.
			return DetailedScope{
				Scope:          "STANDALONE",
				Classification: "AMBIGUOUS",
				Confidence:     0.5,
			}
		}
		scope := "STANDALONE"
		if localDown {
			scope = "NODE_LOCAL"
		}
		return DetailedScope{
			Scope:          scope,
			Classification: "LOCAL_FAILURE",
			DownNodes:      condSlice(localDown, localNode),
			UpNodes:        condSlice(!localDown, localNode),
			Confidence:     1.0,
		}
	}

	// ── Cluster mode ───────────────────────────────────────────────────────
	peerStates := e.clusterMgr.PeerStatesForTarget(targetID)
	members := e.clusterMgr.Members()

	// Collect per-node votes.
	reporting := make(map[string]bool, len(peerStates)+1)
	var downNodes, upNodes []string

	if localKnown {
		reporting[localNode] = true
		if localDown {
			downNodes = append(downNodes, localNode)
		} else {
			upNodes = append(upNodes, localNode)
		}
	}

	for _, p := range peerStates {
		if p.NodeName == localNode {
			continue // self already counted above
		}
		reporting[p.NodeName] = true
		switch p.State {
		case "hard_down":
			downNodes = append(downNodes, p.NodeName)
		case "up":
			upNodes = append(upNodes, p.NodeName)
		}
	}

	// Alive members that have not reported on this target at all.
	var offlineNodes []string
	for _, m := range members {
		if m.Status == "alive" && !reporting[m.Name] {
			offlineNodes = append(offlineNodes, m.Name)
		}
	}

	sort.Strings(downNodes)
	sort.Strings(upNodes)
	sort.Strings(offlineNodes)

	downCount := len(downNodes)
	upCount := len(upNodes)
	offlineCount := len(offlineNodes)
	totalKnown := downCount + upCount
	clusterSize := len(members)

	// ── No data available at all ───────────────────────────────────────────
	if totalKnown == 0 {
		scope := "STANDALONE"
		if offlineCount > 0 {
			scope = "NODE_LOCAL"
		}
		return DetailedScope{
			Scope:          scope,
			Classification: "AMBIGUOUS",
			OfflineNodes:   offlineNodes,
			Confidence:     0.4,
		}
	}

	// ── All reporting nodes say down; no silent members ────────────────────
	if upCount == 0 && offlineCount == 0 {
		return DetailedScope{
			Scope:          "GLOBAL",
			Classification: "REAL_OUTAGE",
			DownNodes:      downNodes,
			Confidence:     1.0,
		}
	}

	// ── All reporting nodes say down, but some members are silent ──────────
	if upCount == 0 && offlineCount > 0 {
		// The picture looks global but we can't be certain — silent nodes
		// might see the target differently.
		conf := 0.5
		if clusterSize > 0 {
			conf = float64(downCount) / float64(clusterSize)
			if conf > 0.95 {
				conf = 0.95 // cap below 1.0: offline nodes introduce uncertainty
			}
		}
		return DetailedScope{
			Scope:          "GLOBAL",
			Classification: "AMBIGUOUS",
			DownNodes:      downNodes,
			OfflineNodes:   offlineNodes,
			Confidence:     conf,
		}
	}

	// ── Only this node is down; all peers say up — local connectivity issue ─
	if downCount == 1 && downNodes[0] == localNode && upCount > 0 {
		// Confidence grows with the number of agreeing peers.
		conf := float64(upCount) / float64(totalKnown)
		return DetailedScope{
			Scope:          "NODE_LOCAL",
			Classification: "LOCAL_FAILURE",
			DownNodes:      downNodes,
			UpNodes:        upNodes,
			OfflineNodes:   offlineNodes,
			Confidence:     conf,
		}
	}

	// ── Mixed: some down, some up — network partition ─────────────────────
	if downCount > 0 && upCount > 0 {
		// Confidence: a clean 50/50 split is the most "certain" partition signal.
		// A very lopsided split (one node vs many) is a weaker signal.
		ratio := float64(downCount) / float64(totalKnown)
		// Mirror ratio around 0.5: distance from 0.5 reduces confidence.
		symmetry := 1.0 - absFloat(ratio-0.5)/0.5
		conf := 0.5 + 0.5*symmetry
		if downCount == 1 && downNodes[0] != localNode {
			conf = 0.6 // a single remote down-report is a weaker partition signal
		}
		return DetailedScope{
			Scope:           "PARTIAL",
			Classification:  "NETWORK_PARTITION",
			DownNodes:       downNodes,
			UpNodes:         upNodes,
			OfflineNodes:    offlineNodes,
			PartitionGroups: [][]string{downNodes, upNodes},
			Confidence:      conf,
		}
	}

	// ── Fallback (should not normally be reached) ─────────────────────────
	return DetailedScope{
		Scope:          e.computeScope(targetID, localDown),
		Classification: "AMBIGUOUS",
		DownNodes:      downNodes,
		UpNodes:        upNodes,
		OfflineNodes:   offlineNodes,
		Confidence:     0.5,
	}
}

// condSlice returns []string{val} when include is true, otherwise nil.
func condSlice(include bool, val string) []string {
	if include {
		return []string{val}
	}
	return nil
}

// absFloat returns the absolute value of x.
func absFloat(x float64) float64 {
	if x < 0 {
		return -x
	}
	return x
}
