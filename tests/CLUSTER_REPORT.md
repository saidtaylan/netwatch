# netwatch — 16-Node Cluster E2E Test Report

> **Date:** Thu May 14 23:35:58 +03 2026
> **Platform:** 26.2
> **Go version:** go1.26.0
> **netwatch commit:** 04343e3 fix: SLO metrics on startup + DispatchPeerAlert syncing guard removed + E2E cluster test

---

## Cluster Architecture

| Nodes | Zone | HTTP Ports | Gossip Ports | Notes |
|---|---|---|---|---|
| node-01..04 | istanbul | 15001–15004 | 17001–17004 | min_probe_confirmations=2 on node-01 |
| node-05..07 | ankara | 15005–15007 | 17005–17007 | |
| node-08..09 | izmir | 15008–15009 | 17008–17009 | probe_from for target-vpn |
| node-10 | antalya | 15010 | 17010 | single-node zone |
| node-11..16 | (none) | 15011–15016 | 17011–17016 | zoneless |

**Keyring:** AES-256 (ISq9otLFkB+t...)
**probe_replication_factor:** 3 (default)
**expected_node_count:** 16
**min_quorum_ratio:** 0.5 → need ≥9 alive nodes

## Mock Targets

| Target ID | Type | Port | Status |
|---|---|---|---|
| target-api-gw | HTTP | 18001 | depends_on: target-primary-db |
| target-primary-db | TCP | 18002 | core dependency |
| target-redis | TCP | 18003 | |
| target-msgbroker | TCP | 18004 | depends_on: target-redis |
| target-vpn | TCP | 18005 | probe_from: [node-08, node-09] |
| target-dead | TCP | 18099 | never listening — always DOWN |

## Apps

| App | Team | Targets |
|---|---|---|
| e-commerce | platform-sre | target-api-gw, target-primary-db |
| analytics-platform | data-eng | target-primary-db, target-msgbroker |
| realtime-service | infra-dev | target-redis, target-msgbroker |

## Test Cases

| Result | Test | Detail |
|---|---|---|
| ✅ PASS | T01 — 16-node cluster formation | All 16 members visible from node-01 |
| ✅ PASS | T02 — Every node has consistent member view | 6 sampled nodes all see 16 members |
| ✅ PASS | T03 — /health returns 200 on all nodes | All 16 /health → 200 |
| ✅ PASS | T04 — /metrics returns probe metrics | network_probe_local_status found (7 time-series) |
| ✅ PASS | T05 — Only 3 nodes probe each target (probe_replication_factor=3) | target-primary-db prober_count=3 |
| ✅ PASS | T06 — Non-prober nodes have local_assigned=0 | Exactly 3 nodes have local_assigned=1 for target-primary-db (stable) |
| ✅ PASS | T07 — Probers selected from different zones when possible | /cluster/probers returns 200 |
| ✅ PASS | T08 — Hard-down alert fires for always-down target | target-dead fired 1 unreachable alert(s) |
| ✅ PASS | T09 — Exactly one unreachable alert per hard-down event | Exactly 1 alert for target-dead |
| ✅ PASS | T10 — AFFECTED_APPS injected in alert env | target-dead has no apps — AFFECTED_APPS correctly empty |
| ✅ PASS | T11 — SCOPE env var present in alert | SCOPE=GLOBAL in alert |
| ✅ PASS | T12 — /fleet/status returns valid JSON with target summary | summary.total=6, hard_down=1 |
| ✅ PASS | T13 — /fleet/status?format=text returns ASCII table | ASCII table received (20 lines) |
| ✅ PASS | T14 — /topology shows dependency chain | /topology valid (6 targets; dep chain: '') |
| ✅ PASS | T15 — /cluster/config returns drift snapshot | drift_count=0, peers_in_snapshot=13 |
| ✅ PASS | T16 — /geo/latency/{targetID} returns per-node data | Geo latency snapshot for target-api-gw, 15 nodes reported |
| ✅ PASS | T17 — /slo returns uptime metrics | /slo returns data (2 SLO entries) |
| ✅ PASS | T18 — /slo?format=text returns readable table | SLO text table contains uptime data |
| ✅ PASS | T19 — Admin endpoints reject wrong token (403) | POST /cluster/config/sync → 403 with wrong token |
| ✅ PASS | T20 — Admin endpoints reject missing token (401) | POST /cluster/config/sync → 401 without token |
| ✅ PASS | T21 — POST /cluster/config/sync distributes to all peers | Synced to all 15 peers (0 failures) |
| ✅ PASS | T22 — PUT /cluster/config applies partial SharedConfig to all nodes | applied_locally=true fields=watchdog_threshold_sec, |
| ✅ PASS | T23 — probe_from=['node-08','node-09'] limits probers to izmir zone | Assigned probers: node-08 node-09 — no unauthorized probers |
| ✅ PASS | T24 — network_probe_target_orphaned fires when probe_from nodes are gone | target-vpn orphaned=1 after killing both izmir probers |
| ✅ PASS | T25 — Re-joining node restores prober assignment | target-vpn no longer orphaned after node-08 rejoin (orphan=1, node-08 assigned=1) |
| ✅ PASS | T26 — Paused node (SIGSTOP) is detected as suspect, then resumes cleanly | Cluster restored to 16 members after node-05 resume (was 15 during pause) |
| ✅ PASS | T27 — Node rejoin does not produce duplicate alerts (anti-entropy) | Rejoin produced 0 new alerts (expected ≤2 for ongoing hard-down target) |
| ✅ PASS | T28 — Losing quorum causes isolated mode | isolated=1 quorum_healthy=0 — detected in 22s |
| ✅ PASS | T29 — No new alerts while node is isolated | No new alerts during isolated mode |
| ✅ PASS | T30 — Quorum recovery exits isolated mode | Quorum restored — isolated=0, quorum_healthy=1, size=16 |
| ❌ FAIL | T31 — target-primary-db down triggers ROOT_CAUSE in target-api-gw alert | No alert for target-api-gw after 40s — debug info above |
| ❌ FAIL | T32 — Restarting services triggers 'reachable' alerts | No recovery alerts (db=0 api=0) after 30s |
| ❌ FAIL | T33 — min_probe_confirmations=2 allows alert when ≥2 probers confirm | No alert after 30s — min_probe_confirmations may be blocking when it should not |
| ❌ FAIL | T34 — network_probe_prober_underreplicated=1 when probers < factor | Expected prober_underreplicated=1, got '' after 30s |
| ✅ PASS | T35 — Keyring rotation (add new key) keeps cluster intact | Key added (count=2), cluster size maintained (2→2) |
| ✅ PASS | T36 — GET /cluster/keyring/rotate returns key info | keyring info: key_count=2 |
| ✅ PASS | T37 — /cluster/probers reports zone labels for members | 2 cluster members have zone labels in /cluster/probers |
| ✅ PASS | T38 — POST /cluster/leave triggers graceful departure | Leave accepted, size 2→2 (may need more time) |
| ✅ PASS | T39 — /status returns target list with seq and error_code | 6 targets in /status, 5 with seq>0 |
| ✅ PASS | T40 — node_alias appears as app_name label in Prometheus metrics | app_name="node-01" label present in metrics |
| ✅ PASS | T41 — SLO Prometheus metrics appear in /metrics | network_probe_slo_uptime_ratio present (4 entries) |
| ✅ PASS | T42 — Cluster Prometheus metrics present (quorum, isolated, size) | All cluster metrics present (cluster_size=3) |
| ✅ PASS | T43 — Node without node_alias still works (optional field) | node-11 /status returns valid JSON (optional alias supported) |
| ✅ PASS | T44 — All targets have prober_count ≤ probe_replication_factor | All sampled targets have prober_count ≤ 3 |
| ❌ FAIL | T45 — Final cluster state (all nodes alive, quorum OK) | Cluster not fully healthy: size=3 quorum=0 isolated=1 |

---

## Summary

| | Count |
|---|---|
| ✅ PASS | 40 |
| ❌ FAIL | 5 |
| ⏭ SKIP | 0 |
| **Total** | 45 |

**Total alerts fired during test:** 1

## Alert Log (first 30 entries)

```
2026-05-14T20:29:32Z STATUS=unreachable TARGET=127.0.0.1:18099 NAME=Dead Service SCOPE=GLOBAL AFFECTED_APPS= ROOT_CAUSE=target-dead NODE_ALIAS=node-10 SEQ=1
```

## Failed Tests — Analysis

The following tests failed and may indicate bugs or timing issues:
- | ❌ FAIL | T31 — target-primary-db down triggers ROOT_CAUSE in target-api-gw alert | No alert for target-api-gw after 40s — debug info above |
- | ❌ FAIL | T32 — Restarting services triggers 'reachable' alerts | No recovery alerts (db=0 api=0) after 30s |
- | ❌ FAIL | T33 — min_probe_confirmations=2 allows alert when ≥2 probers confirm | No alert after 30s — min_probe_confirmations may be blocking when it should not |
- | ❌ FAIL | T34 — network_probe_prober_underreplicated=1 when probers < factor | Expected prober_underreplicated=1, got '' after 30s |
- | ❌ FAIL | T45 — Final cluster state (all nodes alive, quorum OK) | Cluster not fully healthy: size=3 quorum=0 isolated=1 |

