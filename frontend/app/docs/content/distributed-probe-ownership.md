# Distributed Probe Ownership

This is netwatch's defining design idea. In a cluster, **every node does not probe every target.** For each target a small, deterministic subset of nodes is selected to probe it; the rest stay quiet and only consume gossip. This page documents the exact selection algorithm, the maths, and the edge cases.

## The problem it solves

Redundant monitoring naively means "every node probes every target". With 50 nodes that hits each database, API and DNS server **50 times per interval** — the monitor becomes an accidental DoS, and a struggling target is pushed over the edge by health checks alone. At the same time you still want several independent vantage points to tell a real outage from one node's bad uplink. netwatch bounds the prober count *and* spreads probers across failure domains.

## The selection algorithm, step by step

`SelectProbers(targetID)` is a pure function of gossiped state, run identically on every node:

### 1. Candidate set — `CandidatesFor(targetID)`

Every **alive** node that knows the target (it is in their storage/config), discovered from `peerStates` plus the local provider. The set is then narrowed by any pins:

- `probe_from: [...]` → intersect with these node names.
- `probe_from_regions: [...]` → keep only nodes in these regions.

The candidate list is returned in a **deterministic order** (sorted), so every node builds the identical list.

### 2. Rotation — `hashCandidateOrder`

The candidate list is **rotated** (not re-sorted) by a target-dependent offset:

```
start = FNV‑32a(targetID) mod N           # N = candidate count
order = candidates[start:] + candidates[:start]
```

A rotation, rather than a hash-sort, keeps the list deterministic and stable while making a *different* node the natural "first" for each target — so ownership and the primary role spread evenly across the fleet instead of always landing on the same node.

### 3. Zone-aware pick — `zoneAwarePick(order, factor)`

Walk the rotated list and select `factor` nodes by a strict 3-tier rule:

- **Tier 1 — zone diversity:** the first node of each *distinct* zone (in rotation order). Guarantees redundancy across failure domains.
- **Tier 2 — zone repeat:** if there are fewer distinct zones than `factor`, fill from the remaining zone-tagged nodes.
- **Tier 3 — zone-less fallback:** nodes with no `zone` label are chosen only when nothing else is left.

The **primary** (the responsible node for [exactly-once alerting](exactly-once-alerting)) is `picked[0]`.

If the candidate count is `≤ factor`, **all** candidates probe and zones are irrelevant — small clusters keep the simple "everyone probes" behaviour for free.

> **Worked example** — 5 candidates `[a,b,c,d,e]`, zones `a,b→eu`, `c,d→us`, `e→none`, `factor=3`, and `FNV(targetID)%5 = 2` (rotation starts at `c`):
> rotated order = `[c,d,e,a,b]`. Tier 1 picks `c` (us), then `e`… wait — `e` is zone-less, deferred to tier 3; next zone-tagged is `a` (eu). So Tier 1 = `c (us), a (eu)`. Only two zones exist, factor is 3 → Tier 2 adds the next zone-tagged node `d`. Result: **`[c, a, d]`**, primary `c`. `e` (zone-less) is never chosen because two zones filled the budget.

## How many probers: factor vs percent

Resolved by `effectiveReplicationFactor(candidates)`:

- **`probe_replication_factor`** *(default 3; `0`→3)* — a fixed number. Larger than the cluster ⇒ everyone probes (legacy behaviour).
- **`probe_replication_percent`** *(0 = off)* — `ceil(percent/100 × candidates)`, never below 1; **overrides** the factor when `> 0`. Use it on large clusters where a constant is awkward.

| candidates | `factor: 3` | `percent: 10` | `percent: 50` |
|---|---|---|---|
| 100 | 3 | 10 | 50 |
| 20 | 3 | 2 | 10 |
| 3 | 3 (all) | 1 | 2 |

Both are gossip-synced shared config, so every node computes the same count.

## Probe staggering

When a target has `N` probers, the prober at sorted index `i` delays its first probe by:

```
offset(i) = i × (probe_interval / N)
```

then ticks normally every `probe_interval`. So prober 0 probes at `T, T+I, …`; prober 1 at `T+I/N, …`; etc. **No burst on the target, and mean detection latency drops from `I` to ≈ `I/N`.** Standalone mode applies no stagger.

## Pinning probers

```yaml
targets:
  - id: "iran-vpn"
    type: tcp
    target: "10.50.50.10:443"
    probe_from: ["frankfurt-1", "istanbul-1"]   # only these are candidates
    # or: probe_from_regions: ["eu-west"]
```

> **Contract:** every node that carries the same target must declare the same `probe_from`. Otherwise candidate sets differ between nodes, the algorithm diverges, and the exactly-once guarantee breaks.

## Recomputation & stability

Assignments are recomputed when membership changes (`NotifyJoin`/`Leave`/`Update`) or when a new peer's targets first appear — but **debounced by 5 seconds** (`scheduleRecompute`) so a flapping cluster doesn't trigger a recompute storm. On a transition the engine gets `StartProbing`/`StopProbing` callbacks only for the targets whose ownership actually changed on this node. At startup, `SeedProberAssignments` registers the already-running loops so the first reactive recompute is a no-op.

Because selection is a pure function of the gossiped candidate set, **all nodes agree on who probes what with zero coordination** — and that deterministic agreement is precisely what makes exactly-once alerting and clean failover possible.

## Edge cases

- **Membership flux** — during the seconds a join/leave is propagating, two nodes may briefly compute different candidate sets and therefore different probers. This self-corrects as gossip converges; the 5 s debounce keeps it from thrashing. A target is never *un*-probed for long.
- **Orphaned target** — if a `probe_from`/`zone` misconfiguration leaves a target with zero candidates, no node probes it. `network_probe_target_orphaned{...}=1` and an edge-triggered log fire so you notice.
- **Zones fewer than factor** — Tier 2 fills the remainder; you still get `factor` probers, just not all in distinct zones.

## Observability

| Metric | Meaning |
|---|---|
| `network_probe_local_assigned{name,target,type}` | `1` if this node probes the target. |
| `network_probe_prober_count{name,target,type}` | Number of nodes selected. |
| `network_probe_target_orphaned{...}` | `1` if **no** node probes the target. |
| `network_probe_inventory_peers` | Distinct peers seen via gossip. |

`GET /cluster/probers` returns the full per-target assignment (selected probers, primary, candidate set, zones, effective factor) — rendered by the UI's Cluster view.
