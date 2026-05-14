package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
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

// incidentFileV1 is the on-disk envelope for incidents.json.
type incidentFileV1 struct {
	Version   int              `json:"version"`
	Incidents []IncidentRecord `json:"incidents"`
}

// ── sloManager ────────────────────────────────────────────────────────────────

// sloManager persists incident history and answers SLO queries.
// All exported methods are safe for concurrent use.
type sloManager struct {
	mu            sync.Mutex
	path          string             // absolute path to incidents.json; "" = no persistence
	incidents     []IncidentRecord   // in-memory + persisted
	openStart     map[string]time.Time // targetID → start of the currently open incident
	breachAlerted map[string]bool    // targetID → true once a breach alert has been sent
}

// newSLOManager creates an sloManager that persists incidents next to stateFile.
// When stateFile is empty, persistence is skipped (in-memory only).
func newSLOManager(stateFile string) *sloManager {
	path := ""
	if stateFile != "" {
		path = filepath.Join(filepath.Dir(stateFile), "incidents.json")
	}
	m := &sloManager{
		path:          path,
		openStart:     make(map[string]time.Time),
		breachAlerted: make(map[string]bool),
	}
	if path != "" {
		m.load()
	}
	return m
}

// load reads incidents from disk. Called once at startup.
func (m *sloManager) load() {
	data, err := os.ReadFile(m.path)
	if os.IsNotExist(err) {
		return
	}
	if err != nil {
		slog.Error("slo: failed to read incidents file", "path", m.path, "err", err)
		return
	}
	var f incidentFileV1
	if err := json.Unmarshal(data, &f); err != nil {
		slog.Error("slo: failed to parse incidents file", "path", m.path, "err", err)
		return
	}
	m.incidents = f.Incidents
	// Re-register any incidents that were open when the process last exited.
	for _, inc := range m.incidents {
		if inc.EndedAt == nil {
			m.openStart[inc.TargetID] = inc.StartedAt
		}
	}
	slog.Info("slo: incidents loaded", "path", m.path, "count", len(m.incidents))
}

// save atomically writes incidents to disk. Called under m.mu.
func (m *sloManager) save() {
	if m.path == "" {
		return
	}
	data, err := json.MarshalIndent(incidentFileV1{Version: 1, Incidents: m.incidents}, "", "  ")
	if err != nil {
		slog.Error("slo: marshal error", "err", err)
		return
	}
	tmp := m.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0644); err != nil {
		slog.Error("slo: write error", "path", tmp, "err", err)
		return
	}
	if err := os.Rename(tmp, m.path); err != nil {
		slog.Error("slo: rename error", "src", tmp, "dst", m.path, "err", err)
	}
}

// RecordStart opens a new downtime incident for targetID.
// If an incident is already open for this target, this is a no-op.
func (m *sloManager) RecordStart(targetID, errCode, scope string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, open := m.openStart[targetID]; open {
		return // already tracking an active incident
	}
	now := time.Now().UTC()
	m.openStart[targetID] = now
	m.incidents = append(m.incidents, IncidentRecord{
		TargetID:  targetID,
		StartedAt: now,
		Scope:     scope,
		ErrorCode: errCode,
	})
	m.save()
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
	for i := len(m.incidents) - 1; i >= 0; i-- {
		inc := &m.incidents[i]
		if inc.TargetID == targetID && inc.EndedAt == nil {
			inc.EndedAt = &now
			inc.DurationSec = int64(dur.Seconds())
			break
		}
	}
	m.save()
	slog.Debug("slo: incident ended", "target", targetID, "duration_sec", int64(dur.Seconds()))
}

// PruneOldIncidents removes incident records that ended before the retention window.
// Open incidents (EndedAt == nil) are always kept.
func (m *sloManager) PruneOldIncidents(retentionDays int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	cutoff := time.Now().UTC().AddDate(0, 0, -retentionDays)
	var kept []IncidentRecord
	for _, inc := range m.incidents {
		// Keep: still open, or started/ended within retention window.
		if inc.EndedAt == nil || inc.EndedAt.After(cutoff) || inc.StartedAt.After(cutoff) {
			kept = append(kept, inc)
		}
	}
	if len(kept) != len(m.incidents) {
		pruned := len(m.incidents) - len(kept)
		m.incidents = kept
		m.save()
		slog.Info("slo: pruned old incidents", "pruned", pruned, "kept", len(kept))
	}
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

	snap := &SLOSnapshot{
		ComputedAt: time.Now().UTC(),
		Targets:    make(map[string]SLOResult, len(sloCfg.Targets)),
	}
	for _, st := range sloCfg.Targets {
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

	for _, st := range sloCfg.Targets {
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
