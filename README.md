# netwatch

**netwatch** is a distributed network monitoring agent written in Go. It runs as a Prometheus exporter and continuously probes TCP, HTTP/HTTPS, ICMP, DNS, and SQL targets. Multiple instances form a gossip cluster that agrees on which node sends each alert — so the same outage never produces duplicate notifications regardless of how many agents are watching the same target.

---

## Features

| Capability | Description |
|---|---|
| **Multi-protocol probes** | TCP, HTTP/HTTPS (body + status assertions), ICMP ping, DNS, SQL (MySQL / PostgreSQL / SQL Server / Oracle) |
| **Smart state machine** | Transient failures cause a *soft-down* retry phase; only after `max_retries` failures is the target declared *hard-down* and an alert sent |
| **Exactly-once alerting** | In cluster mode, consistent hashing assigns a primary per target; only the responsible node sends the alert. Failover is automatic when the primary leaves the cluster. |
| **Distributed probe ownership** | Only `probe_replication_factor` (default 3) nodes probe each target. Zone-aware spread ensures geographic redundancy. A 50-node cluster does not hammer targets 50×. |
| **Active probe delegation** | Optional per-target `probe_from` list — pin probing to named nodes or regions for reachability, credentials, or geo-synthetic use cases |
| **Quorum gating** | If a cluster loses quorum (majority of nodes unreachable), alert dispatch is suppressed to avoid false positives from a split-brain node |
| **Anti-entropy re-join** | When a node restarts and rejoins the cluster, it syncs state before allowing new alerts — no duplicate notifications |
| **Dependency graph & root cause** | `depends_on` relationships between targets let the system tell you "payment-service is down because db-primary is down" instead of alerting on every affected target |
| **Scope classification** | Every alert carries `REAL_OUTAGE` / `NETWORK_PARTITION` / `LOCAL_FAILURE` / `AMBIGUOUS` — you know immediately whether it is the service or your network |
| **Fleet status** | `GET /fleet/status` gives a cluster-wide summary from any node, no central server needed |
| **SLO tracker** | Per-target uptime tracking with rolling windows (30d / 7d / 24h), error budget, and breach alerts — monthly SLO reports without extra tooling |
| **Config drift detection** | Each node gossips its config hash; if any node diverges, `network_probe_config_drift=1` and a log warning fires |
| **Geo latency view** | Per-region latency breakdown with anomaly detection — the foundation of self-hosted multi-region synthetic monitoring |
| **Alert channels** | Script (`.sh` / `.ps1`), SMTP mail (multipart HTML), generic JSON webhook, or Prometheus Alertmanager format |
| **App → target indirection** | Group targets under named *apps* with owner teams; `AFFECTED_APPS` and `OWNER_TEAMS` are injected into every alert |
| **Prometheus metrics** | `network_probe_local_status`, `network_probe_local_latency_seconds`, plus cluster, SLO, geo, and drift gauges |
| **Hot-reload** | `config.yaml` is re-read on a configurable interval — no restart required |
| **Credentials injection** | `${VAR}` placeholders in config are resolved from a separate `credentials.env` file |
| **Prometheus watchdog** | Detects when Prometheus stops scraping and logs a warning; probes continue regardless |
| **Lifecycle CLI** | `netwatch init`, `netwatch leave`, `netwatch uninstall` — manage installation and graceful cluster departure |
| **Windows Service** | Native Windows Service integration via `netwatch service install/remove` |
| **Kubernetes / Helm** | DaemonSet chart with `hostNetwork`, `NET_RAW`, gossip headless service |

---

## Quick Start

```bash
# 1. Build
make build-linux
# or: go build -o bin/netwatch ./cmd/linux/

# 2. Generate a config skeleton
./bin/netwatch init --config-dir /etc/netwatch

# 3. Edit the generated config
$EDITOR /etc/netwatch/config.yaml

# 4. Run
./bin/netwatch --config /etc/netwatch/config.yaml
```

```bash
# Check metrics
curl http://localhost:10240/metrics

# Check target states
curl http://localhost:10240/status

# Cluster state (returns 503 if cluster is disabled)
curl http://localhost:10240/cluster/state

# Rich fleet view
curl http://localhost:10240/fleet/status

# SLO status
curl http://localhost:10240/slo
```

---

## Installation

### Linux — systemd

```bash
# Build and install binary + unit file (requires root)
make build-linux install

# Or use the lifecycle command
sudo netwatch init --config-dir /etc/netwatch
sudo systemctl enable --now netwatch
```

The generated unit file enables `CAP_NET_RAW` for ICMP ping probes. Remove the `AmbientCapabilities` / `CapabilityBoundingSet` lines if you do not use `type: ping` targets.

### Windows — Service

```powershell
# Administrator PowerShell
netwatch.exe service install --config C:\netwatch\config.yaml
sc.exe start netwatch

# Remove
sc.exe stop netwatch
netwatch.exe service remove
```

### Docker

```bash
docker build -t netwatch .

docker run -d \
  -p 10240:10240 \
  -p 7946:7946 \
  -v /etc/netwatch/config.yaml:/etc/netwatch/config.yaml:ro \
  --cap-add NET_RAW \
  netwatch
```

### Kubernetes / Helm (DaemonSet)

```bash
# Install with default values
helm install netwatch ./helm/netwatch \
  --namespace monitoring --create-namespace \
  --set image.repository=your-registry/netwatch \
  --set image.tag=latest

# Install with custom config and keyring
helm install netwatch ./helm/netwatch \
  --namespace monitoring --create-namespace \
  -f my-values.yaml
```

The chart deploys a DaemonSet with:
- `hostNetwork: true` — gossip ports reachable between nodes; raw sockets for ping
- `CAP_NET_RAW` capability
- Headless Service for gossip peer discovery via DNS
- Downward API injection of `NODE_NAME` and `HOST_IP` into the config

---

## Configuration

See [`config.example.yaml`](config.example.yaml) for a fully annotated reference with all supported options and all probe types.

Key fields:

```yaml
app_name: "netwatch-agent"    # appears in metrics labels and alert env vars
port:     "10240"             # HTTP port
state_file: "/var/lib/netwatch/state.json"
log_path:   ""                # empty = stdout

timeout:             5        # per-probe timeout (seconds)
max_retries:         3        # soft-down retries before hard-down
retry_interval_sec: 30
probe_interval_sec: 60        # default probe interval; override per-target
reload_interval_sec: 30       # config hot-reload interval (0 = disabled)
watchdog_threshold_sec: 120   # 0 = watchdog disabled

credentials_file: "/etc/netwatch/credentials.env"  # ${VAR} source

notifications:
  ops-script:
    type: script
    parameters:
      script: "/etc/netwatch/notifications/alert.sh"

  email:
    type: mail
    parameters:
      smtp_host: "smtp.corp.local"
      smtp_port: "587"
      from: "netwatch@corp.local"
      to: "sre@corp.local"
      tls_mode: "starttls"

  alertmanager:
    type: webhook
    parameters:
      url: "http://alertmanager:9093/api/v2/alerts"
      format: "alertmanager"   # or "generic"

default_notify: ["ops-script"]

targets:
  - name: "payments-db"
    type: tcp
    target: "db.prod:5432"
    notify: ["email"]

  - name: "payment-api"
    type: http
    target: "https://api.payments.prod/health"
    options:
      expected_status:
        in: [200, 204]
      body_contains: '"status":"ok"'
```

### Credentials file

```env
# /etc/netwatch/credentials.env
SMTP_PASS=s3cr3t
PG_PASS=dbpass
SLACK_WEBHOOK_URL=https://hooks.slack.com/...
```

`${VAR}` placeholders in `config.yaml` are resolved from this file (or from environment variables as fallback).

---

## Probe Types

### tcp
```yaml
type: tcp
target: "host:port"
```

### http / https
```yaml
type: http
target: "https://host/path"
options:
  method: "GET"                     # GET (default) | POST | PUT | ...
  expected_status:
    in: [200, 204]                  # in / lt / lte / gt / gte / between
  body_contains: '"ok":true'
  body_not_contains: '"error"'
  follow_redirects: true
  headers:
    Authorization: "Bearer ${TOKEN}"
```

### ping (ICMP)
Requires `CAP_NET_RAW` or root.
```yaml
type: ping
target: "10.0.0.1"
options:
  count: 3
  timeout_sec: 2
```

### dns
```yaml
type: dns
target: "api.corp.local"
options:
  nameserver: "10.0.0.53:53"   # optional; OS resolver used if omitted
  expected_ips:
    - "10.0.1.10"
```

### sql
```yaml
type: sql
target: "host:port"
options:
  driver:   "postgres"         # postgres | mysql | mssql | oracle
  username: "${DB_USER}"
  password: "${DB_PASS}"
  database: "mydb"
  ssl_mode: "require"          # postgres only
  query:    "SELECT 1"         # optional validation query
  # service_name: "ORCLPDB1"  # oracle only
```

---

## App → Target Indirection

Group targets under named services to inject ownership context into alerts:

```yaml
apps:
  - name: "payment-gateway"
    owner_team: "fintech-sre"
    uses:
      - "payments-db"      # target id or name
      - "payment-api"
    notifications:
      - "email"            # union with target.notify

  - name: "inventory"
    owner_team: "logistics"
    uses: ["inventory-mysql"]
```

When an alert fires for `payments-db`, every channel receives:
```
AFFECTED_APPS=payment-gateway
OWNER_TEAMS=fintech-sre
```

Notification channels are merged: `union(target.notify, app.notifications)` — deduplicated. If both are empty, `default_notify` applies.

---

## Dependency Graph & Root Cause Detection

Declare relationships between targets so the system can trace failures back to their source.

```yaml
targets:
  - id: "infra-switch"
    type: ping
    target: "10.0.0.1"

  - id: "db-primary"
    type: tcp
    target: "db.prod:5432"
    depends_on: ["infra-switch"]

  - id: "payment-api"
    type: http
    target: "https://payments.prod/health"
    depends_on: ["db-primary"]
```

When `payment-api` goes down, the alert contains:

```
ROOT_CAUSE=infra-switch
CASCADING_IMPACT=db-primary,payment-api
DEPENDENCY_DEPTH=2
```

Instead of receiving three alerts (one per failing target), you receive one alert with the full chain. The system walks the `depends_on` graph, finds the deepest failing dependency, and declares it the root cause.

**Endpoint:** `GET /topology` — returns the full dependency graph with `depends_on` and `depended_on_by` edges for every target.

**Validation:** cyclic dependencies and references to non-existent target IDs are caught at config load time.

---

## Scope Classification

Every alert carries a `CLASSIFICATION` field that tells you what kind of failure you are looking at.

| Classification | Meaning |
|---|---|
| `REAL_OUTAGE` | All cluster nodes see the target as down, and all nodes are online. The target itself is the problem. |
| `NETWORK_PARTITION` | Some nodes see the target as up, others as down. Your network has a partition; the target may be fine. |
| `LOCAL_FAILURE` | Only this node sees the target as down; all other nodes see it as up. The problem is local to this node's network path. |
| `AMBIGUOUS` | Not enough data to classify. Some nodes are offline, making it impossible to distinguish partition from outage. |

Alert environment variables:
```
SCOPE=PARTIAL
CLASSIFICATION=NETWORK_PARTITION
CONFIDENCE=0.85
DOWN_NODES=node-1,node-2
UP_NODES=node-3
OFFLINE_NODES=
```

`CONFIDENCE` is a 0.0–1.0 value. `REAL_OUTAGE` with all nodes online and all nodes seeing down is `1.00`. `AMBIGUOUS` with only one data point might be `0.33`.

---

## SLO Tracker

Track service-level objectives per target with rolling time windows and automatic breach alerts.

```yaml
slo:
  enabled: true
  retention_days: 90
  slo_notify: ["ops"]          # channel for breach alerts; falls back to default_notify
  targets:
    - id: "payments-db"
      target_uptime: 0.999     # 99.9%
      window: "30d"            # 30d | 7d | 24h
    - id: "payment-api"
      target_uptime: 0.9995
      window: "7d"
```

**Endpoint:** `GET /slo`
```json
{
  "targets": {
    "payments-db": {
      "target_uptime": 0.999,
      "actual_uptime": 0.9987,
      "downtime_minutes": 57,
      "incident_count": 3,
      "slo_breached": true,
      "remaining_error_budget_sec": -120
    }
  }
}
```

`GET /slo?format=text`:
```
TARGET          UPTIME   TARGET   STATUS    BUDGET REMAINING
payments-db     99.87%   99.90%   BREACHED  -2m 0s
payment-api     99.97%   99.95%   OK        +8m 38s
```

Incidents are persisted to `incidents.json` (same directory as `state.json`). Open incidents survive restarts. Breach alerts are edge-triggered: one alert per breach, cleared when the window recovers.

**SLO breach alert environment variables:**

| Variable | Value |
|---|---|
| `STATUS` | `slo_breached` |
| `SLO_TARGET_UPTIME` | e.g. `0.9990` |
| `SLO_ACTUAL_UPTIME` | e.g. `0.9981` |
| `SLO_WINDOW` | e.g. `30d` |
| `SLO_DOWNTIME_MINUTES` | total downtime in window |
| `SLO_INCIDENT_COUNT` | number of incidents |
| `SLO_ERROR_BUDGET_SEC` | remaining budget (negative = breach) |
| `SLO_LONGEST_INCIDENT_SEC` | longest single incident |

---

## Config Drift Detection

In a cluster, every node gossips its config hash. If any node loads a different config, it is immediately visible.

**Endpoint:** `GET /cluster/config`
```json
{
  "self": {"node": "node-1", "hash": "a1b2c3d4e5f6...", "loaded_at": "..."},
  "peers": [
    {"node": "node-2", "hash": "a1b2c3d4e5f6...", "in_sync": true},
    {"node": "node-3", "hash": "zzz999...", "in_sync": false}
  ],
  "drift_count": 1
}
```

**Metric:** `network_probe_config_drift` — `1` when any peer has a different hash, `0` when all agree.

This is purely a detection mechanism. Config is never auto-distributed; the operator is responsible for keeping configs consistent.

---

## Geo Latency View

When nodes are spread across regions, per-region latency data is available for every target.

```yaml
cluster:
  region: "eu-central"    # node-level geographic label
```

```yaml
targets:
  - id: "checkout-api"
    probe_from_regions: ["eu-central", "us-east"]  # only these regions probe
```

**Endpoint:** `GET /geo/latency/{targetID}`
```json
{
  "target_id": "checkout-api",
  "by_node": [
    {"node": "frankfurt-1", "region": "eu-central", "latency_sec": 0.042, "anomaly": false},
    {"node": "virginia-1",  "region": "us-east",    "latency_sec": 0.189, "anomaly": false}
  ],
  "anomaly": false
}
```

Anomaly is flagged when any node's latency exceeds 3× the minimum observed latency (requires at least two non-zero data points). This is the detection signal for degraded paths or regional routing problems.

---

## Alert Channels

### Script
Executes a shell script (`/bin/sh` on Linux, PowerShell on Windows) with all context as environment variables.

| Variable | Value | Always? |
|---|---|---|
| `NAME` | target name | ✓ |
| `TARGET` | host:port or URL | ✓ |
| `HOST`, `PORT` | parsed address parts | ✓ |
| `STATUS` | `unreachable` or `reachable` | ✓ |
| `TYPE` | tcp / http / ping / dns / sql | ✓ |
| `SEQ` | Lamport sequence number | ✓ |
| `ERROR_CODE` | last probe error text | ✓ |
| `NODE_NAME` | `os.Hostname()` | ✓ |
| `APP_NAME` | `app_name` config value | ✓ |
| `SCOPE` | GLOBAL / PARTIAL / NODE_LOCAL / STANDALONE | ✓ |
| `CLASSIFICATION` | REAL_OUTAGE / NETWORK_PARTITION / LOCAL_FAILURE / AMBIGUOUS | ✓ |
| `CONFIDENCE` | 0.00–1.00 | ✓ |
| `DOWN_NODES` | comma-separated node names that see the target as down | cluster |
| `UP_NODES` | comma-separated node names that see the target as up | cluster |
| `OFFLINE_NODES` | unreachable cluster members | cluster |
| `AFFECTED_APPS` | comma-separated app names | if apps configured |
| `OWNER_TEAMS` | comma-separated team names | if apps configured |
| `ROOT_CAUSE` | deepest failing dependency ID | if depends_on configured |
| `CASCADING_IMPACT` | comma-separated downstream target IDs | if depends_on configured |
| `DEPENDENCY_DEPTH` | hops from root cause to this target | if depends_on configured |

### Webhook — generic JSON
```json
{
  "name": "payments-db", "target": "db.prod:5432",
  "status": "unreachable", "type": "tcp",
  "seq": 3, "error_code": "connection refused",
  "affected_apps": "payment-gateway", "owner_teams": "fintech-sre",
  "fired_at": "2026-05-14T10:00:00Z"
}
```

### Webhook — Alertmanager format
Compatible with Prometheus Alertmanager v2 `/api/v2/alerts`. Recovery sends `endsAt = now` with `alertname = "ProbeUp"`.

---

## Prometheus Metrics

Default port: `10240`

| Metric | Type | Labels | Description |
|---|---|---|---|
| `network_probe_local_status` | Gauge | name, target, type, source_host, app_name | 1 = UP, 0 = DOWN on this node |
| `network_probe_local_latency_seconds` | Gauge | same | Last probe round-trip time |
| `network_probe_prometheus_connected` | Gauge | — | 1 = scraping normally, 0 = watchdog triggered |
| `network_probe_cluster_status` | Gauge | name, target, type, source_host, app_name | Consensus: 1 if all nodes UP, 0 if any node DOWN |
| `network_probe_local_assigned` | Gauge | name, target, type | 1 = this node is probing the target; 0 = another node was assigned |
| `network_probe_prober_count` | Gauge | name, target, type | Total nodes currently probing this target |
| `network_probe_inventory_peers` | Gauge | — | Distinct peers seen via gossip |
| `network_probe_target_orphaned` | Gauge | name, target, type | 1 = no node is probing this target (config mismatch / bad probe_from) |
| `network_prober_quorum_healthy` | Gauge | — | 1 = quorum present, 0 = quorum lost |
| `network_prober_isolated` | Gauge | — | 1 = this node is isolated (alerts suppressed) |
| `network_prober_cluster_size` | Gauge | — | Number of alive cluster members |
| `network_probe_config_drift` | Gauge | — | 1 = at least one peer has a different config hash |
| `network_probe_slo_uptime_ratio` | Gauge | target_id, window | Actual uptime ratio 0.0–1.0 in the rolling window |
| `network_probe_slo_error_budget_seconds` | Gauge | target_id, window | Remaining error budget in seconds (negative = breach) |
| `network_probe_slo_breached` | Gauge | target_id | 1 = SLO breach active |
| `network_probe_geo_latency_seconds` | Gauge | name, target, type, region | Last probe latency per node/region |
| `network_probe_geo_latency_anomaly` | Gauge | name, target, type | 1 = any node's latency exceeds 3× the minimum |

Cluster, SLO, and geo metrics are only registered when the respective feature is enabled — they do not appear in `/metrics` output when disabled.

---

## HTTP Endpoints

| Endpoint | Description |
|---|---|
| `GET /metrics` | Prometheus scrape — also notifies the watchdog |
| `GET /health` | Liveness check — always `200 OK` |
| `GET /status` | JSON: all targets with current state, seq, error_code |
| `GET /topology` | JSON: full dependency graph — `depends_on` and `depended_on_by` edges per target |
| `GET /fleet/status` | JSON: cluster-wide summary from any node — member list with zones, quorum/isolated flags, target counts, down target IDs |
| `GET /fleet/status?format=text` | Terminal-friendly ASCII table: cluster header, summary line, per-target state/scope/classification |
| `GET /slo` | JSON: per-target SLO status — uptime ratio, error budget, incident count, breach flag |
| `GET /slo?format=text` | Terminal-friendly table: TARGET%, ACTUAL%, STATUS, BUDGET REMAINING |
| `GET /cluster/state` | JSON: member list + raw peer target states; `503` if cluster disabled |
| `GET /cluster/probers` | JSON: per-target prober assignments — selected probers, primary, candidate set, `probe_from` constraint, members with zones |
| `GET /cluster/config` | JSON: this node's config hash + all peer hashes + drift count; `503` if cluster disabled |
| `GET /geo/latency/{targetID}` | JSON: per-node latency with region labels and anomaly flag |
| `GET /cluster/keyring/rotate` | JSON: keyring status — key count and primary key prefix |
| `POST /cluster/keyring/rotate` | Zero-downtime AES key rotation: `{"action":"add|use|remove","key":"base64..."}` |
| `POST /cluster/leave` | Graceful cluster departure + process exit; `?reason=TEXT` optional |

---

## Cluster Setup

netwatch uses [hashicorp/memberlist](https://github.com/hashicorp/memberlist) for gossip-based cluster membership and state propagation.

### Minimal 3-node config

```yaml
# node-1
cluster:
  enabled: true
  node_name: "node-1"
  bind_port: 7946
  advertise_addr: "192.168.1.101"
  peers: ["192.168.1.102:7946", "192.168.1.103:7946"]
  keyring: ["c2VjcmV0a2V5MzJieXRlc2xvbmdoZXJlISE="]
  expected_node_count: 3
  min_quorum_ratio: 0.5
```

Open port `7946` (TCP + UDP) between all cluster members.

### How alerting works in cluster mode

1. A subset of nodes (default 3) probes each target — see "Distributed Probe Ownership" below.
2. State changes are broadcast via gossip — all nodes maintain a view of peer states.
3. Consistent hashing assigns a **primary** responsible node per target.
4. Only the primary calls `sendAlert()`.
5. If the primary leaves, the next node in the hash ring takes over automatically.
6. `SCOPE` env var in the alert: `GLOBAL` (all nodes see down) / `NODE_LOCAL` (one node sees down) / `PARTIAL`.

### Distributed Probe Ownership

By default the cluster automatically distributes probing — not every node probes every target. This avoids hammering the target server with N redundant probes from a 50-node cluster.

```yaml
cluster:
  probe_replication_factor: 3   # max nodes that probe any single target (default 3)
  zone: "istanbul"              # optional node label for zone-aware spread
```

**Selection logic:**

1. **Candidate set** — every alive node that has the target in its config. Derived from the same gossip stream that carries probe results; no extra messages.
2. **Hash ring** — sorted candidate list rotated by `FNV-32a(targetID)` so every node computes the same starting point, independently, with no coordination.
3. **3-tier zone-aware picker:**
   - Tier 1: pick one node per distinct zone (failure-domain redundancy)
   - Tier 2: if the factor isn't filled, take another zone-tagged node (a repeated zone is still preferred over a zoneless one)
   - Tier 3: zone-less nodes — strictly last resort
4. When candidate count ≤ factor, all candidates probe (small cluster behaviour — effectively everyone).

**Active Probe Delegation** — operator-controlled pinning. Override automatic selection per target:

```yaml
targets:
  - id: "vpn-frankfurt-only"
    type: tcp
    target: "10.50.50.10:443"
    probe_from: ["frankfurt-1", "frankfurt-2"]   # only these nodes probe
```

Useful when reachability or credentials are restricted to specific nodes, or for explicit multi-region synthetic monitoring. **Contract:** every node that carries the target must declare the same `probe_from` list — mismatched lists break exactly-once alerting.

**Observability:**

- `GET /cluster/probers` — see what was assigned and why (candidates, picks, zones, pins).
- `GET /fleet/status` — cluster-wide rollup with zone-tagged member list.
- `network_probe_local_assigned{...}` — Prometheus check from each node.
- `network_probe_target_orphaned{...}` — fires when no node is probing a target (bad `probe_from` / zone mismatch).

### Quorum

If fewer than `floor(expected_node_count × min_quorum_ratio) + 1` nodes are alive, alert dispatch is suppressed (`isolated=true`). This prevents a split-brain node from flooding alerts when it loses network connectivity.

### Anti-entropy on re-join

When a node restarts and rejoins the cluster, memberlist triggers a full push-pull state exchange. During this sync (`syncing=true`), all probe execution and alarm dispatch are paused. After sync completes, the node's `lastKnown` state matches the cluster's and no duplicate alerts are generated.

### Encryption

AES-128/192/256 keyring via memberlist. The first key encrypts; all keys decrypt — enabling zero-downtime key rotation (add new key → rolling restart → remove old key).

---

## State Machine

```
UNKNOWN → SOFT_DOWN (RAM, pending queue)
              │  probe still fails, max_retries not exhausted
              ▼
          HARD_DOWN (disk — state.json)
              │
UP ←──────────┘  markRecovered() — Seq++, alert fires

UP → SOFT_DOWN      enqueue() — first failure
SOFT_DOWN → HARD_DOWN  markHardDown() — Seq++, alert fires
```

`state.json` persists hard-down states across restarts, preventing spurious re-alerts after a process restart.

---

## Lifecycle Commands

```bash
# Scaffold config + systemd unit
netwatch init --config-dir /etc/netwatch

# Signal a running agent to leave the cluster gracefully
netwatch leave --reason "maintenance"

# Full uninstall (asks for confirmation)
netwatch uninstall

# Windows: register/remove Windows Service
netwatch.exe service install --config C:\netwatch\config.yaml
netwatch.exe service remove
```

---

## Binary Name Customization

The binary name (`netwatch`) propagates to: systemd service name, Windows service display name, log prefixes, and ICMP echo payload. To rename at build time:

```bash
make build-linux BINARY_NAME=myagent
# or
go build -ldflags "-X github.com/saidtaylan/netwatch/internal/engine.BinaryName=myagent" ./cmd/linux/
```

---

## Build & Test

```bash
# Build Linux binary
make build-linux

# Build Windows binary
make build-windows

# Run unit tests with race detector
make test

# Vet
make vet

# Everything
make all

# Docker image
make docker-build
```

**Note:** `go build ./...` fails on macOS because `cmd/windows/` imports Windows-only packages. Always use `make build-linux` or target `./cmd/linux/` directly on macOS/Linux.

---

## Project Structure

```
├── cmd/
│   ├── linux/main.go         # Signal handler, HTTP server, CLI subcommands
│   └── windows/main.go       # Windows Service integration (full endpoint parity)
├── internal/
│   ├── engine/               # All probe, state, alert, watchdog logic
│   │   ├── engine.go         # Config, Engine, state persistence, metrics
│   │   ├── loop.go           # Probe goroutines, retry loop, state machine
│   │   ├── notify.go         # Alert channel dispatch
│   │   ├── app.go            # App→Target indirection
│   │   ├── topology.go       # Dependency graph, FindRootCause, CascadingImpact
│   │   ├── fleet.go          # FleetSnapshot — /fleet/status aggregation
│   │   ├── scope.go          # classifyScope — REAL_OUTAGE / NETWORK_PARTITION / …
│   │   ├── slo.go            # SLO tracker, incidents.json, breach alerts
│   │   ├── webhook.go        # Webhook alerter (generic + alertmanager)
│   │   ├── mail.go           # SMTP alerter (HTML multipart)
│   │   ├── watchdog.go       # Prometheus scrape watchdog
│   │   ├── appinfo.go        # var BinaryName (ldflags override point)
│   │   └── {http,tcp,ping,dns,sql}.go  # Protocol checkers
│   └── cluster/              # Gossip cluster layer
│       ├── cluster.go        # Manager, memberlist, quorum, hash ring, anti-entropy
│       ├── probers.go        # Distributed probe ownership, zone-aware selection
│       ├── configsync.go     # Config hash gossip, drift detection
│       ├── geolat.go         # Geo latency snapshot, anomaly detection
│       └── testhelpers.go    # Test utilities
├── test/integration/
│   ├── comprehensive_test.go # 31 end-to-end tests covering all features
│   ├── standalone_test.go    # Standalone probe cycle + state migration
│   ├── cluster_test.go       # Exactly-once alerting, recovery
│   ├── antientropy_test.go   # Re-join without duplicate alerts
│   └── keyrotation_test.go   # AES key rotation
├── deploy/
│   └── netwatch.service      # Reference systemd unit file
├── helm/netwatch/            # Kubernetes DaemonSet Helm chart
├── notifications/            # Alert scripts go here (.sh / .ps1)
├── config.example.yaml       # Full annotated config reference
├── Makefile
└── Dockerfile
```

---

## Advanced: Cluster Internals, Distributed Probing, and Config Deep Dive

This section answers the "why" behind every cluster config field and explains how gossip communication and distributed probe assignment actually work.

---

### What is port 7946 and why does memberlist need it?

netwatch uses [hashicorp/memberlist](https://github.com/hashicorp/memberlist) for cluster communication. memberlist opens a single port — configured as `bind_port` — that listens on both TCP and UDP simultaneously. There is nothing special about 7946; it is simply the default memberlist port. You can use any available port.

TCP is used for reliable, full-state exchanges (called push/pull syncs). UDP is used for the fast, lightweight gossip that propagates changes across the cluster continuously. Both protocols serve different purposes and both run on the exact same port number.

When you set `bind_port: 7942` for node-2, that node opens `127.0.0.1:7942/tcp` and `127.0.0.1:7942/udp`. Other nodes reach it on that port for both gossip broadcasts and full state syncs.

---

### How does the peers list work? Do I have to update every node's config when I add a new node?

No. The peers list is only used once: at first join. It is a bootstrap hint, not a complete and always-current membership registry.

Here is what happens when a node starts:

1. The node calls `list.Join(peers)` with whatever addresses are in the config.
2. It only needs to successfully reach **one** peer. That peer shares its full membership table via a TCP push/pull sync.
3. From that point on, the new node knows about every other member — even ones not in its peers list.
4. The full cluster learns about the new node within seconds via UDP gossip.

This means you only need to list a few stable seed nodes (typically 2–3) in the peers list. If you have a 10-node cluster and add an 11th, you do not need to update any existing node's config. The new node just needs to reach any one current member.

`list.Join()` is explicitly designed to be best-effort. If a listed peer is down, memberlist logs the failure and moves on to the next address. If it contacted at least one, the join succeeds.

---

### Why does the peers list include the node itself?

It does not need to. memberlist ignores self-connections — attempting to connect to your own gossip address is a no-op. Including yourself is harmless but redundant.

The reason it appears in test configs is convenience: all three nodes share an identical peers list, which makes the config generation loop simpler. In production, listing yourself wastes a connection attempt on startup. The cleaner approach is to list only the other nodes.

---

### What does `bind_addr` do?

`bind_addr` tells the gossip socket which network interface to bind to. It controls where the node *listens*, not where it connects to others.

- `0.0.0.0` — listen on all network interfaces. This is the standard production choice.
- `127.0.0.1` — listen only on loopback. Useful for local testing where all nodes run on one machine.
- `192.168.1.10` — bind to a specific interface. Useful when a machine has multiple NICs.

`bind_addr` and `advertise_addr` work together:

- `bind_addr` is the interface the socket opens on.
- `advertise_addr` is the address the node tells others to use when reaching it.

On a machine with both a public IP (1.2.3.4) and an internal IP (10.0.0.5), you might bind on `0.0.0.0` but advertise `10.0.0.5` so that intra-cluster gossip stays on the internal network.

**Critical:** `advertise_port` must always equal `bind_port`. memberlist constructs UDP probe packets using the advertised address and port. If they differ, probes reach the wrong port, the target node never responds, and memberlist declares it dead — even while the process is running perfectly.

---

### How does `min_quorum_ratio` work? Do I need to change it on all nodes?

Each node evaluates quorum independently, on its own schedule (every 5 seconds), using only its own config values and its own membership view. There is no distributed consensus about what the quorum threshold is.

The formula is: **minimum alive nodes = floor(expected_node_count × min_quorum_ratio) + 1**

For `expected_node_count: 3` and `min_quorum_ratio: 0.5`:
- floor(3 × 0.5) + 1 = floor(1.5) + 1 = 1 + 1 = **2 nodes must be alive**

If alive nodes drop below 2, the node sets `isolated = true`, stops dispatching alerts, and logs `[CLUSTER] quorum lost`.

Because each node computes this independently, if nodes have *different* values, they can reach *different* conclusions about whether quorum exists. This is not catastrophic, but it is confusing.

Best practice: keep `expected_node_count` and `min_quorum_ratio` identical across all nodes.

---

### With 50 nodes and probe_replication_factor=3, do only 3 nodes probe each target? What do the other 47 do?

Yes — exactly 3 nodes probe each target. The other 47 are passive listeners: they receive probe results via gossip and update their internal state view, but they never open a connection to the target.

Here is the complete flow for a target going down:

1. The 3 designated prober nodes independently run their probe loops. Node A tries to connect to the target, gets "connection refused", and declares it `soft_down` after the first failure. After `max_retries` more failures it declares `hard_down` and broadcasts a `GossipPayload` via gossip.
2. Nodes B and C do the same independently. All 3 broadcast their own `hard_down` declaration.
3. The other 47 nodes receive these gossip messages via `NotifyMsg`. They store the state in their `peerStates` map. They do not start probing. They never open a connection to the target.
4. The consistent-hash ring determines which node is *primary* for this target. The primary checks: is the target `hard_down`? Is quorum healthy? Am I responsible? If yes to all three, it fires the alert. Once.
5. The other 49 nodes know about the outage via `peerStates` and can report it in `/fleet/status` and `/status` — but they do not alert.

The key insight: **probing is decoupled from alerting**. The 3 probers gather evidence. The hash-ring primary decides whether to alert based on that evidence.

If one of the 3 probers leaves the cluster, the assignment is recomputed on all nodes (triggered by `NotifyLeave`). The hash ring selects a replacement prober from the remaining candidates. The new prober begins its probe loop within `probe_interval_sec`.

The 47 non-probing nodes are not idle — they still probe other targets for which they are in the designated 3. In a 50-node cluster with 100 targets, each node probes roughly `100 × 3 / 50 = 6` targets (on average). The rest of the targets they know about via gossip.

---

### With 1000 targets in the cluster, how is the assignment matrix managed?

There is no central matrix. Each node computes its own assignments locally using the same deterministic hash ring algorithm. Because the algorithm is deterministic (sorted node list + FNV-32a hash), every node independently reaches the exact same conclusion about who probes what — with no coordination, no votes, no coordinator.

At runtime, each node iterates over its target list and calls `IsLocalProber(targetID)`. If true, it starts a probe loop for that target. If false, it skips probing and relies on gossip.

For 1000 targets with `probe_replication_factor=3`:

- **Memory:** Each node stores gossip state for all 1000 targets, with at most 3 node-entries per target. Total entries: `1000 × 3 = 3000`. Each entry is a small struct (~100 bytes). Total: ~300 KB per node — negligible.
- **Active probe goroutines per node:** On average, `1000 × 3 / N` where N is the number of alive nodes. In a 10-node cluster: 300 probe loops per node. In a 50-node cluster: 60 probe loops per node.
- **Recomputation on membership change:** When a node joins or leaves, every node calls `recomputeProberAssignments()`. This is a local CPU operation (sort + hash for each target), not a network operation. For 1000 targets it takes microseconds.

The `GET /cluster/probers` endpoint shows the current assignment for all targets — useful for verification after a topology change.

---

### Can I change `probe_replication_factor`? Do I have to update every node?

Yes, the value is configurable. And yes, **all nodes must use the same value**.

Here is why: the factor determines how many nodes the hash ring picks for each target. If node-1 uses `factor=3` and node-2 uses `factor=5`, they compute different prober sets:

```
node-1 thinks: target X → probers are [A, B, C]
node-2 thinks: target X → probers are [A, B, C, D, E]
```

Node-1 suppresses its probe of target X (not in its 3-set). Node-2 probes it (in its 5-set). The primary (say, node-A) fires the alert. But now nodes D and E also probe — potentially causing node-D to become primary after a topology change, leading to a second alert. Exactly-once alerting breaks.

**How to safely change the factor:**

1. If using a config management tool (Ansible, Kubernetes ConfigMap, Puppet): update the config in one place, push to all nodes, let hot-reload pick it up within `reload_interval_sec`. All nodes converge within one reload cycle.
2. If updating manually: update `config.yaml` on all nodes as quickly as possible. Hot-reload is driven by a ticker (`reload_interval_sec`), so within one tick all nodes pick up the new value. During the brief window where some nodes have the old factor and others have the new one, assignment can diverge — keep this window short. A few seconds of inconsistency during a config roll-out is acceptable; minutes are not.
3. No restart is required. Hot-reload handles it.

There is no configuration validation that catches mismatched factors between nodes — the system trusts that operators keep configs consistent. The `GET /cluster/probers` endpoint shows the current factor each node is using; checking it after a config change is good practice.

---

### What is the difference between `probe_from` and `probe_from_regions`?

Both restrict which nodes probe a target. The difference is specificity:

`probe_from` is exact: you name the node (by `node_name`). Only those nodes will probe. This is useful when specific nodes have credentials, firewall access, or VPN routes that others lack.

```yaml
probe_from: ["frankfurt-vpn-node", "amsterdam-vpn-node"]
```

`probe_from_regions` is broader: you name regions (by `cluster.region`), and any node tagged with one of those regions can probe. This is useful for geo-synthetic monitoring where you want coverage from multiple regions but do not want to name specific nodes (which may come and go).

```yaml
probe_from_regions: ["eu-central", "us-east"]
```

When `probe_from` is set, `probe_from_regions` is applied as a secondary filter on top. Both can be used together to restrict probing to named nodes within specific regions.

When neither is set, the zone-aware hash ring selects probers automatically based on `probe_replication_factor`.

**Orphan safety:** If `probe_from` lists nodes that are not alive, or `probe_from_regions` lists regions with no alive nodes, the target becomes *orphaned* — no node probes it. The `network_probe_target_orphaned` metric fires and the log emits a warning. This is intentional: a target that nobody is watching should be visible.

---

### What is TCP push/pull?

memberlist uses two independent mechanisms to propagate state:

**UDP gossip (fast path):** Every ~200ms, each node picks a few random peers and sends them a small UDP packet containing recent state changes. Each node fans out to multiple targets, so information spreads exponentially. This is the primary channel for rapid convergence.

**TCP push/pull (reconciliation path):** Every ~30 seconds, each node picks one random peer and opens a TCP connection. Both sides exchange their complete membership table and merge the result. This corrects inconsistencies that accumulated because UDP packets were dropped, reordered, or arrived out of order.

Together they give you gossip's key property: **eventual consistency without a central coordinator**. UDP gets you speed; TCP push/pull guarantees correctness over time.

---

### How do nodes actually communicate? Every possible case.

**1. Initial join** — A new node dials seed peers over TCP, exchanges full membership tables. The cluster learns about the new node within a few gossip rounds (under a second).

**2. Routine health monitoring** — Every ~1 second, each node UDP-pings one random member. Failed direct pings trigger indirect probing via two other nodes (to avoid false positives from point-to-point network blips). Sustained failure → node declared suspect → dead.

**3. Death declaration** — `NotifyLeave` fires in netwatch, triggering `updateRing()` — the hash ring is rebuilt without the dead node. Some targets may get new primary responsible nodes.

**4. Graceful leave (`POST /cluster/leave`)** — The departing node gossips its own death announcement. Other nodes receive it immediately rather than waiting for the suspicion timeout.

**5. State broadcast (netwatch-specific)** — When a target changes state, the engine calls `Broadcast()`, which queues a serialized `GossipPayload`. Peers receive it in `NotifyMsg`, parse it, and update `peerStates`. The responsible primary uses this shared state to decide whether an alert is justified.

**6. Anti-entropy on rejoin** — memberlist's join-time push/pull triggers `LocalState(join=true)` / `MergeRemoteState(join=true)`. netwatch reconciles probe state: if the cluster already knows `seq=5, alarm_sent=true`, the rejoining node updates its local state and does not re-fire the alert. The `syncing` flag suppresses alarm dispatch during this window.

**7. Key rotation** — All gossip is AES-encrypted via the keyring. Add the new key to the front, rolling-restart, remove the old key. During the transition, new and old nodes can communicate — new nodes encrypt with the new key but accept the old key for inbound messages.

---

### What is "fake-down"? Is "soft-down" what you mean?

"fake-down" is the **name of a test target** — not a state. It is called "fake" because nothing is actually listening on that port; the connection is intentionally refused to simulate a real service being down. It is a testing device, not a technical term.

The actual states in the system are:

**UP:** The last probe succeeded. The target is healthy. No alarm pending.

**SOFT_DOWN:** A probe failed but `max_retries` has not been exhausted yet. The failure is held in memory only — nothing is written to `state.json` and no alert is sent. This phase exists to absorb transient network blips. A single dropped packet should not wake someone at 3 AM.

**HARD_DOWN:** All retry attempts failed. The target is declared hard-down. The state is written to `state.json` and an alert is dispatched (subject to cluster quorum and responsibility checks).

State transitions:

```
UP → SOFT_DOWN    first probe failure, retries queued
SOFT_DOWN → UP    a retry succeeds (silent recovery, no alert)
SOFT_DOWN → HARD_DOWN    all retries exhausted → alert fired
HARD_DOWN → UP    probe succeeds again → "reachable" alert fired, Seq++
```

The `Seq` counter (Lamport sequence) increments on every HARD_DOWN and every recovery. It lets you correlate alert pairs: `seq=1` unreachable and `seq=2` reachable belong to the same incident.

---

### Does every node need an `app_name`? What is the difference between `app_name` and `apps`?

These are two completely separate concepts that happen to share a word.

**`app_name`** (top-level config field) is the *monitoring agent's identity*. It appears in Prometheus metric labels and in the `APP_NAME` environment variable of every alert script. Its purpose is to tell you *which agent* produced a given metric or alert when you have multiple netwatch instances monitoring different things. Every node should have one, but there is no requirement that different nodes share the same value.

**`apps`** (optional config section) is a *service ownership mapping*. An "app" here means "a named service with an owner team that depends on certain targets." When one of those targets goes down, the alert is enriched with `AFFECTED_APPS=payment-gateway` and `OWNER_TEAMS=fintech-sre` so the right team gets paged.

All nodes should carry the **same `apps` section** — the apps section is "the service catalog that every node in this cluster should know about." If nodes have different apps sections, the node that fires the alert uses its own catalog, which could produce different enrichment depending on which node happens to be primary.

---

### How does App → Target work exactly?

When a target transitions to HARD_DOWN, the engine looks up the target's ID in the `appIndex` (built from the `apps` section at config load time). It:

1. Collects all apps that reference this target via `uses`.
2. Builds the channel set: `union(target.notify, app.notifications for each matching app)`, deduplicated.
3. Falls back to `default_notify` if the union is empty.
4. Injects `AFFECTED_APPS` and `OWNER_TEAMS` into the alert environment.

A concrete example:

```yaml
targets:
  - id: "payments-db"
    notify: ["ops-pager"]

apps:
  - name: "payment-gateway"
    owner_team: "fintech-sre"
    uses: ["payments-db"]
    notifications: ["slack-fintech"]

  - name: "fraud-detection"
    owner_team: "security-team"
    uses: ["payments-db"]
    notifications: ["pagerduty-security"]
```

When `payments-db` goes down: channels fired = `{ops-pager, slack-fintech, pagerduty-security}`. `AFFECTED_APPS=payment-gateway,fraud-detection`. `OWNER_TEAMS=fintech-sre,security-team`.

---

### Does state.json cause false alerts for UP targets on restart?

Not for targets that are UP. On restart with no state.json, all targets start as UNKNOWN. A target that responds successfully is marked UP with no alert produced.

The storm scenario applies specifically to targets that were **already hard-down before the restart**: without `state.json`, the agent has no memory of having already sent the alarm. With `state.json`, it loads `alarm_sent=true` and does not re-alert until the target recovers and goes down again.

In cluster mode, anti-entropy handles this even when state.json is missing: the rejoining node receives the cluster's current state (including `alarm_sent=true, seq=5`) during the push/pull sync, and updates its local copy before allowing alert dispatch. The `syncing` flag suppresses alerting during this reconciliation window.

---

### Why both TCP and UDP? Is UDP not unreliable?

UDP is unreliable in the sense that individual packets can be dropped, reordered, or duplicated. But gossip protocols are specifically designed to tolerate packet loss — each piece of information is gossiped repeatedly to multiple peers, and mathematical convergence guarantees delivery over a few rounds even with substantial packet loss.

UDP is preferred for gossip because it has no connection setup overhead, no handshake latency, and works well at high fan-out. TCP is used where reliability genuinely matters: push/pull state sync, join messages, and explicit reliable broadcasts for important state transitions.

---

### Does a 100-node cluster create slowness or excessive load?

No, and the reason is gossip's fundamental property: each node's per-round work is constant regardless of cluster size.

Each gossip round, a node contacts a fixed number of peers (3 by default). It does not contact all N nodes. This means adding more nodes does not increase any individual node's gossip load.

Convergence time grows as O(log N):
- 5 nodes: convergence in ~0.5s
- 100 nodes: ~1.2s
- 1000 nodes: ~2s

**What actually scales linearly:** the number of probes sent to your monitored targets. If 3 nodes each probe `payments-db` every 60 seconds, `payments-db` receives 3 TCP connections per minute regardless of how many nodes are in the cluster — because only 3 nodes are assigned as probers. This is the main benefit of distributed probe ownership: target load is O(probe_replication_factor), not O(cluster_size).

A netwatch node consumes single-digit megabytes of RAM and well under 1% CPU in normal operation. The bottleneck in practice is outbound network connections for probes, not internal coordination overhead.

---

### When does cluster mode make sense, and when does it not?

**Cluster mode is the right choice when you need fault-tolerant alerting from multiple vantage points.** Three nodes in three different availability zones means: if one zone loses connectivity, the other two still probe and alert. The cluster coordination ensures you receive exactly one notification per incident.

**Cluster mode is the wrong choice when nodes are colocated.** If all three of your monitoring nodes live in the same datacenter, a network failure that takes them all down also takes out the gossip mesh. Three standalone nodes in three datacenters outperforms one cluster in one datacenter.

**The OpenShift / Kubernetes DaemonSet question:**

Running netwatch as a DaemonSet on every node in a 100-node cluster where each pod probes all applications creates a probe storm: without distributed probe ownership, 100 pods × 100 application targets × 1 probe per 15 seconds = 667 probes per second. With distributed probe ownership (`probe_replication_factor=3`), this is reduced to 20 probes per second cluster-wide regardless of node count.

The correct model for Kubernetes:
- Run **2–3 netwatch pods** in a dedicated monitoring namespace, each with access to all application endpoints.
- The 2–3 pod cluster gives you fault tolerance without probe multiplication.
- For per-node local service monitoring (localhost probes), run one standalone netwatch per node for only that node's local services, outside the cluster.

---

### What happens if state.json is deleted from only one node?

Say you have a 3-node cluster where `payments-db` has been hard-down for an hour. All nodes have `seq=5, alarm_sent=true`. You delete state.json on node-2 and restart it.

Node-2 restarts with no persistent state. It probes. `payments-db` fails → hard-down. Node-2 broadcasts `GossipPayload{seq:1}`. Node-1 and node-3 reject this (their `seq=5 > 1` wins). During push/pull anti-entropy sync on rejoin, node-2 receives node-1's full state with `seq=5, alarm_sent=true`. The anti-entropy merge applies this to node-2.

In theory no duplicate alert fires. In practice there is a narrow race window: if node-2 is the consistent-hash primary and fires the alert **before** anti-entropy sync completes. The `syncing` flag is designed to close this window — alarm dispatch is suppressed while anti-entropy is in progress.

**Summary:**

| State when restarted | node-2 is primary? | Result |
|---|---|---|
| Target UP | either | No alert. Normal operation. |
| Target hard-down, state.json missing | No | Suppressed (not responsible). No duplicate. |
| Target hard-down, state.json missing | Yes | Anti-entropy sync runs first; `syncing` flag mitigates race. |
| Target hard-down, state.json present | Yes | `alarm_sent=true` prevents re-alert. |

Keep state files intact.

---

### How much server load do all these recurring processes create?

**UDP gossip (~200ms per node):** 3 outbound UDP datagrams per 200ms = 15 per second per node. Each ~100–500 bytes. For a 5-node cluster, roughly 75 UDP packets per second cluster-wide.

**TCP push/pull (~30 seconds per node):** One TCP connection per 30 seconds per node. A few kilobytes exchanged. For 10 nodes: 0.33 TCP connections per second cluster-wide.

**Probe goroutines:** Each target has one goroutine that sleeps for `probe_interval_sec`, wakes up, opens one connection, closes it, then sleeps again. A sleeping goroutine costs ~2–8 KB of stack and 0% CPU. For 100 targets: under 1 MB stack, 0% CPU between probes.

**Config reload (`reload_interval_sec`):** One file read + YAML parse per interval. A 10 KB config file parses in microseconds.

**Cumulative verdict:** Single-digit MB RAM, well under 1% CPU in normal operation. The bottleneck is outbound probe connections, not internal coordination.

---

## License

MIT
