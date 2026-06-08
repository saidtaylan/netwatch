#!/usr/bin/env bash
# install.sh — Install the netwatch backend (systemd) + UI (nginx static)
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
#   - nginx              (serves the static UI; no Node.js runtime needed)
#   - Built backend binary:  cd backend && make build-linux       (GOARCH=arm64 for arm)
#   - Built frontend:        cd frontend && pnpm build            (emits .output/public/)

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

# ── Detect target architecture (amd64 / arm64) ───────────────────────────────
case "$(uname -m)" in
  x86_64|amd64)   ARCH=amd64 ;;
  aarch64|arm64)  ARCH=arm64 ;;
  *) echo "WARN: unknown architecture '$(uname -m)', defaulting to amd64"; ARCH=amd64 ;;
esac

echo "==> Installing netwatch"
echo "    Architecture   : $ARCH"
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

# ── Backend binary (arch-aware; falls back to any built linux binary) ─────────
BUILT_BIN="$REPO_ROOT/backend/bin/netwatch-linux-$ARCH"
if [[ ! -f "$BUILT_BIN" ]]; then
  BUILT_BIN="$(ls "$REPO_ROOT"/backend/bin/netwatch-linux-* 2>/dev/null | head -1 || true)"
fi
if [[ -n "${BUILT_BIN:-}" && -f "$BUILT_BIN" ]]; then
  echo "==> Installing backend binary → $BACKEND_BIN  (from $(basename "$BUILT_BIN"))"
  install -m 755 "$BUILT_BIN" "$BACKEND_BIN"
else
  echo "WARN: No built binary found in $REPO_ROOT/backend/bin/"
  echo "      Run:  cd backend && make build-linux            # amd64"
  echo "      or:   cd backend && make build-linux GOARCH=arm64"
fi

# ── Config (minimal runnable skeleton; full reference: config.example.yaml) ───
if [[ ! -f "$CONFIG_DIR/config.yaml" ]]; then
  echo "==> Copying starter config → $CONFIG_DIR/config.yaml"
  cp "$REPO_ROOT/backend/config.skeleton.yaml" "$CONFIG_DIR/config.yaml"
  chown netwatch:netwatch "$CONFIG_DIR/config.yaml"
  echo "    Set a real admin.setup_token and add targets before/after first start."
  echo "    Full reference: $REPO_ROOT/backend/config.example.yaml"
fi

# ── systemd unit (backend only; UI is served by nginx) ───────────────────────
echo "==> Installing systemd unit"
install -m 644 "$SCRIPT_DIR/netwatch-backend.service" /etc/systemd/system/
install -m 644 "$SCRIPT_DIR/netwatch.target"          /etc/systemd/system/
systemctl daemon-reload

# ── Frontend (static files served by nginx) ──────────────────────────────────
FRONTEND_OUTPUT="$REPO_ROOT/frontend/.output/public"
if [[ -d "$FRONTEND_OUTPUT" ]]; then
  echo "==> Installing UI static files → $FRONTEND_DIR"
  mkdir -p "$FRONTEND_DIR"
  cp -r "$FRONTEND_OUTPUT/." "$FRONTEND_DIR/"

  if command -v nginx &>/dev/null; then
    echo "==> Configuring nginx site"
    if [[ -d /etc/nginx/sites-available ]]; then            # Debian/Ubuntu
      install -m 644 "$SCRIPT_DIR/netwatch-ui.nginx.conf" /etc/nginx/sites-available/netwatch
      ln -sf /etc/nginx/sites-available/netwatch /etc/nginx/sites-enabled/netwatch
      rm -f /etc/nginx/sites-enabled/default
    else                                                    # RHEL/CentOS
      install -m 644 "$SCRIPT_DIR/netwatch-ui.nginx.conf" /etc/nginx/conf.d/netwatch.conf
    fi
    if nginx -t; then
      systemctl enable nginx >/dev/null 2>&1 || true
      systemctl reload nginx 2>/dev/null || systemctl restart nginx
      echo "    UI available on http://<host>/  (port 80)"
    else
      echo "WARN: nginx config test failed — review /etc/nginx and reload manually."
    fi
  else
    echo "WARN: nginx not installed — install it, then re-run this script."
    echo "      The static UI is in $FRONTEND_DIR; point nginx 'root' there with"
    echo "      an SPA fallback (try_files \$uri \$uri/ /index.html;)."
  fi
else
  echo "WARN: Frontend build not found at $FRONTEND_OUTPUT"
  echo "      Run:  cd frontend && pnpm build"
fi

# ── Enable + start backend ───────────────────────────────────────────────────
if [[ "$ENABLE" == "true" ]]; then
  echo "==> Enabling and starting netwatch.target"
  systemctl enable netwatch-backend.service netwatch.target
  systemctl start  netwatch.target
  echo ""
  echo "    Status:  systemctl status netwatch.target"
  echo "    Logs:    journalctl -u netwatch-backend -f"
else
  echo "==> Units installed but not enabled (--no-enable)"
  echo "    To enable: systemctl enable --now netwatch.target"
fi

echo ""
echo "==> Done."
echo "    Backend  : http://localhost:10240/health"
echo "    UI       : http://localhost/        (nginx, port 80)"
