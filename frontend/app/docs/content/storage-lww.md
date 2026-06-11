# Storage & LWW

All **dynamic** configuration and history lives in a per-node SQLite database — `netwatch.db`, in the same directory as `state_file`. There is no external database and no shared store; each node has its own copy and the cluster keeps them in sync over gossip.

## The interface

The engine never talks to SQLite directly. It goes through a `StorageBackend` interface, wrapped by a gossip layer:

```
Engine / HTTP handlers
        │
   StorageBackend (interface): Upsert / Delete / Get / List / Watch
        │
   gossip.Storage  ── wraps the SQLite backend, adds replication + LWW + isolation guard
        │
   SQLite (modernc.org/sqlite — pure Go, no CGO)
```

Keeping it behind an interface means the backend could be swapped later (e.g. a Raft-based store) without touching the engine.

## Tables

Each domain is one table; the row `payload` is the JSON-encoded object plus LWW metadata:

```sql
CREATE TABLE <entity> (
  id          TEXT PRIMARY KEY,
  payload     BLOB    NOT NULL,   -- JSON object
  seq         INTEGER NOT NULL,   -- Lamport timestamp
  updated_at  TEXT    NOT NULL,   -- ISO-8601 wall clock
  updated_by  TEXT    NOT NULL,   -- node name (tie-breaker)
  tombstone   INTEGER NOT NULL DEFAULT 0  -- soft delete
);
```

| Table | Replication | First-boot seed |
|---|---|---|
| `targets`, `apps`, `notification_channels`, `slo_targets`, `silences`, `maintenance_windows`, `users`, `frontend_settings` | Cluster (gossip + LWW) | from `config.yaml` where applicable |
| `slo_incidents`, `target_states`, `audit_log` | Local-only | — |
| `alerts`, `alert_events` | Reserved | — |

## Last-writer-wins (LWW)

Conflicts between concurrent writes are resolved **deterministically** so every node converges to the same winner. The comparison order is:

1. **`seq`** — higher Lamport sequence wins. The local counter is always kept above any sequence the node has ever seen (local or remote), so a fresh write beats anything it knows about.
2. **`updated_at`** — if `seq` ties, the later wall-clock time wins.
3. **`updated_by`** — if both tie, the lexicographically greater node name wins.

Because this is a pure function of the row metadata, two nodes that receive the same set of writes in any order end up with the same row.

> LWW resolves conflicts deterministically but does **not** prevent data loss in a true write–write race (two nodes writing the same key at the same instant — one wins, one is dropped). For netwatch's data (UUID-keyed, low write rate) this is acceptable; the highest-risk asset — user accounts — is the reason a Raft backend is kept as a future option behind the same interface.

## Config seeding

`config.yaml`'s `targets:`, `apps:`, `notifications:`, `slo.targets:` are written into the DB **once**, on first boot with an empty table. After that the DB is authoritative; edits to those sections of `config.yaml` are ignored. Manage them through the UI / API instead.

## Replication & isolation

- **On write** → write local SQLite first, then best-effort broadcast the change to peers (UDP). Peers apply it under LWW with no re-broadcast. The full trace is in [How a target propagates](gossip-propagation).
- **Anti-entropy** reconciles drops: during memberlist push-pull cycles nodes exchange state and converge, so a lost UDP packet self-heals.
- **Isolated mode** rejects writes (`ErrSplitBrain` → 503) when the node has lost quorum — a minority can't accept divergent config. See [Quorum & isolated mode](quorum-isolation).
