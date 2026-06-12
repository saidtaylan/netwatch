# Getting started

This is the operator's quick path: get a node running, log in, and add your first target.

## 1. Run a node

The fastest way is Docker (one agent, instant API + metrics):

```bash
docker run -d --name netwatch -p 10240:10240 --cap-add NET_RAW \
  ghcr.io/saidtaylan/netwatch:latest
curl http://localhost:10240/health      # → OK
```

For a real deployment (systemd, Ansible, Helm) see [Deployment](deployment). Either way you need `admin.setup_token` set in the config before the UI works.

## 2. First login

The UI is a three-step first-run wizard:

1. **Connect** — enter a backend node URL (e.g. `http://localhost:10240`). The frontend stores it and connects directly; it can hold several node URLs and fail over between them.
2. **Setup** *(first run only)* — if no admin exists yet, enter the `setup_token` from the config and create your admin username + password.
3. **Login** — afterwards, just username + password. The JWT is kept in the browser and works against any node.

> The setup page is usable exactly once. After the first admin exists, the token has no further effect (except password recovery).

## 3. Add a target

Targets live in the cluster database, not in `config.yaml` (which only seeds them on first boot). Add one from the UI:

- **Targets → + New Target** → pick a type (tcp/http/ping/dns/sql), enter the address, optionally set an interval, notify channels, or dependencies.

The change is written locally and **gossiped to every node** within a second or two — see [How a target propagates](gossip-propagation). Or do it over the API:

```bash
curl -X PUT http://localhost:10240/targets/my-api \
  -H "Authorization: Bearer $JWT" \
  -d '{"id":"my-api","type":"http","target":"https://api.internal/health"}'
```

## 4. Read the UI

| Page | What it shows |
|---|---|
| **Cluster Overview** | Quorum health, node count, targets up/down, config drift, cluster members with zones. |
| **Targets** | All targets with live status, scope/classification badges, search and filters; detail page has the by-node breakdown, dependencies, prober set and geo latency. |
| **Topology** | The dependency graph and cascading impact. |
| **SLO** | Per-target uptime, error budget and incident history. |
| **Geo Latency** | Per-region latency and anomalies. |
| **Alerts** | Recent alert feed. |
| **Maintenance / Silences** | Create and manage suppression windows and matcher mutes. |
| **Channels** | Notification channels (script/mail/webhook). |
| **Config Sync / Push / Keyring** | Cross-node config diff, shared-config push, key rotation. |
| **Users** | (admin) user management. |
| **Docs** | This documentation. |

## 5. Wire up alerting

Define at least one [notification channel](notifications) and set `default_notify`, or attach `notify:` to specific targets / apps. Test it by pointing a target at a closed port and watching the alert fire — then recover it.
