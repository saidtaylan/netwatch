// geolat.go — Phase P1.6: Geographic latency view and anomaly detection.
//
// When cluster.region is set on each node's config, every probe result
// includes the measured round-trip latency in the GossipPayload. This gives
// every node a cluster-wide view of latency grouped by geographic region.
//
// The /geo/latency/{targetID} endpoint returns per-node latency alongside
// region labels, and an anomaly flag when any node's latency is more than
// 3× the minimum non-zero latency seen for that target.
//
// probe_from_regions: operators can restrict which regions probe a target
// (similar to probe_from for individual nodes). CandidatesFor applies this
// constraint in probers.go.
package cluster

import (
	"encoding/json"
	"log/slog"
	"sort"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

// ── Snapshot types ────────────────────────────────────────────────────────────

// GeoLatencyEntry holds one node's most-recently-reported probe latency.
type GeoLatencyEntry struct {
	NodeName string  `json:"node_name"`
	Region   string  `json:"region,omitempty"`
	Latency  float64 `json:"latency_seconds"` // 0 means not yet measured / not applicable
}

// GeoLatencySnapshot is returned by Manager.GeoLatencyForTarget and served at
// GET /geo/latency/{targetID}.
type GeoLatencySnapshot struct {
	TargetID string            `json:"target_id"`
	ComputedAt time.Time       `json:"computed_at"`
	ByNode   []GeoLatencyEntry `json:"by_node"`
	// Anomaly is true when any node's latency exceeds 3× the minimum non-zero
	// latency seen for this target across all reporting nodes.
	Anomaly  bool              `json:"anomaly"`
}

// ── Prometheus metrics ────────────────────────────────────────────────────────

// GaugeGeoLatency reports each node's last probe latency with a region label.
// Labels: name, target, type, region (region may be "" for nodes without region set).
// Registered by RegisterClusterMetrics. Updated in runCheck (local) and
// updateClusterMetrics (for anomaly detection from peer states).
var GaugeGeoLatency = prometheus.NewGaugeVec(prometheus.GaugeOpts{
	Name: "network_probe_geo_latency_seconds",
	Help: "Last probe round-trip for this target as seen from a specific region (labels: name, target, type, region).",
}, []string{"name", "target", "type", "region"})

// GaugeGeoLatencyAnomaly is 1 when any reporting node's latency exceeds
// 3× the minimum non-zero latency for that target; 0 otherwise.
// Labels: name, target, type.
var GaugeGeoLatencyAnomaly = prometheus.NewGaugeVec(prometheus.GaugeOpts{
	Name: "network_probe_geo_latency_anomaly",
	Help: "1 when any node's latency for this target is >3× the minimum observed latency across nodes; 0 = normal.",
}, []string{"name", "target", "type"})

// ── regionOf ─────────────────────────────────────────────────────────────────

// regionOf returns the region label declared by nodeName, or "" when the node
// has not set one. Parallels zoneOf — uses testRegionOverride in tests and
// memberlist NodeMeta in production.
func (m *Manager) regionOf(nodeName string) string {
	// Test override takes precedence.
	if m.testRegionOverride != nil {
		if r, ok := m.testRegionOverride[nodeName]; ok {
			return r
		}
	}
	// Fast path: local node.
	if nodeName == m.cfg.NodeName {
		return m.cfg.Region
	}
	if m.list == nil {
		return ""
	}
	for _, mem := range m.list.Members() {
		if mem.Name != nodeName {
			continue
		}
		if len(mem.Meta) == 0 {
			return ""
		}
		var meta nodeMeta
		if err := json.Unmarshal(mem.Meta, &meta); err != nil {
			return ""
		}
		return meta.Region
	}
	return ""
}

// RegionOf is the exported view of regionOf.
func (m *Manager) RegionOf(nodeName string) string {
	return m.regionOf(nodeName)
}

// ── GeoLatencyForTarget ───────────────────────────────────────────────────────

// GeoLatencyForTarget builds a GeoLatencySnapshot for targetID from the current
// peerStates. Each peer's most-recently-received GossipPayload.Latency is used.
//
// Anomaly detection: when at least two nodes have reported non-zero latencies
// and any value exceeds 3× the minimum, Anomaly is set to true.
func (m *Manager) GeoLatencyForTarget(targetID string) GeoLatencySnapshot {
	m.mu.RLock()
	var entries []GeoLatencyEntry
	for nodeName, targets := range m.peerStates {
		p, ok := targets[targetID]
		if !ok {
			continue
		}
		entries = append(entries, GeoLatencyEntry{
			NodeName: nodeName,
			Region:   m.regionOf(nodeName),
			Latency:  p.Latency,
		})
	}
	m.mu.RUnlock()

	sort.Slice(entries, func(i, j int) bool {
		return entries[i].NodeName < entries[j].NodeName
	})

	anomaly := detectLatencyAnomaly(entries)
	return GeoLatencySnapshot{
		TargetID:   targetID,
		ComputedAt: time.Now(),
		ByNode:     entries,
		Anomaly:    anomaly,
	}
}

// anomalyMinimumLatencySec is the absolute floor below which we don't flag
// 3× variance as an anomaly. Sub-5ms measurements are dominated by system
// jitter (scheduler, kernel buffer cycles) — a 3× ratio on values < 5ms
// is normal noise, not a real network problem.
const anomalyMinimumLatencySec = 0.005 // 5 ms

// detectLatencyAnomaly returns true when:
//   - at least two nodes have reported non-zero latencies,
//   - the minimum non-zero latency is >= 5 ms (below that, jitter dominates), AND
//   - the maximum exceeds 3× the minimum.
//
// The 5 ms floor prevents false positives on localhost or same-rack probes
// where 0.2 ms vs 0.6 ms looks like a "3× anomaly" but is in fact normal
// measurement noise at sub-millisecond scale.
func detectLatencyAnomaly(entries []GeoLatencyEntry) bool {
	var nonZero []float64
	for _, e := range entries {
		if e.Latency > 0 {
			nonZero = append(nonZero, e.Latency)
		}
	}
	if len(nonZero) < 2 {
		return false
	}
	min, max := nonZero[0], nonZero[0]
	for _, v := range nonZero[1:] {
		if v < min {
			min = v
		}
		if v > max {
			max = v
		}
	}
	if min < anomalyMinimumLatencySec {
		// Sub-5ms — variance dominated by jitter, not a real anomaly.
		return false
	}
	return max > 3*min
}

// ── UpdateGeoMetrics ──────────────────────────────────────────────────────────

// UpdateGeoMetrics refreshes GaugeGeoLatency and GaugeGeoLatencyAnomaly for
// all peer states. Called every 5 s from engine.updateClusterMetrics.
//
// targetInfos maps targetID → {name, targetAddr, probeType} so the metric
// labels can be populated. Entries not in the map get the targetID as a
// fallback for all three fields.
func (m *Manager) UpdateGeoMetrics(targetInfos map[string][3]string) {
	m.mu.RLock()

	// Collect per-target per-node latencies from peerStates.
	type nodeEntry struct {
		region  string
		latency float64
	}
	// targetID → []nodeEntry
	latMap := make(map[string][]nodeEntry)
	for nodeName, targets := range m.peerStates {
		region := m.regionOf(nodeName)
		for targetID, p := range targets {
			latMap[targetID] = append(latMap[targetID], nodeEntry{
				region:  region,
				latency: p.Latency,
			})
		}
	}
	m.mu.RUnlock()

	for targetID, nodes := range latMap {
		info, hasInfo := targetInfos[targetID]
		name := targetID
		tgt := targetID
		typ := ""
		if hasInfo {
			name = info[0]
			tgt = info[1]
			typ = info[2]
		}

		// Per-region last latency: use max (least optimistic), helps surface outliers.
		regionLat := make(map[string]float64) // region → max latency seen
		var nonZero []float64
		for _, ne := range nodes {
			if ne.latency > 0 {
				nonZero = append(nonZero, ne.latency)
				if ne.region != "" {
					if ne.latency > regionLat[ne.region] {
						regionLat[ne.region] = ne.latency
					}
				}
			}
		}

		for region, lat := range regionLat {
			GaugeGeoLatency.With(prometheus.Labels{
				"name":   name,
				"target": tgt,
				"type":   typ,
				"region": region,
			}).Set(lat)
		}

		anomaly := 0.0
		if detectLatencyAnomaly(func() []GeoLatencyEntry {
			out := make([]GeoLatencyEntry, len(nodes))
			for i, n := range nodes {
				out[i] = GeoLatencyEntry{Latency: n.latency}
			}
			return out
		}()) {
			anomaly = 1.0
		}
		anomalyLabels := prometheus.Labels{
			"name":   name,
			"target": tgt,
			"type":   typ,
		}
		GaugeGeoLatencyAnomaly.With(anomalyLabels).Set(anomaly)

		if anomaly == 1.0 {
			slog.Debug("geo latency anomaly detected", "target", targetID)
		}
	}
}
