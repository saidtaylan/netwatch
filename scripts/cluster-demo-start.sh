#!/usr/bin/env bash
# Start 5-node netwatch cluster from workspace/cluster-demo/.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
DEMO="$ROOT/cluster-demo"
BIN="$DEMO/netwatch"

if [[ ! -x "$BIN" ]]; then
  echo "Binary missing: $BIN  — build with: (cd backend && go build -o ../cluster-demo/netwatch ./cmd/linux)"
  exit 1
fi

mkdir -p "$DEMO/pids" "$DEMO/logs"

for i in 1 2 3 4 5; do
  CFG="$DEMO/n$i/config.yaml"
  LOG="$DEMO/logs/n$i.log"
  PID="$DEMO/pids/n$i.pid"
  if [[ -f "$PID" ]] && kill -0 "$(cat "$PID")" 2>/dev/null; then
    echo "n$i already running (PID $(cat "$PID"))"
    continue
  fi
  nohup "$BIN" -config "$CFG" >>"$LOG" 2>&1 &
  echo $! > "$PID"
  echo "n$i started: PID=$(cat "$PID")  HTTP=$((10240 + i))  log=$LOG"
done

echo
echo "Wait ~3s for cluster to converge, then:"
echo "  curl http://127.0.0.1:10241/health"
echo "  curl http://127.0.0.1:10241/auth/status"
echo "  curl http://127.0.0.1:10241/cluster/state"
