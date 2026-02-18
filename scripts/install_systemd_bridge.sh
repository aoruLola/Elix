#!/usr/bin/env bash
set -euo pipefail

require_cmd() {
  if ! command -v "$1" >/dev/null 2>&1; then
    echo "missing required command: $1" >&2
    exit 2
  fi
}

require_cmd install
require_cmd systemctl
require_cmd make

if [[ "${EUID:-$(id -u)}" -ne 0 ]]; then
  echo "this script must run as root (use sudo)" >&2
  exit 2
fi

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
INSTALL_DIR="${INSTALL_DIR:-/opt/echohelix}"
SERVICE_NAME="${SERVICE_NAME:-elix-bridge}"
RUN_USER="${RUN_USER:-root}"
RUN_GROUP="${RUN_GROUP:-$RUN_USER}"
ENV_DIR="${ENV_DIR:-/etc/echohelix}"
ENV_FILE="${ENV_FILE:-$ENV_DIR/elix-bridge.env}"
UNIT_PATH="/etc/systemd/system/${SERVICE_NAME}.service"

echo "building binaries..."
(
  cd "$ROOT_DIR"
  make build
)

echo "installing binaries into $INSTALL_DIR/bin ..."
install -d -m 0755 "$INSTALL_DIR/bin"
install -m 0755 "$ROOT_DIR/elix-bridge" "$INSTALL_DIR/bin/elix-bridge"
install -m 0755 "$ROOT_DIR/codex-adapter" "$INSTALL_DIR/bin/codex-adapter"
install -m 0755 "$ROOT_DIR/gemini-adapter" "$INSTALL_DIR/bin/gemini-adapter"
install -m 0755 "$ROOT_DIR/claude-adapter" "$INSTALL_DIR/bin/claude-adapter"
install -m 0755 "$ROOT_DIR/elix-wallet" "$INSTALL_DIR/bin/elix-wallet"

echo "installing environment file at $ENV_FILE ..."
install -d -m 0755 "$ENV_DIR"
if [[ ! -f "$ENV_FILE" ]]; then
  install -m 0644 "$ROOT_DIR/deploy/systemd/elix-bridge.env.example" "$ENV_FILE"
  echo "created $ENV_FILE from template; update secrets before production use"
fi

tmp_unit="$(mktemp)"
trap 'rm -f "$tmp_unit"' EXIT
sed \
  -e "s#__INSTALL_DIR__#$INSTALL_DIR#g" \
  -e "s#__RUN_USER__#$RUN_USER#g" \
  -e "s#__RUN_GROUP__#$RUN_GROUP#g" \
  "$ROOT_DIR/deploy/systemd/elix-bridge.service" >"$tmp_unit"
install -m 0644 "$tmp_unit" "$UNIT_PATH"

echo "reloading and enabling service $SERVICE_NAME ..."
systemctl daemon-reload
systemctl enable --now "$SERVICE_NAME"

echo
echo "installed successfully"
echo "- service: $SERVICE_NAME"
echo "- unit:    $UNIT_PATH"
echo "- env:     $ENV_FILE"
echo
echo "useful commands:"
echo "  systemctl status $SERVICE_NAME"
echo "  journalctl -u $SERVICE_NAME -f"
