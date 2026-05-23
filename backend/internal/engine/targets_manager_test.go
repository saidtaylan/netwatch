package engine

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/saidtaylan/netwatch/internal/storage"
	"github.com/saidtaylan/netwatch/internal/storage/gossip"
)

// recordingReconciler captures every reconcileFn invocation so tests can
// assert that the right target list reached the engine callback.
type recordingReconciler struct {
	mu    sync.Mutex
	calls [][]Target
	err   error // returned from reconcileFn
}

func (r *recordingReconciler) fn(targets []Target) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	cp := append([]Target(nil), targets...)
	r.calls = append(r.calls, cp)
	return r.err
}

func (r *recordingReconciler) lastCall() []Target {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.calls) == 0 {
		return nil
	}
	return r.calls[len(r.calls)-1]
}

func (r *recordingReconciler) callCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.calls)
}

func makeTargetsManager(t *testing.T, seed []Target) (*targetsManager, *recordingReconciler) {
	t.Helper()
	mem := storage.NewMemoryStorage()
	t.Cleanup(func() { _ = mem.Close() })
	gs := gossip.NewStorage(mem, nil, nil, "test-node")
	rec := &recordingReconciler{}
	tm, err := newTargetsManager(context.Background(), gs, "test-node", seed, rec.fn)
	if err != nil {
		t.Fatalf("newTargetsManager: %v", err)
	}
	t.Cleanup(tm.Close)
	return tm, rec
}

func TestTargets_SeedFromConfig(t *testing.T) {
	seed := []Target{
		{ID: "db-primary", Type: "tcp", Target: "127.0.0.1:5432", Name: "db"},
		{ID: "api", Type: "http", Target: "https://example.com", Name: "api"},
	}
	tm, rec := makeTargetsManager(t, seed)

	got := tm.Targets()
	if len(got) != 2 {
		t.Fatalf("want 2 seeded targets, got %d", len(got))
	}
	// Seed must NOT trigger reconcileFn — the engine is already
	// initialised with these exact targets at boot.
	if n := rec.callCount(); n != 0 {
		t.Errorf("seed should not call reconcileFn, got %d calls", n)
	}
}

func TestTargets_UpsertTriggersReconcile(t *testing.T) {
	tm, rec := makeTargetsManager(t, nil)

	err := tm.Upsert(Target{ID: "new", Type: "tcp", Target: "127.0.0.1:1", Name: "new"})
	if err != nil {
		t.Fatalf("upsert: %v", err)
	}

	if rec.callCount() != 1 {
		t.Errorf("want 1 reconcile call, got %d", rec.callCount())
	}
	got := rec.lastCall()
	if len(got) != 1 || got[0].ID != "new" {
		t.Errorf("reconcile got unexpected targets: %+v", got)
	}
}

func TestTargets_UpsertSurfacesReconcileError(t *testing.T) {
	mem := storage.NewMemoryStorage()
	defer mem.Close()
	gs := gossip.NewStorage(mem, nil, nil, "test-node")
	rec := &recordingReconciler{err: errors.New("invalid")}
	tm, _ := newTargetsManager(context.Background(), gs, "test-node", nil, rec.fn)
	defer tm.Close()

	err := tm.Upsert(Target{ID: "x", Type: "tcp", Target: "y", Name: "x"})
	if err == nil || err.Error() != "invalid" {
		t.Errorf("expected reconcile error to propagate, got %v", err)
	}
}

func TestTargets_DeleteTriggersReconcile(t *testing.T) {
	seed := []Target{
		{ID: "keep", Type: "tcp", Target: "x", Name: "keep"},
		{ID: "drop", Type: "tcp", Target: "y", Name: "drop"},
	}
	tm, rec := makeTargetsManager(t, seed)

	ok, err := tm.Delete("drop")
	if err != nil {
		t.Fatalf("delete: %v", err)
	}
	if !ok {
		t.Error("expected Delete to return true for existing target")
	}
	if rec.callCount() != 1 {
		t.Errorf("want 1 reconcile call, got %d", rec.callCount())
	}
	got := rec.lastCall()
	if len(got) != 1 || got[0].ID != "keep" {
		t.Errorf("reconcile after delete: expected only 'keep', got %+v", got)
	}
}

func TestTargets_DeleteIdempotent(t *testing.T) {
	tm, _ := makeTargetsManager(t, nil)
	ok, err := tm.Delete("missing")
	if err != nil {
		t.Fatalf("delete: %v", err)
	}
	if ok {
		t.Error("expected Delete on missing to return false")
	}
}

func TestTargets_OrderingDeterministic(t *testing.T) {
	tm, _ := makeTargetsManager(t, nil)
	// Insert in a non-sorted order
	for _, id := range []string{"zeta", "alpha", "mu"} {
		_ = tm.Upsert(Target{ID: id, Type: "tcp", Target: "x", Name: id})
	}
	got := tm.Targets()
	if len(got) != 3 {
		t.Fatalf("want 3 targets, got %d", len(got))
	}
	// Should come back sorted by key for prober-assignment determinism.
	expected := []string{"alpha", "mu", "zeta"}
	for i, want := range expected {
		if got[i].ID != want {
			t.Errorf("Targets[%d]: want %q got %q", i, want, got[i].ID)
		}
	}
}

func TestTargets_PersistAcrossInstances(t *testing.T) {
	mem := storage.NewMemoryStorage()
	defer mem.Close()
	gs := gossip.NewStorage(mem, nil, nil, "node-A")

	tm1, err := newTargetsManager(context.Background(), gs, "node-A", nil, nil)
	if err != nil {
		t.Fatalf("first: %v", err)
	}
	if err := tm1.Upsert(Target{ID: "persisted", Type: "tcp", Target: "x", Name: "p"}); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	tm1.Close()

	rec := &recordingReconciler{}
	tm2, err := newTargetsManager(context.Background(), gs, "node-A", nil, rec.fn)
	if err != nil {
		t.Fatalf("second: %v", err)
	}
	defer tm2.Close()
	got := tm2.Targets()
	if len(got) != 1 || got[0].ID != "persisted" {
		t.Errorf("second manager should load persisted target, got %+v", got)
	}
}

func TestTargets_SplitBrainBlocksWrites(t *testing.T) {
	mem := storage.NewMemoryStorage()
	defer mem.Close()
	isolated := &flagChecker{value: true}
	gs := gossip.NewStorage(mem, isolated, nil, "node-1")

	rec := &recordingReconciler{}
	tm, err := newTargetsManager(context.Background(), gs, "node-1", nil, rec.fn)
	if err != nil {
		t.Fatalf("manager: %v", err)
	}
	defer tm.Close()

	err = tm.Upsert(Target{ID: "blocked", Type: "tcp", Target: "x", Name: "b"})
	if !errors.Is(err, storage.ErrSplitBrain) {
		t.Errorf("expected ErrSplitBrain, got %v", err)
	}
	if rec.callCount() != 0 {
		t.Error("reconcile should not run when storage rejected the write")
	}
}

func TestTargets_EmptyKeyRejected(t *testing.T) {
	tm, _ := makeTargetsManager(t, nil)
	err := tm.Upsert(Target{Type: "tcp", Target: "x"}) // no ID and no Name
	if err == nil {
		t.Error("expected empty key to be rejected")
	}
}

func TestTargets_Get(t *testing.T) {
	tm, _ := makeTargetsManager(t, []Target{
		{ID: "found", Type: "tcp", Target: "x", Name: "f"},
	})
	if got, ok := tm.Get("found"); !ok || got.ID != "found" {
		t.Errorf("Get(found): want hit, got %+v ok=%v", got, ok)
	}
	if _, ok := tm.Get("missing"); ok {
		t.Error("Get(missing): want miss")
	}
}

// sameTargetSet is tested via the public helper.
func TestSameTargetSet(t *testing.T) {
	a := []Target{
		{ID: "x", Type: "tcp", Target: "1", Name: "x"},
		{ID: "y", Type: "tcp", Target: "2", Name: "y"},
	}
	b := []Target{
		{ID: "y", Type: "tcp", Target: "2", Name: "y"},
		{ID: "x", Type: "tcp", Target: "1", Name: "x"},
	}
	if !sameTargetSet(a, b) {
		t.Error("same content, different order should be equal")
	}
	c := []Target{
		{ID: "x", Type: "tcp", Target: "1", Name: "x"},
		{ID: "y", Type: "tcp", Target: "DIFFERENT", Name: "y"},
	}
	if sameTargetSet(a, c) {
		t.Error("different payload should be unequal")
	}
	d := append([]Target(nil), a...)
	d = append(d, Target{ID: "extra", Type: "tcp", Target: "3", Name: "extra"})
	if sameTargetSet(a, d) {
		t.Error("different sizes should be unequal")
	}
}
