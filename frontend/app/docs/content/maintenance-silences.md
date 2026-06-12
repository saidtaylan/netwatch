# Maintenance windows & silences

Two complementary ways to stop alerts you already know about — a planned-downtime window, and an ad-hoc matcher-based mute. Both are gossip-replicated, so a window or silence created on one node applies cluster-wide.

## Maintenance windows

A maintenance window suppresses alerts for a set of targets during a planned time range — deploys, migrations, scheduled reboots.

```bash
PUT /cluster/maintenance
{
  "targets": ["db-primary", "api-gateway"],   # ids; empty = all targets
  "starts_at": "2026-06-12T02:00:00Z",
  "ends_at":   "2026-06-12T04:00:00Z",
  "comment":   "Postgres major upgrade"
}
```

While the window is active, the matching targets are still probed (state still updates, metrics still flow) but **no alerts are dispatched**. Outside the window, normal alerting resumes.

| Operation | Endpoint |
|---|---|
| List active windows | `GET /cluster/maintenance` |
| Create | `PUT /cluster/maintenance` (auth) |
| Cancel | `DELETE /cluster/maintenance/{id}` (auth) |

## Silences

A silence is a **matcher-based** mute — the sibling of a maintenance window, for "we know X is broken, stop paging us" during an active incident. Instead of a time window around specific ids, it matches targets by field.

```bash
PUT /cluster/silences
{
  "matchers": [
    { "field": "type", "value": "tcp", "is_regex": false },
    { "field": "name", "value": "db-.*",  "is_regex": true }
  ],
  "duration_sec": 3600,
  "comment": "DB cluster maintenance — known noisy"
}
```

- **Matcher fields:** `id`, `name`, `type`.
- **`is_regex`** matches the value as a regular expression.
- Within one silence, matchers are **AND**ed; multiple silences are **OR**ed.
- Matchers are compiled and validated at creation time (known field, valid regex, non-empty value) — a bad rule never reaches the cluster.
- Expired silences are tombstoned by a background pruner.

| Operation | Endpoint |
|---|---|
| List | `GET /cluster/silences` |
| Create | `PUT /cluster/silences` (auth) |
| Cancel | `DELETE /cluster/silences/{id}` (auth) |

## Which to use

| Use case | Use |
|---|---|
| Planned downtime for specific, known targets | **Maintenance window** (time-bounded, by id). |
| "Mute anything matching this pattern for a while" during an incident | **Silence** (matcher-based, duration-bounded). |

Both are checked in the alert gate before dispatch, so a suppressed target never pages — but it stays fully observable in the UI and metrics, so you can still watch it recover.
