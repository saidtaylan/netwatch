# Introduction

**netwatch** is a self-hosted, distributed network monitoring system. Each node is a single Go binary that continuously probes TCP, HTTP/HTTPS, ICMP ping, DNS and SQL targets, and exposes the results over a REST API and Prometheus metrics. Multiple nodes form a **leaderless gossip cluster**: they share probe state in real time and coordinate so that every outage produces exactly one alert — never duplicates — no matter how many nodes watch the same target.

This documentation is split into two halves:

- **For operators** — how to use the UI, configure targets, read alerts. Start with the [Architecture](architecture) page and the UI guide.
- **For engineers** — every design decision, parameter and internal mechanism is documented in depth, like the [How a target propagates](gossip-propagation) deep dive and the [Configuration reference](config-reference).

## Why netwatch exists

Most monitoring tools work fine with **one** watcher and **one** target. The hard problems begin when you put **many** watchers in front of **shared** targets across **multiple** regions. netwatch is built specifically around those problems.

### Many watchers must not DoS the target

The naive way to get redundant monitoring is to have every node probe every target. In a 50-node cluster each database, API and DNS server is then hit 50× per interval — your monitoring becomes an accidental denial-of-service attack on the very things it protects. netwatch instead treats *"who probes what"* as a **distributed ownership problem**: only a small, deterministic subset of nodes probes each target. See [Distributed Probe Ownership](distributed-probe-ownership).

### Many watchers must not cause alert storms

If three nodes all notice the database is down, you want **one** alert, not three (or fifty). A consistent-hash ring elects exactly one responsible node per target; only that node dispatches the alert, and ownership reassigns automatically on failure.

### "Is it really down, or is it just me?"

Because netwatch probes from several nodes and shares results over gossip, it can compare what every node sees and **classify** each event as a real outage, a network partition, or a single node's local failure — so you stop waking people up for one probe node's switch hiccup.

### No external database, no leader, no babysitting

There is no Prometheus server, no Alertmanager, no etcd, no elected leader and no shared SQL database to keep alive. Every node is one binary with its own embedded SQLite file; the cluster is leaderless gossip (`hashicorp/memberlist`). Dynamic configuration (targets, apps, channels, SLOs, users) is replicated across the cluster over gossip with last-writer-wins conflict resolution.

## Core concepts at a glance

| Term | Meaning |
|---|---|
| **Target** | A monitored endpoint (TCP/HTTP/ping/DNS/SQL). Has a unique `id`, a `type` and type-specific `options`. |
| **Probe** | A single health check against a target. Each assigned node probes on a schedule. |
| **App** | A logical grouping of targets with an owner team. Purely a labelling/ownership overlay — it does **not** create extra probes. |
| **Node** | One netwatch agent process. Nodes form the cluster. |
| **Consensus state** | The cluster's merged view of a target: `up`, `soft_down` (pending retries), `hard_down` (confirmed), recovering. |
| **Prober** | A node currently assigned to probe a given target. |
| **Quorum** | The minimum fraction of nodes that must be alive before alerts may fire — protects against split-brain. |
| **Gossip** | The UDP/TCP protocol nodes use to share state and replicate config. |
| **LWW** | Last-writer-wins: how conflicting writes are resolved deterministically (`seq` → `updated_at` → `updated_by`). |

## What to read next

- **[Architecture](architecture)** — the components and how data flows from a probe to an alert.
- **[Distributed Probe Ownership](distributed-probe-ownership)** — the single most important design idea.
- **[How a target propagates](gossip-propagation)** — a line-by-line trace of replication across the cluster.
- **[Configuration reference](config-reference)** — every `config.yaml` field.
