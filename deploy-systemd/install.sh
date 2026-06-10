#!/usr/bin/env bash
# install.sh — Install the netwatch backend (systemd) + UI (nginx static)
#
# By default this downloads the prebuilt backend binary and frontend bundle from
# the GitHub Releases page — nothing is compiled on the target host.
#
# Usage:
#   sudo ./install.sh [--version vX.Y.Z] [--from-source]
#                     [--backend-bin PATH] [--frontend-dir PATH] [--no-enable]
#
#   --version vX.Y.Z  Install a specific release (default: latest)
#   --from-source     Use locally built artifacts instead of downloading:
#                       backend/bin/netwatch-linux-<arch>  (make build-linux)
#                       frontend/.output/public            (pnpm build)
#
# Requirements:
#   - systemd
#   - nginx   (serves the static UI; no Node.js runtime needed)
#   - curl + tar          (download mode, the default)
#   - Go + pnpm           (only for --from-source)

set -euo pipefail

REPO_SLUG="${REPO_SLUG:-saidtaylan/netwatch}"
VERSION="${VERSION:-latest}"
SOURCE=false

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
    --version)      VERSION="$2"; shift 2 ;;
    --from-source)  SOURCE=true; shift ;;
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

# release asset URL helper (latest vs pinned tag)
asset_url() {
  if [[ "$VERSION" == "latest" ]]; then
    echo "https://github.com/$REPO_SLUG/releases/latest/download/$1"
  else
    echo "https://github.com/$REPO_SLUG/releases/download/$VERSION/$1"
  fi
}

echo "==> Installing netwatch"
echo "    Source         : $([[ "$SOURCE" == true ]] && echo 'local build (--from-source)' || echo "GitHub Releases ($VERSION)")"
echo "    Architecture   : $ARCH"
echo "    Backend binary : $BACKEND_BIN"
echo "    Frontend dir   : $FRONTEND_DIR"

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
if [[ "$SOURCE" == true ]]; then
  BUILT_BIN="$REPO_ROOT/backend/bin/netwatch-linux-$ARCH"
  [[ -f "$BUILT_BIN" ]] || BUILT_BIN="$(ls "$REPO_ROOT"/backend/bin/netwatch-linux-* 2>/dev/null | head -1 || true)"
  if [[ -n "${BUILT_BIN:-}" && -f "$BUILT_BIN" ]]; then
    echo "==> Installing backend binary → $BACKEND_BIN  (local: $(basename "$BUILT_BIN"))"
    install -m 755 "$BUILT_BIN" "$BACKEND_BIN"
  else
    echo "ERROR: --from-source set but no binary in $REPO_ROOT/backend/bin/."
    echo "       Run:  cd backend && make build-linux [GOARCH=arm64]"; exit 1
  fi
else
  echo "==> Downloading backend binary → $BACKEND_BIN  (netwatch-linux-$ARCH)"
  curl -fSL --retry 3 -o "$BACKEND_BIN" "$(asset_url "netwatch-linux-$ARCH")"
  chmod 755 "$BACKEND_BIN"
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

# ── Frontend static files → $FRONTEND_DIR ────────────────────────────────────
mkdir -p "$FRONTEND_DIR"
if [[ "$SOURCE" == true ]]; then
  FRONTEND_OUTPUT="$REPO_ROOT/frontend/.output/public"
  if [[ -d "$FRONTEND_OUTPUT" ]]; then
    echo "==> Installing UI static files → $FRONTEND_DIR  (local build)"
    cp -r "$FRONTEND_OUTPUT/." "$FRONTEND_DIR/"
  else
    echo "ERROR: --from-source set but $FRONTEND_OUTPUT missing. Run: cd frontend && pnpm build"; exit 1
  fi
else
  echo "==> Downloading UI bundle → $FRONTEND_DIR  (netwatch-frontend.tar.gz)"
  tmp="$(mktemp)"
  curl -fSL --retry 3 -o "$tmp" "$(asset_url netwatch-frontend.tar.gz)"
  tar -xzf "$tmp" -C "$FRONTEND_DIR"
  rm -f "$tmp"
fi

# ── nginx site ───────────────────────────────────────────────────────────────
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
