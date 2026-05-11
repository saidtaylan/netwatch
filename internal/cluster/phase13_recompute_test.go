package cluster

import (
	"reflect"
	"sort"
	"sync"
	"testing"
	"time"
)

// ── Test helpers ─────────────────────────────────────────────────────────────

// recordingListener captures Start/Stop callbacks for inspection.
type recordingListener struct {
	mu      sync.Mutex
	starts  []string
	stops   []string
	startCh chan string
	stopCh  chan string
}

func newRecordingListener() *recordingListener {
	return &recordingListener{
		startCh: make(chan string, 16),
		stopCh:  make(chan string, 16),
	}
}

func (l *recordingListener) StartProbing(targetID string) {
	l.mu.Lock()
	l.starts = append(l.starts, targetID)
	l.mu.Unlock()
	select {
	case l.startCh <- targetID:
	default:
	}
}

func (l *recordingListener) StopProbing(targetID string) {
	l.mu.Lock()
	l.stops = append(l.stops, targetID)
	l.mu.Unlock()
	select {
	case l.stopCh <- targetID:
	default:
	}
}

// drain empties a channel without blocking. Used between assertion phases to
// discard events that belong to an earlier recompute pass.
func drain(ch chan string) {
	for {
		select {
		case <-ch:
		default:
			return
		}
	}
}

func (l *recordingListener) snapshot() (starts, stops []string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	s1 := append([]string(nil), l.starts...)
	s2 := append([]string(nil), l.stops...)
	sort.Strings(s1)
	sort.Strings(s2)
	return s1, s2
}

// ── recomputeProberAssignments ───────────────────────────────────────────────

func TestRecompute_NoCallbacksWhenAssignmentsUnchanged(t *testing.T) {
	m := makeMgr("n1", "", 3)
	setAliveForTest(m, "n1", "n2", "n3")
	for _, n := range []string{"n1", "n2", "n3"} {
		seed(m, n, "t1")
	}
	m.SetLocalTargetProvider(stubProvider{ids: []string{"t1"}})
	listener := newRecordingListener()
	m.SetProberAssignmentListener(listener)

	// Initial recompute — should StartProbing for t1.
	m.recomputeProberAssignments()
	// Second recompute with identical state — should be silent.
	listener.mu.Lock()
	listener.starts = nil
	listener.stops = nil
	listener.mu.Unlock()
	m.recomputeProberAssignments()

	starts, stops := listener.snapshot()
	if len(starts) != 0 || len(stops) != 0 {
		t.Errorf("idempotent recompute should not callback; got starts=%v stops=%v",
			starts, stops)
	}
}

func TestRecompute_StartsOnTransitionToProber(t *testing.T) {
	// Begin with the local node NOT a prober (alive=others, no provider entry
	// for t1 → not in candidate set). Then add the local target to the
	// provider — recompute should fire StartProbing.
	m := makeMgr("n1", "", 1) // factor=1 so only one node probes
	setAliveForTest(m, "n1", "n2", "n3")
	for _, n := range []string{"n2", "n3"} {
		seed(m, n, "t1")
	}
	// Provider initially empty.
	m.SetLocalTargetProvider(stubProvider{ids: nil})
	listener := newRecordingListener()
	m.SetProberAssignmentListener(listener)
	m.recomputeProberAssignments()

	// Now register the local target.
	m.SetLocalTargetProvider(stubProvider{ids: []string{"t1"}})
	// Force the local node to win the hash by being the only candidate.
	setAliveForTest(m, "n1")
	m.recomputeProberAssignments()

	starts, stops := listener.snapshot()
	if !reflect.DeepEqual(starts, []string{"t1"}) {
		t.Errorf("expected StartProbing(t1), got starts=%v", starts)
	}
	if len(stops) != 0 {
		t.Errorf("no stops expected, got %v", stops)
	}
}

func TestRecompute_StopsWhenTargetLeavesLocalConfig(t *testing.T) {
	m := makeMgr("n1", "", 3)
	setAliveForTest(m, "n1")
	m.SetLocalTargetProvider(stubProvider{ids: []string{"t1", "t2"}})
	listener := newRecordingListener()
	m.SetProberAssignmentListener(listener)
	m.recomputeProberAssignments() // seed: starts t1 + t2

	listener.mu.Lock()
	listener.starts = nil
	listener.mu.Unlock()

	// Remove t2 from local config.
	m.SetLocalTargetProvider(stubProvider{ids: []string{"t1"}})
	m.recomputeProberAssignments()

	_, stops := listener.snapshot()
	if !reflect.DeepEqual(stops, []string{"t2"}) {
		t.Errorf("expected StopProbing(t2), got %v", stops)
	}
}

func TestRecompute_StopBeforeStartOnSwap(t *testing.T) {
	// Verify ordering: stops come before starts within one recompute pass.
	// Build a state where t-old is assigned and t-new is not, then flip them.
	m := makeMgr("n1", "", 3)
	setAliveForTest(m, "n1")
	m.SetLocalTargetProvider(stubProvider{ids: []string{"t-old"}})
	listener := newRecordingListener()
	m.SetProberAssignmentListener(listener)
	m.recomputeProberAssignments() // start t-old

	// Drain residual events from the seeding pass so the assertions below
	// only observe the swap recompute.
	listener.mu.Lock()
	listener.starts = nil
	listener.mu.Unlock()
	drain(listener.startCh)
	drain(listener.stopCh)

	// Swap inventory.
	m.SetLocalTargetProvider(stubProvider{ids: []string{"t-new"}})
	m.recomputeProberAssignments()

	// We cannot easily assert ordering with a slice-only API; use the channels.
	var first, second string
	select {
	case first = <-listener.stopCh:
	case <-time.After(time.Second):
		t.Fatal("expected a StopProbing event")
	}
	select {
	case second = <-listener.startCh:
	case <-time.After(time.Second):
		t.Fatal("expected a StartProbing event after the stop")
	}
	if first != "t-old" || second != "t-new" {
		t.Errorf("expected stop(t-old) then start(t-new); got stop=%q start=%q",
			first, second)
	}
}

func TestRecompute_NoListenerNoPanic(t *testing.T) {
	m := makeMgr("n1", "", 3)
	setAliveForTest(m, "n1")
	m.SetLocalTargetProvider(stubProvider{ids: []string{"t1"}})
	// No listener registered — recompute must be a silent no-op.
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("recompute panicked without listener: %v", r)
		}
	}()
	m.recomputeProberAssignments()
}

func TestRecompute_NoProviderNoPanic(t *testing.T) {
	m := makeMgr("n1", "", 3)
	listener := newRecordingListener()
	m.SetProberAssignmentListener(listener)
	// No provider — recompute returns without firing callbacks.
	m.recomputeProberAssignments()
	starts, stops := listener.snapshot()
	if len(starts) != 0 || len(stops) != 0 {
		t.Errorf("no provider should yield no callbacks, got %v %v", starts, stops)
	}
}

// ── SeedProberAssignments ────────────────────────────────────────────────────

func TestSeed_SuppressesRedundantStartsOnFirstRecompute(t *testing.T) {
	// Simulate Engine.Init flow: seed assignments to match what we just
	// launched, then run recompute — listener must NOT see any callback.
	m := makeMgr("n1", "", 3)
	setAliveForTest(m, "n1")
	m.SetLocalTargetProvider(stubProvider{ids: []string{"t1", "t2"}})

	// Seed: both targets are already running.
	m.SeedProberAssignments(map[string]bool{
		"t1": true,
		"t2": true,
	})

	listener := newRecordingListener()
	m.SetProberAssignmentListener(listener)

	m.recomputeProberAssignments()

	starts, stops := listener.snapshot()
	if len(starts) != 0 || len(stops) != 0 {
		t.Errorf("seeded state should suppress all callbacks; starts=%v stops=%v",
			starts, stops)
	}
}

func TestSeed_DoesNotShareInputSlice(t *testing.T) {
	// Defensive copy: mutating the caller's map must not affect the manager.
	m := makeMgr("n1", "", 3)
	src := map[string]bool{"t1": true}
	m.SeedProberAssignments(src)
	src["t1"] = false

	listener := newRecordingListener()
	m.SetLocalTargetProvider(stubProvider{ids: []string{"t1"}})
	m.SetProberAssignmentListener(listener)
	setAliveForTest(m, "n1")
	m.recomputeProberAssignments()

	starts, _ := listener.snapshot()
	if len(starts) != 0 {
		t.Errorf("internal state was mutated by caller; starts=%v", starts)
	}
}

// ── scheduleRecompute debounce ──────────────────────────────────────────────

func TestScheduleRecompute_BurstCollapsesToOne(t *testing.T) {
	// Hammer scheduleRecompute many times — only one recompute should fire
	// after the debounce window. We can't wait the full 5s in a unit test,
	// so we override the timer state directly.
	m := makeMgr("n1", "", 3)
	setAliveForTest(m, "n1")
	m.SetLocalTargetProvider(stubProvider{ids: []string{"t1"}})
	listener := newRecordingListener()
	m.SetProberAssignmentListener(listener)

	// Pre-seed so a recompute that does fire produces a callback we can count.
	m.SeedProberAssignments(nil)

	// Schedule 50 times in quick succession.
	for i := 0; i < 50; i++ {
		m.scheduleRecompute()
	}

	// Force the timer to fire immediately by re-arming with a near-zero delay.
	m.recomputeMu.Lock()
	if m.recomputeTimer != nil {
		m.recomputeTimer.Stop()
	}
	m.recomputeTimer = time.AfterFunc(time.Millisecond, m.recomputeProberAssignments)
	m.recomputeMu.Unlock()

	// Wait briefly for the timer.
	select {
	case <-listener.startCh:
		// good — first callback
	case <-time.After(time.Second):
		t.Fatal("recompute did not fire within 1s")
	}

	// No additional callbacks should arrive after another short wait.
	time.Sleep(50 * time.Millisecond)
	starts, _ := listener.snapshot()
	if len(starts) != 1 {
		t.Errorf("debounce should collapse 50 schedules to 1 recompute; got %d starts: %v",
			len(starts), starts)
	}
}
