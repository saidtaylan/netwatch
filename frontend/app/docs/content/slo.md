# SLO tracking

netwatch can track per-target **Service Level Objectives** — uptime targets over a rolling window, with an error budget and edge-triggered breach alerts. Enable it under `slo:` in the config.

```yaml
slo:
  enabled: true
  retention_days: 90
  slo_notify: ["ops"]
  targets:
    - id: "db-primary"
      target_uptime: 0.999     # 99.9%
      window: "30d"            # 30d | 7d | 24h
```

## Concepts

| Term | Meaning |
|---|---|
| **Target uptime** | The objective, e.g. `0.999` = 99.9%. |
| **Window** | The rolling measurement window: `30d`, `7d`, or `24h`. |
| **Actual uptime** | Measured ratio of up-time within the window. |
| **Error budget** | The downtime you're *allowed*: `(1 − target) × window`. A 99.9% / 30d target allows ~43 minutes/30 days. |
| **Breach** | The budget is exhausted (remaining seconds ≤ 0). |

## How it's measured

Outages are recorded as **incidents** when the state machine fires `markHardDown` / `markRecovered`:

- `RecordStart` opens an incident; `RecordEnd` closes it.
- Incidents persist to `incidents.json` (next to `state_file`) in the `slo_incidents` table. **They are local-only** — each node keeps its own observations; aggregating them cluster-wide would multiply downtime.
- Open incidents (no end time) are re-opened automatically after a restart, so a crash mid-outage doesn't lose the incident.

`ComputeSLO(target, targetUptime, window)` evaluates the SLO over a **rolling** window ending now. For each incident overlapping `[now − window, now]`:

```
start    = max(incident.startedAt, now − window)   # clamp to window
end      = incident.endedAt  OR  now               # an open incident counts up to "now"
downtime += max(0, end − start)
```

then (with `windowSec = window in seconds`, `downtime` capped at `windowSec` as a clock-skew guard):

```
actualUptime = (windowSec − downtime) / windowSec
errorBudget  = round(windowSec × (1 − targetUptime))   # allowed downtime, seconds
remaining    = errorBudget − downtime                  # negative ⇒ breached
breached     = actualUptime < targetUptime
```

So an **active** outage erodes the budget in real time (its `end` is `now`), and the result also exposes the incident count and the longest single incident — surfaced as alert vars and in the UI.

## Breach alerts

A background checker (`runSLOChecker`, hourly) evaluates each SLO target and sends a breach alert via `slo_notify` (or `default_notify`). Breach alerts are **edge-triggered**: exactly one alert per breach period. When the target returns within budget the flag clears, so a future breach alerts again.

Breach alerts carry extra env vars (`STATUS=slo_breached`): `SLO_TARGET_UPTIME`, `SLO_ACTUAL_UPTIME`, `SLO_WINDOW`, `SLO_DOWNTIME_MINUTES`, `SLO_INCIDENT_COUNT`, `SLO_ERROR_BUDGET_SEC`, `SLO_LONGEST_INCIDENT_SEC`.

## Retention

`retention_days` prunes incidents older than the cutoff so the store doesn't grow unbounded.

## Where you see it

- **UI** — the SLO dashboard: per-target target%, actual%, status, remaining budget, incident history.
- **API** — `GET /slo` (returns 503 if `slo.enabled: false`); `GET /slo?format=text` for a terminal table.
- **CRUD** — `GET/PUT/DELETE /slo/targets/{id}`.
- **Metrics** — `network_probe_slo_uptime_ratio`, `network_probe_slo_error_budget_seconds`, `network_probe_slo_breached`.
