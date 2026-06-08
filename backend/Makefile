## ─────────────────────────────────────────────────────────────────────────────
## netwatch — Makefile
## Usage: make <target>
## ─────────────────────────────────────────────────────────────────────────────

# Override at invocation:
#   make build-linux BINARY_NAME=myagent
#   make build-linux GOARCH=arm64     # for arm64 servers (Graviton, Pi, Ampere)
BINARY_NAME   ?= netwatch
GOARCH        ?= amd64
MODULE        := github.com/saidtaylan/netwatch
GO_VERSION    := $(shell go version | awk '{print $$3}')

LDFLAGS       := -s -w -X $(MODULE)/internal/engine.BinaryName=$(BINARY_NAME)
BUILD_FLAGS   := -ldflags="$(LDFLAGS)" -trimpath

LINUX_BIN     := bin/$(BINARY_NAME)-linux-$(GOARCH)
DARWIN_BIN    := bin/$(BINARY_NAME)-darwin
WINDOWS_BIN   := bin/$(BINARY_NAME)-windows-amd64.exe
DOCKER_IMAGE  := $(BINARY_NAME):latest

## ─────────────────────────────────────────────────────────────────────────────
## Build
## ─────────────────────────────────────────────────────────────────────────────

.PHONY: build-linux
build-linux:                          ## Build Linux binary (static, no CGO; GOARCH=amd64|arm64)
	@mkdir -p bin
	CGO_ENABLED=0 GOOS=linux GOARCH=$(GOARCH) \
	    go build $(BUILD_FLAGS) -o $(LINUX_BIN) ./cmd/linux/
	@echo "→ $(LINUX_BIN)"

.PHONY: build-darwin
build-darwin:                         ## Build macOS binary (native platform, for local testing)
	@mkdir -p bin
	go build $(BUILD_FLAGS) -o $(DARWIN_BIN) ./cmd/linux/
	@echo "→ $(DARWIN_BIN)"

.PHONY: build-windows
build-windows:                        ## Build Windows amd64 binary
	@mkdir -p bin
	CGO_ENABLED=0 GOOS=windows GOARCH=amd64 \
	    go build $(BUILD_FLAGS) -o $(WINDOWS_BIN) ./cmd/windows/
	@echo "→ $(WINDOWS_BIN)"

.PHONY: build
build: build-linux build-windows      ## Build both Linux and Windows binaries

## ─────────────────────────────────────────────────────────────────────────────
## Test
## ─────────────────────────────────────────────────────────────────────────────

.PHONY: test
test:                                 ## Run internal unit tests with race detector
	go test -race ./internal/engine/... ./internal/cluster/...

.PHONY: test-unit
test-unit:                            ## Run tests/engine + tests/cluster (fast, no I/O)
	go test -race -timeout 60s -count=1 ./tests/engine/... ./tests/cluster/...

.PHONY: test-domain
test-domain:                          ## Run tests/domain (full-stack, ~90s, uses real ports)
	go test -race -timeout 200s -count=1 ./tests/domain/...

.PHONY: test-integration
test-integration:                     ## Run integration tests (requires docker / local ports)
	go test -race -tags integration -timeout 120s ./test/integration/...

.PHONY: test-all
test-all: test test-unit test-domain  ## Run all test suites (internal + tests/ + integration)
	@echo "✓ All test suites passed."

## ─────────────────────────────────────────────────────────────────────────────
## Quality
## ─────────────────────────────────────────────────────────────────────────────

.PHONY: vet
vet:                                  ## Run go vet
	go vet ./internal/engine/ ./internal/cluster/ ./cmd/linux/

.PHONY: lint
lint:                                 ## Run golangci-lint (must be installed)
	golangci-lint run ./...

## ─────────────────────────────────────────────────────────────────────────────
## Docker
## ─────────────────────────────────────────────────────────────────────────────

.PHONY: docker-build
docker-build:                         ## Build Docker image
	docker build \
	    --build-arg GO_VERSION=1.24 \
	    --build-arg BINARY_NAME=$(BINARY_NAME) \
	    -t $(DOCKER_IMAGE) .

.PHONY: docker-push
docker-push:                          ## Push Docker image (set DOCKER_IMAGE=registry/repo:tag)
	docker push $(DOCKER_IMAGE)

## ─────────────────────────────────────────────────────────────────────────────
## Install (Linux — requires root)
## ─────────────────────────────────────────────────────────────────────────────

INSTALL_DIR  := /usr/local/bin
SERVICE_NAME := $(BINARY_NAME)

.PHONY: install
install: build-linux                  ## Install binary + systemd unit (Linux, requires root)
	install -m 755 $(LINUX_BIN) $(INSTALL_DIR)/$(BINARY_NAME)
	@if [ -d /etc/systemd/system ]; then \
	    install -m 644 deploy/$(BINARY_NAME).service /etc/systemd/system/$(SERVICE_NAME).service; \
	    systemctl daemon-reload; \
	    echo "→ systemd unit installed. Run: systemctl enable --now $(SERVICE_NAME)"; \
	else \
	    echo "→ Binary installed to $(INSTALL_DIR)/$(BINARY_NAME) (no systemd found)"; \
	fi

.PHONY: uninstall
uninstall:                            ## Remove binary + systemd unit (Linux, requires root)
	@if [ -d /etc/systemd/system ] && [ -f /etc/systemd/system/$(SERVICE_NAME).service ]; then \
	    systemctl stop $(SERVICE_NAME) || true; \
	    systemctl disable $(SERVICE_NAME) || true; \
	    rm -f /etc/systemd/system/$(SERVICE_NAME).service; \
	    systemctl daemon-reload; \
	fi
	rm -f $(INSTALL_DIR)/$(BINARY_NAME)
	@echo "→ $(BINARY_NAME) uninstalled"

## ─────────────────────────────────────────────────────────────────────────────
## All
## ─────────────────────────────────────────────────────────────────────────────

.PHONY: all
all: build test vet                   ## Build + test + vet

.PHONY: clean
clean:                                ## Remove build artifacts
	rm -rf bin/

.PHONY: help
help:                                 ## Show this help
	@grep -E '^[a-zA-Z_-]+:.*?##' $(MAKEFILE_LIST) | \
	    awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-22s\033[0m %s\n", $$1, $$2}'

.DEFAULT_GOAL := help
