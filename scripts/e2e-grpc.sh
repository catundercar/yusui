#!/usr/bin/env bash
# draft10 grpc-mode e2e: a REAL yusui-agent (userspace forwarder enforcer) in
# the loop over gRPC, all on loopback (no NetBird needed). Asserts that Web Shell
# dials the agent's forwarder address, which relays to the sshd asset, with the
# command filter active. Run/assert/exit (not up/down).
#
# Local:  starts a throwaway brew PostgreSQL@16. CI: set PG_EXTERNAL=1 + PGHOST/
#         PGPORT/PGUSER/PGPASSWORD (a postgres service container).
set -uo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
H="${PGHOST:-127.0.0.1}"
PORT="${PGPORT:-5441}"
SUPER="${PGUSER:-postgres}"
PG_EXTERNAL="${PG_EXTERNAL:-0}"
API=http://127.0.0.1:8089
GRPC=127.0.0.1:9091
REGTOK=devtok

PGDATA=/tmp/yusui-grpc-pg
REC=/tmp/yusui-grpc-rec
BIN=/tmp/yusui-grpc-srv
AGT=/tmp/yusui-grpc-agt
WST=/tmp/yusui-grpc-wst
SRVLOG=/tmp/yusui-grpc-srv.log
AGTLOG=/tmp/yusui-grpc-agt.log
WSTOUT=/tmp/yusui-grpc-wst.out

if command -v brew >/dev/null 2>&1; then
  PGBIN="$(brew --prefix postgresql@16 2>/dev/null)/bin"
  [ -d "$PGBIN" ] && export PATH="$PGBIN:$PATH"
fi
psuper() { PGPASSWORD="${PGPASSWORD:-}" psql -h "$H" -p "$PORT" -U "$SUPER" "$@"; }

SRVPID=""
AGTPID=""
cleanup() {
  [ -n "$SRVPID" ] && kill "$SRVPID" 2>/dev/null
  [ -n "$AGTPID" ] && kill "$AGTPID" 2>/dev/null
  lsof -ti tcp:8089 2>/dev/null | xargs -r kill -9 2>/dev/null
  lsof -ti tcp:9091 2>/dev/null | xargs -r kill -9 2>/dev/null
  docker rm -f yusui-grpc-sshd >/dev/null 2>&1
  if [ "$PG_EXTERNAL" != "1" ]; then pg_ctl -D "$PGDATA" stop -m fast >/dev/null 2>&1; fi
}
trap cleanup EXIT
cleanup

echo "== sshd asset =="
docker run -d --name yusui-grpc-sshd -p 2222:2222 \
  -e PASSWORD_ACCESS=true -e USER_NAME=ops-yusui -e USER_PASSWORD=hunter2 -e PUID=1000 -e PGID=1000 \
  linuxserver/openssh-server >/dev/null
for _ in $(seq 1 60); do bash -c 'exec 3<>/dev/tcp/127.0.0.1/2222; read -t 3 -u 3 l; [[ "$l" == SSH-* ]]' 2>/dev/null && break; sleep 1; done

echo "== postgres =="
if [ "$PG_EXTERNAL" != "1" ]; then
  export LC_ALL=C LANG=C
  pg_ctl -D "$PGDATA" stop -m immediate >/dev/null 2>&1
  rm -rf "$PGDATA"
  initdb -D "$PGDATA" -U "$SUPER" --auth=trust --locale=C --encoding=UTF8 >/dev/null
  pg_ctl -D "$PGDATA" -o "-p $PORT -k /tmp -c listen_addresses=$H" -l /tmp/yusui-grpc-pg.log start >/dev/null
  for _ in $(seq 1 30); do pg_isready -h "$H" -p "$PORT" -U "$SUPER" >/dev/null 2>&1 && break; sleep 1; done
  createdb -h "$H" -p "$PORT" -U "$SUPER" yusui
else
  for _ in $(seq 1 30); do pg_isready -h "$H" -p "$PORT" -U "$SUPER" >/dev/null 2>&1 && break; sleep 1; done
  psuper -tc "SELECT 1 FROM pg_database WHERE datname='yusui'" | grep -q 1 || psuper -c "CREATE DATABASE yusui"
fi
psuper -v ON_ERROR_STOP=1 \
  -c "DO \$\$ BEGIN IF NOT EXISTS (SELECT FROM pg_roles WHERE rolname='yusui_migrate') THEN CREATE ROLE yusui_migrate LOGIN PASSWORD 'migratesecret'; END IF; END \$\$;" \
  -c "DO \$\$ BEGIN IF NOT EXISTS (SELECT FROM pg_roles WHERE rolname='yusui_app') THEN CREATE ROLE yusui_app LOGIN PASSWORD 'appsecret'; END IF; END \$\$;" \
  -c "GRANT CREATE,CONNECT,TEMPORARY ON DATABASE yusui TO yusui_migrate;" \
  -c "GRANT CONNECT ON DATABASE yusui TO yusui_app;" >/dev/null
psuper -d yusui -c "GRANT CREATE ON SCHEMA public TO yusui_migrate;" >/dev/null

echo "== build =="
rm -rf "$REC"; mkdir -p "$REC"
(cd "$ROOT/server" && go build -o "$BIN" ./cmd/yusui-server && go build -o "$WST" ./cmd/wstest) || { echo "server build failed"; exit 1; }
(cd "$ROOT/agent" && go build -o "$AGT" ./cmd/yusui-agent) || { echo "agent build failed"; exit 1; }

DATABASE_URL="postgres://yusui_migrate:migratesecret@$H:$PORT/yusui?sslmode=disable" "$BIN" migrate >/dev/null 2>&1

echo "== server (AGENT_GATEWAY=grpc) =="
DATABASE_URL="postgres://yusui_app:appsecret@$H:$PORT/yusui?sslmode=disable" HTTP_ADDR=":8089" JWT_SECRET=devsecret \
  ADMIN_PASSWORD='Admin12345!@' CREDENTIAL_KEY=devkey RECORDINGS_DIR="$REC" \
  AGENT_GATEWAY=grpc AGENT_GRPC_ADDR="$GRPC" AGENT_REGISTER_TOKEN="$REGTOK" \
  "$BIN" serve >"$SRVLOG" 2>&1 &
SRVPID=$!
for _ in $(seq 1 30); do curl -fsS $API/healthz >/dev/null 2>&1 && break; sleep 1; done

j() { python3 -c 'import sys,json;print(json.load(sys.stdin)[sys.argv[1]])' "$1"; }
login() { curl -fsS -X POST $API/api/v1/auth/login -H 'content-type: application/json' -d "{\"username\":\"$1\",\"password\":\"$2\"}"; }
ADM=$(login admin 'Admin12345!@' | j access_token)
A=(-H "Authorization: Bearer $ADM" -H 'content-type: application/json')
curl -fsS "${A[@]}" -X POST $API/api/v1/users -d '{"username":"req1","role":"requester","password":"Req12345!@xy"}' >/dev/null
curl -fsS "${A[@]}" -X POST $API/api/v1/users -d '{"username":"appr1","role":"approver","password":"Appr12345!@xy"}' >/dev/null
PID=$(curl -fsS "${A[@]}" -X POST $API/api/v1/projects -d '{"code":"alpha","name":"Alpha","cidrs":["127.0.0.0/8"]}' | j id)
curl -fsS "${A[@]}" -X POST $API/api/v1/agents -d "{\"project_id\":$PID,\"role\":\"primary\",\"hostname\":\"alpha-agent\"}" >/dev/null
AID=$(curl -fsS "${A[@]}" -X POST $API/api/v1/assets -d "{\"project_id\":$PID,\"name\":\"sshd\",\"ip_internal\":\"127.0.0.1\",\"ports\":[2222]}" | j id)
curl -fsS "${A[@]}" -X POST $API/api/v1/assets/$AID/credentials -d '{"ssh_user":"ops-yusui","auth_kind":"password","secret":"hunter2"}' >/dev/null

echo "== real agent (YUSUI_ENFORCER=forward) =="
YUSUI_SERVER_GRPC="$GRPC" YUSUI_PROJECT=alpha YUSUI_REGISTER_TOKEN="$REGTOK" YUSUI_ENFORCER=forward YUSUI_LISTEN_HOST=127.0.0.1 \
  "$AGT" >"$AGTLOG" 2>&1 &
AGTPID=$!
for _ in $(seq 1 30); do grep -q 'control stream open' "$AGTLOG" 2>/dev/null && break; sleep 1; done

echo "== ticket =="
REQ=$(login req1 'Req12345!@xy' | j access_token)
TID=$(curl -fsS -H "Authorization: Bearer $REQ" -H 'content-type: application/json' -X POST $API/api/v1/tickets -d "{\"project_id\":$PID,\"asset_ids\":[$AID],\"ports\":[2222],\"reason\":\"grpc fwd e2e\",\"duration_sec\":600}" | j id)
APR=$(login appr1 'Appr12345!@xy' | j access_token)
ST=$(curl -fsS -H "Authorization: Bearer $APR" -H 'content-type: application/json' -X POST $API/api/v1/tickets/$TID/approve | j status)
echo "ticket $TID status=$ST"

echo "== drive Web Shell (admin) =="
"$WST" "ws://127.0.0.1:8089/api/v1/ws/tickets/$TID/terminal?access_token=$ADM" "$ADM" >"$WSTOUT" 2>&1
cat "$WSTOUT"

echo "===== ASSERTIONS ====="
pass=1
check() { if eval "$2"; then echo "  PASS: $1"; else echo "  FAIL: $1"; pass=0; fi; }
check "agent opened a forwarder listener"            "grep -qi 'forwarder up' '$AGTLOG'"
check "server dialed the asset via the forwarder"    "grep -qi 'dialing asset via agent forwarder' '$SRVLOG'"
check "ticket went active"                           "[ '$ST' = active ]"
check "whoami output relayed back through forwarder" "grep -q 'OUTPUT_HAS_USER=true' '$WSTOUT'"
check "command filter blocked rm -rf / via forwarder" "grep -q 'GOT_BLOCK=true' '$WSTOUT'"
if [ "$pass" = 1 ]; then echo "GRPC FORWARDER E2E: PASS"; else echo "GRPC FORWARDER E2E: FAIL"; exit 1; fi
