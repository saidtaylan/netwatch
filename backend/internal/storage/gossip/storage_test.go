package gossip

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/saidtaylan/netwatch/internal/storage"
)

// ── Test doubles ────────────────────────────────────────────────────────────

type fakeIsolated struct {
	mu       sync.RWMutex
	isolated bool
}

func (f *fakeIsolated) IsolatedMode() bool {
	f.mu.RLock()
	defer f.mu.RUnlock()
	return f.isolated
}

func (f *fakeIsolated) set(v bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.isolated = v
}

type recordingBroadcaster struct {
	mu      sync.Mutex
	changes []StorageChange
	fail    bool
}

func (r *recordingBroadcaster) BroadcastStorageChange(c StorageChange) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.fail {
		return errors.New("broadcast failed")
	}
	r.changes = append(r.changes, c)
	return nil
}

func (r *recordingBroadcaster) list() []StorageChange {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]StorageChange, len(r.changes))
	copy(out, r.changes)
	return out
}

// ── Constructor + defaults ──────────────────────────────────────────────────

func TestNewStorage_NilDefaults(t *testing.T) {
	mem := storage.NewMemoryStorage()
	defer mem.Close()
	s := NewStorage(mem, nil, nil, "node-1")
	// nil isolated → AlwaysHealthy, nil broadcaster → NoopBroadcaster
	if s.isolated.IsolatedMode() {
		t.Error("nil isolated should default to AlwaysHealthy (never isolated)")
	}
	// NoopBroadcaster doesn't error
	err := s.broadcaster.BroadcastStorageChange(StorageChange{})
	if err != nil {
		t.Errorf("noop broadcaster should not error: %v", err)
	}
}

func TestNextVersion_Monotonic(t *testing.T) {
	mem := storage.NewMemoryStorage()
	defer mem.Close()
	s := NewStorage(mem, nil, nil, "node-1")

	v1 := s.NextVersion()
	v2 := s.NextVersion()
	v3 := s.NextVersion()
	if v1.Seq != 1 || v2.Seq != 2 || v3.Seq != 3 {
		t.Errorf("Seq not monotonic: %d, %d, %d", v1.Seq, v2.Seq, v3.Seq)
	}
	if v2.Compare(v1) != 1 {
		t.Error("v2 must beat v1")
	}
	if v1.UpdatedBy != "node-1" {
		t.Errorf("UpdatedBy: %q", v1.UpdatedBy)
	}
}

// ── Write broadcast ─────────────────────────────────────────────────────────

func TestUpsert_BroadcastsChange(t *testing.T) {
	mem := storage.NewMemoryStorage()
	defer mem.Close()
	rb := &recordingBroadcaster{}
	s := NewStorage(mem, nil, rb, "node-1")

	v := s.NextVersion()
	err := s.Upsert(context.Background(), storage.TableSLOTargets, "x", []byte(`v1`), v)
	if err != nil {
		t.Fatalf("upsert: %v", err)
	}

	changes := rb.list()
	if len(changes) != 1 {
		t.Fatalf("expected 1 broadcast, got %d", len(changes))
	}
	c := changes[0]
	if c.Table != storage.TableSLOTargets || c.ID != "x" {
		t.Errorf("wrong fields: %+v", c)
	}
	if c.Tombstone {
		t.Error("upsert broadcast should not be tombstone")
	}
	if string(c.Payload) != "v1" {
		t.Errorf("payload mismatch: %q", string(c.Payload))
	}
}

func TestDelete_BroadcastsTombstone(t *testing.T) {
	mem := storage.NewMemoryStorage()
	defer mem.Close()
	rb := &recordingBroadcaster{}
	s := NewStorage(mem, nil, rb, "node-1")
	ctx := context.Background()

	_ = s.Upsert(ctx, storage.TableSLOTargets, "x", []byte(`v`), s.NextVersion())
	_ = s.Delete(ctx, storage.TableSLOTargets, "x", s.NextVersion())

	changes := rb.list()
	if len(changes) != 2 {
		t.Fatalf("expected 2 broadcasts (upsert+delete), got %d", len(changes))
	}
	if !changes[1].Tombstone {
		t.Error("delete broadcast should have Tombstone=true")
	}
}

func TestUpsert_BroadcastFailureDoesNotFailWrite(t *testing.T) {
	mem := storage.NewMemoryStorage()
	defer mem.Close()
	rb := &recordingBroadcaster{fail: true}
	s := NewStorage(mem, nil, rb, "node-1")

	err := s.Upsert(context.Background(), storage.TableSLOTargets, "x",
		[]byte(`v`), s.NextVersion())
	if err != nil {
		t.Errorf("broadcast failure should NOT fail local write: %v", err)
	}

	// Local write should still be readable
	rec, err := s.Get(context.Background(), storage.TableSLOTargets, "x")
	if err != nil {
		t.Errorf("get after broadcast-fail upsert: %v", err)
	}
	if rec.ID != "x" {
		t.Error("local write should have succeeded")
	}
}

// ── IsolatedMode guard ──────────────────────────────────────────────────────

func TestUpsert_RejectedDuringIsolatedMode(t *testing.T) {
	mem := storage.NewMemoryStorage()
	defer mem.Close()
	fi := &fakeIsolated{isolated: true}
	rb := &recordingBroadcaster{}
	s := NewStorage(mem, fi, rb, "node-1")

	err := s.Upsert(context.Background(), storage.TableSLOTargets, "x",
		[]byte(`v`), s.NextVersion())
	if !errors.Is(err, storage.ErrSplitBrain) {
		t.Errorf("expected ErrSplitBrain, got %v", err)
	}
	if len(rb.list()) > 0 {
		t.Error("isolated mode should not produce broadcasts")
	}

	// Underlying record should not exist
	_, err = mem.Get(context.Background(), storage.TableSLOTargets, "x")
	if !errors.Is(err, storage.ErrNotFound) {
		t.Error("isolated-mode rejected write should not commit")
	}
}

func TestDelete_RejectedDuringIsolatedMode(t *testing.T) {
	mem := storage.NewMemoryStorage()
	defer mem.Close()
	fi := &fakeIsolated{}
	s := NewStorage(mem, fi, nil, "node-1")
	ctx := context.Background()

	// Pre-populate
	_ = s.Upsert(ctx, storage.TableSLOTargets, "x", []byte(`v`), s.NextVersion())

	// Now enter isolated mode
	fi.set(true)
	err := s.Delete(ctx, storage.TableSLOTargets, "x", s.NextVersion())
	if !errors.Is(err, storage.ErrSplitBrain) {
		t.Errorf("expected ErrSplitBrain, got %v", err)
	}

	// Record should still be readable (delete didn't go through)
	if _, err := s.Get(ctx, storage.TableSLOTargets, "x"); err != nil {
		t.Errorf("record should still exist: %v", err)
	}
}

func TestReadsWorkDuringIsolatedMode(t *testing.T) {
	mem := storage.NewMemoryStorage()
	defer mem.Close()
	fi := &fakeIsolated{}
	s := NewStorage(mem, fi, nil, "node-1")
	ctx := context.Background()

	// Pre-populate
	_ = s.Upsert(ctx, storage.TableSLOTargets, "x", []byte(`v`), s.NextVersion())

	fi.set(true) // enter isolated mode

	// Get and List must still work
	if _, err := s.Get(ctx, storage.TableSLOTargets, "x"); err != nil {
		t.Errorf("Get in isolated mode: %v", err)
	}
	if _, err := s.List(ctx, storage.TableSLOTargets, storage.Filter{}); err != nil {
		t.Errorf("List in isolated mode: %v", err)
	}
}

func TestIsolatedMode_AutoRecovery(t *testing.T) {
	mem := storage.NewMemoryStorage()
	defer mem.Close()
	fi := &fakeIsolated{isolated: true}
	s := NewStorage(mem, fi, nil, "node-1")
	ctx := context.Background()

	// Write rejected while isolated
	err := s.Upsert(ctx, storage.TableSLOTargets, "x", []byte(`v`), s.NextVersion())
	if !errors.Is(err, storage.ErrSplitBrain) {
		t.Error("expected ErrSplitBrain initially")
	}

	// Quorum recovers
	fi.set(false)
	err = s.Upsert(ctx, storage.TableSLOTargets, "x", []byte(`v`), s.NextVersion())
	if err != nil {
		t.Errorf("write should succeed after quorum recovery: %v", err)
	}
}

// ── ApplyRemoteChange ───────────────────────────────────────────────────────

func TestApplyRemoteChange_Upsert(t *testing.T) {
	mem := storage.NewMemoryStorage()
	defer mem.Close()
	rb := &recordingBroadcaster{}
	s := NewStorage(mem, nil, rb, "node-1")
	ctx := context.Background()

	remoteVer := storage.Version{Seq: 5, UpdatedAt: time.Now().UTC(), UpdatedBy: "node-remote"}
	change := StorageChange{
		Table:   storage.TableSLOTargets,
		ID:      "remote-id",
		Payload: []byte(`remote payload`),
		Version: remoteVer,
	}

	if err := s.ApplyRemoteChange(ctx, change); err != nil {
		t.Fatalf("apply: %v", err)
	}

	rec, err := s.Get(ctx, storage.TableSLOTargets, "remote-id")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if string(rec.Payload) != "remote payload" {
		t.Errorf("payload mismatch: %q", string(rec.Payload))
	}
	if rec.Version.UpdatedBy != "node-remote" {
		t.Errorf("remote node attribution lost: %q", rec.Version.UpdatedBy)
	}

	// Critical: remote apply must NOT trigger our own broadcast (loop)
	if len(rb.list()) > 0 {
		t.Error("ApplyRemoteChange must not re-broadcast (infinite loop)")
	}
}

func TestApplyRemoteChange_Delete(t *testing.T) {
	mem := storage.NewMemoryStorage()
	defer mem.Close()
	s := NewStorage(mem, nil, nil, "node-1")
	ctx := context.Background()

	// Pre-populate
	_ = s.Upsert(ctx, storage.TableSLOTargets, "x", []byte(`v`), s.NextVersion())

	// Remote delete with higher seq
	remoteVer := storage.Version{Seq: 100, UpdatedAt: time.Now().UTC(), UpdatedBy: "node-remote"}
	if err := s.ApplyRemoteChange(ctx, StorageChange{
		Table: storage.TableSLOTargets, ID: "x",
		Version: remoteVer, Tombstone: true,
	}); err != nil {
		t.Fatalf("apply delete: %v", err)
	}

	_, err := s.Get(ctx, storage.TableSLOTargets, "x")
	if !errors.Is(err, storage.ErrNotFound) {
		t.Errorf("remote delete should tombstone: %v", err)
	}
}

func TestApplyRemoteChange_StaleDropped(t *testing.T) {
	mem := storage.NewMemoryStorage()
	defer mem.Close()
	s := NewStorage(mem, nil, nil, "node-1")
	ctx := context.Background()

	// Local write with high seq
	highVer := storage.Version{Seq: 100, UpdatedAt: time.Now().UTC(), UpdatedBy: "node-1"}
	_ = s.Upsert(ctx, storage.TableSLOTargets, "x", []byte(`local`), highVer)

	// Remote stale change
	staleVer := storage.Version{Seq: 5, UpdatedAt: time.Now().UTC(), UpdatedBy: "node-remote"}
	err := s.ApplyRemoteChange(ctx, StorageChange{
		Table: storage.TableSLOTargets, ID: "x",
		Payload: []byte(`old`), Version: staleVer,
	})
	// ApplyRemoteChange must NOT return error for stale (benign — local is newer)
	if err != nil {
		t.Errorf("stale remote should be dropped silently, got %v", err)
	}

	// Local copy must remain
	rec, _ := s.Get(ctx, storage.TableSLOTargets, "x")
	if string(rec.Payload) != "local" {
		t.Errorf("local copy overwritten by stale remote: %q", string(rec.Payload))
	}

	// Stats should show 1 rejected
	stats := s.Stats()
	if stats.TotalRejected != 1 {
		t.Errorf("expected 1 rejected, got %d", stats.TotalRejected)
	}
	if stats.TotalReceived != 1 {
		t.Errorf("expected 1 received, got %d", stats.TotalReceived)
	}
}

func TestApplyRemoteChange_DoesNotCheckIsolatedMode(t *testing.T) {
	// Critical: receiving peer broadcasts must work even when this node
	// is in isolated mode (otherwise we'd reject reconciliation traffic).
	mem := storage.NewMemoryStorage()
	defer mem.Close()
	fi := &fakeIsolated{isolated: true}
	s := NewStorage(mem, fi, nil, "node-1")
	ctx := context.Background()

	remoteVer := storage.Version{Seq: 1, UpdatedAt: time.Now().UTC(), UpdatedBy: "peer"}
	err := s.ApplyRemoteChange(ctx, StorageChange{
		Table: storage.TableSLOTargets, ID: "x",
		Payload: []byte(`v`), Version: remoteVer,
	})
	if err != nil {
		t.Errorf("ApplyRemoteChange must work in isolated mode: %v", err)
	}

	// Verify it was actually applied
	if _, err := s.Get(ctx, storage.TableSLOTargets, "x"); err != nil {
		t.Error("remote change should apply even when local is isolated")
	}
}

// ── Seq counter behaviors ───────────────────────────────────────────────────

func TestObserveSeq_FromRemote(t *testing.T) {
	// When we receive a remote write with high Seq, our local
	// NextVersion() should produce something even higher — so the next
	// local write definitively beats anything in cluster history.
	mem := storage.NewMemoryStorage()
	defer mem.Close()
	s := NewStorage(mem, nil, nil, "node-1")

	remoteVer := storage.Version{Seq: 42, UpdatedAt: time.Now().UTC(), UpdatedBy: "node-remote"}
	_ = s.ApplyRemoteChange(context.Background(), StorageChange{
		Table: storage.TableSLOTargets, ID: "x",
		Payload: []byte(`v`), Version: remoteVer,
	})

	// Our next local Seq should be > 42
	v := s.NextVersion()
	if v.Seq <= 42 {
		t.Errorf("local Seq did not advance past remote: got %d", v.Seq)
	}
}

// ── Interface compliance ────────────────────────────────────────────────────

func TestSatisfiesInterface(t *testing.T) {
	var _ storage.StorageBackend = (*Storage)(nil)
}
