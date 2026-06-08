# netwatch

Self-hosted, distributed network monitoring with a web UI — no cloud, no agents-as-a-service.

<!-- screenshot / demo gif placeholder -->
<!-- ![netwatch dashboard](docs/screenshot.png) -->

---

## What is netwatch?

**netwatch** is a distributed network monitoring system you run on your own infrastructure. Each backend node is a single Go binary that continuously probes TCP, HTTP/HTTPS, ICMP ping, DNS, and SQL targets, then exposes results over a REST API. Multiple nodes form a leaderless gossip cluster — they share probe state in real time and coordinate to ensure every outage produces exactly one alert, never duplicates, no matter how many nodes are watching the same target.

The frontend is a Nuxt 3 single-page application served by nginx. It connects directly to any backend node in your cluster and gives you a live dashboard: target states, dependency graphs, SLO tracking, per-region latency, and cluster health — all in one place, with no external database required.

netwatch is designed for teams who want the power of Prometheus-level monitoring without the operational complexity of a full Prometheus + Alertmanager + Grafana stack. Everything ships as two binaries and a YAML file.

---

## Features

| Capability | Description |
|---|---|
| **Multi-protocol probes** | TCP, HTTP/HTTPS (status + body assertions), ICMP ping, DNS, SQL (MySQL / PostgreSQL / SQL Server / Oracle) |
| **Smart state machine** | Transient failures enter a *soft-down* retry phase; only after `max_retries` consecutive failures is the target declared *hard-down* and an alert sent |
| **Exactly-once alerting** | Consistent hashing assigns one responsible node per target — only that node sends the alert. Failover is automatic when the primary leaves the cluster |
| **Leaderless cluster** | Gossip-based (hashicorp/memberlist); no single point of failure, no elected leader, no ZooKeeper |
| **LWW conflict resolution** | Lamport sequence numbers on every state change; highest seq wins if two nodes disagree |
| **Distributed probe ownership** | Only `probe_replication_factor` nodes (default 3) probe each target — a 50-node cluster does not hammer targets 50× |
| **Dependency graph & root cause** | `depends_on` relationships between targets let the system report "checkout is down because db-primary is down" instead of alerting on every affected service |
| **Scope classification** | Every alert carries `REAL_OUTAGE` / `NETWORK_PARTITION` / `LOCAL_FAILURE` / `AMBIGUOUS` — you know immediately whether the service or your network is the problem |
| **SLO tracker** | Per-target uptime tracking with rolling windows (30d / 7d / 24h), error budget, and breach alerts |
| **Config drift detection** | Each node gossips its config hash; diverging nodes are immediately visible in the UI and via a Prometheus metric |
| **Quorum gating** | Alert dispatch is suppressed when the cluster loses quorum, preventing false positives from a split-brain node |
| **Geo latency view** | Per-region latency breakdown with anomaly detection — self-hosted multi-region synthetic monitoring |
| **Alert channels** | Shell script (`.sh` / `.ps1`), SMTP (HTML multipart), generic JSON webhook, Prometheus Alertmanager format |
| **App → target indirection** | Group targets under named apps with owner teams; `AFFECTED_APPS` and `OWNER_TEAMS` are injected into every alert |
| **Prometheus metrics** | `/metrics` endpoint with probe status, latency, SLO, cluster, and geo gauges |
| **Hot-reload** | `config.yaml` is re-read on a configurable interval — no restart required |
| **SQLite storage** | Single-binary, no external database — state and incidents persist across restarts |
| **Credentials injection** | `${VAR}` placeholders in config are resolved from a separate `credentials.env` file |
| **Windows Service** | Native Windows Service integration via `netwatch.exe service install/remove` |

---

## Architecture Overview

```
┌──────────────────────────────────────────────────────────┐
│  Browser                                                  │
│  Nuxt 3 SPA — served by nginx as static files            │
│  Connects to any backend node over HTTP                   │
└────────────────────────┬─────────────────────────────────┘
                         │ REST API (port 10240)
          ┌──────────────┼──────────────┐
          ▼              ▼              ▼
   ┌─────────────┐ ┌─────────────┐ ┌─────────────┐
   │  Backend    │ │  Backend    │ │  Backend    │
   │  node-1     │ │  node-2     │ │  node-3     │
   │  Go binary  │ │  Go binary  │ │  Go binary  │
   │  port 10240 │ │  port 10240 │ │  port 10240 │
   └──────┬──────┘ └──────┬──────┘ └──────┬──────┘
          │               │               │
          └───────────────┴───────────────┘
              gossip cluster (port 7946 UDP+TCP)
```

Each backend node:
- Runs its own probe loop independently
- Shares probe state via gossip with all peers
- Stores state in a local SQLite file — no shared database
- Exposes the full API (fleet view, SLO, topology, etc.)

The frontend can connect to **any** backend node — they all serve the same cluster-wide view.

---

## Requirements

| Component | Requirement |
|---|---|
| **OS (production)** | Linux with systemd (Ubuntu 20.04+, Debian 11+, RHEL 8+) |
| **OS (development)** | macOS or Windows |
| **Go** | 1.22+ (only needed to build from source) |
| **Node.js** | 20+ with pnpm (build-time only — runtime is static files) |
| **nginx** | Any recent version (frontend static file serving) |
| **Firewall** | Port `10240` (API) and `7946` TCP+UDP (gossip) open between nodes |

> **ICMP ping probes** require `CAP_NET_RAW` on Linux. The systemd unit file enables this automatically. On macOS/Windows, ping probes require running as root/Administrator.

---

## Installation

### Option A — systemd (Recommended for Linux)

The fastest path for a single node or small cluster on Linux.

#### 1. Build the backend binary

```bash
cd backend
GOOS=linux GOARCH=amd64 go build -o bin/netwatch-linux-amd64 ./cmd/linux/
```

#### 2. Build the frontend

```bash
cd frontend
pnpm install
pnpm build
# Output: frontend/.output/public/  (static files)
```

#### 3. Run the install script

```bash
cd deploy-systemd
sudo ./install.sh
```

This script:
- Creates a `netwatch` system user
- Installs the binary to `/usr/local/bin/netwatch`
- Copies a skeleton `config.yaml` to `/etc/netwatch/config.yaml`
- Copies frontend static files to `/opt/netwatch-ui`
- Installs and starts `netwatch-backend.service`, `netwatch-frontend.service`, and `netwatch.target`

#### 4. Edit the config

```bash
sudo nano /etc/netwatch/config.yaml
```

See [Configuration](#configuration) for the key fields to set.

#### 5. Restart and verify

```bash
sudo systemctl restart netwatch-backend
sudo systemctl status netwatch.target
journalctl -u netwatch-backend -f
```

Check the API is up:
```bash
curl http://localhost:10240/health
```

---

### Option B — Manual (Custom setup)

#### Linux (systemd, step-by-step)

**Backend:**
```bash
# Build
cd backend
GOOS=linux GOARCH=amd64 go build -o netwatch ./cmd/linux/

# Install
sudo install -m 755 netwatch /usr/local/bin/netwatch
sudo mkdir -p /etc/netwatch /var/lib/netwatch
sudo cp backend/config.example.yaml /etc/netwatch/config.yaml
sudo nano /etc/netwatch/config.yaml

# Install and start service
sudo cp deploy-systemd/netwatch-backend.service /etc/systemd/system/
sudo systemctl daemon-reload
sudo systemctl enable --now netwatch-backend
```

**Frontend:**
```bash
cd frontend
pnpm install && pnpm build

# Copy static files to nginx web root
sudo mkdir -p /var/www/netwatch
sudo cp -r .output/public/. /var/www/netwatch/

# nginx — serve the SPA (all routes → index.html)
# Add a server block like:
#   root /var/www/netwatch;
#   location / { try_files $uri $uri/ /index.html; }
sudo nginx -t && sudo systemctl reload nginx
```

---

#### macOS (development only)

> **Note:** macOS is not supported for production. ICMP ping probes require root, and there is no systemd. Use macOS for local development and testing only.

**Backend:**
```bash
cd backend
go run ./cmd/linux/   # or: go build -o netwatch ./cmd/linux/ && ./netwatch
```

**Frontend:**
```bash
cd frontend
pnpm install
pnpm dev   # dev server at http://localhost:3000
```

---

#### Windows

> **Note:** ICMP ping probes require Administrator privileges on Windows.

**Backend:**
```powershell
# Build
cd backend
$env:GOOS="windows"; $env:GOARCH="amd64"; go build -o netwatch.exe ./cmd/windows/

# Install as Windows Service (Administrator PowerShell)
.\netwatch.exe service install --config C:\netwatch\config.yaml
sc.exe start netwatch

# Remove
sc.exe stop netwatch
.\netwatch.exe service remove
```

**Frontend:**
```powershell
cd frontend
pnpm install
pnpm build
# Serve .output/public/ with IIS, nginx for Windows, or any static file server
```

---

#### Docker

```bash
docker run -d \
  --name netwatch \
  -p 10240:10240 \
  -p 7946:7946 \
  -v /etc/netwatch/config.yaml:/etc/netwatch/config.yaml:ro \
  -v netwatch-state:/var/lib/netwatch \
  --cap-add NET_RAW \
  ghcr.io/saidtaylan/netwatch:latest
```

> `--cap-add NET_RAW` is required for ICMP ping probes. Remove it if you don't use `type: ping` targets.

#### Kubernetes (Helm)

A DaemonSet Helm chart ships under [`backend/helm/netwatch`](backend/helm/netwatch). It runs one agent per node with `hostNetwork: true` (so gossip and ICMP work), a headless service for peer discovery, and the keyring as a Secret.

```bash
# Render to review, then install
helm template nw backend/helm/netwatch -f my-values.yaml
helm install   nw backend/helm/netwatch -f my-values.yaml
```

Put your probe targets, cluster settings, and keyring in a values file (see [`values.yaml`](backend/helm/netwatch/values.yaml) for every option):

```yaml
image:
  repository: ghcr.io/saidtaylan/netwatch
  tag: latest

keyring:
  - "base64-encoded-AES-256-key=="   # same on every node

config: |
  node_alias: "netwatch-agent"
  port: "10240"
  cluster:
    enabled: true
    bind_port: 7946
  targets:
    - name: example-tcp
      type: tcp
      target: "10.0.0.5:5432"
```

> The chart name is not hard-coded into resource names — they derive from the release name and `nameOverride`, so you can rename the project freely (`helm install <release> ... --set nameOverride=<newname>`).

---

## First Login

1. Open your browser and navigate to `http://<frontend-ip>` (or `http://localhost:3000` in dev mode).
2. You'll be prompted to enter the **backend node URL** — e.g. `http://192.168.1.10:10240`. The frontend stores this in the browser and connects directly.
3. Go to the **`/setup`** page (linked from the login screen on first run).
4. Enter the `setup_token` from your `config.yaml` (the `admin.setup_token` field).
5. Create your **admin username and password**.
6. That's it — log in with your new credentials. The setup page is only usable once.

> The `setup_token` is a one-time bootstrap secret. After the admin account is created, it has no further effect.

---

## Configuration

Key fields in `/etc/netwatch/config.yaml`:

```yaml
# ── Identity ──────────────────────────────────────────────────────────────────
app_name: "netwatch"         # appears in metrics labels and alert env vars
port:     "10240"            # HTTP API port

# ── Auth ──────────────────────────────────────────────────────────────────────
admin:
  setup_token: "change-me"  # one-time bootstrap token → used on /setup page

# ── Storage ───────────────────────────────────────────────────────────────────
state_file: "/var/lib/netwatch/state.json"

# ── Probe timing ──────────────────────────────────────────────────────────────
probe_interval_sec:  60      # how often each target is probed
retry_interval_sec:  30      # wait between retries after a failure
max_retries:         3       # failures before hard-down (and alert)
timeout:             5       # per-probe timeout in seconds
reload_interval_sec: 30      # config hot-reload interval (0 = disabled)

# ── Credentials ───────────────────────────────────────────────────────────────
credentials_file: "/etc/netwatch/credentials.env"  # ${VAR} source

# ── Notifications ─────────────────────────────────────────────────────────────
notifications:
  ops-webhook:
    type: webhook
    parameters:
      url: "https://hooks.example.com/netwatch"
      format: "generic"      # or "alertmanager"

  ops-mail:
    type: mail
    parameters:
      smtp_host: "smtp.corp.local"
      smtp_port: "587"
      from:      "netwatch@corp.local"
      to:        "sre@corp.local"
      tls_mode:  "starttls"

default_notify: ["ops-webhook"]

# ── Targets ───────────────────────────────────────────────────────────────────
targets:
  - name: "payments-db"
    type: tcp
    target: "db.prod:5432"

  - name: "payment-api"
    type: http
    target: "https://api.payments.prod/health"
    options:
      expected_status:
        in: [200, 204]
      body_contains: '"status":"ok"'
    depends_on: ["payments-db"]

  - name: "office-gateway"
    type: ping
    target: "10.0.0.1"
```

For a full annotated reference of all supported fields and probe types, see [`backend/config.example.yaml`](backend/config.example.yaml).

---

## Multi-Node Cluster

All nodes run the same binary and the same basic config structure. Add a `cluster:` block to each node's `config.yaml`:

```yaml
cluster:
  enabled:             true
  node_name:           "node-1"          # unique per node
  bind_port:           7946
  advertise_addr:      "192.168.1.101"   # this node's IP, reachable by peers
  peers:
    - "192.168.1.102:7946"
    - "192.168.1.103:7946"
  keyring:
    - "c2VjcmV0a2V5MzJieXRlc2xvbmdoZXJlISE="  # base64 AES key, same on all nodes
  expected_node_count: 3
  min_quorum_ratio:    0.5               # suppress alerts if < 50% of nodes alive
  probe_replication_factor: 3            # max nodes that probe any single target
  zone: "eu-central"                     # optional, for geo-aware probe distribution
```

**Rules for a working cluster:**

- Every node must use the **same `keyring`** (gossip encryption key).
- Every node must use the **same `admin.setup_token`** (so any node can bootstrap or serve the UI).
- Port `7946` (TCP + UDP) must be open between all nodes.
- The `peers` list on each node should point to the other nodes' gossip addresses.
- Targets and `depends_on` should be identical across all nodes (drift is detected and reported, but not auto-resolved).

**How gossip works:**

- Nodes exchange probe results continuously via UDP multicast and TCP push-pull.
- State is propagated cluster-wide within seconds.
- Consistent hashing (FNV-32a on target ID) picks one responsible node to fire each alert — no coordination needed, no leader election.
- When the responsible node leaves, the next node in the hash ring takes over automatically.
- On restart, a node performs a full push-pull sync before accepting new probe results, preventing duplicate alerts.

---

## Updating

### Backend binary

```bash
# Rebuild
cd backend
GOOS=linux GOARCH=amd64 go build -o netwatch ./cmd/linux/

# Deploy
sudo install -m 755 netwatch /usr/local/bin/netwatch
sudo systemctl restart netwatch-backend
```

### Frontend

```bash
cd frontend
pnpm install   # pick up any new dependencies
pnpm build

sudo cp -r .output/public/. /var/www/netwatch/
sudo nginx -s reload
```

### Full re-install (using install.sh)

If you used `deploy-systemd/install.sh` originally, re-running it after building new binaries/frontend will update everything in place:

```bash
cd deploy-systemd
sudo ./install.sh
```

---

## Contributing

Pull requests are welcome. For backend build, configuration, and the full API reference, see [backend/README.md](backend/README.md); for the frontend, see [frontend/README.md](frontend/README.md).

Bug reports: open a GitHub issue with your `config.yaml` (redact secrets), Go version, and the output of `journalctl -u netwatch-backend -n 50`.

---

## License

MIT — see [LICENSE](LICENSE).
