# netwatch — Frontend

Nuxt 3 SPA admin UI for the netwatch monitoring backend.

## Overview

The netwatch frontend is a pure static single-page application built with:

- **[Nuxt 3](https://nuxt.com)** with `ssr: false` — compiles to plain HTML/JS/CSS with no server-side runtime requirement
- **[Tailwind CSS](https://tailwindcss.com)** + `@nuxtjs/color-mode` — light/dark/system theme support
- **[Pinia](https://pinia.vuejs.org)** — state management (auth, node connections, UI state, client-side alert feed)
- **JWT authentication** — login flow with role-based access; tokens stored in `localStorage` per connected node
- **Multi-node aware** — you can connect to any backend node in the cluster; the UI reflects that node's view of the fleet

**Key pages:**

| Path | Purpose |
|---|---|
| `/connect` | Enter backend URL and verify connectivity |
| `/setup` | First-time admin account creation (runs once) |
| `/login` | Username + password authentication |
| `/` (index) | Fleet dashboard — all targets, consensus state, scope, classification |
| `/targets` | Target list with filtering and detail view |
| `/topology` | Dependency graph — root cause and cascade impact |
| `/slo` | SLO uptime ratios, error budgets, and incident history |
| `/maintenance` | Maintenance windows — create and manage |
| `/silences` | Alert silence rules |
| `/apps` | App-to-target groupings and owner teams |
| `/channels` | Notification channel management |
| `/users` | User account management (admin only) |
| `/config` | Cluster config push and keyring rotation |
| `/geo` | Per-node geographic latency breakdown |
| `/alerts` | Client-side alert feed |
| `/docs` | Embedded API documentation |

> [!NOTE]
> The `/logs` and `/audit` pages exist on disk but are excluded from routing in `nuxt.config.ts` — they require direct HTTP access to each backend node and are not yet production-ready.

---

## Production Deployment

### Why nginx?

netwatch frontend compiles to pure static files (`ssr: false`). In production, nginx serves these files directly — **no Node.js runtime required**. This is simpler, lighter, and more reliable than running a Node.js process in production.

### Build

```bash
cd frontend
pnpm install
pnpm build
# Output: .output/public/
```

### Copy to the server

```bash
rsync -a .output/public/ user@your-server:/opt/netwatch-frontend/
```

### Configure nginx

```nginx
server {
    listen 80;
    server_name netwatch.example.com;

    root /opt/netwatch-frontend;
    index index.html;

    location / {
        try_files $uri $uri/ /index.html;  # SPA fallback — required for client-side routing
    }

    # Optional: cache static assets aggressively (they are content-hashed)
    location ~* \.(js|css|woff2?|png|svg|ico)$ {
        expires 1y;
        add_header Cache-Control "public, immutable";
    }
}
```

For HTTPS, add a `listen 443 ssl` block and reference your certificate:

```nginx
    listen 443 ssl;
    ssl_certificate     /etc/ssl/certs/netwatch.crt;
    ssl_certificate_key /etc/ssl/private/netwatch.key;
```

### Using Ansible

See `../ansible/` — the `roles/netwatch-frontend` role handles nginx installation, static file copy, and vhost configuration automatically. No manual steps required on the target host.

---

## Development

### Prerequisites

- Node.js 20 or later
- [pnpm](https://pnpm.io) (`npm install -g pnpm`)

### Setup

```bash
cd frontend
pnpm install
pnpm dev
```

The dev server starts at `http://localhost:3000`. Open the `/connect` page and enter your backend URL (e.g. `http://localhost:10240`).

To skip the URL input step during development, pre-fill it via environment variable:

```bash
NUXT_PUBLIC_DEFAULT_BACKEND_URL=http://localhost:10240 pnpm dev
```

### Environment Variables

| Variable | Description |
|---|---|
| `NUXT_PUBLIC_DEFAULT_BACKEND_URL` | Pre-fills the backend URL on the `/connect` page. Useful during development; not required in production (users enter the URL manually) |

### Run Tests

```bash
pnpm test         # Vitest unit tests
pnpm test:e2e     # Playwright end-to-end tests (requires a running backend)
```

---

## Architecture

### Auth Flow

```
App opens
  ├─ localStorage has node URL(s)?
  │   ├─ YES + valid JWT  →  Dashboard (/)
  │   └─ YES + no JWT     →  /login
  └─ NO  →  /connect  →  GET /auth/status
                              ├─ setup_completed: true   →  /login
                              └─ setup_completed: false  →  /setup
```

Route guards are implemented as global Nuxt middleware:

- `auth.global.ts` — redirects unauthenticated requests to `/connect` or `/login`
- `node-health.global.ts` — redirects to `/connect` when no backend node is configured

### Pinia Stores

| Store | File | Responsibility |
|---|---|---|
| `auth` | `stores/auth.ts` | JWT token, current user, login/logout |
| `nodes` | `stores/nodes.ts` | Connected backend node URLs, active node selection |
| `ui` | `stores/ui.ts` | Sidebar state, theme, global loading flags |
| `alerts` | `stores/alerts.ts` | Client-side alert feed (max 100 entries, resets on page reload) |

> [!NOTE]
> The alert feed is client-side only. It detects state transitions by diffing consecutive `/fleet/status` poll results. If the browser tab is closed, in-flight state changes are missed. A persistent backend `/alerts` endpoint is planned (backlog item B7).

### Composables

| Composable | Purpose |
|---|---|
| `useApi` | Typed fetch wrapper — injects `Authorization` header, handles 401 redirects |
| `useAuth` | Login, logout, token refresh helpers |
| `usePolling` | Generic interval polling with visibility-aware pause (stops when tab is hidden) |
| `useFleet` | Polls `/fleet/status` every 5 s; feeds the dashboard and alert store |
| `useCluster` | Polls `/cluster/state` every 5 s |
| `useMaintenance` | Polls `/cluster/maintenance` every 15 s |
| `useSLO` | Fetches `/slo` on demand |
| `useTopology` | Fetches `/topology` on demand |
| `useGeoLatency` | Fetches `/geo/latency/{targetID}` on demand |
| `useNodeConnection` | Manages connecting to / switching between backend nodes |

### Pages Structure

```
app/pages/
├── connect.vue          # Backend URL entry + connectivity check
├── setup.vue            # Initial admin account creation
├── login.vue            # JWT authentication
├── reset-password.vue   # Password reset
├── index.vue            # Fleet dashboard
├── targets/             # Target list and detail views
├── topology.vue         # Dependency graph
├── slo.vue              # SLO results
├── maintenance.vue      # Maintenance windows
├── silences.vue         # Alert silences
├── apps.vue             # App groupings
├── channels.vue         # Notification channels
├── users.vue            # User management
├── config/              # Cluster config and keyring
├── geo.vue              # Geographic latency
├── alerts.vue           # Client-side alert feed
└── docs.vue             # Embedded API documentation
```

---

## Directory Structure

```
frontend/
├── app/
│   ├── pages/           # Nuxt auto-routed pages (see above)
│   ├── components/      # Shared Vue components (auto-imported, no prefix)
│   ├── composables/     # useApi, useAuth, usePolling, useFleet, etc.
│   ├── stores/          # Pinia stores (auth, nodes, ui, alerts)
│   ├── types/           # TypeScript type definitions for all API responses
│   ├── middleware/       # auth.global.ts, node-health.global.ts
│   ├── layouts/         # Default layout (sidebar + topbar)
│   ├── plugins/         # Nuxt plugins
│   └── assets/css/      # Tailwind entry point (main.css)
├── tests/
│   ├── unit/            # Vitest unit tests
│   └── e2e/             # Playwright end-to-end tests
├── nuxt.config.ts       # Nuxt configuration
├── tailwind.config.ts   # Tailwind configuration
└── package.json
```
