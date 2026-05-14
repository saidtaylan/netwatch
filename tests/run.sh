#!/usr/bin/env bash
# tests/run.sh — single entry point to run the entire netwatch test suite.
#
# Usage:
#   ./tests/run.sh            # all tests (unit + domain)
#   ./tests/run.sh unit       # fast unit tests only (engine + cluster)
#   ./tests/run.sh domain     # domain tests only (need ~90s, use real ports)
#   ./tests/run.sh internal   # existing internal tests (195 functions)
#   ./tests/run.sh all        # unit + domain + internal (full suite)
#
# Options forwarded to `go test`:
#   ./tests/run.sh -v         # verbose output
#   ./tests/run.sh unit -v    # verbose unit tests

set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT"

FILTER="${1:-all}"
shift 2>/dev/null || true
EXTRA_ARGS=("$@")

UNIT_PKGS="./tests/engine/... ./tests/cluster/..."
DOMAIN_PKGS="./tests/domain/..."
INTERNAL_PKGS="./internal/engine/... ./internal/cluster/..."

RACE="-race"
UNIT_TIMEOUT="-timeout 60s"
DOMAIN_TIMEOUT="-timeout 200s"
INTERNAL_TIMEOUT="-timeout 180s"

run_unit() {
    echo "▶ Unit tests (tests/engine + tests/cluster)"
    go test $RACE -count=1 $UNIT_TIMEOUT "${EXTRA_ARGS[@]}" $UNIT_PKGS
}

run_domain() {
    echo "▶ Domain tests (tests/domain) — uses real TCP ports, ~90s"
    go test $RACE -count=1 $DOMAIN_TIMEOUT "${EXTRA_ARGS[@]}" $DOMAIN_PKGS
}

run_internal() {
    echo "▶ Internal tests (internal/engine + internal/cluster)"
    go test $RACE -count=1 $INTERNAL_TIMEOUT "${EXTRA_ARGS[@]}" $INTERNAL_PKGS
}

case "$FILTER" in
  unit)
    run_unit
    ;;
  domain)
    run_domain
    ;;
  internal)
    run_internal
    ;;
  all)
    run_unit
    run_domain
    run_internal
    ;;
  *)
    # Default: unit + domain (without internal to keep CI reasonable)
    run_unit
    run_domain
    ;;
esac

echo ""
echo "✓ All requested tests passed."
