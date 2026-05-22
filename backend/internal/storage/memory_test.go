package storage

import (
	"context"
	"errors"
	"testing"
	"time"
)

func testVer(seq uint64, node string) Version {
	return Version{Seq: seq, UpdatedAt: time.Now(), UpdatedBy: node}
}

func TestMemoryStorage_UpsertAndGet(t *testing.T) {
	m := NewMemoryStorage()
	defer m.Close()
	ctx := context.Background()

	err := m.Upsert(ctx, TableSLOTargets, "db-primary", []byte(`{"target_uptime":0.999}`), testVer(1, "node-1"))
	if err != nil {
		t.Fatalf("upsert: %v", err)
	}

	rec, err := m.Get(ctx, TableSLOTargets, "db-primary")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if rec.ID != "db-primary" {
		t.Errorf("ID mismatch: %q", rec.ID)
	}
	if string(rec.Payload) != `{"target_uptime":0.999}` {
		t.Errorf("payload mismatch: %q", string(rec.Payload))
	}
	if rec.Version.Seq != 1 {
		t.Errorf("Seq mismatch: %d", rec.Version.Seq)
	}
	if rec.Tombstone {
		t.Error("should not be tombstoned")
	}
}

func TestMemoryStorage_Get_NotFound(t *testing.T) {
	m := NewMemoryStorage()
	defer m.Close()
	_, err := m.Get(context.Background(), TableSLOTargets, "missing")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestMemoryStorage_Upsert_StaleRejected(t *testing.T) {
	m := NewMemoryStorage()
	defer m.Close()
	ctx := context.Background()

	if err := m.Upsert(ctx, TableSLOTargets, "x", []byte(`a`), testVer(5, "node-1")); err != nil {
		t.Fatalf("initial upsert: %v", err)
	}
	// Attempt to overwrite with lower Seq → must fail
	err := m.Upsert(ctx, TableSLOTargets, "x", []byte(`b`), testVer(4, "node-2"))
	if !errors.Is(err, ErrStaleWrite) {
		t.Fatalf("expected ErrStaleWrite, got %v", err)
	}
	// Verify payload was not modified
	rec, _ := m.Get(ctx, TableSLOTargets, "x")
	if string(rec.Payload) != "a" {
		t.Errorf("payload was overwritten by stale write: %q", string(rec.Payload))
	}
}

func TestMemoryStorage_Upsert_HigherSeqWins(t *testing.T) {
	m := NewMemoryStorage()
	defer m.Close()
	ctx := context.Background()

	_ = m.Upsert(ctx, TableSLOTargets, "x", []byte(`v1`), testVer(1, "node-1"))
	if err := m.Upsert(ctx, TableSLOTargets, "x", []byte(`v2`), testVer(2, "node-2")); err != nil {
		t.Fatalf("higher-seq upsert: %v", err)
	}
	rec, _ := m.Get(ctx, TableSLOTargets, "x")
	if string(rec.Payload) != "v2" {
		t.Errorf("higher seq did not win: %q", string(rec.Payload))
	}
}

func TestMemoryStorage_Delete_Tombstone(t *testing.T) {
	m := NewMemoryStorage()
	defer m.Close()
	ctx := context.Background()

	_ = m.Upsert(ctx, TableSLOTargets, "x", []byte(`v1`), testVer(1, "node-1"))
	if err := m.Delete(ctx, TableSLOTargets, "x", testVer(2, "node-1")); err != nil {
		t.Fatalf("delete: %v", err)
	}

	// Get must return ErrNotFound for tombstoned record
	_, err := m.Get(ctx, TableSLOTargets, "x")
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("expected ErrNotFound for tombstone, got %v", err)
	}

	// List with IncludeTombstones=true must surface it
	recs, _ := m.List(ctx, TableSLOTargets, Filter{IncludeTombstones: true})
	if len(recs) != 1 || !recs[0].Tombstone {
		t.Errorf("tombstone not returned with IncludeTombstones=true: %+v", recs)
	}
}

func TestMemoryStorage_Delete_StaleRejected(t *testing.T) {
	m := NewMemoryStorage()
	defer m.Close()
	ctx := context.Background()

	_ = m.Upsert(ctx, TableSLOTargets, "x", []byte(`v1`), testVer(5, "node-1"))
	err := m.Delete(ctx, TableSLOTargets, "x", testVer(3, "node-1"))
	if !errors.Is(err, ErrStaleWrite) {
		t.Errorf("expected ErrStaleWrite, got %v", err)
	}
}

func TestMemoryStorage_List_FilterByIDs(t *testing.T) {
	m := NewMemoryStorage()
	defer m.Close()
	ctx := context.Background()

	for i, id := range []string{"a", "b", "c", "d"} {
		_ = m.Upsert(ctx, TableSLOTargets, id, []byte(id), testVer(uint64(i+1), "node-1"))
	}
	recs, _ := m.List(ctx, TableSLOTargets, Filter{IDs: []string{"a", "c"}})
	if len(recs) != 2 {
		t.Fatalf("expected 2, got %d", len(recs))
	}
	if recs[0].ID != "a" || recs[1].ID != "c" {
		t.Errorf("wrong records returned: %v", recs)
	}
}

func TestMemoryStorage_List_SinceSeq(t *testing.T) {
	m := NewMemoryStorage()
	defer m.Close()
	ctx := context.Background()

	for i, id := range []string{"a", "b", "c"} {
		_ = m.Upsert(ctx, TableSLOTargets, id, []byte(id), testVer(uint64(i+1), "node-1"))
	}
	recs, _ := m.List(ctx, TableSLOTargets, Filter{SinceSeq: 2})
	if len(recs) != 2 {
		t.Fatalf("SinceSeq=2 should return seq>=2, got %d records", len(recs))
	}
}

func TestMemoryStorage_List_Limit(t *testing.T) {
	m := NewMemoryStorage()
	defer m.Close()
	ctx := context.Background()
	for i := 0; i < 10; i++ {
		_ = m.Upsert(ctx, TableSLOTargets, string(rune('a'+i)), []byte("v"), testVer(uint64(i+1), "n"))
	}
	recs, _ := m.List(ctx, TableSLOTargets, Filter{Limit: 3})
	if len(recs) != 3 {
		t.Errorf("expected 3, got %d", len(recs))
	}
}

func TestMemoryStorage_UnknownTable(t *testing.T) {
	m := NewMemoryStorage()
	defer m.Close()
	ctx := context.Background()
	err := m.Upsert(ctx, "not_a_table", "x", []byte(`v`), testVer(1, "n"))
	if !errors.Is(err, ErrTableNotKnown) {
		t.Errorf("expected ErrTableNotKnown, got %v", err)
	}
}

func TestMemoryStorage_EmptyID(t *testing.T) {
	m := NewMemoryStorage()
	defer m.Close()
	err := m.Upsert(context.Background(), TableSLOTargets, "", []byte(`v`), testVer(1, "n"))
	if err == nil {
		t.Error("expected error for empty ID")
	}
}

func TestMemoryStorage_Watch_EmitsUpsert(t *testing.T) {
	m := NewMemoryStorage()
	defer m.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ch, err := m.Watch(ctx, TableSLOTargets)
	if err != nil {
		t.Fatalf("watch: %v", err)
	}

	go func() {
		_ = m.Upsert(context.Background(), TableSLOTargets, "x", []byte(`v`), testVer(1, "n"))
	}()

	select {
	case evt := <-ch:
		if evt.Type != EventUpsert || evt.Record.ID != "x" {
			t.Errorf("unexpected event: %+v", evt)
		}
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for event")
	}
}

func TestMemoryStorage_Watch_EmitsDelete(t *testing.T) {
	m := NewMemoryStorage()
	defer m.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	_ = m.Upsert(ctx, TableSLOTargets, "x", []byte(`v`), testVer(1, "n"))

	ch, _ := m.Watch(ctx, TableSLOTargets)
	go func() {
		_ = m.Delete(context.Background(), TableSLOTargets, "x", testVer(2, "n"))
	}()

	select {
	case evt := <-ch:
		if evt.Type != EventDelete {
			t.Errorf("expected EventDelete, got %s", evt.Type)
		}
		if !evt.Record.Tombstone {
			t.Errorf("delete event should carry tombstone")
		}
	case <-time.After(time.Second):
		t.Fatal("timeout")
	}
}

func TestMemoryStorage_Watch_ClosedOnCancel(t *testing.T) {
	m := NewMemoryStorage()
	defer m.Close()
	ctx, cancel := context.WithCancel(context.Background())

	ch, _ := m.Watch(ctx, TableSLOTargets)
	cancel()

	// Channel must close within a reasonable time
	select {
	case _, ok := <-ch:
		if ok {
			// Drain — next read must be a close
			select {
			case _, ok2 := <-ch:
				if ok2 {
					t.Fatal("channel should close after ctx cancel")
				}
			case <-time.After(500 * time.Millisecond):
				t.Fatal("channel not closed after cancel")
			}
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("channel not closed after cancel")
	}
}

func TestMemoryStorage_DefensiveCopy(t *testing.T) {
	m := NewMemoryStorage()
	defer m.Close()
	ctx := context.Background()

	payload := []byte(`mutable`)
	_ = m.Upsert(ctx, TableSLOTargets, "x", payload, testVer(1, "n"))
	payload[0] = 'X' // mutate after upsert

	rec, _ := m.Get(ctx, TableSLOTargets, "x")
	if string(rec.Payload) != "mutable" {
		t.Errorf("payload was mutated externally: %q", string(rec.Payload))
	}

	rec.Payload[0] = 'Y' // mutate returned copy
	rec2, _ := m.Get(ctx, TableSLOTargets, "x")
	if string(rec2.Payload) != "mutable" {
		t.Errorf("returned payload mutation leaked back into store: %q", string(rec2.Payload))
	}
}

func TestMemoryStorage_Close(t *testing.T) {
	m := NewMemoryStorage()
	if err := m.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	// All ops should fail after Close
	err := m.Upsert(context.Background(), TableSLOTargets, "x", []byte(`v`), testVer(1, "n"))
	if err == nil {
		t.Error("expected error after Close")
	}
}

func TestMemoryStorage_ConcurrentWrites(t *testing.T) {
	m := NewMemoryStorage()
	defer m.Close()
	ctx := context.Background()

	// 10 goroutines each doing 50 writes on distinct keys
	done := make(chan bool, 10)
	for g := 0; g < 10; g++ {
		go func(g int) {
			for i := 0; i < 50; i++ {
				id := string(rune('A'+g)) + string(rune('0'+i%10))
				_ = m.Upsert(ctx, TableSLOTargets, id, []byte("v"), testVer(uint64(i+1), "n"))
			}
			done <- true
		}(g)
	}
	for i := 0; i < 10; i++ {
		<-done
	}
	recs, _ := m.List(ctx, TableSLOTargets, Filter{})
	if len(recs) < 100 {
		t.Errorf("expected many records, got %d", len(recs))
	}
}
