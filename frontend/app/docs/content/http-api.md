# HTTP API reference

Every node serves the same REST API on `port` (default `10240`). Read endpoints are generally open; **write** endpoints require a JWT (obtained via `/auth/login`) when `admin.setup_token` is configured. Cluster-replicated writes return **503** with `Retry-After` when the node has lost quorum.

## Auth

| Method | Path | Auth | Description |
|---|---|---|---|
| GET | `/auth/status` | none | `{ setup_completed, user_count }`. |
| POST | `/auth/setup` | setup_token | Create the first admin user; returns a JWT. |
| POST | `/auth/login` | none | `username` + `password` → JWT (+ stored cluster node URLs). |
| GET | `/auth/me` | JWT | The current user. |
| PUT | `/auth/password` | JWT | Change your own password. |
| POST | `/auth/reset-password` | setup_token | Reset a user's password (recovery). |
| GET/PUT | `/auth/cluster-nodes` | JWT / admin | Read / update the saved backend node URLs. |
| GET | `/users` | admin JWT | List users. |
| PUT/DELETE | `/users/{id}` | admin JWT | Create/update / delete a user. |

## Health & monitoring

| Method | Path | Description |
|---|---|---|
| GET | `/health` | Liveness — always `200 OK`. |
| GET | `/metrics` | Prometheus scrape (also pokes the scrape watchdog). |
| GET | `/status` | Per-target snapshot: name, status, seq, error_code. |
| GET | `/fleet/status` | Cluster-wide view: consensus state, scope, classification, confidence, by-node breakdown, affected apps, root cause, incidents. `?format=text` for an ASCII table. Works standalone too. |
| GET | `/topology` | Dependency graph: direct deps, reverse deps, transitive cascading impact. |
| GET | `/slo` | SLO results per target (503 if `slo.enabled: false`). `?format=text` available. |
| GET | `/geo/latency/{targetID}` | Per-node latency view: region labels, last latency, anomaly flag. |

## Targets, apps, channels, SLO (CRUD)

These are storage-backed and gossip-replicated; writes need a JWT.

| Method | Path | Description |
|---|---|---|
| GET/PUT/DELETE | `/targets`, `/targets/{id}` | List / upsert / delete a target. |
| GET/PUT/DELETE | `/apps`, `/apps/{id}` | App registry CRUD. |
| GET/PUT/DELETE | `/channels`, `/channels/{name}` | Notification channel CRUD. |
| GET | `/channels/{name}/script` | View a script channel's source (read-only). |
| GET/PUT/DELETE | `/slo/targets`, `/slo/targets/{id}` | SLO target CRUD. |

## Cluster

| Method | Path | Description |
|---|---|---|
| GET | `/cluster/state` | Members + raw peer target states (503 if cluster disabled). |
| GET | `/cluster/probers` | Per-target prober assignment: selected probers, primary, candidates, zones, replication factor. |
| GET | `/cluster/config` | Config-sync snapshot: this node's hash + peer hashes + drift count. |
| PUT | `/cluster/config` | Apply a partial shared config and gossip it to peers (auth). |
| POST | `/cluster/config/sync` | Re-broadcast this node's shared fields to peers (auth). |
| GET | `/cluster/sync/effective` | This node's shared (non-bootstrap) config fields. |
| GET | `/cluster/sync/aggregate` | Field-level diff of shared config across all reachable peers. |
| GET/POST | `/cluster/keyring/rotate` | Keyring status / zero-downtime AES key rotation (`add`/`use`/`remove`). |
| POST | `/cluster/leave` | Graceful cluster leave, then process exit. |
| GET/PUT/DELETE | `/cluster/maintenance`, `/cluster/maintenance/{id}` | Maintenance window CRUD (gossip-replicated). |
| GET/PUT/DELETE | `/cluster/silences`, `/cluster/silences/{id}` | Silence (matcher-based mute) CRUD. |

## Conventions

- **Auth header:** `Authorization: Bearer <jwt>`. The same JWT is valid on **any** node, because all nodes share the `setup_token` HMAC secret.
- **Quorum:** cluster-replicated writes on a node without quorum return `503` + `Retry-After: 10` (`ErrSplitBrain`).
- **Content type:** request and response bodies are JSON unless `?format=text` is requested.
