# Distributed Probe Ownership

This is netwatch's defining design idea. In a cluster, **every node does not probe every target.** Instead, for each target a small, deterministic subset of nodes is selected to probe it; the rest stay quiet and only consume gossip.

## The problem it solves

Redundant monitoring naively means "every node probes every target". With 50 nodes that hits each database, API and DNS server **50 times per interval** — the monitor becomes an accidental DoS, and a struggling target is pushed over the edge by health checks alone. At the same time, you still want multiple independent vantage points so you can tell a real outage from one node's bad uplink.

netwatch resolves the tension by bounding the number of probers per target while spreading them across failure domains.

## How probers are selected

For a given target, selection happens in `cluster.SelectProbers(targetID)`:

1. **Candidate set** — every *alive* node that knows the target (it is in their config / storage), built in `CandidatesFor`. If the target pins `probe_from`, the candidates are intersected with that list.
2. **Deterministic ordering** — candidates are ordered by hashing `(targetID, nodeName)` with FNV-32a (`hashCandidateOrder`). Every node computes the identical order from the same candidate set — no coordination needed.
3. **Zone-aware pick** — the first `factor` nodes are chosen by a 3-tier rule (`zoneAwarePick`):
   - **Tier 1 — zone diversity:** walk the ordered list and take the first node of each *distinct* zone. This guarantees redundancy across failure domains (data centres / AZs).
   - **Tier 2 — zone repeat:** if there are fewer distinct zones than `factor`, fill the rest from remaining zone-tagged nodes.
   - **Tier 3 — zone-less fallback:** finally fill from nodes without a zone label.

If the candidate count is `≤ factor`, **all** candidates probe and zones don't matter — small clusters keep the simple "everyone probes" behaviour for free.

> Because the result is a pure function of the gossiped candidate set, **all nodes agree on who probes what** without talking to each other. That deterministic agreement is what makes exactly-once alerting possible.

## How many probers: factor vs percent

Two mutually-exclusive ways to set the count (`effectiveReplicationFactor`):

- **`probe_replication_factor`** *(default 3)* — a fixed number. `0` means the default of 3. Setting it larger than the cluster (e.g. `999`) restores legacy "every node probes" behaviour.
- **`probe_replication_percent`** *(0 = off)* — express it as a **percentage of the candidate nodes**, for large clusters where a constant is awkward. When `> 0` it overrides the fixed factor. The effective count is `ceil(percent/100 × candidates)`, never below 1.

| candidates | `factor: 3` | `percent: 10` | `percent: 50` |
|---|---|---|---|
| 100 | 3 | 10 | 50 |
| 20 | 3 | 2 | 10 |
| 3 | 3 (all) | 1 | 2 |

Both are part of the gossip-synced shared config, so all nodes use the same setting and therefore compute the same prober sets.

## Pinning targets to specific nodes

Some targets can only be reached from certain nodes (a restricted VPN segment, a private subnet). Pin them:

```yaml
targets:
  - id: "iran-vpn"
    type: tcp
    target: "10.50.50.10:443"
    probe_from: ["frankfurt-1", "istanbul-1"]   # only these nodes are candidates
```

There is also `probe_from_regions: ["eu-west", ...]` to restrict by region label instead of explicit node names.

> **Contract:** every node that carries the same target must declare the same `probe_from`. Otherwise the candidate sets differ between nodes and the exactly-once guarantee breaks.

## Exactly-once alerting & failover

The same ring decides the **responsible** node (the primary in the ordered set). `IsResponsible(targetID)` gates alert dispatch, so only that one node alerts. When the responsible node leaves the cluster, a membership change triggers `updateRing()` and the next node in the order becomes responsible automatically — no elected leader, no failover script.

## Staggering

When a target has N probers, their probe schedules are offset by `(probe_interval / N) × prober_index`, so the N probes are spread across the interval instead of bursting together. Mean detection latency drops to roughly `probe_interval / N` while keeping per-tick load low.

## Recomputation

Prober assignments are recomputed when membership changes (`NotifyJoin`/`Leave`/`Update`) or when a new peer's targets appear, **debounced by 5 seconds** so a flapping cluster doesn't trigger a recompute storm. On a transition, the engine receives `StartProbing`/`StopProbing` callbacks for the targets that changed ownership on this node.

## Observability

| Metric | Meaning |
|---|---|
| `network_probe_local_assigned{name,target,type}` | `1` if this node is probing the target, else `0`. |
| `network_probe_prober_count{name,target,type}` | How many nodes were selected to probe the target. |
| `network_probe_target_orphaned{...}` | `1` if **no** node probes the target (usually a `probe_from` / `zone` misconfiguration). |
| `network_probe_inventory_peers` | Distinct peers seen via gossip. |

The `GET /cluster/probers` endpoint returns the full per-target assignment (selected probers, primary, candidate set, zones) — the UI's **Cluster** view renders it.
