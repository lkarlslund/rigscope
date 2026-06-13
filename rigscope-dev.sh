#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
cd "$ROOT_DIR"

POLL_SECONDS="${RIGSCOPE_DEV_POLL_SECONDS:-1}"
SETTLE_SECONDS="${RIGSCOPE_DEV_SETTLE_SECONDS:-2}"
STOP_GRACE_SECONDS="${RIGSCOPE_DEV_STOP_GRACE_SECONDS:-5}"
STOP_TIMEOUT_SECONDS="${RIGSCOPE_DEV_STOP_TIMEOUT_SECONDS:-15}"
BIN="${RIGSCOPE_DEV_BIN:-/tmp/rigscope-dev-${USER:-user}/rigscope}"
LOG="${RIGSCOPE_DEV_LOG:-/tmp/rigscope-dev-${USER:-user}/rigscope.log}"
PID_FILE="${RIGSCOPE_DEV_PID_FILE:-/tmp/rigscope.pid}"
ADDR="${RIGSCOPE_DEV_ADDR:-127.0.0.1:7077}"
DATA_DIR="${RIGSCOPE_DEV_DATA_DIR:-$ROOT_DIR/data-dev}"
INTERVAL="${RIGSCOPE_DEV_INTERVAL:-1s}"
RETENTION="${RIGSCOPE_DEV_RETENTION:-0}"

child_pid=""
expected_version=""

mkdir -p "$(dirname "$BIN")" "$(dirname "$LOG")" "$(dirname "$PID_FILE")"

log() {
  printf '[%s] %s\n' "$(date '+%Y-%m-%d %H:%M:%S')" "$*"
}

build_url() {
  local addr="${ADDR#http://}"
  addr="${addr#https://}"
  local host="${addr%:*}"
  local port="${addr##*:}"
  if [[ "$host" == "$addr" ]]; then
    host="127.0.0.1"
    port="$addr"
  fi
  case "$host" in
    ""|"0.0.0.0"|"::"|"[::]")
      host="127.0.0.1"
      ;;
  esac
  printf 'http://%s:%s/api/build\n' "$host" "$port"
}

source_signature() {
  (
    # Keep embedded UI assets in the rebuild signature.
    git ls-files -z --cached --others --exclude-standard -- \
      'cmd/**/*.go' \
      'internal/**/*.go' \
      'internal/web/static/**' \
      'go.mod' \
      'go.sum' \
      'rigscope-dev.sh' 2>/dev/null \
      | grep -z -vE '(^|/)[^/]+_test\.go$' \
      | sort -z \
      | xargs -0 -r sha256sum
  ) | sha256sum | awk '{print $1}'
}

wait_for_settle() {
  local previous current quiet
  previous="$(source_signature)"
  quiet=0
  while (( quiet < SETTLE_SECONDS )); do
    sleep 1
    current="$(source_signature)"
    if [[ "$current" == "$previous" ]]; then
      quiet=$((quiet + 1))
    else
      previous="$current"
      quiet=0
    fi
  done
}

version_value_from_json() {
  sed -n 's/.*"version"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p'
}

build_rigscope() {
  local commit built_at hash
  commit="$(git rev-parse --short=12 HEAD 2>/dev/null || printf 'unknown')"
  built_at="$(date -u '+%Y-%m-%dT%H:%M:%SZ')"
  hash="$(source_signature | cut -c1-12)"
  expected_version="dev-${built_at}-${hash}"
  log "building rigscope version=$expected_version"
  go build \
    -ldflags "-X github.com/lkarlslund/rigscope/internal/buildinfo.Version=$expected_version -X github.com/lkarlslund/rigscope/internal/buildinfo.Commit=$commit -X github.com/lkarlslund/rigscope/internal/buildinfo.BuiltAt=$built_at" \
    -o "$BIN" \
    ./cmd/rigscope
}

launch_rigscope() {
  : >"$LOG"
  "$BIN" serve \
    --addr "$ADDR" \
    --data-dir "$DATA_DIR" \
    --interval "$INTERVAL" \
    --retention "$RETENTION" \
    "$@" > >(sed -u 's/^/[rigscope] /' | tee -a "$LOG") 2>&1 &
  child_pid="$!"
  printf '%s\n' "$child_pid" >"$PID_FILE"
  log "launched rigscope pid=$child_pid"
  log "log: $LOG"
}

stop_rigscope() {
  local pid="$1"
  local waited=0
  if [[ -z "$pid" ]] || ! kill -0 "$pid" 2>/dev/null; then
    wait "$pid" 2>/dev/null || true
    return 0
  fi
  log "stopping rigscope pid=$pid"
  kill -TERM "$pid" 2>/dev/null || true
  while kill -0 "$pid" 2>/dev/null; do
    if (( waited >= STOP_GRACE_SECONDS )); then
      break
    fi
    sleep 1
    waited=$((waited + 1))
  done
  if kill -0 "$pid" 2>/dev/null; then
    log "rigscope pid=$pid did not stop after ${STOP_GRACE_SECONDS}s; killing"
    kill -KILL "$pid" 2>/dev/null || true
  fi
  while kill -0 "$pid" 2>/dev/null; do
    if (( waited >= STOP_TIMEOUT_SECONDS )); then
      break
    fi
    sleep 1
    waited=$((waited + 1))
  done
  wait "$pid" 2>/dev/null || true
}

stop_existing_pid_file_process() {
  if [[ -f "$PID_FILE" ]]; then
    local pid
    pid="$(cat "$PID_FILE" 2>/dev/null || true)"
    if [[ -n "$pid" ]] && kill -0 "$pid" 2>/dev/null; then
      stop_rigscope "$pid"
    fi
  fi
}

verify_running_build() {
  local url output got attempt
  url="$(build_url)"
  for attempt in $(seq 1 40); do
    if output="$(curl --fail --silent --show-error --max-time 2 "$url" 2>/dev/null)"; then
      got="$(printf '%s\n' "$output" | version_value_from_json)"
      if [[ "$got" == "$expected_version" ]]; then
        log "verified running build version=$got"
        return 0
      fi
      if [[ -n "$got" ]]; then
        log "build endpoint is serving version=$got; waiting for $expected_version"
      fi
    fi
    sleep 0.25
  done
  log "failed to verify $expected_version at $url"
  log "last rigscope log:"
  tail -40 "$LOG" 2>/dev/null || true
  return 1
}

cleanup() {
  if [[ -n "$child_pid" ]] && kill -0 "$child_pid" 2>/dev/null; then
    stop_rigscope "$child_pid"
  fi
}

handle_signal() {
  cleanup
  exit "$1"
}

trap cleanup EXIT
trap 'handle_signal 130' INT
trap 'handle_signal 143' TERM

if (( "$#" > 0 )) && [[ "$1" == "serve" ]]; then
  shift
fi

build_rigscope
stop_existing_pid_file_process
launch_rigscope "$@"
verify_running_build
last_signature="$(source_signature)"

while true; do
  sleep "$POLL_SECONDS"
  if [[ -n "$child_pid" ]] && ! kill -0 "$child_pid" 2>/dev/null; then
    wait "$child_pid" 2>/dev/null || true
    child_pid=""
    log "rigscope exited; waiting for next successful build"
  fi

  current_signature="$(source_signature)"
  if [[ "$current_signature" == "$last_signature" ]]; then
    continue
  fi

  log "changes detected; waiting ${SETTLE_SECONDS}s for code to settle..."
  wait_for_settle
  last_signature="$(source_signature)"

  if build_rigscope; then
    if [[ -n "$child_pid" ]] && kill -0 "$child_pid" 2>/dev/null; then
      stop_rigscope "$child_pid"
    fi
    launch_rigscope "$@"
    verify_running_build || true
  else
    log "build failed; keeping current rigscope process"
  fi
done
