package storage

import (
	"context"
	"fmt"
	"sort"
	"sync"
)

// MemoryStorage is an in-memory StorageBackend. Not durable, not replicated.
// Intended for unit tests and as the reference implementation that locks down
// the semantics of the interface (Compare order, tombstone handling,
// staleness rejection, Watch emission).
//
// All operations are O(n) over the table. That's fine for tests; the SQLite
// backend (B19) has proper indexes for production use.
type MemoryStorage struct {
	mu      sync.RWMutex
	tables  map[string]map[string]Record // table → id → record
	watches map[string][]chan Event      // table → list of watcher channels
	closed  bool
}

// NewMemoryStorage creates an empty in-memory backend. The set of known
// tables is fixed by KnownTables() — operations on other table names will
// return ErrTableNotKnown.
func NewMemoryStorage() *MemoryStorage {
	m := &MemoryStorage{
		tables:  make(map[string]map[string]Record),
		watches: make(map[string][]chan Event),
	}
	for _, t := range KnownTables() {
		m.tables[t] = make(map[string]Record)
	}
	return m
}

// known returns true if the table is registered.
// Caller must hold mu (read or write).
func (m *MemoryStorage) known(table string) bool {
	_, ok := m.tables[table]
	return ok
}

// checkOpen returns an error if the in-memory store has been closed, so
// operations after Close fail rather than silently succeed.
func (m *MemoryStorage) checkOpen() error {
	if m.closed {
		return fmt.Errorf("storage: closed")
	}
	return nil
}

// Upsert implements StorageBackend.
func (m *MemoryStorage) Upsert(_ context.Context, table, id string, payload []byte, ver Version) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.checkOpen(); err != nil {
		return err
	}
	if !m.known(table) {
		return fmt.Errorf("%w: %q", ErrTableNotKnown, table)
	}
	if id == "" {
		return fmt.Errorf("storage: empty id")
	}

	rec := Record{
		ID:        id,
		Payload:   append([]byte(nil), payload...),
		Version:   ver,
		Tombstone: false,
	}

	// Staleness check
	if existing, ok := m.tables[table][id]; ok {
		if ver.Compare(existing.Version) <= 0 {
			return ErrStaleWrite
		}
	}

	m.tables[table][id] = rec
	m.emit(table, Event{Type: EventUpsert, Table: table, Record: rec})
	return nil
}

// Delete implements StorageBackend (soft delete via tombstone).
func (m *MemoryStorage) Delete(_ context.Context, table, id string, ver Version) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.checkOpen(); err != nil {
		return err
	}
	if !m.known(table) {
		return fmt.Errorf("%w: %q", ErrTableNotKnown, table)
	}

	if existing, ok := m.tables[table][id]; ok {
		if ver.Compare(existing.Version) <= 0 {
			return ErrStaleWrite
		}
	}

	tomb := Record{
		ID:        id,
		Payload:   nil,
		Version:   ver,
		Tombstone: true,
	}
	m.tables[table][id] = tomb
	m.emit(table, Event{Type: EventDelete, Table: table, Record: tomb})
	return nil
}

// Get implements StorageBackend.
func (m *MemoryStorage) Get(_ context.Context, table, id string) (Record, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if err := m.checkOpen(); err != nil {
		return Record{}, err
	}
	if !m.known(table) {
		return Record{}, fmt.Errorf("%w: %q", ErrTableNotKnown, table)
	}
	rec, ok := m.tables[table][id]
	if !ok || rec.Tombstone {
		return Record{}, ErrNotFound
	}
	// Defensive copy of payload
	out := rec
	out.Payload = append([]byte(nil), rec.Payload...)
	return out, nil
}

// List implements StorageBackend.
func (m *MemoryStorage) List(_ context.Context, table string, filter Filter) ([]Record, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if err := m.checkOpen(); err != nil {
		return nil, err
	}
	if !m.known(table) {
		return nil, fmt.Errorf("%w: %q", ErrTableNotKnown, table)
	}

	out := make([]Record, 0, len(m.tables[table]))
	idSet := map[string]bool{}
	for _, id := range filter.IDs {
		idSet[id] = true
	}

	for _, rec := range m.tables[table] {
		if len(filter.IDs) > 0 && !idSet[rec.ID] {
			continue
		}
		if rec.Tombstone && !filter.IncludeTombstones {
			continue
		}
		if rec.Version.Seq < filter.SinceSeq {
			continue
		}
		// Defensive copy of payload
		r := rec
		r.Payload = append([]byte(nil), rec.Payload...)
		out = append(out, r)
	}

	// Stable sort by ID for deterministic output
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })

	if filter.Limit > 0 && len(out) > filter.Limit {
		out = out[:filter.Limit]
	}
	return out, nil
}

// Watch implements StorageBackend.
func (m *MemoryStorage) Watch(ctx context.Context, table string) (<-chan Event, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.checkOpen(); err != nil {
		return nil, err
	}
	if !m.known(table) {
		return nil, fmt.Errorf("%w: %q", ErrTableNotKnown, table)
	}

	ch := make(chan Event, 16)
	m.watches[table] = append(m.watches[table], ch)

	// Tear down watcher when ctx is cancelled
	go func() {
		<-ctx.Done()
		m.mu.Lock()
		defer m.mu.Unlock()
		watchers := m.watches[table]
		for i, w := range watchers {
			if w == ch {
				m.watches[table] = append(watchers[:i], watchers[i+1:]...)
				close(ch)
				return
			}
		}
	}()
	return ch, nil
}

// emit broadcasts an event to all watchers for the table.
// Caller must hold mu (write lock).
//
// Non-blocking: drops events for slow consumers rather than blocking
// the writer. This matches the "best-effort" contract documented on
// StorageBackend.Watch.
func (m *MemoryStorage) emit(table string, evt Event) {
	for _, ch := range m.watches[table] {
		select {
		case ch <- evt:
		default:
			// drop — slow consumer
		}
	}
}

// Close implements StorageBackend.
func (m *MemoryStorage) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return nil
	}
	m.closed = true
	for _, watchers := range m.watches {
		for _, ch := range watchers {
			close(ch)
		}
	}
	m.watches = nil
	return nil
}
