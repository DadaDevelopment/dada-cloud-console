#!/usr/bin/env bash
# box-walk.sh — walk the whole Dada Box vertical against a really running backend
# and a really running Postgres, and fail loudly if any step does not work.
#
# WHAT THIS IS FOR. The box feature accumulated a metrics contract, test seams, a
# table and a REST skeleton before anything could actually be done. This script is
# the answer to "can someone walk the path today": it calls the real HTTP API for
# every step, shows the real output of a command executing inside a real box, and
# exits non-zero the moment a step does not do what it claims.
#
# There are no mocked responses and no in-process shortcuts here. Every step is a
# curl against a backend started by this script, and every assertion is made on the
# body that backend returned.
#
# WHAT IS A STAND-IN, stated up front rather than buried:
#   * The box runs on internal/box.LocalRuntime — a real adapter, but the
#     SINGLE-HOST one. Production runs a box as a Pod in the existing cloud per
#     ADR-019. LocalRuntime gives the box its own filesystem tree, mount/PID/UTS/IPC
#     namespaces, its own /etc, its own container daemon and its own 0600 env file.
#     It does NOT give it a network namespace, cgroup limits or a user namespace.
#   * Crystallization materializes onto a separate root directory that stands in for
#     the VM. There is no hypervisor and no Beget token here. Everything after "the
#     VM exists" is the real ADR-019 mechanism against real files, and the
#     verification report prints which parts are the stand-in.
#   * systemd is not present, so the rendered unit is written and its ExecStart is
#     executed by the same supervisor the box used.
#   * The box runs its OWN endpoint (cmd/box-broker) and that is what `box up`
#     publishes, so the commands in the box's-own-door step below do not pass
#     through the control plane. What is a stand-in is the ADDRESS: LocalRuntime
#     gives the box no network namespace, so the broker's listener is loopback on
#     the box host rather than the box's own hostname. In production it is the box's
#     Pod address (ADR-019). POST /api/v1/box/session/exec is still served and still
#     exercised here — it is the named fallback for a box whose broker did not come
#     up, not the product's path.
#
# REQUIREMENTS (all verified present in the environment this was written in):
#   root (the runtime creates mount namespaces), Postgres on 127.0.0.1:5433 as
#   user `dada` with trust auth, rsync, nsenter, unshare, curl, python3, psql, and
#   dockerd for the warm toolchain the readiness canary insists on.
#
# Usage:  sudo scripts/box-walk.sh
#         PORT=18080 DB_PORT=5433 scripts/box-walk.sh
set -Eeuo pipefail

# ---------------------------------------------------------------------------
# configuration
# ---------------------------------------------------------------------------
REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
BACKEND_DIR="$REPO_ROOT/backend"

PORT="${PORT:-18080}"
DB_HOST="${DB_HOST:-127.0.0.1}"
DB_PORT="${DB_PORT:-5433}"
DB_USER="${DB_USER:-dada}"
DB_NAME="${DB_NAME:-dada_box_walk}"
BASE="http://127.0.0.1:${PORT}"
JWT_SECRET="${JWT_SECRET:-box-walk-secret}"

WORKDIR="${WORKDIR:-/var/tmp/dada-box-walk}"
BOX_LOCAL_ROOT="${BOX_LOCAL_ROOT:-$WORKDIR/runtime}"
LOG="$WORKDIR/backend.log"

# The port the demo app serves on INSIDE the box. Fixed so the transcript is
# reproducible; the crystallization socket comparison is over exactly this set.
APP_PORT="${APP_PORT:-18099}"

STEP=0
FAILED=""

# ---------------------------------------------------------------------------
# output helpers — every step prints what it did and the real output it got
# ---------------------------------------------------------------------------
hr() { printf '%s\n' "------------------------------------------------------------------------------"; }

step() {
  STEP=$((STEP + 1))
  printf '\n'
  hr
  printf 'STEP %d — %s\n' "$STEP" "$1"
  hr
}

info() { printf '  %s\n' "$1"; }

die() {
  printf '\n!! FAILED at step %d: %s\n' "$STEP" "$1" >&2
  FAILED="$1"
  exit 1
}

on_exit() {
  local code=$?
  if [[ -n "${BACKEND_PID:-}" ]] && kill -0 "$BACKEND_PID" 2>/dev/null; then
    kill "$BACKEND_PID" 2>/dev/null || true
    wait "$BACKEND_PID" 2>/dev/null || true
  fi
  # The box's namespaces are owned by processes the backend started, and they
  # outlive it — so a walk that left them behind would make the next run's
  # database drop fail on a connection nobody can find. Tear them down explicitly.
  pkill -f "$BOX_LOCAL_ROOT" 2>/dev/null || true
  if (( code != 0 )); then
    printf '\n'
    hr
    printf 'WALK FAILED (exit %d). Last 30 non-routing lines of the backend log:\n' "$code"
    hr
    grep -v 'GIN-debug' "$LOG" 2>/dev/null | tail -30 || true
  fi
  exit "$code"
}
trap on_exit EXIT

# jqish extracts one dotted path from a JSON document on stdin, using python3 so
# the script does not depend on jq being installed.
jqish() {
  python3 -c '
import json,sys
path=sys.argv[1]
try:
    cur=json.load(sys.stdin)
except Exception as e:
    print("JSON-PARSE-ERROR: %s" % e); sys.exit(0)
for part in path.split("."):
    if part=="": continue
    if isinstance(cur,list):
        try: cur=cur[int(part)]
        except Exception: print(""); sys.exit(0)
    elif isinstance(cur,dict):
        if part not in cur: print(""); sys.exit(0)
        cur=cur[part]
    else:
        print(""); sys.exit(0)
if isinstance(cur,(dict,list)):
    print(json.dumps(cur))
elif isinstance(cur,bool):
    print("true" if cur else "false")
elif cur is None:
    print("")
else:
    print(cur)
' "$1"
}

pretty() { python3 -m json.tool 2>/dev/null || cat; }

# api METHOD PATH [BODY] — calls the real HTTP API with the console JWT.
api() {
  local method="$1" path="$2" body="${3:-}"
  if [[ -n "$body" ]]; then
    curl -sS -X "$method" "$BASE$path" \
      -H "Authorization: Bearer $TOKEN" \
      -H 'Content-Type: application/json' \
      -d "$body"
  else
    curl -sS -X "$method" "$BASE$path" -H "Authorization: Bearer $TOKEN"
  fi
}

# boxexec COMMAND — runs a command INSIDE the box through the box's own door,
# authenticated by the one-time dadabox_ session token (never the console JWT).
boxexec() {
  local cmd="$1" timeout="${2:-60}"
  python3 -c '
import json,sys
print(json.dumps({"command": sys.argv[1], "timeout_seconds": int(sys.argv[2])}))
' "$cmd" "$timeout" | curl -sS -X POST "$BASE/api/v1/box/session/exec" \
    -H "X-Dada-Box-Token: $BOX_TOKEN" \
    -H 'Content-Type: application/json' \
    --data-binary @-
}

require_tool() {
  command -v "$1" >/dev/null 2>&1 || die "required tool '$1' is not installed"
}

# ===========================================================================
printf '==============================================================================\n'
printf 'DADA BOX — END-TO-END WALK\n'
printf 'started      %s\n' "$(date -u +%Y-%m-%dT%H:%M:%SZ)"
printf 'repo         %s\n' "$REPO_ROOT"
printf 'commit       %s\n' "$(git -C "$REPO_ROOT" rev-parse --short HEAD 2>/dev/null || echo unknown)"
printf 'backend      %s\n' "$BASE"
printf 'postgres     %s:%s user=%s db=%s\n' "$DB_HOST" "$DB_PORT" "$DB_USER" "$DB_NAME"
printf 'box runtime  LocalRuntime at %s\n' "$BOX_LOCAL_ROOT"
printf '==============================================================================\n'

# ---------------------------------------------------------------------------
step "preflight: the tools and the services this walk really needs"
[[ "$(id -u)" == "0" ]] || die "this walk must run as root: LocalRuntime creates mount and PID namespaces"
for t in curl python3 psql pg_isready rsync nsenter unshare go; do
  require_tool "$t"
  info "$t -> $(command -v "$t")"
done
if command -v dockerd >/dev/null 2>&1; then
  info "dockerd -> $(command -v dockerd) (the box starts its OWN daemon inside itself)"
else
  info "dockerd -> MISSING. The readiness canary probes a docker SERVER, so the warm"
  info "           toolchain will read as incomplete and 'box up' will correctly refuse."
fi
pg_isready -h "$DB_HOST" -p "$DB_PORT" -U "$DB_USER" || die "Postgres is not accepting connections on $DB_HOST:$DB_PORT"
info "postgres -> $(psql -h "$DB_HOST" -p "$DB_PORT" -U "$DB_USER" -d postgres -tAc 'select version()' | head -1)"

# The app port must start FREE. If anything already answers on it, every later
# "the exposed port returned 200" would be evidence about that process and not
# about the box — the single most plausible way this walk could pass while proving
# nothing. So it is checked, not assumed.
if python3 -c "import socket,sys; s=socket.socket(); s.settimeout(1); sys.exit(s.connect_ex(('127.0.0.1',$APP_PORT)))"; then
  die "something is already listening on 127.0.0.1:$APP_PORT; a 200 from it would not be evidence about the box"
fi
info "app port $APP_PORT is free — a later 200 on it can only come from the box"

# Every proof in this walk is tied to a value minted here, so a stale process or a
# cached response cannot stand in for the box.
RUN_MARKER="box-walk-$(date -u +%Y%m%dT%H%M%SZ)-$$"
info "run marker: $RUN_MARKER (must appear in the served body and in the crystallized VM's response)"

mkdir -p "$WORKDIR"
rm -rf "$BOX_LOCAL_ROOT"
mkdir -p "$BOX_LOCAL_ROOT"

# ---------------------------------------------------------------------------
step "create the walk's own database (the cluster already running is left alone)"
# A previous walk's backend may still hold a connection, so the sessions are closed
# explicitly rather than the drop being retried until it happens to work.
psql -h "$DB_HOST" -p "$DB_PORT" -U "$DB_USER" -d postgres -q -c \
  "SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE datname = '$DB_NAME'" >/dev/null 2>&1 || true
psql -h "$DB_HOST" -p "$DB_PORT" -U "$DB_USER" -d postgres -v ON_ERROR_STOP=1 -q \
  -c "DROP DATABASE IF EXISTS $DB_NAME" \
  -c "CREATE DATABASE $DB_NAME" || die "could not create $DB_NAME"
info "created database $DB_NAME"
# The migrations create tables owned by this role and GRANT to 'dada'; the role
# has to exist for 062/066 to succeed.
psql -h "$DB_HOST" -p "$DB_PORT" -U "$DB_USER" -d "$DB_NAME" -tAc \
  "SELECT 1 FROM pg_roles WHERE rolname='dada'" | grep -q 1 \
  || psql -h "$DB_HOST" -p "$DB_PORT" -U "$DB_USER" -d postgres -q -c "CREATE ROLE dada LOGIN" \
  || die "could not ensure the dada role exists"
info "role 'dada' present (migrations 062 and 066 GRANT to it by name)"

# ---------------------------------------------------------------------------
step "build and start the real backend — it applies every migration itself"
export DB_URL="postgres://${DB_USER}@${DB_HOST}:${DB_PORT}/${DB_NAME}?sslmode=disable"
export JWT_SECRET PORT
export AUTH_MODE=local
export DEV_MODE=true
export MCP_ENABLED=false
export BILLING_ENABLED=false
export PROJECT_GROUP_SYNC_ENABLED=false
export PREVIEW_HOST_BASE=""
export PREVIEW_HOST_SECRET=""
export KEYCLOAK_ISSUER=""
# The box runtime. ONE switch turns the whole feature on; unset, every box verb
# answers 503 with a reason.
export BOX_LOCAL_ROOT="$BOX_LOCAL_ROOT"
export BOX_WARM_POOL_SIZE="${BOX_WARM_POOL_SIZE:-2}"
export BOX_MANAGED_PG_URL="postgres://${DB_USER}@${DB_HOST}:${DB_PORT}/postgres?sslmode=disable"
export BOX_MANAGED_PG_HOST="$DB_HOST"
export BOX_MANAGED_PG_PORT="$DB_PORT"
export BOX_SESSION_BASE_URL="$BASE"
# The box's own door (D6). BOX_BROKER_DIR is a DIRECTORY, and only the box-broker
# binary is put in it: the value is a bind-mount source, so pointing it at a
# populated bin directory would mount that whole directory into every tenant's body.
export BOX_BROKER_DIR="$WORKDIR/broker"
export BOX_HOSTNAME_BASE="box.dada-tuda.ru"

info "DB_URL=$DB_URL"
info "BOX_LOCAL_ROOT=$BOX_LOCAL_ROOT  BOX_WARM_POOL_SIZE=$BOX_WARM_POOL_SIZE"
info "BOX_MANAGED_PG_URL points at the SAME really-running cluster; the attach path"
info "  creates a real role and a real database on it, OUTSIDE the box."
# Built rather than `go run`, and deliberately: `go run` starts the server as a
# CHILD of itself, so killing the shell's job leaves the real server holding a
# database connection and the next run's DROP DATABASE fails on a process nobody can
# see. One binary means one pid to stop.
info "building ./cmd/server"
( cd "$BACKEND_DIR" && go build -o "$WORKDIR/dada-server" ./cmd/server ) || die "the backend did not build"
info "building ./cmd/box-broker — the endpoint that runs INSIDE the box"
mkdir -p "$BOX_BROKER_DIR"
( cd "$BACKEND_DIR" && go build -o "$BOX_BROKER_DIR/box-broker" ./cmd/box-broker ) || die "the box broker did not build"
"$WORKDIR/dada-server" >"$LOG" 2>&1 &
BACKEND_PID=$!
info "backend pid $BACKEND_PID, log $LOG"

# Warming the pool is synchronous and deliberately so (see box_runtime.go), so the
# first /health can take a while: it includes creating the box trees and bringing
# each box's container daemon up.
info "waiting for /health (the warm pool is built before the server listens)"
for i in $(seq 1 240); do
  if curl -sf "$BASE/health" >/dev/null 2>&1; then break; fi
  kill -0 "$BACKEND_PID" 2>/dev/null || die "the backend exited during startup"
  sleep 1
done
curl -sf "$BASE/health" >/dev/null 2>&1 || die "the backend never became healthy"
info "health -> $(curl -sS "$BASE/health")"
grep -E 'box: (LocalRuntime|session|runtime)' "$LOG" | sed 's/^/  | /' || true

# ---------------------------------------------------------------------------
step "authenticate through the real login handler (local HS256 mode)"
# The identity comes from migration 002's dev seed and the token from the real
# POST /api/v1/auth/login — bcrypt-verified, no bypass, no hand-signed JWT. Every
# subsequent call carries it, so the authorization ladder in boxes.go is exercised
# rather than skipped.
LOGIN="$(curl -sS -X POST "$BASE/api/v1/auth/login" -H 'Content-Type: application/json' \
  -d '{"username":"admin","password":"admin"}')"
TOKEN="$(printf '%s' "$LOGIN" | jqish token)"
[[ -n "$TOKEN" && "$TOKEN" != "JSON-PARSE-ERROR"* ]] || die "login failed: $LOGIN"
info "POST /api/v1/auth/login -> token acquired (${#TOKEN} chars)"
info "GET  /api/v1/me         -> $(api GET /api/v1/me | jqish username)"

# ---------------------------------------------------------------------------
step "create a project through the real API"
PROJ="$(api POST /api/v1/projects "{\"slug\":\"box-walk-$$\",\"display_name\":\"Box Walk\"}")"
PROJECT_ID="$(printf '%s' "$PROJ" | jqish project_id)"
[[ -n "$PROJECT_ID" ]] || die "could not create a project: $PROJ"
info "project id $PROJECT_ID  org $(printf '%s' "$PROJ" | jqish org_id)  role $(printf '%s' "$PROJ" | jqish role)"

# ---------------------------------------------------------------------------
step "GET /box/catalog — the frozen warm-image and size catalog, plus the live pool"
CATALOG="$(api GET /api/v1/box/catalog)"
printf '%s\n' "$CATALOG" | pretty | sed 's/^/  /'
POOL_AVAIL="$(printf '%s' "$CATALOG" | jqish pool.available)"
[[ "${POOL_AVAIL:-0}" -ge 1 ]] || die "the warm pool is empty; a claim would pay a cold start"
info "warm pool available=$POOL_AVAIL target=$(printf '%s' "$CATALOG" | jqish pool.target)"

# ---------------------------------------------------------------------------
step "box up — one call, and it returns only once a command has run INSIDE the box"
UP="$(api POST "/api/v1/projects/$PROJECT_ID/box-up" \
  '{"ssh_public_key":"ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIBoxWalkPublicKeyOnly box-walk","wait_seconds":120}')"
printf '%s\n' "$UP" | pretty | sed 's/^/  /'
BOX_NAME="$(printf '%s' "$UP" | jqish box.name)"
BOX_TOKEN="$(printf '%s' "$UP" | jqish session.token)"
BOX_STATUS="$(printf '%s' "$UP" | jqish box.status)"
TIME_TO_READY_MS="$(printf '%s' "$UP" | jqish ready.time_to_ready_ms)"
POOL_LABEL="$(printf '%s' "$UP" | jqish ready.pool)"
[[ "$BOX_STATUS" == "Ready" ]] || die "box up did not produce a Ready box (status=$BOX_STATUS)"
[[ -n "$BOX_TOKEN" ]] || die "box up returned no session token"
[[ "$BOX_TOKEN" == dadabox_* ]] || die "the session token is not a dadabox_ token: $BOX_TOKEN"
[[ -n "$TIME_TO_READY_MS" ]] || die "box up published no measured time to ready"
info "box=$BOX_NAME status=$BOX_STATUS pool=$POOL_LABEL"
info "one-time credential: ${BOX_TOKEN:0:14}… (only its sha256 and 6-hex prefix are stored)"

# The token's plaintext must not be retrievable a second time.
CONN="$(api GET "/api/v1/projects/$PROJECT_ID/boxes/$BOX_NAME/connection")"
if printf '%s' "$CONN" | grep -q "$BOX_TOKEN"; then
  die "the connection endpoint echoed the plaintext token back; it must be shown exactly once"
fi
info "GET .../connection does NOT reveal the token again — confirmed"

# The canary is what "ready" means, and it is stated in the response.
info "readiness canary the box actually ran:"
printf '%s' "$UP" | jqish ready.canary | sed 's/^/    /'
info "critical path walked: $(printf '%s' "$UP" | jqish ready.critical_path)"
info "phase breakdown (ms): $(printf '%s' "$UP" | jqish ready.phase_ms)"

# ---------------------------------------------------------------------------
step "run commands INSIDE the box and see their real output"
info "the box's own door: POST /api/v1/box/session/exec with X-Dada-Box-Token"
info "(no boxExec tool exists on our MCP surface — see box_session.go)"

show_exec() {
  local label="$1" cmd="$2"
  local out rc
  out="$(boxexec "$cmd")"
  rc="$(printf '%s' "$out" | jqish exit_code)"
  printf '\n  $ %s\n' "$cmd"
  printf '%s' "$out" | jqish stdout | sed 's/^/    /'
  local err
  err="$(printf '%s' "$out" | jqish stderr)"
  [[ -n "$err" ]] && printf '%s' "$err" | sed 's/^/    (stderr) /'
  printf '    [exit %s]\n' "$rc"
  [[ "$rc" == "0" ]] || die "$label failed inside the box (exit $rc)"
}

show_exec "identity"   'echo "hostname=$(cat /etc/hostname)"; echo "whoami=$(whoami)"; echo "pwd=$(pwd)"; echo "pid1=$(cat /proc/1/comm)"'
show_exec "toolchain"  'echo "node=$(node -v)"; echo "python=$(python3 -V)"; echo "go=$(go version)"; echo "git=$(git --version)"; echo "docker-server=$(docker info --format "{{.ServerVersion}}")"'
show_exec "isolation"  'echo "the box sees its own root:"; ls / | tr "\n" " "; echo; echo "and its own tmp:"; ls -a /tmp | tr "\n" " "; echo'
show_exec "write file" "mkdir -p /srv/app && printf '%s\\n' '$RUN_MARKER' > /srv/app/marker.txt && cat /srv/app/marker.txt"
show_exec "volume"     "printf '%s\\n' 'volume-$RUN_MARKER' > /data/notes.txt && ls -l /data && cat /data/notes.txt"

# The toolchain assertions are separate from the printout, and deliberately so.
# The readiness canary writes `go=$(go version 2>&1)`, so a MISSING binary still
# yields a non-empty field: the canary would report a complete toolchain whose
# answer is the text "go: not found". These checks ask the question the canary
# cannot.
for tool in "node -v" "python3 -V" "go version" "git --version" "psql --version" "docker info --format {{.ServerVersion}}"; do
  out="$(boxexec "$tool >/dev/null 2>&1 && echo present || echo MISSING")"
  got="$(printf '%s' "$out" | jqish stdout | tr -d '[:space:]')"
  printf '  toolchain: %-42s %s\n' "$tool" "$got"
  [[ "$got" == "present" ]] || die "the warm toolchain is incomplete inside the box: '$tool' is not usable"
done

# The box's PID namespace is its own: a host process must not be visible.
HOSTPROC="$(boxexec 'ls /proc | grep -cE "^[0-9]+$"' | jqish stdout | tr -d '[:space:]')"
info "processes visible inside the box: $HOSTPROC (the host has $(ls /proc | grep -cE '^[0-9]+$'))"
[[ "$HOSTPROC" -lt 50 ]] || die "the box sees the host's process table; the PID namespace is not doing its job"

# ---------------------------------------------------------------------------
step "the box's OWN door — cmd/box-broker, with the control plane off the path"
# This is D6 made real rather than described. The step above went through
# POST /api/v1/box/session/exec, which is OUR process: it works, it is authenticated
# by the box's own credential, and it is still a path on which the customer's
# commands traverse the control plane. The product's promise is that they do not, so
# the box runs its own endpoint and that is what the agent connects to.
#
# What is asserted here, and why each one:
#   * box up published the BOX's URL, not ours, and said so in `available`
#   * the listener is a process INSIDE the box's PID namespace and its own root
#   * MCP tools/list on that endpoint advertises run_command — the verb our surface
#     deliberately does not have
#   * a command run through it produces output from inside the box
#   * a wrong credential is refused by the box, not by us

MCP_URL="$(printf '%s' "$UP" | jqish connect.mcp.url)"
MCP_AVAILABLE="$(printf '%s' "$UP" | jqish connect.mcp.available)"
info "connect.mcp.url       = $MCP_URL"
info "connect.mcp.available = $MCP_AVAILABLE"
[[ "$MCP_AVAILABLE" == "true" ]] || die "box up did not report an endpoint of the box's own (available=$MCP_AVAILABLE)"
case "$MCP_URL" in
  */api/v1/box/session/mcp) die "the published MCP URL is the control-plane fallback, not the box's own door" ;;
  http://*/mcp) : ;;
  *) die "the published MCP URL has an unexpected shape: $MCP_URL" ;;
esac
BROKER_BASE="${MCP_URL%/mcp}"

# The listener really is the box's process. Read from inside the box, so this is the
# box's own view of its own process table rather than a claim made from the host.
BROKER_PID="$(boxexec 'cat /run/dada-broker/broker.pid' | jqish stdout | tr -d '[:space:]')"
[[ -n "$BROKER_PID" ]] || die "the box has no broker pid file"
BROKER_COMM="$(boxexec "cat /proc/$BROKER_PID/comm 2>/dev/null" | jqish stdout | tr -d '[:space:]')"
[[ "$BROKER_COMM" == "box-broker" ]] || die "pid $BROKER_PID inside the box is '$BROKER_COMM', not box-broker"
info "the endpoint is pid $BROKER_PID inside the box, comm=$BROKER_COMM"
# Its root is the box's root: /proc/<pid>/root/etc/dada/root-marker exists only in
# this box's tree, so reading it proves the process is confined to this box and not
# a host process that happens to be listening.
BROKER_ROOT_MARKER="$(boxexec "cat /proc/$BROKER_PID/root/etc/dada/root-marker 2>/dev/null" | jqish stdout | tr -d '[:space:]')"
[[ -n "$BROKER_ROOT_MARKER" ]] || die "the broker's root carries no box marker; it may be running on the host"
info "the endpoint's root marker: $BROKER_ROOT_MARKER (the box's own tree)"

# The binary came from the read-only bind, under /run — machine-owned, so ADR-019
# excludes it from the userland a crystallization carries.
BROKER_MOUNT="$(boxexec 'grep -c " /run/dada-broker/bin " /proc/mounts' | jqish stdout | tr -d '[:space:]')"
[[ "$BROKER_MOUNT" == "1" ]] || die "/run/dada-broker/bin is not a mount inside the box"
BROKER_RO="$(boxexec 'touch /run/dada-broker/bin/probe 2>/dev/null && echo WRITABLE || echo readonly' | jqish stdout | tr -d '[:space:]')"
[[ "$BROKER_RO" == "readonly" ]] || die "the broker binary directory is writable inside the box"
info "/run/dada-broker/bin is a read-only bind inside the box — the tenant cannot replace its own door"

# tools/list on the BOX's endpoint. The tool that is absent from our MCP surface on
# purpose is the tool that is present here.
MCP_TOOLS="$(curl -sS -X POST "$MCP_URL" -H "Authorization: Bearer $BOX_TOKEN" \
  -H 'Content-Type: application/json' \
  -d '{"jsonrpc":"2.0","id":1,"method":"tools/list"}')"
printf '%s\n' "$MCP_TOOLS" | pretty | sed 's/^/  /'
printf '%s' "$MCP_TOOLS" | grep -q '"run_command"' \
  || die "the box's MCP surface does not advertise run_command"
info "the box advertises run_command; our control-plane surface has no such tool and no keep-list entry that could create one"

# And it runs. The marker is generated on the host and must come back out of the
# box, so a canned response cannot pass this.
BOX_INSTANCE_HOSTNAME="$(boxexec 'cat /etc/hostname' | jqish stdout | tr -d '[:space:]')"
[[ -n "$BOX_INSTANCE_HOSTNAME" ]] || die "the box has no hostname"
DOOR_MARKER="door-$RUN_MARKER"
MCP_CALL="$(curl -sS -X POST "$MCP_URL" -H "Authorization: Bearer $BOX_TOKEN" \
  -H 'Content-Type: application/json' \
  -d "{\"jsonrpc\":\"2.0\",\"id\":2,\"method\":\"tools/call\",\"params\":{\"name\":\"run_command\",\"arguments\":{\"command\":\"echo $DOOR_MARKER; cat /etc/hostname; echo served_by=\$(cat /run/dada-broker/addr)\"}}}")"
printf '%s\n' "$MCP_CALL" | pretty | sed 's/^/  /'
printf '%s' "$MCP_CALL" | grep -q "$DOOR_MARKER" \
  || die "the box's endpoint did not return the marker the host generated"
# The tool result is JSON nested inside a JSON string, so both of these match the
# escaped form on the wire rather than a pretty-printed one.
printf '%s' "$MCP_CALL" | grep -q "box.*$BOX_NAME" \
  || die "the box's endpoint did not name this box in its response"
# Note what the box's /etc/hostname actually is: the instance ref, not the box name.
# Asserted as such rather than glossed — a check whose label says "hostname" while it
# matches a different field is a check that will pass for the wrong reason later.
printf '%s' "$MCP_CALL" | grep -q "$BOX_INSTANCE_HOSTNAME" \
  || die "the box's endpoint did not return this box's own /etc/hostname ($BOX_INSTANCE_HOSTNAME)"
info "a command ran inside the box through the box's own endpoint — the control plane was not on that path"

# The env an attach injects is visible through this door too, so the box's own
# endpoint is not a second-class path with a different environment. (Checked again
# after the attach step, below.)
DOOR_EXEC="$(curl -sS -X POST "$BROKER_BASE/exec" -H "X-Box-Token: $BOX_TOKEN" \
  -H 'Content-Type: application/json' -d '{"command":"id -u; pwd"}')"
DOOR_EXIT="$(printf '%s' "$DOOR_EXEC" | jqish exit_code)"
[[ "$DOOR_EXIT" == "0" ]] || die "POST /exec on the box's endpoint exited $DOOR_EXIT"
info "POST $BROKER_BASE/exec -> exit 0, served_by=$(printf '%s' "$DOOR_EXEC" | jqish served_by)"

# A wrong credential is refused BY THE BOX. The refusal has to come from the box,
# because a door that delegates its own authentication to the control plane has not
# moved anything.
BAD_STATUS="$(curl -sS -o /dev/null -w '%{http_code}' -X POST "$BROKER_BASE/exec" \
  -H "X-Box-Token: dadabox_definitelynotthetoken" \
  -H 'Content-Type: application/json' -d '{"command":"echo nope"}')"
[[ "$BAD_STATUS" == "401" ]] || die "the box's endpoint answered $BAD_STATUS to a wrong credential, expected 401"
info "a wrong credential gets 401 from the box itself"
NOAUTH_STATUS="$(curl -sS -o /dev/null -w '%{http_code}' -X POST "$BROKER_BASE/exec" \
  -H 'Content-Type: application/json' -d '{"command":"echo nope"}')"
[[ "$NOAUTH_STATUS" == "401" ]] || die "the box's endpoint answered $NOAUTH_STATUS to a request with no credential, expected 401"
info "no credential gets 401 as well"

# ---------------------------------------------------------------------------
step "attach db — a REAL managed Postgres database, reachable from inside the box"
ATTACH="$(api POST "/api/v1/projects/$PROJECT_ID/boxes/$BOX_NAME/attach/database" \
  '{"name":"app","env_prefix":""}')"
printf '%s\n' "$ATTACH" | pretty | sed 's/^/  /'
ATTACH_STATUS="$(printf '%s' "$ATTACH" | jqish attachment.status)"
ATTACH_DB="$(printf '%s' "$ATTACH" | jqish attachment.resource_name)"
[[ "$ATTACH_STATUS" == "Attached" ]] || die "attach db did not report Attached: $ATTACH"
info "database $ATTACH_DB exists on the managed cluster, OUTSIDE the box:"
psql -h "$DB_HOST" -p "$DB_PORT" -U "$DB_USER" -d postgres -tAc \
  "SELECT datname FROM pg_database WHERE datname = '$ATTACH_DB'" | sed 's/^/    /'
psql -h "$DB_HOST" -p "$DB_PORT" -U "$DB_USER" -d postgres -tAc \
  "SELECT datname FROM pg_database WHERE datname = '$ATTACH_DB'" | grep -q "$ATTACH_DB" \
  || die "the attach reported success but no such database exists"

# The response must never carry the credential values.
if printf '%s' "$ATTACH" | grep -qE 'postgres://[^"]*:[^"@]+@'; then
  die "the attach response leaked a DSN with a password in it"
fi
info "the response carries env key NAMES only, never values — confirmed"

info "the box's env file is 0600 and root-owned:"
show_exec "env perms" 'stat -c "%n mode=%a owner=%U" /etc/dada/box.env'

printf '\n  THE PROOF: psql "$DATABASE_URL" -c '"'"'select 1'"'"' FROM INSIDE THE BOX\n'
show_exec "psql select 1" 'psql "$DATABASE_URL" -c "select 1"'
show_exec "psql write"    'psql "$DATABASE_URL" -v ON_ERROR_STOP=1 -tAc "create table if not exists walk(t text)" && psql "$DATABASE_URL" -v ON_ERROR_STOP=1 -tAc "insert into walk values ('"'"'written from inside the box'"'"')" && psql "$DATABASE_URL" -tAc "select t from walk"'

# The SAME credential through the box's OWN door. Asserted rather than assumed: the
# two doors source the box's env with the same prelude, and if they ever diverged
# the box's own endpoint would become a second-class path where the customer's
# attached database is simply missing.
DOOR_DB="$(curl -sS -X POST "$BROKER_BASE/exec" -H "X-Box-Token: $BOX_TOKEN" \
  -H 'Content-Type: application/json' \
  -d '{"command":"psql \"$DATABASE_URL\" -tAc \"select t from walk\""}')"
DOOR_DB_EXIT="$(printf '%s' "$DOOR_DB" | jqish exit_code)"
[[ "$DOOR_DB_EXIT" == "0" ]] || die "the attached database is not reachable through the box's own door (exit $DOOR_DB_EXIT)"
printf '  through the BOX'"'"'S OWN door: psql "$DATABASE_URL" -> %s\n' \
  "$(printf '%s' "$DOOR_DB" | jqish stdout | tr -d '\n')"

# ---------------------------------------------------------------------------
step "start a server INSIDE the box, then expose it and get a real 200"
APP_SRC=$(cat <<PY
import http.server, socketserver, pathlib
MARKER = pathlib.Path("/srv/app/marker.txt").read_text().strip()
NOTES  = pathlib.Path("/data/notes.txt").read_text().strip()
class H(http.server.BaseHTTPRequestHandler):
    def do_GET(self):
        body = ("dada-box demo app\n"
                "marker: %s\n"
                "volume: %s\n" % (MARKER, NOTES)).encode()
        self.send_response(200)
        self.send_header("Content-Type", "text/plain; charset=utf-8")
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)
    def log_message(self, *a): pass
socketserver.TCPServer.allow_reuse_address = True
socketserver.TCPServer(("0.0.0.0", $APP_PORT), H).serve_forever()
PY
)
# The app is written into the box through the box's own door, so its source really
# is part of the box's filesystem — which is what crystallization later carries.
WRITE_APP="$(python3 -c '
import json,sys
print(json.dumps({"command": "cat > /srv/app/server.py <<\"EOF\"\n" + sys.stdin.read() + "\nEOF\necho wrote $(wc -l < /srv/app/server.py) lines"}))
' <<<"$APP_SRC" | curl -sS -X POST "$BASE/api/v1/box/session/exec" \
    -H "X-Dada-Box-Token: $BOX_TOKEN" -H 'Content-Type: application/json' --data-binary @-)"
printf '  wrote the app inside the box: %s [exit %s]\n' \
  "$(printf '%s' "$WRITE_APP" | jqish stdout | tr -d '\n')" \
  "$(printf '%s' "$WRITE_APP" | jqish exit_code)"
[[ "$(printf '%s' "$WRITE_APP" | jqish exit_code)" == "0" ]] || die "could not write the app inside the box"

SVC="$(python3 -c '
import json,sys
print(json.dumps({"command":"python3 /srv/app/server.py","background":True,
                  "service_name":"demo-app","ports":[int(sys.argv[1])],"working_dir":"/srv/app"}))
' "$APP_PORT" | curl -sS -X POST "$BASE/api/v1/box/session/exec" \
    -H "X-Dada-Box-Token: $BOX_TOKEN" -H 'Content-Type: application/json' --data-binary @-)"
printf '%s\n' "$SVC" | pretty | sed 's/^/  /'
if [[ "$(printf '%s' "$SVC" | jqish listening)" != "[$APP_PORT]" ]]; then
  # A service that did not come up must show WHY in the transcript, or the next
  # person re-runs the walk to learn what the walk already knew.
  printf "  the box's own log for the service:\n"
  boxexec 'cat /var/log/demo-app.log 2>&1; echo "--- the file it was asked to run ---"; head -30 /srv/app/server.py' \
    | jqish stdout | sed 's/^/    /'
  die "the service did not come up listening on $APP_PORT inside the box"
fi
info "the service descriptor now lives INSIDE the box; crystallization reads it:"
show_exec "descriptor" 'cat /etc/dada/services/demo-app.json'

EXPOSE="$(api POST "/api/v1/projects/$PROJECT_ID/boxes/$BOX_NAME/expose" "{\"port\":$APP_PORT}")"
printf '%s\n' "$EXPOSE" | pretty | sed 's/^/  /'
EXP_URL="$(printf '%s' "$EXPOSE" | jqish exposure.url)"
EXP_HOST="$(printf '%s' "$EXPOSE" | jqish exposure.hostname)"
[[ -n "$EXP_URL" ]] || die "expose returned no URL"
info "platform-assigned hostname: $EXP_HOST (the caller cannot choose it)"

printf '\n  THE PROOF: curl against the exposed address\n'
CURL_OUT="$(curl -sS -D- -H "Host: $EXP_HOST" "$EXP_URL")"
printf '%s\n' "$CURL_OUT" | sed 's/^/    /'
printf '%s' "$CURL_OUT" | head -1 | grep -q ' 200 ' || die "the exposed port did not answer 200"
printf '%s' "$CURL_OUT" | grep -qi 'X-Robots-Tag: noindex' || die "the edge did not set X-Robots-Tag: noindex"
# The body has to carry this run's marker, which only exists because a command
# earlier in this walk wrote it inside this box. A 200 alone could come from any
# process that happened to hold the port.
printf '%s' "$CURL_OUT" | grep -q "$RUN_MARKER" \
  || die "the 200 did not carry this run's marker, so it is not evidence about this box"
info "HTTP 200 through the platform edge, and the body carries this run's marker —"
info "so it was served by the process inside this box and read this box's filesystem"

# ---------------------------------------------------------------------------
step "crystallize — materialize the box's userland onto a separate root, per ADR-019"
info "the consent gate first: without ack_monthly_charge the answer must be 409"
NOACK_CODE="$(curl -sS -o /tmp/box-walk-noack.json -w '%{http_code}' \
  -X POST "$BASE/api/v1/projects/$PROJECT_ID/boxes/$BOX_NAME/crystallize" \
  -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' \
  -d '{"app_server_name":"walk-vm"}')"
info "POST .../crystallize without consent -> HTTP $NOACK_CODE"
cat /tmp/box-walk-noack.json | pretty | sed 's/^/    /'
[[ "$NOACK_CODE" == "409" ]] || die "crystallize without ack_monthly_charge must be 409, got $NOACK_CODE"

CRYST="$(api POST "/api/v1/projects/$PROJECT_ID/boxes/$BOX_NAME/crystallize" \
  '{"app_server_name":"walk-vm","domain":"walk-vm.dada-tuda.ru","ack_monthly_charge":true,"probe_path":"/"}')"
VERIFIED="$(printf '%s' "$CRYST" | jqish verified)"
printf '\n'
printf '%s' "$CRYST" | jqish report_text | sed 's/^/  /'
printf '\n'
[[ "$VERIFIED" == "true" ]] || {
  printf '%s\n' "$CRYST" | pretty | sed 's/^/  /'
  die "crystallization verification FAILED"
}
info "verified=true — manifest equality, socket-set equality, env digests, volumes and the HTTP probe all held"

# The probe body must carry this run's marker AND the volume's content. That is
# what makes it a statement about the crystallized VM: the marker file and the
# volume file were both written inside the box, and the VM's process could only
# read them from a filesystem the materialization produced.
PROBE_BODY="$(printf '%s' "$CRYST" | jqish report.probe.body)"
printf '%s' "$PROBE_BODY" | grep -q "$RUN_MARKER" \
  || die "the crystallized VM's response does not carry this run's marker: $PROBE_BODY"
printf '%s' "$PROBE_BODY" | grep -q "volume-$RUN_MARKER" \
  || die "the crystallized VM's response does not carry the restored volume's content: $PROBE_BODY"
info "the VM's own response contains both the marker file and the restored volume file"

# The carry manifest must declare no loss: that is the only critical box alert.
CARRY="$(printf '%s' "$CRYST" | jqish report.carry)"
info "carry manifest: $CARRY"
printf '%s' "$CARRY" | grep -q '"lost"' && die "the carry manifest reports lost state"
info "no state reported lost (dada_box_crystallize_state_loss_total stays at zero)"

# THE VM CARRIES NO DOOR. The broker binary and the box's credential digests live
# under /run, which ADR-019 excludes as machine-owned — so a crystallized VM has
# neither. That is the correct outcome and not an accident of the copy: a permanent
# VM is not an ephemeral body an agent claims, and carrying a live box token onto
# one would extend that credential past the life of the box it was minted for.
VM_ROOT="$BOX_LOCAL_ROOT/vms/walk-vm/root"
[[ -d "$VM_ROOT" ]] || die "the crystallized VM root $VM_ROOT does not exist"
if [[ -e "$VM_ROOT/run/dada-broker" ]]; then
  die "the crystallized VM carries $VM_ROOT/run/dada-broker — a box credential reached a permanent machine"
fi
CARRIED_BROKER="$(find "$VM_ROOT" -name 'box-broker' -o -name 'tokens' -path '*dada-broker*' 2>/dev/null | head -5)"
[[ -z "$CARRIED_BROKER" ]] || die "the crystallized VM carries broker files: $CARRIED_BROKER"
info "the crystallized VM contains no broker binary and no box credential — checked on the VM's real filesystem"

info "the stored report is re-readable afterwards:"
api GET "/api/v1/projects/$PROJECT_ID/boxes/$BOX_NAME/crystallizations" \
  | jqish crystallizations.0.verified | sed 's/^/    verified=/'

# ---------------------------------------------------------------------------
step "delete the box — every live session is revoked BEFORE the enqueue"
BEFORE_SESSIONS="$(psql -h "$DB_HOST" -p "$DB_PORT" -U "$DB_USER" -d "$DB_NAME" -tAc \
  "SELECT count(*) FROM box_sessions WHERE revoked_at IS NULL")"
info "live sessions before delete: $BEFORE_SESSIONS"
DEL="$(api DELETE "/api/v1/projects/$PROJECT_ID/boxes/$BOX_NAME")"
printf '%s\n' "$DEL" | pretty | sed 's/^/  /'
AFTER_SESSIONS="$(psql -h "$DB_HOST" -p "$DB_PORT" -U "$DB_USER" -d "$DB_NAME" -tAc \
  "SELECT count(*) FROM box_sessions WHERE revoked_at IS NULL")"
info "live sessions after delete: $AFTER_SESSIONS"
[[ "$AFTER_SESSIONS" == "0" ]] || die "sessions survived the delete"
DEAD="$(boxexec 'echo should-not-run')"
printf '  the revoked token now gets: %s\n' "$(printf '%s' "$DEAD" | tr -d '\n')"
printf '%s' "$DEAD" | grep -qi 'unauthorized\|error' || die "a revoked session token still worked"

# AND THE BOX'S OWN DOOR IS SHUT. This is the assertion that would have caught the
# whole point of the door being somewhere else: revoking a session in our table does
# nothing to a listener inside the box, so the revocation has to reach the digest
# file the box authenticates against. Without this check a "revoked" credential would
# keep working on exactly the path the control plane is deliberately not on.
DOOR_DEAD="$(curl -sS -o /dev/null -w '%{http_code}' -X POST "$BROKER_BASE/exec" \
  -H "X-Box-Token: $BOX_TOKEN" -H 'Content-Type: application/json' \
  -d '{"command":"echo should-not-run"}')"
printf '  the revoked token on the BOX'"'"'S OWN door gets: HTTP %s\n' "$DOOR_DEAD"
[[ "$DOOR_DEAD" == "401" ]] || die "the box's own door still accepts a revoked credential (HTTP $DOOR_DEAD)"

# ---------------------------------------------------------------------------
step "the measured numbers"
info "these come from box.PhaseTimeline, which has no method that accepts a"
info "caller-supplied timestamp — a guest-reported instant cannot enter the"
info "measurement even by accident."
printf '\n'
printf '  TIME TO READY (T0 admission -> T1 canary exit status): %s ms\n' "$TIME_TO_READY_MS"
printf '  pool: %s   budget: %s ms\n' "$POOL_LABEL" "$(printf '%s' "$UP" | jqish ready.budget_ms)"
printf '  per phase (ms): %s\n' "$(printf '%s' "$UP" | jqish ready.phase_ms)"
printf '  attach to a usable credential: %s ms\n' "$(printf '%s' "$ATTACH" | jqish attach_ms)"
printf '  expose to the first real 200:  %s ms\n' "$(printf '%s' "$EXPOSE" | jqish expose_ms)"
printf '  crystallize end to end:        %s ms\n' "$(printf '%s' "$CRYST" | jqish report.duration_ms)"
printf '\n'
hr
printf 'WALK COMPLETE — every step above ran against the real HTTP API.\n'
printf 'finished %s\n' "$(date -u +%Y-%m-%dT%H:%M:%SZ)"
hr
