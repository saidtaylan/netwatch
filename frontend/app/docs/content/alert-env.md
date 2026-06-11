# Alert environment variables

When a target changes state, the responsible node dispatches the alert to the selected channels. **All** channel types — shell script, SMTP mail, and webhook — receive the same set of environment variables (webhooks also embed them in the JSON body). This is the contract you write alert scripts against.

## Always present

| Variable | Meaning |
|---|---|
| `NAME` | Target display name. |
| `TARGET` | `host:port` or URL. |
| `HOST`, `PORT` | Parsed address components. |
| `TYPE` | `tcp` / `http` / `ping` / `dns` / `sql`. |
| `STATUS` | `unreachable` (down) or `reachable` (recovered). |
| `SEQ` | Lamport sequence — increments on every state transition. |
| `ERROR_CODE` | Last failure message; empty on recovery. |
| `NODE_ALIAS` | This agent's `node_alias`. |
| `APP_NAME` | Same as `NODE_ALIAS` (legacy name, kept for compatibility). |
| `NODE_NAME` | `os.Hostname()` of the node that detected the event. |
| `SCOPE` | `GLOBAL` / `PARTIAL` / `NODE_LOCAL` / `STANDALONE`. |
| `CLASSIFICATION` | `REAL_OUTAGE` / `NETWORK_PARTITION` / `LOCAL_FAILURE` / `AMBIGUOUS`. |
| `CONFIDENCE` | `0.00`–`1.00`. |

See [Scope classification](scope-classification) for the meaning of the last three.

## Present when apps reference the target

| Variable | Meaning |
|---|---|
| `AFFECTED_APPS` | Comma-separated app names that `use` this target. |
| `OWNER_TEAMS` | Comma-separated owner teams. |

## Present when `depends_on` is configured and the target is down

| Variable | Meaning |
|---|---|
| `ROOT_CAUSE` | The deepest down dependency — the actual root cause, not the symptom. |
| `CASCADING_IMPACT` | Comma-separated targets that depend on this one. |
| `DEPENDENCY_DEPTH` | Hop distance from the root cause (`0` = this is the root). |

## Present in cluster mode when down

| Variable | Meaning |
|---|---|
| `DOWN_NODES` | Nodes reporting `hard_down`. |
| `UP_NODES` | Nodes reporting `up`. |
| `OFFLINE_NODES` | Alive nodes that haven't reported on this target. |

## SLO breach alerts (`STATUS=slo_breached`)

Sent by the SLO checker, not the state machine. In addition to the common vars:

| Variable | Meaning |
|---|---|
| `SLO_TARGET_UPTIME` | Objective, e.g. `0.9990`. |
| `SLO_ACTUAL_UPTIME` | Measured uptime in the window. |
| `SLO_WINDOW` | `30d` / `7d` / `24h`. |
| `SLO_DOWNTIME_MINUTES` | Total downtime in the window. |
| `SLO_INCIDENT_COUNT` | Incidents in the window. |
| `SLO_ERROR_BUDGET_SEC` | Remaining budget seconds (negative = breached). |
| `SLO_LONGEST_INCIDENT_SEC` | Longest single incident. |

## Example: a minimal script channel

```bash
#!/usr/bin/env bash
# notify.sh — receives the variables above as env.
if [ "$CLASSIFICATION" = "LOCAL_FAILURE" ]; then
  exit 0   # one node's own connectivity — don't page
fi
echo "[$STATUS] $NAME ($TARGET) — scope=$SCOPE class=$CLASSIFICATION conf=$CONFIDENCE root=$ROOT_CAUSE" \
  | mail -s "netwatch: $NAME $STATUS" oncall@example.com
```
