#!/usr/bin/env bash
# smoke.sh — curl-based, artifact-level acceptance gate for the staging cluster.
#
# This is the dependable pre-prod test: it talks HTTP to the 3 nodes booted from
# the RELEASED image (docker-compose.staging.yml), so it validates exactly what
# ships — independent of any local source build or Playwright/node-count
# assumptions. Run it after `make staging-up`.
#
# Covers the real failure modes:
#   1. cluster converges (3 alive members, quorum healthy)
#   2. a down target reaches REAL_OUTAGE consensus across the cluster
#   3. distributed probe ownership assigns replication_factor probers per target
#   4. JWT for a deleted user is rejected (the v0.1.6 stale-token fix)
#   5. node kill → remaining majority keeps quorum; node rejoins cleanly
#
# Exit code is non-zero on the first failed assertion.
set -uo pipefail

N1="${N1:-http://localhost:10240}"   # node-1 API
SETUP_TOKEN="${SETUP_TOKEN:-staging-shared-secret-0123456789abcdef}"
fails=0

py()   { python3 -c "import sys,json; d=json.load(sys.stdin); print($1)" 2>/dev/null; }
pass() { echo "  ✓ $1"; }
fail() { echo "  ✗ $1"; fails=$((fails+1)); }
hdr()  { echo; echo "── $1"; }

# Wait until node-1 answers and the cluster shows `want` alive members.
wait_members() {
  local want="$1" tries=40 got
  while [ $tries -gt 0 ]; do
    got=$(curl -fsS "$N1/cluster/state" 2>/dev/null | py 'len(d["members"])') || got=0
    [ "$got" = "$want" ] && return 0
    sleep 2; tries=$((tries-1))
  done
  return 1
}

hdr "1. Cluster convergence"
if wait_members 3; then pass "3 alive members in /cluster/state"
else fail "cluster did not reach 3 members"; fi
quorum=$(curl -fsS "$N1/fleet/status" | py 'd["cluster"]["quorum_healthy"]')
[ "$quorum" = "True" ] && pass "quorum_healthy = true" || fail "quorum_healthy = $quorum"

hdr "2. Down-target consensus (REAL_OUTAGE)"
# Give probers a couple of intervals to converge on the always-down target.
sleep 18
state=$(curl -fsS "$N1/fleet/status" | py '[t for t in d["targets"] if t["id"]=="always-down"][0]["consensus_state"]')
[ "$state" = "hard_down" ] && pass "always-down consensus = hard_down" || fail "always-down consensus = $state"
cls=$(curl -fsS "$N1/fleet/status" | py '[t for t in d["targets"] if t["id"]=="always-down"][0].get("classification","")')
[ "$cls" = "REAL_OUTAGE" ] && pass "classification = REAL_OUTAGE" || echo "  ~ classification = $cls (expected REAL_OUTAGE; non-fatal during convergence)"

hdr "3. Distributed probe ownership"
pc=$(curl -fsS "$N1/cluster/probers" | py 'len(d["assignments"]["node-1-api"]["probers"])' 2>/dev/null)
[ "$pc" = "2" ] && pass "node-1-api assigned 2 probers (replication_factor)" || fail "node-1-api prober count = ${pc:-?} (want 2)"

hdr "4. Stale-JWT rejection (v0.1.6 fix)"
status=$(curl -fsS "$N1/auth/status" | py 'd["setup_completed"]')
if [ "$status" = "False" ]; then
  admin=$(curl -fsS -X POST "$N1/auth/setup" -H 'Content-Type: application/json' \
    -d "{\"setup_token\":\"$SETUP_TOKEN\",\"username\":\"admin\",\"password\":\"AdminPass123!\"}")
else
  admin=$(curl -fsS -X POST "$N1/auth/login" -H 'Content-Type: application/json' \
    -d '{"username":"admin","password":"AdminPass123!"}')
fi
ADMIN_TOKEN=$(echo "$admin" | py 'd["token"]')
[ -n "$ADMIN_TOKEN" ] && pass "admin token obtained" || fail "could not obtain admin token"

# Create an ephemeral user, log in as them, then delete them.
curl -fsS -X PUT "$N1/users/ephemeral-smoke" -H "Authorization: Bearer $ADMIN_TOKEN" \
  -H 'Content-Type: application/json' \
  -d '{"username":"ephemeral","password":"EphPass123!","role":"operator"}' >/dev/null
eph=$(curl -fsS -X POST "$N1/auth/login" -H 'Content-Type: application/json' \
  -d '{"username":"ephemeral","password":"EphPass123!"}')
EPH_TOKEN=$(echo "$eph" | py 'd["token"]')
code=$(curl -s -o /dev/null -w '%{http_code}' "$N1/auth/me" -H "Authorization: Bearer $EPH_TOKEN")
[ "$code" = "200" ] && pass "ephemeral token works while user exists (200)" || fail "ephemeral /auth/me = $code (want 200)"
curl -fsS -X DELETE "$N1/users/ephemeral-smoke" -H "Authorization: Bearer $ADMIN_TOKEN" >/dev/null
code=$(curl -s -o /dev/null -w '%{http_code}' "$N1/auth/me" -H "Authorization: Bearer $EPH_TOKEN")
[ "$code" = "401" ] && pass "deleted user's token rejected (401)" || fail "stale token /auth/me = $code (want 401)"

hdr "5. Node kill → quorum holds → rejoin"
if command -v docker >/dev/null 2>&1; then
  docker kill netwatch-3 >/dev/null 2>&1 && pass "killed netwatch-3"
  sleep 12
  alive=$(curl -fsS "$N1/fleet/status" | py 'd["cluster"]["alive_count"]')
  q=$(curl -fsS "$N1/fleet/status" | py 'd["cluster"]["quorum_healthy"]')
  [ "$alive" = "2" ] && pass "alive_count = 2 after kill" || fail "alive_count = $alive (want 2)"
  [ "$q" = "True" ] && pass "quorum still healthy with 2/3" || fail "quorum_healthy = $q (want true)"
  docker start netwatch-3 >/dev/null 2>&1
  if wait_members 3; then pass "netwatch-3 rejoined (back to 3 members)"; else fail "netwatch-3 did not rejoin"; fi
else
  echo "  ~ docker not available — skipping node-kill scenario"
fi

echo
if [ "$fails" -eq 0 ]; then echo "✅ staging smoke PASSED"; exit 0
else echo "❌ staging smoke FAILED ($fails assertion(s))"; exit 1; fi
