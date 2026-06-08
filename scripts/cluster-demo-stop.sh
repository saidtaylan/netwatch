#!/usr/bin/env bash
# Stop the 5-node netwatch cluster started by cluster-demo-start.sh.
set -uo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
DEMO="$ROOT/cluster-demo"

for i in 1 2 3 4 5; do
  PID="$DEMO/pids/n$i.pid"
  if [[ -f "$PID" ]]; then
    P=$(cat "$PID")
    if kill -0 "$P" 2>/dev/null; then
      kill "$P" && echo "n$i stopped (PID $P)"
    else
      echo "n$i pidfile stale (PID $P)"
    fi
    rm -f "$PID"
  else
    echo "n$i no pidfile"
  fi
done

# Belt-and-suspenders: any survivors
sleep 1
LEFT=$(pgrep -f "cluster-demo/netwatch -config" || true)
if [[ -n "$LEFT" ]]; then
  echo "Force-killing leftovers: $LEFT"
  echo "$LEFT" | xargs -r kill -9
fi
echo "done"
