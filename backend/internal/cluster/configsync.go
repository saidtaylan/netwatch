// configsync.go — Phase P1.5: Gossip-based config drift detection.
//
// Every node periodically broadcasts a SHA-256 fingerprint of its raw
// config.yaml file. Peers compare the received hash against their own:
// a mismatch triggers a warning log and sets network_probe_config_drift=1.
//
// Two modes are defined:
//   - "drift_detection" (default, safe): detect and warn, config unchanged.
//   - "auto_sync" (reserved for a future phase): not yet implemented.
//
// Config sync is fully opt-in. Setting cluster.config_sync.enabled=false
// (the default) disables all gossip traffic and metric updates in this file.
package cluster

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"log/slog"
	"sort"
	"time"

	"github.com/hashicorp/memberlist"
	"github.com/prometheus/client_golang/prometheus"
)

// ── ConfigSyncConfig ─────────────────────────────────────────────────────────

// ConfigSyncConfig controls gossip-based config synchronisation between nodes.
// It is embedded as cluster.config_sync in the top-level cluster Config.
type ConfigSyncConfig struct {
	// Enabled must be true to activate config sync. Default: false.
	Enabled bool `yaml:"enabled" json:"enabled"`

	// Mode selects the sync behaviour:
	//   "drift_detection" (default) — compare hashes, log warnings, update metric.
	//   "auto_sync"                 — reserved; not implemented in this release.
	// When empty, "drift_detection" is assumed.
	Mode string `yaml:"mode,omitempty" json:"mode,omitempty"`

	// PrimaryNode is required for Mode="auto_sync" to identify the authoritative
	// config source. Ignored in drift_detection mode.
	PrimaryNode string `yaml:"primary_node,omitempty" json:"primary_node,omitempty"`

	// SyncIntervalSec controls how often this node re-broadcasts its config
	// hash. Default: 30 s. Minimum: 5 s.
	SyncIntervalSec int `yaml:"sync_interval_sec,omitempty" json:"sync_interval_sec,omitempty"`
}

// effectiveSyncInterval returns how often the config fingerprint is broadcast,
// enforcing a 5 s floor and defaulting to 30 s when unset/too small.
func (c ConfigSyncConfig) effectiveSyncInterval() time.Duration {
	if c.SyncIntervalSec >= 5 {
		return time.Duration(c.SyncIntervalSec) * time.Second
	}
	return 30 * time.Second
}

// ── Message types ─────────────────────────────────────────────────────────────

// msgTypeConfig is the msg_type tag that distinguishes config broadcasts from
// state broadcasts in NotifyMsg. The existing GossipPayload messages do not
// carry a msg_type field, so the tag acts as a safe version discriminator.
const msgTypeConfig = "config"

// ConfigBroadcast is the gossip payload used to share a node's config hash.
// NotifyMsg identifies it by MsgType == "config".
type ConfigBroadcast struct {
	// MsgType is always "config"; used by NotifyMsg for type discrimination.
	MsgType string `json:"msg_type"`

	NodeName string `json:"node_name"`

	// ConfigHash is the first 16 hex chars of SHA-256(raw config.yaml bytes).
	// Short for display; collision probability is negligible at this scale.
	ConfigHash string `json:"config_hash"`

	ConfigSize int64     `json:"config_size"`
	LoadedAt   time.Time `json:"loaded_at"`
}

// cfgBroadcast wraps a ConfigBroadcast for the memberlist broadcast queue.
// It implements memberlist.Broadcast.
type cfgBroadcast struct {
	data []byte
}

// newCfgBroadcast wraps a ConfigBroadcast in a memberlist.Broadcast, marshalling
// it once. Returns an error if the payload can't be encoded.
func newCfgBroadcast(cb ConfigBroadcast) (*cfgBroadcast, error) {
	data, err := json.Marshal(cb)
	if err != nil {
		return nil, err
	}
	return &cfgBroadcast{data: data}, nil
}

// Invalidates returns true so that only the most recent config broadcast from
// this node is queued (older ones are superseded).
func (b *cfgBroadcast) Invalidates(other memberlist.Broadcast) bool {
	_, ok := other.(*cfgBroadcast)
	return ok
}

// Message returns the encoded config broadcast bytes for memberlist to gossip.
func (b *cfgBroadcast) Message() []byte { return b.data }

// Finished is memberlist's post-send callback; nothing to clean up.
func (b *cfgBroadcast) Finished() {}

// ── ConfigDrift ───────────────────────────────────────────────────────────────

// ConfigDrift records a mismatch between this node's config hash and a peer's.
type ConfigDrift struct {
	NodeName   string    `json:"node_name"`
	LocalHash  string    `json:"local_hash"`
	RemoteHash string    `json:"remote_hash"`
	DetectedAt time.Time `json:"detected_at"`
}

// ── Snapshot ──────────────────────────────────────────────────────────────────

// PeerConfigInfo is one row in ConfigSyncSnapshot.Peers.
type PeerConfigInfo struct {
	NodeName   string    `json:"node_name"`
	ConfigHash string    `json:"config_hash"`
	InSync     bool      `json:"in_sync"`
	LoadedAt   time.Time `json:"loaded_at"`
}

// ConfigSyncSnapshot is returned by Manager.ConfigSyncSnapshot() and served at
// GET /cluster/config.
type ConfigSyncSnapshot struct {
	Self       ConfigBroadcast  `json:"self"`
	Peers      []PeerConfigInfo `json:"peers"`
	DriftCount int              `json:"drift_count"`
}

// ── Prometheus metric ─────────────────────────────────────────────────────────

// GaugeConfigDrift is 1 when any cluster peer has a different config hash.
// Registered by RegisterClusterMetrics; always 0 when config_sync is disabled.
var GaugeConfigDrift = prometheus.NewGauge(prometheus.GaugeOpts{
	Name: "network_probe_config_drift",
	Help: "1 when at least one peer has a different config hash; 0 = all in sync.",
})

// ── Hash helper ───────────────────────────────────────────────────────────────

// ConfigHashOf returns the first 16 hex characters of SHA-256(rawBytes).
// Call it with the raw config.yaml bytes (before variable substitution) so the
// hash reflects the on-disk file content rather than resolved credentials.
func ConfigHashOf(rawBytes []byte) string {
	sum := sha256.Sum256(rawBytes)
	return hex.EncodeToString(sum[:])[:16]
}

// ── Manager methods ───────────────────────────────────────────────────────────

// SetLocalConfigInfo records this node's config hash and immediately broadcasts
// it to peers when config_sync is enabled. Called by the engine after every
// successful LoadConfig.
//
// Safe to call when config_sync is disabled (becomes a no-op after storing the
// hash internally so ConfigSyncSnapshot can still report self info).
func (m *Manager) SetLocalConfigInfo(hash string, size int64, loadedAt time.Time) {
	m.cfgMu.Lock()
	m.localCfgHash = hash
	m.localCfgSize = size
	m.localCfgLoadedAt = loadedAt
	m.cfgMu.Unlock()

	if m.cfg.ConfigSync != nil && m.cfg.ConfigSync.Enabled {
		m.broadcastConfigInfo()
	}
}

// broadcastConfigInfo sends this node's config fingerprint to all cluster peers
// via the gossip broadcast queue.
func (m *Manager) broadcastConfigInfo() {
	if m.delegate == nil {
		return
	}
	m.cfgMu.RLock()
	cb := ConfigBroadcast{
		MsgType:    msgTypeConfig,
		NodeName:   m.cfg.NodeName,
		ConfigHash: m.localCfgHash,
		ConfigSize: m.localCfgSize,
		LoadedAt:   m.localCfgLoadedAt,
	}
	m.cfgMu.RUnlock()

	bc, err := newCfgBroadcast(cb)
	if err != nil {
		slog.Warn("cluster: config broadcast marshal failed", "err", err)
		return
	}
	m.delegate.broadcasts.QueueBroadcast(bc)
}

// handleConfigBroadcast is called from NotifyMsg when msg_type == "config".
// It stores the peer's hash, logs on mismatch and updates the drift metric.
func (m *Manager) handleConfigBroadcast(cb ConfigBroadcast) {
	if cb.NodeName == "" || cb.NodeName == m.cfg.NodeName {
		return // ignore empty or self messages
	}

	m.mu.Lock()
	if m.peerConfigs == nil {
		m.peerConfigs = make(map[string]ConfigBroadcast)
	}
	m.peerConfigs[cb.NodeName] = cb
	m.mu.Unlock()

	m.cfgMu.RLock()
	localHash := m.localCfgHash
	m.cfgMu.RUnlock()

	if localHash != "" && cb.ConfigHash != "" && localHash != cb.ConfigHash {
		slog.Warn("cluster: config drift detected",
			"peer", cb.NodeName,
			"local_hash", localHash,
			"peer_hash", cb.ConfigHash,
			"peer_loaded_at", cb.LoadedAt.Format(time.RFC3339),
		)
	}
	m.updateConfigDriftMetric()
}

// ConfigDriftDetected returns all peers whose config hash differs from ours.
// Returns nil when no drift is detected or when this node has no hash yet.
func (m *Manager) ConfigDriftDetected() []ConfigDrift {
	m.cfgMu.RLock()
	localHash := m.localCfgHash
	m.cfgMu.RUnlock()

	if localHash == "" {
		return nil
	}

	m.mu.RLock()
	defer m.mu.RUnlock()

	var drifts []ConfigDrift
	for _, cb := range m.peerConfigs {
		if cb.ConfigHash != "" && cb.ConfigHash != localHash {
			drifts = append(drifts, ConfigDrift{
				NodeName:   cb.NodeName,
				LocalHash:  localHash,
				RemoteHash: cb.ConfigHash,
				DetectedAt: time.Now(),
			})
		}
	}
	return drifts
}

// ConfigSyncSnapshot returns the current config-sync state for GET /cluster/config.
func (m *Manager) ConfigSyncSnapshot() ConfigSyncSnapshot {
	m.cfgMu.RLock()
	localHash := m.localCfgHash
	self := ConfigBroadcast{
		MsgType:    msgTypeConfig,
		NodeName:   m.cfg.NodeName,
		ConfigHash: m.localCfgHash,
		ConfigSize: m.localCfgSize,
		LoadedAt:   m.localCfgLoadedAt,
	}
	m.cfgMu.RUnlock()

	m.mu.RLock()
	peers := make([]PeerConfigInfo, 0, len(m.peerConfigs))
	driftCount := 0
	for _, cb := range m.peerConfigs {
		inSync := localHash == "" || cb.ConfigHash == "" || cb.ConfigHash == localHash
		if !inSync {
			driftCount++
		}
		peers = append(peers, PeerConfigInfo{
			NodeName:   cb.NodeName,
			ConfigHash: cb.ConfigHash,
			InSync:     inSync,
			LoadedAt:   cb.LoadedAt,
		})
	}
	m.mu.RUnlock()

	sort.Slice(peers, func(i, j int) bool {
		return peers[i].NodeName < peers[j].NodeName
	})
	return ConfigSyncSnapshot{Self: self, Peers: peers, DriftCount: driftCount}
}

// updateConfigDriftMetric refreshes GaugeConfigDrift to 0/1.
func (m *Manager) updateConfigDriftMetric() {
	if len(m.ConfigDriftDetected()) > 0 {
		GaugeConfigDrift.Set(1)
	} else {
		GaugeConfigDrift.Set(0)
	}
}

// UpdateConfigDriftMetric is the exported view of updateConfigDriftMetric.
// Called by the engine's cluster-metrics updater goroutine.
func (m *Manager) UpdateConfigDriftMetric() {
	m.updateConfigDriftMetric()
}

// runConfigSyncLoop periodically re-broadcasts the config hash and refreshes
// the drift metric. Terminates when ctx is cancelled (called by Leave).
func (m *Manager) runConfigSyncLoop(ctx context.Context) {
	interval := m.cfg.ConfigSync.effectiveSyncInterval()
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			m.broadcastConfigInfo()
			m.updateConfigDriftMetric()
		}
	}
}
