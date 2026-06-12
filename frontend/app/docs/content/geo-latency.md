# Geo latency & regions

Because probers are tagged with a region, netwatch can answer *"is the target slow from Europe but fine from US-East?"* — self-hosted, multi-region synthetic latency monitoring, without paying a SaaS.

## Region labels

Give each node a region (separate from `zone`, which is for failure-domain spreading):

```yaml
cluster:
  region: "eu-west"
```

`region` is propagated to every node via memberlist `NodeMeta`, so any node knows every other node's region.

## Per-region latency

On every successful probe, the prober records the round-trip time and broadcasts it with its region. The `GET /geo/latency/{targetID}` endpoint returns a per-node breakdown:

- each probing node's last measured latency,
- its region label,
- and an **anomaly flag** for the target.

The same data drives the `network_probe_geo_latency_seconds{region}` metric and the UI's Geo Latency page.

## Anomaly detection

A latency anomaly is flagged (`detectLatencyAnomaly`) when:

- there are at least **two** non-zero measurements, and
- the maximum exceeds **3×** the minimum, and
- the minimum is at least **5 ms** (an absolute floor).

The 5 ms floor is deliberate: on loopback / same-rack paths, sub-millisecond scheduler jitter can make `0.5 ms` vs `0.17 ms` look like a 3× anomaly. The floor ignores that noise, so only real network-path differences (say 10 ms vs 35 ms) trip the flag.

| Metric | Meaning |
|---|---|
| `network_probe_geo_latency_seconds{name,target,type,region}` | Last probe latency per region. |
| `network_probe_geo_latency_anomaly{name,target,type}` | `1` if some node's latency exceeds 3× the minimum (above the 5 ms floor). |

## Restricting probers by region

You can pin a target to be probed only from certain regions — useful when only some regions have a network path to it:

```yaml
targets:
  - id: "api-gw"
    type: http
    target: "https://api.example.com/health"
    probe_from_regions: ["eu-west", "us-east"]
```

This intersects the prober candidate set with the listed regions, the region-level equivalent of `probe_from`. See [Distributed Probe Ownership](distributed-probe-ownership).
