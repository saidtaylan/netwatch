// Package gossip wraps an inner storage.StorageBackend with cluster-aware
// behaviors:
//
//  1. **Write replication** — every successful local Upsert/Delete is
//     broadcast to peers via the ChangeBroadcaster (best-effort UDP).
//     Peers receive the broadcast and apply it via ApplyRemoteChange,
//     which goes through the same inner backend (LWW conflict resolution
//     in storage.Version.Compare handles staleness automatically).
//
//  2. **Split-brain write guard** — when the cluster has lost quorum
//     (IsolatedModeChecker.IsolatedMode() returns true), writes are
//     rejected with storage.ErrSplitBrain. Reads continue to work.
//     This is the pragmatic alternative to Raft consensus: instead of
//     leader election, we use the existing IsolatedMode signal to prevent
//     minority partitions from accepting writes that would later conflict.
//
// Cluster integration:
//   - Cluster.Manager satisfies IsolatedModeChecker (already has IsolatedMode()).
//   - Cluster.Manager will satisfy ChangeBroadcaster after B23 (separate
//     wire). For now in B21+B22, callers can pass a NoopBroadcaster to
//     run without cluster integration (e.g. single-node deployments).
//
// This package has NO dependency on internal/cluster — interfaces only.
// Cycle-free.
package gossip

import (
	"context"
	"log/slog"
	"sync/atomic"
	"time"

	"github.com/saidtaylan/netwatch/internal/storage"
)

// IsolatedModeChecker is satisfied by cluster.Manager. When the cluster
// has lost quorum (minority partition), writes are rejected to avoid
// data loss when partitions re-merge.
//
// For single-node deployments or tests, AlwaysHealthy{} or a noop returns
// false unconditionally.
type IsolatedModeChecker interface {
	IsolatedMode() bool
}

// AlwaysHealthy is a no-op IsolatedModeChecker that never reports isolated
// mode. Useful for tests and single-node deployments.
type AlwaysHealthy struct{}

// IsolatedMode implements IsolatedModeChecker.
func (AlwaysHealthy) IsolatedMode() bool { return false }

// ChangeBroadcaster sends a storage change to other cluster nodes.
// Implemented by cluster.Manager (to be wired in B23).
//
// The Broadcast call is fire-and-forget; the backend caller does not
// wait for peers to acknowledge. Anti-entropy push-pull (B23) catches
// any messages dropped by UDP.
type ChangeBroadcaster interface {
	BroadcastStorageChange(change StorageChange) error
}

// NoopBroadcaster discards all broadcasts. Used for single-node
// deployments and tests where cluster integration is not desired.
type NoopBroadcaster struct{}

// BroadcastStorageChange implements ChangeBroadcaster (no-op).
func (NoopBroadcaster) BroadcastStorageChange(StorageChange) error { return nil }

// StorageChange is the wire format for one storage mutation. Marshalled
// as JSON by cluster.Manager when sent over the memberlist broadcast queue.
//
// Tombstone=true means this is a delete (Payload is empty).
type StorageChange struct {
	MsgType   string          `json:"msg_type"` // always "storage_change"
	Table     string          `json:"table"`
	ID        string          `json:"id"`
	Payload   []byte          `json:"payload,omitempty"`
	Version   storage.Version `json:"version"`
	Tombstone bool            `json:"tombstone,omitempty"`
}

// Storage is a StorageBackend that wraps another backend with gossip
// broadcast + IsolatedMode write guard.
//
// All read methods (Get, List, Watch) pass through unchanged — reads
// always work, even in isolated mode.
//
// Writes (Upsert, Delete) first check IsolatedMode, then commit locally,
// then broadcast. If broadcast fails (network down), the local write
// still succeeds; anti-entropy will reconcile later.
type Storage struct {
	// inner is the underlying durable backend (e.g. SQLite).
	inner storage.StorageBackend

	// isolated lets us reject writes during split-brain. nil → always healthy.
	isolated IsolatedModeChecker

	// broadcaster ships changes to peers. nil → no replication.
	broadcaster ChangeBroadcaster

	// nodeName stamps Version.UpdatedBy when callers don't supply a
	// fully-formed Version.
	nodeName string

	// seqCounter tracks the highest Seq we've ever written for OUR node.
	// Used by NextVersion to produce monotonically-increasing Seq values
	// when the caller passes Version{} (delegated assignment).
	seqCounter atomic.Uint64

	// totalReceived counts broadcasts received from peers (visibility).
	totalReceived atomic.Uint64

	// totalRejected counts broadcasts dropped due to stale Version.
	totalRejected atomic.Uint64
}

// NewStorage wraps `inner` with gossip + isolated-mode behaviors.
//
// Args:
//   - inner: the durable backend (typically SQLite)
//   - isolated: IsolatedMode signal. Pass AlwaysHealthy{} for single-node.
//   - broadcaster: change shipper. Pass NoopBroadcaster{} to disable replication.
//   - nodeName: this node's cluster name (used for Version.UpdatedBy).
func NewStorage(inner storage.StorageBackend, isolated IsolatedModeChecker, broadcaster ChangeBroadcaster, nodeName string) *Storage {
	if isolated == nil {
		isolated = AlwaysHealthy{}
	}
	if broadcaster == nil {
		broadcaster = NoopBroadcaster{}
	}
	s := &Storage{
		inner:       inner,
		isolated:    isolated,
		broadcaster: broadcaster,
		nodeName:    nodeName,
	}
	// Pre-seed seqCounter so NextVersion() always produces a Seq higher
	// than any record already in the DB. Without this, a fresh restart
	// would start at seq=1 and fail with ErrStaleWrite on any write to
	// a table that contains records with seq > 1.
	s.seedSeqFromStorage(inner)
	return s
}

// seedSeqFromStorage scans all known tables and advances seqCounter past
// the highest seq found in any record (including tombstones).
func (s *Storage) seedSeqFromStorage(inner storage.StorageBackend) {
	ctx := context.Background()
	for _, table := range storage.KnownTables() {
		recs, err := inner.List(ctx, table, storage.Filter{IncludeTombstones: true})
		if err != nil {
			continue // table may not exist yet (first boot) — skip
		}
		for _, rec := range recs {
			s.observeSeq(rec.Version.Seq)
		}
	}
}

// NextVersion returns a monotonically-increasing Version stamped by this
// node. Callers should use this for local writes to avoid manual seq
// tracking. Existing peer-supplied Versions (from ApplyRemoteChange)
// must be used as-is.
func (s *Storage) NextVersion() storage.Version {
	return storage.Version{
		Seq:       s.seqCounter.Add(1),
		UpdatedAt: time.Now().UTC(),
		UpdatedBy: s.nodeName,
	}
}

// NodeName returns the cluster name stamped on writes from this node.
func (s *Storage) NodeName() string { return s.nodeName }

// observeSeq bumps the local seq counter so that NextVersion always
// produces a Seq higher than any version we've ever seen — local or
// remote. Called after each successful upsert/apply to keep our counter
// monotonic against the cluster's combined history.
func (s *Storage) observeSeq(seq uint64) {
	for {
		cur := s.seqCounter.Load()
		if seq <= cur {
			return
		}
		if s.seqCounter.CompareAndSwap(cur, seq) {
			return
		}
	}
}

// Upsert implements storage.StorageBackend. Rejects writes during isolated
// mode. On success, broadcasts the change to peers (best-effort).
func (s *Storage) Upsert(ctx context.Context, table, id string, payload []byte, ver storage.Version) error {
	if s.isolated.IsolatedMode() {
		return storage.ErrSplitBrain
	}
	if err := s.inner.Upsert(ctx, table, id, payload, ver); err != nil {
		return err
	}
	s.observeSeq(ver.Seq)

	// Best-effort broadcast — broadcast errors don't fail the local write,
	// they're logged and anti-entropy reconciles later.
	change := StorageChange{
		MsgType: "storage_change",
		Table:   table,
		ID:      id,
		Payload: payload,
		Version: ver,
	}
	if err := s.broadcaster.BroadcastStorageChange(change); err != nil {
		slog.Warn("[STORAGE-GOSSIP] broadcast failed", "table", table, "id", id, "err", err)
	}
	return nil
}

// Delete implements storage.StorageBackend. Tombstone variant of Upsert.
func (s *Storage) Delete(ctx context.Context, table, id string, ver storage.Version) error {
	if s.isolated.IsolatedMode() {
		return storage.ErrSplitBrain
	}
	if err := s.inner.Delete(ctx, table, id, ver); err != nil {
		return err
	}
	s.observeSeq(ver.Seq)

	change := StorageChange{
		MsgType:   "storage_change",
		Table:     table,
		ID:        id,
		Version:   ver,
		Tombstone: true,
	}
	if err := s.broadcaster.BroadcastStorageChange(change); err != nil {
		slog.Warn("[STORAGE-GOSSIP] broadcast failed", "table", table, "id", id, "err", err)
	}
	return nil
}

// Get passes through to inner — reads always work, even in isolated mode.
func (s *Storage) Get(ctx context.Context, table, id string) (storage.Record, error) {
	return s.inner.Get(ctx, table, id)
}

// List passes through to inner.
func (s *Storage) List(ctx context.Context, table string, filter storage.Filter) ([]storage.Record, error) {
	return s.inner.List(ctx, table, filter)
}

// Watch passes through to inner.
func (s *Storage) Watch(ctx context.Context, table string) (<-chan storage.Event, error) {
	return s.inner.Watch(ctx, table)
}

// Close closes the inner backend.
func (s *Storage) Close() error { return s.inner.Close() }

// Inner returns the underlying durable backend, bypassing the gossip
// broadcast layer. Use this for entities that should be persisted but
// must NOT be replicated to peers — currently only SLO incidents, where
// each node's incident list reflects that node's individual observation
// and aggregating them across the cluster would inflate downtime counts.
//
// Writes through Inner() still respect LWW (Version.Compare) at the
// underlying backend level, but skip the broadcast hop. They also bypass
// IsolatedMode write guard, so callers must accept that local-only writes
// continue even during cluster partition.
//
// Use sparingly: this is an escape hatch from the cluster-replicated
// contract.  Most data should flow through the regular Upsert/Delete
// methods on *Storage itself.
func (s *Storage) Inner() storage.StorageBackend { return s.inner }

// ApplyRemoteChange applies a StorageChange received from a peer. Called
// by the cluster layer when a gossip broadcast arrives.
//
// The inner backend's LWW check handles staleness automatically — older
// versions return storage.ErrStaleWrite which we count and drop silently.
// This is the expected behavior during convergence: two nodes write the
// same record concurrently → one node's write loses LWW and gets dropped
// here on apply.
//
// ApplyRemoteChange does NOT trigger another broadcast (which would
// create an infinite loop). Anti-entropy push-pull handles cases where
// a node missed an original broadcast.
func (s *Storage) ApplyRemoteChange(ctx context.Context, change StorageChange) error {
	s.totalReceived.Add(1)

	var err error
	if change.Tombstone {
		err = s.inner.Delete(ctx, change.Table, change.ID, change.Version)
	} else {
		err = s.inner.Upsert(ctx, change.Table, change.ID, change.Payload, change.Version)
	}

	if err == storage.ErrStaleWrite {
		s.totalRejected.Add(1)
		return nil // benign — our local copy is newer
	}
	if err != nil {
		return err
	}
	s.observeSeq(change.Version.Seq)
	return nil
}

// Stats returns observability counters.
func (s *Storage) Stats() Stats {
	return Stats{
		TotalReceived: s.totalReceived.Load(),
		TotalRejected: s.totalRejected.Load(),
		HighestSeq:    s.seqCounter.Load(),
	}
}

// Stats holds observability counters for the gossip wrapper.
type Stats struct {
	TotalReceived uint64
	TotalRejected uint64
	HighestSeq    uint64
}

// Compile-time interface compliance check.
var _ storage.StorageBackend = (*Storage)(nil)
