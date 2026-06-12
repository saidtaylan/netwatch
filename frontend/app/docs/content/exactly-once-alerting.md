# Exactly-once alerting

With many nodes watching the same target, the hard part isn't *detecting* an outage — it's making sure it produces **exactly one** alert, from the right node, even as nodes come and go. This page walks the full decision chain, in the exact order it executes.

## The responsible node

The same selection that picks probers also picks, for each target, a single **responsible node** — `picked[0]`, the first node after the target-keyed rotation and zone-aware pick (see [Distributed Probe Ownership](distributed-probe-ownership)). Only that node may dispatch the alert. Because every node computes the identical ordering from the same gossiped candidate set, they all agree on who is responsible with no coordination.

When the responsible node leaves, a membership event recomputes the ring and the next node becomes responsible — **automatic failover, no elected leader**.

## The alert gate — `shouldAlert(t)`

Before any alert is sent, these checks run **in this order**; the first failing check suppresses the alert:

1. **Maintenance window** — if the target is inside an active [maintenance window](maintenance-silences), suppress. (Highest priority — probing continues, only the alert is muted.)
2. **Silence** — if the target matches an active silence, suppress.
3. **Standalone?** — if there is no cluster (`clusterMgr == nil`), alert. A single observer has nothing to coordinate.
4. **Isolated?** — if the node has lost quorum (`IsolatedMode`), suppress. Its view is untrustworthy. See [Quorum & isolated mode](quorum-isolation).
5. **Responsible?** — if this node is not the primary (`IsResponsible`), suppress. Some other node owns the alert.
6. **Confirmations** — *conditionally*; see below.

Only if every gate passes does the responsible node build the alert env and dispatch.

## Confirmations: the prober-primary exemption

`min_probe_confirmations` *(default 0/1)* can require multiple probers to agree before alerting — but with a deliberate, important exemption:

- The confirmation guard applies **only when the responsible node is *not itself* a prober** for the target. Such a node is relying entirely on gossip from others, so it waits until `min_probe_confirmations` peers have gossiped `hard_down`:

  ```
  confirmCount = count(peer in PeerStatesForTarget(target) where peer.state == "hard_down")
  if confirmCount < min_probe_confirmations: suppress
  ```

- If the responsible node **is** a prober, it has **first-hand evidence** — it probed the target itself and got a connection error. It fires immediately; the confirmation guard adds no safety and would only introduce a silent suppression window.

So `min_probe_confirmations: 2` means: *a non-prober primary waits for two probers to agree* (eliminating the classic false alarm where one prober has a broken path while everyone else sees the target up), while a prober-primary with direct proof still alerts at once.

## The cross-node safety net (primary-forwards-peer-alert)

The responsible node (by rotation) might not actually probe the target itself — responsibility and the prober set are computed independently. netwatch closes that gap:

- Probers broadcast their state including the target's name and type.
- When a node receives a peer `hard_down` for a target it does **not** probe locally — and it is responsible, quorum is healthy, and the sequence is newer than the last it alerted on (`peerAlerted[targetID]`) — it dispatches the alert on that peer's behalf (`DispatchPeerAlert`).
- The `peerAlerted` map deduplicates by sequence, so a target never double-alerts. `NODE_NAME` in the alert reflects the node that actually detected the outage.

## Recovery

Recovery alerts pass the same gate. A `hard_down → up` transition (after `recovery_probes` successes) increments `seq` and, on the responsible node, emits the "reachable" alert. The monotonic `seq` makes each `ProbeDown` strictly orderable against its later `ProbeUp`, so downstream consumers (e.g. Alertmanager) can pair them.

## Channel selection

Once the gate passes, channels are chosen as `union(target.notify, apps.notifications)`, deduplicated; if empty → `default_notify`; if still empty → logged only. Each channel (script / mail / webhook) receives the full alert env — see [Alert env reference](alert-env) and [Notifications](notifications).

## The whole chain

```
probe fails → state machine → hard_down (seq++)
        │  on EVERY node that observes it
        ▼
   shouldAlert(t):
     maintenance? silence? standalone? isolated? responsible?
        │ all pass
        ▼
     prober-primary → fire immediately
     non-prober primary → require min_probe_confirmations peers in hard_down
        │
        ▼
   build env (scope, classification, root cause, apps) → dispatch to channels
```

The result: **one outage, one alert, from the right node**, with automatic failover, confirmation safety for gossip-only primaries, and no duplicates — the property the entire distributed design exists to guarantee.
