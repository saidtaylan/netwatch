package engine

import (
	"sort"
	"time"
)

// ── FleetSnapshot — rich engine-level fleet view ──────────────────────────────
//
// FleetSnapshot enriches the cluster-layer FleetSummarySnapshot with per-target
// detail the engine layer has access to:
//   - local + peer state breakdown (by_node)
//   - consensus state, scope, affected apps, root cause
//   - active incidents (currently hard-down targets with start time)
//
// Standalone mode (cluster disabled): cluster section is nil, targets come from
// local state only. The payload shape is identical so dashboards need no change.

// FleetCluster is the cluster-health section of FleetSnapshot.
type FleetCluster struct {
	LocalNode         string   `json:"local_node"`
	Members           []string `json:"members"`           // alive member names
	Size              int      `json:"size"`              // total (alive + suspect)
	AliveCount        int      `json:"alive_count"`
	QuorumHealthy     bool     `json:"quorum_healthy"`
	Isolated          bool     `json:"isolated"`
	ReplicationFactor int      `json:"replication_factor"`
}

// FleetNodeView is one node's reported state for a target.
type FleetNodeView struct {
	State     string  `json:"state"` // up | hard_down | unknown
	Seq       uint64  `json:"seq"`
	ErrorCode string  `json:"error_code,omitempty"`
	Latency   float64 `json:"latency,omitempty"` // last measured round-trip in seconds; 0 = not measured
}

// FleetTarget is one target's entry in the fleet view.
type FleetTarget struct {
	ID              string                   `json:"id"` // target.key() — matches config id or name
	Name            string                   `json:"name"`
	TargetAddr      string                   `json:"target"`
	Type            string                   `json:"type"`
	ConsensusState  string                   `json:"consensus_state"` // up | hard_down | soft_down | unknown
	Scope           string                   `json:"scope,omitempty"` // GLOBAL | PARTIAL | NODE_LOCAL
	Classification  string                   `json:"classification,omitempty"` // REAL_OUTAGE | NETWORK_PARTITION | LOCAL_FAILURE | AMBIGUOUS
	Confidence      float64                  `json:"confidence,omitempty"`     // 0.0–1.0
	ByNode          map[string]FleetNodeView `json:"by_node,omitempty"`
	AffectedApps    []string                 `json:"affected_apps,omitempty"`
	OwnerTeams      []string                 `json:"owner_teams,omitempty"`
	RootCause       string                   `json:"root_cause,omitempty"`       // target ID of root cause
	CascadingImpact []string                 `json:"cascading_impact,omitempty"` // transitive dependents
	DownSince       *time.Time               `json:"down_since,omitempty"`       // first hard_down seen (best-effort)
}

// FleetSummarySection is the rollup count section.
type FleetSummarySection struct {
	Total    int `json:"total"`
	Up       int `json:"up"`
	SoftDown int `json:"soft_down"`
	HardDown int `json:"hard_down"`
	Unknown  int `json:"unknown"`
}

// FleetIncident is an active outage entry.
type FleetIncident struct {
	TargetID   string `json:"target_id"`
	TargetName string `json:"target_name"`
	Scope      string `json:"scope"`
	Seq        uint64 `json:"seq"`
	ErrorCode  string `json:"error_code,omitempty"`
	RootCause  string `json:"root_cause,omitempty"`
}

// FleetSnapshot is the full GET /fleet/status response payload.
type FleetSnapshot struct {
	Cluster   *FleetCluster       `json:"cluster,omitempty"` // nil in standalone mode
	Summary   FleetSummarySection `json:"summary"`
	Targets   []FleetTarget       `json:"targets"`
	Incidents []FleetIncident     `json:"incidents,omitempty"`
}

// FleetSnapshot computes the rich engine-level fleet view.
//
// It merges local state (e.lastKnown, e.pending) with peer gossip states from
// the cluster manager, then enriches each target with app context, root-cause
// analysis, and cascading impact derived from the topology graph.
func (e *Engine) FleetSnapshot() FleetSnapshot {
	// Snapshot config under read lock.
	e.mu.RLock()
	targets := make([]Target, len(e.cfg.Targets))
	copy(targets, e.cfg.Targets)
	appIndex := e.appIndex
	g := e.topoGraph
	mgr := e.clusterMgr
	e.mu.RUnlock()

	// Snapshot local state under read lock.
	e.stateMu.RLock()
	localStates := make(map[string]PersistedState, len(e.lastKnown))
	for k, v := range e.lastKnown {
		localStates[k] = v
	}
	pendingKeys := make(map[string]bool, len(e.pending))
	for k := range e.pending {
		pendingKeys[k] = true
	}
	e.stateMu.RUnlock()

	// Build merged (local + peer) state map for root-cause queries.
	allStates := make(map[string]PersistedState, len(localStates))
	for k, v := range localStates {
		allStates[k] = v
	}

	// Peer states from cluster layer.
	var peerStatesByTarget map[string][]FleetNodeView // targetID → per-node views
	var clusterSection *FleetCluster
	if mgr != nil {
		peers := mgr.AllPeerStates()
		peerStatesByTarget = make(map[string][]FleetNodeView, len(peers))
		for _, p := range peers {
			if _, exists := allStates[p.TargetID]; !exists || p.Seq > allStates[p.TargetID].Seq {
				allStates[p.TargetID] = PersistedState{
					State:     p.State,
					Seq:       p.Seq,
					ErrorCode: p.ErrorCode,
				}
			}
			peerStatesByTarget[p.TargetID] = append(peerStatesByTarget[p.TargetID], FleetNodeView{
				State:     p.State,
				Seq:       p.Seq,
				ErrorCode: p.ErrorCode,
			})
		}

		members := mgr.Members()
		memberNames := make([]string, 0, len(members))
		alive := 0
		for _, m := range members {
			if m.Status == "alive" {
				alive++
				memberNames = append(memberNames, m.Name)
			}
		}
		sort.Strings(memberNames)
		clusterSection = &FleetCluster{
			LocalNode:         e.clusterNodeName(),
			Members:           memberNames,
			Size:              len(members),
			AliveCount:        alive,
			QuorumHealthy:     mgr.QuorumHealthy(),
			Isolated:          mgr.IsolatedMode(),
			ReplicationFactor: mgr.ReplicationFactor(),
		}
	}

	// Build per-target fleet entries.
	var fleetTargets []FleetTarget
	var incidents []FleetIncident
	var summary FleetSummarySection

	for _, t := range targets {
		if !t.active() {
			continue
		}
		key := t.key()
		summary.Total++

		// Determine consensus state.
		consensusState := "unknown"
		var scope string
		localPS, localKnown := localStates[key]
		softDown := pendingKeys[t.typeKey()]

		byNode := make(map[string]FleetNodeView)
		localNodeName := e.clusterNodeName()

		var localLatency float64
		if v, ok := e.lastLatency.Load(key); ok {
			localLatency, _ = v.(float64)
		}

		if localKnown {
			byNode[localNodeName] = FleetNodeView{
				State:     localPS.State,
				Seq:       localPS.Seq,
				ErrorCode: localPS.ErrorCode,
				Latency:   localLatency,
			}
		}
		if softDown {
			byNode[localNodeName] = FleetNodeView{State: "soft_down", Latency: localLatency}
		}

		for _, nv := range peerStatesByTarget[key] {
			// peer node name not directly available in FleetNodeView — we show
			// the state without the node name here (node name is in the gossip
			// payload but not propagated to FleetNodeView yet). Future work.
			_ = nv
		}
		// Richer by_node: pull node name + latency from raw peer states (gossip
		// payloads carry the reporting node's last measured round-trip).
		if mgr != nil {
			for _, p := range mgr.PeerStatesForTarget(key) {
				byNode[p.NodeName] = FleetNodeView{
					State:     p.State,
					Seq:       p.Seq,
					ErrorCode: p.ErrorCode,
					Latency:   p.Latency,
				}
			}
		}

		var classification string
		var confidence float64

		switch {
		case softDown:
			consensusState = "soft_down"
			summary.SoftDown++
			scope = "NODE_LOCAL"
			classification = "LOCAL_FAILURE"
			confidence = 1.0
		case localKnown && localPS.State == "hard_down":
			// Use classifyScope for richer GLOBAL/PARTIAL/NODE_LOCAL + classification.
			ds := e.classifyScope(key)
			consensusState = "hard_down"
			summary.HardDown++
			scope = ds.Scope
			classification = ds.Classification
			confidence = ds.Confidence
		case localKnown && localPS.State == "up":
			consensusState = "up"
			summary.Up++
		default:
			consensusState = "unknown"
			summary.Unknown++
		}

		// App context.
		apps := appIndex[key]
		var affectedApps, ownerTeams []string
		teamSet := map[string]bool{}
		for _, a := range apps {
			affectedApps = append(affectedApps, a.Name)
			if a.OwnerTeam != "" && !teamSet[a.OwnerTeam] {
				teamSet[a.OwnerTeam] = true
				ownerTeams = append(ownerTeams, a.OwnerTeam)
			}
		}

		// Root cause + cascading impact (topology).
		var rootCause string
		var cascadingImpact []string
		if consensusState == "hard_down" || consensusState == "soft_down" {
			rootCause = g.FindRootCause(key, allStates)
			cascadingImpact = g.CascadingImpact(key)
		}

		ft := FleetTarget{
			ID:              key,
			Name:            t.Name,
			TargetAddr:      t.Target,
			Type:            t.Type,
			ConsensusState:  consensusState,
			Scope:           scope,
			Classification:  classification,
			Confidence:      confidence,
			AffectedApps:    affectedApps,
			OwnerTeams:      ownerTeams,
			RootCause:       rootCause,
			CascadingImpact: cascadingImpact,
		}
		if len(byNode) > 0 {
			ft.ByNode = byNode
		}
		fleetTargets = append(fleetTargets, ft)

		// Accumulate active incidents.
		if consensusState == "hard_down" {
			seq := localPS.Seq
			errCode := localPS.ErrorCode
			incidents = append(incidents, FleetIncident{
				TargetID:   key,
				TargetName: t.Name,
				Scope:      scope,
				Seq:        seq,
				ErrorCode:  errCode,
				RootCause:  rootCause,
			})
		}
	}

	// Sort targets alphabetically for stable output.
	sort.Slice(fleetTargets, func(i, j int) bool {
		return fleetTargets[i].Name < fleetTargets[j].Name
	})

	return FleetSnapshot{
		Cluster:   clusterSection,
		Summary:   summary,
		Targets:   fleetTargets,
		Incidents: incidents,
	}
}

// ── helpers needed from cluster.Manager ──────────────────────────────────────
// QuorumHealthy and ReplicationFactor are thin accessors on *cluster.Manager
// used by FleetSnapshot above. They are defined in cluster.go.

