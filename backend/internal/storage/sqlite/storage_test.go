package sqlite

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/saidtaylan/netwatch/internal/storage"
)

// testDB opens a fresh on-disk DB for one test. We use file-based (not
// :memory:) to verify the WAL pragmas and real persistence path. Each
// test gets its own temp directory which is cleaned automatically.
func testDB(t *testing.T) *Storage {
	t.Helper()
	dir := t.TempDir()
	s, err := Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func ver(seq uint64, node string) storage.Version {
	return storage.Version{Seq: seq, UpdatedAt: time.Now().UTC(), UpdatedBy: node}
}

func TestOpen_MissingPath(t *testing.T) {
	_, err := Open("")
	if err == nil {
		t.Fatal("expected error for empty path")
	}
}

func TestOpen_MigrationsApplied(t *testing.T) {
	s := testDB(t)
	// All known tables should now exist — try an Upsert to one of each.
	ctx := context.Background()
	for _, tbl := range []string{
		storage.TableSLOTargets, storage.TableApps, storage.TableTargets,
		storage.TableAlerts, storage.TableMaintenance, storage.TableAuditLog,
	} {
		err := s.Upsert(ctx, tbl, "probe", []byte(`{}`), ver(1, "n"))
		if err != nil {
			t.Errorf("table %q should exist (upsert failed: %v)", tbl, err)
		}
	}
}

func TestUpsert_AndGet(t *testing.T) {
	s := testDB(t)
	ctx := context.Background()

	err := s.Upsert(ctx, storage.TableSLOTargets, "db-primary",
		[]byte(`{"target_uptime":0.999}`), ver(1, "node-1"))
	if err != nil {
		t.Fatalf("upsert: %v", err)
	}

	rec, err := s.Get(ctx, storage.TableSLOTargets, "db-primary")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if rec.ID != "db-primary" {
		t.Errorf("ID mismatch")
	}
	if string(rec.Payload) != `{"target_uptime":0.999}` {
		t.Errorf("payload mismatch: %q", string(rec.Payload))
	}
	if rec.Version.Seq != 1 {
		t.Errorf("seq mismatch: %d", rec.Version.Seq)
	}
	if rec.Version.UpdatedBy != "node-1" {
		t.Errorf("updatedBy mismatch: %q", rec.Version.UpdatedBy)
	}
}

func TestGet_NotFound(t *testing.T) {
	s := testDB(t)
	_, err := s.Get(context.Background(), storage.TableSLOTargets, "missing")
	if !errors.Is(err, storage.ErrNotFound) {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

func TestUpsert_StaleRejected(t *testing.T) {
	s := testDB(t)
	ctx := context.Background()

	_ = s.Upsert(ctx, storage.TableSLOTargets, "x", []byte(`a`), ver(5, "node-1"))

	err := s.Upsert(ctx, storage.TableSLOTargets, "x", []byte(`b`), ver(4, "node-2"))
	if !errors.Is(err, storage.ErrStaleWrite) {
		t.Fatalf("expected ErrStaleWrite, got %v", err)
	}
	// Verify payload not overwritten
	rec, _ := s.Get(ctx, storage.TableSLOTargets, "x")
	if string(rec.Payload) != "a" {
		t.Errorf("stale write modified payload: %q", string(rec.Payload))
	}
}

func TestUpsert_HigherSeqWins(t *testing.T) {
	s := testDB(t)
	ctx := context.Background()

	_ = s.Upsert(ctx, storage.TableSLOTargets, "x", []byte(`v1`), ver(1, "node-1"))
	_ = s.Upsert(ctx, storage.TableSLOTargets, "x", []byte(`v2`), ver(2, "node-2"))

	rec, _ := s.Get(ctx, storage.TableSLOTargets, "x")
	if string(rec.Payload) != "v2" {
		t.Errorf("higher seq did not win: %q", string(rec.Payload))
	}
}

func TestDelete_Tombstone(t *testing.T) {
	s := testDB(t)
	ctx := context.Background()

	_ = s.Upsert(ctx, storage.TableSLOTargets, "x", []byte(`v`), ver(1, "n"))
	if err := s.Delete(ctx, storage.TableSLOTargets, "x", ver(2, "n")); err != nil {
		t.Fatalf("delete: %v", err)
	}

	_, err := s.Get(ctx, storage.TableSLOTargets, "x")
	if !errors.Is(err, storage.ErrNotFound) {
		t.Errorf("tombstoned record should ErrNotFound, got %v", err)
	}

	// IncludeTombstones=true should surface it
	recs, _ := s.List(ctx, storage.TableSLOTargets,
		storage.Filter{IncludeTombstones: true})
	if len(recs) != 1 || !recs[0].Tombstone {
		t.Errorf("tombstone not surfaced: %+v", recs)
	}
}

func TestDelete_NonExistent_WritesTombstone(t *testing.T) {
	s := testDB(t)
	ctx := context.Background()

	// Delete on missing record should still write a tombstone so future
	// stale upserts from peers get rejected.
	if err := s.Delete(ctx, storage.TableSLOTargets, "ghost", ver(1, "n")); err != nil {
		t.Fatalf("delete missing: %v", err)
	}
	recs, _ := s.List(ctx, storage.TableSLOTargets,
		storage.Filter{IncludeTombstones: true})
	if len(recs) != 1 {
		t.Errorf("expected tombstone written, got %d records", len(recs))
	}
}

func TestList_FilterByIDs(t *testing.T) {
	s := testDB(t)
	ctx := context.Background()
	for i, id := range []string{"a", "b", "c", "d"} {
		_ = s.Upsert(ctx, storage.TableSLOTargets, id, []byte(id), ver(uint64(i+1), "n"))
	}
	recs, _ := s.List(ctx, storage.TableSLOTargets,
		storage.Filter{IDs: []string{"b", "d"}})
	if len(recs) != 2 {
		t.Fatalf("expected 2, got %d", len(recs))
	}
	if recs[0].ID != "b" || recs[1].ID != "d" {
		t.Errorf("wrong IDs returned: %v", recs)
	}
}

func TestList_SinceSeq(t *testing.T) {
	s := testDB(t)
	ctx := context.Background()
	for i, id := range []string{"a", "b", "c"} {
		_ = s.Upsert(ctx, storage.TableSLOTargets, id, []byte(id), ver(uint64(i+1), "n"))
	}
	recs, _ := s.List(ctx, storage.TableSLOTargets,
		storage.Filter{SinceSeq: 2})
	if len(recs) != 2 {
		t.Errorf("SinceSeq=2 → expected 2 records, got %d", len(recs))
	}
}

func TestList_Limit(t *testing.T) {
	s := testDB(t)
	ctx := context.Background()
	for i := 0; i < 10; i++ {
		_ = s.Upsert(ctx, storage.TableSLOTargets, string(rune('a'+i)),
			[]byte("v"), ver(uint64(i+1), "n"))
	}
	recs, _ := s.List(ctx, storage.TableSLOTargets, storage.Filter{Limit: 3})
	if len(recs) != 3 {
		t.Errorf("expected 3, got %d", len(recs))
	}
}

func TestList_ExcludesTombstones(t *testing.T) {
	s := testDB(t)
	ctx := context.Background()
	_ = s.Upsert(ctx, storage.TableSLOTargets, "a", []byte(`a`), ver(1, "n"))
	_ = s.Upsert(ctx, storage.TableSLOTargets, "b", []byte(`b`), ver(1, "n"))
	_ = s.Delete(ctx, storage.TableSLOTargets, "a", ver(2, "n"))

	recs, _ := s.List(ctx, storage.TableSLOTargets, storage.Filter{})
	if len(recs) != 1 || recs[0].ID != "b" {
		t.Errorf("tombstone leaked into default list: %+v", recs)
	}
}

func TestUnknownTable(t *testing.T) {
	s := testDB(t)
	err := s.Upsert(context.Background(), "fake_table", "x", []byte(`v`), ver(1, "n"))
	if !errors.Is(err, storage.ErrTableNotKnown) {
		t.Errorf("expected ErrTableNotKnown, got %v", err)
	}
}

func TestPersistence_Reopen(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "persist.db")
	s, err := Open(path)
	if err != nil {
		t.Fatalf("first open: %v", err)
	}

	_ = s.Upsert(context.Background(), storage.TableSLOTargets, "persist-me",
		[]byte(`payload`), ver(7, "node-1"))
	_ = s.Close()

	// Re-open and verify data survives
	s2, err := Open(path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer s2.Close()

	rec, err := s2.Get(context.Background(), storage.TableSLOTargets, "persist-me")
	if err != nil {
		t.Fatalf("get after reopen: %v", err)
	}
	if string(rec.Payload) != "payload" {
		t.Errorf("payload not persisted: %q", string(rec.Payload))
	}
	if rec.Version.Seq != 7 {
		t.Errorf("seq not persisted: %d", rec.Version.Seq)
	}

	// Migrations should be idempotent — schema_migrations table prevents re-apply
}

func TestMigrations_Idempotent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "idem.db")
	s, err := Open(path)
	if err != nil {
		t.Fatalf("first open: %v", err)
	}
	_ = s.Close()

	// Reopen — applyMigrations runs again but should skip already-applied files
	s2, err := Open(path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer s2.Close()

	// Sanity — table still usable
	if err := s2.Upsert(context.Background(), storage.TableSLOTargets, "x",
		[]byte(`v`), ver(1, "n")); err != nil {
		t.Errorf("upsert after re-migration: %v", err)
	}
}

func TestWatch_EmitsUpsert(t *testing.T) {
	s := testDB(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ch, err := s.Watch(ctx, storage.TableSLOTargets)
	if err != nil {
		t.Fatalf("watch: %v", err)
	}

	go func() {
		_ = s.Upsert(context.Background(), storage.TableSLOTargets, "x",
			[]byte(`v`), ver(1, "n"))
	}()

	select {
	case evt := <-ch:
		if evt.Type != storage.EventUpsert || evt.Record.ID != "x" {
			t.Errorf("bad event: %+v", evt)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timeout")
	}
}

func TestWatch_EmitsDelete(t *testing.T) {
	s := testDB(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	_ = s.Upsert(ctx, storage.TableSLOTargets, "x", []byte(`v`), ver(1, "n"))
	ch, _ := s.Watch(ctx, storage.TableSLOTargets)
	go func() {
		_ = s.Delete(context.Background(), storage.TableSLOTargets, "x", ver(2, "n"))
	}()

	select {
	case evt := <-ch:
		if evt.Type != storage.EventDelete {
			t.Errorf("expected EventDelete, got %s", evt.Type)
		}
		if !evt.Record.Tombstone {
			t.Errorf("delete event must carry tombstone")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timeout")
	}
}

func TestClose_BlocksFurtherWrites(t *testing.T) {
	dir := t.TempDir()
	s, _ := Open(filepath.Join(dir, "close.db"))
	_ = s.Close()
	err := s.Upsert(context.Background(), storage.TableSLOTargets, "x",
		[]byte(`v`), ver(1, "n"))
	if err == nil {
		t.Error("expected error after close")
	}
}

func TestConcurrentWrites(t *testing.T) {
	s := testDB(t)
	ctx := context.Background()

	done := make(chan bool, 5)
	for g := 0; g < 5; g++ {
		go func(g int) {
			for i := 0; i < 20; i++ {
				id := string(rune('A'+g)) + string(rune('0'+i%10))
				_ = s.Upsert(ctx, storage.TableSLOTargets, id,
					[]byte("v"), ver(uint64(i+1), "n"))
			}
			done <- true
		}(g)
	}
	for i := 0; i < 5; i++ {
		<-done
	}
	recs, _ := s.List(ctx, storage.TableSLOTargets, storage.Filter{})
	if len(recs) < 30 {
		t.Errorf("expected many concurrent writes to succeed, got %d", len(recs))
	}
}

// Interface compliance check — caught at compile time.
var _ storage.StorageBackend = (*Storage)(nil)
