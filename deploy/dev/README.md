# Dev stack — local code, live development

The development counterpart to [`deploy/staging`](../staging). Where staging
proves a **released** artifact, this runs **your local code**:

- **Backend** is built from `../../backend` (`build:` in the compose file) — no
  ghcr image is pulled. After Go changes, `make dev-rebuild`.
- **Frontend** runs the **Nuxt dev server with HMR** off `../../frontend` — edit
  a `.vue`/`.ts` file and the browser updates instantly. No release bundle.

Two backend nodes form a real cluster so you can develop cluster features.

## Quick start

```bash
make dev-up          # build + start 2 backend nodes + the Nuxt dev server
make dev-status      # show cluster members
make dev-down        # stop + wipe volumes
```

- Frontend (HMR) → http://localhost:3000 — the backend URL is pre-filled as
  `http://localhost:11240`; create your admin on first run.
- Backend node-1 → http://localhost:11240   node-2 → http://localhost:11241

The first `dev-up` is slower: it compiles the backend image and runs
`pnpm install` inside the UI container (cached in a named volume afterwards).

## The dev loop

| You changed… | Do this |
|---|---|
| Frontend (`.vue` / `.ts` / styles) | nothing — HMR reloads automatically |
| Backend (Go) | `make dev-rebuild` (rebuilds + restarts the two nodes) |
| A node's `config.yaml` (`node-1.yaml` / `node-2.yaml`) | nothing — `reload_interval_sec` re-reads it within ~15s |

```bash
make dev-rebuild     # docker compose up -d --build for the backend services
make dev-logs        # follow logs
```

## Side by side with staging

Dev uses offset host ports (`112xx`, `3000`) while staging uses `102xx` / `8080`,
so both can run at once. They use separate Docker volumes and networks.

## Notes

- `CHOKIDAR_USEPOLLING=true` makes HMR file-watching reliable over bind mounts
  (Docker Desktop on macOS/Windows doesn't forward native FS events).
- The frontend talks to the backend cross-origin (`:3000` → `:11240`); the
  backend already sends permissive CORS for local development.
- This stack is for development only. To test what actually ships, use
  `deploy/staging` (released artifacts) or the curl/netem gates there.
