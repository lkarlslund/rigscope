#!/usr/bin/env bash
set -euo pipefail

INSTALL_DIR="${RIGSCOPE_INSTALL_DIR:-/opt/rigscope}"
USER_NAME="${RIGSCOPE_USER:-rigscope}"
GROUP_NAME="${RIGSCOPE_GROUP:-rigscope}"
SERVICE_NAME="${RIGSCOPE_SERVICE_NAME:-rigscope.service}"
REMOVE_DATA="${RIGSCOPE_REMOVE_DATA:-0}"

if [[ "$(id -u)" -ne 0 ]]; then
  echo "uninstall.sh must be run as root" >&2
  exit 1
fi

if [[ "$(uname -s)" != "Linux" ]]; then
  echo "uninstall.sh supports Linux only" >&2
  exit 1
fi

if command -v systemctl >/dev/null 2>&1; then
  systemctl disable --now "$SERVICE_NAME" >/dev/null 2>&1 || true
  rm -f "/etc/systemd/system/${SERVICE_NAME}"
  systemctl daemon-reload
fi

rm -f "${INSTALL_DIR}/rigscope"

if [[ "$REMOVE_DATA" == "1" ]]; then
  rm -rf "$INSTALL_DIR"
  if id -u "$USER_NAME" >/dev/null 2>&1; then
    userdel "$USER_NAME" >/dev/null 2>&1 || true
  fi
  if getent group "$GROUP_NAME" >/dev/null; then
    groupdel "$GROUP_NAME" >/dev/null 2>&1 || true
  fi
else
  echo "left ${INSTALL_DIR}/data in place"
  echo "set RIGSCOPE_REMOVE_DATA=1 to remove data and the ${USER_NAME} user/group"
fi

echo "rigscope service removed"
