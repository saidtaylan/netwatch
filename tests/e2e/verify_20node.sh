#!/usr/bin/env bash
# Focused verification: 20 nodes form a cluster, gossip propagates, alerts work.
# Exits 0 on full success, non-zero on any failure.
set -u
BIN="/tmp/nwtest-e2e"
DIR="/tmp/v20"
N=20
PASS=0; FAIL=0
ok()   { PASS=$((PASS+1)); echo "✓ $*"; }
bad()  { FAIL=$((FAIL+1)); echo "✗ $*"; }

cleanup() {
  pkill -f "nwtest-e2e" 2>/dev/null
  pkill -f "127.0.0.1:21099" 2>/dev/null
  sleep 1
}
trap cleanup EXIT

cleanup
rm -rf "$DIR"
mkdir -p "$DIR"

KEYRING=$("$BIN" keyring generate)

# Mock target on port 21099 (always down) and 21001 (up, will be killed mid-test)
python3 -c "
import socket
s = socket.socket(); s.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1)
s.bind(('127.0.0.1', 21001)); s.listen(64)
while True:
    try: c,_=s.accept(); c.close()
    except: break
" &
SVC_UP=$!
sleep 0.3

# Alert handler
cat > "$DIR/alert.sh" << 'SH'
#!/bin/bash
echo "$(date -u +%s) STATUS=$STATUS NAME=$NAME SEQ=$SEQ SCOPE=$SCOPE NODE_ALIAS=$NODE_ALIAS" >> /tmp/v20/alerts.log
SH
chmod +x "$DIR/alert.sh"
touch "$DIR/alerts.log"

# Generate 20 node configs
# 5 zones × 4 nodes
ZONES=(zone-a zone-b zone-c zone-d zone-e)
for i in $(seq 1 $N); do
  mkdir -p "$DIR/n$(printf %02d $i)"
  PORT=$((21100 + i))
  GPORT=$((21200 + i))
  ZIDX=$(( (i - 1) / 4 ))
  ZONE="${ZONES[$ZIDX]}"

  cat > "$DIR/n$(printf %02d $i)/config.yaml" << YAML
node_alias: "n$(printf %02d $i)"
port: "$PORT"
state_file: "$DIR/n$(printf %02d $i)/state.json"
log_path: ""
timeout: 2
max_retries: 1
retry_interval_sec: 5
probe_interval_sec: 5
ticker_interval_sec: 1
reload_interval_sec: 0
notifications:
  ops:
    type: script
    parameters:
      script: "$DIR/alert.sh"
default_notify: ["ops"]
targets:
  - id: "alive-target"
    name: "Alive TCP"
    type: tcp
    target: "127.0.0.1:21001"
  - id: "dead-target"
    name: "Dead TCP"
    type: tcp
    target: "127.0.0.1:21099"
cluster:
  enabled: true
  node_name: "n$(printf %02d $i)"
  bind_addr: "127.0.0.1"
  bind_port: $GPORT
  advertise_addr: "127.0.0.1"
  advertise_port: $GPORT
  peers: ["127.0.0.1:21201"]
  keyring: ["$KEYRING"]
  expected_node_count: $N
  min_quorum_ratio: 0.5
  probe_replication_factor: 3
  zone: "$ZONE"
YAML
done

# Start node 01 first
"$BIN" -config "$DIR/n01/config.yaml" >> "$DIR/n01.log" 2>&1 &
sleep 2

# Start remaining 19 nodes in 4 batches
for batch_start in 2 7 12 17; do
  batch_end=$((batch_start + 4))
  [[ $batch_end -gt $N ]] && batch_end=$N
  for i in $(seq $batch_start $batch_end); do
    "$BIN" -config "$DIR/n$(printf %02d $i)/config.yaml" >> "$DIR/n$(printf %02d $i).log" 2>&1 &
  done
  sleep 1
done

# Wait for cluster formation (up to 60s)
for attempt in $(seq 1 60); do
  SIZE=$(curl -sf --max-time 2 http://127.0.0.1:21101/cluster/state 2>/dev/null | jq '.members | length' 2>/dev/null)
  [[ "$SIZE" == "$N" ]] && break
  sleep 1
done
SIZE=$(curl -sf --max-time 2 http://127.0.0.1:21101/cluster/state 2>/dev/null | jq '.members | length' 2>/dev/null)
[[ "$SIZE" == "$N" ]] && ok "20-node cluster formed in ${attempt}s" || bad "Cluster size $SIZE/20 after 60s"

# Verify consistent view from 5 different nodes
MISMATCH=0
for p in 21101 21105 21110 21115 21120; do
  S=$(curl -sf --max-time 2 "http://127.0.0.1:$p/cluster/state" 2>/dev/null | jq '.members | length' 2>/dev/null)
  [[ "$S" != "$N" ]] && MISMATCH=1 && echo "  port $p sees $S members"
done
[[ $MISMATCH -eq 0 ]] && ok "All 5 sampled nodes see 20 members" || bad "Inconsistent member views"

# Verify probe_replication_factor=3 honored for alive-target
sleep 8  # let metrics stabilize
PROBER_COUNT=$(curl -sf "http://127.0.0.1:21101/metrics" 2>/dev/null | grep "network_probe_prober_count" | grep "alive-target" | tail -1 | awk '{print $NF}')
[[ "$PROBER_COUNT" == "3" ]] && ok "prober_count=3 for alive-target" || bad "Expected prober_count=3, got '$PROBER_COUNT'"

# Verify exactly 3 nodes have local_assigned=1
ASSIGNED=0
for i in $(seq 1 $N); do
  PORT=$((21100 + i))
  V=$(curl -sf "http://127.0.0.1:$PORT/metrics" 2>/dev/null | grep "network_probe_local_assigned" | grep "alive-target" | tail -1 | awk '{print $NF}')
  [[ "$V" == "1" ]] && ASSIGNED=$((ASSIGNED+1))
done
[[ "$ASSIGNED" == "3" ]] && ok "Exactly 3 nodes assigned for alive-target" || bad "Got $ASSIGNED assignees, want 3"

# Verify dead-target produces exactly 1 alert (exactly-once across 20 nodes)
sleep 5
DEAD_ALERTS=$(grep "NAME=Dead TCP" "$DIR/alerts.log" 2>/dev/null | wc -l | tr -d ' ')
[[ "$DEAD_ALERTS" == "1" ]] && ok "Exactly 1 alert for dead-target (exactly-once across 20 nodes)" || bad "Dead alerts: $DEAD_ALERTS (expected 1)"

# Verify zone-aware spread — selected probers should come from at least 2 different zones
PROBER_NODES=()
for i in $(seq 1 $N); do
  PORT=$((21100 + i))
  V=$(curl -sf "http://127.0.0.1:$PORT/metrics" 2>/dev/null | grep "network_probe_local_assigned" | grep "alive-target" | tail -1 | awk '{print $NF}')
  [[ "$V" == "1" ]] && PROBER_NODES+=($i)
done
# Compute zones
declare -A ZONE_SEEN
for i in "${PROBER_NODES[@]}"; do
  ZIDX=$(( (i - 1) / 4 ))
  ZONE_SEEN["${ZONES[$ZIDX]}"]=1
done
UNIQUE_ZONES=${#ZONE_SEEN[@]}
[[ "$UNIQUE_ZONES" -ge 2 ]] && ok "Zone-aware: 3 probers from $UNIQUE_ZONES zones" || bad "All probers in same zone (zone-aware not working)"

# Kill alive service, wait for hard-down alert
kill $SVC_UP 2>/dev/null
sleep 25
ALIVE_ALERTS=$(grep "NAME=Alive TCP" "$DIR/alerts.log" 2>/dev/null | grep "STATUS=unreachable" | wc -l | tr -d ' ')
[[ "$ALIVE_ALERTS" == "1" ]] && ok "Exactly 1 alert when alive-target killed" || bad "Got $ALIVE_ALERTS unreachable alerts for alive-target (expected 1)"

# Verify quorum loss + recovery
# Kill 11 nodes (need ≥11 for quorum of 20)
KILLED=(2 3 5 7 9 11 13 15 17 19 20)
for n in "${KILLED[@]}"; do
  P="$DIR/n$(printf %02d $n).log"
  PIDS=$(pgrep -f "config $DIR/n$(printf %02d $n)/config.yaml")
  for p in $PIDS; do kill -KILL "$p" 2>/dev/null; done
done

# Poll for isolated=1 (up to 40s)
for attempt in $(seq 1 40); do
  ISOLATED=$(curl -sf "http://127.0.0.1:21101/metrics" 2>/dev/null | grep "^network_prober_isolated " | awk '{print $NF}')
  [[ "$ISOLATED" == "1" ]] && break
  sleep 1
done
[[ "$ISOLATED" == "1" ]] && ok "Quorum loss → isolated=1 (in ${attempt}s)" || bad "Isolation not detected after 40s"

echo
echo "═══════════════════════════════════════════════"
echo "  PASS: $PASS  /  FAIL: $FAIL"
echo "═══════════════════════════════════════════════"
[[ $FAIL -eq 0 ]] && exit 0 || exit 1
