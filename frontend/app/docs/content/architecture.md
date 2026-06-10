# Architecture

netwatch is two deployable pieces and one embedded store:

- **Backend agent** — a single Go binary (`cmd/linux`). Runs probe loops, the gossip cluster, the state machine, alerting, an HTTP API and a Prometheus `/metrics` endpoint.
- **Frontend** — a Nuxt single-page app, built to static files and served by nginx. It talks to any backend node over the REST API. No Node.js runtime in production.
- **Embedded storage** — a per-node SQLite database (`netwatch.db`) holding all dynamic configuration and history, replicated across the cluster by gossip.

```
 Browser ── Nuxt SPA (static, nginx) ──HTTP──►  any backend node :10240
                                                      │
        ┌─────────────────────────────────────────────┼─────────────────────┐
        ▼                       ▼                       ▼
   ┌─────────┐            ┌─────────┐            ┌─────────┐
   │ node-1  │◄──gossip──►│ node-2  │◄──gossip──►│ node-3  │   (UDP/TCP :7946)
   │ SQLite  │            │ SQLite  │            │ SQLite  │
   └─────────┘            └─────────┘            └─────────┘
```

Every node serves the **same** cluster-wide view, so the frontend can connect to any of them.

## Backend internals

The Go code is organised into three packages, layered so the upper layers never depend on transport details:

| Package | Responsibility |
|---|---|
| `internal/engine` | Probe loops, the state machine, alerting/notifications, apps, SLO, topology, scope classification, the HTTP handlers' business logic. |
| `internal/cluster` | Gossip (`memberlist`), the hash ring, quorum, anti-entropy, prober selection. |
| `internal/storage` | The `StorageBackend` interface, the SQLite implementation, and the gossip-replicated wrapper with LWW conflict resolution. |

The engine talks to storage through an **interface**, and to the cluster through small callback interfaces (`AntiEntropyProvider`, `ProberAssignmentListener`, …). This keeps the layers swappable — e.g. the storage backend could become Raft-based later without touching the engine.

## The probe path (per target, per node)

1. On init the engine loads targets from storage and, for each target this node is **assigned** to probe, starts a goroutine (`startProbeLoop`).
2. The goroutine runs the target's `Checker` (tcp/http/ping/dns/sql) every `probe_interval_sec`.
3. On failure it enters the **state machine**: `up → soft_down` (retrying) → `hard_down` after `max_retries`. On success from a down state it recovers (optionally after `recovery_probes`).
4. Every transition bumps a per-target **Lamport sequence** and is broadcast to the cluster over gossip, and persisted to disk (`state.json` / `target_states`).
5. The **responsible** node (and only it) decides whether to dispatch an alert, after consulting the cluster-wide view (scope classification, quorum, confirmations).

> In a cluster a target is probed by `probe_replication_factor` nodes, not all of them — see [Distributed Probe Ownership](distributed-probe-ownership).

## The configuration path (write once, converge everywhere)

Dynamic config — targets, apps, channels, SLO targets, silences, maintenance windows, users — lives in SQLite, **not** in `config.yaml` at runtime.

- `config.yaml` is a **bootstrap + first-boot seed**. On the very first start with an empty database, its `targets:`, `apps:`, etc. are written into the DB once. After that the DB is authoritative and those sections of `config.yaml` are ignored.
- Writes go through the storage layer, which stamps a last-writer-wins version and **broadcasts the change to every peer over gossip**. Peers apply it (LWW) and reconcile their live state.

The full mechanics — every function call, the LWW stamp, and a live demonstration — are in [How a target propagates](gossip-propagation).

## State and durability

| Data | Where | Replicated? |
|---|---|---|
| Real-time probe state | gossip + `target_states` | gossip (consensus), local persistence |
| Targets / apps / channels / SLO / silences / maintenance / users | SQLite | gossip (LWW) |
| SLO incidents | SQLite (local-only) | no — each node keeps its own observations |
| Probe history / alerts | SQLite | reserved for anti-entropy sync |

Persisting probe state is deliberate: after a restart or rejoin, a node reconciles via **anti-entropy** before alerting, so a rolling restart of the whole fleet does not produce a storm of phantom recovery/outage alerts.

## Ports

| Port | Protocol | Purpose |
|---|---|---|
| `10240` | TCP/HTTP | REST API, `/metrics`, `/health` |
| `7946` | TCP **and** UDP | gossip cluster (memberlist) |
