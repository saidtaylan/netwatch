// Package storage defines the StorageBackend abstraction for netwatch's
// persistent state (SLO targets, apps, channels, silences, maintenance
// windows, alert history, etc.).
//
// Architecture (decided 2026-05-22):
//
//	HTTP Handlers / Engine
//	          │
//	          ▼
//	┌─────────────────────────────┐
//	│  StorageBackend interface   │
//	└─────────────────────────────┘
//	          │
//	          ▼
//	 ┌────────────────────┐    ┌────────────────────┐
//	 │ GossipLWWStorage   │    │ RaftStorage        │
//	 │ (V1, B18-B26)      │    │ (V2.0, B27, future)│
//	 │ SQLite + gossip    │    │ hashicorp/raft +   │
//	 │ + LWW              │    │ bbolt              │
//	 └────────────────────┘    └────────────────────┘
//
// Conflict resolution for V1 (GossipLWWStorage) follows a strict Lamport-
// style LWW (Last Write Wins) ordering:
//
//  1. Higher Seq wins.
//  2. Equal Seq → newer UpdatedAt wins.
//  3. Equal Seq + Equal UpdatedAt → lexicographically greater UpdatedBy wins.
//
// V2.0 (RaftStorage) eliminates LWW entirely via Raft log linearization
// while still implementing the same StorageBackend interface — upper layers
// (engine, HTTP handlers) are unchanged.
package storage

import (
	"strings"
	"time"
)

// Version is the LWW conflict-resolution metadata attached to every record.
//
// Seq is incremented monotonically by the writing node. Concurrent writes
// across nodes can produce the same Seq for the same key — the secondary
// tiebreakers (UpdatedAt, UpdatedBy) ensure deterministic resolution.
type Version struct {
	// Seq is the Lamport timestamp for this record on the writing node.
	// Incremented on every successful Upsert/Delete on that record.
	Seq uint64 `json:"seq"`

	// UpdatedAt is the wall-clock time the change was applied locally.
	// Used as the secondary tiebreaker — clock skew between nodes is
	// accepted (NTP-synced clusters have <1s skew which is fine for LWW).
	UpdatedAt time.Time `json:"updated_at"`

	// UpdatedBy is the cluster node name that originated the write.
	// Used as the final tiebreaker (lexicographic comparison) so that
	// two nodes with identical (Seq, UpdatedAt) deterministically agree
	// on a winner without needing further communication.
	UpdatedBy string `json:"updated_by"`
}

// Compare returns:
//
//	-1 if a < b (a is older/loses)
//	 0 if a == b (identical version, no-op)
//	 1 if a > b (a is newer/wins)
//
// Ordering rule (highest precedence first):
//  1. Seq desc
//  2. UpdatedAt desc
//  3. UpdatedBy lex desc
//
// Note: This must be deterministic across all nodes — any node comparing
// the same two Versions must produce the same result.
func (a Version) Compare(b Version) int {
	if a.Seq != b.Seq {
		if a.Seq > b.Seq {
			return 1
		}
		return -1
	}
	if !a.UpdatedAt.Equal(b.UpdatedAt) {
		if a.UpdatedAt.After(b.UpdatedAt) {
			return 1
		}
		return -1
	}
	// UpdatedBy comparison — reverse lex (higher node name wins) for
	// stable resolution. Empty string sorts lowest.
	switch strings.Compare(a.UpdatedBy, b.UpdatedBy) {
	case 1:
		return 1
	case -1:
		return -1
	}
	return 0
}

// IsZero returns true when the Version has never been written.
// Useful for callers to distinguish "no version yet" from "version 0".
func (v Version) IsZero() bool {
	return v.Seq == 0 && v.UpdatedAt.IsZero() && v.UpdatedBy == ""
}

// NextVersion returns a Version one step newer than `prev`, stamped by
// `nodeName` at `now`. If `prev` is zero, the new Version starts at Seq=1.
//
// This is the standard helper for nodes performing local writes — they call
// NextVersion to derive a Version, then Upsert(record, version) on the
// backend.
func NextVersion(prev Version, nodeName string, now time.Time) Version {
	return Version{
		Seq:       prev.Seq + 1,
		UpdatedAt: now,
		UpdatedBy: nodeName,
	}
}
