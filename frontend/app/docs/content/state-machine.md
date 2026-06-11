# State machine

Every target moves through a small, explicit state machine on each prober. The states and transitions are deliberately conservative so that a single dropped packet never pages anyone.

## States

| State | Where it lives | Meaning |
|---|---|---|
| `unknown` | — | Initial; not yet probed. |
| `up` | disk + gossip | Last probe succeeded. |
| `soft_down` | RAM only (pending map) | A probe failed, but we haven't given up — retries are in flight. **Never persisted**, never alerts. |
| `hard_down` | disk + gossip | Failed `max_retries` consecutive times. This is the state that can produce an alert. |
| `soft_up` (recovering) | RAM | A `hard_down` target started succeeding again but hasn't met `recovery_probes` yet. |

## Transitions

```
unknown ──► up
   │
   ▼ (probe fails)
soft_down ──(retry succeeds)──► up
   │
   ▼ (max_retries consecutive failures)
hard_down ──(recovery_probes consecutive successes)──► up
```

- **up → soft_down** — first failed probe. The target enters the pending/retry queue. No alert yet.
- **soft_down → hard_down** — after `max_retries` consecutive failures, separated by `retry_interval_sec`. `markHardDown()` fires: it bumps the Lamport `seq`, records the error code, persists to disk, broadcasts over gossip, and is the trigger for alert evaluation.
- **soft_down → up** — a retry succeeds before the budget is exhausted. The target silently recovers; nothing is persisted or alerted.
- **hard_down → up** — requires `recovery_probes` (default 1) consecutive successes. `markRecovered()` fires: bumps `seq`, clears the error code, persists, broadcasts, and emits the recovery alert. The intermediate "recovering" phase prevents a flapping target from alternating up/down alerts.

## Why two "soft" phases

- **soft_down** absorbs transient failures (a dropped packet, a momentary GC pause on the target). Most blips never become alerts.
- **soft_up / recovery_probes** absorbs transient *recoveries* — a target that comes back for one probe then dies again won't generate a premature "recovered" alert followed by another outage alert.

## Sequence numbers (Lamport clock)

Every `hard_down`/`recovered` transition increments a per-target `seq` under a lock. The pair `(seq, owner_node)` orders events causally across the cluster:

- Comparison is `seq` first, then `owner_node` lexicographically as a tie-breaker.
- This is what lets two nodes that both observed the same target reconcile to a single agreed state during gossip merges, and what stamps each alert with a monotonic `SEQ`.

## Persistence (state.json / target_states)

`hard_down` and `up` are written to disk (`state.json` v2, also mirrored into the `target_states` table). `soft_down` is **not** — it is a transient RAM-only suspect state.

```json
{
  "version": 2,
  "targets": {
    "core-db": { "state": "hard_down", "seq": 3, "error_code": "dial tcp: connection refused", "owner_node": "" }
  }
}
```

Persisting the confirmed state is what lets a node restart (or rejoin the cluster) and reconcile via [anti-entropy](gossip-propagation) **before** alerting — so a rolling restart of the whole fleet doesn't generate phantom recovery/outage alerts. Writes are atomic (`.tmp` then `os.Rename`).

## In a cluster

The state machine above runs on each **prober**. The cluster then merges all probers' views into a **consensus state** and decides — once — whether to alert. How that decision is made (responsible node, confirmations, scope) is covered in [Exactly-once alerting](exactly-once-alerting) and [Scope classification](scope-classification).
