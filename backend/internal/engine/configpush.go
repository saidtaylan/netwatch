package engine

// configpush.go — Shared config push/sync between cluster nodes.
//
// Two endpoints are exposed by cmd/linux/main.go:
//
//	PUT  /cluster/config       — caller provides a partial SharedConfig body;
//	                             the engine applies it locally and broadcasts to
//	                             all cluster peers via gossip.
//	POST /cluster/config/sync  — no body; the engine reads its own shared fields
//	                             from disk (pre-credential-injection) and
//	                             broadcasts them to all peers.
//
// Node-specific fields (port, node_alias, log_path, state_file,
// credentials_file, targets, apps, slo, cluster.node_name/bind_*/
// advertise_*/zone/region/config_sync) are deliberately excluded from
// SharedConfig and never overwritten on receiving nodes.
//
// Receiving nodes merge the payload into their own config.yaml (atomically,
// using .tmp + rename) and trigger Reload() so changes take effect immediately
// without a restart.

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/saidtaylan/netwatch/internal/cluster"
	"sigs.k8s.io/yaml"
)

// ── SharedConfig ──────────────────────────────────────────────────────────────

// SharedConfig holds the configuration fields that should be identical across
// all nodes in a cluster. It is used as the body for PUT /cluster/config and
// as the gossip payload for POST /cluster/config/sync.
//
// All fields are optional (omitempty). A partial SharedConfig updates only the
// fields that are present; omitted fields are left unchanged on each receiving node.
type SharedConfig struct {
	Timeout              int                           `json:"timeout,omitempty" yaml:"timeout,omitempty"`
	MaxRetries           *int                          `json:"max_retries,omitempty" yaml:"max_retries,omitempty"`
	RetryIntervalSec     *int                          `json:"retry_interval_sec,omitempty" yaml:"retry_interval_sec,omitempty"`
	TickerIntervalSec    *int                          `json:"ticker_interval_sec,omitempty" yaml:"ticker_interval_sec,omitempty"`
	ProbeIntervalSec     *int                          `json:"probe_interval_sec,omitempty" yaml:"probe_interval_sec,omitempty"`
	ReloadIntervalSec    *int                          `json:"reload_interval_sec,omitempty" yaml:"reload_interval_sec,omitempty"`
	WatchdogThresholdSec *int                          `json:"watchdog_threshold_sec,omitempty" yaml:"watchdog_threshold_sec,omitempty"`
	RecoveryProbes       *int                          `json:"recovery_probes,omitempty" yaml:"recovery_probes,omitempty"`
	Notifications        map[string]AlertChannelConfig `json:"notifications,omitempty" yaml:"notifications,omitempty"`
	DefaultNotify        []string                      `json:"default_notify,omitempty" yaml:"default_notify,omitempty"`
	Cluster              *SharedClusterConfig          `json:"cluster,omitempty" yaml:"cluster,omitempty"`
}

// SharedClusterConfig holds the cluster-section fields that must match across nodes.
type SharedClusterConfig struct {
	Keyring                []string `json:"keyring,omitempty" yaml:"keyring,omitempty"`
	Peers                  []string `json:"peers,omitempty" yaml:"peers,omitempty"`
	ExpectedNodeCount      int      `json:"expected_node_count,omitempty" yaml:"expected_node_count,omitempty"`
	MinQuorumRatio         float64  `json:"min_quorum_ratio,omitempty" yaml:"min_quorum_ratio,omitempty"`
	ProbeReplicationFactor  int      `json:"probe_replication_factor,omitempty" yaml:"probe_replication_factor,omitempty"`
	ProbeReplicationPercent int      `json:"probe_replication_percent,omitempty" yaml:"probe_replication_percent,omitempty"`
	MinProbeConfirmations   int      `json:"min_probe_confirmations,omitempty" yaml:"min_probe_confirmations,omitempty"`
}

// ConfigPushResult is returned by PUT /cluster/config and POST /cluster/config/sync.
type ConfigPushResult struct {
	AppliedLocally bool              `json:"applied_locally"`
	BroadcastTo    []string          `json:"broadcast_to"`
	FailedNodes    map[string]string `json:"failed_nodes,omitempty"`
	FieldsApplied  []string          `json:"fields_applied"`
	PushedAt       time.Time         `json:"pushed_at"`
}

// ── Extract: this node → SharedConfig ────────────────────────────────────────

// ExtractSharedConfig reads the pre-injection raw config bytes stored by the
// last successful LoadConfig and extracts only the shared fields. This avoids
// leaking resolved credentials (e.g. expanded ${SMTP_PASS}) to peer nodes.
//
// Returns an error when no raw bytes are available (before the first
// LoadConfig succeeds) or when the raw YAML cannot be parsed.
func (e *Engine) ExtractSharedConfig() (SharedConfig, error) {
	e.mu.RLock()
	raw := e.rawConfigBytes
	e.mu.RUnlock()

	if len(raw) == 0 {
		return SharedConfig{}, fmt.Errorf("no config loaded yet")
	}

	// Parse raw YAML (pre-injection) into a generic map so we can cherry-pick
	// shared fields without unmarshalling into the full Config struct (which
	// would have already resolved credentials in memory).
	var m map[string]interface{}
	if err := yaml.Unmarshal(raw, &m); err != nil {
		return SharedConfig{}, fmt.Errorf("parse raw config: %w", err)
	}

	// Re-marshal just the shared fields.
	shared := extractSharedMap(m)
	data, err := json.Marshal(shared)
	if err != nil {
		return SharedConfig{}, fmt.Errorf("marshal shared: %w", err)
	}
	var sc SharedConfig
	if err := json.Unmarshal(data, &sc); err != nil {
		return SharedConfig{}, fmt.Errorf("unmarshal SharedConfig: %w", err)
	}
	return sc, nil
}

// extractSharedMap returns a map containing only the shared keys from m.
func extractSharedMap(m map[string]interface{}) map[string]interface{} {
	out := make(map[string]interface{})

	topLevelShared := []string{
		"timeout", "max_retries", "retry_interval_sec", "ticker_interval_sec",
		"probe_interval_sec", "reload_interval_sec", "watchdog_threshold_sec",
		"recovery_probes",
		"notifications", "default_notify",
	}
	for _, k := range topLevelShared {
		if v, ok := m[k]; ok {
			// Strip node-specific `script` parameter from notification channels.
			// The `script` field is a local filesystem path and legitimately
			// differs between nodes. `script_body` (inline, DB-replicated) should
			// remain. Without this, every cluster using file-based scripts would
			// show permanent false drift.
			if k == "notifications" {
				v = stripScriptFileParams(v)
			}
			out[k] = v
		}
	}

	clusterShared := []string{
		"keyring", "peers", "expected_node_count", "min_quorum_ratio",
		"probe_replication_factor", "probe_replication_percent", "min_probe_confirmations",
	}
	if clusterRaw, ok := m["cluster"]; ok {
		if cm, ok := clusterRaw.(map[string]interface{}); ok {
			cOut := make(map[string]interface{})
			for _, k := range clusterShared {
				if v, ok := cm[k]; ok {
					cOut[k] = v
				}
			}
			if len(cOut) > 0 {
				out["cluster"] = cOut
			}
		}
	}
	return out
}

// stripScriptFileParams removes the `script` key from every notification
// channel's `parameters` map. The `script` value is a local filesystem path
// and therefore legitimately differs between nodes; including it in the drift
// hash would produce false positives. `script_body` (inline, DB-replicated)
// is left intact.
func stripScriptFileParams(notifRaw interface{}) interface{} {
	notifMap, ok := notifRaw.(map[string]interface{})
	if !ok {
		return notifRaw
	}
	out := make(map[string]interface{}, len(notifMap))
	for name, chanRaw := range notifMap {
		chanMap, ok := chanRaw.(map[string]interface{})
		if !ok {
			out[name] = chanRaw
			continue
		}
		chanCopy := make(map[string]interface{}, len(chanMap))
		for k, v := range chanMap {
			chanCopy[k] = v
		}
		if params, ok := chanCopy["parameters"].(map[string]interface{}); ok {
			paramsCopy := make(map[string]interface{}, len(params))
			for k, v := range params {
				if k != "script" { // `script` is a local path → exclude from drift hash
					paramsCopy[k] = v
				}
			}
			chanCopy["parameters"] = paramsCopy
		}
		out[name] = chanCopy
	}
	return out
}

// SharedConfigHash returns a stable 16-hex-char hash of the node's shared
// (cluster-wide) configuration. Unlike ConfigHashOf(rawYAML), this hash
// excludes node-specific fields (port, node_alias, log_path, etc.) and
// node-local notification script paths, so all nodes with identical cluster
// config will produce the same hash.
func (e *Engine) SharedConfigHash() string {
	sc, err := e.ExtractSharedConfig()
	if err != nil {
		return ""
	}
	data, err := json.Marshal(sc)
	if err != nil {
		return ""
	}
	return cluster.ConfigHashOf(data)
}

// ── Apply: SharedConfig → this node's config.yaml ────────────────────────────

// ApplySharedConfigJSON merges the given SharedConfig JSON into this node's
// config.yaml on disk, then triggers Reload() to activate the changes.
//
// Only fields present in sc are overwritten; node-specific fields are never
// touched. The write is atomic: a .tmp file is written first, then renamed.
func (e *Engine) ApplySharedConfigJSON(raw json.RawMessage) error {
	var sc SharedConfig
	if err := json.Unmarshal(raw, &sc); err != nil {
		return fmt.Errorf("parse SharedConfig: %w", err)
	}
	return e.applySharedConfig(sc)
}

// applySharedConfig merges a partial shared config (received via PUT
// /cluster/config or gossiped from a peer) into this node's on-disk config.yaml,
// touching only the cluster-shared fields and preserving node-specific bootstrap
// fields, then reloads so the change takes effect. Returns an error if the config
// can't be read, rewritten or reloaded.
func (e *Engine) applySharedConfig(sc SharedConfig) error {
	e.mu.RLock()
	rawBytes := e.rawConfigBytes
	cfgPath := e.configPath
	e.mu.RUnlock()

	if cfgPath == "" {
		cfgPath = "config.yaml"
	}

	// Parse the current on-disk config as a generic map (preserves ${VAR} refs).
	var m map[string]interface{}
	if len(rawBytes) > 0 {
		if err := yaml.Unmarshal(rawBytes, &m); err != nil {
			return fmt.Errorf("parse current config: %w", err)
		}
	} else {
		m = make(map[string]interface{})
	}
	if m == nil {
		m = make(map[string]interface{})
	}

	// Convert SharedConfig → map and deep-merge into m (shared keys only).
	scJSON, err := json.Marshal(sc)
	if err != nil {
		return fmt.Errorf("marshal incoming config: %w", err)
	}
	var scMap map[string]interface{}
	if err := json.Unmarshal(scJSON, &scMap); err != nil {
		return fmt.Errorf("unmarshal incoming config: %w", err)
	}

	mergeSharedMap(m, scMap)

	// Marshal merged map back to YAML.
	out, err := yaml.Marshal(m)
	if err != nil {
		return fmt.Errorf("marshal merged config: %w", err)
	}

	// Atomic write: .tmp → rename.
	tmp := cfgPath + ".tmp"
	if err := os.WriteFile(tmp, out, 0644); err != nil {
		return fmt.Errorf("write temp config %s: %w", tmp, err)
	}
	if err := os.Rename(tmp, cfgPath); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("rename config: %w", err)
	}

	slog.Info("[CONFIG-PUSH] config updated on disk, reloading", "path", cfgPath)
	e.Reload()
	return nil
}

// mergeSharedMap copies shared top-level keys from src into dst.
// For the "cluster" key the merge is one level deep (individual cluster
// fields are updated without overwriting node-specific cluster fields).
func mergeSharedMap(dst, src map[string]interface{}) {
	topLevelShared := map[string]bool{
		"timeout": true, "max_retries": true, "retry_interval_sec": true,
		"ticker_interval_sec": true, "probe_interval_sec": true,
		"reload_interval_sec": true, "watchdog_threshold_sec": true,
		"recovery_probes": true,
		"notifications":   true, "default_notify": true,
	}
	clusterShared := map[string]bool{
		"keyring": true, "peers": true, "expected_node_count": true,
		"min_quorum_ratio": true, "probe_replication_factor": true,
		"probe_replication_percent": true, "min_probe_confirmations": true,
	}

	for k, v := range src {
		if k == "cluster" {
			// Merge cluster sub-fields individually.
			srcC, ok := v.(map[string]interface{})
			if !ok {
				continue
			}
			dstC, _ := dst["cluster"].(map[string]interface{})
			if dstC == nil {
				dstC = make(map[string]interface{})
			}
			for ck, cv := range srcC {
				if clusterShared[ck] {
					dstC[ck] = cv
				}
			}
			dst["cluster"] = dstC
		} else if topLevelShared[k] {
			dst[k] = v
		}
	}
}

// ── fieldsApplied helper ──────────────────────────────────────────────────────

// AppliedFields returns a sorted list of top-level SharedConfig fields that are
// non-zero in sc, for informational reporting in ConfigPushResult.
func AppliedFields(sc SharedConfig) []string {
	var fields []string
	if sc.Timeout != 0 {
		fields = append(fields, "timeout")
	}
	if sc.MaxRetries != nil {
		fields = append(fields, "max_retries")
	}
	if sc.RetryIntervalSec != nil {
		fields = append(fields, "retry_interval_sec")
	}
	if sc.TickerIntervalSec != nil {
		fields = append(fields, "ticker_interval_sec")
	}
	if sc.ProbeIntervalSec != nil {
		fields = append(fields, "probe_interval_sec")
	}
	if sc.ReloadIntervalSec != nil {
		fields = append(fields, "reload_interval_sec")
	}
	if sc.WatchdogThresholdSec != nil {
		fields = append(fields, "watchdog_threshold_sec")
	}
	if sc.RecoveryProbes != nil {
		fields = append(fields, "recovery_probes")
	}
	if len(sc.Notifications) > 0 {
		fields = append(fields, "notifications")
	}
	if len(sc.DefaultNotify) > 0 {
		fields = append(fields, "default_notify")
	}
	if sc.Cluster != nil {
		fields = append(fields, "cluster.*")
	}
	return fields
}

// configPathFor returns the resolved config file path without acquiring locks.
// Mirrors the path-resolution logic in LoadConfig.
func (e *Engine) configPathFor() string {
	if e.configPath != "" {
		return e.configPath
	}
	if p := os.Getenv("CONFIG_PATH"); p != "" {
		return p
	}
	if _, err := os.Stat("config.yaml"); err == nil {
		return "config.yaml"
	}
	return filepath.Join(filepath.Dir(os.Args[0]), "config.yaml")
}
