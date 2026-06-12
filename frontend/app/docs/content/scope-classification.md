# Scope classification

When a target is down, the most important question is *"down for whom?"* A single observer can't tell a real outage from its own broken uplink. Because netwatch probes from several nodes and shares results over gossip, it compares every node's vote and **classifies** the event with a confidence score. This page documents the exact decision tree (`classifyScope`).

## The votes

For a target, each node's last-known state is a vote. The classifier buckets every cluster member into one of three lists:

| Bucket | Definition |
|---|---|
| `DOWN_NODES` | Nodes whose gossiped state is `hard_down`. |
| `UP_NODES` | Nodes whose state is `up`. |
| `OFFLINE_NODES` | Alive members that have **not reported** on this target at all (silent probers). |

Let `down`, `up`, `offline` be the counts, `totalKnown = down + up`, and `clusterSize` the alive member count. The decision is made purely from these numbers.

## The decision tree (cluster mode)

Evaluated top to bottom; the first matching rule wins:

| # | Condition | Scope | Classification | Confidence |
|---|---|---|---|---|
| 1 | `totalKnown == 0` (nobody has reported) | STANDALONE / NODE_LOCAL¹ | **AMBIGUOUS** | `0.4` |
| 2 | `up == 0` **and** `offline == 0` (every node that can vote says down, none silent) | GLOBAL | **REAL_OUTAGE** | `1.0` |
| 3 | `up == 0` **and** `offline > 0` (all reporters down, but some silent) | GLOBAL | **AMBIGUOUS** | `down / clusterSize`, capped at `0.95` |
| 4 | `down == 1` **and** the lone down node is *this* node **and** `up > 0` | NODE_LOCAL | **LOCAL_FAILURE** | `up / totalKnown` |
| 5 | `down > 0` **and** `up > 0` (mixed) | PARTIAL | **NETWORK_PARTITION** | see formula below |

¹ NODE_LOCAL when some members are silent, else STANDALONE.

### The key subtlety: silent nodes block REAL_OUTAGE

Rule 2 vs rule 3 is the heart of it. "Every node that reported says down" is **only** a REAL_OUTAGE if there are **no silent nodes** — because a silent node might see the target differently. If any prober hasn't weighed in yet, the same all-down picture is downgraded to **AMBIGUOUS** with confidence proportional to how much of the cluster actually confirmed it. This is what stops a premature "global outage!" page while the cluster is still gathering votes.

### Partition confidence formula

For a mixed result (rule 5), confidence rewards a *clean* split and penalises a lopsided one:

```
ratio    = down / totalKnown
symmetry = 1 − |ratio − 0.5| / 0.5      # 1.0 at a perfect 50/50, 0 at all-or-nothing
conf     = 0.5 + 0.5 × symmetry
```

A balanced split (half see it down, half up) is the strongest partition signal (`conf → 1.0`); one node disagreeing with many is weak. A single *remote* node reporting down (not this node) is treated as an especially weak signal and pinned to `conf = 0.6`.

## Standalone mode

With `cluster.enabled: false` there is exactly one observer, so there is nothing to compare against:

- Not yet probed → STANDALONE / AMBIGUOUS / `0.5`.
- Known state → classification is always **LOCAL_FAILURE** (`1.0`); scope is `NODE_LOCAL` when down, `STANDALONE` when up. The honest meaning: "this is just what *I* see; I can't tell you if it's global."

## Scope vs classification

Two fields travel with every alert:

- **`SCOPE`** — `GLOBAL` / `PARTIAL` / `NODE_LOCAL` / `STANDALONE`: a coarse blast-radius label.
- **`CLASSIFICATION`** + **`CONFIDENCE`** — the richer judgement and how sure we are, from the table above.

Plus the raw evidence: `DOWN_NODES`, `UP_NODES`, `OFFLINE_NODES`.

## Why it matters

- A **LOCAL_FAILURE** can be dropped or routed differently — don't wake the on-call for one prober's bad NIC. (This also pairs with the [confirmations exemption](exactly-once-alerting) in the alert gate.)
- A **NETWORK_PARTITION** points you at the network path, not the service.
- A **REAL_OUTAGE** at confidence `1.0` is the page that matters — and you know it's `1.0` precisely *because* every prober agreed and none were silent.

## Where you see it

- **Alerts** — `SCOPE`, `CLASSIFICATION`, `CONFIDENCE`, `DOWN_NODES`, `UP_NODES`, `OFFLINE_NODES` in every channel. See [Alert env](alert-env).
- **API** — `GET /fleet/status` returns `classification` and `confidence` per target.
- **UI** — the target detail and Cluster Overview render the classification and the by-node breakdown.
