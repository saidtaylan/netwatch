# State machine

Every target moves through a small, explicit state machine on each prober. The design is deliberately conservative: a single dropped packet never pages anyone, and a flapping target never alternates outage/recovery alerts. This page documents the exact mechanics, timing and edge cases.

## States

| State | Stored where | Alerts? | Meaning |
|---|---|---|---|
| `unknown` | — | no | Initial; not yet probed. |
| `up` | disk + gossip | — | Last probe succeeded. |
| `soft_down` | **RAM only** (`pending` map) | **no** | A probe failed; retries are in flight. Never persisted. |
| `hard_down` | disk + gossip | **yes** | Failed enough times to be declared down. The only state that can alert. |
| soft-up (recovering) | RAM (`pendingRecovery`) | no | A `hard_down` target started succeeding but hasn't met `recovery_probes` yet. |

## Two loops, not one

The single most important implementation detail: there are **two independent goroutines** per target's lifecycle.

1. **The probe loop** (`startProbeLoop`, one goroutine per assigned target) fires every `probe_interval_sec`. It runs the checker (`runCheck`). It is what **first detects** a failure and enqueues the target into the `pending` (soft_down) map — it does **not** itself escalate to hard_down.
2. **The retry loop** (`runRetryLoop` → `processPending`, a single shared goroutine) ticks on `ticker_interval_sec` and re-probes every pending entry whose next-check time has arrived, every `retry_interval_sec`. **This** loop owns escalation to `hard_down` and recovery.

Once a target is in `pending`, the probe loop's subsequent failures are ignored (`!inPending` guard) — the retry loop is in charge, so there is no double-counting.

## Transitions, precisely

```
            probe loop                         retry loop
            ──────────                         ──────────
up ──fail──► enqueue(RetryCount=0)  ──►  soft_down
                                          │
                          re-probe every retry_interval_sec:
                                          │
              success ◄───────────────────┤────────────► fail: RetryCount++
                 │                                            │
            markRecovered                            RetryCount ≥ max_retries ?
                 │                                       │ yes        │ no
                 ▼                                       ▼            ▼
                up                                  hard_down    stay soft_down
```

### Down: how many failures, and how long

- The probe loop's first failure enqueues with `RetryCount = 0` and schedules the first retry at `now + retry_interval_sec`. **State is now `soft_down`. No alert.**
- Each retry that fails does `RetryCount++`. When `RetryCount ≥ max_retries`, the retry loop calls `markHardDown`.

So a target becomes `hard_down` after **`1 + max_retries` consecutive failed probes**, taking roughly **`max_retries × retry_interval_sec`** after the first failure.

> **Worked example** — `max_retries: 2`, `retry_interval_sec: 30`:
> `T+0s` probe fails → soft_down (RetryCount 0) · `T+30s` retry fails → RetryCount 1 (< 2, stays soft) · `T+60s` retry fails → RetryCount 2 (≥ 2) → **hard_down + alert**. Three failed probes, ~60 s.

### Up: recovery and the soft-up guard

When a `hard_down` (or `soft_down`) target's probe succeeds:

- `recovery_probes: 1` (default) → recover immediately (`markRecovered`).
- `recovery_probes: N > 1` → enter soft-up: increment a `pendingRecovery` counter, and only `markRecovered` after **N consecutive** successes. **Any failure mid-recovery resets the counter** and the target stays `hard_down`. This absorbs a target that flaps back for one probe then dies again, so you don't get a premature "recovered" followed by another outage alert.

## What `markHardDown` / `markRecovered` do (atomically)

Both run under `stateMu` and:

1. increment the per-target **Lamport `seq`** (the causal clock — see below),
2. write the new state and `error_code` to disk (`state.json` v2 / `target_states`), atomically (`.tmp` then `os.Rename`),
3. broadcast the new state to the cluster over gossip,
4. record an SLO incident start/end,
5. return whether an alert should follow (the responsible-node decision is layered on top — see [Exactly-once alerting](exactly-once-alerting)).

## Two-phase processing (the root-cause race fix)

When several targets escalate to `hard_down` in the **same** retry tick, `processPending` deliberately splits work into two phases:

1. **Phase 1** — probe every due target and commit all the state writes to `lastKnown`.
2. **Phase 2** — only then dispatch alerts.

This guarantees that when a dependent target (e.g. `api-gateway`) builds its alert, its dependency (`db-primary`) has **already** been written as `hard_down` — so [root-cause analysis](root-cause-topology) sees the true cause instead of racing it.

## Sequence numbers (Lamport clock)

Every `hard_down`/`recovered` transition increments a per-target `seq` under lock. The pair `(seq, owner_node)` orders events causally across the cluster — compared by `seq` first, then `owner_node` lexicographically. This is what lets two nodes that both observed a target reconcile to one agreed state during a gossip merge, and what stamps each alert (and the recovery that matches it) with a monotonic `SEQ`.

## Persistence and why it matters

`hard_down` and `up` are persisted; `soft_down` is **not** (it is a transient RAM-only suspect state). Persisting the confirmed state lets a node restart or rejoin and reconcile via [anti-entropy](anti-entropy) **before** alerting — which is why a rolling restart of the whole fleet produces no phantom recovery/outage storm.

## Parameters (all overridable per target)

| Field | Default | Effect |
|---|---|---|
| `probe_interval_sec` (`interval_sec`) | 60 | How often the probe loop checks. |
| `timeout` | 5 | Per-probe deadline; a timeout counts as a failure. |
| `max_retries` | 2 | Retries before `hard_down` (total failures = `1 + max_retries`). |
| `retry_interval_sec` | 30 | Delay between retries while `soft_down`. |
| `ticker_interval_sec` | 5 | Granularity of the retry loop. |
| `recovery_probes` | 1 | Consecutive successes required to leave `hard_down`. |

## Edge cases

- **Standalone vs cluster** — the state machine runs identically; in a cluster the per-prober state is then merged into a consensus and gated by the responsible node before alerting.
- **Anti-entropy in progress** — `processPending` early-returns while `syncing` is true, so a node catching up never escalates on stale data.
- **Config hot-reload** — changing `max_retries`/intervals takes effect on the next evaluation via the `effective*` helpers; in-flight pending entries keep their counter and simply use the new thresholds going forward.
- **Probe loop while soft_down** — ignored; the retry loop is authoritative, so the effective failure cadence is `retry_interval_sec`, not `probe_interval_sec`.
