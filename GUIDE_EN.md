# netwatch — User Guide

This guide is written for someone who has never seen netwatch before and wants to understand, install, and use it.
For technical internals see `README.md`, for developer notes see `CLAUDE.md`.

---

## What does netwatch do?

netwatch is a network monitoring agent that periodically checks services (HTTP, TCP, ping, DNS, SQL databases)
and notifies you when a problem is detected.

Core features:

- **Probe types:** HTTP/HTTPS, TCP, ICMP ping, DNS, PostgreSQL/MySQL/MSSQL/Oracle
- **Notification channels:** Shell script, e-mail (SMTP), webhook (Alertmanager-compatible included)
- **Prometheus metrics:** Full state data exposed at `/metrics`
- **Cluster mode:** Multiple nodes communicate via gossip protocol, make quorum-based decisions,
  and fire each alert exactly once
- **SLO tracking:** Define uptime targets and receive notifications on breach
- **Application context:** Map which services depend on which infrastructure so alerts read
  "payment-gateway affected, team: fintech-sre"

---

## Quick Start (single node)

```bash
# Build the binary
go build -o netwatch ./cmd/linux/

# Copy the example config
cp config.example.yaml config.yaml

# Run
./netwatch -config config.yaml

# Health check
curl http://localhost:10240/health

# Status of all targets
curl http://localhost:10240/status
```

---

## Installation

### Linux — systemd

```bash
# Copy the binary
sudo cp netwatch /usr/local/bin/netwatch
sudo chmod +x /usr/local/bin/netwatch

# Create config and notifications directories
sudo mkdir -p /etc/netwatch /etc/netwatch/notifications
sudo cp config.yaml /etc/netwatch/config.yaml

# Generate the systemd unit file
sudo netwatch init --config-dir /etc/netwatch

# Enable and start
sudo systemctl enable --now netwatch
```

> The ICMP ping probe type requires `CAP_NET_RAW`. The provided `deploy/netwatch.service`
> already includes `AmbientCapabilities=CAP_NET_RAW`.

### Docker

```bash
docker run -d \
  -p 10240:10240 \
  -v $(pwd)/config.yaml:/etc/netwatch/config.yaml \
  -v $(pwd)/notifications:/etc/netwatch/notifications \
  ghcr.io/saidtaylan/netwatch:latest \
  -config /etc/netwatch/config.yaml
```

### Kubernetes (DaemonSet)

```bash
helm install netwatch ./helm/netwatch \
  --set config.clusterEnabled=true \
  --set config.nodeName=$(hostname)
```

---

## Basic Configuration

```yaml
# config.yaml — minimum working configuration

port: "10240"
app_name: "production-monitor"
timeout: 5
max_retries: 3
retry_interval_sec: 30
probe_interval_sec: 60

notifications:
  ops-pager:
    type: script
    parameters:
      script: "/etc/netwatch/notifications/alert.sh"

default_notify: ["ops-pager"]

targets:
  - name: "api-gateway"
    type: http
    target: "https://api.company.com/health"
    options:
      expected_status:
        in: [200, 204]

  - name: "database"
    type: tcp
    target: "db.internal:5432"

  - name: "redis"
    type: tcp
    target: "redis.internal:6379"
    notify: ["ops-pager"]   # target-specific channel override
```

---

## Probe Types

### HTTP / HTTPS

```yaml
- name: "checkout-api"
  type: http
  target: "https://api.company.com/checkout/health"
  options:
    method: "GET"
    expected_status:
      in: [200]           # or: eq: 200 / between: [200, 299]
    body_contains: "ok"   # optional — response body check
    follow_redirects: true
    timeout_sec: 10
```

### TCP

```yaml
- name: "postgres"
  type: tcp
  target: "db.internal:5432"
```

### ICMP Ping

```yaml
- name: "core-router"
  type: ping
  target: "10.0.0.1"
```

> Requires `CAP_NET_RAW`. In Docker: `--cap-add NET_RAW`.

### DNS

```yaml
- name: "internal-dns"
  type: dns
  target: "api.company.com"
  options:
    nameserver: "8.8.8.8:53"       # optional
    expected_ips: ["203.0.113.10"]  # optional — IP verification
```

### SQL (PostgreSQL / MySQL / MSSQL / Oracle)

```yaml
- name: "primary-db"
  type: sql
  target: "db.internal:5432"
  options:
    driver: "postgres"
    username: "${DB_USER}"     # injected from credentials.env
    password: "${DB_PASS}"
    database: "production"
    query: "SELECT 1"          # optional — execute a query
    ssl_mode: "require"
```

---

## Notification Channels

### Script

The most flexible channel. netwatch invokes the script with environment variables for each alert:

```yaml
notifications:
  ops-pager:
    type: script
    parameters:
      script: "/etc/netwatch/notifications/alert.sh"
```

Key environment variables available to the script:

| Variable | Content |
|---|---|
| `NAME` | target name |
| `TARGET` | host:port or URL |
| `STATUS` | `unreachable` or `reachable` |
| `TYPE` | tcp / http / ping / dns / sql |
| `ERROR_CODE` | error message (empty on recovery) |
| `SEQ` | Lamport sequence — increments on every transition |
| `SCOPE` | GLOBAL / NODE\_LOCAL / PARTIAL / STANDALONE |
| `AFFECTED_APPS` | affected applications (comma-separated) |
| `OWNER_TEAMS` | responsible teams (comma-separated) |
| `ROOT_CAUSE` | deepest failing dependency in the chain |
| `CLASSIFICATION` | REAL\_OUTAGE / NETWORK\_PARTITION / LOCAL\_FAILURE |

### E-mail (SMTP)

```yaml
notifications:
  mail-ops:
    type: mail
    parameters:
      from: "netwatch@company.com"
      to: "ops@company.com,on-call@company.com"
      smtp_host: "smtp.company.com"
      smtp_port: "587"
      username: "${SMTP_USER}"
      password: "${SMTP_PASS}"
      tls: "starttls"
```

### Webhook (generic JSON)

```yaml
notifications:
  slack-hook:
    type: webhook
    parameters:
      url: "https://hooks.slack.com/services/T000/B000/xxx"
      timeout_sec: "10"
```

### Webhook (Alertmanager-compatible)

```yaml
notifications:
  alertmanager:
    type: webhook
    parameters:
      url: "http://alertmanager:9093/api/v2/alerts"
      format: "alertmanager"
      header_Authorization: "Bearer ${AM_TOKEN}"
```

---

## Application → Infrastructure Mapping (Apps)

You can declare which application depends on which infrastructure target.
When a target goes down, the alert is automatically enriched with affected service and team information.

```yaml
targets:
  - id: "payments-db"
    type: tcp
    target: "db-payments:5432"
    name: "Payments DB"

apps:
  - name: "payment-gateway"
    owner_team: "fintech-sre"
    uses: ["payments-db"]
    notifications: ["slack-fintech"]

  - name: "fraud-detection"
    owner_team: "security-team"
    uses: ["payments-db"]
    notifications: ["pagerduty-sec"]
```

When `payments-db` goes down:
- `AFFECTED_APPS=payment-gateway,fraud-detection`
- `OWNER_TEAMS=fintech-sre,security-team`
- Notification channels: `ops-pager ∪ slack-fintech ∪ pagerduty-sec` (union, deduplicated)

---

## Dependency Graph & Root Cause Detection

Use `depends_on` to model service dependencies.
When a root infrastructure target fails, every dependent service's alert carries "root cause: primary-db".

```yaml
targets:
  - id: "primary-db"
    type: tcp
    target: "db:5432"
    name: "Primary DB"

  - id: "api-gateway"
    type: http
    target: "https://api.company.com/health"
    name: "API Gateway"
    depends_on: ["primary-db"]

  - id: "checkout-service"
    type: http
    target: "https://checkout.company.com/health"
    name: "Checkout Service"
    depends_on: ["api-gateway"]
```

When `primary-db` goes down:
- `api-gateway` alert: `ROOT_CAUSE=primary-db`, `DEPENDENCY_DEPTH=1`
- `checkout-service` alert: `ROOT_CAUSE=primary-db`, `DEPENDENCY_DEPTH=2`, `CASCADING_IMPACT=checkout-service`

`GET /topology` returns the full dependency graph as JSON.

---

## SLO Tracking

```yaml
slo:
  enabled: true
  retention_days: 90
  slo_notify: ["ops-pager"]
  targets:
    - id: "api-gateway"
      target_uptime: 0.999   # 99.9%
      window: "30d"
    - id: "primary-db"
      target_uptime: 0.9999  # 99.99%
      window: "7d"
```

- `GET /slo` — uptime ratio, remaining error budget, active breach status per target
- `GET /slo?format=text` — terminal-friendly ASCII table
- Breach alerts are edge-triggered: only one alert per SLO period

---

## HTTP Endpoints

| Endpoint | Description |
|---|---|
| `GET /health` | Always returns 200 — liveness check |
| `GET /metrics` | Prometheus metrics |
| `GET /status` | JSON status of all targets (name, status, seq, error_code) |
| `GET /fleet/status` | Rich view: scope, classification, affected apps, root cause, incidents |
| `GET /fleet/status?format=text` | Same, terminal ASCII table |
| `GET /topology` | Dependency graph (depends\_on relationships) |
| `GET /slo` | SLO metrics: uptime, budget, breach |
| `GET /slo?format=text` | Terminal ASCII table |
| `GET /cluster/state` | Gossip members + peer target states |
| `GET /cluster/probers` | Assigned prober nodes per target and selection reasoning |
| `GET /cluster/config` | Config hash comparison — drift detection |
| `GET /geo/latency/{targetID}` | Per-node latency + anomaly flag for a target |
| `GET /cluster/keyring/rotate` | Active key information |
| `POST /cluster/keyring/rotate` | Zero-downtime AES key rotation |
| `POST /cluster/leave` | Graceful cluster leave + process exit |

---

## Prometheus Metrics (summary)

| Metric | Description |
|---|---|
| `network_probe_local_status` | 1=UP, 0=DOWN (on this node) |
| `network_probe_local_latency_seconds` | Last probe round-trip time |
| `network_probe_prometheus_connected` | 1=scraping normally, 0=watchdog triggered |
| `network_probe_cluster_status` | Consensus: all nodes UP=1, any node DOWN=0 |
| `network_probe_local_assigned` | 1=this node is probing the target |
| `network_probe_prober_count` | Total assigned probers for this target |
| `network_probe_prober_underreplicated` | 1=fewer probers than factor (degraded coverage) |
| `network_probe_target_orphaned` | 1=no node is probing this target (config error) |
| `network_prober_quorum_healthy` | 1=quorum present |
| `network_prober_isolated` | 1=this node is isolated (alerts suppressed) |
| `network_probe_slo_uptime_ratio` | Actual uptime ratio in the rolling window |
| `network_probe_slo_breached` | 1=SLO breach active |
| `network_probe_config_drift` | 1=at least one peer has a different config hash |
| `network_probe_geo_latency_seconds` | Per-region probe latency |

---

## State Machine — How It Behaves

```
UP → SOFT_DOWN → HARD_DOWN → UP
```

**UP:** Last probe succeeded. No alert pending.

**SOFT_DOWN:** First probe failure. No alert yet — retries are queued.
This state is held in memory only — nothing is written to `state.json`.
Transient network blips (packet loss, brief restarts) are absorbed here.

**HARD_DOWN:** All `max_retries` probe attempts failed. Alert fired, state written to `state.json`.
This state survives a process restart.

**Recovery:** The next successful probe sends a "reachable" alert and increments seq.
`seq=1 unreachable` + `seq=2 reachable` belong to the same incident.

**Restart safety:** On startup, `state.json` is read. A target that was already hard-down
does not fire a duplicate alert — `AlarmSent=true` is persisted alongside the state.

---

## Cluster Mode

When multiple netwatch nodes monitor the same infrastructure, cluster mode provides:
- Each alert fires **exactly once** (gossip + consistent hash primary)
- Alerts are **suppressed during quorum loss** (an isolated node never false-alerts)
- **No alert storm on node restart** (anti-entropy sync before resuming alarms)

### Minimal 3-node cluster

`node-1/config.yaml`:
```yaml
cluster:
  enabled: true
  node_name: "node-1"
  bind_addr: "0.0.0.0"
  bind_port: 7946
  advertise_addr: "192.168.1.101"
  peers:
    - "192.168.1.101:7946"
    - "192.168.1.102:7946"
    - "192.168.1.103:7946"
  keyring:
    - "base64_aes256_key_here=="
  expected_node_count: 3
  min_quorum_ratio: 0.5
```

`node-2` and `node-3`: same structure, only `node_name` and `advertise_addr` differ.

### Distributed Probe Ownership

In a 50-node cluster, each target is probed by only 3 nodes.
The other 47 receive probe results via gossip — they never open a connection to the target.

```yaml
cluster:
  probe_replication_factor: 3   # default
```

**Zone-aware spread** — redundant coverage across failure domains:

```yaml
# node-1 config
cluster:
  zone: "us-east-1a"

# node-2 config
cluster:
  zone: "us-east-1b"
```

The system automatically picks probers from distinct zones.

**Manual pinning** (only specific nodes should probe):

```yaml
targets:
  - id: "internal-vpn"
    type: tcp
    target: "10.50.50.10:443"
    probe_from: ["frankfurt-1", "amsterdam-1"]
```

**False-alert protection** (`min_probe_confirmations`):
If a prober's own network path to a target is broken, it should not alert alone:

```yaml
cluster:
  min_probe_confirmations: 2   # require 2 probers to agree on hard_down
```

### Quorum

With `expected_node_count: 3` and `min_quorum_ratio: 0.5`, at least 2 nodes must be alive.
If 2 nodes go down, the remaining node enters isolated mode and suppresses all alerts.
When quorum is restored, anti-entropy sync runs automatically and alerting resumes.

### Gossip Encryption

```yaml
cluster:
  keyring:
    - "newKeyBase64=="   # first key is used for encryption
    - "oldKeyBase64=="   # all keys are tried for decryption
```

Zero-downtime key rotation: add the new key to the front of the list, roll it out to all nodes,
then remove the old key in a second pass.

---

## Frequently Asked Questions

### Why is my alert delayed?

Alerts wait for `max_retries × retry_interval_sec`. Example: `max_retries: 3`,
`retry_interval_sec: 30` → 90 seconds before hard-down.
Lower these values for faster alerting; keep them higher to absorb transient blips.

### Will the agent re-alert for an already-down target after restart?

No. `state.json` records `AlarmSent: true`. After a restart, no duplicate "unreachable" alert
is fired for a target that was already hard-down. A new alert fires only when the target
recovers and then goes down again (seq increments).

### What if a prober crashes mid-retry before reaching hard-down?

The prober broadcasts a `soft_down` gossip signal on the very first failed probe
(and again on each retry). Co-probers receive this and immediately fire an
out-of-schedule probe — without waiting for their next ticker tick. Even if the
original prober is killed during retries, the other 2 probers have already started
their own independent verification and will reach hard-down on their own. The failure
evidence is distributed immediately, not held privately.

### When does isolated mode activate?

When the number of alive nodes falls below `floor(expected_node_count × min_quorum_ratio) + 1`.
For a 3-node cluster with `min_quorum_ratio: 0.5`: requires at least 2 alive nodes.
An isolated node continues probing but suppresses all alerts.
It exits isolated mode automatically when quorum is restored.

### Can multiple nodes fire the same alert in cluster mode?

No. The consistent hash ring designates one "primary" node per target.
Only the primary fires the alert. If the primary leaves, the next node in the ring
takes over automatically within the same gossip round.

### Do I need to update every node when changing `probe_replication_factor`?

Yes — all nodes must use the same value. If they differ, nodes compute different
prober sets and exactly-once alerting breaks. Hot-reload is supported:
update `config.yaml` and all nodes pick it up within `reload_interval_sec`.
No restart required.

### What happens when Prometheus stops scraping?

If `watchdog_threshold_sec` is set (disabled by default), the
`network_probe_prometheus_connected` metric drops to 0 and a warning is logged
after that many seconds without a scrape. Probes and alerts are unaffected —
the agent continues operating autonomously.

### What is the difference between `/fleet/status` and `/status`?

`/status`: simple list — name, status, seq, error_code per target.

`/fleet/status`: rich view — scope (GLOBAL/NODE\_LOCAL/PARTIAL),
classification (REAL\_OUTAGE/NETWORK\_PARTITION/LOCAL\_FAILURE/AMBIGUOUS),
affected apps, root cause, active incidents, per-node breakdown.
Works in standalone mode too (no cluster required).

### Why is `network_probe_target_orphaned` set to 1?

It means no alive node is probing that target. Common causes:
`probe_from` lists node names that never joined the cluster, or
`probe_from_regions` lists regions with no alive nodes.
The log emits a `[CLUSTER] target orphaned` line with a hint.

### How are config changes applied?

netwatch checks the config file's modification time every `reload_interval_sec` seconds
(default: 30). If it changed, new targets get probe goroutines and removed targets
have theirs cancelled. No restart required.
Set `reload_interval_sec: 0` to disable hot-reload.

### What is `network_probe_prober_underreplicated`?

This metric fires (value 1) when a target has at least one assigned prober but fewer
than `probe_replication_factor`. This happens when a prober node recently left and
a replacement has not yet been assigned. It is distinct from `target_orphaned`
(which means zero probers). Underreplicated means degraded coverage, not blind coverage.

---

## CLI Commands

```bash
# Validate configuration without running
netwatch validate -config config.yaml

# Generate systemd unit file + skeleton config
netwatch init --config-dir /etc/netwatch

# Gracefully leave the cluster (sends HTTP to the running agent)
netwatch leave --addr http://localhost:10240

# Remove everything (leave + service + files)
netwatch uninstall

# Windows Service management
netwatch service install
netwatch service remove
```

---

## Security Notes

- All gossip traffic is AES-256 encrypted via `cluster.keyring`.
- SQL passwords and other secrets are stored in `credentials.env` and injected into config as `${VAR}`.
- Only ICMP ping requires `CAP_NET_RAW` — all other probe types run without elevated privileges.
- The `/metrics` endpoint does not expose sensitive information; network access for Prometheus scraping is sufficient.
- `cluster.keyring` values are base64-encoded AES keys (raw length must be 16, 24, or 32 bytes).
