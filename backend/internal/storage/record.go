package storage

// Record is the unit of storage — one row in a logical table.
//
// Payload is opaque to the storage layer: callers serialize their domain
// objects (SLOTarget, App, NotificationChannel, etc.) to JSON before
// passing them in. The storage layer never inspects Payload.
//
// Tombstone indicates a soft-deleted record. Tombstoned records are not
// returned by Get/List (filter handled by backend) but are still kept in
// the underlying store with their Version so that anti-entropy can
// reconcile deletes correctly (a peer that missed the delete must not
// resurrect the record via stale Upsert).
type Record struct {
	ID        string  `json:"id"`
	Payload   []byte  `json:"payload,omitempty"`
	Version   Version `json:"version"`
	Tombstone bool    `json:"tombstone,omitempty"`
}

// Filter narrows a List call. All fields are optional — empty means
// "no filter".
//
// SinceSeq is useful for anti-entropy: a peer asks "give me all rows
// with Seq > N" to catch up after missing broadcasts.
//
// IncludeTombstones is rarely needed by domain callers; anti-entropy
// uses it to propagate deletes during sync.
type Filter struct {
	IDs               []string `json:"ids,omitempty"`
	SinceSeq          uint64   `json:"since_seq,omitempty"`
	IncludeTombstones bool     `json:"include_tombstones,omitempty"`
	Limit             int      `json:"limit,omitempty"`
}

// EventType enumerates change events emitted by Watch().
type EventType string

const (
	EventUpsert EventType = "upsert"
	EventDelete EventType = "delete"
)

// Event is published on Watch channels whenever a Record changes in a
// table — either via local Upsert/Delete or via remote gossip apply.
//
// Subscribers (e.g. probe loop watching the targets table) react to
// these events to keep their in-memory state in sync.
type Event struct {
	Type   EventType
	Table  string
	Record Record
}
