#!/usr/bin/env bash
set -euo pipefail

repo_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)"
output_path="${1:-$repo_dir/.tmp/rigscope-tui.png}"
capture_delay="${RIGSCOPE_CAPTURE_DELAY:-1}"

if [[ "$output_path" != /* ]]; then
  output_path="$PWD/$output_path"
fi

# Re-enter on a private D-Bus session. The compositor created below has no
# connection to the user's desktop, so capturing its active window is safe.
if [[ "${RIGSCOPE_HEADLESS_CAPTURE:-}" != 1 ]]; then
  mkdir -p -- "$(dirname -- "$output_path")"
  go test ./...
  session_log="$(dirname -- "$output_path")/rigscope-tui-session.log"
  exec env RIGSCOPE_HEADLESS_CAPTURE=1 \
    RIGSCOPE_CAPTURE_DELAY="$capture_delay" \
    dbus-run-session -- "$0" "$output_path" 2>"$session_log"
fi

tmp_dir="$(mktemp -d)"
runtime_dir="$tmp_dir/runtime"
socket_name="rigscope-test-wayland"
kitty_socket="$tmp_dir/kitty.sock"
kwin_pid=""
kitty_pid=""

cleanup() {
  if [[ -S "$kitty_socket" ]]; then
    kitty @ --to "unix:$kitty_socket" close-window --match all >/dev/null 2>&1 || true
  fi
  [[ -z "$kitty_pid" ]] || kill "$kitty_pid" >/dev/null 2>&1 || true
  [[ -z "$kwin_pid" ]] || kill "$kwin_pid" >/dev/null 2>&1 || true
  [[ -z "$kitty_pid" ]] || wait "$kitty_pid" 2>/dev/null || true
  [[ -z "$kwin_pid" ]] || wait "$kwin_pid" 2>/dev/null || true
  rm -rf -- "$tmp_dir"
}
trap cleanup EXIT

mkdir -m 700 -- "$runtime_dir"
export XDG_RUNTIME_DIR="$runtime_dir"
export XDG_SESSION_TYPE=wayland
export WAYLAND_DISPLAY="$socket_name"
export QT_QPA_PLATFORM=wayland
unset DISPLAY
unset NO_COLOR

go build -o "$tmp_dir/rigscope" ./cmd/rigscope

kwin_wayland \
  --virtual \
  --width 1280 \
  --height 800 \
  --socket "$socket_name" \
  --no-lockscreen \
  --no-global-shortcuts \
  >"$tmp_dir/kwin.log" 2>&1 &
kwin_pid=$!

for _ in $(seq 1 100); do
  [[ -S "$runtime_dir/$socket_name" ]] && break
  kill -0 "$kwin_pid" 2>/dev/null || {
    cat "$tmp_dir/kwin.log" >&2
    exit 1
  }
  sleep 0.05
done
[[ -S "$runtime_dir/$socket_name" ]] || {
  printf 'Headless KWin did not become ready\n' >&2
  exit 1
}

kitty \
  --config NONE \
  --class rigscope-visual-test \
  --name rigscope-visual-test \
  --title rigscope-visual-test \
  --listen-on "unix:$kitty_socket" \
  -o allow_remote_control=yes \
  -o initial_window_width=1280 \
  -o initial_window_height=800 \
  -o remember_window_size=no \
  "$tmp_dir/rigscope" \
  >"$tmp_dir/kitty.log" 2>&1 &
kitty_pid=$!

kitty_ready=false
for _ in $(seq 1 100); do
  if kitty @ --to "unix:$kitty_socket" ls >/dev/null 2>&1; then
    kitty_ready=true
    break
  fi
  kill -0 "$kitty_pid" 2>/dev/null || {
    cat "$tmp_dir/kitty.log" >&2
    exit 1
  }
  sleep 0.05
done

if [[ "$kitty_ready" != true ]]; then
  printf 'Kitty did not become ready\n' >&2
  exit 1
fi

sleep "$capture_delay"
spectacle --activewindow --background --nonotify --output "$output_path" --delay 0

printf '%s\n' "$output_path"
