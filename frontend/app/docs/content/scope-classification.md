# Scope classification

When a target is down, the most important question is *"down for whom?"* A single observer can't tell a real outage from its own broken uplink. Because netwatch probes from several nodes and shares results over gossip, it compares every node's vote and **classifies** the event. The classification is attached to every alert (`SCOPE`, `CLASSIFICATION`, `CONFIDENCE` and the node lists).

## The four classifications

Computed in `classifyScope(targetID)` from the per-node votes:

| Classification | Condition | What it usually means |
|---|---|---|
| **REAL_OUTAGE** | Every node that can vote sees it `hard_down`, and no probers are silent. | The service is genuinely down. Page someone. |
| **NETWORK_PARTITION** | Some nodes see it up, others down. | A network split between part of the cluster and the target — not necessarily a service failure. |
| **LOCAL_FAILURE** | Only the alerting node sees it down; others see it up. | Almost certainly that one node's own connectivity, not the target. |
| **AMBIGUOUS** | Not enough nodes have reported to decide. | Wait for more votes; low confidence. |

## Scope vs classification

Two related fields travel with every alert:

- **`SCOPE`** — `GLOBAL` / `PARTIAL` / `NODE_LOCAL` / `STANDALONE`. A coarse blast-radius label.
- **`CLASSIFICATION`** — the richer judgement above, plus a **`CONFIDENCE`** score from `0.00` to `1.00`.

In standalone mode (`cluster.enabled: false`) there is only one observer, so scope is `STANDALONE` and classification can't distinguish local from real — there's nothing to compare against.

## The node lists

The classification is backed by the exact votes, exposed as alert env vars and in `/fleet/status`:

| Field | Meaning |
|---|---|
| `DOWN_NODES` | Nodes that currently report the target `hard_down`. |
| `UP_NODES` | Nodes that report it `up`. |
| `OFFLINE_NODES` | Nodes that are alive in the cluster but have **not** reported on this target (silent probers) — they lower confidence. |

## Why this matters

Without scope classification, three nodes seeing a target down would either page three times (alert storm) or page once but with no idea whether the service died or one node's switch hiccupped. With it:

- A `LOCAL_FAILURE` can be suppressed or routed differently — you don't wake the on-call for one prober's bad NIC.
- A `NETWORK_PARTITION` tells you to look at the network path, not the service.
- A `REAL_OUTAGE` with high confidence is the page that matters.

## Where you see it

- **Alerts** — every script/webhook/mail alert receives `SCOPE`, `CLASSIFICATION`, `CONFIDENCE`, `DOWN_NODES`, `UP_NODES`, `OFFLINE_NODES`. See the [Alert env reference](alert-env).
- **UI** — the target detail page and the Cluster Overview show the classification and the by-node breakdown.
- **API** — `GET /fleet/status` returns `classification` and `confidence` per target.
