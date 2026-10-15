#!/usr/bin/env bash
#
# Development launcher for Storvia.
#
# It exists because the manual sequence — build, start, watch the log, open a browser,
# stop it again — is easy to get subtly wrong. In particular, a stale process holding
# port 8080 makes the new binary look broken, which has cost real debugging time twice.
# So this script refuses to guess: it checks the port, says who holds it, and offers to
# stop it.
#
# Usage:
#   scripts/dev.sh start     build, start, wait for readiness, open the admin panel
#   scripts/dev.sh stop      stop everything this script started
#   scripts/dev.sh restart   stop then start
#   scripts/dev.sh status    is it up, and where are the logs
#   scripts/dev.sh logs      follow the API log (Ctrl-C to stop; the API keeps running)
#
# Options:
#   --fresh        delete the local SQLite database before starting
#   --no-open      do not open a browser
#   --faults-off   turn off Drive outage simulation, so uploads use the real Drive path
#
# Secrets are read from the gitignored .env.development. They are never echoed.

set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$REPO_ROOT"

# --- configuration -----------------------------------------------------------

readonly CONFIG_FILE="${STORVIA_CONFIG:-config/config.development.json}"
readonly ENV_FILE="${STORVIA_ENV:-.env.development}"
readonly LOG_DIR="${STORVIA_LOG_DIR:-logs}"
readonly LOG_FILE="$LOG_DIR/storvia-api.log"
readonly PID_FILE="$LOG_DIR/storvia-api.pid"
readonly ADMIN_URL="http://127.0.0.1:8080/admin"
readonly READY_URL="http://127.0.0.1:8080/readyz"
readonly PORT=8080

FRESH=0
OPEN_BROWSER=1
FAULT_OVERRIDE=""

# --- output ------------------------------------------------------------------

if [[ -t 1 ]]; then
  BOLD=$'\033[1m'; DIM=$'\033[2m'; RED=$'\033[31m'; GREEN=$'\033[32m'
  YELLOW=$'\033[33m'; RESET=$'\033[0m'
else
  BOLD=""; DIM=""; RED=""; GREEN=""; YELLOW=""; RESET=""
fi

info()  { printf '%s==>%s %s\n' "$BOLD" "$RESET" "$*"; }
ok()    { printf '%s  ok%s %s\n' "$GREEN" "$RESET" "$*"; }
warn()  { printf '%swarn%s %s\n' "$YELLOW" "$RESET" "$*" >&2; }
fail()  { printf '%sfail%s %s\n' "$RED" "$RESET" "$*" >&2; }
hint()  { printf '%s     %s%s\n' "$DIM" "$*" "$RESET"; }

die() { fail "$*"; exit 1; }

# --- helpers -----------------------------------------------------------------

# port_owner prints the PID listening on the given port, if any.
#
# It always succeeds. lsof exits non-zero when nothing matches, and under `set -e`
# that would abort the script silently — which is exactly what it did before this
# guard was added, leaving `dev.sh status` to print nothing at all.
port_owner() {
  lsof -nP -iTCP:"$1" -sTCP:LISTEN -t 2>/dev/null | head -1 || true
}

# describe_process prints a human-readable label for a PID.
describe_process() {
  local pid="$1" name=""
  [[ -z "$pid" ]] && return
  name="$(basename "$(ps -o comm= -p "$pid" 2>/dev/null | tr -d ' ')" 2>/dev/null)"
  [[ -z "$name" ]] && name="unknown"
  # A go run parent is not what holds the socket; the compiled child is.
  local cmd
  cmd="$(ps -o command= -p "$pid" 2>/dev/null | cut -c1-80)"
  printf '%s (%s)' "$name" "$cmd"
}

require_file() {
  [[ -f "$1" ]] || die "$1 not found. It holds the secrets and is gitignored, so create it from .env.example:
     cp .env.example $1"
}

# build compiles the API.
build() {
  command -v go >/dev/null 2>&1 || die "go is not on PATH"
  info "Building the API"
  if ! go build -o "$LOG_DIR/storvia-api" ./cmd/storvia-api 2>&1 | sed 's/^/     /'; then
    die "build failed"
  fi
  ok "built $LOG_DIR/storvia-api"
}

# validate runs the loader without serving, so a broken config fails before a
# process is left running and confusing.
validate() {
  info "Validating configuration"
  local output
  if ! output="$("$LOG_DIR/storvia-api" -config "$CONFIG_FILE" -env "$ENV_FILE" -check 2>&1)"; then
    printf '%s\n' "$output" | sed 's/^/     /' >&2
    die "configuration is not valid; not starting"
  fi
  if grep -q '"issues":null' <<<"$output"; then
    ok "configuration is valid, no issues"
  else
    warn "configuration reported issues (see above); starting anyway"
  fi
}

# apply_flag_override rewrites simulate_drive_outage when --faults-off is given.
#
# The config is rewritten in place and restored on stop, so the flag does not silently
# persist and surprise a later run that wanted the outage path.
apply_flag_override() {
  if [[ "$FAULT_OVERRIDE" != "off" ]]; then
    return
  fi
  local backup="$LOG_DIR/config.development.json.bak"
  cp "$CONFIG_FILE" "$backup"
  python3 - "$CONFIG_FILE" <<'PY'
import json, sys, collections
path = sys.argv[1]
with open(path) as fh:
    cfg = json.load(fh, object_pairs_hook=collections.OrderedDict)
cfg["admin_panel"]["simulate_drive_outage"] = False
with open(path, "w") as fh:
    json.dump(cfg, fh, indent=2)
    fh.write("\n")
PY
  ok "Drive outage simulation turned OFF (restored when you run 'dev.sh stop')"
}

# stop_api terminates a running instance, preferring the recorded PID.
stop_api() {
  local pid=""
  [[ -f "$PID_FILE" ]] && pid="$(cat "$PID_FILE" 2>/dev/null || true)"

  # A PID file can be stale after a reboot, so confirm the port is actually held
  # before deciding there is something to stop.
  local holder
  holder="$(port_owner "$PORT")"

  if [[ -z "$holder" ]]; then
    [[ -f "$PID_FILE" ]] && rm -f "$PID_FILE"
    hint "nothing listening on $PORT"
    return 0
  fi

  if [[ -n "$pid" ]] && [[ "$pid" != "$holder" ]]; then
    # Something else holds the port. Report it rather than killing a stranger.
    fail "port $PORT is held by a process this script did not start:"
    hint "$(describe_process "$holder")"
    hint "stop it yourself, or re-run after it exits"
    return 1
  fi

  local target="${pid:-$holder}"
  info "Stopping the API ($(describe_process "$target"))"
  kill "$target" 2>/dev/null || true

  # Give it the same grace period the server itself uses, so an in-flight upload
  # is drained rather than cut.
  for _ in $(seq 1 30); do
    [[ -z "$(port_owner "$PORT")" ]] && break
    sleep 0.5
  done

  if [[ -n "$(port_owner "$PORT")" ]]; then
    warn "did not stop within 15s; sending TERM to the process group"
    kill -TERM "$target" 2>/dev/null || true
    sleep 1
  fi

  [[ -f "$PID_FILE" ]] && rm -f "$PID_FILE"
  ok "stopped"
}

# restore_config puts back a config changed by --faults-off.
restore_config() {
  local backup="$LOG_DIR/config.development.json.bak"
  if [[ -f "$backup" ]]; then
    mv "$backup" "$CONFIG_FILE"
    ok "restored $CONFIG_FILE"
  fi
}

# start_api launches the server in the background with its output captured.
start_api() {
  info "Starting the API"
  : >"$LOG_FILE"
  "$LOG_DIR/storvia-api" -config "$CONFIG_FILE" -env "$ENV_FILE" >>"$LOG_FILE" 2>&1 &
  echo $! >"$PID_FILE"
  ok "pid $(cat "$PID_FILE"), logging to $LOG_FILE"
}

# wait_ready blocks until the server answers, and explains a failure.
wait_ready() {
  info "Waiting for the API to answer"
  for _ in $(seq 1 60); do
    if curl -fsS --max-time 2 "$READY_URL" >/dev/null 2>&1; then
      ok "ready"
      return 0
    fi
    # The process died during startup; no point waiting on a corpse.
    if [[ -f "$PID_FILE" ]] && ! kill -0 "$(cat "$PID_FILE")" 2>/dev/null; then
      fail "the API exited during startup. Last lines of $LOG_FILE:"
      tail -n 20 "$LOG_FILE" | sed 's/^/     /' >&2
      return 1
    fi
    sleep 0.25
  done
  fail "the API did not become ready. Last lines of $LOG_FILE:"
  tail -n 20 "$LOG_FILE" | sed 's/^/     /' >&2
  return 1
}

# open_panel opens the admin page, tolerating a headless machine.
open_panel() {
  [[ "$OPEN_BROWSER" -eq 0 ]] && return 0
  info "Opening the admin panel"
  if command -v open >/dev/null 2>&1; then
    open "$ADMIN_URL" && ok "$ADMIN_URL"
  elif command -v xdg-open >/dev/null 2>&1; then
    xdg-open "$ADMIN_URL" && ok "$ADMIN_URL"
  else
    hint "no opener found; visit $ADMIN_URL yourself"
  fi
}

# summarise prints the state an operator wants at a glance.
summarise() {
  echo
  info "Ready"
  printf '     panel   %s\n' "$ADMIN_URL"
  printf '     api     http://127.0.0.1:%s\n' "$PORT"
  printf '     logs    %s\n' "$LOG_FILE"
  printf '     stop    scripts/dev.sh stop\n'
  echo
  local fault
  fault="$(python3 -c "
import json,sys
try:
    print(json.load(open('$CONFIG_FILE'))['admin_panel']['simulate_drive_outage'])
except Exception:
    print('unknown')
")"
  if [[ "$fault" == "True" ]]; then
    warn "Drive outage simulation is ACTIVE: new uploads route to the fallback, not Drive."
    hint "run scripts/dev.sh start --faults-off to test the real Drive path"
  fi
  echo
  hint "tail the log in another terminal: scripts/dev.sh logs"
}

# --- commands ----------------------------------------------------------------

cmd_start() {
  require_file "$ENV_FILE"
  require_file "$CONFIG_FILE"
  mkdir -p "$LOG_DIR"

  if [[ "$FRESH" -eq 1 ]]; then
    info "Removing the local database"
    rm -f storvia.dev.db storvia.dev.db-shm storvia.dev.db-wal
    ok "fresh database"
  fi

  local holder
  holder="$(port_owner "$PORT")"
  if [[ -n "$holder" ]]; then
    warn "port $PORT is already in use by $(describe_process "$holder")"
    hint "this is the stale-process trap: an old binary makes the new one look broken"
    read -r -p "     stop it and continue? [y/N] " reply
    if [[ "${reply:-}" =~ ^[Yy]$ ]]; then
      kill "$holder" 2>/dev/null || true
      sleep 1
    else
      die "not starting"
    fi
  fi

  build
  apply_flag_override
  validate
  start_api
  wait_ready || { stop_api || true; exit 1; }
  open_panel
  summarise
}

cmd_stop() {
  mkdir -p "$LOG_DIR"
  stop_api
  restore_config
}

cmd_status() {
  local holder
  holder="$(port_owner "$PORT")"
  if [[ -n "$holder" ]]; then
    ok "running: $(describe_process "$holder")"
    printf '     panel %s\n' "$ADMIN_URL"
    printf '     logs  %s\n' "$LOG_FILE"
  else
    info "not running"
  fi
  [[ -f "$LOG_DIR/config.development.json.bak" ]] && \
    warn "a --faults-off override is still in place; run 'dev.sh stop' to restore it"
  return 0
}

cmd_logs() {
  [[ -f "$LOG_FILE" ]] || die "no log at $LOG_FILE. Run 'dev.sh start' first."
  info "Following $LOG_FILE (Ctrl-C stops following, the API keeps running)"
  tail -n 40 -f "$LOG_FILE"
}

# --- argument parsing --------------------------------------------------------

usage() {
  # Printed straight from the header comment so the help and the script cannot drift
  # apart. The range stops at the first non-comment line.
  sed -n '3,/^$/p' "${BASH_SOURCE[0]}" | sed 's/^# \{0,1\}//'
}

cmd="${1:-start}"
[[ $# -gt 0 ]] && shift || true

while [[ $# -gt 0 ]]; do
  case "$1" in
    --fresh)      FRESH=1 ;;
    --no-open)    OPEN_BROWSER=0 ;;
    --faults-off) FAULT_OVERRIDE="off" ;;
    -h|--help)    usage; exit 0 ;;
    *)            die "unknown option: $1 (try --help)" ;;
  esac
  shift
done

case "$cmd" in
  start)   cmd_start ;;
  stop)    cmd_stop ;;
  restart) cmd_stop; cmd_start ;;
  status)  cmd_status ;;
  logs)    cmd_logs ;;
  *)       usage; exit 1 ;;
esac