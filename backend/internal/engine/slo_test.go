package engine

import (
	"context"
	"testing"
	"time"

	"github.com/saidtaylan/netwatch/internal/storage"
	"github.com/saidtaylan/netwatch/internal/storage/gossip"
)

// ── parseWindow ───────────────────────────────────────────────────────────────

func TestParseWindow(t *testing.T) {
	cases := []struct {
		input   string
		wantSec int64
		wantErr bool
	}{
		{"30d", 30 * 24 * 3600, false},
		{"7d", 7 * 24 * 3600, false},
		{"24h", 24 * 3600, false},
		{"1h", 3600, false},
		{"0d", 0, true},
		{"bad", 0, true},
		{"", 0, true},
	}
	for _, c := range cases {
		d, err := parseWindow(c.input)
		if c.wantErr {
			if err == nil {
				t.Errorf("parseWindow(%q): expected error, got nil", c.input)
			}
			continue
		}
		if err != nil {
			t.Errorf("parseWindow(%q): unexpected error: %v", c.input, err)
			continue
		}
		if int64(d.Seconds()) != c.wantSec {
			t.Errorf("parseWindow(%q): want %d sec, got %d", c.input, c.wantSec, int64(d.Seconds()))
		}
	}
}

// ── sloManager: basic incident lifecycle ─────────────────────────────────────

// sloTestEnv keeps the shared backend alive across re-instantiations of the
// SLO manager within a single test (used by the persistence round-trip test).
type sloTestEnv struct {
	mem storage.StorageBackend
	gs  *gossip.Storage
}

func newTestSLOMgrEnv(t *testing.T) (*sloManager, *sloTestEnv) {
	t.Helper()
	mem := storage.NewMemoryStorage()
	t.Cleanup(func() { _ = mem.Close() })
	gs := gossip.NewStorage(mem, nil, nil, "test-node")
	mm, err := newSLOManager(context.Background(), gs, "test-node", nil)
	if err != nil {
		t.Fatalf("newSLOManager: %v", err)
	}
	t.Cleanup(mm.Close)
	return mm, &sloTestEnv{mem: mem, gs: gs}
}

func newTestSLOMgr(t *testing.T) (*sloManager, *sloTestEnv) {
	return newTestSLOMgrEnv(t)
}

func TestSLOManager_RecordStartEnd(t *testing.T) {
	m, _ := newTestSLOMgr(t)

	m.RecordStart("db", "timeout", "GLOBAL")
	if len(m.incidents) != 1 {
		t.Fatalf("want 1 incident, got %d", len(m.incidents))
	}
	if m.incidents[0].EndedAt != nil {
		t.Error("incident should be open (EndedAt == nil)")
	}

	time.Sleep(10 * time.Millisecond)
	m.RecordEnd("db")
	if m.incidents[0].EndedAt == nil {
		t.Error("incident should be closed (EndedAt != nil)")
	}
	if m.incidents[0].DurationSec < 0 {
		t.Errorf("duration_sec should be >= 0, got %d", m.incidents[0].DurationSec)
	}
}

func TestSLOManager_RecordStart_NoopWhenOpen(t *testing.T) {
	m, _ := newTestSLOMgr(t)

	m.RecordStart("db", "err1", "GLOBAL")
	m.RecordStart("db", "err2", "GLOBAL") // should be a no-op
	if len(m.incidents) != 1 {
		t.Errorf("want 1 incident (second RecordStart is no-op), got %d", len(m.incidents))
	}
}

func TestSLOManager_RecordEnd_NoopWhenClosed(t *testing.T) {
	m, _ := newTestSLOMgr(t)

	// RecordEnd when no incident is open should not panic or change state.
	m.RecordEnd("db")
	if len(m.incidents) != 0 {
		t.Errorf("want 0 incidents, got %d", len(m.incidents))
	}
}

// ── sloManager: persistence round-trip ───────────────────────────────────────

func TestSLOManager_PersistAndLoad(t *testing.T) {
	m, env := newTestSLOMgrEnv(t)

	m.RecordStart("api", "connection refused", "NODE_LOCAL")
	time.Sleep(5 * time.Millisecond)
	m.RecordEnd("api")
	m.Close() // stop watch goroutine on first manager

	// Re-instantiate against the SAME underlying memory storage — loadIncidents
	// should rehydrate the persisted record.
	m2, err := newSLOManager(context.Background(), env.gs, "test-node", nil)
	if err != nil {
		t.Fatalf("second newSLOManager: %v", err)
	}
	t.Cleanup(m2.Close)
	if len(m2.incidents) != 1 {
		t.Fatalf("loaded: want 1 incident, got %d", len(m2.incidents))
	}
	if m2.incidents[0].TargetID != "api" {
		t.Errorf("loaded target_id: want api, got %q", m2.incidents[0].TargetID)
	}
}

// ── ComputeSLO ────────────────────────────────────────────────────────────────

func TestComputeSLO_NoDowntime(t *testing.T) {
	m, _ := newTestSLOMgr(t)

	result, err := m.ComputeSLO("db", 0.999, "30d")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.DowntimeSec != 0 {
		t.Errorf("want 0 downtime, got %d", result.DowntimeSec)
	}
	if result.ActualUptime != 1.0 {
		t.Errorf("want uptime 1.0, got %.4f", result.ActualUptime)
	}
	if result.SLOBreached {
		t.Error("SLO should not be breached with zero downtime")
	}
	if result.RemainingBudgetSec <= 0 {
		t.Errorf("remaining budget should be > 0 for 99.9%% SLO, got %d", result.RemainingBudgetSec)
	}
}

func TestComputeSLO_WithDowntime_Breached(t *testing.T) {
	m, _ := newTestSLOMgr(t)

	// Simulate a 2-hour incident within a 7-day window.
	start := time.Now().UTC().Add(-3 * time.Hour)
	end := start.Add(2 * time.Hour)
	m.mu.Lock()
	m.incidents = append(m.incidents, IncidentRecord{
		TargetID:    "db",
		StartedAt:   start,
		EndedAt:     &end,
		DurationSec: int64((2 * time.Hour).Seconds()),
	})
	m.mu.Unlock()

	// 7-day window; 99.9% uptime means budget = 7*24*3600 * 0.001 ≈ 604 s
	result, err := m.ComputeSLO("db", 0.999, "7d")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.DowntimeSec != int64((2 * time.Hour).Seconds()) {
		t.Errorf("downtime: want %d, got %d", int64((2*time.Hour).Seconds()), result.DowntimeSec)
	}
	if !result.SLOBreached {
		t.Error("SLO should be breached (2h downtime >> 604s budget)")
	}
	if result.RemainingBudgetSec >= 0 {
		t.Errorf("remaining budget should be negative when breached, got %d", result.RemainingBudgetSec)
	}
	if result.IncidentCount != 1 {
		t.Errorf("incident_count: want 1, got %d", result.IncidentCount)
	}
}

func TestComputeSLO_ActiveIncident_CountsAsOngoing(t *testing.T) {
	m, _ := newTestSLOMgr(t)

	// Open incident (no EndedAt) started 10 minutes ago.
	m.RecordStart("svc", "timeout", "GLOBAL")
	// Move the start time back.
	m.mu.Lock()
	m.incidents[0].StartedAt = time.Now().UTC().Add(-10 * time.Minute)
	m.mu.Unlock()

	result, err := m.ComputeSLO("svc", 0.9999, "1h")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Active incident contributes ~600 seconds downtime.
	if result.DowntimeSec < 590 {
		t.Errorf("active incident should contribute ~600s, got %d", result.DowntimeSec)
	}
}

func TestComputeSLO_InvalidWindow(t *testing.T) {
	m, _ := newTestSLOMgr(t)
	_, err := m.ComputeSLO("db", 0.999, "bad")
	if err == nil {
		t.Error("expected error for invalid window, got nil")
	}
}

// ── PruneOldIncidents ─────────────────────────────────────────────────────────

func TestPruneOldIncidents(t *testing.T) {
	m, _ := newTestSLOMgr(t)

	// Add an incident 100 days ago (fully outside 90-day retention).
	old := time.Now().UTC().AddDate(0, 0, -100)
	oldEnd := old.Add(time.Hour)
	m.mu.Lock()
	m.incidents = append(m.incidents, IncidentRecord{
		TargetID:  "old-target",
		StartedAt: old,
		EndedAt:   &oldEnd,
	})
	// Add a recent incident.
	recent := time.Now().UTC().Add(-24 * time.Hour)
	recentEnd := recent.Add(time.Hour)
	m.incidents = append(m.incidents, IncidentRecord{
		TargetID:  "new-target",
		StartedAt: recent,
		EndedAt:   &recentEnd,
	})
	m.mu.Unlock()

	m.PruneOldIncidents(90)

	m.mu.Lock()
	count := len(m.incidents)
	m.mu.Unlock()
	if count != 1 {
		t.Errorf("after prune: want 1 incident, got %d", count)
	}
	if m.incidents[0].TargetID != "new-target" {
		t.Errorf("remaining incident should be new-target, got %q", m.incidents[0].TargetID)
	}
}

// ── BreachAlerted flag ────────────────────────────────────────────────────────

func TestBreachAlertedFlag(t *testing.T) {
	m, _ := newTestSLOMgr(t)

	if m.WasBreachAlerted("db") {
		t.Error("should not be alerted initially")
	}
	m.SetBreachAlerted("db", true)
	if !m.WasBreachAlerted("db") {
		t.Error("should be alerted after SetBreachAlerted(true)")
	}
	m.SetBreachAlerted("db", false)
	if m.WasBreachAlerted("db") {
		t.Error("should not be alerted after SetBreachAlerted(false)")
	}
}

// ── Engine.sloRecordStart / sloRecordEnd ─────────────────────────────────────

func TestEngine_SLORecordHooks_NilSafeWhenDisabled(t *testing.T) {
	targets := []Target{{ID: "db", Name: "DB", Type: "tcp", Target: "127.0.0.1:5432"}}
	e := newStandaloneEngine(targets, nil, nil)
	// sloMgr is nil — these must not panic.
	e.sloRecordStart(targets[0], "test error")
	e.sloRecordEnd(targets[0])
}

// ── SLOSnapshot — nil when disabled ──────────────────────────────────────────

func TestEngine_SLOSnapshot_NilWhenDisabled(t *testing.T) {
	e := newStandaloneEngine([]Target{
		{ID: "db", Name: "DB", Type: "tcp", Target: "127.0.0.1:5432"},
	}, nil, nil)

	if snap := e.SLOSnapshot(); snap != nil {
		t.Error("SLOSnapshot should return nil when SLO is not enabled")
	}
}
