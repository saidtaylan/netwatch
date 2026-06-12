# Notifications & channels

When the responsible node decides to alert, it dispatches to one or more **channels**. There are three channel types, all receiving the same [alert env variables](alert-env).

## Defining channels

```yaml
notifications:
  ops-script:
    type: "script"
    parameters: { script: "/etc/netwatch/notify.sh" }
  dba-mail:
    type: "mail"
    parameters:
      host: "smtp.internal"
      port: "587"
      from: "netwatch@example.com"
      to: "dba@example.com"
      tls_mode: "starttls"        # starttls | tls | none
  pager:
    type: "webhook"
    parameters:
      url: "https://events.pagerduty.com/v2/enqueue"
      format: "generic"          # generic | alertmanager

default_notify: ["ops-script"]
```

Channels are storage-backed and gossip-replicated; once seeded from `config.yaml` they are managed via the UI / API.

## Channel selection

For each down target the channels are chosen by:

1. `union(target.notify, apps.notifications)` for every app that references the target — deduplicated.
2. If that set is empty → `default_notify`.
3. If still empty → the alert is logged only (info), never silently lost.

## Type: script

Runs an executable (`.sh` on Linux, `.ps1` on Windows) with the alert env exported. The `script` parameter is the path; if omitted, `<scripts_dir>/<channel-name>` is used. This is the most flexible channel — your script can do anything (Slack, PagerDuty, ticket creation), and can branch on `CLASSIFICATION` to suppress local failures.

## Type: mail

Sends SMTP mail as `multipart/alternative` (plain + HTML). The HTML body includes an affected-apps table. TLS:

| Parameter | Meaning |
|---|---|
| `tls_mode` | `starttls` (upgrade on 587), `tls` (implicit on 465), or `none`. |
| `tls_insecure` | `true` skips certificate verification (test only). |
| `ca_cert` | Path to a custom CA bundle. |
| `username` / `password` | SMTP auth. |

## Type: webhook

HTTP POST in one of two formats:

**`generic`** — a single JSON object:

```json
{
  "name": "db-probe", "target": "127.0.0.1:5432", "host": "127.0.0.1", "port": "5432",
  "status": "unreachable", "type": "tcp", "seq": 1,
  "error_code": "connection refused", "affected_apps": "payment-service",
  "owner_teams": "fintech-sre", "fired_at": "2026-05-06T07:39:38Z"
}
```

**`alertmanager`** — a Prometheus Alertmanager v2 `/api/v2/alerts` array (`alertname: ProbeDown`; on recovery `ProbeUp` with `endsAt`). Lets you feed netwatch into an existing Alertmanager.

Extra webhook parameters:

| Parameter | Meaning |
|---|---|
| `timeout_sec` | Request timeout. |
| `tls_insecure` | Skip TLS verification. |
| `header_<Name>` | Add a custom header, e.g. `header_Authorization: Bearer xxx`. |
| `username` / `password` | HTTP Basic auth. |

## Recovery

Recovery is just another dispatch with `STATUS=reachable` (and, for alertmanager, `alertName=ProbeUp` + `endsAt=now`). The monotonic `SEQ` lets consumers pair an outage with its later recovery.
