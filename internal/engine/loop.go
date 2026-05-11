package engine

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/saidtaylan/netwatch/internal/cluster"
)

// PendingEntry tracks retry state for a target that is failing but has not yet
// exhausted its retry budget. Lives only in RAM — never persisted to disk.
//
// Invariant: a target is in pending iff it has failed at least once and its
// hard-down notification has NOT yet been sent.
type PendingEntry struct {
	Target        Target
	RetryCount    int
	NextCheckTime time.Time
	LastErrorCode string // most recent probe failure message
}

// ── Probe loop (autonomous, per-target) ───────────────────────────────────────

// startProbeLoop launches a goroutine that probes t on its configured interval.
// Calling this again for the same target.key() cancels the previous goroutine first.
func (e *Engine) startProbeLoop(t Target) {
	e.probesMu.Lock()
	if cancel, ok := e.probeCancel[t.key()]; ok {
		cancel()
	}
	ctx, cancel := context.WithCancel(context.Background())
	e.probeCancel[t.key()] = cancel
	e.probesMu.Unlock()

	go func() {
		// Probe immediately on start so metrics are populated before the first interval.
		e.runCheck(ctx, t)

		e.mu.RLock()
		intervalSec := e.cfg.globalProbeInterval()
		e.mu.RUnlock()
		if t.IntervalSec != nil {
			intervalSec = *t.IntervalSec
		}

		ticker := time.NewTicker(time.Duration(intervalSec) * time.Second)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				e.runCheck(ctx, t)
			}
		}
	}()
}

// stopProbeLoop cancels the probe goroutine for the given target key.
func (e *Engine) stopProbeLoop(key string) {
	e.probesMu.Lock()
	if cancel, ok := e.probeCancel[key]; ok {
		cancel()
		delete(e.probeCancel, key)
	}
	e.probesMu.Unlock()
}

// runCheck executes a single probe and drives the state machine.
// It is called by both startProbeLoop (scheduled) and runRetryLoop (retries).
func (e *Engine) runCheck(ctx context.Context, t Target) {
	// During anti-entropy re-join the engine merges remote state; skip probes
	// and alarms until reconciliation is complete to avoid alarm storms.
	if e.syncing.Load() {
		return
	}

	labels := prometheus.Labels{
		"name":        t.key(),
		"target":      t.Target,
		"type":        t.Type,
		"source_host": e.hostname,
		"app_name":   e.AppName(),
	}

	start := time.Now()
	ok, probeErr := e.execProbe(ctx, t)
	elapsed := time.Since(start).Seconds()

	pkey := t.typeKey()

	e.stateMu.RLock()
	ps, seen := e.lastKnown[t.key()]
	prevUp := ps.State == "up"
	_, inPending := e.pending[pkey]
	e.stateMu.RUnlock()

	if ok {
		if !seen {
			// First observation: register silently.
			e.stateMu.Lock()
			e.lastKnown[t.key()] = PersistedState{State: "up"}
			e.stateMu.Unlock()
			e.persistState()
		} else if inPending || !prevUp {
			// Recovery from soft-down or hard-down.
			if e.markRecovered(pkey, t) {
				slog.Info("target recovered", "name", t.key(), "target", t.Target, "latency", elapsed)
				if e.shouldAlert(t.key()) {
					e.sendAlert(t, "reachable")
				}
			}
		}
		GaugeUp.With(labels).Set(1)
	} else {
		errCode := ""
		if probeErr != nil {
			errCode = probeErr.Error()
		}
		if seen && !prevUp && !inPending {
			// Already hard-down; retry loop will handle it.
		} else if !inPending {
			interval := e.effectiveRetryInterval(t)
			if e.enqueue(pkey, t, interval, errCode) {
				slog.Warn("probe failed, queued for retry", "name", t.key(), "target", t.Target, "err", probeErr, "latency", elapsed)
			}
		}
		GaugeUp.With(labels).Set(0)
	}

	GaugeDuration.With(labels).Set(elapsed)
}

// ── Retry loop (soft-down management) ────────────────────────────────────────

// runRetryLoop is the background goroutine that re-probes pending (soft-down) targets
// and escalates to hard-down after max_retries are exhausted.
func (e *Engine) runRetryLoop(ctx context.Context) {
	e.mu.RLock()
	intervalSec := e.cfg.globalTickerInterval()
	e.mu.RUnlock()

	interval := time.Duration(intervalSec) * time.Second
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			e.processPending(ctx)

			// Adapt if config was hot-reloaded with a new ticker interval.
			e.mu.RLock()
			newSec := e.cfg.globalTickerInterval()
			e.mu.RUnlock()
			if newInterval := time.Duration(newSec) * time.Second; newInterval != interval {
				interval = newInterval
				ticker.Reset(interval)
			}
		}
	}
}

// processPending iterates due pending entries, re-probes each, and transitions state.
func (e *Engine) processPending(ctx context.Context) {
	// Skip during anti-entropy sync — state is being reconciled from peers.
	if e.syncing.Load() {
		return
	}

	now := time.Now()

	type due struct {
		key   string
		entry PendingEntry
	}

	e.stateMu.RLock()
	var dues []due
	for k, s := range e.pending {
		if !now.Before(s.NextCheckTime) {
			dues = append(dues, due{k, s})
		}
	}
	e.stateMu.RUnlock()

	for _, d := range dues {
		t := d.entry.Target

		e.mu.RLock()
		timeoutSec := e.cfg.Timeout
		if t.Timeout != nil {
			timeoutSec = *t.Timeout
		}
		if timeoutSec <= 0 {
			timeoutSec = 5
		}
		e.mu.RUnlock()

		probeCtx, cancel := context.WithTimeout(ctx, time.Duration(timeoutSec)*time.Second)
		ok, probeErr := e.execProbe(probeCtx, t)
		cancel()

		pkey := t.typeKey()
		maxRetries := e.effectiveMaxRetries(t)
		retryInterval := e.effectiveRetryInterval(t)

		errCode := ""
		if probeErr != nil {
			errCode = probeErr.Error()
		}

		if ok {
			if e.markRecovered(pkey, t) {
				slog.Info("target recovered after retries", "name", t.key(), "target", t.Target, "retries", d.entry.RetryCount)
				if e.shouldAlert(t.key()) {
					e.sendAlert(t, "reachable")
				}
			}
			continue
		}

		newCount := d.entry.RetryCount + 1
		if newCount >= maxRetries {
			if e.markHardDown(pkey, t, errCode) {
				slog.Error("target hard-down after retries", "name", t.key(), "target", t.Target, "retries", newCount)
				if e.shouldAlert(t.key()) {
					e.sendAlert(t, "unreachable")
				}
			}
		} else {
			e.stateMu.Lock()
			if cur, exists := e.pending[pkey]; exists {
				cur.RetryCount = newCount
				cur.NextCheckTime = time.Now().Add(retryInterval)
				cur.LastErrorCode = errCode
				e.pending[pkey] = cur
			}
			e.stateMu.Unlock()
			slog.Warn("probe retry failed", "name", t.key(), "target", t.Target, "retry", newCount, "max", maxRetries, "next_in", retryInterval)
		}
	}
}

// ── Atomic state transitions ──────────────────────────────────────────────────

// markRecovered atomically transitions a target from soft/hard-down to up.
// Returns true if the transition was actually performed (false = already up).
// Bumps Seq, clears ErrorCode, and broadcasts the new state to the cluster.
func (e *Engine) markRecovered(pkey string, t Target) bool {
	skey := t.key()
	e.stateMu.Lock()
	_, inPending := e.pending[pkey]
	ps, seen := e.lastKnown[skey]
	if seen && ps.State == "up" && !inPending {
		e.stateMu.Unlock()
		return false
	}
	delete(e.pending, pkey)
	ps.State = "up"
	ps.Seq++
	ps.ErrorCode = ""
	e.lastKnown[skey] = ps
	e.stateMu.Unlock()
	e.persistState()
	e.broadcastState(t, ps)
	return true
}

// markHardDown atomically escalates a pending target to hard-down state.
// Returns true if the transition was performed (false = entry already gone).
// Bumps Seq, stores errCode, and broadcasts the new state to the cluster.
func (e *Engine) markHardDown(pkey string, t Target, errCode string) bool {
	e.stateMu.Lock()
	if _, exists := e.pending[pkey]; !exists {
		e.stateMu.Unlock()
		return false
	}
	delete(e.pending, pkey)
	ps := e.lastKnown[t.key()]
	ps.State = "hard_down"
	ps.Seq++
	ps.ErrorCode = errCode
	e.lastKnown[t.key()] = ps
	e.stateMu.Unlock()
	e.persistState()
	e.broadcastState(t, ps)
	return true
}

// enqueue adds a target to the pending (soft-down) map if not already present.
// Returns true if the entry was newly added.
func (e *Engine) enqueue(pkey string, t Target, delay time.Duration, errCode string) bool {
	e.stateMu.Lock()
	defer e.stateMu.Unlock()
	if _, exists := e.pending[pkey]; exists {
		return false
	}
	e.pending[pkey] = PendingEntry{
		Target:        t,
		RetryCount:    0,
		NextCheckTime: time.Now().Add(delay),
		LastErrorCode: errCode,
	}
	return true
}

// ── Probe execution ───────────────────────────────────────────────────────────

// execProbe runs a single probe for t using its registered checker.
func (e *Engine) execProbe(ctx context.Context, t Target) (bool, error) {
	checker, ok := e.checkers[t.Type]
	if !ok {
		return false, fmt.Errorf("no checker for type %q", t.Type)
	}
	return checker.Run(ctx, t.Target, t.Options)
}

// ── Effective parameter helpers ───────────────────────────────────────────────

func (e *Engine) effectiveMaxRetries(t Target) int {
	if t.MaxRetries != nil {
		return *t.MaxRetries
	}
	e.mu.RLock()
	v := e.cfg.globalMaxRetries()
	e.mu.RUnlock()
	return v
}

func (e *Engine) effectiveRetryInterval(t Target) time.Duration {
	if t.RetryIntervalSec != nil {
		return time.Duration(*t.RetryIntervalSec) * time.Second
	}
	e.mu.RLock()
	v := e.cfg.globalRetryInterval()
	e.mu.RUnlock()
	return time.Duration(v) * time.Second
}

// broadcastState sends a GossipPayload for target t to the cluster.
// It is a no-op when clusterMgr is nil (standalone mode).
//
// TargetName and TargetType are included in the payload so that a primary
// node that does not probe this target locally can still build a meaningful
// alert message. Without these fields the primary would only know the raw
// target ID (e.g. "127.0.0.1:5432") and lose the human-readable name.
func (e *Engine) broadcastState(t Target, ps PersistedState) {
	if e.clusterMgr == nil {
		return
	}
	e.clusterMgr.Broadcast(cluster.GossipPayload{
		TargetID:   t.key(),
		TargetName: t.Name,
		TargetType: t.Type,
		State:      ps.State,
		Seq:        ps.Seq,
		ErrorCode:  ps.ErrorCode,
		NodeName:   e.hostname,
		Timestamp:  time.Now(),
	})
}

// broadcastStateByID looks up the target by ID in the current config and calls
// broadcastState. Used by ApplyRemoteState where only the target ID is known.
// If the target is not in the local config (different-config node), a minimal
// payload is sent with only the ID — peers that know this target will have
// already populated TargetName/Type from their own broadcasts.
func (e *Engine) broadcastStateByID(targetID string, ps PersistedState) {
	if e.clusterMgr == nil {
		return
	}
	e.mu.RLock()
	var found *Target
	for i := range e.cfg.Targets {
		if e.cfg.Targets[i].key() == targetID {
			found = &e.cfg.Targets[i]
			break
		}
	}
	e.mu.RUnlock()

	if found != nil {
		e.broadcastState(*found, ps)
		return
	}
	// Target not in local config — send minimal payload.
	e.clusterMgr.Broadcast(cluster.GossipPayload{
		TargetID:  targetID,
		State:     ps.State,
		Seq:       ps.Seq,
		ErrorCode: ps.ErrorCode,
		NodeName:  e.hostname,
		Timestamp: time.Now(),
	})
}

// effectiveTimeout returns the probe timeout for t in seconds (minimum 1).
func (e *Engine) effectiveTimeout(t Target) int {
	if t.Timeout != nil && *t.Timeout > 0 {
		return *t.Timeout
	}
	e.mu.RLock()
	v := e.cfg.Timeout
	e.mu.RUnlock()
	if v <= 0 {
		return 5
	}
	return v
}

// ── Hot-reload helper (called externally) ─────────────────────────────────────

// Reload re-reads the config file and reconciles running probe goroutines.
// New targets get a goroutine; removed targets have theirs cancelled.
func (e *Engine) Reload() {
	e.mu.RLock()
	oldTargets := e.cfg.Targets
	e.mu.RUnlock()

	oldKeys := make(map[string]bool, len(oldTargets))
	for _, t := range oldTargets {
		if t.active() {
			oldKeys[t.key()] = true
		}
	}

	if err := e.LoadConfig(); err != nil {
		slog.Error("config reload failed, keeping last config", "err", err)
		return
	}

	e.mu.RLock()
	newTargets := e.cfg.Targets
	e.mu.RUnlock()

	newKeys := make(map[string]bool, len(newTargets))
	for _, t := range newTargets {
		if t.active() {
			newKeys[t.key()] = true
		}
	}

	// Stop probes for removed targets.
	for k := range oldKeys {
		if !newKeys[k] {
			e.stopProbeLoop(k)
		}
	}

	// Start probes for new targets.
	for _, t := range newTargets {
		if t.active() && !oldKeys[t.key()] {
			e.startProbeLoop(t)
		}
	}

	// Phase 13: re-announce inventory so peers learn about any added targets
	// (and re-affirm existing ones). bootstrapInventoryBroadcast is a no-op
	// when the cluster layer is disabled.
	e.bootstrapInventoryBroadcast()
}
