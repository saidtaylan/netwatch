// Package sqlite implements storage.StorageBackend over a per-node SQLite
// database file.
//
// This is the V1 production storage backend used by GossipLWWStorage
// (B21+). Without the gossip wrapper, this layer is fully usable for
// single-node deployments (cluster.enabled=false) and unit/integration
// tests.
//
// Concurrency:
//   - All writes serialize through a single transaction (busy_timeout
//     handles brief lock contention).
//   - Reads are concurrent (WAL mode).
//
// File format:
//   - One SQLite file per node, default at <data_dir>/netwatch.db.
//   - WAL journal mode for crash safety and reader/writer concurrency.
//   - foreign_keys = ON (we don't use FKs yet but keep the option).
package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"time"

	_ "modernc.org/sqlite" // pure-Go driver, CGO-free

	"github.com/saidtaylan/netwatch/internal/storage"
)

// allowedTables is the subset of storage.KnownTables() that has a
// corresponding CREATE TABLE in migrations/. Anything outside this set
// is rejected with ErrTableNotKnown so typos can't silently break.
var allowedTables = map[string]bool{
	storage.TableSLOTargets:    true,
	storage.TableApps:          true,
	storage.TableNotifChannels: true,
	storage.TableSilences:      true,
	storage.TableMaintenance:   true,
	storage.TableTargets:       true,
	storage.TableAlerts:        true,
	storage.TableAlertEvents:   true,
	storage.TableSLOIncidents:  true,
	storage.TableTargetStates:  true,
	storage.TableAuditLog:      true,
}

// Storage is the SQLite-backed implementation of storage.StorageBackend.
type Storage struct {
	db      *sql.DB
	path    string
	mu      sync.RWMutex            // protects watches map
	watches map[string][]chan storage.Event
	closed  bool
}

// Open creates or opens a SQLite database at the given path. The parent
// directory must exist; the file is created if missing. Migrations run
// automatically on every Open call (idempotent).
//
// Pass path = ":memory:" for an in-memory database (useful for tests).
func Open(path string) (*Storage, error) {
	if path == "" {
		return nil, fmt.Errorf("sqlite: empty path")
	}

	// Build connection DSN with safe defaults:
	//   _journal_mode=WAL    — better concurrency, crash-safe
	//   _busy_timeout=5000   — 5s lock wait before SQLITE_BUSY
	//   _foreign_keys=on     — enforce FK if any future migration adds them
	dsn := path
	if path != ":memory:" {
		// Use file: URI so we can append params
		abs, err := filepath.Abs(path)
		if err != nil {
			return nil, fmt.Errorf("resolve path: %w", err)
		}
		dsn = fmt.Sprintf("file:%s?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=foreign_keys(on)", abs)
	}

	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}

	// SQLite is single-writer; multiple connections fight for the lock
	// and waste resources. One connection serializes everything cleanly.
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	db.SetConnMaxLifetime(0)

	if err := db.Ping(); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("ping: %w", err)
	}

	if err := applyMigrations(db); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("migrations: %w", err)
	}

	return &Storage{
		db:      db,
		path:    path,
		watches: make(map[string][]chan storage.Event),
	}, nil
}

// Path returns the underlying database file path.
func (s *Storage) Path() string { return s.path }

func (s *Storage) checkOpen() error {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.closed {
		return fmt.Errorf("sqlite: closed")
	}
	return nil
}

func (s *Storage) checkTable(table string) error {
	if !allowedTables[table] {
		return fmt.Errorf("%w: %q", storage.ErrTableNotKnown, table)
	}
	return nil
}

// Upsert implements storage.StorageBackend.
func (s *Storage) Upsert(ctx context.Context, table, id string, payload []byte, ver storage.Version) error {
	if err := s.checkOpen(); err != nil {
		return err
	}
	if err := s.checkTable(table); err != nil {
		return err
	}
	if id == "" {
		return fmt.Errorf("sqlite: empty id")
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck

	// Fetch existing version for staleness check
	var existSeq sql.NullInt64
	var existAt sql.NullString
	var existBy sql.NullString
	row := tx.QueryRowContext(ctx,
		fmt.Sprintf(`SELECT seq, updated_at, updated_by FROM %s WHERE id = ?`, table),
		id,
	)
	switch err := row.Scan(&existSeq, &existAt, &existBy); {
	case errors.Is(err, sql.ErrNoRows):
		// fresh insert — no staleness check needed
	case err != nil:
		return fmt.Errorf("read existing: %w", err)
	default:
		// Compare versions
		existAtT, _ := time.Parse(time.RFC3339Nano, existAt.String)
		existing := storage.Version{
			Seq:       uint64(existSeq.Int64),
			UpdatedAt: existAtT,
			UpdatedBy: existBy.String,
		}
		if ver.Compare(existing) <= 0 {
			return storage.ErrStaleWrite
		}
	}

	// Upsert
	_, err = tx.ExecContext(ctx,
		fmt.Sprintf(`
			INSERT INTO %s (id, payload, seq, updated_at, updated_by, tombstone)
			VALUES (?, ?, ?, ?, ?, 0)
			ON CONFLICT(id) DO UPDATE SET
				payload = excluded.payload,
				seq = excluded.seq,
				updated_at = excluded.updated_at,
				updated_by = excluded.updated_by,
				tombstone = 0
		`, table),
		id, payload, ver.Seq, ver.UpdatedAt.UTC().Format(time.RFC3339Nano), ver.UpdatedBy,
	)
	if err != nil {
		return fmt.Errorf("upsert: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit: %w", err)
	}

	s.emit(table, storage.Event{
		Type:  storage.EventUpsert,
		Table: table,
		Record: storage.Record{
			ID:      id,
			Payload: append([]byte(nil), payload...),
			Version: ver,
		},
	})
	return nil
}

// Delete implements storage.StorageBackend (soft delete via tombstone).
func (s *Storage) Delete(ctx context.Context, table, id string, ver storage.Version) error {
	if err := s.checkOpen(); err != nil {
		return err
	}
	if err := s.checkTable(table); err != nil {
		return err
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck

	var existSeq sql.NullInt64
	var existAt sql.NullString
	var existBy sql.NullString
	row := tx.QueryRowContext(ctx,
		fmt.Sprintf(`SELECT seq, updated_at, updated_by FROM %s WHERE id = ?`, table),
		id,
	)
	switch err := row.Scan(&existSeq, &existAt, &existBy); {
	case errors.Is(err, sql.ErrNoRows):
		// Deleting non-existent record — write a tombstone anyway so peers
		// that later try to Upsert this id with a lower version get rejected.
	case err != nil:
		return fmt.Errorf("read existing: %w", err)
	default:
		existAtT, _ := time.Parse(time.RFC3339Nano, existAt.String)
		existing := storage.Version{
			Seq:       uint64(existSeq.Int64),
			UpdatedAt: existAtT,
			UpdatedBy: existBy.String,
		}
		if ver.Compare(existing) <= 0 {
			return storage.ErrStaleWrite
		}
	}

	_, err = tx.ExecContext(ctx,
		fmt.Sprintf(`
			INSERT INTO %s (id, payload, seq, updated_at, updated_by, tombstone)
			VALUES (?, '', ?, ?, ?, 1)
			ON CONFLICT(id) DO UPDATE SET
				payload = '',
				seq = excluded.seq,
				updated_at = excluded.updated_at,
				updated_by = excluded.updated_by,
				tombstone = 1
		`, table),
		id, ver.Seq, ver.UpdatedAt.UTC().Format(time.RFC3339Nano), ver.UpdatedBy,
	)
	if err != nil {
		return fmt.Errorf("delete (tombstone): %w", err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit: %w", err)
	}

	s.emit(table, storage.Event{
		Type:  storage.EventDelete,
		Table: table,
		Record: storage.Record{
			ID:        id,
			Version:   ver,
			Tombstone: true,
		},
	})
	return nil
}

// Get implements storage.StorageBackend.
func (s *Storage) Get(ctx context.Context, table, id string) (storage.Record, error) {
	if err := s.checkOpen(); err != nil {
		return storage.Record{}, err
	}
	if err := s.checkTable(table); err != nil {
		return storage.Record{}, err
	}

	row := s.db.QueryRowContext(ctx,
		fmt.Sprintf(`SELECT id, payload, seq, updated_at, updated_by, tombstone FROM %s WHERE id = ?`, table),
		id,
	)

	rec, err := scanRecord(row)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && rec.Tombstone) {
		return storage.Record{}, storage.ErrNotFound
	}
	if err != nil {
		return storage.Record{}, err
	}
	return rec, nil
}

// List implements storage.StorageBackend.
func (s *Storage) List(ctx context.Context, table string, filter storage.Filter) ([]storage.Record, error) {
	if err := s.checkOpen(); err != nil {
		return nil, err
	}
	if err := s.checkTable(table); err != nil {
		return nil, err
	}

	query := fmt.Sprintf(`SELECT id, payload, seq, updated_at, updated_by, tombstone FROM %s WHERE 1=1`, table)
	args := make([]any, 0, 4)

	if !filter.IncludeTombstones {
		query += ` AND tombstone = 0`
	}
	if filter.SinceSeq > 0 {
		query += ` AND seq >= ?`
		args = append(args, filter.SinceSeq)
	}
	if len(filter.IDs) > 0 {
		placeholders := make([]string, len(filter.IDs))
		for i, id := range filter.IDs {
			placeholders[i] = "?"
			args = append(args, id)
		}
		query += ` AND id IN (` + strings.Join(placeholders, ",") + `)`
	}
	query += ` ORDER BY id`
	if filter.Limit > 0 {
		query += ` LIMIT ?`
		args = append(args, filter.Limit)
	}

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("query: %w", err)
	}
	defer rows.Close()

	var out []storage.Record
	for rows.Next() {
		rec, err := scanRecord(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, rec)
	}
	return out, rows.Err()
}

// Watch implements storage.StorageBackend.
func (s *Storage) Watch(ctx context.Context, table string) (<-chan storage.Event, error) {
	if err := s.checkOpen(); err != nil {
		return nil, err
	}
	if err := s.checkTable(table); err != nil {
		return nil, err
	}

	ch := make(chan storage.Event, 16)
	s.mu.Lock()
	s.watches[table] = append(s.watches[table], ch)
	s.mu.Unlock()

	go func() {
		<-ctx.Done()
		s.mu.Lock()
		defer s.mu.Unlock()
		watchers := s.watches[table]
		for i, w := range watchers {
			if w == ch {
				s.watches[table] = append(watchers[:i], watchers[i+1:]...)
				close(ch)
				return
			}
		}
	}()
	return ch, nil
}

// Close implements storage.StorageBackend.
func (s *Storage) Close() error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil
	}
	s.closed = true
	for _, watchers := range s.watches {
		for _, ch := range watchers {
			close(ch)
		}
	}
	s.watches = nil
	s.mu.Unlock()
	return s.db.Close()
}

// emit broadcasts an event to all watchers for the table (non-blocking).
func (s *Storage) emit(table string, evt storage.Event) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, ch := range s.watches[table] {
		select {
		case ch <- evt:
		default:
			// drop — slow consumer
		}
	}
}

// scanRecord scans a row into a storage.Record. Works for both *sql.Row
// and *sql.Rows (both satisfy the rowScanner interface).
type rowScanner interface {
	Scan(dest ...any) error
}

func scanRecord(r rowScanner) (storage.Record, error) {
	var id string
	var payload []byte
	var seq int64
	var updatedAt string
	var updatedBy string
	var tomb int
	if err := r.Scan(&id, &payload, &seq, &updatedAt, &updatedBy, &tomb); err != nil {
		return storage.Record{}, err
	}
	at, _ := time.Parse(time.RFC3339Nano, updatedAt)
	return storage.Record{
		ID:        id,
		Payload:   payload,
		Version:   storage.Version{Seq: uint64(seq), UpdatedAt: at, UpdatedBy: updatedBy},
		Tombstone: tomb != 0,
	}, nil
}
