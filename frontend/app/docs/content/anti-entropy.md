# Anti-entropy & rejoin

Gossip broadcasts are best-effort UDP — packets get dropped, and a node that was offline missed everything while it was gone. **Anti-entropy** is the safety net that makes the cluster *eventually consistent* despite that: nodes periodically exchange their full state and reconcile, so any divergence self-heals.

## Why it's needed

Two ways state can diverge:

1. **Dropped broadcasts** — a UDP storage-change or probe-state packet is lost, so one node never saw an update.
2. **Rejoin** — a node restarts or partitions away, then comes back having missed every change in the interim.

Without reconciliation, that node would alert on stale information — e.g. fire a "recovered!" alert for a target that's actually still down, or page on an outage that was resolved while it was away.

## Push-pull sync

netwatch rides memberlist's **push-pull** mechanism. Periodically (and on every join) two nodes exchange a full snapshot of their state and merge:

- The cluster layer asks the engine for a snapshot via the `AntiEntropyProvider` interface — `FullState()` serializes the node's `lastKnown` map.
- The remote snapshot is merged back with `ApplyRemoteState()`, using the same Lamport rule as live gossip: a remote entry wins only if its `seq` is higher (or equal with a greater node name).
- Because the merge is Lamport-ordered, it is **idempotent and commutative** — applying snapshots in any order converges to the same result.

## Suppression during sync

The dangerous moment is exactly when a rejoining node is catching up: if it started alerting mid-sync it could page on half-merged state. To prevent this:

- During a join-time full sync the engine sets a `syncing` flag (`SetSyncing(true)`).
- While `syncing` is true, the probe loops and the alert path **early-return** — no alerts are dispatched.
- When the sync completes (`SetSyncing(false)`), normal operation resumes and prober assignments recompute.

This is why a **rolling restart of the entire fleet does not produce an alert storm**: each node reconciles to the cluster's current truth before it's allowed to alert.

## State persistence is the seed

Anti-entropy starts from the node's persisted state (`state.json` / `target_states`), not from scratch. So a restarting node says "here's what I last knew" and the cluster corrects only the parts that changed — minimal churn, no phantom alerts.

## Relationship to the write path

The [write path](gossip-propagation) is the fast path (best-effort UDP for low latency). Anti-entropy is the slow, reliable path that guarantees convergence. Together they give you cheap real-time updates *and* eventual consistency:

```
write → local SQLite + best-effort UDP   (fast, may drop)
                    │
                    ▼ (drops / rejoins reconciled by)
        push-pull anti-entropy            (periodic, reliable)
```
