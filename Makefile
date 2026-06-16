# Root Makefile — orchestrates backend and frontend builds/tests
# Usage:
#   make build        build both backend + frontend
#   make test         run all tests (backend -race + frontend vitest)
#   make dev          start both dev servers in parallel (requires tmux or just)
#   make clean        remove build artifacts

.PHONY: build build-backend build-frontend \
        test test-backend test-frontend test-frontend-e2e test-all \
        ci dev clean install

# ── Build ─────────────────────────────────────────────────────────────────────
build: build-backend build-frontend

build-backend:
	@echo "==> Building backend"
	cd backend && $(MAKE) build-linux

build-frontend:
	@echo "==> Building frontend"
	cd frontend && pnpm build

# ── Test ──────────────────────────────────────────────────────────────────────
# `make test`  runs unit/integration tests for both services (fast, no browser).
# `make test-all`  also runs frontend e2e (requires Chromium, slower).
# `make ci`  is the full CI gate: build + lint + test + e2e.
test: test-backend test-frontend

test-all: test-backend test-frontend test-frontend-e2e

test-backend:
	@echo "==> Testing backend"
	cd backend && go test -race ./internal/engine/... ./internal/cluster/...

test-frontend:
	@echo "==> Testing frontend (unit)"
	cd frontend && pnpm test

test-frontend-e2e: build-frontend
	@echo "==> Testing frontend (Playwright e2e)"
	cd frontend && pnpm exec playwright test --reporter=line

# Full CI gate: clean → build → lint → unit + e2e tests
ci: clean build lint test-all
	@echo "==> CI gate passed ✓"

# ── Lint / Vet ────────────────────────────────────────────────────────────────
lint: lint-backend

lint-backend:
	cd backend && go vet ./internal/engine/ ./internal/cluster/ ./cmd/linux/

# ── Clean ─────────────────────────────────────────────────────────────────────
clean:
	cd backend && $(MAKE) clean 2>/dev/null || true
	rm -rf frontend/.output frontend/.nuxt frontend/dist

# ── Install (Linux systemd) ───────────────────────────────────────────────────
install: build
	@echo "==> Installing systemd services (requires sudo)"
	sudo BACKEND_BIN=/usr/local/bin/netwatch \
	     FRONTEND_DIR=/opt/netwatch-ui \
	     ./deploy-systemd/install.sh

# ── Staging (prod-faithful 3-node cluster from RELEASED artifacts) ────────────
# The most realistic pre-prod test on a single machine: boots the published
# ghcr image + published UI bundle as a real 3-node cluster, then asserts the
# real failure modes via curl. Override the version with VERSION=0.1.7-rc.1
# (no leading "v" — it is the container tag; the release asset path is v$(VERSION)).
STAGING := deploy/staging/docker-compose.staging.yml
NODE    ?= netwatch-3
export VERSION ?= 0.1.6

.PHONY: staging-up staging-down staging-status staging-smoke staging-logs \
        staging-netem-partition staging-netem-clear

staging-up:
	docker compose -f $(STAGING) pull
	docker compose -f $(STAGING) up -d
	@echo "==> Waiting for cluster to converge…"; sleep 8
	@$(MAKE) --no-print-directory staging-status

staging-status:
	@curl -fsS localhost:10240/cluster/state 2>/dev/null \
	  | python3 -c "import sys,json; d=json.load(sys.stdin); print('alive members:', len(d['members'])); [print(' -', m['name'], m['status'], m.get('zone','')) for m in d['members']]" \
	  || echo "node-1 not ready yet — retry in a few seconds"

staging-smoke:
	deploy/staging/smoke.sh

staging-logs:
	docker compose -f $(STAGING) logs -f --tail=50

staging-netem-partition:
	deploy/staging/netem.sh $(NODE) partition

staging-netem-clear:
	deploy/staging/netem.sh $(NODE) clear

staging-down:
	docker compose -f $(STAGING) down -v
