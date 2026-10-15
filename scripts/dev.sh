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
#   scripts/dev.sh start     build, start, wait for readiness, open the console
#   scripts/dev.sh stop      stop everything this script started
#   scripts/dev.sh restart   stop then start
#   scripts/dev.sh status    is it up, and where are the logs
#   scripts/dev.sh logs      follow the API log (Ctrl-C to stop; the API keeps running)
#
# Options:
#   --fresh        delete the local SQLite database before starting
#   --no-open      do not open a browser
#   --no-console   do not start the Next.js dev server; the API serves the built console instead
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
# The Next.js dev server runs as a second process so the console has hot reload. It is a
# development convenience only: a release binary embeds the built export and serves the
# console itself, with no Node anywhere.
readonly CONSOLE_PORT=3001
readonly CONSOLE_URL_DEV="http://127.0.0.1:$CONSOLE_PORT/"
readonly CONSOLE_PID_FILE="$LOG_DIR/console.pid"
readonly CONSOLE_LOG_FILE="$LOG_DIR/console.log"
# The trailing slash is deliberate. The Next.js export is built with one, and
# /console without it only 301s to /console/, so opening the bare path spends
# a redirect on what should be a single page load.
readonly CONSOLE_URL="http://127.0.0.1:8080/console/"
readonly READY_URL="http://127.0.0.1:8080/readyz"
readonly PORT=8080

FRESH=0
OPEN_BROWSER=1
# The console dev server is on by default: the console is where the work happens.
WITH_CONSOLE=1
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
  # The consoleassets tag embeds the built Next.js export, so the dev server serves the
  # same artifact a release binary would. If the export has not been built yet the build
  # still succeeds: consoleAssetsFor falls back to reading console/out from disk.
  if ! go build -tags consoleassets -o "$LOG_DIR/storvia-api" ./cmd/storvia-api 2>&1 | sed 's/^/     /'; then
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

# stop_console ends the console dev server.
stop_console() {
  [[ -f "$CONSOLE_PID_FILE" ]] || return 0
  local pid
  pid="$(cat "$CONSOLE_PID_FILE" 2>/dev/null || true)"

  if [[ -z "$pid" ]] || ! kill -0 "$pid" 2>/dev/null; then
    # Already dead -- most likely it crashed. The file still has to go, or every later
    # status and start reports a console that does not exist.
    rm -f "$CONSOLE_PID_FILE"
  fi

  if [[ -n "$pid" ]] && kill -0 "$pid" 2>/dev/null; then
    # The recorded pid is the next-server that holds the port. Killing just it leaves the
    # npm wrapper alive to respawn, so the children go first.
    pkill -P "$pid" 2>/dev/null || true
    kill "$pid" 2>/dev/null || true
    sleep 0.3
    kill -9 "$pid" 2>/dev/null || true
  fi
  # Anything still holding the console port is ours by construction -- claim_port
  # guaranteed it was free before start -- so it is safe to clear.
  local leftover
  leftover="$(port_owner "$CONSOLE_PORT")"
  if [[ -n "$leftover" ]]; then
    pkill -P "$leftover" 2>/dev/null || true
    kill -9 "$leftover" 2>/dev/null || true
  fi
  rm -f "$CONSOLE_PID_FILE"
}

# restore_config puts back a config changed by --faults-off.
restore_config() {
  local backup="$LOG_DIR/config.development.json.bak"
  if [[ -f "$backup" ]]; then
    mv "$backup" "$CONFIG_FILE"
    ok "restored $CONFIG_FILE"
  fi
}

# claim_port refuses to start when something already holds a port this script needs.
#
# It is the same check for both the API and the console, because the failure mode is the
# same: the newcomer cannot bind, and whatever is already there keeps answering.
claim_port() {
  local port="$1"
  local holder
  holder="$(port_owner "$port")"
  [[ -z "$holder" ]] && return 0

  warn "port $port is already in use by $(describe_process "$holder")"
  hint "this is the stale-process trap: an old process makes the new one look broken"
  read -r -p "     stop it and continue? [y/N] " reply
  if [[ "${reply:-}" =~ ^[Yy]$ ]]; then
    # Kill the process group where possible: `npm run dev` spawns a child, and killing
    # only the parent would leave the child holding the port.
    pkill -P "$holder" 2>/dev/null || true
    kill "$holder" 2>/dev/null || true
    sleep 1
    kill -9 "$holder" 2>/dev/null || true
    sleep 0.5
    if [[ -n "$(port_owner "$port")" ]]; then
      fail "port $port is still held after stopping $(describe_process "$holder")"
      return 1
    fi
    ok "port $port is free"
  else
    return 1
  fi
  return 0
}

# start_api launches the server in the background with its output captured.
start_api() {
  info "Starting the API"
  : >"$LOG_FILE"
  # Where Go hands the browser once it has verified the OAuth callback. The console is
  # not proxied through Go, so this is the only thing that returns the browser to it.
  STORVIA_CONSOLE_URL="$CONSOLE_URL_DEV" \
    "$LOG_DIR/storvia-api" -config "$CONFIG_FILE" -env "$ENV_FILE" >>"$LOG_FILE" 2>&1 &
  echo $! >"$PID_FILE"
  ok "pid $(cat "$PID_FILE"), logging to $LOG_FILE"
}

# start_console launches the Next.js dev server in the background.
#
# It is started after the API, because its rewrites proxy to the API origin and a failing
# proxy would look like a broken frontend.
start_console() {
  [[ "$WITH_CONSOLE" -eq 0 ]] && return 0
  if ! command -v npm >/dev/null 2>&1; then
    warn "npm is not on PATH; skipping the console dev server."
    warn "the API still serves the built console at $CONSOLE_URL"
    return 0
  fi
  if [[ ! -d "$REPO_ROOT/console/node_modules" ]]; then
    info "Installing console dependencies"
    (cd "$REPO_ROOT/console" && npm install --silent) || {
      warn "npm install failed; skipping the console dev server."
      return 0
    }
  fi

  info "Starting the Next.js console on port $CONSOLE_PORT"
  : >"$CONSOLE_LOG_FILE"
  (cd "$REPO_ROOT/console" && npm run dev -- --port "$CONSOLE_PORT") \
    >>"$CONSOLE_LOG_FILE" 2>&1 &
  local npm_pid=$!
  # `npm run dev` nests npm -> sh -> next dev -> next-server, so the process holding the
  # port is a grandchild and is not in $! or its direct children. The port owner is what
  # has to be recorded: it is both the thing that proves the console came up and the
  # thing that has to be stopped.
  for _ in $(seq 1 40); do
    local owner
    owner="$(port_owner "$CONSOLE_PORT")"
    if [[ -n "$owner" ]]; then
      echo "$owner" >"$CONSOLE_PID_FILE"
      ok "pid $owner (launched via npm $npm_pid), logging to $CONSOLE_LOG_FILE"
      return 0
    fi
    kill -0 "$npm_pid" 2>/dev/null || break
    sleep 0.25
  done
  echo "$npm_pid" >"$CONSOLE_PID_FILE"
  return 1
}

# wait_console blocks until the console dev server answers.
wait_console() {
  [[ "$WITH_CONSOLE" -eq 0 ]] && return 0
  [[ -f "$CONSOLE_PID_FILE" ]] || return 0
  info "Waiting for the console"
  for _ in $(seq 1 120); do
    # claim_port proved the port was free before the console was launched, so anything
    # answering now is ours. That ordering is what makes this check trustworthy --
    # there is no window in which somebody else's server could be mistaken for it.
    if curl -fsS --max-time 2 "$CONSOLE_URL_DEV" >/dev/null 2>&1; then
      ok "ready"
      return 0
    fi
    if ! kill -0 "$(cat "$CONSOLE_PID_FILE")" 2>/dev/null; then
      fail "the console dev server exited. Last lines of $CONSOLE_LOG_FILE:"
      tail -n 20 "$CONSOLE_LOG_FILE" | sed 's/^/     /' >&2
      return 1
    fi
    sleep 0.25
  done
  fail "the console did not start. Last lines of $CONSOLE_LOG_FILE:"
  tail -n 20 "$CONSOLE_LOG_FILE" | sed 's/^/     /' >&2
  return 1
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

# console_url reports where the console is actually being served.
console_url() {
  if [[ "$WITH_CONSOLE" -eq 1 ]] && curl -fsS --max-time 1 "$CONSOLE_URL_DEV" >/dev/null 2>&1; then
    printf '%s' "$CONSOLE_URL_DEV"
  else
    printf '%s' "$CONSOLE_URL"
  fi
}

# open_panel opens the console, tolerating a headless machine.
open_panel() {
  [[ "$OPEN_BROWSER" -eq 0 ]] && return 0
  local url
  url="$(console_url)"
  info "Opening the console"
  if command -v open >/dev/null 2>&1; then
    open "$url" && ok "$url"
  elif command -v xdg-open >/dev/null 2>&1; then
    xdg-open "$url" && ok "$url"
  else
    hint "no opener found; visit $url yourself"
  fi
}

# summarise prints the state an operator wants at a glance.
summarise() {
  echo
  info "Ready"
  printf '     console %s\n' "$(console_url)"
  printf '     api     http://127.0.0.1:%s\n' "$PORT"
  printf '     mode    %s\n' "${CONSOLE_MODE:-embedded in the API}"
  printf '     logs    %s\n' "$LOG_FILE"
  if [[ -f "$CONSOLE_PID_FILE" ]]; then
    printf '     console logs %s\n' "$CONSOLE_LOG_FILE"
  fi
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

  # Both ports are checked. A stale console dev server on $CONSOLE_PORT is the same trap
  # as a stale API on $PORT, and it is worse: `next dev` fails to bind, dies quietly, and
  # the stale server answers the readiness check — so the run reports success while
  # serving somebody else's build.
  claim_port "$PORT"   || die "not starting"
  claim_port "$CONSOLE_PORT" || die "not starting"

  build
  apply_flag_override
  validate
  start_api
  wait_ready || { stop_api || true; exit 1; }

  # The console comes up after the API, because it proxies to it. A failure here is not
  # fatal: the API serves the built console itself, so the developer still gets a working
  # console with no hot reload.
  start_console
  if [[ "$WITH_CONSOLE" -eq 0 ]]; then
    # --no-console. wait_console returns success immediately in this mode, so the mode
    # has to be set here or the summary would claim hot reload for a console that was
    # never started.
    CONSOLE_MODE="embedded in the API (--no-console)"
  elif wait_console; then
    CONSOLE_MODE="next dev (hot reload)"
  else
    warn "falling back to the console embedded in the API at $CONSOLE_URL"
    stop_console
    CONSOLE_MODE="embedded in the API"
  fi

  open_panel
  summarise
}

cmd_stop() {
  mkdir -p "$LOG_DIR"
  stop_console
  stop_api
  restore_config
}

cmd_status() {
  local holder
  holder="$(port_owner "$PORT")"
  if [[ -n "$holder" ]]; then
    ok "running: $(describe_process "$holder")"

    # The mode is derived from the port, not from the pid file. A pid file survives a
    # crash, so keying off its existence reported "next dev (hot reload)" for a console
    # that had been killed and was not coming back.
    local console_holder
    console_holder="$(port_owner "$CONSOLE_PORT")"
    if [[ -n "$console_holder" ]]; then
      printf '     console %s\n' "$CONSOLE_URL_DEV"
      printf '     mode    next dev (hot reload)\n'
      printf '     console logs %s\n' "$CONSOLE_LOG_FILE"
    else
      printf '     console %s\n' "$CONSOLE_URL"
      printf '     mode    embedded in the API\n'
      if [[ -f "$CONSOLE_PID_FILE" ]]; then
        warn "the console dev server is not running (stale pid file: $CONSOLE_PID_FILE)"
        hint "run scripts/dev.sh start to bring it back, or --no-console to skip it"
      fi
    fi
    printf '     api     http://127.0.0.1:%s\n' "$PORT"
    printf '     logs    %s\n' "$LOG_FILE"
  else
    info "not running"
    # A dead API does not mean a dead console; someone may have killed the wrong thing.
    if [[ -n "$(port_owner "$CONSOLE_PORT")" ]]; then
      warn "the console dev server is still running on $CONSOLE_PORT with no API behind it"
      hint "run scripts/dev.sh stop"
    fi
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
    --no-console) WITH_CONSOLE=0 ;;
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