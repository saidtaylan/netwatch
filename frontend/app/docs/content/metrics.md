# Metrics reference

netwatch exposes Prometheus metrics on `GET /metrics`. Cluster metrics are only registered when `cluster.enabled: true`; SLO metrics only when `slo.enabled: true`.

## Per-node probe metrics

| Metric | Type | Labels | Meaning |
|---|---|---|---|
| `network_probe_local_status` | gauge | name, target, type, source_host, app_name | This node's last probe result: `1`=UP, `0`=DOWN. |
| `network_probe_local_latency_seconds` | gauge | name, target, type, source_host, app_name | Last probe round-trip time. |
| `network_probe_prometheus_connected` | gauge | — | `1`=scraping normal, `0`=scrape watchdog threshold exceeded. Always `1` when `watchdog_threshold_sec: 0`. |

## Cluster metrics

Registered only when `cluster.enabled: true`.

| Metric | Type | Labels | Meaning |
|---|---|---|---|
| `network_probe_cluster_status` | gauge | name, target, type, source_host, app_name | Consensus result: `1` if all nodes see up, `0` if any sees down. |
| `network_prober_quorum_healthy` | gauge | — | `1`=quorum intact, `0`=lost. |
| `network_prober_isolated` | gauge | — | `1`=isolated mode (alerts suppressed). |
| `network_prober_cluster_size` | gauge | — | Alive members this node sees. |
| `network_probe_local_assigned` | gauge | name, target, type | `1`=this node probes the target, `0`=not assigned. |
| `network_probe_prober_count` | gauge | name, target, type | Number of nodes selected to probe the target. |
| `network_probe_inventory_peers` | gauge | — | Peers discovered via gossip. |
| `network_probe_target_orphaned` | gauge | name, target, type | `1`=no node probes the target (usually a `probe_from`/`zone` misconfig). |
| `network_probe_config_drift` | gauge | — | `1`=at least one peer has a different config hash. |

## Geo-latency metrics

| Metric | Type | Labels | Meaning |
|---|---|---|---|
| `network_probe_geo_latency_seconds` | gauge | name, target, type, region | Last probe latency per region. |
| `network_probe_geo_latency_anomaly` | gauge | name, target, type | `1`=some node's latency exceeds 3× the minimum (with a 5 ms floor to ignore sub-ms jitter). |

## SLO metrics

Registered only when `slo.enabled: true`.

| Metric | Type | Labels | Meaning |
|---|---|---|---|
| `network_probe_slo_uptime_ratio` | gauge | target_id, window | Actual uptime ratio in the window (`0.0`–`1.0`). |
| `network_probe_slo_error_budget_seconds` | gauge | target_id, window | Remaining error budget in seconds (negative = breached). |
| `network_probe_slo_breached` | gauge | target_id | `1`=SLO breach active. |

## Notes

- Every `GET /metrics` scrape also pokes the **scrape watchdog** (`NotifyScrape`), which drives `network_probe_prometheus_connected`. The watchdog never affects probing or alerting — it only tells you Prometheus went blind.
- Metric names were renamed once from the legacy `netwatch_probe_*` to `network_probe_*`; there are no aliases.
