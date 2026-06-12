# Quorum & isolated mode

A monitoring cluster that gets cut off from the rest of the network is dangerous: from inside the minority, *everything* looks down. netwatch guards against this with **quorum gating** — a node that can't see enough of its peers stops trusting its own judgement.

## Quorum

Each node continuously checks how many peers it can see against the expected size:

- `expected_node_count` — how big the cluster should be.
- `min_quorum_ratio` *(default 0.5)* — the fraction that must be alive.
- A node has quorum when `alive ≥ floor(expected_node_count × min_quorum_ratio) + 1` — i.e. a strict majority by default.

```
needed = floor(expected_node_count × min_quorum_ratio) + 1
quorum = aliveCount ≥ needed
```

> **Opt-out:** if `expected_node_count ≤ 0`, the check always returns "quorum healthy" — quorum gating is effectively disabled. Set it to your real steady-state size to turn the protection on.

The check runs on a loop (`runQuorumLoop`, every 5 s) and drives the `network_prober_quorum_healthy` metric; a lost-quorum transition flips the node into isolated mode.

## Isolated mode

When a node loses quorum it enters **isolated mode** (`IsolatedMode() == true`). In that state it deliberately becomes passive:

| Behaviour | Effect |
|---|---|
| **Alert dispatch suppressed** | The node will not fire alerts, because its view is untrustworthy. `network_prober_isolated = 1`. |
| **Writes rejected** | Storage writes (targets, apps, config, …) return `ErrSplitBrain` → HTTP **503** with `Retry-After: 10`. A minority partition cannot accept divergent configuration. |
| **Probing continues** | It keeps probing and gossiping — so the moment it rejoins a healthy majority it has fresh data and recovers automatically. |

> Split-brain therefore produces **silence, not false alarms** — and no conflicting config writes that would later have to be reconciled.

## Why majority, not "any peer"

If two nodes were enough to alert, a 5-node cluster splitting 2/3 would have *both* halves alerting on each other's targets. Requiring a strict majority means at most one partition can ever be authoritative; the minority goes quiet.

## Tuning

| Field | Guidance |
|---|---|
| `expected_node_count` | Set to your real steady-state cluster size. Too high → you can lose quorum during normal maintenance. |
| `min_quorum_ratio` | `0.5` (majority) is the safe default. Lower it only if you understand the split-brain trade-off. |

## Interaction with alerting

Quorum is the **first gate** in the alert decision: even if a node is the responsible prober and sees a real `hard_down`, an isolated node will not alert. The full decision chain is in [Exactly-once alerting](exactly-once-alerting).

## Metrics

| Metric | Meaning |
|---|---|
| `network_prober_quorum_healthy` | `1` = quorum intact, `0` = lost. |
| `network_prober_isolated` | `1` = isolated mode (alerts suppressed). |
| `network_prober_cluster_size` | Alive members this node currently sees. |
