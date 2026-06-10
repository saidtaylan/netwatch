# How a target propagates

This page traces exactly what happens when you add a target on one node and it replicates to the whole cluster. It is the canonical example of how **all** dynamic configuration (apps, channels, SLOs, silences, users) propagates — they all flow through the same storage + gossip path.

## The journey

```
NODE-1 (the writer)                       NODE-2..N (the receivers)
──────────────────                        ─────────────────────────
1  PUT /targets/{id}        main.go
2  Engine.UpsertTarget      engine.go
3  targetsManager.Upsert    targets_manager.go
4  persistTarget            targets_manager.go
5  gossip.Storage.Upsert    storage/gossip
     ├─ write local SQLite (inner)
     └─ BroadcastStorageChange                memberlist UDP
6  Manager.BroadcastStorageChange   ───────►  7  NotifyMsg → handleStorageChange
                                              8  ApplyRemoteChange
                                                   └─ LWW compare → write SQLite (no re-broadcast)
                                              9  Watch event → targetsManager
                                             10  reconcile → start/stop probe loop
```

## The write side (node-1)

### 1. HTTP handler

`PUT /targets/{id}` requires admin auth, unmarshals the body into a `Target`, defaults the id from the URL, validates `type` and `target`, then calls `Engine.UpsertTarget`. If the cluster has lost quorum the storage layer returns `ErrSplitBrain` and the handler responds **503** with `Retry-After` — writes are paused during a split-brain.

### 2. Engine.UpsertTarget

A thin pass-through to the storage-backed `targetsManager`. (Before the engine is fully initialised it falls back to writing the in-memory config only.)

### 3–4. targetsManager.Upsert → persistTarget

The manager **persists first, then updates memory, then reconciles** — order matters:

```go
func (m *targetsManager) Upsert(t Target) error {
    if err := m.persistTarget(t); err != nil { return err }  // (a) durable write + gossip
    m.targets[t.key()] = t                                   // (b) live in-memory map
    return m.reconcile(snapshot)                             // (c) sync local probe loops
}
```

`persistTarget` marshals the target to JSON and writes it with a fresh **last-writer-wins version**:

```go
func (s *Storage) NextVersion() storage.Version {
    return storage.Version{
        Seq:       s.seqCounter.Add(1),   // node-local monotonic counter
        UpdatedAt: time.Now().UTC(),
        UpdatedBy: s.nodeName,            // e.g. "node-1"
    }
}
```

The `seqCounter` is always kept higher than any sequence the node has ever seen (local or remote), so a fresh write always wins the LWW comparison. `UpdatedBy` is the deterministic tie-breaker.

### 5. gossip.Storage.Upsert — write locally, then broadcast

```go
func (s *Storage) Upsert(ctx, table, id, payload, ver) error {
    if s.isolated.IsolatedMode() { return storage.ErrSplitBrain }  // quorum gate
    if err := s.inner.Upsert(...); err != nil { return err }       // (1) write local SQLite first
    s.observeSeq(ver.Seq)
    change := StorageChange{Table: table, ID: id, Payload: payload, Version: ver}
    if err := s.broadcaster.BroadcastStorageChange(change); err != nil {  // (2) then tell peers
        slog.Warn("[STORAGE-GOSSIP] broadcast failed", ...)        //     best-effort: error swallowed
    }
    return nil
}
```

Durability first (local disk), then a **best-effort** broadcast. If the UDP packet is lost, the local write still succeeded and anti-entropy reconciles the peer later.

### 6. Manager.BroadcastStorageChange — send to every peer

Stamps a `msg_type` so receivers can route it, JSON-encodes the change, and sends it to each peer with `SendBestEffort` (UDP). UDP is used deliberately: storage changes are not financially critical, and TCP would add head-of-line blocking under write bursts.

## The receive side (node-2..N)

### 7–8. NotifyMsg → handleStorageChange → ApplyRemoteChange

memberlist delivers the bytes to `NotifyMsg`, which routes by `msg_type` to `handleStorageChange`, which calls `ApplyRemoteChange`. There the **LWW comparison** decides whether the incoming version beats what's already stored (`seq` → `updated_at` → `updated_by`). If it wins, it is written to the local SQLite — **without re-broadcasting** (no gossip amplification). Stale writes are silently dropped.

### 9–10. Watch event → reconcile → probe loop

Writing to storage emits a `Watch` event. Each domain manager (here `targetsManager`) runs a `Watch` goroutine that receives it and calls `reconcile`, which rebuilds the live target set and starts or stops probe goroutines as ownership dictates.

## Watch it happen (live)

Adding a target on `node-1` of a 5-node demo cluster:

```bash
curl -X PUT localhost:10241/targets/walkthrough-demo \
  -H "Authorization: Bearer $TOKEN" \
  -d '{"id":"walkthrough-demo","type":"tcp","target":"1.1.1.1:53"}'
# → HTTP 200
```

Two seconds later, `node-2` and `node-5` both list `walkthrough-demo`, and node-2's database shows where it came from:

```
walkthrough-demo | seq=27 | updated_by=node-1
```

node-2's log shows the receive → reconcile step firing:

```
targets: reconciled  count=4        ← registry grew 3 → 4 on the receiver
```

So the 6-step write+broadcast chain and the 4-step receive chain work end to end, and the LWW stamp correctly preserves the origin (`node-1`).

## Why this design

- **Durable-first** means a write is never lost even if gossip drops it.
- **Best-effort UDP + anti-entropy** keeps the hot path cheap; reconciliation is the safety net.
- **No re-broadcast on apply** prevents gossip storms.
- **LWW with `(seq, updated_at, updated_by)`** resolves concurrent writes deterministically — every node converges to the same winner.
- **Quorum gate (`IsolatedMode`)** refuses writes during a split-brain, so a minority partition can't accept divergent config.
