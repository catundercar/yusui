#!/usr/bin/env bash
# e2e-stack.sh up|down — bring up / tear down the full e2e stack so Playwright
# can drive the real critical path: PostgreSQL + an sshd "asset" container +
# the Go server (mock agent gateway) + the web preview (production bundle).
#
# Local:  starts a throwaway brew PostgreSQL@16 on :5440 (trust auth).
# CI:     set PG_EXTERNAL=1 and point PGHOST/PGPORT/PGUSER/PGPASSWORD at a
#         postgres service container; the script only creates db/roles + serves.
#
# Roles always get passwords; trust-auth local PG simply ignores them, so the
# DSNs are identical in both environments.
set -uo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
ACTION="${1:-up}"

H="${PGHOST:-127.0.0.1}"
PORT="${PGPORT:-5440}"
SUPER="${PGUSER:-postgres}"
API_PORT="${API_PORT:-8088}"
WEB_PORT="${WEB_PORT:-5173}"
PG_EXTERNAL="${PG_EXTERNAL:-0}"

PGDATA=/tmp/yusui-e2e-pg
REC=/tmp/yusui-e2e-rec
BIN=/tmp/yusui-e2e-bin
PIDDIR=/tmp/yusui-e2e-pids
SSHD_NAME=yusui-e2e-sshd

APP_DSN="postgres://yusui_app:appsecret@${H}:${PORT}/yusui?sslmode=disable"
MIGRATE_DSN="postgres://yusui_migrate:migratesecret@${H}:${PORT}/yusui?sslmode=disable"

# Prefer brew postgresql@16 client/server binaries when present (local macOS).
if command -v brew >/dev/null 2>&1; then
  PGBIN="$(brew --prefix postgresql@16 2>/dev/null)/bin"
  [ -d "$PGBIN" ] && export PATH="$PGBIN:$PATH"
fi

psuper() { PGPASSWORD="${PGPASSWORD:-}" psql -h "$H" -p "$PORT" -U "$SUPER" "$@"; }

wait_for() { # wait_for <desc> <cmd...>
  local desc="$1"; shift
  for _ in $(seq 1 60); do "$@" >/dev/null 2>&1 && return 0; sleep 1; done
  echo "timeout waiting for $desc" >&2; return 1
}

down() {
  [ -f "$PIDDIR/server.pid" ] && kill "$(cat "$PIDDIR/server.pid")" 2>/dev/null
  [ -f "$PIDDIR/web.pid" ] && kill "$(cat "$PIDDIR/web.pid")" 2>/dev/null
  lsof -ti tcp:"$API_PORT" 2>/dev/null | xargs -r kill -9 2>/dev/null
  lsof -ti tcp:"$WEB_PORT" 2>/dev/null | xargs -r kill -9 2>/dev/null
  docker rm -f "$SSHD_NAME" >/dev/null 2>&1
  if [ "$PG_EXTERNAL" != "1" ]; then
    pg_ctl -D "$PGDATA" stop -m fast >/dev/null 2>&1
    rm -rf "$PGDATA"
  fi
  rm -rf "$REC" "$PIDDIR"
  echo "e2e stack down"
}

up() {
  export LC_ALL=C LANG=C
  mkdir -p "$PIDDIR" "$REC"

  echo "== sshd asset container :2222 =="
  docker rm -f "$SSHD_NAME" >/dev/null 2>&1
  docker run -d --name "$SSHD_NAME" -p 2222:2222 \
    -e PASSWORD_ACCESS=true -e USER_NAME=ops-yusui -e USER_PASSWORD=hunter2 -e PUID=1000 -e PGID=1000 \
    linuxserver/openssh-server >/dev/null
  # Wait for the SSH protocol banner, not just an open port — sshd accepts TCP
  # before it can authenticate, which would flake the first connect in CI.
  wait_for "sshd banner" bash -c 'exec 3<>/dev/tcp/127.0.0.1/2222; read -t 3 -u 3 line; [[ "$line" == SSH-* ]]'

  if [ "$PG_EXTERNAL" != "1" ]; then
    echo "== local PostgreSQL :$PORT =="
    pg_ctl -D "$PGDATA" stop -m immediate >/dev/null 2>&1
    rm -rf "$PGDATA"
    initdb -D "$PGDATA" -U "$SUPER" --auth=trust --locale=C --encoding=UTF8 >/dev/null
    pg_ctl -D "$PGDATA" -o "-p $PORT -k /tmp -c listen_addresses=$H" -l /tmp/yusui-e2e-pg.log start
    wait_for "pg ready" pg_isready -h "$H" -p "$PORT" -U "$SUPER"
    createdb -h "$H" -p "$PORT" -U "$SUPER" yusui
  else
    echo "== external PostgreSQL $H:$PORT =="
    wait_for "pg ready" pg_isready -h "$H" -p "$PORT" -U "$SUPER"
    psuper -tc "SELECT 1 FROM pg_database WHERE datname='yusui'" | grep -q 1 || psuper -c "CREATE DATABASE yusui"
  fi

  echo "== roles + grants =="
  psuper -v ON_ERROR_STOP=1 \
    -c "DO \$\$ BEGIN IF NOT EXISTS (SELECT FROM pg_roles WHERE rolname='yusui_migrate') THEN CREATE ROLE yusui_migrate LOGIN PASSWORD 'migratesecret'; END IF; END \$\$;" \
    -c "DO \$\$ BEGIN IF NOT EXISTS (SELECT FROM pg_roles WHERE rolname='yusui_app') THEN CREATE ROLE yusui_app LOGIN PASSWORD 'appsecret'; END IF; END \$\$;" \
    -c "GRANT CREATE,CONNECT,TEMPORARY ON DATABASE yusui TO yusui_migrate;" \
    -c "GRANT CONNECT ON DATABASE yusui TO yusui_app;" >/dev/null
  psuper -d yusui -c "GRANT CREATE ON SCHEMA public TO yusui_migrate;" >/dev/null

  echo "== build + migrate + serve (mock gateway) =="
  (cd "$ROOT/server" && go build -o "$BIN" ./cmd/yusui-server) || { echo "server build failed"; exit 1; }
  DATABASE_URL="$MIGRATE_DSN" "$BIN" migrate
  # Seed two PENDING agents (no API creates pending rows — only auto-register
  # does, draft12) so the agent-approve UI spec has something to approve. Two,
  # so the spec stays green under Playwright's CI retry (each run approves one).
  psuper -d yusui -v ON_ERROR_STOP=1 \
    -c "INSERT INTO yusui.projects (code, name, cidrs) VALUES ('seed-enroll','Seed Enrollment','{10.9.0.0/16}') ON CONFLICT (code) DO NOTHING;" \
    -c "INSERT INTO yusui.agents (project_id, role, hostname, enrollment)
        SELECT p.id, r.role, r.host, 'pending'
        FROM yusui.projects p,
             (VALUES ('primary','pending-agent-1'),('secondary','pending-agent-2')) AS r(role,host)
        WHERE p.code='seed-enroll' ON CONFLICT (project_id, role) DO NOTHING;" >/dev/null
  DATABASE_URL="$APP_DSN" HTTP_ADDR=":$API_PORT" \
    JWT_SECRET="e2e-secret" ADMIN_PASSWORD="Admin12345!@" CREDENTIAL_KEY="e2e-credential-key" \
    RECORDINGS_DIR="$REC" AGENT_GATEWAY="mock" \
    "$BIN" serve >/tmp/yusui-e2e-serve.log 2>&1 &
  echo $! > "$PIDDIR/server.pid"
  wait_for "server healthz" curl -fsS "http://127.0.0.1:$API_PORT/healthz"

  echo "== build + preview web :$WEB_PORT =="
  (cd "$ROOT/web" && npm run build) || { echo "web build failed"; exit 1; }
  (cd "$ROOT/web" && npm run preview >/tmp/yusui-e2e-web.log 2>&1 &)
  echo "$!" > "$PIDDIR/web.pid"
  wait_for "web preview" curl -fsS "http://127.0.0.1:$WEB_PORT/"

  echo "e2e stack up — web http://127.0.0.1:$WEB_PORT  api http://127.0.0.1:$API_PORT"
}

case "$ACTION" in
  up) up ;;
  down) down ;;
  *) echo "usage: $0 up|down" >&2; exit 2 ;;
esac
