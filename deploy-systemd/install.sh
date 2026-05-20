#!/usr/bin/env bash
# install.sh — Install netwatch backend + frontend as systemd services
#
# Usage:
#   sudo ./install.sh [--backend-bin PATH] [--frontend-dir PATH] [--no-enable]
#
# Defaults:
#   --backend-bin  /usr/local/bin/netwatch
#   --frontend-dir /opt/netwatch-ui
#   --config-dir   /etc/netwatch
#   --state-dir    /var/lib/netwatch
#
# Requirements:
#   - systemd
#   - Node.js ≥ 20 (for netwatch-frontend)
#   - Built frontend: frontend/.output/  (run `pnpm build` in frontend/)
#   - Built backend binary (run `make build-linux` in backend/)

set -euo pipefail

BACKEND_BIN="${BACKEND_BIN:-/usr/local/bin/netwatch}"
FRONTEND_DIR="${FRONTEND_DIR:-/opt/netwatch-ui}"
CONFIG_DIR="${CONFIG_DIR:-/etc/netwatch}"
STATE_DIR="${STATE_DIR:-/var/lib/netwatch}"
ENABLE="${ENABLE:-true}"

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"

# ── Parse args ───────────────────────────────────────────────────────────────
while [[ $# -gt 0 ]]; do
  case "$1" in
    --backend-bin)  BACKEND_BIN="$2"; shift 2 ;;
    --frontend-dir) FRONTEND_DIR="$2"; shift 2 ;;
    --no-enable)    ENABLE=false; shift ;;
    *) echo "Unknown option: $1"; exit 1 ;;
  esac
done

echo "==> Installing netwatch"
echo "    Backend binary : $BACKEND_BIN"
echo "    Frontend dir   : $FRONTEND_DIR"
echo "    Config dir     : $CONFIG_DIR"
echo "    State dir      : $STATE_DIR"

# ── Create user ───────────────────────────────────────────────────────────────
if ! id netwatch &>/dev/null; then
  echo "==> Creating 'netwatch' system user"
  useradd --system --no-create-home --shell /usr/sbin/nologin netwatch
fi

# ── Directories ───────────────────────────────────────────────────────────────
mkdir -p "$CONFIG_DIR" "$STATE_DIR"
chown netwatch:netwatch "$CONFIG_DIR" "$STATE_DIR"
chmod 750 "$CONFIG_DIR" "$STATE_DIR"

# ── Backend binary ────────────────────────────────────────────────────────────
BUILT_BIN="$REPO_ROOT/backend/bin/netwatch-linux-amd64"
if [[ -f "$BUILT_BIN" ]]; then
  echo "==> Installing backend binary → $BACKEND_BIN"
  install -m 755 "$BUILT_BIN" "$BACKEND_BIN"
else
  echo "WARN: Built binary not found at $BUILT_BIN"
  echo "      Run:  cd backend && make build-linux"
fi

# ── Config skeleton ───────────────────────────────────────────────────────────
if [[ ! -f "$CONFIG_DIR/config.yaml" ]]; then
  echo "==> Copying example config → $CONFIG_DIR/config.yaml"
  cp "$REPO_ROOT/backend/config.example.yaml" "$CONFIG_DIR/config.yaml"
  echo "    Edit $CONFIG_DIR/config.yaml before starting the service."
fi

# ── Frontend ──────────────────────────────────────────────────────────────────
FRONTEND_OUTPUT="$REPO_ROOT/frontend/.output"
if [[ -d "$FRONTEND_OUTPUT" ]]; then
  echo "==> Installing frontend → $FRONTEND_DIR"
  mkdir -p "$FRONTEND_DIR"
  cp -r "$FRONTEND_OUTPUT/." "$FRONTEND_DIR/.output/"
  chown -R netwatch:netwatch "$FRONTEND_DIR"
else
  echo "WARN: Frontend build not found at $FRONTEND_OUTPUT"
  echo "      Run:  cd frontend && pnpm build"
fi

# ── systemd units ─────────────────────────────────────────────────────────────
echo "==> Installing systemd units"
install -m 644 "$SCRIPT_DIR/netwatch-backend.service"  /etc/systemd/system/
install -m 644 "$SCRIPT_DIR/netwatch-frontend.service" /etc/systemd/system/
install -m 644 "$SCRIPT_DIR/netwatch.target"           /etc/systemd/system/

systemctl daemon-reload

if [[ "$ENABLE" == "true" ]]; then
  echo "==> Enabling and starting netwatch.target"
  systemctl enable netwatch-backend.service netwatch-frontend.service netwatch.target
  systemctl start  netwatch.target
  echo ""
  echo "    Status:  systemctl status netwatch.target"
  echo "    Logs:    journalctl -u netwatch-backend -u netwatch-frontend -f"
else
  echo "==> Units installed but not enabled (--no-enable)"
  echo "    To enable: systemctl enable --now netwatch.target"
fi

echo ""
echo "==> Done."
echo "    Backend  : http://localhost:10240/health"
echo "    Frontend : http://localhost:3000"
