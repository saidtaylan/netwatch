# Dev stack — local code, live, multi-node (no Docker)

The development counterpart to [`deploy/staging`](../staging). Staging proves a
**released** artifact in containers; this runs **your local code** as **host
processes** so changes are live:

- **Backend** — 3 nodes built from `../../backend` and run on `127.0.0.1` as a
  real gossip cluster. After a Go change, `make dev-restart` rebuilds and
  relaunches in ~2s. (Docker is intentionally avoided: a container backend would
  need an image rebuild on every change.)
- **Frontend** — `make dev-frontend` runs the Nuxt dev server with HMR, so
  `.vue`/`.ts` edits hit the browser instantly.

It ships a **rich, demo-ready dataset** (9 targets across tcp/http/dns, a down
dependency chain with root-cause, 3 apps with owner teams, 5 SLOs, 3 notification
channels, plus a seeded maintenance window) so every page is full for
screenshots.

## Quick start

```bash
make dev-up          # build + start the 3-node backend cluster + seed demo data
make dev-frontend    # in a second terminal: Nuxt dev server with HMR
```

- Frontend → http://localhost:3000 — backend URL is pre-filled as
  `http://localhost:11240`. On first run go to `/setup` and use the setup token
  `dev-setup-token-change-me-0001` to create your admin.
- Backend nodes → http://localhost:11240 / :11241 / :11242

## The dev loop

| You changed… | Do this |
|---|---|
| Frontend (`.vue` / `.ts` / styles) | nothing — HMR reloads automatically |
| Backend (Go) | `make dev-restart` (rebuild + relaunch, keeps state) |
| A node's `config.yaml` | nothing — `reload_interval_sec` re-reads it within ~15s |

```bash
make dev-status      # cluster members
make dev-logs        # tail all three node logs
make dev-seed        # re-seed runtime-only data (the maintenance window)
make dev-down        # stop all nodes
```

## What's in the demo dataset

- **Up**: `node-1/2/3-api`, `dns-resolver` (tcp), `company-website` (http),
  `public-dns` (dns)
- **Down chain** (root-cause + cascade): `payments-db` → `payment-api` →
  `checkout` — all REAL_OUTAGE, root cause resolves to `payments-db`
- **Apps**: Payments (fintech-sre), Web Platform (platform-eng), Infrastructure
  (infra-ops)
- **SLOs**: 5 targets across 24h / 7d / 30d windows
- **Maintenance**: one window on `company-website` (seeded by `seed.sh`)

The external targets (`company-website`, `public-dns`, `dns-resolver`) need
outbound internet; without it they simply show as down.

## Side by side with staging

Dev uses host ports `112xx` / `3000`; staging uses `102xx` / `8080` in Docker.
They don't collide, so you can run both. Runtime state lives in
`/tmp/netwatch-dev/` (wiped by `make dev-up`).
