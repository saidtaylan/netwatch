package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"math"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/saidtaylan/netwatch/internal/storage"
	gossipstore "github.com/saidtaylan/netwatch/internal/storage/gossip"
)

// ── SLO Config ────────────────────────────────────────────────────────────────

// SLOConfig is the top-level slo: section in config.yaml.
//
// Example:
//
//	slo:
//	  enabled: true
//	  retention_days: 90
//	  slo_notify: ["ops-channel"]
//	  targets:
//	    - id: "db-primary"
//	      target_uptime: 0.999   # 99.9%
//	      window: "30d"
type SLOConfig struct {
	// Enabled must be true to activate incident tracking and /slo endpoint.
	Enabled bool `json:"enabled"`

	// RetentionDays controls how long historical incident records are kept.
	// Default: 90.
	RetentionDays int `json:"retention_days,omitempty"`

	// SLONotify is the list of notification channels to use for SLO breach
	// alerts. If empty, cfg.DefaultNotify is used.
	SLONotify []string `json:"slo_notify,omitempty"`

	// Targets specifies per-target SLO objectives.
	Targets []SLOTarget `json:"targets,omitempty"`
}

// SLOTarget defines the SLO parameters for one monitored target.
type SLOTarget struct {
	// ID must match a target id or name declared in the targets: list.
	ID string `json:"id"`

	// TargetUptime is the desired uptime ratio in [0, 1] (e.g. 0.999 = 99.9%).
	TargetUptime float64 `json:"target_uptime"`

	// Window is the rolling measurement period (e.g. "30d", "7d").
	// Supported units: d (days), h (hours).
	Window string `json:"window"`
}

func (s *SLOConfig) retentionDays() int {
	if s != nil && s.RetentionDays > 0 {
		return s.RetentionDays
	}
	return 90
}

// ── SLO Prometheus metrics ────────────────────────────────────────────────────

var (
	// GaugeSLOUptimeRatio reports the actual uptime ratio within the window.
	// Labels: target_id, window (e.g. "30d").
	GaugeSLOUptimeRatio = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "network_probe_slo_uptime_ratio",
		Help: "Actual uptime ratio for the SLO window (0.0–1.0). 1.0 = no downtime.",
	}, []string{"target_id", "window"})

	// GaugeSLOErrorBudgetSeconds reports the remaining error budget in seconds.
	// Negative values mean the budget has been exhausted (SLO breached).
	// Labels: target_id, window.
	GaugeSLOErrorBudgetSeconds = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "network_probe_slo_error_budget_seconds",
		Help: "Remaining error budget for the SLO window in seconds (negative = breached).",
	}, []string{"target_id", "window"})

	// GaugeSLOBreached is 1 when the target's SLO is currently breached, 0 otherwise.
	// Labels: target_id.
	GaugeSLOBreached = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "network_probe_slo_breached",
		Help: "1 = SLO is currently breached for this target, 0 = within budget.",
	}, []string{"target_id"})
)

// RegisterSLOMetrics registers the SLO Prometheus metrics with reg.
// Must be called only when slo.enabled=true.
func RegisterSLOMetrics(reg *prometheus.Registry) {
	reg.MustRegister(GaugeSLOUptimeRatio, GaugeSLOErrorBudgetSeconds, GaugeSLOBreached)
}

// ── Incident model ────────────────────────────────────────────────────────────

// IncidentRecord is a single downtime event for one target.
// Active incidents have EndedAt = nil.
type IncidentRecord struct {
	TargetID    string     `json:"target_id"`
	StartedAt   time.Time  `json:"started_at"`
	EndedAt     *time.Time `json:"ended_at,omitempty"`
	DurationSec int64      `json:"duration_sec,omitempty"` // set when EndedAt != nil
	Scope       string     `json:"scope,omitempty"`
	ErrorCode   string     `json:"error_code,omitempty"`
}

// ── sloManager ────────────────────────────────────────────────────────────────

// sloManager persists incident history and SLO target definitions, and
// answers SLO queries. All exported methods are safe for concurrent use.
//
// Storage model (B24):
//   - **Incidents** live in storage.TableSLOIncidents, written via
//     gossip.Storage.Inner() (local-only, NOT gossip-replicated). Each
//     node's incident list reflects its own observation — aggregating
//     across the cluster would inflate downtime counts in a 3-node
//     replication factor scenario (3× the same incident).
//   - **SLO targets** (cfg) live in storage.TableSLOTargets, written via
//     gossip.Storage.Upsert (cluster-replicated). UI CRUD updates are
//     visible on all nodes within seconds.
//
// In-memory state (runtime, not persisted):
//   - openStart: tracks which targets currently have an unresolved
//     incident — drives RecordEnd's "close the open incident" logic.
//   - breachAlerted: tracks which targets have an active breach alert
//     so we send only once per breach period (edge-triggered).
type sloManager struct {
	mu sync.Mutex

	// storage backend (gossip-wrapped). incidents use Inner() (no replication);
	// targets use the wrapped Upsert (cluster broadcast).
	storage *gossipstore.Storage
	nodeName string

	// In-memory caches, kept in sync with the SQLite tables.
	incidents []IncidentRecord     // append+update only, local
	targets   map[string]SLOTarget // id → target, cluster-replicated cache

	openStart     map[string]time.Time // targetID → start of the currently open incident
	breachAlerted map[string]bool      // targetID → true once a breach alert has been sent

	// watchCancel stops the targets-table Watch goroutine on Close().
	watchCancel context.CancelFunc
}

// newSLOManager constructs a storage-backed SLO manager. The constructor
// loads existing incidents (local) and targets (cluster) into the in-memory
// caches and starts a Watch goroutine for the targets table so peer SLO
// changes propagate to this node.
//
// Returns an error only when the initial storage List fails.
func newSLOManager(parent context.Context, gs *gossipstore.Storage, nodeName string, seedTargets []SLOTarget) (*sloManager, error) {
	if gs == nil {
		return nil, fmt.Errorf("slo: nil storage")
	}
	ctx, cancel := context.WithCancel(parent)
	m := &sloManager{
		storage:       gs,
		nodeName:      nodeName,
		incidents:     nil,
		targets:       make(map[string]SLOTarget),
		openStart:     make(map[string]time.Time),
		breachAlerted: make(map[string]bool),
		watchCancel:   cancel,
	}
	if err := m.loadIncidents(ctx); err != nil {
		cancel()
		return nil, fmt.Errorf("slo: load incidents: %w", err)
	}
	if err := m.loadTargets(ctx); err != nil {
		cancel()
		return nil, fmt.Errorf("slo: load targets: %w", err)
	}
	// One-time bootstrap: if the targets table is empty AND config.yaml has
	// SLO targets, seed them. This eases first-boot UX — operators don't
	// have to do anything special; their existing slo.targets work.
	if len(m.targets) == 0 && len(seedTargets) > 0 {
		slog.Info("slo: seeding targets table from config.yaml", "count", len(seedTargets))
		for _, st := range seedTargets {
			if err := m.UpsertTarget(st); err != nil {
				slog.Warn("slo: seed upsert failed", "id", st.ID, "err", err)
			}
		}
	}
	go m.watchTargetsLoop(ctx)
	return m, nil
}

// Close stops the Watch goroutine. Safe to call multiple times.
func (m *sloManager) Close() {
	if m.watchCancel != nil {
		m.watchCancel()
	}
}

// ── Targets (cluster-replicated) ──────────────────────────────────────────

// Targets returns the current SLO target list. Snapshot — caller may
// modify the returned slice without affecting the manager.
func (m *sloManager) Targets() []SLOTarget {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]SLOTarget, 0, len(m.targets))
	for _, t := range m.targets {
		out = append(out, t)
	}
	return out
}

// GetTarget returns the SLO target by ID, or (zero, false).
func (m *sloManager) GetTarget(id string) (SLOTarget, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	t, ok := m.targets[id]
	return t, ok
}

// UpsertTarget adds or updates an SLO target. Writes to storage (which
// broadcasts to peers via gossip) and updates the local cache.
func (m *sloManager) UpsertTarget(st SLOTarget) error {
	if st.ID == "" {
		return fmt.Errorf("slo: empty target id")
	}
	payload, err := json.Marshal(st)
	if err != nil {
		return fmt.Errorf("slo: marshal target: %w", err)
	}
	ver := m.storage.NextVersion()
	if err := m.storage.Upsert(context.Background(),
		storage.TableSLOTargets, st.ID, payload, ver); err != nil {
		return fmt.Errorf("slo: storage upsert: %w", err)
	}
	m.mu.Lock()
	m.targets[st.ID] = st
	m.mu.Unlock()
	return nil
}

// DeleteTarget removes an SLO target. Tombstone is gossip-replicated.
// Returns false when the target did not exist (idempotent).
func (m *sloManager) DeleteTarget(id string) (bool, error) {
	m.mu.Lock()
	_, exists := m.targets[id]
	m.mu.Unlock()
	if !exists {
		return false, nil
	}
	ver := m.storage.NextVersion()
	if err := m.storage.Delete(context.Background(),
		storage.TableSLOTargets, id, ver); err != nil {
		return false, fmt.Errorf("slo: storage delete: %w", err)
	}
	m.mu.Lock()
	delete(m.targets, id)
	delete(m.breachAlerted, id) // forget breach flag too
	m.mu.Unlock()
	return true, nil
}

// loadTargets populates m.targets from the storage backend.
func (m *sloManager) loadTargets(ctx context.Context) error {
	recs, err := m.storage.List(ctx, storage.TableSLOTargets, storage.Filter{})
	if err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, rec := range recs {
		if rec.Tombstone {
			continue
		}
		var st SLOTarget
		if err := json.Unmarshal(rec.Payload, &st); err != nil {
			slog.Warn("slo: malformed target in storage", "id", rec.ID, "err", err)
			continue
		}
		m.targets[st.ID] = st
	}
	if n := len(m.targets); n > 0 {
		slog.Info("slo: targets loaded from storage", "count", n)
	}
	return nil
}

// watchTargetsLoop receives change events for the slo_targets table and
// applies them to the local cache. Peers' UpsertTarget / DeleteTarget
// arrive here via the gossip storage layer.
func (m *sloManager) watchTargetsLoop(ctx context.Context) {
	ch, err := m.storage.Watch(ctx, storage.TableSLOTargets)
	if err != nil {
		slog.Warn("slo: watch targets failed", "err", err)
		return
	}
	for evt := range ch {
		switch evt.Type {
		case storage.EventUpsert:
			var st SLOTarget
			if err := json.Unmarshal(evt.Record.Payload, &st); err != nil {
				slog.Warn("slo: watch target unmarshal failed", "id", evt.Record.ID, "err", err)
				continue
			}
			m.mu.Lock()
			m.targets[st.ID] = st
			m.mu.Unlock()
		case storage.EventDelete:
			m.mu.Lock()
			delete(m.targets, evt.Record.ID)
			delete(m.breachAlerted, evt.Record.ID)
			m.mu.Unlock()
		}
	}
}

// ── Incidents (local-only) ────────────────────────────────────────────────

// loadIncidents populates m.incidents from the storage backend (via the
// non-broadcast inner backend — each node's incident list is local).
func (m *sloManager) loadIncidents(ctx context.Context) error {
	inner := m.storage.Inner()
	recs, err := inner.List(ctx, storage.TableSLOIncidents, storage.Filter{})
	if err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, rec := range recs {
		if rec.Tombstone {
			continue
		}
		var inc IncidentRecord
		if err := json.Unmarshal(rec.Payload, &inc); err != nil {
			slog.Warn("slo: malformed incident in storage", "id", rec.ID, "err", err)
			continue
		}
		m.incidents = append(m.incidents, inc)
		// Re-register any incidents that were open when the process last exited.
		if inc.EndedAt == nil {
			m.openStart[inc.TargetID] = inc.StartedAt
		}
	}
	if n := len(m.incidents); n > 0 {
		slog.Info("slo: incidents loaded from storage", "count", n)
	}
	return nil
}

// persistIncidentLocked writes a single incident to local storage (no
// gossip). Caller must hold m.mu. Errors are logged, not returned — the
// in-memory record is the authoritative copy and SLO compute keeps working.
//
// The incident's storage ID is "<target_id>-<unix_started_at>" so updates
// to the same incident (RecordEnd setting EndedAt) reuse the same row.
func (m *sloManager) persistIncidentLocked(inc IncidentRecord) {
	payload, err := json.Marshal(inc)
	if err != nil {
		slog.Warn("slo: marshal incident failed", "target", inc.TargetID, "err", err)
		return
	}
	id := fmt.Sprintf("%s-%d", inc.TargetID, inc.StartedAt.UTC().Unix())
	ver := m.storage.NextVersion()
	// Use Inner() — incidents are per-node, not gossip-replicated.
	if err := m.storage.Inner().Upsert(context.Background(),
		storage.TableSLOIncidents, id, payload, ver); err != nil {
		slog.Warn("slo: storage upsert incident failed", "id", id, "err", err)
	}
}

// RecordStart opens a new downtime incident for targetID.
// If an incident is already open for this target, this is a no-op.
//
// Persists the incident to the local storage table (no gossip — see
// type-level comment). Writes complete asynchronously inside the lock;
// errors are logged.
func (m *sloManager) RecordStart(targetID, errCode, scope string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, open := m.openStart[targetID]; open {
		return // already tracking an active incident
	}
	now := time.Now().UTC()
	m.openStart[targetID] = now
	inc := IncidentRecord{
		TargetID:  targetID,
		StartedAt: now,
		Scope:     scope,
		ErrorCode: errCode,
	}
	m.incidents = append(m.incidents, inc)
	m.persistIncidentLocked(inc)
	slog.Debug("slo: incident started", "target", targetID, "scope", scope)
}

// RecordEnd closes the currently open incident for targetID.
// If no incident is open, this is a no-op.
func (m *sloManager) RecordEnd(targetID string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	start, open := m.openStart[targetID]
	if !open {
		return
	}
	now := time.Now().UTC()
	delete(m.openStart, targetID)
	dur := now.Sub(start)
	// Close the most recent open incident record for this target.
	var closed *IncidentRecord
	for i := len(m.incidents) - 1; i >= 0; i-- {
		inc := &m.incidents[i]
		if inc.TargetID == targetID && inc.EndedAt == nil {
			inc.EndedAt = &now
			inc.DurationSec = int64(dur.Seconds())
			closed = inc
			break
		}
	}
	if closed != nil {
		// Re-upsert with the same storage ID (synthesized from target + start_unix)
		// so the EndedAt update overwrites the original record.
		m.persistIncidentLocked(*closed)
	}
	slog.Debug("slo: incident ended", "target", targetID, "duration_sec", int64(dur.Seconds()))
}

// PruneOldIncidents removes incident records that ended before the retention window.
// Open incidents (EndedAt == nil) are always kept.
//
// Tombstones are written to storage (rather than hard-deleting) so the
// SQLite row count grows bounded by retention × incident frequency. The
// tombstone path is used because storage Get / List filter them out by
// default — no manual maintenance needed.
func (m *sloManager) PruneOldIncidents(retentionDays int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	cutoff := time.Now().UTC().AddDate(0, 0, -retentionDays)

	var kept []IncidentRecord
	var pruned []IncidentRecord
	for _, inc := range m.incidents {
		// Keep: still open, or started/ended within retention window.
		if inc.EndedAt == nil || inc.EndedAt.After(cutoff) || inc.StartedAt.After(cutoff) {
			kept = append(kept, inc)
		} else {
			pruned = append(pruned, inc)
		}
	}
	if len(pruned) == 0 {
		return
	}
	m.incidents = kept

	// Tombstone the pruned incidents in storage (local-only, no broadcast).
	inner := m.storage.Inner()
	ctx := context.Background()
	for _, inc := range pruned {
		id := fmt.Sprintf("%s-%d", inc.TargetID, inc.StartedAt.UTC().Unix())
		ver := m.storage.NextVersion()
		if err := inner.Delete(ctx, storage.TableSLOIncidents, id, ver); err != nil {
			slog.Warn("slo: prune storage delete failed", "id", id, "err", err)
		}
	}
	slog.Info("slo: pruned old incidents", "pruned", len(pruned), "kept", len(kept))
}

// incidentsForTarget returns all incident records for targetID that overlap
// with the half-open interval [since, now). Returned slice is sorted by StartedAt.
// Caller must NOT hold m.mu.
func (m *sloManager) incidentsForTarget(targetID string, since time.Time) []IncidentRecord {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := time.Now().UTC()
	var result []IncidentRecord
	for _, inc := range m.incidents {
		if inc.TargetID != targetID {
			continue
		}
		// Exclude incidents that ended entirely before the window.
		if inc.EndedAt != nil && inc.EndedAt.Before(since) {
			continue
		}
		// Exclude incidents that haven't started yet (shouldn't happen, but guard).
		if inc.StartedAt.After(now) {
			continue
		}
		result = append(result, inc)
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].StartedAt.Before(result[j].StartedAt)
	})
	return result
}

// WasBreachAlerted returns whether a breach alert has been sent for targetID
// in the current breach period. Resets when the breach clears.
func (m *sloManager) WasBreachAlerted(targetID string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.breachAlerted[targetID]
}

// SetBreachAlerted marks (or clears) the breach-alerted flag for targetID.
func (m *sloManager) SetBreachAlerted(targetID string, v bool) {
	m.mu.Lock()
	m.breachAlerted[targetID] = v
	m.mu.Unlock()
}

// ── SLO calculation ───────────────────────────────────────────────────────────

// SLOResult holds the computed SLO metrics for one target over a window.
type SLOResult struct {
	TargetID           string           `json:"target_id"`
	TargetUptime       float64          `json:"target_uptime"`
	ActualUptime       float64          `json:"actual_uptime"`
	Window             string           `json:"window"`
	WindowDurationSec  int64            `json:"window_duration_sec"`
	DowntimeSec        int64            `json:"downtime_sec"`
	DowntimeMinutes    float64          `json:"downtime_minutes"`
	IncidentCount      int              `json:"incident_count"`
	LongestIncidentSec int64            `json:"longest_incident_sec,omitempty"`
	SLOBreached        bool             `json:"slo_breached"`
	// RemainingBudgetSec is positive when within budget, negative when breached.
	RemainingBudgetSec int64            `json:"remaining_budget_sec"`
	Incidents          []IncidentRecord `json:"incidents"`
}

// SLOSnapshot is the full GET /slo response payload.
type SLOSnapshot struct {
	ComputedAt time.Time              `json:"computed_at"`
	Targets    map[string]SLOResult   `json:"targets"`
}

// parseWindow converts a duration string ("30d", "7d", "24h") to time.Duration.
func parseWindow(s string) (time.Duration, error) {
	s = strings.TrimSpace(s)
	if strings.HasSuffix(s, "d") {
		days, err := strconv.Atoi(strings.TrimSuffix(s, "d"))
		if err != nil || days <= 0 {
			return 0, fmt.Errorf("invalid window %q: expected positive integer followed by 'd' or 'h'", s)
		}
		return time.Duration(days) * 24 * time.Hour, nil
	}
	if strings.HasSuffix(s, "h") {
		hours, err := strconv.Atoi(strings.TrimSuffix(s, "h"))
		if err != nil || hours <= 0 {
			return 0, fmt.Errorf("invalid window %q", s)
		}
		return time.Duration(hours) * time.Hour, nil
	}
	return 0, fmt.Errorf("invalid window %q: must be like '30d' or '24h'", s)
}

// ComputeSLO calculates SLO metrics for targetID over window.
func (m *sloManager) ComputeSLO(targetID string, targetUptime float64, window string) (SLOResult, error) {
	dur, err := parseWindow(window)
	if err != nil {
		return SLOResult{}, err
	}

	now := time.Now().UTC()
	since := now.Add(-dur)
	windowSec := int64(dur.Seconds())

	incidents := m.incidentsForTarget(targetID, since)

	var downtimeSec, longestSec int64
	for _, inc := range incidents {
		// Clamp start to window boundary.
		start := inc.StartedAt
		if start.Before(since) {
			start = since
		}
		var end time.Time
		if inc.EndedAt != nil {
			end = *inc.EndedAt
		} else {
			end = now // incident still active
		}
		d := int64(math.Max(0, end.Sub(start).Seconds()))
		downtimeSec += d
		if d > longestSec {
			longestSec = d
		}
	}
	// Cap downtime at window size (shouldn't happen, but guard against clock skew).
	if downtimeSec > windowSec {
		downtimeSec = windowSec
	}

	uptimeSec := windowSec - downtimeSec
	actualUptime := float64(uptimeSec) / float64(windowSec)

	// Error budget: allowed downtime in seconds for the given target uptime.
	budgetSec := int64(math.Round(float64(windowSec) * (1.0 - targetUptime)))
	remainingBudgetSec := budgetSec - downtimeSec

	return SLOResult{
		TargetID:           targetID,
		TargetUptime:       targetUptime,
		ActualUptime:       actualUptime,
		Window:             window,
		WindowDurationSec:  windowSec,
		DowntimeSec:        downtimeSec,
		DowntimeMinutes:    float64(downtimeSec) / 60.0,
		IncidentCount:      len(incidents),
		LongestIncidentSec: longestSec,
		SLOBreached:        actualUptime < targetUptime,
		RemainingBudgetSec: remainingBudgetSec,
		Incidents:          incidents,
	}, nil
}

// ── Engine integration ────────────────────────────────────────────────────────

// sloRecordStart records the start of a downtime incident for target t.
// It is a no-op when the SLO tracker is not enabled (sloMgr == nil).
func (e *Engine) sloRecordStart(t Target, errCode string) {
	if e.sloMgr == nil {
		return
	}
	scope := e.computeScope(t.key(), true)
	e.sloMgr.RecordStart(t.key(), errCode, scope)
}

// sloRecordEnd closes the open incident for target t.
// It is a no-op when the SLO tracker is not enabled (sloMgr == nil).
func (e *Engine) sloRecordEnd(t Target) {
	if e.sloMgr == nil {
		return
	}
	e.sloMgr.RecordEnd(t.key())
}

// SLOSnapshot computes current SLO metrics for all configured SLO targets.
// Returns nil when SLO is not enabled.
//
// B24: targets are sourced from the storage-backed sloManager (cluster-
// replicated), not config.yaml directly.
func (e *Engine) SLOSnapshot() *SLOSnapshot {
	if e.sloMgr == nil {
		return nil
	}
	e.mu.RLock()
	sloCfg := e.cfg.SLO
	e.mu.RUnlock()
	if sloCfg == nil || !sloCfg.Enabled {
		return nil
	}

	targets := e.sloMgr.Targets()
	snap := &SLOSnapshot{
		ComputedAt: time.Now().UTC(),
		Targets:    make(map[string]SLOResult, len(targets)),
	}
	for _, st := range targets {
		result, err := e.sloMgr.ComputeSLO(st.ID, st.TargetUptime, st.Window)
		if err != nil {
			slog.Warn("slo: compute error", "target", st.ID, "err", err)
			continue
		}
		snap.Targets[st.ID] = result
	}
	return snap
}

// runSLOChecker is the background goroutine that:
//  1. Prunes stale incident records (retentionDays)
//  2. Checks each SLO target for breaches hourly
//  3. Updates Prometheus SLO metrics
//  4. Sends breach alerts (edge-triggered: once per breach period)
func (e *Engine) runSLOChecker(ctx context.Context) {
	// Prune on startup, then hourly.
	e.mu.RLock()
	sloCfg := e.cfg.SLO
	e.mu.RUnlock()
	if sloCfg == nil {
		return
	}
	e.sloMgr.PruneOldIncidents(sloCfg.retentionDays())

	// Run an initial check immediately so Prometheus SLO gauges are populated
	// from the first scrape — without this, metrics only appear after 1 hour.
	e.checkSLOBreaches()

	ticker := time.NewTicker(time.Hour)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			e.checkSLOBreaches()
		}
	}
}

// checkSLOBreaches evaluates all SLO targets and dispatches breach alerts.
//
// B24: targets sourced from storage-backed sloManager (not config.yaml).
func (e *Engine) checkSLOBreaches() {
	e.mu.RLock()
	sloCfg := e.cfg.SLO
	channels := e.channels
	defaultNotify := e.cfg.DefaultNotify
	e.mu.RUnlock()

	if sloCfg == nil || !sloCfg.Enabled {
		return
	}

	sloNotify := sloCfg.SLONotify
	retentionDays := sloCfg.retentionDays()
	e.sloMgr.PruneOldIncidents(retentionDays)

	for _, st := range e.sloMgr.Targets() {
		result, err := e.sloMgr.ComputeSLO(st.ID, st.TargetUptime, st.Window)
		if err != nil {
			slog.Warn("slo: compute error in breach check", "target", st.ID, "err", err)
			continue
		}

		// Update Prometheus metrics.
		GaugeSLOUptimeRatio.WithLabelValues(st.ID, st.Window).Set(result.ActualUptime)
		GaugeSLOErrorBudgetSeconds.WithLabelValues(st.ID, st.Window).Set(float64(result.RemainingBudgetSec))
		if result.SLOBreached {
			GaugeSLOBreached.WithLabelValues(st.ID).Set(1)
		} else {
			GaugeSLOBreached.WithLabelValues(st.ID).Set(0)
		}

		// Edge-triggered breach alert: alert when breach first detected, clear when resolved.
		wasAlerted := e.sloMgr.WasBreachAlerted(st.ID)
		if result.SLOBreached && !wasAlerted {
			e.sloMgr.SetBreachAlerted(st.ID, true)
			e.sendSLOBreachAlert(st.ID, result, sloNotify, defaultNotify, channels)
		} else if !result.SLOBreached && wasAlerted {
			// Budget recovered — clear flag so a future breach sends a fresh alert.
			e.sloMgr.SetBreachAlerted(st.ID, false)
			slog.Info("slo: SLO budget recovered", "target", st.ID, "actual_uptime", fmt.Sprintf("%.4f", result.ActualUptime))
		}
	}
}

// sendSLOBreachAlert dispatches an alert to the configured SLO notify channels.
// It resolves the target's display name from the current config (best-effort).
func (e *Engine) sendSLOBreachAlert(targetID string, result SLOResult, sloNotify, defaultNotify []string, channels map[string]Alerter) {
	names := sloNotify
	if len(names) == 0 {
		names = defaultNotify
	}
	if len(names) == 0 {
		return
	}

	// Resolve display name and type from config (best-effort).
	e.mu.RLock()
	var targetName, targetAddr, targetType string
	for _, t := range e.cfg.Targets {
		if t.key() == targetID {
			targetName = t.Name
			targetAddr = t.Target
			targetType = t.Type
			break
		}
	}
	e.mu.RUnlock()
	if targetName == "" {
		targetName = targetID
	}

	env := map[string]string{
		"NAME":                    targetName,
		"TARGET":                  targetAddr,
		"TYPE":                    targetType,
		"APP_NAME":                e.NodeAlias(),
		"NODE_NAME":               e.hostname,
		"STATUS":                  "slo_breached",
		"SLO_TARGET_UPTIME":       fmt.Sprintf("%.4f", result.TargetUptime),
		"SLO_ACTUAL_UPTIME":       fmt.Sprintf("%.4f", result.ActualUptime),
		"SLO_WINDOW":              result.Window,
		"SLO_DOWNTIME_MINUTES":    fmt.Sprintf("%.1f", result.DowntimeMinutes),
		"SLO_INCIDENT_COUNT":      strconv.Itoa(result.IncidentCount),
		"SLO_ERROR_BUDGET_SEC":    strconv.FormatInt(result.RemainingBudgetSec, 10),
		"SLO_LONGEST_INCIDENT_SEC": strconv.FormatInt(result.LongestIncidentSec, 10),
	}

	slog.Info("slo: sending breach alert",
		"target", targetID,
		"actual_uptime", fmt.Sprintf("%.4f", result.ActualUptime),
		"target_uptime", fmt.Sprintf("%.4f", result.TargetUptime),
		"channels", names,
	)

	for _, name := range names {
		ch, ok := channels[name]
		if !ok {
			slog.Warn("slo: alert channel not found", "channel", name, "target", targetID)
			continue
		}
		go func(n string, c Alerter) {
			if err := c.Send(env); err != nil {
				slog.Error("slo: alert send failed", "channel", n, "target", targetID, "err", err)
			}
		}(name, ch)
	}
}
