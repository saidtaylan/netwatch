# Config sync & drift

In a cluster, certain config fields are supposed to be **identical on every node** — the keyring, the prober replication factor, the quorum settings. If they drift apart (someone edited one node's `config.yaml` and forgot the others), the cluster can behave inconsistently. netwatch detects and helps fix that.

## Shared vs bootstrap fields

Not all config can be compared by hashing `config.yaml`, because every node's file *must* differ in bootstrap fields (`node_name`, `bind_port`, `state_file`, ports). So netwatch distinguishes:

- **Bootstrap fields** — node-specific by design; never compared.
- **Shared fields** — must match cluster-wide: `keyring`, `peers`, `expected_node_count`, `min_quorum_ratio`, `probe_replication_factor`, `probe_replication_percent`, `min_probe_confirmations`.

Only the shared fields participate in drift detection.

## Drift detection

Two complementary mechanisms:

**1. Fingerprint gossip.** Each node broadcasts a fingerprint of its config; `GET /cluster/config` returns this node's hash plus every peer's hash and a drift count. The `network_probe_config_drift` metric is `1` when any peer disagrees. This is enabled with:

```yaml
cluster:
  config_sync:
    enabled: true
    mode: "drift_detection"   # drift_detection (default, safe) | auto_sync (reserved)
    sync_interval_sec: 30
```

**2. Field-level diff (pull).** Because gossip is lossy, the UI's Config Sync page actively pulls each peer's shared fields over HTTP and shows a **field-by-field** diff:

- `GET /cluster/sync/effective` — this node's shared config fields (bootstrap excluded).
- `GET /cluster/sync/aggregate` — fan-out to every reachable peer, returning per-node `same` / `differs` with the exact `field: { baseline, peer }` pairs.

```json
{
  "baseline_node": "node-1",
  "peers": [
    { "node_name": "node-2", "same": true, "diff_count": 0, "fields": {} },
    { "node_name": "node-3", "same": false, "diff_count": 1,
      "fields": { "timeout": { "baseline": 3, "peer": 7 } } }
  ]
}
```

## Pushing config to peers

When you do want to converge a shared field across the cluster:

- `PUT /cluster/config` — send a partial shared config; the node applies it locally and gossips it (over TCP) to all peers.
- `POST /cluster/config/sync` — re-broadcast this node's current shared fields to peers.

Both require auth and are refused (503) when the node has lost quorum, so a split-brain minority can't push divergent config.

## Why detection-only by default

`mode: drift_detection` only *surfaces* drift; it never silently overwrites a node's config. Auto-sync (one node pushing its config to the rest) is reserved and off by default, because silently rewriting config is risky — you usually want a human to see the diff (in the UI) and decide. The keyring is the one field where drift is operationally critical: if it diverges, gossip encryption breaks and nodes can't talk — see [Security](security).
