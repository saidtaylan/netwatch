# netwatch

**netwatch** is a distributed network monitoring agent written in Go. It runs as a Prometheus exporter and continuously probes TCP, HTTP/HTTPS, ICMP, DNS, and SQL targets. Multiple instances form a gossip cluster that agrees on which node sends each alert — so the same outage never produces duplicate notifications regardless of how many agents are watching the same target.

---

## Features

| Capability | Description |
|---|---|
| **Multi-protocol probes** | TCP, HTTP/HTTPS (body + status assertions), ICMP ping, DNS, SQL (MySQL / PostgreSQL / SQL Server / Oracle) |
| **Smart state machine** | Transient failures cause a *soft-down* retry phase; only after `max_retries` failures is the target declared *hard-down* and an alert sent |
| **Exactly-once alerting** | In cluster mode, consistent hashing assigns a primary + secondary owner per target; only the responsible node sends the alert |
| **Quorum gating** | If a cluster loses quorum (majority of nodes unreachable), alert dispatch is suppressed to avoid false positives from a split-brain node |
| **Anti-entropy re-join** | When a node restarts and rejoins the cluster, it syncs state before allowing new alerts — no storm of duplicate notifications |
| **Alert channels** | Script (`.sh` / `.ps1`), SMTP mail (multipart HTML), generic JSON webhook, or Prometheus Alertmanager format |
| **App → target indirection** | Group targets under named *apps* with owner teams; `AFFECTED_APPS` and `OWNER_TEAMS` are injected into every alert |
| **Prometheus metrics** | `network_probe_local_status`, `network_probe_local_latency_seconds`, plus cluster health gauges |
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
    in: [200, 204]                  # eq / in / lt / lte / gt / gte / between
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

---

## Alert Channels

### Script
Executes a shell script (`/bin/sh` on Linux, PowerShell on Windows) with all context as environment variables.

| Variable | Value |
|---|---|
| `NAME` | target name |
| `TARGET` | host:port or URL |
| `HOST`, `PORT` | parsed address parts |
| `STATUS` | `unreachable` or `reachable` |
| `TYPE` | tcp / http / ping / dns / sql |
| `SEQ` | Lamport sequence number |
| `ERROR_CODE` | last probe error text |
| `NODE_NAME` | `os.Hostname()` |
| `APP_NAME` | `app_name` config value |
| `AFFECTED_APPS` | comma-separated app names (if configured) |
| `OWNER_TEAMS` | comma-separated team names (if configured) |

### Webhook — generic JSON
```json
{
  "name": "payments-db", "target": "db.prod:5432",
  "status": "unreachable", "type": "tcp",
  "seq": 3, "error_code": "connection refused",
  "affected_apps": "payment-gateway", "owner_teams": "fintech-sre",
  "fired_at": "2026-05-07T10:00:00Z"
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
| `network_probe_cluster_status` | Gauge | same | Consensus view: 1 if all nodes see UP, 0 if any node sees DOWN |
| `network_prober_quorum_healthy` | Gauge | — | 1 = quorum present, 0 = quorum lost |
| `network_prober_isolated` | Gauge | — | 1 = this node is isolated (alert dispatch suppressed) |
| `network_prober_cluster_size` | Gauge | — | Number of alive cluster members |

---

## HTTP Endpoints

| Endpoint | Description |
|---|---|
| `GET /metrics` | Prometheus scrape — also notifies the watchdog |
| `GET /health` | Liveness check — always `200 OK` |
| `GET /status` | JSON: all targets with current state, seq, error_code |
| `GET /cluster/state` | JSON: member list + peer target states; `503` if cluster is disabled |
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

1. Every node probes every target independently (local health signal).
2. State changes are broadcast via gossip — all nodes maintain a view of peer states.
3. Consistent hashing assigns a **primary** and **secondary** responsible node per target.
4. Only the responsible node calls `sendAlert()`.
5. If the responsible node dies, the secondary takes over (the hash ring is recomputed when membership changes).
6. `SCOPE` env var in the alert: `GLOBAL` (all nodes see down) / `NODE_LOCAL` (one node sees down) / `PARTIAL`.

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
│   └── windows/main.go       # Windows Service integration
├── internal/
│   ├── engine/               # All probe, state, alert, watchdog logic
│   │   ├── engine.go         # Config, Engine, state persistence, metrics
│   │   ├── loop.go           # Probe goroutines, retry loop, state machine
│   │   ├── notify.go         # Alert channel dispatch
│   │   ├── app.go            # App→Target indirection
│   │   ├── webhook.go        # Webhook alerter (generic + alertmanager)
│   │   ├── mail.go           # SMTP alerter (HTML multipart)
│   │   ├── watchdog.go       # Prometheus scrape watchdog
│   │   ├── appinfo.go        # var BinaryName (ldflags override point)
│   │   └── {http,tcp,ping,dns,sql}.go  # Protocol checkers
│   └── cluster/              # Gossip cluster layer
│       ├── cluster.go        # Manager, memberlist, quorum, hash ring, anti-entropy
│       ├── cluster_test.go
│       ├── antientropy_test.go
│       └── testhelpers.go
├── deploy/
│   └── netwatch.service      # Reference systemd unit file
├── helm/netwatch/            # Kubernetes DaemonSet Helm chart
├── notifications/            # Alert scripts go here (.sh / .ps1)
├── config.example.yaml       # Full annotated config reference
├── Makefile
└── Dockerfile
```

---

## Advanced: Cluster Internals and Config Deep Dive

This section answers the "why" behind every cluster config field and explains how gossip communication actually works under the hood.

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

`list.Join()` is explicitly designed to be best-effort. If a listed peer is down, memberlist logs the failure and moves on to the next address. If it contacted at least one, the join succeeds. This is also why listing node-3 when node-3 is not yet running is completely fine — memberlist gets "connection refused", skips it, and joins via the others.

The behavior is similar to Elasticsearch's `discovery.seed_hosts`: seeds are starting points, not membership declarations.

---

### Why does the peers list include the node itself?

It does not need to. memberlist ignores self-connections — attempting to connect to your own gossip address is a no-op. Including yourself is harmless but redundant.

The reason it appears in the test configs is convenience: all three nodes share an identical peers list, which makes the config generation loop simpler. In production, listing yourself wastes a connection attempt on startup. The cleaner approach is to list only the other nodes.

---

### What does `bind_addr` do?

`bind_addr` tells the gossip socket which network interface to bind to. It controls where the node *listens*, not where it connects to others.

- `0.0.0.0` — listen on all network interfaces. Any other node on any reachable network can connect. This is the standard production choice.
- `127.0.0.1` — listen only on loopback. Only processes on the same machine can connect. Useful for local testing where all nodes run on one machine.
- `192.168.1.10` — bind to a specific interface. Useful when a machine has multiple NICs and you want gossip to travel only over the internal network.

`bind_addr` and `advertise_addr` work together:

- `bind_addr` is the interface the socket opens on.
- `advertise_addr` is the address the node tells others to use when reaching it.

On a machine with both a public IP (1.2.3.4) and an internal IP (10.0.0.5), you might bind on `0.0.0.0` but advertise `10.0.0.5` so that intra-cluster gossip stays on the internal network.

**Critical:** `advertise_port` must always equal `bind_port`. memberlist constructs UDP probe packets using the advertised address and port. If they differ, probes reach the wrong port, the target node never responds, and memberlist declares it "suspect" then "dead" — even while the process is running perfectly.

---

### How does `min_quorum_ratio` work? Do I need to change it on all nodes?

Each node evaluates quorum independently, on its own schedule (every 5 seconds), using only its own config values and its own membership view. There is no distributed consensus about what the quorum threshold is.

The formula is: **minimum alive nodes = floor(expected_node_count × min_quorum_ratio) + 1**

For `expected_node_count: 3` and `min_quorum_ratio: 0.5`:
- floor(3 × 0.5) + 1 = floor(1.5) + 1 = 1 + 1 = **2 nodes must be alive**

If alive nodes drop below 2, the node sets `isolated = true`, stops dispatching alerts, and logs `[CLUSTER] quorum lost`.

Because each node computes this independently, if nodes have *different* values, they can reach *different* conclusions about whether quorum exists. Node-1 might think quorum is lost while node-2 thinks it is fine, and node-2 would then send alerts while node-1 suppresses them. This is not catastrophic, but it is confusing.

Best practice: keep `expected_node_count` and `min_quorum_ratio` identical across all nodes. When changing these values, update all nodes and reload (or restart) within a short window.

`expected_node_count` should reflect your *intended* cluster size, not the current number of running nodes. During a rolling deployment where you temporarily have 2 of 3 nodes running, quorum is still satisfied because 2 ≥ floor(3 × 0.5) + 1.

---

### What is TCP push/pull?

memberlist uses two independent mechanisms to propagate state:

**UDP gossip (fast path):** Every `GossipInterval` milliseconds (roughly 200ms by default), each node picks a few random peers and sends them a small UDP packet containing recent state changes — new members, departures, health updates. Each node fans out to multiple targets, so information spreads exponentially. This is the primary channel for rapid convergence.

**TCP push/pull (reconciliation path):** Every `PushPullInterval` seconds (roughly 30s by default), each node picks one random peer and opens a TCP connection. Both sides exchange their *complete* membership table and merge the result. This corrects any inconsistencies that accumulated because UDP packets were dropped, reordered, or arrived out of order.

Together they give you gossip's key property: **eventual consistency without a central coordinator**. UDP gets you speed; TCP push/pull guarantees correctness over time.

The debug logs that say `[DEBUG] memberlist: Initiating push/pull sync with: node-2 127.0.0.1:7942` are these TCP reconciliation cycles. They appear roughly every 30 seconds per node pair and are completely normal.

---

### How do nodes actually communicate? Every possible case.

Here is the full lifecycle of inter-node communication:

**1. Initial join**

A new node calls `list.Join(peers)`. For each peer address, it dials a TCP connection and sends a push/pull message containing its own state. The peer responds with its complete membership table. After this exchange, the new node knows all existing members. The existing node broadcasts a gossip message to the rest of the cluster: "a new node arrived." Within a few gossip rounds (usually under a second), every node in the cluster has updated its membership list.

**2. Routine health monitoring**

Every `ProbeInterval` (roughly 1 second), each node picks one random member and sends it a UDP ping. If the pinged node responds with an `ack`, it is considered alive. If no `ack` arrives within `ProbeTimeout`, memberlist initiates an indirect probe: it asks two other nodes to ping the suspect node and report back. This avoids false positives from transient network hiccups between the two nodes directly.

**3. Suspicion**

If both direct and indirect probes fail, the node is moved to "suspect" state. A `suspect` message is gossiped to the whole cluster. The suspected node, if still alive, will hear this, immediately broadcast an `alive` message contradicting the suspicion, and the cluster updates accordingly. If no contradiction arrives within `SuspicionMult × log(N)` intervals, the node is declared dead.

**4. Death declaration**

Once a node is declared dead, a `dead` message is gossiped. All other nodes remove it from their membership tables. In netwatch, `NotifyLeave` is called, which triggers `updateRing()` — the consistent hash ring is rebuilt without the dead node, and a different node may now become responsible for alerting on certain targets.

**5. Graceful leave (`POST /cluster/leave`)**

The departing node sends a `leave` message (which is gossiped as a `dead` message of its own choosing). This is more polite than dying: other nodes receive the notification immediately instead of waiting for the suspicion timeout. In netwatch this is the recommended shutdown path.

**6. State broadcast (netwatch-specific gossip)**

Beyond memberlist's own membership messages, netwatch broadcasts its probe results across the cluster. When a target changes state (UP → HARD_DOWN), the engine calls `Broadcast()`, which queues a serialized `GossipPayload` for transmission via the next gossip round. Peers receive this in their `NotifyMsg` callback, parse the payload, and update `peerStates`. The responsible node (determined by consistent hash) uses this shared state to decide whether an alert is justified.

**7. Anti-entropy on rejoin**

When a node restarts and rejoins, memberlist's push/pull mechanism triggers a join-time state sync (the `LocalState(join=true)` / `MergeRemoteState(join=true)` path). netwatch uses this hook to reconcile probe state: if the cluster already knows a target is `hard_down` with `seq=5`, and the rejoining node has `seq=5` with `alarm_sent=true`, it updates its local state but does not fire another alert. The `syncing` flag suppresses alarm dispatch during this window.

**8. Key rotation**

All gossip messages are encrypted with AES using the keyring. When you rotate keys, you add the new key to the *front* of the keyring on all nodes. memberlist encrypts outgoing messages with the first key and tries all keys for decryption. This means during a rolling update, new and old nodes can still communicate — old nodes decrypt with the old key, new nodes encrypt with the new key but still accept the old key for inbound messages. Once all nodes are updated, remove the old key.

---

### What is "fake-down"? Is "soft-down" what you mean?

"fake-down" is the **name of a test target** — not a state. It is called "fake" because nothing is actually listening on `127.0.0.1:19999`; the connection is intentionally refused to simulate a real service being down. It is a testing device, not a technical term.

The actual states in the system are:

**UP:** The last probe succeeded within the timeout. The target is healthy. No alarm pending.

**SOFT_DOWN:** A probe failed but `max_retries` has not been exhausted yet. The failure is held in memory only — nothing is written to `state.json` and no alert is sent. This phase exists to absorb transient network blips. A single dropped packet should not wake someone at 3 AM.

**HARD_DOWN:** All retry attempts failed. The target is declared hard-down. The state is written to `state.json` and an alert is dispatched (subject to cluster quorum and responsibility checks). This is the only state that produces an alert.

State transitions:

```
UP → SOFT_DOWN    first probe failure, retries queued
SOFT_DOWN → UP    a retry succeeds (silent recovery)
SOFT_DOWN → HARD_DOWN    all retries exhausted → alert fired
HARD_DOWN → UP    probe succeeds again → "reachable" alert fired, seq incremented
```

The `seq` counter (Lamport sequence) increments on every HARD_DOWN and every recovery. It lets you correlate alert pairs: `seq=1` unreachable and `seq=2` reachable belong to the same incident.

---

### Does every node need an `app_name`? What is the difference between `app_name` and `apps`?

These are two completely separate concepts that happen to share a word.

**`app_name`** (top-level config field) is the *monitoring agent's identity*. It appears in Prometheus metric labels (`app_name="payments-monitor"`) and in the `APP_NAME` environment variable of every alert script. Its purpose is to tell you *which agent* produced a given metric or alert when you have multiple netwatch instances monitoring different things. Every node should have one, but there is no requirement that different nodes share the same value — you could run one node as `"app_name: infra-monitor"` and another as `"app_name: app-monitor"`. It is purely a label.

**`apps`** (optional config section) is a *service ownership mapping*. It has nothing to do with operating system processes or agent identity. An "app" here means "a named service with an owner team that depends on certain targets." When one of those targets goes down, the alert is enriched with `AFFECTED_APPS=payment-gateway` and `OWNER_TEAMS=fintech-sre` so the right team gets paged.

You are right to be skeptical about having apps defined inside each node's config. Here is the intended mental model:

All nodes are identical — they probe the same targets, carry the same apps definition, and apply the same notification routing. The apps section is not "this node's apps"; it is "the service catalog that every node in this cluster should know about." The primary responsible node reads this catalog when constructing an alert. If nodes have different apps sections, the node that fires the alert uses its own catalog — which could produce different enrichment depending on which node happens to be primary for that target.

The practical consequence: **keep the apps section identical across all nodes**, or omit it entirely if you do not need service ownership enrichment. You are not forced to use apps at all. Without apps, alerts still fire — they just lack `AFFECTED_APPS` and `OWNER_TEAMS`.

A node can have any number of apps defined. There is no limit. Each app can reference multiple targets via `uses`. The same target can appear in multiple apps (a shared database owned by two teams, for example).

---

### How does App → Target work exactly?

When a target transitions to HARD_DOWN, the engine calls `buildAppTargetIndex()` once at startup and again on each hot-reload. This index is a map from target ID (or name, if no ID is set) to a list of `*App` objects that reference that target via their `uses` list.

At alert time, the engine:

1. Looks up the target's ID in `appIndex`.
2. Collects all matching apps.
3. Builds the notification channel set: **union(target.notify, app.notifications for each matching app)**, deduplicated.
4. If the union is empty, falls back to `default_notify`.
5. Builds the alert environment: `AFFECTED_APPS` = comma-separated app names, `OWNER_TEAMS` = comma-separated owner teams (deduplicated).
6. Calls each channel's `Send()` with that environment.

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

When `payments-db` goes down:
- Channels fired: `{"ops-pager", "slack-fintech", "pagerduty-security"}` (union, 3 channels)
- `AFFECTED_APPS=payment-gateway,fraud-detection`
- `OWNER_TEAMS=fintech-sre,security-team`

If `payments-db` had no `notify` field and no app referenced it, `default_notify` would apply. If `default_notify` is also empty, the alert is silently logged (no channel called).

---

### Does state.json cause false alerts for UP targets on restart?

Not for targets that are UP. On restart, if `state.json` does not exist, all targets start as UNKNOWN. They probe immediately. A target that responds successfully is marked UP and no alert is produced — there was no prior state to contradict.

The storm scenario applies specifically to targets that were **already hard-down before the restart**:

- Without `state.json`: the agent restarts with no memory. The target fails its probe. After `max_retries` failures it declares HARD_DOWN and sends an alert. This is a *duplicate* — the on-call engineer already got this alert an hour ago.
- With `state.json`: the agent loads `state=hard_down, alarm_sent=true`. When the target fails again, it sees that the alarm was already dispatched for this incident (`seq=3`) and does not fire again until the target recovers and goes down again.

In cluster mode the problem is amplified further. On a rolling restart of all three nodes, each node that comes back up with no state.json goes through the probe→retry→hard_down sequence independently. Three nodes means potentially three re-fired alerts for the same ongoing outage.

The "smart system" defense here is exactly `state.json` combined with anti-entropy. Anti-entropy tells the rejoining node "seq=3 was already dispatched." state.json tells it "I was already in hard_down." Together they let the node skip re-alerting. Without either one, there is no way to distinguish "this is a new outage" from "this is the same outage I already know about."

---

### Why both TCP and UDP? Is UDP not unreliable?

UDP is unreliable in the sense that individual packets can be dropped, reordered, or duplicated. But "unreliable" does not mean "useless" — it means you cannot depend on any single packet arriving.

Gossip protocols are specifically designed to tolerate packet loss. Each piece of information is gossiped repeatedly to multiple peers. If one UDP packet carrying "node-3 is alive" gets dropped, the next gossip round sends it again to different peers. Over a few rounds, the information reaches everyone. The mathematical property of gossip (epidemic dissemination) guarantees convergence even with substantial packet loss.

UDP is preferred for gossip because:
- No connection setup overhead — fire and forget
- Low latency — no handshake, no acknowledgement wait
- Works well at high fan-out — broadcasting to 10 peers simultaneously via TCP would open 10 connections; with UDP it is 10 packets on one socket

TCP is used where reliability genuinely matters:
- **Push/pull state sync** — exchanging complete membership tables. A dropped packet here could leave the sync incomplete.
- **Join messages** — establishing initial membership. You need confirmation that the join was received.
- **Large reliable messages** — netwatch's `BroadcastReliable()` path, used for important state transitions.

The combination gives you the best of both: UDP for fast propagation of frequent, small, recoverable messages; TCP for occasional, large, must-not-fail exchanges.

---

### What is the pids file? Is it required?

The `/tmp/netwatch-demo/pids` file is a shell convenience created in the test scripts — it stores the process IDs of the three background nodes so that later `kill` commands can reference them with `read N1 N2 N3 < /tmp/netwatch-demo/pids`. It is not part of netwatch at all. The binary never reads or writes it.

In real deployments, process management is handled by systemd (which tracks the PID itself), Kubernetes (which manages the pod lifecycle), or your init system of choice. The pids file is purely a testing shortcut.

---

### Is `list.Join` behavior correct when a peer is unreachable?

Yes. `list.Join()` is documented as best-effort. It iterates the provided addresses, attempts a TCP connection to each, and counts successes. "Connection refused" means the process is not running yet, which is completely normal when all nodes start simultaneously. memberlist does not treat a failed peer address as a fatal error.

netwatch wraps this with a background `runRejoinLoop`: if after the initial join only 1 member is visible (the node itself), it retries `list.Join()` every 5 seconds. This handles the simultaneous-start case where all nodes try to join each other before any of them is listening. Within about 5–10 seconds, all nodes find at least one peer and converge into a full cluster.

The contacted count you see in logs (`"cluster joined" contacted=2`) tells you how many seed nodes responded. Contacted < len(peers) is normal and expected.

---

### Do all nodes probe the same targets? What do nodes know about each other's targets and apps?

This is one of the most important architectural points to understand, because the answer shapes how you design your deployment.

**netwatch is a mesh of identical monitors, not a distributed task scheduler.**

Every node probes every target defined in its own config file — independently, on its own schedule, with no coordination. If node-1, node-2, and node-3 all have `payments-db` in their config, all three of them open TCP connections to `payments-db` every `probe_interval_sec` seconds. They do not negotiate who probes when. They do not take turns. They do not share retry state. Each node maintains its own independent view of whether that target is up or down.

What nodes *do* share via gossip is their **probe results** — not their configs. When node-1 declares `payments-db` hard-down, it broadcasts a `GossipPayload` containing the target ID, state, and sequence number. Node-2 and node-3 receive this and store it in their `peerStates` map. Now every node in the cluster knows what every other node *concluded* about every target.

This shared state is used for two things:
- **Scope calculation:** If all three nodes see `payments-db` as down → `SCOPE=GLOBAL` (genuine outage). If only node-1 sees it down → `SCOPE=NODE_LOCAL` (likely a network partition or routing issue specific to node-1's location).
- **Exactly-once alerting:** The consistent-hash ring determines which node is primary for each target. Only the primary sends the alert — it does not matter which node actually detected the failure first.

Nodes do **not** share their config, their target definitions, or their apps sections over gossip. Node-2 has no idea what targets node-1 has configured unless both have the same targets in their own config files. This is intentional: configs are operator-managed, not auto-distributed.

**Practical implication:** Identical target lists across all cluster nodes is still the recommended deployment model for shared infrastructure. However, netwatch handles the mixed-config case correctly via the **primary-forwards-peer-alert** mechanism:

If node-1 is the consistent-hash primary for `payments-db` but only node-2 has `payments-db` in its config:
- Node-2 probes `payments-db`, declares it hard-down, and broadcasts a `GossipPayload` that includes the target's display name and type.
- Node-1 receives the gossip via `OnStateReceived`. It is the primary, the state is `hard_down`, and it does **not** have `payments-db` in its local config (`HasLocalProbe` returns false).
- Node-1 calls `DispatchPeerAlert` and fires the alert on behalf of node-2. The `NODE_NAME` in the alert shows node-2 (the node that actually detected the failure).
- Node-2 still suppresses its own alert (`not responsible`).
- Result: exactly one alert, from the correct primary, even with different target configs.

This also means your `apps:` section on each node provides best-effort enrichment: if node-1 has `payments-db` in its `apps.uses` list (even without a local probe), `AFFECTED_APPS` and `OWNER_TEAMS` are still injected into the forwarded alert.

The identical-config recommendation stands for correctness of `SCOPE` calculation (GLOBAL vs NODE_LOCAL): if only one node probes a target, the scope will always be `NODE_LOCAL` regardless of the real cause, because no other node can contribute a data point.

---

### When does cluster mode make sense, and when does it not?

The most important architectural decision when deploying netwatch is choosing between cluster mode and standalone mode. Getting this wrong does not break anything, but it does create unnecessary load and confusion.

**Cluster mode is the right choice when you need fault-tolerant alerting from multiple vantage points.** Three nodes in three different availability zones means: if one zone loses connectivity, the other two still probe and alert. The cluster coordination ensures you receive exactly one notification per incident rather than three. This is the primary value of cluster mode — redundancy without noise.

**Cluster mode is the wrong choice when nodes are colocated and probe the same infrastructure.** If all three of your monitoring nodes live in the same datacenter, a network failure that takes them all down simultaneously also takes out the gossip mesh. You lose both the redundancy and the cluster coordination overhead simultaneously. Three standalone nodes in three datacenters outperforms one cluster in one datacenter.

**The OpenShift / Kubernetes DaemonSet question:**

Running netwatch as a DaemonSet on every node in a 100-node OpenShift cluster and having each pod probe all 100 applications creates a **probe storm**: 100 monitoring pods × 100 application targets × 1 probe per 15 seconds = 667 probes per second hitting your applications. A typical health endpoint is cheap to serve, but 667 requests per second just from monitoring is a meaningful floor that scales with cluster size. This is not the right model.

The correct model for OpenShift/Kubernetes:
- Run **2–3 netwatch pods** (not a DaemonSet) in a dedicated monitoring namespace.
- Each pod probes all application endpoints over the cluster network.
- The 2–3 pod cluster gives you fault tolerance without the probe multiplication.
- If you need per-node local service monitoring (localhost probes), run one **standalone** netwatch per node for only that node's local services, and keep it out of the cluster.

**Is netwatch just a lightweight Zabbix?**

Not exactly, but the comparison is fair enough to be worth unpacking.

Zabbix is an agent-based monitoring platform. Agents run on monitored hosts, collect metrics locally, and ship them to a central Zabbix server. The server aggregates, stores, and alerts. It has a database, a UI, user management, escalation policies, and years of accumulated features. It monitors the insides of systems (CPU, memory, processes, logs) as well as network connectivity.

netwatch is a **synthetic prober with cluster-aware alerting**. It only cares about "can I reach this endpoint from my vantage point?" It has no database, no UI, no central server. Its output is Prometheus metrics and alert notifications. It is closer to the Prometheus Blackbox Exporter than to Zabbix.

The use cases where netwatch genuinely adds value over existing tools:
- You want probe-based monitoring (not just Prometheus scraping) with built-in redundant alerting that does not require a separate Alertmanager setup.
- You need exactly-once alerting from multiple probe locations with automatic scope detection (GLOBAL vs NODE_LOCAL).
- You have multi-protocol requirements beyond HTTP (TCP health checks, SQL connectivity, DNS resolution, ICMP ping) without deploying separate exporters for each.
- You want lightweight deployment — a single binary, no database, no external dependencies.

The use cases where Prometheus + Blackbox Exporter + Alertmanager is a better fit:
- You already have Prometheus infrastructure and just need probe results fed into it.
- You need sophisticated alert routing, silencing, grouping, and inhibition rules.
- You have a large team and need a UI for managing alert policies.
- You are monitoring 500+ targets with complex routing requirements.

netwatch and Zabbix/Prometheus are not direct substitutes. The question is what layer of your monitoring stack you want to fill with what tool.

**When it makes sense to have different targets per node:**

There is one legitimate use case for non-uniform configs: monitoring localhost-only services. Suppose each server runs its own PostgreSQL instance accessible only on 127.0.0.1. In this case:
- Run netwatch in **standalone mode** (`cluster: enabled: false`) on each host, each monitoring its own local Postgres.
- Or use cluster mode with per-node configs accepting that scope will always be `NODE_LOCAL` for these targets (only one node ever probes them).

For shared infrastructure targets (external APIs, shared databases, DNS servers), identical configs are the right model.

---

### Why does `app_name` exist when `node_name` already does?

They serve genuinely different purposes, but the user intuition that they overlap is correct — and in practice, many deployments leave them identical or near-identical.

`node_name` is a **cluster-internal identifier**. memberlist uses it to uniquely address nodes in the ring. It appears in gossip messages, consistent-hash computations, and the `/cluster/state` endpoint. It is meaningless outside the cluster.

`app_name` is a **Prometheus and alerting label**. It appears as a label on every metric (`app_name="payments-monitor"`) and as the `APP_NAME` environment variable in every alert script. Its purpose is to distinguish between *multiple separate netwatch deployments* when they share the same Prometheus instance or alerting pipeline.

A concrete scenario where they differ: you run two separate netwatch clusters — one monitoring your payment infrastructure and one monitoring your internal tooling. Both clusters have nodes named `node-1`, `node-2`, `node-3`. If both write to the same Prometheus, every metric from both clusters would collide. Setting `app_name: "payments-monitor"` on the first cluster and `app_name: "tooling-monitor"` on the second disambiguates them in dashboards and alert routing.

If you only ever run one netwatch cluster, `app_name` is indeed redundant. Setting it to the same value as the cluster purpose (e.g., "infra-monitor") still adds filtering ergonomics in Prometheus queries but provides no logical difference from just using `node_name`.

---

### What happens if state.json is deleted from only one node?

Say you have a 3-node cluster where `payments-db` has been hard-down for an hour. All nodes have `seq=5, alarm_sent=true` in state and gossip. You delete state.json on node-2 and restart it.

Node-2 restarts with no persistent state. It begins probing. `payments-db` fails → soft-down → hard-down. Node-2 has `seq=1` for this target (fresh counter). It broadcasts `GossipPayload{target_id:"payments-db", state:"hard_down", seq:1}`.

Node-1 and node-3 receive this and compare: their `seq=5 > 1`. They reject node-2's stale broadcast (lower sequence wins nothing; higher sequence always wins).

During the push/pull anti-entropy sync on rejoin, node-2 receives node-1's full state with `seq=5, alarm_sent=true`. The anti-entropy merge logic (`MergeRemoteState`) applies the higher-seq remote state to node-2's local copy. Node-2 now knows `seq=5, alarm_sent=true`.

In theory this prevents a duplicate alert. In practice there is a narrow race window: if node-2 is the consistent-hash primary for `payments-db` and it fires the alert **before** anti-entropy sync completes, a duplicate goes out. The `syncing` flag is designed to close this window — alarm dispatch is suppressed while anti-entropy is in progress. Anti-entropy runs at join time, before the probe loops are allowed to alert.

**Summary of outcomes:**

| State when restarted | node-2 is primary? | Result |
|---|---|---|
| Target UP, no prior issue | either | No alert. Fresh probe succeeds. Normal operation. |
| Target hard-down, state.json missing | No | node-2 suppresses (`not responsible`). No duplicate. |
| Target hard-down, state.json missing | Yes | Anti-entropy sync runs first. If it completes before the probe loop escalates, no duplicate. Race window exists but `syncing` flag mitigates it. |
| Target hard-down, state.json present | Yes | state.json has `alarm_sent=true`. No re-alert even if primary. |

The bottom line: deleting state.json from one node is not catastrophic, but it is the one scenario where a duplicate alert is theoretically possible. Keep state files intact.

---

### How much server load do all these recurring processes create?

Each process by itself is negligible. The question is whether they add up to something meaningful.

**UDP gossip (every ~200ms per node):**
memberlist fans out to 3 peers per round by default, regardless of cluster size. That is 3 outbound UDP datagrams per 200ms = 15 per second per node. Each datagram is typically 100–500 bytes. For a 5-node cluster, total cluster-wide gossip traffic is roughly 75 small UDP packets per second — comparable to a lightly busy DNS resolver. CPU cost is microseconds per packet.

**TCP push/pull (every ~30 seconds per node):**
One TCP connection per 30 seconds. The exchanged payload is the full membership table — a few kilobytes even for a large cluster. If you have 10 nodes this is 10 × (1 connection / 30s) = 0.33 TCP connections per second cluster-wide. Essentially zero.

**Probe goroutines:**
Each target has one goroutine that sleeps for `probe_interval_sec` (typically 60 seconds), wakes up, opens one TCP/HTTP/DNS connection, closes it, then sleeps again. A goroutine in Go costs roughly 2–8 KB of stack and zero CPU while sleeping. For 100 targets, that is 100 sleeping goroutines = under 1 MB of stack memory and 0% CPU between probes. During a probe, a TCP connect/close takes a few milliseconds.

**Probe retry (only when something is down):**
In steady-state healthy operation, zero retries happen. Retries only occur for targets that are actually failing. Even with 10 targets simultaneously failing: 10 retries over 10–30 seconds — far from a concern.

**Config reload (every `reload_interval_sec`):**
One file read + YAML parse per interval. A 10 KB config file parses in microseconds. Invisible.

**Rejoin loop (only while isolated):**
Runs every 5 seconds only while `NumMembers() == 1`. Once the cluster forms, it exits. Cost during normal operation: zero.

**Cumulative verdict:** On a modern server, a netwatch node consumes single-digit megabytes of RAM and well under 1% CPU in normal operation. Probe-heavy configs (hundreds of targets, short intervals) will increase CPU proportionally but remain modest. The bottleneck in practice is outbound network connections for probes, not the internal coordination overhead.

---

### Does a 100-node cluster create slowness or excessive load?

No, and the reason is gossip's fundamental property: **each node's per-round work is constant regardless of cluster size.**

Each gossip round, a node contacts a fixed number of peers (3 by default, configurable as `GossipNodes`). It does not contact all N nodes. This means adding more nodes does not increase any individual node's gossip load. A 100-node cluster looks identical to a 5-node cluster from any single node's perspective in terms of gossip CPU and bandwidth.

Convergence time (how long until every node knows about a change) grows as O(log N), not O(N). With 200ms gossip interval and 3 fan-out:
- 5 nodes: convergence in ~2–3 rounds (~0.5s)
- 100 nodes: convergence in ~5–7 rounds (~1.2s)
- 1000 nodes: convergence in ~7–10 rounds (~2s)

This is why gossip protocols scale to large clusters. memberlist is documented to work well up to a few hundred nodes. Beyond ~1000 nodes, you start hitting practical limits around message batching and the size of the membership table in memory.

**What actually scales linearly:** The number of probes the cluster sends to your monitored targets. If 100 nodes each probe `payments-db` every 60 seconds, `payments-db` receives 100 TCP connections per minute. For most services this is trivial, but it is worth considering for targets that are themselves under load.

**A note on deployment model:** In most real-world monitoring setups, you run 3–5 netwatch nodes — not 100. The reason is redundancy, not coverage. Three nodes in three different availability zones gives you fault tolerance (any two can go down while the third keeps alerting) without the coordination overhead of a larger cluster. You do not need 100 monitoring agents to watch 100 servers; you need 3 well-placed agents that can all reach all 100 servers over the network.

If you have 100 physical servers and want each to monitor its own local services, run netwatch in **standalone mode** (`cluster: enabled: false`) on each, with each node monitoring only its own localhost services. Use a separate small cluster (3 nodes) for shared infrastructure. This is the architecture that scales without bottlenecks.

---

## License

MIT
