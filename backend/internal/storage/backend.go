package storage

import (
	"context"
	"errors"
)

// Standard storage errors. Backends should return one of these (or wrap
// them with %w) so that callers can branch on errors.Is(...).
var (
	// ErrNotFound is returned by Get when no record matches the given ID.
	ErrNotFound = errors.New("storage: record not found")

	// ErrStaleWrite is returned by Upsert/Delete when the supplied Version
	// is older than (or equal to) the version currently stored. The caller
	// is expected to fetch the latest version and retry if needed.
	ErrStaleWrite = errors.New("storage: stale write (version conflict)")

	// ErrSplitBrain is returned by Upsert/Delete on a node that has lost
	// quorum (IsolatedMode active). Writes are rejected to prevent data
	// divergence; reads remain available. HTTP layer translates this to
	// 503 Service Unavailable with a Retry-After hint.
	ErrSplitBrain = errors.New("storage: cluster in isolated mode, writes paused")

	// ErrTableNotKnown is returned when an unknown table name is used.
	// Backends declare known tables at construction time.
	ErrTableNotKnown = errors.New("storage: unknown table")
)

// StorageBackend is the persistence abstraction for netwatch's dynamic
// state. Implementations:
//
//   - MemoryStorage   — testing only (no persistence, no replication)
//   - GossipLWWStorage — V1 production (SQLite + gossip broadcast + LWW)
//   - RaftStorage     — V2.0 future (hashicorp/raft + bbolt)
//
// All methods are safe for concurrent use across goroutines.
type StorageBackend interface {
	// Upsert inserts or updates a record. The Version must be derived via
	// NextVersion (or assigned by the gossip broadcast layer when applying
	// a remote change).
	//
	// If the existing record's Version is newer than the supplied Version
	// (per Version.Compare), Upsert returns ErrStaleWrite and does NOT
	// modify the stored record. Callers should fetch the current Version
	// and retry only when appropriate.
	//
	// Side effect: emits an EventUpsert on all active Watch channels for
	// this table, AFTER the local commit succeeds.
	Upsert(ctx context.Context, table, id string, payload []byte, ver Version) error

	// Delete performs a soft-delete by writing a Tombstone record with the
	// given Version. The actual row is retained (with Tombstone=true) so
	// that anti-entropy can propagate the deletion to peers that missed it.
	//
	// Same staleness check as Upsert.
	//
	// Side effect: emits an EventDelete.
	Delete(ctx context.Context, table, id string, ver Version) error

	// Get returns the record for the given ID, or ErrNotFound if missing or
	// tombstoned. To inspect a tombstone explicitly, use List with
	// Filter{IDs: []string{id}, IncludeTombstones: true}.
	Get(ctx context.Context, table, id string) (Record, error)

	// List returns records matching the filter. Tombstones are excluded by
	// default; set Filter.IncludeTombstones=true to include them
	// (used by anti-entropy sync).
	List(ctx context.Context, table string, filter Filter) ([]Record, error)

	// Watch returns a channel that receives Events for the given table.
	// The channel is closed when ctx is cancelled.
	//
	// Watch is best-effort: under heavy load, slow consumers may miss
	// events. Consumers needing strong consistency should use a periodic
	// List call instead (the anti-entropy sync layer does exactly that).
	Watch(ctx context.Context, table string) (<-chan Event, error)

	// Close releases all resources held by the backend (database
	// connections, goroutines, etc.). After Close, all methods return
	// an error.
	Close() error
}

// KnownTables is the registry of all logical tables backed by the storage
// layer. Backends should reject operations on unknown tables.
//
// New tables added here MUST also be reflected in any SQL migration files
// produced by the SQLite backend (B19).
const (
	TableSLOTargets         = "slo_targets"
	TableApps               = "apps"
	TableNotifChannels      = "notification_channels"
	TableSilences           = "silences"             // B1, B24
	TableMaintenance        = "maintenance_windows"  // migrates from maintenance.json
	TableTargets            = "targets"              // B24 — migrates from config.yaml targets
	TableAlerts             = "alerts"               // B25
	TableAlertEvents        = "alert_events"         // B25
	TableSLOIncidents       = "slo_incidents"        // migrates from incidents.json
	TableTargetStates       = "target_states"        // migrates from state.json (B20)
	TableAuditLog           = "audit_log"            // B25, local-only
	TableUsers              = "users"                // B28 — user accounts
	TableFrontendSettings   = "frontend_settings"    // B28 — frontend config (cluster nodes etc.)
)

// KnownTables returns the full set of tables managed by the storage
// layer. Used by anti-entropy to iterate sync per-table.
func KnownTables() []string {
	return []string{
		TableSLOTargets,
		TableApps,
		TableNotifChannels,
		TableSilences,
		TableMaintenance,
		TableTargets,
		TableAlerts,
		TableAlertEvents,
		TableSLOIncidents,
		TableTargetStates,
		TableAuditLog,
		TableUsers,
		TableFrontendSettings,
	}
}
