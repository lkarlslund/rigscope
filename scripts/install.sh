#!/usr/bin/env bash
set -euo pipefail

REPO="${RIGSCOPE_REPO:-lkarlslund/rigscope}"
INSTALL_DIR="${RIGSCOPE_INSTALL_DIR:-/opt/rigscope}"
DATA_DIR="${RIGSCOPE_DATA_DIR:-${INSTALL_DIR}/data}"
USER_NAME="${RIGSCOPE_USER:-rigscope}"
GROUP_NAME="${RIGSCOPE_GROUP:-rigscope}"
SERVICE_NAME="${RIGSCOPE_SERVICE_NAME:-rigscope.service}"
VERSION="${RIGSCOPE_VERSION:-latest}"

if [[ "$(id -u)" -ne 0 ]]; then
  echo "install.sh must be run as root" >&2
  exit 1
fi

if [[ "$(uname -s)" != "Linux" ]]; then
  echo "install.sh supports Linux only" >&2
  exit 1
fi

arch="$(uname -m)"
case "$arch" in
  x86_64|amd64)
    asset_arch="amd64"
    ;;
  *)
    echo "unsupported architecture: $arch (only linux amd64 release assets are available)" >&2
    exit 1
    ;;
esac

if ! command -v systemctl >/dev/null 2>&1; then
  echo "systemctl is required" >&2
  exit 1
fi

download() {
  local url="$1"
  local out="$2"
  if command -v curl >/dev/null 2>&1; then
    curl --fail --location --silent --show-error "$url" --output "$out"
  elif command -v wget >/dev/null 2>&1; then
    wget -qO "$out" "$url"
  else
    echo "curl or wget is required" >&2
    exit 1
  fi
}

latest_tag() {
  local api="https://api.github.com/repos/${REPO}/releases/latest"
  if command -v curl >/dev/null 2>&1; then
    curl --fail --location --silent --show-error "$api"
  elif command -v wget >/dev/null 2>&1; then
    wget -qO- "$api"
  else
    echo "curl or wget is required" >&2
    exit 1
  fi | sed -n 's/.*"tag_name"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' | head -n 1
}

if [[ "$VERSION" == "latest" ]]; then
  VERSION="$(latest_tag)"
fi
if [[ -z "$VERSION" ]]; then
  echo "could not determine release version" >&2
  exit 1
fi

asset="rigscope-${VERSION}-linux-${asset_arch}"
binary_url="${RIGSCOPE_BINARY_URL:-https://github.com/${REPO}/releases/download/${VERSION}/${asset}}"
checksum_url="${RIGSCOPE_SHA256_URL:-${binary_url}.sha256}"

tmpdir="$(mktemp -d)"
trap 'rm -rf "$tmpdir"' EXIT

echo "downloading ${binary_url}"
download "$binary_url" "${tmpdir}/rigscope"
if [[ "${RIGSCOPE_SKIP_CHECKSUM:-0}" != "1" ]]; then
  echo "verifying checksum"
  download "$checksum_url" "${tmpdir}/rigscope.sha256"
  want_hash="$(awk '{print $1; exit}' "${tmpdir}/rigscope.sha256")"
  got_hash="$(sha256sum "${tmpdir}/rigscope" | awk '{print $1}')"
  if [[ -z "$want_hash" || "$got_hash" != "$want_hash" ]]; then
    echo "checksum mismatch for ${binary_url}" >&2
    exit 1
  fi
fi
chmod 0755 "${tmpdir}/rigscope"

if ! getent group "$GROUP_NAME" >/dev/null; then
  groupadd --system "$GROUP_NAME"
fi

if ! id -u "$USER_NAME" >/dev/null 2>&1; then
  useradd --system --gid "$GROUP_NAME" --home-dir "$INSTALL_DIR" --shell /usr/sbin/nologin "$USER_NAME"
fi

install -d -m 0755 -o root -g root "$INSTALL_DIR"
install -d -m 0755 -o "$USER_NAME" -g "$GROUP_NAME" "$DATA_DIR"
install -m 0755 -o root -g root "${tmpdir}/rigscope" "${INSTALL_DIR}/rigscope"

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
service_template="${script_dir}/../systemd/rigscope.service"
if [[ -f "$service_template" ]]; then
  install -m 0644 -o root -g root "$service_template" "/etc/systemd/system/${SERVICE_NAME}"
else
  cat >"/etc/systemd/system/${SERVICE_NAME}" <<SERVICE
[Unit]
Description=rigscope telemetry daemon
Documentation=https://github.com/${REPO}
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
User=${USER_NAME}
Group=${GROUP_NAME}
WorkingDirectory=${INSTALL_DIR}
ExecStart=${INSTALL_DIR}/rigscope serve --addr 0.0.0.0:7077 --data-dir ${DATA_DIR} --interval 1s --retention 0 --log-level info
Restart=on-failure
RestartSec=3
NoNewPrivileges=true
PrivateTmp=true
ProtectHome=true
ReadWritePaths=${INSTALL_DIR}

[Install]
WantedBy=multi-user.target
SERVICE
fi

systemctl daemon-reload
systemctl enable --now "$SERVICE_NAME"

echo "rigscope installed to ${INSTALL_DIR}"
echo "data directory: ${DATA_DIR}"
echo "service: ${SERVICE_NAME}"
echo "dashboard: http://127.0.0.1:7077"
