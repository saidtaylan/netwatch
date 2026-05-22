package engine

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/saidtaylan/netwatch/internal/storage"
	"github.com/saidtaylan/netwatch/internal/storage/gossip"
)

// makeManager constructs a maintenance manager backed by an in-memory
// storage (no cluster, no replication). Cleanup is registered via t.Cleanup.
func makeManager(t *testing.T) *maintenanceManager {
	t.Helper()
	mem := storage.NewMemoryStorage()
	t.Cleanup(func() { _ = mem.Close() })

	gs := gossip.NewStorage(mem, nil, nil, "test-node")
	mm, err := newMaintenanceManager(context.Background(), gs)
	if err != nil {
		t.Fatalf("newMaintenanceManager: %v", err)
	}
	t.Cleanup(mm.Close)
	return mm
}

func TestMaintenance_SetAndIsInMaintenance(t *testing.T) {
	mm := makeManager(t)

	w := MaintenanceWindow{
		ID:        "mw-test-1",
		TargetIDs: []string{"db-primary"},
		StartedAt: time.Now(),
		ExpiresAt: time.Now().Add(time.Hour),
		Reason:    "DB upgrade",
	}
	if err := mm.Set(w); err != nil {
		t.Fatalf("Set: %v", err)
	}
	if !mm.IsInMaintenance("db-primary") {
		t.Error("db-primary should be in maintenance")
	}
	if mm.IsInMaintenance("api-gateway") {
		t.Error("api-gateway should NOT be in maintenance")
	}
}

func TestMaintenance_Cancel(t *testing.T) {
	mm := makeManager(t)
	w := MaintenanceWindow{
		ID:        "mw-cancel-1",
		TargetIDs: []string{"db-primary"},
		ExpiresAt: time.Now().Add(time.Hour),
	}
	_ = mm.Set(w)
	if !mm.IsInMaintenance("db-primary") {
		t.Fatal("setup precondition failed")
	}

	if err := mm.Cancel("mw-cancel-1"); err != nil {
		t.Fatalf("Cancel: %v", err)
	}
	if mm.IsInMaintenance("db-primary") {
		t.Error("after cancel should NOT be in maintenance")
	}
}

func TestMaintenance_Cancel_MissingID(t *testing.T) {
	mm := makeManager(t)
	if err := mm.Cancel("nonexistent"); err != nil {
		t.Errorf("missing ID should not error, got %v", err)
	}
}

func TestMaintenance_List(t *testing.T) {
	mm := makeManager(t)
	for i, id := range []string{"a", "b", "c"} {
		_ = mm.Set(MaintenanceWindow{
			ID:        id,
			TargetIDs: []string{"t" + id},
			StartedAt: time.Now(),
			ExpiresAt: time.Now().Add(time.Duration(i+1) * time.Hour),
		})
	}
	got := mm.List()
	if len(got) != 3 {
		t.Errorf("expected 3, got %d", len(got))
	}
}

func TestMaintenance_List_ExcludesExpired(t *testing.T) {
	mm := makeManager(t)
	// Add an already-expired window
	_ = mm.Set(MaintenanceWindow{
		ID:        "expired",
		TargetIDs: []string{"t1"},
		ExpiresAt: time.Now().Add(-time.Hour),
	})
	// Add an active one
	_ = mm.Set(MaintenanceWindow{
		ID:        "active",
		TargetIDs: []string{"t2"},
		ExpiresAt: time.Now().Add(time.Hour),
	})
	got := mm.List()
	if len(got) != 1 || got[0].ID != "active" {
		t.Errorf("expected only the active window, got %+v", got)
	}
}

func TestMaintenance_PruneExpired(t *testing.T) {
	mm := makeManager(t)
	_ = mm.Set(MaintenanceWindow{
		ID:        "expired",
		TargetIDs: []string{"t1"},
		ExpiresAt: time.Now().Add(-time.Hour),
	})
	_ = mm.Set(MaintenanceWindow{
		ID:        "active",
		TargetIDs: []string{"t2"},
		ExpiresAt: time.Now().Add(time.Hour),
	})

	mm.PruneExpired()

	// Verify cache only has active
	mm.mu.RLock()
	count := len(mm.windows)
	mm.mu.RUnlock()
	if count != 1 {
		t.Errorf("expected 1 active window after prune, got %d", count)
	}

	// Verify storage has tombstone for expired (so peers won't resurrect)
	recs, err := mm.storage.List(context.Background(),
		storage.TableMaintenance, storage.Filter{IncludeTombstones: true})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	var foundTombstone bool
	for _, r := range recs {
		if r.ID == "expired" && r.Tombstone {
			foundTombstone = true
		}
	}
	if !foundTombstone {
		t.Error("expired window should leave a tombstone in storage")
	}
}

func TestMaintenance_PersistAcrossInstances(t *testing.T) {
	// Construct first manager, write windows, close it.
	mem := storage.NewMemoryStorage()
	defer mem.Close()
	gs := gossip.NewStorage(mem, nil, nil, "node-A")

	mm1, err := newMaintenanceManager(context.Background(), gs)
	if err != nil {
		t.Fatalf("first manager: %v", err)
	}
	_ = mm1.Set(MaintenanceWindow{
		ID:        "persist-1",
		TargetIDs: []string{"t1"},
		ExpiresAt: time.Now().Add(time.Hour),
		Reason:    "persisted",
	})
	mm1.Close()

	// Second manager on the SAME storage — should see the window.
	mm2, err := newMaintenanceManager(context.Background(), gs)
	if err != nil {
		t.Fatalf("second manager: %v", err)
	}
	defer mm2.Close()
	if !mm2.IsInMaintenance("t1") {
		t.Error("second manager should see persisted window in storage")
	}
}

func TestMaintenance_SplitBrainBlocksWrites(t *testing.T) {
	// Create an isolated checker we can flip
	mem := storage.NewMemoryStorage()
	defer mem.Close()
	isolated := &flagChecker{value: true}
	gs := gossip.NewStorage(mem, isolated, nil, "node-1")

	mm, err := newMaintenanceManager(context.Background(), gs)
	if err != nil {
		t.Fatalf("manager: %v", err)
	}
	defer mm.Close()

	err = mm.Set(MaintenanceWindow{
		ID:        "blocked",
		TargetIDs: []string{"t"},
		ExpiresAt: time.Now().Add(time.Hour),
	})
	if !errors.Is(err, storage.ErrSplitBrain) {
		t.Errorf("expected ErrSplitBrain, got %v", err)
	}
}

// flagChecker is a trivial IsolatedModeChecker for tests.
type flagChecker struct{ value bool }

func (f *flagChecker) IsolatedMode() bool { return f.value }
