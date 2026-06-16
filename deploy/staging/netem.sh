#!/usr/bin/env bash
# netem.sh — inject network faults into a staging node's network namespace.
#
# The netwatch image is distroless and has no `tc`, so we attach a throwaway
# netshoot container (which ships iproute2) to the TARGET container's network
# namespace and run `tc` there. Because the helper shares the node's netns, the
# qdisc it installs applies to the node's own traffic — a faithful way to
# simulate a partition / loss / latency on one cluster member.
#
# Usage:
#   netem.sh <container> partition        # 100% packet loss (full partition)
#   netem.sh <container> loss <pct>       # N% packet loss
#   netem.sh <container> delay <ms>       # add N ms latency
#   netem.sh <container> clear            # remove all injected faults
#
# Example:
#   ./netem.sh netwatch-3 partition       # node-3 drops off the gossip mesh
#   ./netem.sh netwatch-3 clear           # node-3 rejoins
set -euo pipefail

NODE="${1:?usage: netem.sh <container> <partition|loss PCT|delay MS|clear>}"
ACTION="${2:?usage: netem.sh <container> <partition|loss PCT|delay MS|clear>}"
HELPER_IMAGE="${NETEM_HELPER_IMAGE:-nicolaka/netshoot:latest}"

run_tc() {
  docker run --rm --network "container:${NODE}" --cap-add NET_ADMIN \
    "${HELPER_IMAGE}" sh -c "$1"
}

case "${ACTION}" in
  partition) run_tc "tc qdisc replace dev eth0 root netem loss 100%";    echo "✓ ${NODE}: full partition (100% loss)";;
  loss)      run_tc "tc qdisc replace dev eth0 root netem loss ${3:?pct}%"; echo "✓ ${NODE}: ${3}% packet loss";;
  delay)     run_tc "tc qdisc replace dev eth0 root netem delay ${3:?ms}ms"; echo "✓ ${NODE}: +${3}ms latency";;
  clear)     run_tc "tc qdisc del dev eth0 root || true";                echo "✓ ${NODE}: faults cleared";;
  *) echo "unknown action: ${ACTION}" >&2; exit 2;;
esac
