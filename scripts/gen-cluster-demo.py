#!/usr/bin/env python3
"""Generate 5-node cluster-demo configs under workspace/cluster-demo/."""
import os

ROOT = "/Users/saidtaylan/Documents/network cluster/cluster-demo"
KEY = "8LQlBXa99ZlCSiM7Qp0kFqj2vYrGUTpsoyDmiCoiAB4="
ZONES = ["zone-istanbul", "zone-ankara", "zone-izmir", "zone-bursa", "zone-antalya"]

for i in range(1, 6):
    http = 10240 + i
    gossip = 17940 + i
    zone = ZONES[i - 1]
    d = f"{ROOT}/n{i}"
    os.makedirs(f"{d}/log", exist_ok=True)
    os.makedirs(f"{d}/notifications", exist_ok=True)

    notif = (
        '#!/usr/bin/env bash\n'
        'echo "[ops] $(date -u +%FT%TZ) $*" >> "$(dirname "$0")/../log/ops.log"\n'
    )
    sp = f"{d}/notifications/ops.sh"
    with open(sp, "w") as f:
        f.write(notif)
    os.chmod(sp, 0o755)

    cfg = f"""admin:
  setup_token: demo-setup-token-shared-secret

port: "{http}"
node_alias: node-{i}
log_path: {d}/log/netwatch.log
state_file: {d}/state.json

timeout: 7
max_retries: 1
retry_interval_sec: 10
ticker_interval_sec: 5
probe_interval_sec: 20
recovery_probes: 1
reload_interval_sec: 0

cluster:
  enabled: true
  node_name: node-{i}
  bind_addr: 127.0.0.1
  advertise_addr: 127.0.0.1
  bind_port: {gossip}
  expected_node_count: 5
  min_quorum_ratio: 0.5
  probe_replication_factor: 3
  zone: {zone}
  keyring:
    - {KEY}
  peers:
    - 127.0.0.1:17941
    - 127.0.0.1:17942
    - 127.0.0.1:17943
    - 127.0.0.1:17944
    - 127.0.0.1:17945

default_notify:
  - ops

notifications:
  ops:
    type: script
    parameters:
      script: {d}/notifications/ops.sh

apps:
  - name: demo-app
    owner_team: demo-team
    notifications:
      - ops
    uses:
      - google-dns
      - fake-down

targets:
  - id: self-loopback
    name: self-loopback-{i}
    target: 127.0.0.1:{http}
    type: tcp
  - id: google-dns
    name: google-dns
    target: 8.8.8.8:53
    type: tcp
  - id: fake-down
    name: fake-unreachable
    target: 127.0.0.1:1
    type: tcp

slo:
  enabled: true
  retention_days: 30
  slo_notify:
    - ops
  targets:
    - id: google-dns
      target_uptime: 0.99
      window: 1h
"""
    with open(f"{d}/config.yaml", "w") as f:
        f.write(cfg)
    print(f"[gen] n{i}: HTTP={http} GOSSIP={gossip} ZONE={zone}")

print("done")
