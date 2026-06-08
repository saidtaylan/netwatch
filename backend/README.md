# netwatch — Backend

Go-based distributed network monitoring agent.

## Overview

The netwatch backend is a self-contained Go binary that continuously probes network targets and exposes their health state over HTTP. Key capabilities:

- **Probe types** — TCP, HTTP/HTTPS, ICMP ping, DNS, SQL (PostgreSQL, MySQL, Oracle, SQL Server)
- **State machine** — soft-down → hard-down with configurable retries and recovery confirmation probes
- **Gossip cluster** — leaderless multi-node clustering via [memberlist](https://github.com/hashicorp/memberlist); nodes share state without a central coordinator
- **Distributed probe ownership** — consistent hash ring assigns each target to `probe_replication_factor` nodes; the rest listen passively (eliminates duplicate alerting in a cluster)
- **SQLite storage** — all dynamic config (targets, apps, channels, silences, SLO targets, users, maintenance windows) is stored in SQLite and replicated to peers via gossip last-writer-wins
- **HTTP API** — REST endpoints for status, fleet snapshot, cluster management, SLO reporting, topology, user management, and all CRUD resources
- **Prometheus metrics** — `network_probe_local_status`, `network_probe_local_latency_seconds`, cluster quorum/isolation gauges, and per-target prober ownership metrics
- **Notification channels** — shell script, SMTP email, generic JSON webhook, and Prometheus Alertmanager webhook format

---

## Building

### Prerequisites

- Go 1.22 or later

### Build for Linux (production)

```bash
cd backend
GOOS=linux GOARCH=amd64 go build -o netwatch ./cmd/linux/
```

With version information embedded:

```bash
GOOS=linux GOARCH=amd64 go build -ldflags "-X main.version=1.0.0" -o netwatch ./cmd/linux/
```

### Build for Windows

```powershell
GOOS=windows GOARCH=amd64 go build -o netwatch.exe ./cmd/windows/
```

### Run Tests

```bash
go test -race ./internal/engine/... ./internal/cluster/...
```

---

## Configuration

Copy `config.example.yaml` as a starting point. Values that contain `${VARNAME}` placeholders are resolved from `credentials_file` (a `KEY=VALUE` file) and then from the process environment.

### Full Field Reference

#### Agent identity

| Field | Type | Default | Description |
|---|---|---|---|
| `port` | string | `"10240"` | HTTP listen port for `/metrics`, `/health`, `/status`, API endpoints |
| `node_alias` | string | _(empty)_ | Human-readable label for this node. Appears in Prometheus metric label `app_name` and alert env var `NODE_ALIAS` |

#### File paths

| Field | Type | Default | Description |
|---|---|---|---|
| `state_file` | string | _(empty)_ | Path to `state.json` — persists target health state across restarts to prevent spurious alerts |
| `log_path` | string | _(empty)_ | Structured JSON log file path. Empty = text output to stdout only |
| `credentials_file` | string | _(empty)_ | Path to a `KEY=VALUE` file used to resolve `${VAR}` placeholders in the config |

#### Probe timings

| Field | Type | Default | Description |
|---|---|---|---|
| `timeout` | int (s) | `5` | Per-probe connect/read timeout for all target types |
| `probe_interval_sec` | int (s) | `60` | Default probe cadence per target; overridable per target with `interval_sec` |
| `max_retries` | int | `1` | Consecutive failures before a target is declared `hard_down` |
| `retry_interval_sec` | int (s) | `30` | Pause between retry probes during the soft-down phase |
| `recovery_probes` | int | `1` | Consecutive successful probes required before a `hard_down` target recovers and a "reachable" alert fires |
| `ticker_interval_sec` | int (s) | `5` | Internal scheduler resolution — minimum probe granularity. Do not set below 2 |
| `reload_interval_sec` | int (s) | `30` | How often the engine checks for `config.yaml` changes. Minimum enforced: 5 s. 0 = disabled |
| `watchdog_threshold_sec` | int (s) | `0` | If Prometheus does not scrape `/metrics` within this window, logs `[WATCHDOG]` and sets `network_probe_prometheus_connected=0`. 0 = disabled |

#### Admin / auth

| Field | Type | Default | Description |
|---|---|---|---|
| `admin.setup_token` | string | _(empty)_ | One-time token for the initial admin user creation (`POST /auth/setup`). Also acts as the JWT signing secret. Supports `${VAR}` substitution. Empty = setup endpoint is unrestricted |
| `admin.cors_origin` | string | `"*"` | Value for the `Access-Control-Allow-Origin` response header. Set to your frontend origin in production |

> [!IMPORTANT]
> The field is `admin.setup_token`, **not** `admin.token`. Configs using the old name will silently skip authentication.

#### Cluster

| Field | Type | Default | Description |
|---|---|---|---|
| `cluster.enabled` | bool | `false` | Enable gossip clustering. When false the cluster layer is a no-op |
| `cluster.node_name` | string | _(required)_ | Unique name for this node within the cluster |
| `cluster.bind_addr` | string | `"0.0.0.0"` | Address the gossip listener binds to |
| `cluster.bind_port` | int | `7946` | Gossip TCP+UDP port. Must be open between all nodes |
| `cluster.advertise_addr` | string | _(auto)_ | Address other nodes use to reach this one. Required behind NAT or in containers |
| `cluster.peers` | []string | `[]` | Seed peer addresses (`host:port`). At least one must be reachable on first join |
| `cluster.keyring` | []string | _(empty)_ | Base64-encoded AES keys (16/24/32 bytes) for gossip encryption. First key encrypts; all keys decrypt — supports zero-downtime rotation |
| `cluster.expected_node_count` | int | `0` | Total expected cluster members. Used to compute quorum threshold |
| `cluster.min_quorum_ratio` | float | `0.5` | Fraction of `expected_node_count` that must be alive for alerts to fire |
| `cluster.probe_replication_factor` | int | `3` | Number of nodes that probe each target. Others listen passively |
| `cluster.zone` | string | _(empty)_ | Availability zone / data-centre label. Prober selection spreads across distinct zones |
| `cluster.region` | string | _(empty)_ | Broader geographic region. Used for `probe_from_regions` target constraint |

#### Notifications

Defined under the `notifications:` map. Each entry has a `type` and a `parameters` map.

| Type | Required parameters | Optional parameters |
|---|---|---|
| `script` | `script` (path to executable) | — |
| `mail` | `smtp_host`, `smtp_port`, `from`, `to` | `username`, `password`, `tls_mode` (`starttls`/`tls`/`none`), `tls_insecure`, `ca_cert` |
| `webhook` | `url` | `format` (`generic`/`alertmanager`), `timeout_sec`, `tls_insecure`, `header_*` |

Use `default_notify: [channel-name, ...]` to set fallback channels for targets with no `notify:` list.

Alert env vars injected into script channels: `NAME`, `TARGET`, `HOST`, `PORT`, `STATUS` (`unreachable`|`reachable`), `TYPE`, `SEQ`, `ERROR_CODE`, `NODE_NAME`, `APP_NAME`, `NODE_ALIAS`, `AFFECTED_APPS`, `OWNER_TEAMS`.

#### Targets

Each target supports the following fields:

| Field | Type | Default | Description |
|---|---|---|---|
| `id` | string | _(uses name)_ | Stable identifier. Required when referenced by `apps.uses` or `depends_on`. If omitted, `name` is used as the key |
| `name` | string | _(required)_ | Human-readable display name |
| `type` | string | _(required)_ | `tcp`, `http`, `ping`, `dns`, `sql` |
| `target` | string | _(required)_ | Endpoint to probe — format varies by type |
| `enabled` | bool | `true` | Set to `false` to disable probing without removing the target |
| `options` | object | _(none)_ | Type-specific options (see below) |
| `notify` | []string | _(default_notify)_ | Channel names to alert via |
| `timeout` | int (s) | _(global)_ | Per-target timeout override |
| `interval_sec` | int (s) | _(global)_ | Per-target probe interval override |
| `max_retries` | int | _(global)_ | Per-target retry count override |
| `retry_interval_sec` | int (s) | _(global)_ | Per-target retry interval override |
| `recovery_probes` | int | _(global)_ | Per-target recovery probes override |
| `depends_on` | []string | `[]` | Target IDs this target depends on. Used for root cause analysis and cascade impact |
| `probe_from` | []string | `[]` | Pin probing to specific node names. Overrides hash-ring selection |
| `probe_from_regions` | []string | `[]` | Restrict probing to nodes in these `cluster.region` values. ANDed with `probe_from` |

**HTTP options:**

| Option | Description |
|---|---|
| `method` | HTTP method (default `GET`) |
| `expected_status.in` | List of acceptable status codes |
| `expected_status.eq/lt/lte/gt/gte/between` | Range operators for status code validation |
| `body_contains` | String that must appear in the response body |
| `follow_redirects` | Follow HTTP redirects (default `true`) |
| `timeout_sec` | Request timeout override |
| `headers` | Map of extra request headers |

**DNS options:** `nameserver`, `expected_ips`

**Ping options:** `count`, `timeout_sec`

**SQL options:** `driver` (`postgres`/`mysql`/`oracle`/`mssql`), `username`, `password`, `database`, `ssl_mode`, `service_name` (Oracle), `query`

#### Apps

Apps group targets under a named service and team. When an alert fires, `AFFECTED_APPS` and `OWNER_TEAMS` are injected into the notification.

```yaml
apps:
  - name: payment-gateway
    owner_team: fintech-sre
    uses:
      - postgres-primary      # target id or name
      - payment-api-health
    notifications:
      - email-ops
```

#### SLO

| Field | Type | Default | Description |
|---|---|---|---|
| `slo.enabled` | bool | `false` | Enable SLO incident tracking and the `/slo` endpoint |
| `slo.retention_days` | int | `90` | How long incident history is retained |
| `slo.targets` | []object | `[]` | SLO targets defined at config level (also manageable via API) |
| `slo.targets[].id` | string | _(required)_ | Must match a target `id` |
| `slo.targets[].target_uptime` | float | _(required)_ | Required uptime ratio, e.g. `0.999` |
| `slo.targets[].window` | string | _(required)_ | Rolling window, e.g. `"30d"`, `"24h"` |

---

## Running

### Standalone (single node)

```bash
./netwatch --config /etc/netwatch/config.yaml
```

### systemd (Linux production)

The `deploy-systemd/netwatch-backend.service` unit file runs the agent as the `netwatch` user. Copy it to `/etc/systemd/system/`:

```bash
sudo cp deploy-systemd/netwatch-backend.service /etc/systemd/system/
sudo systemctl daemon-reload
sudo systemctl enable --now netwatch-backend
```

Key service properties:

- Runs as `netwatch` user/group — create with `useradd -r -s /sbin/nologin netwatch`
- **`AmbientCapabilities=CAP_NET_RAW`** — required for ICMP ping probes; harmless when ping targets are not used
- `ReadWritePaths=/etc/netwatch /var/lib/netwatch` — config reads from `/etc/netwatch`; `state.json` and logs write to `/var/lib/netwatch`
- `Restart=on-failure` with 5-second backoff, max 5 restarts per 60-second window
- Logs via journald (`journalctl -u netwatch-backend -f`)
- Gossip port (default `7946` TCP+UDP) must be open in the host firewall between all cluster nodes

Using Ansible? See `ansible/` — the `roles/netwatch-backend` role handles user creation, binary copy, config templating, and service management automatically.

### Windows Service

```powershell
# Install
.\netwatch.exe service install --config C:\netwatch\config.yaml
sc.exe start netwatch

# Remove
sc.exe stop netwatch
.\netwatch.exe service remove
```

---

## API Reference

All write endpoints and user-management endpoints require a JWT (`Authorization: Bearer <token>`) obtained via `POST /auth/login`. Read-only status endpoints are publicly accessible by default.

### Health & Info

| Method | Path | Description |
|---|---|---|
| `GET` | `/health` | Liveness check — returns `200 OK` |
| `GET` | `/version` | Build info — `{ version, build_time }` |
| `GET` | `/metrics` | Prometheus metrics endpoint |

### Authentication

| Method | Path | Description |
|---|---|---|
| `GET` | `/auth/status` | `{ setup_completed: bool, user_count: int }` — check whether initial setup has been done |
| `POST` | `/auth/setup` | First-time setup. Body: `{ setup_token, username, password }`. Creates the admin user |
| `POST` | `/auth/login` | `{ username, password }` → `{ token }` (JWT) |
| `GET` | `/auth/me` | Current authenticated user info |
| `PUT` | `/auth/password` | Change password for the current user |

### User Management _(admin JWT required)_

| Method | Path | Description |
|---|---|---|
| `GET` | `/users` | List all users |
| `PUT` | `/users/{id}` | Create or update a user |
| `DELETE` | `/users/{id}` | Delete a user |

### Monitoring

| Method | Path | Description |
|---|---|---|
| `GET` | `/status` | All target states from this node |
| `GET` | `/fleet/status` | Full cluster snapshot — per-node states, consensus state, scope, classification |
| `GET` | `/fleet/status?format=text` | ASCII table — useful for terminal inspection |
| `GET` | `/slo` | SLO results — uptime ratio, error budget, incident list per target |
| `GET` | `/topology` | Dependency graph — root cause and cascade impact for all targets |
| `GET` | `/geo/latency/{targetID}` | Per-node latency breakdown with anomaly flag |

### Cluster

| Method | Path | Description |
|---|---|---|
| `GET` | `/cluster/state` | Member list and per-peer states |
| `GET` | `/cluster/sync/effective` | This node's shared config fields |
| `GET` | `/cluster/sync/aggregate` | Field-level diff across all peers (config drift view) |
| `POST` | `/cluster/config/sync` | Push this node's shared config to all peers |
| `GET` | `/cluster/maintenance` | List active maintenance windows |
| `PUT` | `/cluster/maintenance` | Create a maintenance window |
| `DELETE` | `/cluster/maintenance/{id}` | Delete a maintenance window |

### Config & Keyring

| Method | Path | Description |
|---|---|---|
| `GET` | `/cluster/config` | Current shared config snapshot |
| `PUT` | `/cluster/config` | Push a new shared config to all peers |
| `GET` | `/cluster/keyring/rotate` | Current keyring info |
| `POST` | `/cluster/keyring/rotate` | Rotate gossip encryption key — `{ action: "add"|"use"|"remove", key: "base64..." }` |

### Storage-backed Resources _(JWT required)_

All endpoints follow the same pattern: `GET` (list), `PUT /{id}` (upsert), `DELETE /{id}` (remove).

| Resource | Base path |
|---|---|
| Targets | `/targets` |
| Apps | `/apps` |
| Notification channels | `/channels` |
| SLO targets | `/slo/targets` |
| Silences | `/silences` |

---

## Storage Layer

State is persisted in SQLite via the `internal/storage` package. Records are stored schema-less — each row is a JSON payload plus last-writer-wins (LWW) metadata (sequence number, node name, timestamp).

**Tables:** `targets`, `apps`, `channels`, `slo_targets`, `maintenance_windows`, `silences`, `users`, `frontend_settings`

**Replication:** Every write is broadcast to all cluster peers via gossip. On conflict (two nodes writing simultaneously) the higher sequence number wins. On node re-join, full anti-entropy sync resolves any divergence.

**State file (`state.json`):** Target health states are separately persisted in a lightweight JSON file (not SQLite). On restart, the engine loads this file and cross-checks with gossip anti-entropy to avoid spurious recovery or down alerts.

---

## Cluster Architecture

netwatch uses a **leaderless gossip** model built on [hashicorp/memberlist](https://github.com/hashicorp/memberlist):

- **Consistent hash ring** — target-to-node probe assignment is deterministic and self-healing; no coordination needed when nodes join or leave
- **Zone-aware prober spread** — when nodes declare a `cluster.zone`, the ring assignment algorithm tries to select probers from distinct zones for failure-domain redundancy
- **Quorum gating** — when alive node count falls below `floor(expected_node_count × min_quorum_ratio) + 1`, alert dispatch is suppressed to avoid false alarms during network partitions
- **IsolatedMode** — a node that loses quorum enters isolated mode (metric `network_prober_isolated=1`) and silences all outbound alerts until the cluster recovers
- **Probe confirmations** — `min_probe_confirmations` (optional) requires N independent nodes to reach `hard_down` before an alert fires, eliminating false positives from a single node's bad uplink
- **Anti-entropy** — on join or reconnect, nodes merge their full state tables, resolving conflicts with LWW sequence numbers

---

## Directory Structure

```
backend/
├── cmd/
│   ├── linux/          # Linux binary entrypoint (main.go)
│   └── windows/        # Windows binary entrypoint + service installer
├── internal/
│   ├── engine/         # Core engine — probing loops, state machine, SLO, topology
│   ├── cluster/        # Gossip cluster manager, hash ring, quorum, geo-latency
│   └── storage/        # SQLite + gossip replication storage layer
├── notifications/      # Example notification scripts
├── tests/              # Integration tests
├── test_cluster/       # Local multi-node test configs (node-01 … node-N)
├── config.example.yaml # Full annotated configuration reference
└── Makefile            # Common build / lint / test targets
```
