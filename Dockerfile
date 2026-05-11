# ── Stage 1: Build ────────────────────────────────────────────────────────────
# Update GO_VERSION to match the `go` directive in go.mod.
ARG GO_VERSION=1.24
ARG BINARY_NAME=netwatch

FROM golang:${GO_VERSION}-alpine AS builder

ARG BINARY_NAME
WORKDIR /src

# Download dependencies in a separate layer for better cache utilisation.
COPY go.mod go.sum ./
RUN go mod download

# Copy source and build a statically linked, stripped binary.
# BinaryName is embedded at link time so CLI output uses the correct name.
COPY . .
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
    go build \
    -ldflags="-s -w -X github.com/saidtaylan/netwatch/internal/engine.BinaryName=${BINARY_NAME}" \
    -trimpath \
    -o /out/${BINARY_NAME} ./cmd/linux/

# ── Stage 2: Run ──────────────────────────────────────────────────────────────
# gcr.io/distroless/static-debian12:nonroot runs as uid 65532 (no shell, no pm).
# Alternatives:
#   :latest     — same image, runs as root (not recommended for production)
#   alpine:3.21 — has a shell for easier debugging
FROM gcr.io/distroless/static-debian12:nonroot

ARG BINARY_NAME=netwatch

# Copy notification scripts.
# Mount your custom scripts here: -v /host/notifications:/notifications
COPY --from=builder /src/notifications /notifications

COPY --from=builder /out/${BINARY_NAME} /netwatch

# CONFIG_PATH: fulfilled by a ConfigMap volume mount in Kubernetes,
# or a bind-mount on plain Docker.
ENV CONFIG_PATH=/etc/netwatch/config.yaml

# Prometheus metrics + health + status + cluster endpoints
EXPOSE 10240

# Gossip cluster port (TCP + UDP)
EXPOSE 7946

ENTRYPOINT ["/netwatch"]
CMD ["--config", "/etc/netwatch/config.yaml"]
