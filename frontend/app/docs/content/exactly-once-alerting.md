# Exactly-once alerting

With many nodes watching the same target, the hard part isn't *detecting* an outage — it's making sure it produces **exactly one** alert, from the right node, even as nodes come and go. This page walks the full decision chain.

## The responsible node

The same consistent-hash ring that selects probers also picks, for each target, a single **responsible node** — the primary in the deterministically-ordered set. Only that node may dispatch the alert. Because every node computes the identical ordering from the same gossiped candidate set, they all agree on who is responsible without any coordination. See [Distributed Probe Ownership](distributed-probe-ownership).

When the responsible node leaves, a membership event triggers `updateRing()` and the next node in the order becomes responsible — **automatic failover, no elected leader**.

## The alert gate

Before any alert is sent, `shouldAlert()` checks, in order:

1. **Standalone?** If there is no cluster (`clusterMgr == nil`) → alert (single observer, nothing to coordinate).
2. **Isolated?** If the node has lost quorum → **do not alert** (its view is untrustworthy). See [Quorum & isolated mode](quorum-isolation).
3. **Responsible?** If this node is not the primary for the target → **do not alert** (some other node owns it).
4. **Confirmations met?** If `min_probe_confirmations > 1`, require that many independent probers to report `hard_down` first.

Only if all gates pass does the responsible node build the alert env and dispatch it.

## Confirmations: suppressing single-prober false alarms

`min_probe_confirmations` *(default 0 = treat as 1)* sets how many probers must independently reach `hard_down` before alerting:

- `0` / `1` — alert as soon as any one prober declares the target down. Fastest.
- `2` — wait for a second prober to agree. This eliminates the classic false alarm where one prober has a broken path to the target while everyone else still sees it up. The cost is up to one extra `probe_interval_sec` of detection latency.

This pairs naturally with [scope classification](scope-classification): a lone prober seeing down is a `LOCAL_FAILURE` candidate, and confirmations stop it from paging.

## The cross-node safety net

A subtle case: the responsible node (by hash) might not actually probe the target itself — its prober set and its responsibility are computed independently. netwatch handles this with **primary-forwards-peer-alert**:

- Probers broadcast their state with the target's name and type.
- If the responsible node receives a `hard_down` from a peer for a target it doesn't probe locally — and quorum is healthy and the sequence is new — it dispatches the alert on that peer's behalf (`DispatchPeerAlert`), deduplicating by sequence so a target never double-alerts.
- The `NODE_NAME` in the alert reflects the node that actually detected the outage.

## Recovery

Recovery alerts follow the same gates. A `hard_down → up` transition (after `recovery_probes` successes) increments `seq` and, on the responsible node, emits the "reachable" alert. The monotonic `seq` makes recovery and outage alerts strictly orderable, so consumers (e.g. Alertmanager) can match a `ProbeDown` to its later `ProbeUp`.

## Channel selection

Once the gate passes, the channels are chosen as `union(target.notify, apps.notifications)`, deduplicated; if empty, `default_notify`; if still empty, the alert is logged only. Each channel (script / SMTP / webhook) receives the full alert env — see the [Alert env reference](alert-env).

## Putting it together

```
probe fails → state machine → hard_down (seq++)
        │
        ▼  on the RESPONSIBLE node only:
   shouldAlert(): standalone? isolated? responsible? confirmations?
        │ all pass
        ▼
   build env (scope, classification, root cause, apps) → dispatch to channels
```

The result: one outage, one alert, from the right node, with automatic failover and no duplicates — the property the whole distributed design exists to guarantee.
