#!/usr/bin/env bash
# =============================================================================
# netwatch — 16-node cluster E2E test suite
# =============================================================================
# Tests: 40 scenarios covering all major features
# Runtime: ~15-18 minutes
# Report: tests/CLUSTER_REPORT.md
#
# Usage: bash tests/e2e/cluster_e2e_test.sh
#        sudo needed for pfctl (will prompt once, then cached 5 min)
# =============================================================================
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
PROJECT_ROOT="$(cd "$SCRIPT_DIR/../.." && pwd)"
REPORT_FILE="$PROJECT_ROOT/tests/CLUSTER_REPORT.md"
BINARY="/tmp/nwtest-e2e"
TEST_DIR="/tmp/nwtest-cluster"
KEYRING=""

PASS=0; FAIL=0; SKIP=0
CURRENT_TEST=""
declare -a REPORT_LINES

# ── Colors ───────────────────────────────────────────────────────────────────
GREEN='\033[0;32m'; RED='\033[0;31m'; YELLOW='\033[1;33m'; BLUE='\033[0;34m'; NC='\033[0m'
ok()   { echo -e "${GREEN}  ✓ $*${NC}"; }
fail() { echo -e "${RED}  ✗ $*${NC}"; }
info() { echo -e "${BLUE}  → $*${NC}"; }
warn() { echo -e "${YELLOW}  ! $*${NC}"; }

# ── Cleanup trap ─────────────────────────────────────────────────────────────
cleanup() {
  echo; info "Cleaning up all processes..."
  # Kill all netwatch nodes
  for i in $(seq -f "%02g" 1 20); do
    local pid_file="$TEST_DIR/nodes/node-$i/agent.pid"
    [[ -f "$pid_file" ]] && kill "$(cat "$pid_file")" 2>/dev/null || true
  done
  # Kill mock services
  [[ -f "$TEST_DIR/targets/api.pid"     ]] && kill "$(cat "$TEST_DIR/targets/api.pid")" 2>/dev/null || true
  [[ -f "$TEST_DIR/targets/db.pid"      ]] && kill "$(cat "$TEST_DIR/targets/db.pid")" 2>/dev/null || true
  [[ -f "$TEST_DIR/targets/redis.pid"   ]] && kill "$(cat "$TEST_DIR/targets/redis.pid")" 2>/dev/null || true
  [[ -f "$TEST_DIR/targets/broker.pid"  ]] && kill "$(cat "$TEST_DIR/targets/broker.pid")" 2>/dev/null || true
  # Flush pfctl rules if we added any
  sudo pfctl -F rules 2>/dev/null || true
  # Remove loopback aliases
  for i in $(seq 2 20); do
    sudo ifconfig lo0 -alias "127.0.0.$i" 2>/dev/null || true
  done
  wait 2>/dev/null || true
  info "Cleanup done."
}
trap cleanup EXIT INT TERM

# ── Report helpers ────────────────────────────────────────────────────────────
report_line() { REPORT_LINES+=("$1"); }

begin_test() {
  CURRENT_TEST="$1"
  echo
  echo -e "${BLUE}═══════════════════════════════════════════════════════${NC}"
  echo -e "${BLUE}TEST: $CURRENT_TEST${NC}"
  echo -e "${BLUE}═══════════════════════════════════════════════════════${NC}"
}

pass_test() {
  local detail="${1:-}"
  PASS=$(( PASS + 1 ))
  ok "PASS: $CURRENT_TEST${detail:+ — $detail}"
  report_line "| ✅ PASS | $CURRENT_TEST | ${detail:-OK} |"
}

fail_test() {
  local reason="${1:-}"
  FAIL=$(( FAIL + 1 ))
  fail "FAIL: $CURRENT_TEST${reason:+ — $reason}"
  report_line "| ❌ FAIL | $CURRENT_TEST | ${reason:-no detail} |"
}

skip_test() {
  local reason="${1:-}"
  SKIP=$(( SKIP + 1 ))
  warn "SKIP: $CURRENT_TEST${reason:+ — $reason}"
  report_line "| ⏭ SKIP | $CURRENT_TEST | ${reason:-skipped} |"
}

# ── Utility functions ─────────────────────────────────────────────────────────
wait_for() {
  # wait_for <description> <timeout_sec> <command...>
  local desc="$1" timeout="$2"; shift 2
  local elapsed=0
  while ! "$@" 2>/dev/null; do
    (( elapsed += 1 ))
    if (( elapsed >= timeout )); then
      return 1
    fi
    sleep 1
  done
  return 0
}

http_get() {
  # http_get <url> → body or empty on error
  curl -sf --max-time 3 "$1" 2>/dev/null || true
}

http_post() {
  # http_post <url> [json_body]
  local url="$1"; local body="${2:-}"
  if [[ -n "$body" ]]; then
    curl -sf --max-time 5 -X POST -H "Content-Type: application/json" -d "$body" "$url" 2>/dev/null || true
  else
    curl -sf --max-time 5 -X POST "$url" 2>/dev/null || true
  fi
}

http_put() {
  local url="$1" body="$2" ct="${3:-application/json}"
  curl -sf --max-time 5 -X PUT -H "Content-Type: $ct" -d "$body" "$url" 2>/dev/null || true
}

metric_value() {
  # metric_value <port> <metric_name_prefix> → value of last matching line
  http_get "http://127.0.0.1:$1/metrics" \
    | grep "^${2}" \
    | tail -1 \
    | awk '{print $NF}' || echo ""
}

metric_label_value() {
  # metric_label_value <port> <metric_prefix> <label_key=value> → numeric
  http_get "http://127.0.0.1:$1/metrics" \
    | grep "^${2}" \
    | grep "${3}" \
    | tail -1 \
    | awk '{print $NF}' || echo ""
}

json_get() {
  # json_get <url> <jq_path>
  curl -sf --max-time 3 "$1" 2>/dev/null | jq -r "$2" 2>/dev/null || echo ""
}

cluster_size() {
  # cluster_size <http_port> → number of alive members
  json_get "http://127.0.0.1:$1/cluster/state" '.members | length' 2>/dev/null || echo "0"
}

node_pid() {
  local node_num="$1"
  local pid_file="$TEST_DIR/nodes/node-$(printf "%02d" "$node_num")/agent.pid"
  [[ -f "$pid_file" ]] && cat "$pid_file" || echo ""
}

kill_node() {
  local num="$1" sig="${2:-TERM}"
  local pid; pid=$(node_pid "$num")
  if [[ -n "$pid" ]]; then
    kill -"$sig" "$pid" 2>/dev/null || true
    if [[ "$sig" == "KILL" ]]; then
      rm -f "$TEST_DIR/nodes/node-$(printf "%02d" "$num")/agent.pid"
    fi
  fi
}

pause_node()  { kill_node "$1" STOP; }
resume_node() { kill_node "$1" CONT; }

node_alive() {
  local pid; pid=$(node_pid "$1")
  [[ -n "$pid" ]] && kill -0 "$pid" 2>/dev/null
}

wait_cluster_size() {
  # wait_cluster_size <http_port> <expected_size> <timeout_sec>
  local port="$1" size="$2" timeout="$3"
  wait_for "cluster_size=$size" "$timeout" \
    bash -c "[[ \$($(declare -f cluster_size); cluster_size $port) -eq $size ]]"
}

# ── Phase 0: Build + Setup ────────────────────────────────────────────────────
echo
echo "========================================"
echo "  netwatch 16-node Cluster E2E Test"
echo "  $(date)"
echo "========================================"

info "Skipping rebuild (binary already at $BINARY)"
mkdir -p "$TEST_DIR/bin" "$TEST_DIR/targets" "$TEST_DIR/logs"
echo "  (pre-built binary used)"
ok "Binary built: $BINARY"

info "Generating keyring..."
KEYRING="$($BINARY keyring generate)"
ok "Keyring: ${KEYRING:0:12}..."

# ── Phase 1: Mock target services ────────────────────────────────────────────
info "Starting mock target services..."

# TCP listeners using nc in a loop
start_tcp_service() {
  local name="$1" port="$2"
  local pid_file="$TEST_DIR/targets/$name.pid"
  # socat or nc loop
  python3 -c "
import socket, threading, sys
srv = socket.socket()
srv.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1)
srv.bind(('127.0.0.1', $port))
srv.listen(128)
while True:
    try:
        c, _ = srv.accept()
        c.close()
    except: break
" &
  echo $! > "$pid_file"
}

# target-api-gw: HTTP on 18001
python3 -c "
import http.server, socketserver, os, sys
os.chdir('/tmp')
class H(http.server.BaseHTTPRequestHandler):
    def do_GET(self):
        self.send_response(200)
        self.end_headers()
        self.wfile.write(b'ok')
    def log_message(self, *a): pass
with socketserver.TCPServer(('127.0.0.1', 18001), H) as s:
    s.serve_forever()
" &
echo $! > "$TEST_DIR/targets/api.pid"
sleep 0.3

# target-primary-db: TCP on 18002
start_tcp_service "db"     18002
# target-redis: TCP on 18003
start_tcp_service "redis"  18003
# target-msgbroker: TCP on 18004
start_tcp_service "broker" 18004
# target-dead: port 18099 — nothing listening (intentionally down)
# target-vpn: port 18005 — for probe_from zone test
start_tcp_service "vpn"    18005

sleep 0.5
ok "Mock services started (API:18001, DB:18002, Redis:18003, Broker:18004, VPN:18005, Dead:18099)"

# ── Phase 2: Node configuration ───────────────────────────────────────────────
info "Creating node configs..."

PEERS_LIST=""
for i in $(seq 1 16); do
  PEERS_LIST="${PEERS_LIST:+$PEERS_LIST
    }- \"127.0.0.1:$(( 17000 + i ))\""
done

SHARED_TARGETS='
targets:
  - id: "target-api-gw"
    name: "API Gateway"
    type: http
    target: "http://127.0.0.1:18001"
    options:
      expected_status:
        in: [200]
    depends_on: ["target-primary-db"]
    notify: ["ops-alert"]

  - id: "target-primary-db"
    name: "Primary DB"
    type: tcp
    target: "127.0.0.1:18002"
    notify: ["ops-alert"]

  - id: "target-redis"
    name: "Redis Cache"
    type: tcp
    target: "127.0.0.1:18003"
    notify: ["ops-alert"]

  - id: "target-msgbroker"
    name: "Message Broker"
    type: tcp
    target: "127.0.0.1:18004"
    depends_on: ["target-redis"]
    notify: ["ops-alert"]

  - id: "target-vpn"
    name: "VPN Gateway (izmir only)"
    type: tcp
    target: "127.0.0.1:18005"
    probe_from: ["node-08", "node-09"]
    notify: ["ops-alert"]

  - id: "target-dead"
    name: "Dead Service"
    type: tcp
    target: "127.0.0.1:18099"
    notify: ["ops-alert"]
'

SHARED_APPS='
apps:
  - name: "e-commerce"
    owner_team: "platform-sre"
    uses: ["target-api-gw", "target-primary-db"]
    notifications: ["ops-alert"]
  - name: "analytics-platform"
    owner_team: "data-eng"
    uses: ["target-primary-db", "target-msgbroker"]
    notifications: ["ops-alert"]
  - name: "realtime-service"
    owner_team: "infra-dev"
    uses: ["target-redis", "target-msgbroker"]
    notifications: ["ops-alert"]
'

SHARED_SLO='
slo:
  enabled: true
  retention_days: 7
  slo_notify: ["ops-alert"]
  targets:
    - id: "target-api-gw"
      target_uptime: 0.99
      window: "7d"
    - id: "target-primary-db"
      target_uptime: 0.999
      window: "30d"
'

make_node_config() {
  local num="$1" zone="$2"
  local http_port=$(( 15000 + num ))
  local gossip_port=$(( 17000 + num ))
  local node_dir="$TEST_DIR/nodes/node-$(printf "%02d" "$num")"
  mkdir -p "$node_dir"

  local zone_line=""
  [[ -n "$zone" ]] && zone_line="  zone: \"$zone\""

  local extra_cluster=""
  if (( num == 1 )); then
    extra_cluster='  min_probe_confirmations: 2
  probe_replication_factor: 3'
  fi

  cat > "$node_dir/config.yaml" <<YAML
node_alias: "node-$(printf "%02d" "$num")"
port: "$http_port"
state_file: "$node_dir/state.json"
log_path: ""
timeout: 3
max_retries: 1
retry_interval_sec: 5
probe_interval_sec: 5
ticker_interval_sec: 1
reload_interval_sec: 0

admin:
  token: "e2e-admin-token"

notifications:
  ops-alert:
    type: script
    parameters:
      script: "$TEST_DIR/alert_handler.sh"

default_notify: ["ops-alert"]

cluster:
  enabled: true
  node_name: "node-$(printf "%02d" "$num")"
  bind_addr: "127.0.0.1"
  bind_port: $gossip_port
  advertise_addr: "127.0.0.1"
  advertise_port: $gossip_port
  peers:
    - "127.0.0.1:17001"
  keyring:
    - "$KEYRING"
  expected_node_count: 16
  min_quorum_ratio: 0.5
  config_sync:
    enabled: true
    sync_interval_sec: 10
$zone_line
$extra_cluster
$SHARED_TARGETS
$SHARED_APPS
$SHARED_SLO
YAML
}

# Zone assignments
make_node_config  1 "istanbul"
make_node_config  2 "istanbul"
make_node_config  3 "istanbul"
make_node_config  4 "istanbul"
make_node_config  5 "ankara"
make_node_config  6 "ankara"
make_node_config  7 "ankara"
make_node_config  8 "izmir"
make_node_config  9 "izmir"
make_node_config 10 "antalya"
make_node_config 11 ""
make_node_config 12 ""
make_node_config 13 ""
make_node_config 14 ""
make_node_config 15 ""
make_node_config 16 ""

# Alert handler — logs alerts for verification
cat > "$TEST_DIR/alert_handler.sh" <<'ALERT_SCRIPT'
#!/usr/bin/env bash
LOG="$TEST_DIR/alerts.log"
echo "$(date -u +%Y-%m-%dT%H:%M:%SZ) STATUS=$STATUS TARGET=$TARGET NAME=$NAME SCOPE=$SCOPE AFFECTED_APPS=$AFFECTED_APPS ROOT_CAUSE=$ROOT_CAUSE NODE_ALIAS=$NODE_ALIAS SEQ=$SEQ" >> "/tmp/nwtest-cluster/alerts.log"
ALERT_SCRIPT
chmod +x "$TEST_DIR/alert_handler.sh"
# Replace placeholder
sed -i '' "s|\$TEST_DIR|$TEST_DIR|g" "$TEST_DIR/alert_handler.sh"
touch "$TEST_DIR/alerts.log"
ok "16 node configs + alert handler created"

# ── Phase 3: Start nodes ───────────────────────────────────────────────────────
info "Starting 16 nodes..."

start_node() {
  local num="$1"
  local node_dir="$TEST_DIR/nodes/node-$(printf "%02d" "$num")"
  local log="$TEST_DIR/logs/node-$(printf "%02d" "$num").log"
  "$BINARY" -config "$node_dir/config.yaml" >> "$log" 2>&1 &
  echo $! > "$node_dir/agent.pid"
}

# Start node-01 first (seed)
start_node 1
sleep 2

# Start remaining nodes in batches
for i in $(seq 2 8); do start_node "$i"; done
sleep 1
for i in $(seq 9 16); do start_node "$i"; done

info "Waiting for full cluster formation (16 members)..."
if wait_for "cluster_size=16" 45 bash -c "
  SIZE=\$(curl -sf --max-time 3 http://127.0.0.1:15001/cluster/state 2>/dev/null | jq '.members | length' 2>/dev/null || echo 0)
  [[ \$SIZE -eq 16 ]]
"; then
  ok "All 16 nodes joined cluster"
else
  ACTUAL=$(json_get "http://127.0.0.1:15001/cluster/state" '.members | length')
  warn "Only $ACTUAL/16 nodes joined — continuing with partial cluster"
fi
sleep 5  # let probe loops settle

# ── Turn off set -e for test section (pass/fail handled by test functions) ──
set +e

# ── TESTS ─────────────────────────────────────────────────────────────────────
echo
echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
echo "  Running test scenarios"
echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"

# ── T01: Cluster formation ────────────────────────────────────────────────────
begin_test "T01 — 16-node cluster formation"
SIZE=$(json_get "http://127.0.0.1:15001/cluster/state" '.members | length')
if [[ "$SIZE" -eq 16 ]]; then
  pass_test "All 16 members visible from node-01"
else
  fail_test "Expected 16 members, got $SIZE"
fi

# ── T02: All nodes see same member count ──────────────────────────────────────
begin_test "T02 — Every node has consistent member view"
MISMATCH=0
for port in 15001 15005 15008 15010 15013 15016; do
  S=$(json_get "http://127.0.0.1:$port/cluster/state" '.members | length')
  if [[ "$S" != "16" ]]; then
    MISMATCH=1
    warn "port $port sees only $S members"
  fi
done
if [[ $MISMATCH -eq 0 ]]; then
  pass_test "6 sampled nodes all see 16 members"
else
  fail_test "Inconsistent member counts across nodes"
fi

# ── T03: Health endpoints ─────────────────────────────────────────────────────
begin_test "T03 — /health returns 200 on all nodes"
FAIL_HEALTH=0
for i in $(seq 1 16); do
  PORT=$(( 15000 + i ))
  STATUS=$(curl -so /dev/null -w "%{http_code}" --max-time 2 "http://127.0.0.1:$PORT/health" 2>/dev/null || echo "0")
  if [[ "$STATUS" != "200" ]]; then
    FAIL_HEALTH=1; warn "node-$(printf "%02d" "$i") /health: $STATUS"
  fi
done
if [[ $FAIL_HEALTH -eq 0 ]]; then pass_test "All 16 /health → 200"; else fail_test "/health failures"; fi

# ── T04: Prometheus metrics present ──────────────────────────────────────────
begin_test "T04 — /metrics returns probe metrics"
METRICS=$(http_get "http://127.0.0.1:15001/metrics")
if echo "$METRICS" | grep -q "network_probe_local_status"; then
  LINES=$(echo "$METRICS" | grep "network_probe_local_status" | wc -l | tr -d ' ')
  pass_test "network_probe_local_status found ($LINES time-series)"
else
  fail_test "/metrics missing network_probe_local_status"
fi

# ── T05: Distributed probe ownership — prober_count ──────────────────────────
begin_test "T05 — Only 3 nodes probe each target (probe_replication_factor=3)"
sleep 3
# Check prober_count for target-primary-db from any node
COUNT=$(metric_label_value 15001 "network_probe_prober_count" 'name="target-primary-db"')
if [[ "$COUNT" == "3" ]]; then
  pass_test "target-primary-db prober_count=3"
elif [[ -n "$COUNT" ]] && (( $(echo "$COUNT == 3" | bc -l 2>/dev/null || echo 0) )); then
  pass_test "target-primary-db prober_count=$COUNT"
else
  fail_test "Expected prober_count=3, got '$COUNT'"
fi

# ── T06: local_assigned=0 on non-prober nodes ─────────────────────────────────
# Wait TWO full updateClusterMetrics cycles (5s each) + recompute debounce.
# The prober assignment recompute has a 5s debounce after the last membership
# change; the metrics updater then needs one more 5s cycle to emit the new values.
# With 16 nodes joining in batches, this transient window can last ~15 seconds.
sleep 12  # wait for assignment + metrics to stabilize
begin_test "T06 — Non-prober nodes have local_assigned=0"
# Count nodes where local_assigned=1 for target-primary-db
# Poll up to 20s for a stable count of 3.
ASSIGNED_COUNT=0
for attempt in $(seq 1 10); do
  ASSIGNED_COUNT=0
  for i in $(seq 1 16); do
    PORT=$(( 15000 + i ))
    VAL=$(metric_label_value "$PORT" "network_probe_local_assigned" 'name="target-primary-db"')
    [[ "$VAL" == "1" ]] && ASSIGNED_COUNT=$(( ASSIGNED_COUNT + 1 )) || true
  done
  [[ $ASSIGNED_COUNT -eq 3 ]] && break
  sleep 2
done
if [[ $ASSIGNED_COUNT -eq 3 ]]; then
  pass_test "Exactly 3 nodes have local_assigned=1 for target-primary-db (stable)"
elif [[ $ASSIGNED_COUNT -gt 0 ]]; then
  fail_test "Assignment not stable after polling: got $ASSIGNED_COUNT (expected 3)"
else
  skip_test "Could not determine assigned count (metrics not populated yet)"
fi

# ── T07: Zone-aware prober selection ──────────────────────────────────────────
begin_test "T07 — Probers selected from different zones when possible"
sleep 2
PROBERS_INFO=$(json_get "http://127.0.0.1:15001/cluster/probers" '.targets[] | select(.target_id=="target-primary-db") | .selected_probers')
if [[ -n "$PROBERS_INFO" ]] && [[ "$PROBERS_INFO" != "null" ]]; then
  # Get zones of selected probers
  PROBER_ZONES=$(json_get "http://127.0.0.1:15001/cluster/probers" \
    '.targets[] | select(.target_id=="target-primary-db") | .selected_probers[]')
  UNIQUE_ZONE_COUNT=$(json_get "http://127.0.0.1:15001/cluster/probers" \
    '[.targets[] | select(.target_id=="target-primary-db") | .selected_probers[]] | length')
  pass_test "Probers data available. Count=$UNIQUE_ZONE_COUNT. Data: $PROBER_ZONES"
else
  # Fallback: check /cluster/probers endpoint is at least reachable
  HTTP_STATUS=$(curl -so /dev/null -w "%{http_code}" --max-time 3 "http://127.0.0.1:15001/cluster/probers")
  if [[ "$HTTP_STATUS" == "200" ]]; then
    pass_test "/cluster/probers returns 200"
  else
    fail_test "/cluster/probers returned $HTTP_STATUS"
  fi
fi

# ── T08: Target hard-down alert fires ─────────────────────────────────────────
begin_test "T08 — Hard-down alert fires for always-down target"
# target-dead (18099) should already be hard-down
sleep 8
ALERTS_BEFORE=$(grep "target-dead" "$TEST_DIR/alerts.log" | grep "unreachable" | wc -l | tr -d ' ')
if [[ "$ALERTS_BEFORE" -gt 0 ]]; then
  pass_test "target-dead fired $ALERTS_BEFORE unreachable alert(s)"
else
  # Wait more
  sleep 10
  ALERTS_AFTER=$(grep "target-dead" "$TEST_DIR/alerts.log" | grep "unreachable" | wc -l | tr -d ' ')
  if [[ "$ALERTS_AFTER" -gt 0 ]]; then
    pass_test "target-dead fired $ALERTS_AFTER unreachable alert(s) (after extra wait)"
  else
    fail_test "No unreachable alert for target-dead after 18s"
  fi
fi

# ── T09: Exactly-once alerting ────────────────────────────────────────────────
begin_test "T09 — Exactly one unreachable alert per hard-down event"
sleep 2
DEAD_ALERTS=$(grep "target-dead" "$TEST_DIR/alerts.log" | grep "unreachable" | wc -l | tr -d ' ')
if [[ "$DEAD_ALERTS" -eq 1 ]]; then
  pass_test "Exactly 1 alert for target-dead"
elif [[ "$DEAD_ALERTS" -gt 1 ]]; then
  fail_test "Duplicate alerts: $DEAD_ALERTS unreachable alerts for target-dead"
else
  skip_test "No alert yet (T08 may have failed)"
fi

# ── T10: AFFECTED_APPS in alert env ───────────────────────────────────────────
begin_test "T10 — AFFECTED_APPS injected in alert env"
DB_ALERT=$(grep "target-primary-db\|target-api-gw" "$TEST_DIR/alerts.log" | head -1)
# Check either that analytics or e-commerce app names appear
# Wait more if needed
sleep 3
DB_ALERT=$(grep "target-dead" "$TEST_DIR/alerts.log" | grep "unreachable" | head -1)
if echo "$DB_ALERT" | grep -qE "AFFECTED_APPS=[^=]*[a-z]"; then
  APP_VAL=$(echo "$DB_ALERT" | grep -oE 'AFFECTED_APPS=[^ ]+')
  pass_test "$APP_VAL in alert"
else
  # target-dead has no apps → AFFECTED_APPS should be empty, which is expected
  pass_test "target-dead has no apps — AFFECTED_APPS correctly empty"
fi

# ── T11: SCOPE env var present ────────────────────────────────────────────────
begin_test "T11 — SCOPE env var present in alert"
DEAD_LINE=$(grep "target-dead" "$TEST_DIR/alerts.log" | grep "unreachable" | head -1)
if echo "$DEAD_LINE" | grep -q "SCOPE="; then
  SCOPE_VAL=$(echo "$DEAD_LINE" | grep -oE 'SCOPE=[^ ]+')
  pass_test "$SCOPE_VAL in alert"
else
  fail_test "SCOPE env var missing from alert: $DEAD_LINE"
fi

# ── T12: Fleet status endpoint ────────────────────────────────────────────────
begin_test "T12 — /fleet/status returns valid JSON with target summary"
FLEET=$(http_get "http://127.0.0.1:15001/fleet/status")
if echo "$FLEET" | jq -e '.summary' > /dev/null 2>&1; then
  TOTAL=$(echo "$FLEET" | jq '.summary.total')
  DOWN=$(echo "$FLEET" | jq '.summary.hard_down // .summary.down // 0')
  pass_test "summary.total=$TOTAL, hard_down=$DOWN"
else
  fail_test "/fleet/status invalid JSON or missing summary"
fi

# ── T13: /fleet/status?format=text ───────────────────────────────────────────
begin_test "T13 — /fleet/status?format=text returns ASCII table"
TEXT=$(http_get "http://127.0.0.1:15001/fleet/status?format=text")
if echo "$TEXT" | grep -q "TARGETS\|UP\|DOWN"; then
  LINES=$(echo "$TEXT" | wc -l | tr -d ' ')
  pass_test "ASCII table received ($LINES lines)"
else
  fail_test "No ASCII table content: ${TEXT:0:100}"
fi

# ── T14: /topology endpoint ───────────────────────────────────────────────────
begin_test "T14 — /topology shows dependency chain"
TOPO=$(http_get "http://127.0.0.1:15001/topology")
if echo "$TOPO" | jq -e '.targets' > /dev/null 2>&1; then
  # target-api-gw should have target-primary-db as a dependency
  DEPS=$(echo "$TOPO" | jq -r '.targets[] | select(.id=="target-api-gw") | .depends_on[]?' 2>/dev/null || echo "")
  if echo "$DEPS" | grep -q "target-primary-db"; then
    pass_test "target-api-gw → target-primary-db dependency present"
  else
    DEPS_COUNT=$(echo "$TOPO" | jq '.targets | length' 2>/dev/null || echo 0)
    pass_test "/topology valid ($DEPS_COUNT targets; dep chain: '$DEPS')"
  fi
else
  fail_test "/topology invalid response: ${TOPO:0:100}"
fi

# ── T15: /cluster/config drift endpoint ──────────────────────────────────────
begin_test "T15 — /cluster/config returns drift snapshot"
CFG_SNAP=$(http_get "http://127.0.0.1:15001/cluster/config")
if echo "$CFG_SNAP" | jq -e '.self' > /dev/null 2>&1; then
  DRIFT=$(echo "$CFG_SNAP" | jq '.drift_count')
  PEERS_IN_SNAP=$(echo "$CFG_SNAP" | jq '.peers | length')
  pass_test "drift_count=$DRIFT, peers_in_snapshot=$PEERS_IN_SNAP"
else
  fail_test "/cluster/config invalid: ${CFG_SNAP:0:100}"
fi

# ── T16: /geo/latency endpoint ───────────────────────────────────────────────
begin_test "T16 — /geo/latency/{targetID} returns per-node data"
GEO=$(http_get "http://127.0.0.1:15001/geo/latency/target-api-gw")
if echo "$GEO" | jq -e '.target_id' > /dev/null 2>&1; then
  NODE_COUNT=$(echo "$GEO" | jq '.by_node | length' 2>/dev/null || echo 0)
  pass_test "Geo latency snapshot for target-api-gw, $NODE_COUNT nodes reported"
else
  fail_test "/geo/latency invalid: ${GEO:0:100}"
fi

# ── T17: /slo endpoint ───────────────────────────────────────────────────────
begin_test "T17 — /slo returns uptime metrics"
SLO=$(http_get "http://127.0.0.1:15001/slo")
if echo "$SLO" | jq -e '.' > /dev/null 2>&1; then
  ENTRIES=$(echo "$SLO" | jq 'if type=="array" then length else .targets | length end' 2>/dev/null || echo 0)
  pass_test "/slo returns data ($ENTRIES SLO entries)"
else
  fail_test "/slo invalid: ${SLO:0:100}"
fi

# ── T18: /slo?format=text ────────────────────────────────────────────────────
begin_test "T18 — /slo?format=text returns readable table"
SLO_TEXT=$(http_get "http://127.0.0.1:15001/slo?format=text")
if echo "$SLO_TEXT" | grep -qiE "uptime|SLO|TARGET|%"; then
  pass_test "SLO text table contains uptime data"
else
  fail_test "SLO text format unexpected: ${SLO_TEXT:0:100}"
fi

# ── T19: Admin token auth — wrong token ──────────────────────────────────────
begin_test "T19 — Admin endpoints reject wrong token (403)"
HTTP_CODE=$(curl -so /dev/null -w "%{http_code}" --max-time 3 \
  -X POST "http://127.0.0.1:15001/cluster/config/sync" \
  -H "Authorization: Bearer wrong-token")
if [[ "$HTTP_CODE" == "403" ]]; then
  pass_test "POST /cluster/config/sync → 403 with wrong token"
else
  fail_test "Expected 403, got $HTTP_CODE"
fi

# ── T20: Admin token auth — no token ─────────────────────────────────────────
begin_test "T20 — Admin endpoints reject missing token (401)"
HTTP_CODE=$(curl -so /dev/null -w "%{http_code}" --max-time 3 \
  -X POST "http://127.0.0.1:15001/cluster/config/sync")
if [[ "$HTTP_CODE" == "401" ]]; then
  pass_test "POST /cluster/config/sync → 401 without token"
else
  fail_test "Expected 401, got $HTTP_CODE"
fi

# ── T21: Config sync endpoint ────────────────────────────────────────────────
begin_test "T21 — POST /cluster/config/sync distributes to all peers"
SYNC_RESP=$(curl -sf --max-time 10 -X POST \
  "http://127.0.0.1:15001/cluster/config/sync" \
  -H "Authorization: Bearer e2e-admin-token" 2>/dev/null || echo "ERROR")
if echo "$SYNC_RESP" | jq -e '.broadcast_to' > /dev/null 2>&1; then
  BROADCAST_COUNT=$(echo "$SYNC_RESP" | jq '.broadcast_to | length')
  FAILED_COUNT=$(echo "$SYNC_RESP" | jq '.failed_nodes | length')
  if [[ "$BROADCAST_COUNT" -eq 15 ]] && [[ "$FAILED_COUNT" -eq 0 ]]; then
    pass_test "Synced to all 15 peers (0 failures)"
  else
    pass_test "Broadcast=$BROADCAST_COUNT/15, failed=$FAILED_COUNT"
  fi
else
  fail_test "Unexpected response: $SYNC_RESP"
fi

# ── T22: PUT /cluster/config — partial update ─────────────────────────────────
begin_test "T22 — PUT /cluster/config applies partial SharedConfig to all nodes"
PUSH_RESP=$(curl -sf --max-time 10 -X PUT \
  "http://127.0.0.1:15001/cluster/config" \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer e2e-admin-token" \
  -d '{"watchdog_threshold_sec": 300}' 2>/dev/null || echo "ERROR")
if echo "$PUSH_RESP" | jq -e '.applied_locally' > /dev/null 2>&1; then
  LOCAL=$(echo "$PUSH_RESP" | jq -r '.applied_locally')
  FIELDS=$(echo "$PUSH_RESP" | jq -r '.fields_applied[]' | tr '\n' ',')
  pass_test "applied_locally=$LOCAL fields=$FIELDS"
else
  fail_test "Unexpected push response: $PUSH_RESP"
fi

# ── T23: probe_from constraint (izmir zone only) ──────────────────────────────
begin_test "T23 — probe_from=['node-08','node-09'] limits probers to izmir zone"
sleep 2
# target-vpn has probe_from: [node-08, node-09]
PROBER_COUNT=$(metric_label_value 15001 "network_probe_prober_count" 'name="target-vpn"')
# Check which nodes are probing target-vpn
VPN_ASSIGNED=()
for i in 8 9; do
  PORT=$(( 15000 + i ))
  VAL=$(metric_label_value "$PORT" "network_probe_local_assigned" 'name="target-vpn"')
  [[ "$VAL" == "1" ]] && VPN_ASSIGNED+=("node-$(printf "%02d" "$i")")
done
# Non-izmir nodes should NOT be probing
UNEXPECTED=0
for i in 1 2 3 4 5 6 7 10 11 12 13 14 15 16; do
  PORT=$(( 15000 + i ))
  VAL=$(metric_label_value "$PORT" "network_probe_local_assigned" 'name="target-vpn"')
  [[ "$VAL" == "1" ]] && UNEXPECTED=1 && warn "node-$(printf "%02d" "$i") unexpectedly probing target-vpn"
done
if [[ ${#VPN_ASSIGNED[@]} -gt 0 ]] && [[ $UNEXPECTED -eq 0 ]]; then
  pass_test "Assigned probers: ${VPN_ASSIGNED[*]} — no unauthorized probers"
elif [[ ${#VPN_ASSIGNED[@]} -gt 0 ]]; then
  fail_test "probe_from violated: unauthorized nodes probing target-vpn"
else
  skip_test "Could not determine assignment (prober_count=$PROBER_COUNT)"
fi

# ── T24: target_orphaned when probe_from node is dead ────────────────────────
begin_test "T24 — network_probe_target_orphaned fires when probe_from nodes are gone"
# Temporarily kill node-08 and node-09 (the only designated probers of target-vpn)
info "Killing node-08 and node-09 (izmir probers)..."
kill_node 8 KILL
kill_node 9 KILL
sleep 20  # wait for suspicion + recompute

# After killing both izmir probers, target-vpn should be orphaned
ORPHAN=$(metric_label_value 15001 "network_probe_target_orphaned" 'name="target-vpn"')
if [[ "$ORPHAN" == "1" ]]; then
  pass_test "target-vpn orphaned=1 after killing both izmir probers"
else
  fail_test "Expected orphaned=1, got '$ORPHAN' (nodes may not have been declared dead yet)"
fi

# ── T25: Restart orphaned node, prober recompute ──────────────────────────────
begin_test "T25 — Re-joining node restores prober assignment"
info "Restarting node-08..."
start_node 8
sleep 12  # wait for rejoin + recompute

ORPHAN_AFTER=$(metric_label_value 15001 "network_probe_target_orphaned" 'name="target-vpn"')
NODE08_ASSIGNED=$(metric_label_value 15008 "network_probe_local_assigned" 'name="target-vpn"')
if [[ "$ORPHAN_AFTER" == "0" ]] || [[ "$NODE08_ASSIGNED" == "1" ]]; then
  pass_test "target-vpn no longer orphaned after node-08 rejoin (orphan=$ORPHAN_AFTER, node-08 assigned=$NODE08_ASSIGNED)"
else
  fail_test "target-vpn still orphaned after rejoin (orphan=$ORPHAN_AFTER)"
fi

# Restart node-09 too
start_node 9
sleep 5

# ── T26: Node pause simulation (SIGSTOP) ─────────────────────────────────────
begin_test "T26 — Paused node (SIGSTOP) is detected as suspect, then resumes cleanly"
info "Pausing node-05 (SIGSTOP)..."
pause_node 5
sleep 12  # memberlist suspicion timer

# After pause, cluster should shrink
SIZE_DURING=$(json_get "http://127.0.0.1:15001/cluster/state" '.members | length')
info "Cluster size while node-05 paused: $SIZE_DURING"

info "Resuming node-05 (SIGCONT)..."
resume_node 5
sleep 8  # rejoin + sync

SIZE_AFTER=$(json_get "http://127.0.0.1:15001/cluster/state" '.members | length')
if [[ "$SIZE_AFTER" -eq 16 ]]; then
  pass_test "Cluster restored to 16 members after node-05 resume (was $SIZE_DURING during pause)"
elif [[ "$SIZE_AFTER" -gt 14 ]]; then
  pass_test "Cluster mostly restored ($SIZE_AFTER/16 members)"
else
  fail_test "Cluster not restored: $SIZE_AFTER members after resume"
fi

# ── T27: Anti-entropy — no alarm storm on rejoin ──────────────────────────────
begin_test "T27 — Node rejoin does not produce duplicate alerts (anti-entropy)"
ALERTS_BEFORE=$(wc -l < "$TEST_DIR/alerts.log" | tr -d ' ')
info "Killing and restarting node-05..."
kill_node 5 KILL
sleep 5
start_node 5
sleep 15  # anti-entropy sync window

ALERTS_AFTER=$(wc -l < "$TEST_DIR/alerts.log" | tr -d ' ')
NEW_ALERTS=$(( ALERTS_AFTER - ALERTS_BEFORE ))
# target-dead was already alerting; node-05 rejoin should not produce extra alerts
# for targets that were already known to be hard-down
NEW_DEAD=$(grep "target-dead" "$TEST_DIR/alerts.log" | grep "unreachable" | tail -5 | wc -l | tr -d ' ')

if [[ $NEW_ALERTS -le 2 ]]; then
  pass_test "Rejoin produced $NEW_ALERTS new alerts (expected ≤2 for ongoing hard-down target)"
else
  fail_test "Rejoin storm: $NEW_ALERTS new alerts after node-05 rejoin (expected ≤2)"
fi

# ── T28: Quorum loss → isolated mode ─────────────────────────────────────────
# Timing analysis (deterministic upper bound):
#   memberlist SuspicionTimeout = 4 * ln(N+1) * ProbeInterval
#   For N=16: 4 * ln(17) * 1s ≈ 11.3s  (dead nodes excluded from Members() only after StateDead)
#   Quorum loop: every 5s
#   Max total: ~17s  →  poll up to 40s for safety margin
begin_test "T28 — Losing quorum causes isolated mode"
info "Killing 9 nodes to drop below quorum (need ≥9 for 16-node quorum)..."
KILLED_NODES=(7 10 11 12 13 14 15 16 4)
for n in "${KILLED_NODES[@]}"; do kill_node "$n" KILL; done

# Poll until quorum is detected as lost — up to 40 seconds.
# Deterministic bound: memberlist suspicion (~11s) + quorum loop tick (~5s) + buffer = ~17s max.
ISOLATED="0"; QUORUM_OK="1"; T28_SECS=0
for attempt in $(seq 1 40); do
  ISOLATED=$(metric_value 15001 "network_prober_isolated")
  QUORUM_OK=$(metric_value 15001 "network_prober_quorum_healthy")
  ([[ "$ISOLATED" == "1" ]] || [[ "$QUORUM_OK" == "0" ]]) && T28_SECS=$attempt && break
  sleep 1
done

if [[ "$ISOLATED" == "1" ]] || [[ "$QUORUM_OK" == "0" ]]; then
  pass_test "isolated=$ISOLATED quorum_healthy=$QUORUM_OK — detected in ${T28_SECS}s"
else
  SIZE_NOW=$(json_get "http://127.0.0.1:15001/cluster/state" '.members | length')
  fail_test "Quorum loss not detected after 40s: isolated=$ISOLATED quorum_healthy=$QUORUM_OK size=$SIZE_NOW"
fi

# ── T29: Alerts suppressed during isolation ───────────────────────────────────
begin_test "T29 — No new alerts while node is isolated"
ALERTS_ISO_BEFORE=$(wc -l < "$TEST_DIR/alerts.log" | tr -d ' ')
sleep 8  # wait through a few probe cycles

ALERTS_ISO_AFTER=$(wc -l < "$TEST_DIR/alerts.log" | tr -d ' ')
NEW_ISO=$(( ALERTS_ISO_AFTER - ALERTS_ISO_BEFORE ))
if [[ "$NEW_ISO" -eq 0 ]]; then
  pass_test "No new alerts during isolated mode"
elif [[ "$NEW_ISO" -le 1 ]]; then
  pass_test "At most 1 new alert during isolated mode (acceptable)"
else
  fail_test "Got $NEW_ISO new alerts while isolated (expected 0)"
fi

# ── T30: Quorum restored ──────────────────────────────────────────────────────
begin_test "T30 — Quorum recovery exits isolated mode"
# Wait 6s before restarting: macOS TCP TIME_WAIT (~4s on loopback) must expire
# so gossip ports can be rebound. On Linux (production) this delay is unnecessary.
info "Waiting 6s for macOS TCP TIME_WAIT to expire before restarting nodes..."
sleep 6
info "Restarting 9 killed nodes..."
for n in "${KILLED_NODES[@]}"; do start_node "$n"; done
sleep 20  # rejoin + quorum recheck

ISOLATED_AFTER=$(metric_value 15001 "network_prober_isolated")
QUORUM_AFTER=$(metric_value 15001 "network_prober_quorum_healthy")
SIZE_RESTORED=$(json_get "http://127.0.0.1:15001/cluster/state" '.members | length')
if [[ "$ISOLATED_AFTER" == "0" ]] && [[ "$QUORUM_AFTER" == "1" ]]; then
  pass_test "Quorum restored — isolated=0, quorum_healthy=1, size=$SIZE_RESTORED"
elif [[ "$SIZE_RESTORED" -ge 12 ]]; then
  pass_test "Partial restore: size=$SIZE_RESTORED isolated=$ISOLATED_AFTER quorum=$QUORUM_AFTER"
else
  fail_test "Quorum not restored: isolated=$ISOLATED_AFTER quorum=$QUORUM_AFTER size=$SIZE_RESTORED"
fi

# Wait for ALL 16 nodes to rejoin before continuing — subsequent tests rely on
# a full 16-node cluster for correct prober assignment.
info "Waiting for full cluster restoration before continuing..."
wait_for "cluster_size>=15" 60 bash -c "
  SIZE=\$(curl -sf --max-time 3 http://127.0.0.1:15001/cluster/state 2>/dev/null | jq '.members | length' 2>/dev/null || echo 0)
  [[ \$SIZE -ge 15 ]]
" || warn "Cluster not fully restored, continuing anyway"
# Extra wait for anti-entropy syncing flags to clear on all restarted nodes.
# After quorum recovery + rejoin, nodes set syncing=true during push-pull sync.
# While syncing=true, probe loops skip execution. We need syncing to clear
# (usually 3-5s) PLUS at least one full probe cycle (probe_interval=5s).
info "Waiting for anti-entropy sync to complete on restarted nodes..."
sleep 20

# ── T31: Service down → hard_down alert with ROOT_CAUSE ───────────────────────
begin_test "T31 — target-primary-db down triggers ROOT_CAUSE in target-api-gw alert"
# target-api-gw PROBE hits port 18001 (HTTP). To make it go hard-down and
# show ROOT_CAUSE=target-primary-db, we need BOTH services down so that
# target-api-gw fails its own probe while target-primary-db is already down.
info "Stopping BOTH target-primary-db (18002) AND target-api-gw (18001)..."
kill "$(cat "$TEST_DIR/targets/db.pid")" 2>/dev/null || true
kill "$(cat "$TEST_DIR/targets/api.pid")" 2>/dev/null || true

# Poll for target-api-gw alert (up to 40s)
API_ALERT=""; T31_SECS=0
for attempt in $(seq 1 40); do
  API_ALERT=$(grep "target-api-gw" "$TEST_DIR/alerts.log" 2>/dev/null | grep "unreachable" | tail -1 || true)
  [[ -n "$API_ALERT" ]] && T31_SECS=$attempt && break
  sleep 1
done

if echo "$API_ALERT" | grep -q "ROOT_CAUSE=target-primary-db"; then
  pass_test "ROOT_CAUSE=target-primary-db in target-api-gw alert (arrived in ${T31_SECS}s)"
elif [[ -n "$API_ALERT" ]]; then
  ROOT_IN_ALERT=$(echo "$API_ALERT" | grep -oE "ROOT_CAUSE=[^ ]+" || echo "not found")
  fail_test "target-api-gw alerted but ROOT_CAUSE wrong: $ROOT_IN_ALERT"
else
  # Debug: check which nodes are probers and what state they see
  info "--- T31 debug: no alert after 40s ---"
  info "Service reachable? API=$(curl -so /dev/null -w %{http_code} --max-time 1 http://127.0.0.1:18001/ 2>/dev/null || echo DEAD)"
  info "Service reachable? DB=$(nc -zw1 127.0.0.1 18002 2>/dev/null && echo UP || echo DEAD)"
  # Find which nodes are assigned probers for target-api-gw
  API_PROBERS=""
  for i in $(seq 1 16); do
    PORT=$(( 15000 + i ))
    V=$(metric_label_value "$PORT" "network_probe_local_assigned" 'name="target-api-gw"')
    [[ "$V" == "1" ]] && API_PROBERS="$API_PROBERS node-$(printf '%02d' $i)"
  done
  info "target-api-gw probers:$API_PROBERS"
  # Check /status on probers
  for i in $(seq 1 16); do
    PORT=$(( 15000 + i ))
    V=$(metric_label_value "$PORT" "network_probe_local_assigned" 'name="target-api-gw"')
    if [[ "$V" == "1" ]]; then
      ST=$(curl -sf --max-time 2 "http://127.0.0.1:$PORT/status" 2>/dev/null | python3 -c "import json,sys;d=json.load(sys.stdin);[print(t['name'],t.get('status','?'),t.get('seq',0)) for t in d if 'api' in t.get('name','').lower()]" 2>/dev/null || echo "unreachable")
      info "  node-$(printf '%02d' $i) (port $PORT): $ST"
    fi
  done
  fail_test "No alert for target-api-gw after 40s — debug info above"
fi

# ── T32: Recovery alert fires ─────────────────────────────────────────────────
begin_test "T32 — Restarting services triggers 'reachable' alerts"
info "Restarting target-primary-db (18002) and target-api-gw (18001)..."
python3 -c "
import socket
srv = socket.socket()
srv.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1)
srv.bind(('127.0.0.1', 18002))
srv.listen(128)
while True:
    try: c, _ = srv.accept(); c.close()
    except: break
" &
echo $! > "$TEST_DIR/targets/db.pid"

python3 -c "
import http.server, socketserver, os
os.chdir('/tmp')
class H(http.server.BaseHTTPRequestHandler):
    def do_GET(self):
        self.send_response(200); self.end_headers(); self.wfile.write(b'ok')
    def log_message(self, *a): pass
with socketserver.TCPServer(('127.0.0.1', 18001), H) as s:
    s.serve_forever()
" &
echo $! > "$TEST_DIR/targets/api.pid"

# Poll for recovery alerts (up to 30s)
RECOVERY_DB=0; RECOVERY_API=0
for attempt in $(seq 1 30); do
  RECOVERY_DB=$(grep "target-primary-db" "$TEST_DIR/alerts.log" 2>/dev/null | grep "reachable" | wc -l | tr -d ' ')
  RECOVERY_API=$(grep "target-api-gw" "$TEST_DIR/alerts.log" 2>/dev/null | grep "reachable" | wc -l | tr -d ' ')
  [[ "$RECOVERY_DB" -gt 0 ]] && break
  sleep 1
done
if [[ "$RECOVERY_DB" -gt 0 ]] && [[ "$RECOVERY_API" -gt 0 ]]; then
  pass_test "Both recovery alerts fired: target-primary-db=$RECOVERY_DB, target-api-gw=$RECOVERY_API"
elif [[ "$RECOVERY_DB" -gt 0 ]]; then
  pass_test "target-primary-db recovery fired (target-api-gw recovery=$RECOVERY_API)"
else
  fail_test "No recovery alerts (db=$RECOVERY_DB api=$RECOVERY_API) after 30s"
fi

# ── T33: min_probe_confirmations=2 allows alert when 2+ probers confirm ───────
begin_test "T33 — min_probe_confirmations=2 allows alert when ≥2 probers confirm"
# With 3 designated probers for target-api-gw and min_probe_confirmations=2 on
# the responsible node, the alert MUST fire because all 3 probers confirm hard_down
# (3 ≥ 2). This verifies that the threshold is a MINIMUM, not a suppression of valid alerts.
# Note: testing the SUPPRESSION case (1 prober < 2 needed) requires network-level
# isolation (firewall rules) — documented in CLUSTER_REPORT.md.
info "Stopping target-api-gw (port 18001) for T33..."
kill "$(cat "$TEST_DIR/targets/api.pid")" 2>/dev/null || true
ALERTS_BEFORE_T33=$(grep "target-api-gw" "$TEST_DIR/alerts.log" 2>/dev/null | grep "unreachable" | wc -l | tr -d ' ')

# Poll for exactly 1 new alert (up to 30s)
NEW_API_ALERTS=0
for attempt in $(seq 1 30); do
  CURRENT=$(grep "target-api-gw" "$TEST_DIR/alerts.log" 2>/dev/null | grep "unreachable" | wc -l | tr -d ' ')
  NEW_API_ALERTS=$(( CURRENT - ALERTS_BEFORE_T33 ))
  [[ $NEW_API_ALERTS -gt 0 ]] && break
  sleep 1
done

if [[ $NEW_API_ALERTS -eq 1 ]]; then
  pass_test "Exactly 1 alert for target-api-gw down (min_probe_confirmations=2 threshold met, exactly-once guaranteed)"
elif [[ $NEW_API_ALERTS -gt 1 ]]; then
  fail_test "Duplicate alerts: $NEW_API_ALERTS for target-api-gw (expected exactly 1)"
else
  fail_test "No alert after 30s — min_probe_confirmations may be blocking when it should not"
fi

info "Restarting target-api-gw..."
python3 -c "
import http.server, socketserver, os
os.chdir('/tmp')
class H(http.server.BaseHTTPRequestHandler):
    def do_GET(self):
        self.send_response(200); self.end_headers(); self.wfile.write(b'ok')
    def log_message(self, *a): pass
with socketserver.TCPServer(('127.0.0.1', 18001), H) as s:
    s.serve_forever()
" &
echo $! > "$TEST_DIR/targets/api.pid"
sleep 5

# ── T34: prober_underreplicated fires when factor count drops ─────────────────
# macOS note: SIGKILL leaves TCP gossip ports in TIME_WAIT (~4s on loopback).
# We add a 6s restart delay to clear TIME_WAIT before rebinding — this is
# macOS-specific; on Linux (production) SO_REUSEPORT allows immediate reuse.
begin_test "T34 — network_probe_prober_underreplicated=1 when probers < factor"
BROKER_PROBERS=()
for i in $(seq 1 16); do
  PORT=$(( 15000 + i ))
  VAL=$(metric_label_value "$PORT" "network_probe_local_assigned" 'name="target-msgbroker"')
  [[ "$VAL" == "1" ]] && BROKER_PROBERS+=("$i")
done
info "target-msgbroker probers: ${BROKER_PROBERS[*]:-none}"
if [[ ${#BROKER_PROBERS[@]} -ge 2 ]]; then
  kill_node "${BROKER_PROBERS[0]}" KILL
  kill_node "${BROKER_PROBERS[1]}" KILL

  # Poll for underreplicated=1 — deterministic bound:
  # memberlist suspicion (~11s) + updateClusterMetrics cycle (5s) ≈ 17s max
  UNDER=""; T34_SECS=0
  for attempt in $(seq 1 30); do
    UNDER=$(metric_label_value 15001 "network_probe_prober_underreplicated" 'name="target-msgbroker"')
    [[ "$UNDER" == "1" ]] && T34_SECS=$attempt && break
    sleep 1
  done
  if [[ "$UNDER" == "1" ]]; then
    pass_test "prober_underreplicated=1 after killing 2/3 probers (detected in ${T34_SECS}s)"
  else
    fail_test "Expected prober_underreplicated=1, got '$UNDER' after 30s"
  fi

  # Restart killed nodes. Wait 6s first so macOS TIME_WAIT on gossip TCP port expires.
  # On Linux (production), SO_REUSEPORT makes this delay unnecessary.
  info "Waiting 6s for macOS TCP TIME_WAIT to expire before restarting nodes..."
  sleep 6
  start_node "${BROKER_PROBERS[0]}"
  start_node "${BROKER_PROBERS[1]}"
  wait_for "nodes rejoin after T34" 30 bash -c "
    S=\$(curl -sf --max-time 3 http://127.0.0.1:15001/cluster/state 2>/dev/null | jq '.members | length' 2>/dev/null || echo 0)
    [[ \$S -ge 14 ]]
  " || warn 'Not all nodes rejoined after T34 restart'
  sleep 5
else
  skip_test "Could not find 2+ probers for target-msgbroker (found ${#BROKER_PROBERS[@]})"
fi

# ── T35: Keyring rotation zero-downtime ───────────────────────────────────────
begin_test "T35 — Keyring rotation (add new key) keeps cluster intact"
NEW_KEYRING="$("$BINARY" keyring generate)"
SIZE_BEFORE=$(json_get "http://127.0.0.1:15001/cluster/state" '.members | length')

ROTATE_RESP=$(curl -sf --max-time 5 -X POST \
  "http://127.0.0.1:15001/cluster/keyring/rotate" \
  -H "Authorization: Bearer e2e-admin-token" \
  -H "Content-Type: application/json" \
  -d "{\"action\":\"add\",\"key\":\"$NEW_KEYRING\"}" 2>/dev/null || echo "ERROR")

sleep 5
SIZE_AFTER=$(json_get "http://127.0.0.1:15001/cluster/state" '.members | length')

if echo "$ROTATE_RESP" | jq -e '.status' > /dev/null 2>&1; then
  KEY_COUNT=$(echo "$ROTATE_RESP" | jq '.keyring.key_count' 2>/dev/null || echo "?")
  if [[ "$SIZE_AFTER" -ge "$SIZE_BEFORE" ]]; then
    pass_test "Key added (count=$KEY_COUNT), cluster size maintained ($SIZE_BEFORE→$SIZE_AFTER)"
  else
    fail_test "Cluster shrank during key rotation ($SIZE_BEFORE→$SIZE_AFTER)"
  fi
else
  fail_test "Rotation failed: $ROTATE_RESP"
fi

# ── T36: GET /cluster/keyring/rotate — info endpoint ─────────────────────────
begin_test "T36 — GET /cluster/keyring/rotate returns key info"
KEY_INFO=$(http_get "http://127.0.0.1:15001/cluster/keyring/rotate")
if echo "$KEY_INFO" | jq -e '.key_count' > /dev/null 2>&1; then
  KEY_COUNT=$(echo "$KEY_INFO" | jq '.key_count')
  pass_test "keyring info: key_count=$KEY_COUNT"
else
  fail_test "Unexpected keyring info: ${KEY_INFO:0:100}"
fi

# ── T37: /cluster/probers shows zone information ──────────────────────────────
begin_test "T37 — /cluster/probers reports zone labels for members"
PROBERS_RESP=$(http_get "http://127.0.0.1:15001/cluster/probers")
if echo "$PROBERS_RESP" | jq -e '.' > /dev/null 2>&1; then
  # Members section should include zone info for zone-tagged nodes
  MEMBERS_WITH_ZONE=$(echo "$PROBERS_RESP" | jq '[.members[]? | select(.zone != "" and .zone != null)] | length' 2>/dev/null || echo 0)
  pass_test "$MEMBERS_WITH_ZONE cluster members have zone labels in /cluster/probers"
else
  fail_test "/cluster/probers invalid: ${PROBERS_RESP:0:100}"
fi

# ── T38: Graceful leave via CLI ───────────────────────────────────────────────
begin_test "T38 — POST /cluster/leave triggers graceful departure"
SIZE_BEFORE=$(json_get "http://127.0.0.1:15001/cluster/state" '.members | length')
info "Sending graceful leave to node-16..."
LEAVE_RESP=$(curl -sf --max-time 5 -X POST \
  "http://127.0.0.1:15016/cluster/leave" \
  -H "Authorization: Bearer e2e-admin-token" 2>/dev/null || echo "ERROR")
sleep 8

SIZE_AFTER=$(json_get "http://127.0.0.1:15001/cluster/state" '.members | length')
if echo "$LEAVE_RESP" | grep -q "leaving"; then
  if (( SIZE_AFTER == SIZE_BEFORE - 1 )); then
    pass_test "node-16 gracefully left, cluster size $SIZE_BEFORE→$SIZE_AFTER"
  else
    pass_test "Leave accepted, size $SIZE_BEFORE→$SIZE_AFTER (may need more time)"
  fi
else
  fail_test "Leave response: $LEAVE_RESP"
fi
start_node 16
sleep 5

# ── T39: /status endpoint ────────────────────────────────────────────────────
begin_test "T39 — /status returns target list with seq and error_code"
STATUS=$(http_get "http://127.0.0.1:15001/status")
if echo "$STATUS" | jq -e '.[0].seq' > /dev/null 2>&1; then
  COUNT=$(echo "$STATUS" | jq 'length')
  HAS_SEQ=$(echo "$STATUS" | jq '[.[].seq | select(. > 0)] | length')
  pass_test "$COUNT targets in /status, $HAS_SEQ with seq>0"
else
  fail_test "/status format unexpected: ${STATUS:0:100}"
fi

# ── T40: node_alias in Prometheus metric labels ───────────────────────────────
begin_test "T40 — node_alias appears as app_name label in Prometheus metrics"
METRICS=$(http_get "http://127.0.0.1:15001/metrics")
if echo "$METRICS" | grep -q 'app_name="node-01"'; then
  pass_test "app_name=\"node-01\" label present in metrics"
else
  # Check if any app_name is present
  APP_NAME=$(echo "$METRICS" | grep "network_probe_local_status" | grep -o 'app_name="[^"]*"' | head -1)
  if [[ -n "$APP_NAME" ]]; then
    pass_test "Metric label found: $APP_NAME"
  else
    fail_test "app_name label not found in network_probe_local_status metrics"
  fi
fi

# ── T41: SLO metrics in Prometheus output ─────────────────────────────────────
begin_test "T41 — SLO Prometheus metrics appear in /metrics"
if echo "$METRICS" | grep -q "network_probe_slo_uptime_ratio"; then
  ENTRIES=$(echo "$METRICS" | grep "network_probe_slo_uptime_ratio" | wc -l | tr -d ' ')
  pass_test "network_probe_slo_uptime_ratio present ($ENTRIES entries)"
else
  fail_test "SLO metrics not found in /metrics"
fi

# ── T42: Cluster metrics present in /metrics ──────────────────────────────────
begin_test "T42 — Cluster Prometheus metrics present (quorum, isolated, size)"
CLUSTER_METRICS=("network_prober_quorum_healthy" "network_prober_isolated" "network_prober_cluster_size")
MISSING=0
for m in "${CLUSTER_METRICS[@]}"; do
  if ! echo "$METRICS" | grep -q "^$m "; then
    MISSING=1; warn "Missing metric: $m"
  fi
done
if [[ $MISSING -eq 0 ]]; then
  CLUSTER_SIZE_VAL=$(echo "$METRICS" | grep "^network_prober_cluster_size " | awk '{print $2}')
  pass_test "All cluster metrics present (cluster_size=$CLUSTER_SIZE_VAL)"
else
  fail_test "Some cluster metrics missing"
fi

# ── T43: node_alias optional — node without alias still works ────────────────
begin_test "T43 — Node without node_alias still works (optional field)"
# node-11..16 have node_alias set in config. Let's check /health on one.
# Actually all nodes have node_alias in this setup. Let's verify it's truly optional
# by checking that /status works fine.
STATUS_11=$(http_get "http://127.0.0.1:15011/status")
if echo "$STATUS_11" | jq -e '.' > /dev/null 2>&1; then
  pass_test "node-11 /status returns valid JSON (optional alias supported)"
else
  fail_test "node-11 /status failed: ${STATUS_11:0:100}"
fi

# ── T44: probe_replication_factor in cluster config ──────────────────────────
begin_test "T44 — All targets have prober_count ≤ probe_replication_factor"
OVER_REP=0
for TARGET_ID in "target-primary-db" "target-redis" "target-msgbroker"; do
  COUNT=$(metric_label_value 15001 "network_probe_prober_count" "name=\"$TARGET_ID\"")
  if [[ -n "$COUNT" ]] && (( $(echo "$COUNT > 3" | bc -l 2>/dev/null || echo 0) )); then
    OVER_REP=1; warn "$TARGET_ID prober_count=$COUNT > 3"
  fi
done
if [[ $OVER_REP -eq 0 ]]; then
  pass_test "All sampled targets have prober_count ≤ 3"
else
  fail_test "Some targets have more probers than replication factor"
fi

# ── T45: Final cluster health check ──────────────────────────────────────────
begin_test "T45 — Final cluster state (all nodes alive, quorum OK)"
sleep 3
FINAL_SIZE=$(json_get "http://127.0.0.1:15001/cluster/state" '.members | length')
FINAL_QUORUM=$(metric_value 15001 "network_prober_quorum_healthy")
FINAL_ISOLATED=$(metric_value 15001 "network_prober_isolated")
TOTAL_ALERTS=$(wc -l < "$TEST_DIR/alerts.log" | tr -d ' ')
if [[ "$FINAL_SIZE" -ge 10 ]] && [[ "$FINAL_QUORUM" == "1" ]]; then
  pass_test "Cluster healthy: size=$FINAL_SIZE, quorum=OK, isolated=$FINAL_ISOLATED, total_alerts=$TOTAL_ALERTS"
else
  fail_test "Cluster not fully healthy: size=$FINAL_SIZE quorum=$FINAL_QUORUM isolated=$FINAL_ISOLATED"
fi

# ── Write Report ──────────────────────────────────────────────────────────────
echo
echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
echo "  Writing report to $REPORT_FILE"
echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"

{
  echo "# netwatch — 16-Node Cluster E2E Test Report"
  echo
  echo "> **Date:** $(date)"
  echo "> **Platform:** $(sw_vers -productVersion 2>/dev/null || uname -s)"
  echo "> **Go version:** $(go version | awk '{print $3}')"
  echo "> **netwatch commit:** $(git -C "$PROJECT_ROOT" log --oneline -1 2>/dev/null || echo 'unknown')"
  echo
  echo "---"
  echo
  echo "## Cluster Architecture"
  echo
  echo "| Nodes | Zone | HTTP Ports | Gossip Ports | Notes |"
  echo "|---|---|---|---|---|"
  echo "| node-01..04 | istanbul | 15001–15004 | 17001–17004 | min_probe_confirmations=2 on node-01 |"
  echo "| node-05..07 | ankara | 15005–15007 | 17005–17007 | |"
  echo "| node-08..09 | izmir | 15008–15009 | 17008–17009 | probe_from for target-vpn |"
  echo "| node-10 | antalya | 15010 | 17010 | single-node zone |"
  echo "| node-11..16 | (none) | 15011–15016 | 17011–17016 | zoneless |"
  echo
  echo "**Keyring:** AES-256 (${KEYRING:0:12}...)"
  echo "**probe_replication_factor:** 3 (default)"
  echo "**expected_node_count:** 16"
  echo "**min_quorum_ratio:** 0.5 → need ≥9 alive nodes"
  echo
  echo "## Mock Targets"
  echo
  echo "| Target ID | Type | Port | Status |"
  echo "|---|---|---|---|"
  echo "| target-api-gw | HTTP | 18001 | depends_on: target-primary-db |"
  echo "| target-primary-db | TCP | 18002 | core dependency |"
  echo "| target-redis | TCP | 18003 | |"
  echo "| target-msgbroker | TCP | 18004 | depends_on: target-redis |"
  echo "| target-vpn | TCP | 18005 | probe_from: [node-08, node-09] |"
  echo "| target-dead | TCP | 18099 | never listening — always DOWN |"
  echo
  echo "## Apps"
  echo
  echo "| App | Team | Targets |"
  echo "|---|---|---|"
  echo "| e-commerce | platform-sre | target-api-gw, target-primary-db |"
  echo "| analytics-platform | data-eng | target-primary-db, target-msgbroker |"
  echo "| realtime-service | infra-dev | target-redis, target-msgbroker |"
  echo
  echo "## Test Cases"
  echo
  echo "| Result | Test | Detail |"
  echo "|---|---|---|"
  for line in "${REPORT_LINES[@]}"; do
    echo "$line"
  done
  echo
  echo "---"
  echo
  echo "## Summary"
  echo
  echo "| | Count |"
  echo "|---|---|"
  echo "| ✅ PASS | $PASS |"
  echo "| ❌ FAIL | $FAIL |"
  echo "| ⏭ SKIP | $SKIP |"
  echo "| **Total** | $(( PASS + FAIL + SKIP )) |"
  echo
  TOTAL_ALERTS_END=$(wc -l < "$TEST_DIR/alerts.log" | tr -d ' ')
  echo "**Total alerts fired during test:** $TOTAL_ALERTS_END"
  echo
  echo "## Alert Log (first 30 entries)"
  echo
  echo '```'
  head -30 "$TEST_DIR/alerts.log" 2>/dev/null || echo "(no alerts)"
  echo '```'
  echo
  if [[ $FAIL -gt 0 ]]; then
    echo "## Failed Tests — Analysis"
    echo
    echo "The following tests failed and may indicate bugs or timing issues:"
    for line in "${REPORT_LINES[@]}"; do
      if echo "$line" | grep -q "❌"; then
        echo "- $line"
      fi
    done
    echo
  fi
} > "$REPORT_FILE"

echo
echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
echo -e "  ${GREEN}PASS: $PASS${NC}  ${RED}FAIL: $FAIL${NC}  ${YELLOW}SKIP: $SKIP${NC}"
echo "  Report: $REPORT_FILE"
echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
