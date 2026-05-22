package engine

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/saidtaylan/netwatch/internal/storage"
	"github.com/saidtaylan/netwatch/internal/storage/gossip"
)

// makeAppsManager constructs an appsManager backed by in-memory storage.
// publishIndex calls are captured into *lastIdx for assertion by the test.
func makeAppsManager(t *testing.T, targetKeys []string, seed []App) (*appsManager, *capturedIndex) {
	t.Helper()
	mem := storage.NewMemoryStorage()
	t.Cleanup(func() { _ = mem.Close() })
	gs := gossip.NewStorage(mem, nil, nil, "test-node")

	cap := &capturedIndex{}
	tkFn := func() []string {
		if len(targetKeys) == 0 {
			return nil
		}
		return targetKeys
	}
	am, err := newAppsManager(context.Background(), gs, "test-node",
		seed, tkFn, cap.publish)
	if err != nil {
		t.Fatalf("newAppsManager: %v", err)
	}
	t.Cleanup(am.Close)
	return am, cap
}

type capturedIndex struct {
	mu  sync.Mutex
	idx AppTargetIndex
}

func (c *capturedIndex) publish(i AppTargetIndex) {
	c.mu.Lock()
	c.idx = i
	c.mu.Unlock()
}

func (c *capturedIndex) snapshot() AppTargetIndex {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.idx
}

func TestApps_SeedFromConfig(t *testing.T) {
	seed := []App{
		{Name: "payments", Uses: []string{"db-primary"}, OwnerTeam: "fintech"},
		{Name: "checkout", Uses: []string{"api-gateway"}, OwnerTeam: "shop"},
	}
	am, cap := makeAppsManager(t, []string{"db-primary", "api-gateway"}, seed)

	apps := am.Apps()
	if len(apps) != 2 {
		t.Fatalf("want 2 apps, got %d", len(apps))
	}
	idx := cap.snapshot()
	if len(idx["db-primary"]) != 1 || idx["db-primary"][0].Name != "payments" {
		t.Errorf("index missing payments for db-primary: %+v", idx)
	}
}

func TestApps_UpsertAndIndex(t *testing.T) {
	am, cap := makeAppsManager(t, []string{"db", "api"}, nil)

	if err := am.Upsert(App{Name: "svc1", Uses: []string{"db"}}); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	idx := cap.snapshot()
	if len(idx["db"]) != 1 {
		t.Errorf("expected 1 app for db, got %d", len(idx["db"]))
	}

	// Update — replace the same name with new Uses
	if err := am.Upsert(App{Name: "svc1", Uses: []string{"api"}}); err != nil {
		t.Fatalf("upsert update: %v", err)
	}
	idx = cap.snapshot()
	if len(idx["db"]) != 0 {
		t.Errorf("expected db cleared after Uses change, got %d", len(idx["db"]))
	}
	if len(idx["api"]) != 1 {
		t.Errorf("expected 1 app for api after Uses change, got %d", len(idx["api"]))
	}
}

func TestApps_Delete(t *testing.T) {
	am, cap := makeAppsManager(t, []string{"db"}, []App{
		{Name: "svc1", Uses: []string{"db"}},
	})
	if got := len(cap.snapshot()["db"]); got != 1 {
		t.Fatalf("setup: want 1 app for db, got %d", got)
	}

	ok, err := am.Delete("svc1")
	if err != nil {
		t.Fatalf("delete: %v", err)
	}
	if !ok {
		t.Error("expected Delete to return true for existing app")
	}
	if got := len(cap.snapshot()["db"]); got != 0 {
		t.Errorf("after delete: want 0 apps for db, got %d", got)
	}

	// Idempotent — second delete returns false but no error.
	ok, err = am.Delete("svc1")
	if err != nil {
		t.Fatalf("idempotent delete: %v", err)
	}
	if ok {
		t.Error("expected second Delete to return false")
	}
}

func TestApps_DanglingReferenceDropped(t *testing.T) {
	// db-primary exists, but the app references "missing-target" too.
	am, cap := makeAppsManager(t, []string{"db-primary"}, []App{
		{Name: "mixed", Uses: []string{"db-primary", "missing-target"}},
	})
	_ = am
	idx := cap.snapshot()
	if len(idx["missing-target"]) != 0 {
		t.Errorf("dangling reference 'missing-target' should be dropped: %+v", idx)
	}
	if len(idx["db-primary"]) != 1 {
		t.Errorf("valid reference 'db-primary' should be kept: %+v", idx)
	}
}

func TestApps_PersistAcrossInstances(t *testing.T) {
	mem := storage.NewMemoryStorage()
	defer mem.Close()
	gs := gossip.NewStorage(mem, nil, nil, "node-A")

	tkFn := func() []string { return []string{"db"} }
	pub1 := func(AppTargetIndex) {}
	am1, err := newAppsManager(context.Background(), gs, "node-A",
		nil, tkFn, pub1)
	if err != nil {
		t.Fatalf("first manager: %v", err)
	}
	if err := am1.Upsert(App{Name: "persisted", Uses: []string{"db"}}); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	am1.Close()

	cap := &capturedIndex{}
	am2, err := newAppsManager(context.Background(), gs, "node-A",
		nil, tkFn, cap.publish)
	if err != nil {
		t.Fatalf("second manager: %v", err)
	}
	defer am2.Close()
	apps := am2.Apps()
	if len(apps) != 1 || apps[0].Name != "persisted" {
		t.Errorf("second manager: want 1 persisted app, got %+v", apps)
	}
}

func TestApps_SplitBrainBlocksWrites(t *testing.T) {
	mem := storage.NewMemoryStorage()
	defer mem.Close()
	isolated := &flagChecker{value: true}
	gs := gossip.NewStorage(mem, isolated, nil, "node-1")

	cap := &capturedIndex{}
	am, err := newAppsManager(context.Background(), gs, "node-1",
		nil, func() []string { return nil }, cap.publish)
	if err != nil {
		t.Fatalf("manager: %v", err)
	}
	defer am.Close()

	err = am.Upsert(App{Name: "blocked", Uses: []string{"x"}})
	if !errors.Is(err, storage.ErrSplitBrain) {
		t.Errorf("expected ErrSplitBrain, got %v", err)
	}
}
