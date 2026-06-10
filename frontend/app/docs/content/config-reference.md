# Configuration reference

Every field of `config.yaml`. Two important rules:

- **Bootstrap fields** (paths, timings, cluster transport, auth secret) are read from `config.yaml` on every start.
- **Seed-only sections** (`targets`, `apps`, `notifications`, `slo.targets`) are written to the database **once**, on first boot with an empty DB. After that the database is authoritative and edits to those sections are ignored — manage them through the UI or REST API instead. See [How a target propagates](gossip-propagation).

A minimal runnable starter lives in `config.skeleton.yaml`; the fully annotated reference is `config.example.yaml`.

## Agent identity

| Field | Type | Default | Description |
|---|---|---|---|
| `node_alias` | string | `"netwatch-agent"` | Human-readable label for this node. Appears in metrics (`app_name` label) and alert env vars (`NODE_ALIAS`, `APP_NAME`). |
| `port` | string | `"10240"` | HTTP port for the REST API, `/metrics` and `/health`. |

## Auth

| Field | Type | Default | Description |
|---|---|---|---|
| `admin.setup_token` | string | _(empty)_ | **Required to use the web UI.** Used once on the `/setup` page to create the first admin user, and as the HMAC secret that signs JWTs. **Must be identical on every node** in a cluster. If empty, `/auth/setup` refuses to create an admin. |

## File paths

| Field | Type | Default | Description |
|---|---|---|---|
| `state_file` | string | `state.json` | Path to the persisted probe state. Its **directory** also holds `netwatch.db`. |
| `log_path` | string | _(empty)_ | Log file path; empty → stdout / journald. |
| `credentials_file` | string | _(empty)_ | A `KEY=VALUE` file used to resolve `${VAR}` placeholders elsewhere in the config. |

## Probe timing

| Field | Type | Default | Description |
|---|---|---|---|
| `timeout` | int (s) | `5` | Per-probe connect/response timeout. |
| `max_retries` | int | `2` | Consecutive failures in the `soft_down` phase before a target is declared `hard_down`. |
| `retry_interval_sec` | int (s) | `30` | Delay between retries while `soft_down`. |
| `probe_interval_sec` | int (s) | `60` | Default probe interval; overridable per target via `interval_sec`. |
| `ticker_interval_sec` | int (s) | `5` | Granularity of the internal scheduler tick. |
| `reload_interval_sec` | int (s) | `30` | How often `config.yaml` bootstrap fields are re-read; `0` disables hot-reload. |
| `recovery_probes` | int | `1` | Consecutive successes required to move `hard_down → up` (soft-up). |
| `watchdog_threshold_sec` | int (s) | `0` | If Prometheus hasn't scraped `/metrics` within this many seconds, log a warning and set `network_probe_prometheus_connected=0`. `0` disables. Never affects probing or alerts. |

## Notifications

```yaml
notifications:
  ops-script:
    type: "script"            # script | mail | webhook
    parameters: { script: "/etc/netwatch/notify.sh" }
default_notify: ["ops-script"]
```

| Field | Type | Description |
|---|---|---|
| `notifications.<name>.type` | enum | `script`, `mail` or `webhook`. |
| `notifications.<name>.parameters` | map | Type-specific (script path; SMTP host/port/from/tls; webhook url/format/headers). |
| `default_notify` | list | Channels used when a target (and its apps) name none. |

Channel selection for a target is `union(target.notify, apps.notifications)`, deduplicated; if empty, `default_notify`; if still empty, the alert is logged only.

## Targets (seed-only)

```yaml
targets:
  - id: "postgres-primary"     # stable unique key; apps reference this
    name: "Postgres Primary"   # display name
    type: tcp                  # tcp | http | ping | dns | sql
    target: "db.internal:5432" # host:port or URL
    interval_sec: 30           # optional per-target override
    notify: ["ops-script"]     # optional
    depends_on: ["other-id"]   # optional; for root-cause detection
    options: { }               # type-specific, preserved as raw JSON
    probe_from: ["node-a"]            # optional; pin probers by node
    probe_from_regions: ["eu-west"]   # optional; pin probers by region
```

| Field | Type | Description |
|---|---|---|
| `id` | string | Stable key. Apps' `uses` and `depends_on` reference it. Falls back to `name` if absent. |
| `name` | string | Display name. |
| `type` | enum | `tcp`, `http`, `ping`, `dns`, `sql`. |
| `target` | string | `host:port` or a URL. |
| `interval_sec` | int | Per-target probe interval (overrides global). |
| `notify` | list | Channels for this target. |
| `depends_on` | list | Upstream target ids for root-cause analysis. Cyclic or unknown ids fail config load. |
| `options` | map | Type-specific; kept as raw JSON so rich options are never lost. |
| `probe_from` / `probe_from_regions` | list | Restrict the prober candidate set. See [Distributed Probe Ownership](distributed-probe-ownership). |

### Type-specific `options`

| Type | Key options |
|---|---|
| `http` | `method`, `expected_status` (`{in: [200,204]}`), `body_contains`, `follow_redirects`, `headers`, `timeout_sec`, `tls_insecure`. |
| `dns` | `resolve` (record type), `expected_ips`, `server`. |
| `sql` | `driver` (`oracle`/`mysql`/`postgres`/`mssql`), `dsn`, `query`. |
| `tcp` / `ping` | none required; `ping` needs `CAP_NET_RAW`. |

## Apps (seed-only)

```yaml
apps:
  - name: "payment-gateway"
    owner_team: "fintech-sre"
    uses: ["postgres-primary", "api-gateway"]   # target ids or names
    notifications: ["dba"]                       # unioned with each target's notify
```

Apps are a **labelling/ownership overlay** — they do not create extra probes. A target shared by several apps is probed once; its alerts are enriched with `AFFECTED_APPS` and `OWNER_TEAMS`.

## Cluster

```yaml
cluster:
  enabled: false
  node_name: "node-1"          # required & unique when enabled
  bind_addr: "0.0.0.0"
  bind_port: 7946
  advertise_addr: "192.168.1.100"
  peers: ["192.168.1.101:7946"]
  keyring: ["base64-AES-key=="]
  expected_node_count: 3
  min_quorum_ratio: 0.5
  zone: "istanbul"
  probe_replication_factor: 3
  probe_replication_percent: 0
  min_probe_confirmations: 0
```

| Field | Type | Default | Description |
|---|---|---|---|
| `enabled` | bool | `false` | `false` → standalone; no network ports opened. |
| `node_name` | string | — | **Required when enabled.** Unique across the cluster. |
| `bind_addr` / `bind_port` | string / int | `0.0.0.0` / `7946` | gossip listen address (TCP+UDP). |
| `advertise_addr` | string | _(auto)_ | Address peers use to reach this node; set behind NAT / in containers. |
| `peers` | list | `[]` | Seed peers; best-effort, all may be temporarily down. |
| `keyring` | list | `[]` | base64 AES-128/192/256 keys for gossip encryption. First encrypts; all decrypt (zero-downtime rotation). |
| `expected_node_count` | int | — | Cluster size used for the quorum calculation. |
| `min_quorum_ratio` | float | `0.5` | Fraction of `expected_node_count` that must be alive for alerts to fire. |
| `zone` | string | _(empty)_ | Failure-domain label; prober selection prefers one node per distinct zone. |
| `probe_replication_factor` | int | `3` | Nodes that probe each target. `0` → 3. Larger than the cluster → everyone probes. |
| `probe_replication_percent` | int | `0` | Prober count as a % of candidates (`ceil(pct/100×N)`, min 1). Overrides the factor when `> 0`. |
| `min_probe_confirmations` | int | `0` | Independent probers that must reach `hard_down` before alerting. `0`/`1` = alert on first. `2` suppresses single-node false alerts at the cost of up to one interval of latency. |
| `region` | string | _(empty)_ | Geographic label (separate from `zone`) used by the geo-latency view and `probe_from_regions`. |
| `config_sync.enabled` | bool | `false` | Broadcast a config fingerprint for drift detection (`network_probe_config_drift`). |

## SLO

```yaml
slo:
  enabled: true
  retention_days: 90
  slo_notify: ["ops"]
  targets:                     # seed-only
    - id: "postgres-primary"
      target_uptime: 0.999     # 99.9%
      window: "30d"            # 30d | 7d | 24h
```

| Field | Type | Description |
|---|---|---|
| `slo.enabled` | bool | Enables SLO tracking, the `/slo` endpoint and SLO metrics. |
| `slo.retention_days` | int | Incident history retention. |
| `slo.slo_notify` | list | Channels for breach alerts; falls back to `default_notify`. |
| `slo.targets[].target_uptime` | float | Target ratio, e.g. `0.999`. |
| `slo.targets[].window` | string | Rolling window: `30d`, `7d`, or `24h`. |
