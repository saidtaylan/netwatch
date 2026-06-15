# Staging — prod-faithful pre-production test (single machine)

The most realistic test you can run **before** promoting a release, without
owning a second server. It boots a real **3-node cluster from the published
artifacts** — the `ghcr.io/saidtaylan/netwatch` container image and the released
`netwatch-frontend.tar.gz` bundle — so what you test is byte-for-byte what ships.
No local source build is involved, which is the whole point: you stop trusting
`go build` / `pnpm dev` on your laptop and start trusting the artifact.

Three separate containers = three separate network identities = a genuine
gossip/quorum/hash-ring cluster. The only thing a single host can't reproduce is
true cross-host network hardware; `netem` (below) covers the software side of
that — packet loss, latency, and full partitions.

## Quick start

```bash
make staging-up        # pull + boot 3 nodes + UI, print cluster members
make staging-smoke     # curl-based acceptance gate (the dependable test)
make staging-down      # tear down + wipe volumes
```

- UI:        http://localhost:8080  (on Connect, enter `http://localhost:10240`)
- node-1 API http://localhost:10240   node-2 :10241   node-3 :10242

Test a release candidate instead of the default:

```bash
VERSION=0.1.7-rc.1 make staging-up      # VERSION has NO leading "v"
```

## What `make staging-smoke` asserts

It talks plain HTTP to the running cluster (`deploy/staging/smoke.sh`) and fails
on the first bad assertion. It is intentionally **not** Playwright: no local
build, no 5-node/browser assumptions — just the artifact's real behavior.

1. **Cluster convergence** — `/cluster/state` shows 3 alive members; quorum healthy.
2. **Down-target consensus** — the `always-down` target reaches `hard_down` with
   `REAL_OUTAGE` classification across the cluster.
3. **Distributed probe ownership** — each target is assigned exactly
   `probe_replication_factor` (2) probers via `/cluster/probers`.
4. **Stale-JWT rejection (v0.1.6 fix)** — an ephemeral user is created, logs in
   (200), is deleted, and its token is then rejected (401). This is the
   deleted-user / DB-reset class of bug, tested deterministically.
5. **Node kill → quorum holds → rejoin** — `docker kill netwatch-3` leaves 2/3
   (still quorate); restarting it returns the cluster to 3 members.

## Network fault injection (`netem`)

The image is distroless (no `tc`), so `netem.sh` attaches a throwaway `netshoot`
container to a node's network namespace and applies `tc qdisc netem` there.

```bash
make staging-netem-partition NODE=netwatch-3   # 100% packet loss
make staging-netem-clear     NODE=netwatch-3   # remove the fault
# or directly:
deploy/staging/netem.sh netwatch-3 loss 30     # 30% loss
deploy/staging/netem.sh netwatch-3 delay 200   # +200ms latency
```

### Known behavior — rejoin after a *full* partition

A **brief** partition (cleared before memberlist declares the node dead) heals on
its own. A **prolonged full partition** is different: the other nodes declare the
isolated node dead and remove it, and after the fault clears it does **not**
auto-rejoin — it stays split-brained (it sees only itself; the majority sees only
each other). Recovery today is to restart that node:

```bash
docker restart netwatch-3      # rejoins, cluster returns to 3 members
```

This is a genuine resilience gap surfaced by this harness: there is no periodic
re-join loop against the seed peers after a node has been evicted. Worth a
follow-up (a background re-join ticker in `internal/cluster`).

## Also: run the UI e2e against the released bundle (optional)

The UI is served by nginx from the **released** bundle at :8080. You can point a
browser there manually, or adapt the live Playwright suite
(`frontend/playwright.live.config.ts`) — note it currently hard-codes a 5-node
demo contract (ports 10241-10245, a fixed setup token) and drives a local
`pnpm preview` build, so the curl smoke above is the artifact-level gate.

## CI

`.github/workflows/staging-smoke.yml` runs this exact flow on a clean GitHub
runner (boot the released image as a 3-node cluster, run `smoke.sh`) — so the
gate doesn't depend on your laptop at all. It runs automatically on `-rc` tags
and can be dispatched manually for any version.

## Recommended release flow

1. Cut a release candidate: `git tag v0.1.7-rc.1 && git push origin v0.1.7-rc.1`.
   The release workflow publishes `:0.1.7-rc.1` (a GitHub *pre-release*) and does
   **not** move `:latest` or `:0.1`.
2. `VERSION=0.1.7-rc.1 make staging-up && make staging-smoke` (and any manual UI
   / netem checks).
3. If green, promote: `git tag v0.1.7 && git push origin v0.1.7` → this moves
   `:latest`. Production (`docker compose pull`) only ever sees a gated artifact.
