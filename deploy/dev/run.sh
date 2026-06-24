#!/usr/bin/env bash
# Host-process dev cluster manager (no Docker) — builds the backend from local
# source and runs 3 nodes as a real gossip cluster on 127.0.0.1. Because the
# binary is your local code, `restart` (rebuild + relaunch) reflects backend
# changes in a couple of seconds; the frontend is separately served by
# `pnpm dev` with HMR.
#
#   run.sh up        build + (fresh state) + start 3 nodes
#   run.sh restart   rebuild + restart, keeping state (use after Go changes)
#   run.sh down      stop all nodes
#   run.sh status    show cluster members
set -uo pipefail

HERE="$(cd "$(dirname "$0")" && pwd)"
ROOT="$(cd "$HERE/../.." && pwd)"
RUNDIR="/tmp/netwatch-dev"
BIN="$RUNDIR/netwatch-dev"

build() {
  echo "==> building backend from local source…"
  mkdir -p "$RUNDIR"
  (cd "$ROOT/backend" && go build -o "$BIN" ./cmd/linux/) || { echo "build failed"; exit 1; }
}

start() {
  for n in 1 2 3; do
    mkdir -p "$RUNDIR/n$n"
    nohup "$BIN" -config "$HERE/node-$n.yaml" >"$RUNDIR/n$n/out.log" 2>&1 &
    echo $! >"$RUNDIR/n$n.pid"
    echo "  node-$n → http://localhost:1124$((n-1))  (pid $(cat "$RUNDIR/n$n.pid"))"
  done
}

stop() {
  for n in 1 2 3; do
    if [ -f "$RUNDIR/n$n.pid" ]; then
      kill "$(cat "$RUNDIR/n$n.pid")" 2>/dev/null || true
      rm -f "$RUNDIR/n$n.pid"
    fi
  done
  pkill -f "$BIN -config" 2>/dev/null || true
  # Wait for the processes to actually exit before returning. A graceful cluster
  # leave can take a few seconds, and if we relaunch before the old processes
  # release their HTTP/gossip ports, the new ones fail to bind. SIGKILL anything
  # still alive after the grace period.
  for _ in $(seq 1 20); do
    pgrep -f "$BIN -config" >/dev/null 2>&1 || break
    sleep 0.5
  done
  pkill -9 -f "$BIN -config" 2>/dev/null || true
  echo "stopped dev cluster"
}

status() {
  curl -fsS http://localhost:11240/cluster/state 2>/dev/null \
    | python3 -c "import sys,json; d=json.load(sys.stdin); print('alive members:', len(d['members'])); [print(' -', m['name'], m['status'], m.get('zone','')) for m in d['members']]" \
    || echo "not running / not ready yet"
}

case "${1:-}" in
  up)      build; stop; rm -rf "$RUNDIR"/n1 "$RUNDIR"/n2 "$RUNDIR"/n3; start
           echo "==> 3-node dev cluster up on :11240 / :11241 / :11242" ;;
  restart) build; stop; start; echo "==> rebuilt + restarted (state kept)" ;;
  down)    stop ;;
  status)  status ;;
  build)   build ;;
  *) echo "usage: run.sh {up|restart|down|status|build}"; exit 1 ;;
esac
