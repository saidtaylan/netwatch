package engine

import (
	"bufio"
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"log"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/saidtaylan/netwatch/internal/cluster"
	"sigs.k8s.io/yaml"
)

// varPattern matches ${VARNAME} placeholders in config values.
var varPattern = regexp.MustCompile(`\$\{([^}]+)\}`)

// parseEnvFile reads a KEY=VALUE credentials file.
// Lines beginning with # and blank lines are skipped.
func parseEnvFile(path string) (map[string]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("env file: %w", err)
	}
	defer f.Close()

	vars := make(map[string]string)
	sc := bufio.NewScanner(f)
	line := 0
	for sc.Scan() {
		line++
		raw := sc.Text()
		trimmed := strings.TrimSpace(raw)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		idx := strings.IndexByte(trimmed, '=')
		if idx < 0 {
			return nil, fmt.Errorf("env file: line %d: expected KEY=VALUE, got %q", line, trimmed)
		}
		key := strings.TrimSpace(trimmed[:idx])
		val := trimmed[idx+1:]
		if key == "" {
			return nil, fmt.Errorf("env file: line %d: empty key", line)
		}
		vars[key] = val
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("env file: read error: %w", err)
	}
	return vars, nil
}

// resolveVars replaces every ${VARNAME} in data.
// Resolution order: vars map → system env. Returns error for unresolved placeholders.
// Full-line comments (lines whose first non-space char is #) and the inline-comment
// portion of any line (# outside quoted strings) are left untouched.
func resolveVars(data []byte, vars map[string]string) ([]byte, error) {
	var unresolved []string
	lines := bytes.Split(data, []byte("\n"))
	for i, line := range lines {
		if trimmed := bytes.TrimSpace(line); len(trimmed) == 0 || trimmed[0] == '#' {
			continue
		}
		value, comment := splitInlineComment(line)
		replaced := varPattern.ReplaceAllFunc(value, func(m []byte) []byte {
			key := string(m[2 : len(m)-1])
			if v, ok := vars[key]; ok {
				return []byte(v)
			}
			if v, ok := os.LookupEnv(key); ok {
				return []byte(v)
			}
			unresolved = append(unresolved, key)
			return m
		})
		lines[i] = append(replaced, comment...)
	}
	if len(unresolved) > 0 {
		return nil, fmt.Errorf("unresolved variable(s): ${%s}", strings.Join(unresolved, "}, ${"))
	}
	return bytes.Join(lines, []byte("\n")), nil
}

// splitInlineComment splits a YAML line into the value part and the trailing
// comment (including the # character). The split point is the first '#' that
// appears outside single- or double-quoted strings and is preceded by
// whitespace. If no such '#' exists the whole line is returned as value.
func splitInlineComment(line []byte) (value, comment []byte) {
	inSingle, inDouble := false, false
	for i, b := range line {
		switch {
		case b == '\'' && !inDouble:
			inSingle = !inSingle
		case b == '"' && !inSingle:
			inDouble = !inDouble
		case b == '#' && !inSingle && !inDouble:
			if i > 0 && (line[i-1] == ' ' || line[i-1] == '\t') {
				return line[:i], line[i:]
			}
		}
	}
	return line, nil
}

// ── Config ────────────────────────────────────────────────────────────────────

// Target describes a single monitored endpoint.
type Target struct {
	// ID is an optional stable identifier. When set, App.Uses references must
	// match it; when empty, Name is used as the canonical key. Existing
	// configs without ID continue to work unchanged.
	ID string `json:"id,omitempty"`

	Type    string          `json:"type"`
	Target  string          `json:"target"`
	Name    string          `json:"name"`
	Enabled *bool           `json:"enabled,omitempty"`
	Options json.RawMessage `json:"options,omitempty"`

	Notify           []string `json:"notify,omitempty"`
	MaxRetries       *int     `json:"max_retries,omitempty"`
	RetryIntervalSec *int     `json:"retry_interval_sec,omitempty"`
	Timeout          *int     `json:"timeout,omitempty"`
	IntervalSec      *int     `json:"interval_sec,omitempty"` // per-target probe cadence

	// RecoveryProbes overrides the global recovery_probes for this specific
	// target. Default: inherits Config.RecoveryProbes (or 1 if not set).
	RecoveryProbes *int `json:"recovery_probes,omitempty"`

	// DependsOn lists target IDs (or names) that this target depends on.
	// When a dependency is hard_down at the time this target goes down, that
	// dependency is reported as the ROOT_CAUSE in alert notifications.
	// Cyclic references and unknown IDs are rejected at config load time.
	DependsOn []string `json:"depends_on,omitempty"`

	// ProbeFrom optionally pins probe execution to a fixed set of node names.
	// When non-empty, only the listed nodes are considered candidates for this
	// target — overriding the automatic hash-ring + zone selection. Useful when
	// network reachability or credentials are restricted to specific nodes.
	//
	// All nodes that carry this target in their config SHOULD declare the same
	// ProbeFrom list; mismatched lists cause each node to compute a different
	// candidate set, which breaks exactly-once alerting. Operator's responsibility.
	//
	// When the list is empty (default) the cluster picks probers automatically.
	// In standalone mode (no cluster) ProbeFrom is ignored — the single node
	// always probes its own targets.
	ProbeFrom []string `json:"probe_from,omitempty"`

	// ProbeFromRegions restricts probing to nodes whose cluster.region label
	// matches one of the listed geographic region names (P1.6). Nodes without a
	// region label are excluded when this constraint is active. Applied after
	// ProbeFrom — the two constraints are ANDed together.
	// Empty / nil means no regional restriction.
	ProbeFromRegions []string `json:"probe_from_regions,omitempty"`
}

func (t Target) active() bool { return t.Enabled == nil || *t.Enabled }

// key returns the canonical lookup key (ID if set, else Name).
// It is used as the state.json key and the AppTargetIndex key.
func (t Target) key() string {
	if t.ID != "" {
		return t.ID
	}
	return t.Name
}

func (t Target) typeKey() string { return t.Type + "|" + t.key() }

// Config is the top-level configuration structure.
type Config struct {
	Port      string `json:"port"`
	// NodeAlias is an optional human-readable label for this agent instance.
	// Appears in Prometheus metric labels (label name "app_name") and alert env vars
	// (NODE_ALIAS). Omitting it is fine — metrics and alerts still work without it.
	NodeAlias string `json:"node_alias,omitempty"`
	LogPath   string `json:"log_path"`   // state-change log file; empty = stdout only
	StateFile string `json:"state_file"` // persisted target states; prevents spurious alarms after restart
	Timeout   int    `json:"timeout"`

	// ProbeIntervalSec is the global default probe cadence in seconds (default 60).
	// Each target can override this with its own interval_sec field.
	ProbeIntervalSec *int `json:"probe_interval_sec,omitempty"`

	// ReloadIntervalSec controls how often the engine checks for config file changes (default 30s).
	ReloadIntervalSec *int `json:"reload_interval_sec,omitempty"`

	// WatchdogThresholdSec: if Prometheus does not scrape /metrics for this many
	// seconds the agent logs [WATCHDOG] and sets network_probe_prometheus_connected=0.
	// 0 (default) disables the watchdog.
	WatchdogThresholdSec *int `json:"watchdog_threshold_sec,omitempty"`

	MaxRetries        *int `json:"max_retries,omitempty"`
	RetryIntervalSec  *int `json:"retry_interval_sec,omitempty"`
	TickerIntervalSec *int `json:"ticker_interval_sec,omitempty"`

	// RecoveryProbes is the number of consecutive successful probes required
	// before a hard_down target is declared recovered and a "reachable" alert
	// fires. Default 1 (current behaviour) — set higher (e.g. 2 or 3) on
	// targets prone to transient false recoveries (flapping).
	// Symmetric to max_retries: just as max_retries protects against brief blips
	// causing false alarms, recovery_probes protects against brief recoveries
	// causing premature "all-clear" alerts.
	RecoveryProbes *int `json:"recovery_probes,omitempty"`

	CredentialsFile string                        `json:"credentials_file,omitempty"`
	Notifications   map[string]AlertChannelConfig `json:"notifications,omitempty"`
	DefaultNotify   []string                      `json:"default_notify,omitempty"`
	Targets         []Target                      `json:"targets"`

	// Apps is an optional list of logical applications that depend on
	// targets. Defining apps enriches notifications with AFFECTED_APPS and
	// OWNER_TEAMS context. Configs without apps continue to work unchanged.
	Apps []App `json:"apps,omitempty"`

	// Admin holds authentication settings for write-capable HTTP endpoints
	// (config push/sync, keyring rotation, cluster leave).
	// When nil or Token is empty, those endpoints are unrestricted.
	Admin *AdminConfig `json:"admin,omitempty"`

	// Cluster holds gossip cluster settings. When Cluster.Enabled is false
	// (the default) the entire cluster layer is a no-op.
	Cluster cluster.Config `json:"cluster,omitempty"`

	// SLO holds the SLO tracking configuration.
	// When nil or Enabled=false, incident tracking and /slo are disabled.
	SLO *SLOConfig `json:"slo,omitempty"`
}

// AdminConfig controls access to write-capable HTTP endpoints.
//
// Designed for extensibility: the Token field covers the current single-secret
// bearer-token scheme. When user management is added in a future release, a
// Users []AdminUser field will be appended here without breaking existing configs.
//
// If Token is empty (default), write endpoints are unrestricted — appropriate
// for deployments where network-level access controls are sufficient.
type AdminConfig struct {
	// Token is the required Bearer token for write-capable admin endpoints
	// (PUT /cluster/config, POST /cluster/config/sync, POST /cluster/keyring/rotate,
	// POST /cluster/leave). Supports ${VAR} substitution from credentials_file.
	// When empty, those endpoints accept any request without authentication.
	Token string `json:"token,omitempty"`

	// CORSOrigin is the value for the Access-Control-Allow-Origin header.
	// When empty, the server defaults to "*" (permissive).
	// Example: "https://netwatch.yourcompany.local"
	CORSOrigin string `json:"cors_origin,omitempty"`
}

// AdminToken returns the configured admin token (empty = no auth required).
func (e *Engine) AdminToken() string {
	e.mu.RLock()
	defer e.mu.RUnlock()
	if e.cfg.Admin == nil {
		return ""
	}
	return e.cfg.Admin.Token
}

// CORSOrigin returns the configured CORS origin (empty = use "*").
func (e *Engine) CORSOrigin() string {
	e.mu.RLock()
	defer e.mu.RUnlock()
	if e.cfg.Admin == nil {
		return ""
	}
	return e.cfg.Admin.CORSOrigin
}

func (c Config) globalMaxRetries() int {
	if c.MaxRetries != nil {
		return *c.MaxRetries
	}
	return 1
}

func (c Config) globalRecoveryProbes() int {
	if c.RecoveryProbes != nil && *c.RecoveryProbes > 0 {
		return *c.RecoveryProbes
	}
	return 1 // default: 1 successful probe = recovered (backward compat)
}

func (c Config) globalRetryInterval() int {
	if c.RetryIntervalSec != nil {
		return *c.RetryIntervalSec
	}
	return 30
}

func (c Config) globalTickerInterval() int {
	if c.TickerIntervalSec != nil {
		return *c.TickerIntervalSec
	}
	return 5
}

func (c Config) globalProbeInterval() int {
	if c.ProbeIntervalSec != nil {
		return *c.ProbeIntervalSec
	}
	return 60
}

func (c Config) globalReloadInterval() int {
	if c.ReloadIntervalSec != nil && *c.ReloadIntervalSec >= 5 {
		return *c.ReloadIntervalSec
	}
	return 30
}

func (c Config) watchdogThresholdSec() int {
	if c.WatchdogThresholdSec != nil {
		return *c.WatchdogThresholdSec
	}
	return 0 // disabled by default
}

// ── Metrics ───────────────────────────────────────────────────────────────────

var (
	LabelNames = []string{"name", "target", "type", "source_host", "app_name"}

	// GaugeUp reports the most recent probe outcome on this node.
	// In standalone mode this is the only status metric. When the cluster
	// layer (Aşama 6+) ships, a sibling network_probe_cluster_status will
	// hold the consensus value across nodes.
	GaugeUp = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "network_probe_local_status",
		Help: "Local probe result on this node: 1=reachable, 0=unreachable",
	}, LabelNames)

	GaugeDuration = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "network_probe_local_latency_seconds",
		Help: "Most recent probe round-trip time in seconds (this node)",
	}, LabelNames)

	// GaugePrometheusConnected is set to 1 when Prometheus is scraping on time,
	// 0 when the watchdog threshold is exceeded. Starts at 1 (optimistic).
	// Only meaningful when watchdog_threshold_sec > 0.
	GaugePrometheusConnected = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "network_probe_prometheus_connected",
		Help: "1 = Prometheus scraping on schedule, 0 = scrape gap exceeded watchdog_threshold_sec",
	})

	// Cluster metrics — registered only when cluster.enabled=true via RegisterClusterMetrics.
	// Keeping them as package-level vars allows updateClusterMetrics to reference them
	// without needing a reference to the registry at call time.

	GaugeQuorumHealthy = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "network_prober_quorum_healthy",
		Help: "1 = cluster quorum is healthy, 0 = quorum lost (majority of expected nodes unreachable)",
	})

	GaugeIsolated = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "network_prober_isolated",
		Help: "1 = node is operating in isolated mode (no quorum), 0 = normal cluster operation",
	})

	GaugeClusterSize = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "network_prober_cluster_size",
		Help: "Number of alive cluster members as seen by this node",
	})

	// GaugeClusterStatus reflects the consensus view across all cluster nodes.
	// 1 = every node that has reported on this target sees it as up.
	// 0 = at least one node sees it as down (or hard_down).
	// Updated every 5 s by runClusterMetricsUpdater.
	GaugeClusterStatus = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "network_probe_cluster_status",
		Help: "Cluster-wide consensus probe result: 1=all nodes up, 0=any node down",
	}, LabelNames)

	// ── Phase 13: probe ownership metrics ──────────────────────────────────

	// GaugeLocalAssigned is 1 when this node is one of the selected probers
	// for the labelled target, 0 otherwise. Operators consult this to
	// understand why a node is or is not running probes.
	GaugeLocalAssigned = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "network_probe_local_assigned",
		Help: "1 = this node probes the target locally, 0 = leaves it to other cluster members",
	}, []string{"name", "target", "type"})

	// GaugeProberCount reports how many cluster members are running probes
	// for the labelled target. Helpful for verifying that ProbeReplicationFactor
	// is taking effect.
	GaugeProberCount = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "network_probe_prober_count",
		Help: "Number of cluster members currently assigned to probe the target",
	}, []string{"name", "target", "type"})

	// GaugeInventoryPeers reports the number of peers whose target inventory
	// this node has observed (i.e. peers that have broadcast at least one
	// state). Diverges from cluster size during bootstrap or anti-entropy.
	GaugeInventoryPeers = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "network_probe_inventory_peers",
		Help: "Number of distinct peer nodes whose target inventory has been observed via gossip",
	})

	// GaugeTargetOrphaned is 1 when the labelled target has no eligible prober
	// in the cluster — typically because `probe_from` or `probe_from_regions`
	// filtered the candidate set to empty, or every candidate is dead.
	// 0 when at least one node is selected to probe it.
	GaugeTargetOrphaned = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "network_probe_target_orphaned",
		Help: "1 = no cluster member is currently assigned to probe this target (check probe_from / probe_from_regions / cluster.zone)",
	}, []string{"name", "target", "type"})

	// GaugeProberUnderreplicated is 1 when the actual number of assigned probers
	// is below probe_replication_factor but above zero. Unlike orphaned (which
	// means nobody probes), underreplicated means coverage is degraded — fewer
	// probers than intended are watching the target. Typically caused by node
	// failures or probe_from pin lists with insufficient alive candidates.
	GaugeProberUnderreplicated = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "network_probe_prober_underreplicated",
		Help: "1 = target has fewer assigned probers than probe_replication_factor (coverage degraded but not zero)",
	}, []string{"name", "target", "type"})
)

// RegisterMetrics registers the core (non-cluster) metrics with reg.
func RegisterMetrics(reg *prometheus.Registry) {
	reg.MustRegister(prometheus.NewProcessCollector(prometheus.ProcessCollectorOpts{}))
	reg.MustRegister(prometheus.NewGoCollector())
	reg.MustRegister(GaugeUp, GaugeDuration, GaugePrometheusConnected)
	GaugePrometheusConnected.Set(1) // optimistic default; watchdog will clear if needed
}

// RegisterClusterMetrics registers cluster-specific metrics with reg.
// Must be called only when cluster.enabled=true.  Calling it when cluster is
// disabled would register gauges that are never updated, which is confusing.
func RegisterClusterMetrics(reg *prometheus.Registry) {
	reg.MustRegister(GaugeQuorumHealthy, GaugeIsolated, GaugeClusterSize, GaugeClusterStatus)
	reg.MustRegister(GaugeLocalAssigned, GaugeProberCount, GaugeInventoryPeers)
	reg.MustRegister(GaugeTargetOrphaned, GaugeProberUnderreplicated)
	// P1.5 config drift metric.
	reg.MustRegister(cluster.GaugeConfigDrift)
	// P1.6 geo-latency metrics.
	reg.MustRegister(cluster.GaugeGeoLatency, cluster.GaugeGeoLatencyAnomaly)
	GaugeQuorumHealthy.Set(1) // optimistic: assume quorum until first check
	GaugeIsolated.Set(0)
	GaugeClusterSize.Set(0)
	GaugeInventoryPeers.Set(0)
}

// ── Logger ────────────────────────────────────────────────────────────────────

// initLogger configures the default slog handler.
//
//   - logPath == "" → text handler on stdout
//   - logPath != "" → JSON handler writing to both file and stdout
func initLogger(logPath string) error {
	var h slog.Handler
	if logPath == "" {
		h = slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelDebug})
	} else {
		f, err := os.OpenFile(logPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
		if err != nil {
			return fmt.Errorf("open log file %s: %w", logPath, err)
		}
		// JSON to file, text to stdout simultaneously.
		jsonH := slog.NewJSONHandler(f, &slog.HandlerOptions{Level: slog.LevelDebug})
		textH := slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelDebug})
		h = &multiHandler{handlers: []slog.Handler{jsonH, textH}}
	}
	slog.SetDefault(slog.New(h))
	// Keep stdlib log in sync for third-party libraries.
	log.SetOutput(newSlogWriter())
	log.SetFlags(0)
	return nil
}

// multiHandler fans out a single log record to multiple slog.Handlers.
type multiHandler struct{ handlers []slog.Handler }

func (m *multiHandler) Enabled(ctx context.Context, level slog.Level) bool {
	for _, h := range m.handlers {
		if h.Enabled(ctx, level) {
			return true
		}
	}
	return false
}
func (m *multiHandler) Handle(ctx context.Context, r slog.Record) error {
	for _, h := range m.handlers {
		_ = h.Handle(ctx, r)
	}
	return nil
}
func (m *multiHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	hs := make([]slog.Handler, len(m.handlers))
	for i, h := range m.handlers {
		hs[i] = h.WithAttrs(attrs)
	}
	return &multiHandler{handlers: hs}
}
func (m *multiHandler) WithGroup(name string) slog.Handler {
	hs := make([]slog.Handler, len(m.handlers))
	for i, h := range m.handlers {
		hs[i] = h.WithGroup(name)
	}
	return &multiHandler{handlers: hs}
}

// slogWriter bridges stdlib log → slog.Info so third-party libs use our handler.
type slogWriter struct{}

func newSlogWriter() *slogWriter { return &slogWriter{} }
func (w *slogWriter) Write(p []byte) (int, error) {
	slog.Info(strings.TrimRight(string(p), "\n"))
	return len(p), nil
}

// ── State persistence ─────────────────────────────────────────────────────────

// PersistedState holds the last known health state for a single target.
// It is written to state.json so the agent can resume without false alarms
// after a restart.
//
// Seq is a monotonically increasing counter bumped on every state transition
// (hard-down and recovery). It will be used as the causal key in cluster mode
// so that conflicting peer observations can be ordered deterministically.
//
// OwnerNode is currently always empty (standalone mode). The cluster layer
// (Phase 6+) will set it to the node name responsible for this target so that
// every peer can track which observation wins in gossip anti-entropy.
type PersistedState struct {
	State     string `json:"state"`                // "up" | "hard_down"
	Seq       uint64 `json:"seq"`                  // monotonic transition counter
	ErrorCode string `json:"error_code,omitempty"` // last probe error text; empty when up
	OwnerNode string `json:"owner_node,omitempty"` // cluster: responsible node (reserved)
}

// stateFileV2 is the on-disk JSON envelope for state.json (format version 2).
// Version 1 was a plain map[string]bool; loadPersistedState auto-migrates.
type stateFileV2 struct {
	Version int                       `json:"version"`
	Targets map[string]PersistedState `json:"targets"`
}

func (e *Engine) loadPersistedState() {
	e.mu.RLock()
	path := e.cfg.StateFile
	e.mu.RUnlock()
	if path == "" {
		return
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if !os.IsNotExist(err) {
			slog.Error("failed to read persist file", "path", path, "err", err)
		}
		return
	}

	m := make(map[string]PersistedState)
	migrated := false

	// Try v2 format first ({"version":2,"targets":{...}}).
	var v2 stateFileV2
	if jsonErr := json.Unmarshal(data, &v2); jsonErr == nil && v2.Version == 2 {
		if v2.Targets != nil {
			m = v2.Targets
		}
	} else {
		// Fall back to v1 format: plain map[string]bool
		var v1 map[string]bool
		if jsonErr := json.Unmarshal(data, &v1); jsonErr == nil {
			for k, up := range v1 {
				st := "hard_down"
				if up {
					st = "up"
				}
				m[k] = PersistedState{State: st}
			}
			migrated = true
			slog.Info("state migrated from v1 format", "path", path, "count", len(m))
		} else {
			slog.Error("failed to parse persist file", "path", path, "err", jsonErr)
			return
		}
	}

	e.stateMu.Lock()
	e.lastKnown = m
	e.stateMu.Unlock()
	slog.Info("state loaded", "path", path, "count", len(m))

	// Immediately rewrite the file in v2 format so future starts don't re-migrate.
	if migrated {
		e.persistState()
	}
}

func (e *Engine) persistState() {
	e.mu.RLock()
	path := e.cfg.StateFile
	e.mu.RUnlock()
	if path == "" {
		return
	}
	e.stateMu.RLock()
	v2 := stateFileV2{
		Version: 2,
		Targets: e.lastKnown,
	}
	data, err := json.MarshalIndent(v2, "", "  ")
	e.stateMu.RUnlock()
	if err != nil {
		slog.Error("failed to marshal state", "err", err)
		return
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0644); err != nil {
		slog.Error("failed to write state file", "path", tmp, "err", err)
		return
	}
	if err := os.Rename(tmp, path); err != nil {
		slog.Error("failed to rename state file", "src", tmp, "dst", path, "err", err)
	}
}

// ── Engine ────────────────────────────────────────────────────────────────────

// Engine is the core monitoring runtime.
type Engine struct {
	mu          sync.RWMutex
	cfg         Config
	channels    map[string]Alerter
	appIndex    AppTargetIndex // target.key() → apps that depend on it; empty when no apps configured
	hostname    string
	alertRunner AlertRunner
	checkers    map[string]Checker
	configPath  string    // path to config.yaml; set by New()
	configMtime time.Time // mtime of config at last successful load

	// State maps — both guarded by stateMu.
	// persistState must NOT be called while holding stateMu.
	stateMu   sync.RWMutex
	lastKnown map[string]PersistedState // persisted; key = target.key()
	pending   map[string]PendingEntry   // soft-down queue; RAM only; key = target.typeKey()

	// Per-target probe goroutine management.
	probesMu      sync.Mutex
	probeCancel   map[string]context.CancelFunc // key = target.key()
	probeFastCheck map[string]chan struct{}       // key = target.key(); co-prober soft-down trigger

	// pendingRecovery tracks targets in SOFT_UP state — i.e. hard_down targets
	// that have seen at least one successful probe but haven't yet reached the
	// recovery_probes threshold. Guarded by stateMu.
	pendingRecovery map[string]int // key = typeKey(), value = consecutive success count

	// retryStop cancels the background retry-loop goroutine.
	retryStop func()

	// clusterMgr is non-nil only when cluster.enabled=true.
	// All cluster code paths are guarded by nil checks.
	clusterMgr *cluster.Manager

	// lastScrapeNano holds the Unix nanosecond timestamp of the most recent
	// /metrics HTTP request. Written by NotifyScrape, read by runWatchdog.
	lastScrapeNano atomic.Int64

	// syncing is set true while an anti-entropy state merge is in progress
	// (MergeRemoteState with join=true). runCheck and processPending return
	// immediately when true, preventing alarm storms on node re-join.
	syncing atomic.Bool

	// localProbeIDs is the set of target keys (target.key()) that this engine
	// instance probes locally. Built in Init and rebuilt on hot-reload.
	// Read by HasLocalProbe (cluster.PeerAlertHandler) — guarded by mu.
	localProbeIDs map[string]bool

	// topoGraph holds the dependency graph derived from Target.DependsOn entries.
	// nil when no target declares any dependencies (most configs). Guarded by mu.
	topoGraph *DependencyGraph

	// sloMgr manages incident history and SLO budget calculation.
	// nil when slo.enabled is false (the default).
	sloMgr *sloManager

	// lastLatency stores the most recently measured probe round-trip for each
	// target key (string → float64 seconds). Written by runCheck on success,
	// read by broadcastState to populate GossipPayload.Latency for P1.6.
	// sync.Map avoids the engine-wide mu/stateMu for this hot-path field.
	lastLatency sync.Map

	// maintMgr handles API-driven maintenance windows (suppress alerts for
	// specific targets for a duration). nil on first use; initialized in Init().
	maintMgr *maintenanceManager

	// orphanedSet tracks which local targets currently have no cluster prober
	// assigned, so updateClusterMetrics can log only on transitions (edge-
	// triggered) instead of every 5 s. Guarded by orphanedMu.
	orphanedMu  sync.Mutex
	orphanedSet map[string]bool

	// rawConfigBytes holds the pre-credential-injection bytes of the last
	// successfully loaded config file. Used by configpush /sync to export
	// shared fields without leaking resolved secrets to peer nodes.
	rawConfigBytes []byte

	// Suppress repeated credential-load log entries.
	credLogged bool

	// shutdownOnce ensures Shutdown is idempotent.
	shutdownOnce sync.Once
}

// StatusSnapshot represents the current state of a target.
type StatusSnapshot struct {
	Name          string     `json:"name"`
	Target        string     `json:"target"`
	Type          string     `json:"type"`
	Status        string     `json:"status"`
	Seq           uint64     `json:"seq"`
	ErrorCode     string     `json:"error_code,omitempty"`
	RetryCount    int        `json:"retry_count"`
	NextCheckTime *time.Time `json:"next_check_time,omitempty"`
}

// Status returns a snapshot of all active targets.
func (e *Engine) Status() []StatusSnapshot {
	e.mu.RLock()
	targets := e.cfg.Targets
	e.mu.RUnlock()

	e.stateMu.RLock()
	defer e.stateMu.RUnlock()

	var out []StatusSnapshot
	for _, t := range targets {
		if !t.active() {
			continue
		}

		snap := StatusSnapshot{
			Name:   t.Name,
			Target: t.Target,
			Type:   t.Type,
		}

		pkey := t.typeKey()
		if p, ok := e.pending[pkey]; ok {
			snap.Status = "SOFT_DOWN"
			snap.RetryCount = p.RetryCount
			snap.ErrorCode = p.LastErrorCode
			next := p.NextCheckTime
			snap.NextCheckTime = &next
		} else if ps, ok := e.lastKnown[t.key()]; ok {
			if ps.State == "up" {
				snap.Status = "UP"
			} else {
				snap.Status = "HARD_DOWN"
			}
			snap.Seq = ps.Seq
			snap.ErrorCode = ps.ErrorCode
		} else {
			snap.Status = "UNKNOWN"
		}
		out = append(out, snap)
	}
	return out
}

// New creates an Engine. configPath is the path to config.yaml; pass "" to use
// ValidateConfigFile loads and validates the config at path without starting
// any goroutines or network connections. It returns a summary of what was
// found or a descriptive error. Safe to call from a CLI validate subcommand.
func ValidateConfigFile(path string) (cfg Config, err error) {
	e := New("localhost", ShellRunner, path)
	if loadErr := e.LoadConfig(); loadErr != nil {
		return Config{}, loadErr
	}
	e.mu.RLock()
	cfg = e.cfg
	e.mu.RUnlock()
	return cfg, nil
}

// the default (CONFIG_PATH env var → config.yaml next to the binary).
// Call Init() before use.
func New(hostname string, runner AlertRunner, configPath string) *Engine {
	_ = configPath                                  // stored below
	tlsCfg := &tls.Config{InsecureSkipVerify: true} //nolint:gosec
	tr := &http.Transport{
		TLSClientConfig:     tlsCfg,
		MaxIdleConns:        100,
		IdleConnTimeout:     90 * time.Second,
		TLSHandshakeTimeout: 10 * time.Second,
	}
	hc := &http.Client{
		Transport: tr,
		Timeout:   0,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if follow, ok := req.Context().Value(followKey).(bool); ok && !follow {
				return http.ErrUseLastResponse
			}
			limit := 10
			if v, ok := req.Context().Value(maxHopsKey).(int); ok {
				limit = v
			}
			if len(via) >= limit {
				return fmt.Errorf("stopped after %d redirects", limit)
			}
			return nil
		},
	}

	e := &Engine{
		hostname:    hostname,
		alertRunner: runner,
		configPath:  configPath,
		lastKnown:       make(map[string]PersistedState),
		pending:         make(map[string]PendingEntry),
		pendingRecovery: make(map[string]int),
		probeCancel:     make(map[string]context.CancelFunc),
		probeFastCheck:  make(map[string]chan struct{}),
	}
	hck := &httpChecker{client: hc}
	e.checkers = map[string]Checker{
		"http": hck,
		"tcp":  &tcpChecker{},
		"sql":  &sqlChecker{},
		"ping": &pingChecker{},
		"dns":  &dnsChecker{},
	}
	return e
}

// NodeAlias returns the configured node_alias (safe for concurrent use).
func (e *Engine) NodeAlias() string {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.cfg.NodeAlias
}

// clusterNodeName returns the cluster-configured node name when cluster is
// enabled, and falls back to the OS hostname for standalone mode.
// This value is the authoritative identity used in gossip payloads and must
// match the name used by the consistent hash ring (cfg.Cluster.NodeName).
func (e *Engine) clusterNodeName() string {
	e.mu.RLock()
	n := e.cfg.Cluster.NodeName
	e.mu.RUnlock()
	if n != "" {
		return n
	}
	return e.hostname
}

// Port returns the configured HTTP port (defaults to "9115").
func (e *Engine) Port() string {
	e.mu.RLock()
	defer e.mu.RUnlock()
	if e.cfg.Port == "" {
		return "9115"
	}
	return e.cfg.Port
}

// Shutdown stops all probe goroutines, the retry loop, and gracefully leaves
// the cluster (if enabled) so other nodes update their membership tables.
func (e *Engine) Shutdown() {
	e.shutdownOnce.Do(func() {
		e.probesMu.Lock()
		for _, cancel := range e.probeCancel {
			cancel()
		}
		e.probesMu.Unlock()

		if e.retryStop != nil {
			e.retryStop()
		}

		if e.clusterMgr != nil {
			if err := e.clusterMgr.Leave(5 * time.Second); err != nil {
				slog.Warn("cluster leave error", "err", err)
			}
		}
	})
}

// ClusterManager returns the cluster Manager (nil when cluster is disabled).
// Exposed so cmd/linux/main.go can serve the /cluster/state endpoint.
func (e *Engine) ClusterManager() *cluster.Manager {
	return e.clusterMgr
}

// SLOEnabled reports whether the SLO tracker is active.
// Used by cmd/linux/main.go to decide whether to register SLO metrics.
func (e *Engine) SLOEnabled() bool {
	return e.sloMgr != nil
}

// ── SLO Target CRUD (B12) ─────────────────────────────────────────────────────

// SLOTargets returns the current list of SLO targets (from in-memory config).
func (e *Engine) SLOTargets() []SLOTarget {
	e.mu.RLock()
	defer e.mu.RUnlock()
	if e.cfg.SLO == nil {
		return nil
	}
	out := make([]SLOTarget, len(e.cfg.SLO.Targets))
	copy(out, e.cfg.SLO.Targets)
	return out
}

// UpsertSLOTarget adds or updates an SLO target in the in-memory config.
// Changes are held in RAM; they survive hot-reload because Reload() re-reads
// config.yaml (which may not have the change), so callers should persist to
// config.yaml separately if desired. For now, changes are lost on restart.
func (e *Engine) UpsertSLOTarget(st SLOTarget) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.cfg.SLO == nil {
		e.cfg.SLO = &SLOConfig{Enabled: true}
	}
	for i, t := range e.cfg.SLO.Targets {
		if t.ID == st.ID {
			e.cfg.SLO.Targets[i] = st
			return
		}
	}
	e.cfg.SLO.Targets = append(e.cfg.SLO.Targets, st)
}

// DeleteSLOTarget removes an SLO target from the in-memory config.
// Returns true if it was found and removed.
func (e *Engine) DeleteSLOTarget(id string) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.cfg.SLO == nil {
		return false
	}
	for i, t := range e.cfg.SLO.Targets {
		if t.ID == id {
			e.cfg.SLO.Targets = append(e.cfg.SLO.Targets[:i], e.cfg.SLO.Targets[i+1:]...)
			return true
		}
	}
	return false
}

// ── Maintenance window public API ─────────────────────────────────────────────

// MaintenanceWindows returns the list of currently active maintenance windows.
func (e *Engine) MaintenanceWindows() []MaintenanceWindow {
	if e.maintMgr == nil {
		return nil
	}
	return e.maintMgr.List()
}

// SetMaintenance adds a maintenance window locally and returns its ID.
// The caller is responsible for broadcasting to cluster peers via
// e.ClusterManager().BroadcastMaintenanceSet(...).
func (e *Engine) SetMaintenance(w MaintenanceWindow) error {
	if e.maintMgr == nil {
		return nil
	}
	return e.maintMgr.Set(w)
}

// CancelMaintenance removes a maintenance window by ID locally.
// The caller is responsible for broadcasting to cluster peers.
func (e *Engine) CancelMaintenance(id string) error {
	if e.maintMgr == nil {
		return nil
	}
	return e.maintMgr.Cancel(id)
}

// ApplyMaintenanceSet implements cluster.MaintenanceHandler.
// Called when a peer gossips a "set" maintenance broadcast.
func (e *Engine) ApplyMaintenanceSet(w cluster.MaintenanceWindowPayload) error {
	if e.maintMgr == nil {
		return nil
	}
	return e.maintMgr.Set(MaintenanceWindow{
		ID:        w.ID,
		TargetIDs: w.TargetIDs,
		StartedAt: w.StartedAt,
		ExpiresAt: w.ExpiresAt,
		Reason:    w.Reason,
		StartedBy: w.StartedBy,
	})
}

// ApplyMaintenanceCancel implements cluster.MaintenanceHandler.
// Called when a peer gossips a "cancel" maintenance broadcast.
func (e *Engine) ApplyMaintenanceCancel(id string) error {
	if e.maintMgr == nil {
		return nil
	}
	return e.maintMgr.Cancel(id)
}

// runMaintenancePruner periodically removes expired maintenance windows.
func (e *Engine) runMaintenancePruner(ctx context.Context) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if e.maintMgr != nil {
				e.maintMgr.PruneExpired()
			}
		}
	}
}

// LoadConfig reads the config file, resolves variables, validates, and hot-swaps.
//
// Config path resolution order:
//  1. --config flag (passed to New)
//  2. CONFIG_PATH environment variable
//  3. config.yaml in the current working directory  ← covers `go run` usage
//  4. config.yaml next to the binary               ← covers production/service usage
func (e *Engine) LoadConfig() error {
	cfgPath := e.configPath
	if cfgPath == "" {
		cfgPath = os.Getenv("CONFIG_PATH")
	}
	if cfgPath == "" {
		// Check working directory first (works with `go run`).
		if _, err := os.Stat("config.yaml"); err == nil {
			cfgPath = "config.yaml"
		}
	}
	if cfgPath == "" {
		// Fall back to the directory that contains the running binary (production).
		if exe, err := os.Executable(); err == nil {
			candidate := filepath.Join(filepath.Dir(exe), "config.yaml")
			if _, err := os.Stat(candidate); err == nil {
				cfgPath = candidate
			}
		}
	}
	if cfgPath == "" {
		cfgPath = "config.yaml" // last resort; will produce a clear "file not found" error
	}

	raw, err := os.ReadFile(cfgPath)
	if err != nil {
		return fmt.Errorf("read config %s: %w", cfgPath, err)
	}

	// Resolve relative paths against the directory that contains the config file.
	// This means log_path: "prober.log" ends up next to the config, not the binary.
	configDir := filepath.Dir(cfgPath)
	if !filepath.IsAbs(configDir) {
		if abs, err := filepath.Abs(configDir); err == nil {
			configDir = abs
		}
	}
	resolvePath := func(p string) string {
		if p == "" || filepath.IsAbs(p) {
			return p
		}
		return filepath.Join(configDir, p)
	}

	// Peek at credentials_file before full parse.
	var peek struct {
		CredentialsFile string `json:"credentials_file"`
	}
	_ = yaml.Unmarshal(raw, &peek)

	vars := make(map[string]string)
	if peek.CredentialsFile != "" {
		credPath := resolvePath(peek.CredentialsFile)
		// A missing credentials file is non-fatal: the operator may have set
		// credentials_file: ... in anticipation of future secrets but not yet
		// created the file (e.g. fresh `netwatch join` writes the path but
		// not the file). Continue with empty vars; only fail on malformed contents.
		if _, statErr := os.Stat(credPath); statErr == nil {
			vars, err = parseEnvFile(credPath)
			if err != nil {
				return err
			}
			if !e.credLogged {
				slog.Info("credentials loaded", "path", credPath, "vars", len(vars))
				e.credLogged = true
			}
		} else if !e.credLogged {
			slog.Warn("credentials file not found — continuing with empty vars",
				"path", credPath, "hint", "create the file if any ${VAR} references need resolving")
			e.credLogged = true
		}
	}

	substituted, err := resolveVars(raw, vars)
	if err != nil {
		return fmt.Errorf("config %s: %w", cfgPath, err)
	}

	// Store pre-injection bytes for config-push /sync (avoids leaking resolved
	// credentials back to disk when exporting shared config to peers).
	e.mu.Lock()
	e.rawConfigBytes = make([]byte, len(raw))
	copy(e.rawConfigBytes, raw)
	e.mu.Unlock()

	var newCfg Config
	if err := yaml.Unmarshal(substituted, &newCfg); err != nil {
		return fmt.Errorf("parse config %s: %w", cfgPath, err)
	}

	newCfg.LogPath = resolvePath(newCfg.LogPath)
	newCfg.StateFile = resolvePath(newCfg.StateFile)
	if newCfg.CredentialsFile != "" {
		newCfg.CredentialsFile = resolvePath(newCfg.CredentialsFile)
	}

	if err := validateConfig(newCfg); err != nil {
		return fmt.Errorf("config validation: %w", err)
	}
	if err := e.validateTargets(newCfg.Targets); err != nil {
		return fmt.Errorf("target validation: %w", err)
	}
	if err := validateApps(newCfg); err != nil {
		return fmt.Errorf("app validation: %w", err)
	}

	channels, err := buildAlertChannels(newCfg, e.alertRunner)
	if err != nil {
		return fmt.Errorf("notification channels: %w", err)
	}
	for _, t := range newCfg.Targets {
		if !t.active() {
			continue
		}
		for _, n := range t.Notify {
			if _, ok := channels[n]; !ok {
				return fmt.Errorf("target %q: notify %q is not defined", t.key(), n)
			}
		}
	}
	appIndex := buildAppTargetIndex(newCfg)

	// Build dependency graph (nil when no target declares depends_on).
	topo, topoErr := buildDependencyGraph(newCfg.Targets)
	if topoErr != nil {
		return fmt.Errorf("dependency graph: %w", topoErr)
	}

	// Purge stale state for removed/disabled targets.
	activeKeys := make(map[string]bool)
	activePending := make(map[string]bool)
	for _, t := range newCfg.Targets {
		if t.active() {
			activeKeys[t.key()] = true
			activePending[t.typeKey()] = true
		}
	}

	e.stateMu.Lock()
	changed := false
	for k := range e.pending {
		if !activePending[k] {
			delete(e.pending, k)
		}
	}
	for k := range e.lastKnown {
		if !activeKeys[k] {
			delete(e.lastKnown, k)
			changed = true
		}
	}
	e.stateMu.Unlock()


	if changed {
		e.persistState()
	}

	// Rebuild localProbeIDs from the new target list so HasLocalProbe stays
	// accurate after a hot-reload that adds or removes targets.
	newProbeIDs := make(map[string]bool, len(newCfg.Targets))
	for _, t := range newCfg.Targets {
		if t.active() {
			newProbeIDs[t.key()] = true
		}
	}

	e.mu.Lock()
	e.cfg = newCfg
	e.channels = channels
	e.appIndex = appIndex
	e.topoGraph = topo
	e.localProbeIDs = newProbeIDs
	if info, err := os.Stat(cfgPath); err == nil {
		e.configMtime = info.ModTime()
	}
	e.mu.Unlock()

	// P1.5: inform the cluster manager of this node's config fingerprint so it
	// can broadcast and detect drift against peers.
	if e.clusterMgr != nil {
		hash := cluster.ConfigHashOf(raw)
		e.clusterMgr.SetLocalConfigInfo(hash, int64(len(raw)), time.Now())
	}

	slog.Info("config loaded", "path", cfgPath, "targets", len(newCfg.Targets), "apps", len(newCfg.Apps))
	return nil
}

func validateConfig(c Config) error {
	if err := c.Cluster.Validate(); err != nil {
		return err
	}
	if c.MaxRetries != nil && *c.MaxRetries < 0 {
		return fmt.Errorf("max_retries must be >= 0, got %d", *c.MaxRetries)
	}
	if c.RetryIntervalSec != nil && *c.RetryIntervalSec < 5 {
		return fmt.Errorf("retry_interval_sec must be >= 5, got %d", *c.RetryIntervalSec)
	}
	if c.TickerIntervalSec != nil && *c.TickerIntervalSec < 1 {
		return fmt.Errorf("ticker_interval_sec must be >= 1, got %d", *c.TickerIntervalSec)
	}
	if c.ProbeIntervalSec != nil && *c.ProbeIntervalSec < 5 {
		return fmt.Errorf("probe_interval_sec must be >= 5, got %d", *c.ProbeIntervalSec)
	}
	return nil
}

func (e *Engine) validateTargets(targets []Target) error {
	seen := make(map[string]bool)
	for _, t := range targets {
		if !t.active() {
			continue
		}
		if t.Name == "" {
			return fmt.Errorf("target %q (type=%s): name is required", t.Target, t.Type)
		}
		if seen[t.key()] {
			return fmt.Errorf("duplicate name %q: each target must have a unique name", t.key())
		}
		seen[t.key()] = true

		checker, ok := e.checkers[t.Type]
		if !ok {
			return fmt.Errorf("target %q: unknown type %q (valid: tcp, http, ping, dns, sql)", t.key(), t.Type)
		}
		if t.Type == "http" {
			if !strings.HasPrefix(t.Target, "http://") && !strings.HasPrefix(t.Target, "https://") {
				return fmt.Errorf("target %q: http type requires a URL with http:// or https://", t.key())
			}
		}
		if err := checker.ValidateOptions(t.Options); err != nil {
			return fmt.Errorf("target %q: %w", t.key(), err)
		}
		if t.MaxRetries != nil && *t.MaxRetries < 0 {
			return fmt.Errorf("target %q: max_retries must be >= 0", t.key())
		}
		if t.RetryIntervalSec != nil && *t.RetryIntervalSec < 5 {
			return fmt.Errorf("target %q: retry_interval_sec must be >= 5", t.key())
		}
		if t.IntervalSec != nil && *t.IntervalSec < 5 {
			return fmt.Errorf("target %q: interval_sec must be >= 5", t.key())
		}
	}
	return nil
}

// Init loads config, sets up the logger, loads persisted state, and starts all loops.
// It must be called exactly once before the engine is used.
func (e *Engine) Init() error {
	if err := e.LoadConfig(); err != nil {
		return err
	}

	e.mu.RLock()
	logPath := e.cfg.LogPath
	targets := e.cfg.Targets
	e.mu.RUnlock()

	if logPath != "" {
		if err := initLogger(logPath); err != nil {
			return fmt.Errorf("logger: %w", err)
		}
	}

	e.loadPersistedState()

	// Cluster layer — started before probe loops so broadcasts are available
	// from the first state transition. Skipped when cluster.enabled=false.
	e.mu.RLock()
	clusterCfg := e.cfg.Cluster
	e.mu.RUnlock()
	if clusterCfg.Enabled {
		mgr, err := cluster.New(clusterCfg)
		if err != nil {
			return fmt.Errorf("cluster: %w", err)
		}
		e.clusterMgr = mgr

		// P1.5: LoadConfig ran before clusterMgr was set, so broadcast the
		// config fingerprint now that the manager is available.
		e.mu.RLock()
		raw := e.rawConfigBytes
		e.mu.RUnlock()
		if len(raw) > 0 {
			hash := cluster.ConfigHashOf(raw)
			e.clusterMgr.SetLocalConfigInfo(hash, int64(len(raw)), time.Now())
		}

		// Wire anti-entropy: memberlist will call LocalState/MergeRemoteState
		// during push-pull cycles (join=true) and delegate full-state exchange
		// to the engine via this interface.
		e.clusterMgr.SetStateProvider(e)
		// Wire peer-alert handler: allows this node to dispatch alerts for
		// targets it does not probe locally when it is the primary responsible
		// node (see cluster.PeerAlertHandler and OnStateReceived).
		e.clusterMgr.SetPeerAlertHandler(e)
		// Wire local-target inventory (Phase 13): lets CandidatesFor include
		// this node before the first state broadcast, and lets ProbeFrom
		// constraints flow into the candidate set filter.
		e.clusterMgr.SetLocalTargetProvider(e)
		// Wire prober assignment listener (Phase 13 step 6): the cluster
		// layer will call StartProbing / StopProbing as membership changes
		// shift prober responsibilities on and off this node.
		e.clusterMgr.SetProberAssignmentListener(e)
		// Wire soft-down notifier: when a co-prober broadcasts a soft_down
		// suspect signal, NotifyCoProberSoftDown triggers an immediate out-of-
		// schedule verification probe on this node without waiting for the ticker.
		e.clusterMgr.SetSoftDownNotifier(e)
		// Wire config-push handler: when a peer broadcasts a shared config
		// update, ApplySharedConfigJSON merges it into this node's config.yaml
		// and triggers Reload().
		e.clusterMgr.SetConfigPushHandler(e)
		// Wire maintenance handler: when a peer broadcasts a maintenance
		// set/cancel, apply it locally (RAM + disk).
		e.clusterMgr.SetMaintenanceHandler(e)
		// Wire inventory refresh handler (Phase 13): cluster calls
		// BroadcastInventory on each NotifyJoin so late-joining peers
		// receive this node's current target states.
		e.clusterMgr.SetInventoryRefreshHandler(e)
		// Announce presence for every locally configured target so peers can
		// populate their candidate sets immediately. Cheap one-shot — see
		// bootstrapInventoryBroadcast.
		e.bootstrapInventoryBroadcast()
	}

	// Build the local probe ID set so HasLocalProbe can answer O(1).
	e.mu.Lock()
	ids := make(map[string]bool, len(targets))
	for _, t := range targets {
		if t.active() {
			ids[t.key()] = true
		}
	}
	e.localProbeIDs = ids
	e.mu.Unlock()

	// Background goroutines — all share a single root context.
	rootCtx, rootCancel := context.WithCancel(context.Background())
	e.retryStop = rootCancel

	// Retry loop: re-probes soft-down targets and escalates to hard-down.
	go e.runRetryLoop(rootCtx)

	// Reload watcher: checks config file mtime and hot-swaps when changed.
	// Disabled when reload_interval_sec: 0 is set explicitly in config.
	e.mu.RLock()
	reloadDisabled := e.cfg.ReloadIntervalSec != nil && *e.cfg.ReloadIntervalSec == 0
	e.mu.RUnlock()
	if !reloadDisabled {
		go e.runReloadWatcher(rootCtx)
	}

	// Watchdog: monitors whether Prometheus is scraping on schedule.
	// Disabled when watchdog_threshold_sec is 0 (the default).
	go e.runWatchdog(rootCtx)

	// Cluster metrics updater: pushes quorum/isolated/size gauges every 5s.
	// Only started when cluster is enabled.
	if e.clusterMgr != nil {
		go e.runClusterMetricsUpdater(rootCtx)
	}

	// Maintenance window manager: loads maintenance.json from disk and starts
	// a background pruner. Always initialized so API endpoints work regardless
	// of cluster mode.
	e.mu.RLock()
	sloCfg := e.cfg.SLO
	stateFilePath := e.cfg.StateFile
	e.mu.RUnlock()
	e.maintMgr = newMaintenanceManager(stateFilePath)
	go e.runMaintenancePruner(rootCtx)

	// SLO tracker: persists incident history, checks breaches hourly.
	// Disabled when slo.enabled is false (the default).
	if sloCfg != nil && sloCfg.Enabled {
		e.sloMgr = newSLOManager(stateFilePath)
		go e.runSLOChecker(rootCtx)
	}

	// Phase 13: pre-seed the cluster's proberAssignments map so the first
	// reactive recompute does not see "all assignments new" and needlessly
	// cancel-and-restart every probe loop we are about to launch below.
	if e.clusterMgr != nil {
		initial := make(map[string]bool, len(targets))
		for _, t := range targets {
			if t.active() {
				initial[t.key()] = e.clusterMgr.IsLocalProber(t.key())
			}
		}
		e.clusterMgr.SeedProberAssignments(initial)
	}

	// Start a probe goroutine for each active target. startProbeLoop is itself
	// Phase 13-aware: when the cluster has not selected this node as a prober
	// for the target it returns early, so only the assigned subset actually
	// runs.
	for _, t := range targets {
		if t.active() {
			e.startProbeLoop(t)
		}
	}

	slog.Info("agent started", "host", e.hostname, "app", e.NodeAlias(), "targets", len(targets))
	return nil
}

// ── Cluster metrics ───────────────────────────────────────────────────────────

// updateClusterMetrics snapshots the cluster manager's state and pushes it
// to the cluster Prometheus gauges. Called every 5 s by the updater goroutine;
// safe to call when clusterMgr is non-nil.
func (e *Engine) updateClusterMetrics() {
	if e.clusterMgr == nil {
		return
	}

	// Quorum / isolation / size gauges.
	GaugeClusterSize.Set(float64(e.clusterMgr.AliveCount()))
	if e.clusterMgr.IsolatedMode() {
		GaugeQuorumHealthy.Set(0)
		GaugeIsolated.Set(1)
	} else {
		GaugeQuorumHealthy.Set(1)
		GaugeIsolated.Set(0)
	}

	// Per-target cluster consensus status.
	e.mu.RLock()
	targets := e.cfg.Targets
	appName := e.cfg.NodeAlias
	e.mu.RUnlock()

	for _, t := range targets {
		if !t.active() {
			continue
		}
		labels := prometheus.Labels{
			"name":        t.key(),
			"target":      t.Target,
			"type":        t.Type,
			"source_host": e.hostname,
			"app_name":    appName,
		}

		// Local state for this target.
		e.stateMu.RLock()
		ps := e.lastKnown[t.key()]
		e.stateMu.RUnlock()
		localUp := ps.State == "up"

		// Peer states — any peer reporting down → consensus is down.
		allUp := localUp
		for _, p := range e.clusterMgr.PeerStatesForTarget(t.key()) {
			if p.State != "up" {
				allUp = false
				break
			}
		}

		if allUp {
			GaugeClusterStatus.With(labels).Set(1)
		} else {
			GaugeClusterStatus.With(labels).Set(0)
		}

		// Phase 13 ownership gauges (label set is smaller — no host/app
		// because these are about cluster assignment, not probe results).
		ownerLabels := prometheus.Labels{
			"name":   t.key(),
			"target": t.Target,
			"type":   t.Type,
		}
		probers := e.clusterMgr.SelectProbers(t.key())
		GaugeProberCount.With(ownerLabels).Set(float64(len(probers)))
		if e.clusterMgr.IsLocalProber(t.key()) {
			GaugeLocalAssigned.With(ownerLabels).Set(1)
		} else {
			GaugeLocalAssigned.With(ownerLabels).Set(0)
		}

		// Underreplicated: assigned probers fewer than factor but not zero.
		factor := e.clusterMgr.ReplicationFactor()
		if len(probers) > 0 && len(probers) < factor {
			GaugeProberUnderreplicated.With(ownerLabels).Set(1)
		} else {
			GaugeProberUnderreplicated.With(ownerLabels).Set(0)
		}
	}

	// Inventory-peer count: distinct peer names with at least one broadcast
	// in peerStates. Uses Snapshot().PeerStates which is already deep-copied.
	GaugeInventoryPeers.Set(float64(len(e.clusterMgr.Snapshot().PeerStates)))

	// Orphan detection: targets whose candidate set / SelectProbers is empty.
	// Updates per-target gauge, then logs edge-triggered transitions so
	// operators are alerted exactly once per state change instead of every 5 s.
	e.refreshOrphanState(targets)

	// P1.5: refresh config-drift metric.
	e.clusterMgr.UpdateConfigDriftMetric()

	// P1.6: build targetInfos map (targetID → {displayName, targetAddr, probeType})
	// for geo-latency metric labels, then delegate to the cluster manager.
	targetInfos := make(map[string][3]string, len(targets))
	for _, t := range targets {
		if t.active() {
			targetInfos[t.key()] = [3]string{t.key(), t.Target, t.Type}
		}
	}
	e.clusterMgr.UpdateGeoMetrics(targetInfos)
}

// refreshOrphanState updates the per-target orphaned gauge and emits
// edge-triggered log lines for each transition. An orphaned target is one
// the cluster has no eligible prober for — typically a `probe_from` or
// `probe_from_regions` constraint that filtered to an empty candidate set.
//
// Called only when the cluster manager is non-nil; the gauge is registered
// by RegisterClusterMetrics.
func (e *Engine) refreshOrphanState(targets []Target) {
	orphans := make(map[string]bool)
	for _, id := range e.clusterMgr.OrphanedLocalTargets() {
		orphans[id] = true
	}

	for _, t := range targets {
		if !t.active() {
			continue
		}
		labels := prometheus.Labels{
			"name":   t.key(),
			"target": t.Target,
			"type":   t.Type,
		}
		if orphans[t.key()] {
			GaugeTargetOrphaned.With(labels).Set(1)
		} else {
			GaugeTargetOrphaned.With(labels).Set(0)
		}
	}

	// Edge-triggered logging — compare against previous set, log only diffs.
	e.orphanedMu.Lock()
	prev := e.orphanedSet
	if prev == nil {
		prev = map[string]bool{}
	}
	for id := range orphans {
		if !prev[id] {
			slog.Warn("[CLUSTER] target orphaned — no eligible probers",
				"target", id,
				"hint", "check probe_from / probe_from_regions / cluster.zone")
		}
	}
	for id := range prev {
		if !orphans[id] {
			slog.Info("[CLUSTER] target re-assigned — prober available again",
				"target", id,
				"probers", e.clusterMgr.SelectProbers(id))
		}
	}
	e.orphanedSet = orphans
	e.orphanedMu.Unlock()
}

// GeoLatencySnapshot returns the cluster-wide per-node latency view for
// targetID. Returns a zero-value snapshot when cluster is not enabled.
func (e *Engine) GeoLatencySnapshot(targetID string) cluster.GeoLatencySnapshot {
	if e.clusterMgr == nil {
		return cluster.GeoLatencySnapshot{TargetID: targetID}
	}
	return e.clusterMgr.GeoLatencyForTarget(targetID)
}

// computeScope determines the alert scope based on how many cluster nodes see
// this target as down.
//
//   - GLOBAL    — every node reporting on this target agrees it is down
//   - NODE_LOCAL — only this node is down, peers see it as up
//   - PARTIAL   — mixed observations
//   - STANDALONE — no cluster configured
func (e *Engine) computeScope(targetID string, localDown bool) string {
	if e.clusterMgr == nil {
		return "STANDALONE"
	}
	peerStates := e.clusterMgr.PeerStatesForTarget(targetID)
	if len(peerStates) == 0 {
		// No peer data yet (cluster just joined, or single-node cluster).
		if localDown {
			return "NODE_LOCAL"
		}
		return "STANDALONE"
	}

	downCount, upCount := 0, 0
	if localDown {
		downCount++
	} else {
		upCount++
	}
	for _, p := range peerStates {
		switch p.State {
		case "hard_down":
			downCount++
		case "up":
			upCount++
		}
	}
	switch {
	case downCount > 0 && upCount == 0:
		return "GLOBAL"
	case downCount == 1 && upCount > 0 && localDown:
		return "NODE_LOCAL"
	default:
		return "PARTIAL"
	}
}

// shouldAlert returns true when this node should actually dispatch an alert for
// the given target.
//
//   - Standalone (no cluster): always true
//   - Isolated mode: always false — suppress until quorum recovers
//   - Cluster + quorum: only the responsible node (primary or secondary) alerts
//   - min_probe_confirmations > 1: requires N probers to agree on hard_down
func (e *Engine) shouldAlert(targetID string) bool {
	// Maintenance window check — takes priority over everything else.
	// Probes continue to run; only alert dispatch is suppressed.
	if e.maintMgr != nil && e.maintMgr.IsInMaintenance(targetID) {
		slog.Debug("alert suppressed: target in maintenance", "target", targetID)
		return false
	}

	if e.clusterMgr == nil {
		return true
	}
	if e.clusterMgr.IsolatedMode() {
		slog.Debug("alert suppressed: isolated mode", "target", targetID)
		return false
	}
	if !e.clusterMgr.IsResponsible(targetID) {
		slog.Debug("alert suppressed: not responsible", "target", targetID)
		return false
	}
	// min_probe_confirmations guard: wait until enough independent probers agree.
	//
	// Design intent: this prevents a single node with a flaky network path from
	// alerting when all other probers see the target as healthy. It applies ONLY
	// when this node is NOT itself a designated prober for the target.
	//
	// Why exempt local probers? If this node is both primary AND a prober, it has
	// direct first-hand evidence of the failure — it probed the target itself and
	// got a connection error. This is qualitatively different from a non-prober
	// primary that is relying entirely on gossip from others. A prober-primary
	// should fire immediately; the confirmation guard adds no safety value here
	// and only introduces a window where the alert is silently suppressed even
	// though we have direct proof of the outage.
	minConf := e.effectiveMinConfirmations()
	if minConf > 1 && !e.clusterMgr.IsLocalProber(targetID) {
		// This node is the responsible primary but NOT a prober — it relies on
		// peer gossip alone. Require minConf independent confirmations.
		confirmCount := 0
		for _, peer := range e.clusterMgr.PeerStatesForTarget(targetID) {
			if peer.State == "hard_down" {
				confirmCount++
			}
		}
		if confirmCount < minConf {
			slog.Debug("alert suppressed: insufficient peer confirmations (non-prober primary)",
				"target", targetID, "confirmations", confirmCount, "required", minConf)
			return false
		}
	}
	return true
}

// effectiveMinConfirmations returns the configured min_probe_confirmations for
// the cluster, defaulting to 1 (no multi-confirmation requirement).
func (e *Engine) effectiveMinConfirmations() int {
	if e.clusterMgr == nil {
		return 1
	}
	if v := e.clusterMgr.MinProbeConfirmations(); v > 1 {
		return v
	}
	return 1
}

// NotifyCoProberSoftDown implements cluster.SoftDownNotifier. Called by the
// cluster layer when a co-prober node broadcasts a soft_down suspect signal for
// targetID. Sends a non-blocking signal to the probe loop's fast-check channel
// so the loop fires an immediate out-of-schedule verification probe without
// waiting for the next ticker tick.
func (e *Engine) NotifyCoProberSoftDown(targetID string) {
	e.probesMu.Lock()
	ch, ok := e.probeFastCheck[targetID]
	e.probesMu.Unlock()
	if !ok {
		return
	}
	select {
	case ch <- struct{}{}:
	default: // already signaled; the probe loop will pick it up
	}
}

// ── LocalTargetProvider (cluster.LocalTargetProvider) ────────────────────────

// StartProbing implements cluster.ProberAssignmentListener. Called by the
// cluster layer after a recompute determines this node should now probe
// targetID. Looks up the target in the current config and runs startProbeLoop;
// disabled or removed targets are silently ignored (the listener may briefly
// reference a target that has just been removed by a concurrent Reload).
//
// Suppressed during anti-entropy sync (Step 9): probe-loop changes mid-sync
// could race with the FullState merge. The engine fires a fresh recompute
// when SetSyncing(false) clears the flag, so any missed assignment catches
// up on the next pass.
func (e *Engine) StartProbing(targetID string) {
	if e.syncing.Load() {
		slog.Debug("start probing deferred: anti-entropy sync in progress",
			"target", targetID)
		return
	}
	e.mu.RLock()
	var found *Target
	for i := range e.cfg.Targets {
		if e.cfg.Targets[i].key() == targetID && e.cfg.Targets[i].active() {
			t := e.cfg.Targets[i]
			found = &t
			break
		}
	}
	e.mu.RUnlock()
	if found == nil {
		return
	}
	slog.Info("probe loop started by cluster assignment",
		"target", found.key(), "node", e.hostname)
	e.startProbeLoop(*found)
}

// StopProbing implements cluster.ProberAssignmentListener. Called by the
// cluster layer when this node is no longer in the prober subset for
// targetID. Cancels the probe goroutine if one is running; harmless when
// none exists.
//
// Sync guard: same rationale as StartProbing — we don't tear down probe
// loops while a full-state merge is reconciling everything; the post-sync
// recompute trigger re-applies the correct assignment.
func (e *Engine) StopProbing(targetID string) {
	if e.syncing.Load() {
		slog.Debug("stop probing deferred: anti-entropy sync in progress",
			"target", targetID)
		return
	}
	slog.Info("probe loop stopped by cluster assignment",
		"target", targetID, "node", e.hostname)
	e.stopProbeLoop(targetID)
}

// LocalTargets implements cluster.LocalTargetProvider. Returns the set of
// target keys currently configured on this node — used by the cluster layer's
// CandidatesFor to recognise this node as a candidate before it has broadcast
// any state. Includes both active and disabled targets so prober assignment
// stays stable when an operator toggles `enabled: false` temporarily; the
// probe loop itself separately gates on Target.active().
func (e *Engine) LocalTargets() []string {
	e.mu.RLock()
	defer e.mu.RUnlock()
	out := make([]string, 0, len(e.cfg.Targets))
	for _, t := range e.cfg.Targets {
		out = append(out, t.key())
	}
	return out
}

// ProbeFromConstraint implements cluster.LocalTargetProvider. Returns the
// `probe_from` list configured on the target locally, or nil when the target
// is absent from this node's config (no constraint to contribute) or the list
// is empty (operator opted out of pinning).
//
// Lookup is O(N) over local targets. CandidatesFor calls this at most once per
// recompute so the cost is negligible at typical target counts.
func (e *Engine) ProbeFromConstraint(targetID string) []string {
	e.mu.RLock()
	defer e.mu.RUnlock()
	for _, t := range e.cfg.Targets {
		if t.key() != targetID {
			continue
		}
		if len(t.ProbeFrom) == 0 {
			return nil
		}
		out := make([]string, len(t.ProbeFrom))
		copy(out, t.ProbeFrom)
		return out
	}
	return nil
}

// ProbeFromRegionsConstraint implements cluster.LocalTargetProvider. Returns
// the `probe_from_regions` list configured on the target locally (P1.6).
// An empty / nil return means "no regional constraint".
func (e *Engine) ProbeFromRegionsConstraint(targetID string) []string {
	e.mu.RLock()
	defer e.mu.RUnlock()
	for _, t := range e.cfg.Targets {
		if t.key() != targetID {
			continue
		}
		if len(t.ProbeFromRegions) == 0 {
			return nil
		}
		out := make([]string, len(t.ProbeFromRegions))
		copy(out, t.ProbeFromRegions)
		return out
	}
	return nil
}

// bootstrapInventoryBroadcast emits one cluster broadcast per local target so
// peers can populate their candidate sets immediately, without waiting for the
// first probe cycle. Closes the chicken-and-egg of Phase 13: a node that is
// not selected as a prober for its own targets still needs to be visible to
// peers; otherwise it could be missed during future prober recomputations.
//
// Payload content:
//   - When state.json carries the target, broadcast the persisted state and
//     seq — peers receive an authoritative re-assertion, helpful when this
//     node has the freshest view after a restart.
//   - When the target is new (not yet in lastKnown), broadcast state="unknown"
//     with seq=0. computeScope explicitly ignores anything other than "up" /
//     "hard_down", so this is a benign presence announcement that will be
//     superseded by the first real probe (seq>=1) via Lamport ordering.
//
// No-op when the cluster layer is disabled or while an anti-entropy sync
// is in flight (Step 9): broadcasting bootstrap states during a FullState
// merge would race with ApplyRemoteState and risk overwriting authoritative
// seqs with seq=0 placeholders.
func (e *Engine) bootstrapInventoryBroadcast() {
	if e.clusterMgr == nil {
		return
	}
	if e.syncing.Load() {
		slog.Debug("bootstrap broadcast deferred: anti-entropy sync in progress")
		return
	}
	e.mu.RLock()
	targets := append([]Target(nil), e.cfg.Targets...)
	e.mu.RUnlock()

	e.stateMu.RLock()
	defer e.stateMu.RUnlock()
	for _, t := range targets {
		if !t.active() {
			continue
		}
		ps, ok := e.lastKnown[t.key()]
		if !ok {
			// First-time target — seq=0 so any later real broadcast wins.
			ps = PersistedState{State: "unknown"}
		}
		e.broadcastState(t, ps)
	}
}

// BroadcastInventory implements cluster.InventoryRefreshHandler.
// Called by the cluster layer on each NotifyJoin so that late-joining peers
// receive this node's current target states. Equivalent to
// bootstrapInventoryBroadcast but callable via the interface.
func (e *Engine) BroadcastInventory() {
	e.bootstrapInventoryBroadcast()
}

// ── PeerAlertHandler (cluster.PeerAlertHandler) ──────────────────────────────

// HasLocalProbe implements cluster.PeerAlertHandler.
// Returns true when this engine instance has a probe goroutine running for
// targetID. The cluster layer calls this to avoid double-alerting: if the
// primary probes the target locally, the normal probe→processPending path
// handles alerting and the peer-alert path is skipped.
func (e *Engine) HasLocalProbe(targetID string) bool {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.localProbeIDs[targetID]
}

// DispatchPeerAlert implements cluster.PeerAlertHandler.
// Called by the cluster layer when this node is the primary responsible for
// a target that it does not probe locally, and a peer has reported that
// target as hard_down. Constructs an alert environment from the gossip
// payload and dispatches it through the configured notification channels.
//
// Since the target is not in our config, we use the payload's TargetName/
// TargetType (populated by the probing node) and fall back to the target ID
// when those fields are absent (older payload format).
func (e *Engine) DispatchPeerAlert(p cluster.GossipPayload) {
	// NOTE: syncing guard intentionally omitted here. DispatchPeerAlert is called
	// when a peer gossips a hard_down state to the responsible primary node.
	// Unlike probe-based alerts (runCheck / processPending), this path originates
	// from an already-confirmed peer state, NOT from a local probe that could be
	// racing with anti-entropy state merges. Suppressing it during sync would cause
	// alerts to be lost entirely when the primary restarts and re-joins while a
	// target is already down. The peerAlerted dedup map (in cluster/cluster.go)
	// prevents duplicate dispatches even without the syncing guard.

	name := p.TargetName
	if name == "" {
		name = p.TargetID
	}
	targetType := p.TargetType
	if targetType == "" {
		targetType = "unknown"
	}

	host, port, _ := strings.Cut(p.TargetID, ":")

	e.mu.RLock()
	appName := e.cfg.NodeAlias
	defaultNotify := e.cfg.DefaultNotify
	// Best-effort app enrichment: the target may be referenced in our local
	// apps index even if we don't probe it directly.
	apps := e.appIndex[p.TargetID]
	e.mu.RUnlock()

	channels := mergeNotifyChannels(nil, apps)
	if len(channels) == 0 {
		channels = defaultNotify
	}
	if len(channels) == 0 {
		slog.Debug("peer-alert suppressed: no channels configured", "target", p.TargetID)
		return
	}

	scope := e.computeScope(p.TargetID, false /* no local probe state */)

	env := map[string]string{
		"NAME":       name,
		"TARGET":     p.TargetID,
		"HOST":       host,
		"PORT":       port,
		"STATUS":     "unreachable",
		"TYPE":       targetType,
		"SEQ":        strconv.FormatUint(p.Seq, 10),
		"ERROR_CODE": p.ErrorCode,
		"NODE_NAME":  p.NodeName, // node that actually detected the failure
		"APP_NAME":   appName,
		"SCOPE":      scope,
	}
	if affected, teams := buildAppContext(apps); affected != "" {
		env["AFFECTED_APPS"] = affected
		env["OWNER_TEAMS"] = teams
	}

	slog.Info("sending peer-alert (no local probe)",
		"target", name, "status", "unreachable",
		"from_node", p.NodeName, "channels", channels, "scope", scope)

	e.mu.RLock()
	engineChannels := e.channels
	e.mu.RUnlock()

	for _, chName := range channels {
		ch, ok := engineChannels[chName]
		if !ok {
			slog.Warn("peer-alert: channel not found", "channel", chName, "target", name)
			continue
		}
		go func(n string, c Alerter) {
			if err := c.Send(env); err != nil {
				slog.Error("peer-alert send failed", "channel", n, "target", name, "err", err)
			}
		}(chName, ch)
	}
}

// ── Anti-entropy (cluster.AntiEntropyProvider) ───────────────────────────────

// FullState implements cluster.AntiEntropyProvider.
// It serialises the engine's complete lastKnown map so a remote peer can
// reconcile its view during a memberlist push-pull re-join cycle.
func (e *Engine) FullState() []byte {
	e.stateMu.RLock()
	snap := make(map[string]PersistedState, len(e.lastKnown))
	for k, v := range e.lastKnown {
		snap[k] = v
	}
	e.stateMu.RUnlock()
	data, _ := json.Marshal(snap)
	return data
}

// ApplyRemoteState implements cluster.AntiEntropyProvider.
// It merges a remote full-state snapshot received during a push-pull re-join.
//
// Decision rules per target (Lamport causal ordering):
//
//   - Remote Seq > local Seq → accept remote silently (cluster already alerted)
//   - Remote Seq == local Seq, remote OwnerNode > local OwnerNode → accept (tie-break)
//   - Local Seq ≥ remote in all other cases → keep local; broadcast so the
//     newcomer learns our authoritative state
//
// State changes are persisted to state.json after the merge.
func (e *Engine) ApplyRemoteState(buf []byte) {
	var remote map[string]PersistedState
	if err := json.Unmarshal(buf, &remote); err != nil {
		slog.Warn("anti-entropy: malformed remote state", "err", err)
		return
	}

	type bcastItem struct {
		id string
		ps PersistedState
	}
	var broadcasts []bcastItem
	updated := false

	e.stateMu.Lock()
	for targetID, remotePS := range remote {
		localPS, exists := e.lastKnown[targetID]
		switch {
		case !exists:
			// No local record — accept remote state without alarming.
			e.lastKnown[targetID] = remotePS
			updated = true
		case remotePS.Seq > localPS.Seq:
			// Remote is more recent — accept without alarming (cluster already decided).
			e.lastKnown[targetID] = remotePS
			updated = true
		case remotePS.Seq == localPS.Seq && remotePS.OwnerNode > localPS.OwnerNode:
			// Same sequence number, higher OwnerNode wins the Lamport tie-break.
			e.lastKnown[targetID] = remotePS
			updated = true
		default:
			// Local state is authoritative — broadcast it so the newcomer syncs.
			broadcasts = append(broadcasts, bcastItem{targetID, localPS})
		}
	}
	e.stateMu.Unlock()

	if updated {
		e.persistState()
	}
	for _, b := range broadcasts {
		e.broadcastStateByID(b.id, b.ps)
	}
	slog.Info("[ANTI-ENTROPY] state merged",
		"remote_targets", len(remote),
		"accepted", len(remote)-len(broadcasts),
		"broadcast_back", len(broadcasts),
	)
}

// SetSyncing implements cluster.AntiEntropyProvider.
// While syncing is true, runCheck and processPending return immediately so no
// new alarms are dispatched during state reconciliation. Phase 13 extends
// this guard to probe-loop assignment: StartProbing / StopProbing callbacks
// also defer while syncing.
//
// On the sync-complete transition (true → false), a fresh prober recompute is
// triggered so any assignment changes that arrived via the merged peer state
// are applied. Without this trigger, deferred StartProbing/StopProbing calls
// would only catch up on the next debounced recompute (up to 5 s later).
func (e *Engine) SetSyncing(v bool) {
	prev := e.syncing.Swap(v)
	if v {
		slog.Info("[ANTI-ENTROPY] sync started — alarm dispatch suppressed")
		return
	}
	slog.Info("[ANTI-ENTROPY] sync complete — normal operation resumed")
	// Transitioned from syncing→not-syncing: catch up on any prober changes
	// we deferred during the merge.
	if prev && e.clusterMgr != nil {
		go e.clusterMgr.TriggerProberRecompute()
	}
}

// runClusterMetricsUpdater calls updateClusterMetrics every 5 s.
// Goroutine exits when ctx is cancelled (engine Shutdown).
func (e *Engine) runClusterMetricsUpdater(ctx context.Context) {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			e.updateClusterMetrics()
		}
	}
}

// ── Reload watcher ────────────────────────────────────────────────────────────

// runReloadWatcher checks the config file's modification time every reload_interval_sec
// seconds and calls Reload() when the file has changed.
// This replaces the old behavior of reloading on every /metrics scrape.
func (e *Engine) runReloadWatcher(ctx context.Context) {
	e.mu.RLock()
	intervalSec := e.cfg.globalReloadInterval()
	cfgPath := e.configPath
	e.mu.RUnlock()

	// Resolve configPath the same way LoadConfig does.
	if cfgPath == "" {
		cfgPath = os.Getenv("CONFIG_PATH")
	}
	if cfgPath == "" {
		if _, err := os.Stat("config.yaml"); err == nil {
			cfgPath = "config.yaml"
		}
	}

	ticker := time.NewTicker(time.Duration(intervalSec) * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if cfgPath == "" {
				continue
			}
			info, err := os.Stat(cfgPath)
			if err != nil {
				continue
			}
			e.mu.RLock()
			lastMtime := e.configMtime
			e.mu.RUnlock()
			if info.ModTime().After(lastMtime) {
				e.Reload()
			}
		}
	}
}
