#!/usr/bin/env bash
# Seed runtime-only demo data for screenshots.
#
# Targets, apps, SLOs and channels come from config (node-*.yaml). Maintenance
# windows, however, are created at runtime via the API, so we add one here. Auth
# uses the raw setup_token as a bearer (checkAdminAuth accepts it), so no login
# is required. Safe to run repeatedly.
set -uo pipefail

N1="${N1:-http://localhost:11240}"
TOKEN="${SETUP_TOKEN:-dev-setup-token-change-me-0001}"

echo "==> waiting for dev cluster quorum…"
for _ in $(seq 1 40); do
  q=$(curl -fsS "$N1/fleet/status" 2>/dev/null \
      | python3 -c "import sys,json;print((json.load(sys.stdin).get('cluster') or {}).get('quorum_healthy'))" 2>/dev/null)
  [ "$q" = "True" ] && break
  sleep 2
done

echo "==> seeding a maintenance window (Company Website)…"
curl -fsS -X PUT "$N1/cluster/maintenance" \
  -H "Authorization: Bearer $TOKEN" \
  -H 'Content-Type: application/json' \
  -d '{"target_ids":["company-website"],"duration":"6h","reason":"Scheduled CDN migration","started_by":"platform-eng"}' \
  >/dev/null 2>&1 \
  && echo "    maintenance window created" \
  || echo "    (maintenance seed skipped — cluster not ready yet; re-run 'make dev-seed')"
