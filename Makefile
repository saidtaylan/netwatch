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
