#!/usr/bin/env bash
# Full-overlay e2e: the draft10 path over a REAL NetBird overlay (not loopback).
# Server and Agent are each a NetBird peer (yusui process sharing a netbird-daemon
# container's netns). Proves: server dials the Agent's forwarder at the Agent's
# OVERLAY IP, over WireGuard, and it relays to the asset — with the command filter.
#
# Requires: the local NetBird control plane up (deploy/netbird/ + seed-admin.sh),
# docker, the cross-compiled linux/arm64 binaries in /tmp (ovl-server/agent/wstest).
set -uo pipefail
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
NB_IP="${NETBIRD_HOST_IP:-$(ipconfig getifaddr en0 2>/dev/null || hostname -I | awk '{print $1}')}"
NB_URL="http://${NB_IP}:8081"
REGTOK=devtok
NET=ovlnet
BASE=alpine:3.20
SRVLOG=/tmp/ovl-srv.log; AGTLOG=/tmp/ovl-agt.log; WSTOUT=/tmp/ovl-wst.out

cleanup() {
  for c in ovl-yusui-srv ovl-yusui-agt ovl-nb-srv ovl-nb-agt ovl-pg ovl-sshd ovl-migrate; do docker rm -f "$c" >/dev/null 2>&1; done
  docker network rm "$NET" >/dev/null 2>&1
}
trap cleanup EXIT
cleanup

echo "== mint setup key =="
PAT=$("$ROOT/deploy/netbird/bootstrap-token.sh" 2>/dev/null)
SK=$(curl -s -H "Authorization: Token $PAT" -H 'content-type: application/json' -X POST "$NB_URL/api/setup-keys" \
  -d '{"name":"ovl-e2e","type":"reusable","expires_in":86400,"usage_limit":0,"ephemeral":false}' \
  | python3 -c 'import sys,json;print(json.load(sys.stdin).get("key",""))')
[ -n "$SK" ] || { echo "no setup key (is deploy/netbird up + seeded?)"; exit 1; }
echo "setup key ${SK:0:8}…"

docker network create "$NET" >/dev/null 2>&1

echo "== postgres + sshd asset =="
docker run -d --name ovl-pg --network "$NET" -e POSTGRES_PASSWORD=postgres -e POSTGRES_DB=yusui postgres:16 >/dev/null
docker run -d --name ovl-sshd --network "$NET" -e PASSWORD_ACCESS=true -e USER_NAME=ops-yusui -e USER_PASSWORD=hunter2 \
  -e PUID=1000 -e PGID=1000 linuxserver/openssh-server >/dev/null
for _ in $(seq 1 30); do docker exec ovl-pg pg_isready -U postgres >/dev/null 2>&1 && break; sleep 1; done
ASSET_IP=$(docker inspect -f '{{(index .NetworkSettings.Networks "'"$NET"'").IPAddress}}' ovl-sshd)
PG_IP=$(docker inspect -f '{{(index .NetworkSettings.Networks "'"$NET"'").IPAddress}}' ovl-pg)
echo "asset $ASSET_IP  pg $PG_IP"

# roles + migrate
docker exec ovl-pg psql -U postgres -d yusui -v ON_ERROR_STOP=1 \
  -c "DO \$\$ BEGIN IF NOT EXISTS (SELECT FROM pg_roles WHERE rolname='yusui_migrate') THEN CREATE ROLE yusui_migrate LOGIN PASSWORD 'm'; END IF; END \$\$;" \
  -c "DO \$\$ BEGIN IF NOT EXISTS (SELECT FROM pg_roles WHERE rolname='yusui_app') THEN CREATE ROLE yusui_app LOGIN PASSWORD 'a'; END IF; END \$\$;" \
  -c "GRANT CREATE,CONNECT,TEMPORARY ON DATABASE yusui TO yusui_migrate; GRANT CONNECT ON DATABASE yusui TO yusui_app; GRANT CREATE ON SCHEMA public TO yusui_migrate;" >/dev/null 2>&1
docker run --rm --name ovl-migrate --network "$NET" -v /tmp/ovl-server:/server:ro \
  -e DATABASE_URL="postgres://yusui_migrate:m@$PG_IP:5432/yusui?sslmode=disable" "$BASE" /server migrate >/dev/null 2>&1

echo "== netbird daemons (server-peer + agent-peer) =="
nbup() { # $1=name
  docker run -d --name "$1" --network "$NET" --cap-add NET_ADMIN --device /dev/net/tun "${@:2}" \
    -e NB_SETUP_KEY="$SK" -e NB_MANAGEMENT_URL="$NB_URL" -e NB_HOSTNAME="$1" netbirdio/netbird:latest >/dev/null
}
nbup ovl-nb-srv -p 8089:8089 -p 9091:9091   # publish the yusui-server ports via the netns owner
nbup ovl-nb-agt
ovip() { for _ in $(seq 1 30); do ip=$(docker exec "$1" sh -c "ip -4 -o addr show wt0 2>/dev/null | awk '{print \$4}' | cut -d/ -f1"); [ -n "$ip" ] && { echo "$ip"; return; }; sleep 2; done; }
SRV_OIP=$(ovip ovl-nb-srv); AGT_OIP=$(ovip ovl-nb-agt)
SRV_NET_IP=$(docker inspect -f '{{(index .NetworkSettings.Networks "'"$NET"'").IPAddress}}' ovl-nb-srv)
echo "server overlay IP $SRV_OIP ($NET $SRV_NET_IP)   agent overlay IP $AGT_OIP"
[ -n "$SRV_OIP" ] && [ -n "$AGT_OIP" ] || { echo "peers did not get overlay IPs"; exit 1; }

echo "== yusui-server (in server-peer netns) =="
docker run -d --name ovl-yusui-srv --network "container:ovl-nb-srv" -v /tmp/ovl-server:/server:ro \
  -e DATABASE_URL="postgres://yusui_app:a@$PG_IP:5432/yusui?sslmode=disable" -e HTTP_ADDR=":8089" \
  -e JWT_SECRET=devsecret -e ADMIN_PASSWORD='Admin12345!@' -e CREDENTIAL_KEY=devkey -e RECORDINGS_DIR=/tmp/rec \
  -e AGENT_GATEWAY=grpc -e AGENT_GRPC_ADDR="0.0.0.0:9091" -e AGENT_REGISTER_TOKEN="$REGTOK" -e SERVER_PEER_IPS="$SRV_OIP" \
  "$BASE" /server serve >/dev/null
for _ in $(seq 1 30); do curl -fsS http://localhost:8089/healthz >/dev/null 2>&1 && break; sleep 1; done

echo "== yusui-agent (in agent-peer netns; YUSUI_OVERLAY=netbird) =="
docker run -d --name ovl-yusui-agt --network "container:ovl-nb-agt" -v /tmp/ovl-agent:/agent:ro \
  -e YUSUI_SERVER_GRPC="$SRV_OIP:9091" -e YUSUI_PROJECT=alpha -e YUSUI_REGISTER_TOKEN="$REGTOK" -e YUSUI_HOSTNAME=alpha-agent \
  -e YUSUI_ENFORCER=forward -e YUSUI_OVERLAY=netbird -e YUSUI_NB_IFACE=wt0 \
  "$BASE" /agent >/dev/null

echo "== bootstrap + approve agent =="
j() { python3 -c 'import sys,json;print(json.load(sys.stdin)[sys.argv[1]])' "$1"; }
ADM=$(curl -fsS -X POST http://localhost:8089/api/v1/auth/login -H 'content-type: application/json' -d '{"username":"admin","password":"Admin12345!@"}' | j access_token)
A=(-H "Authorization: Bearer $ADM" -H 'content-type: application/json')
curl -fsS "${A[@]}" -X POST http://localhost:8089/api/v1/users -d '{"username":"req1","role":"requester","password":"Req12345!@xy"}' >/dev/null
curl -fsS "${A[@]}" -X POST http://localhost:8089/api/v1/users -d '{"username":"appr1","role":"approver","password":"Appr12345!@xy"}' >/dev/null
PID=$(curl -fsS "${A[@]}" -X POST http://localhost:8089/api/v1/projects -d '{"code":"alpha","name":"Alpha","cidrs":["10.0.0.0/8"]}' | j id)
AID=$(curl -fsS "${A[@]}" -X POST http://localhost:8089/api/v1/assets -d "{\"project_id\":$PID,\"name\":\"sshd\",\"ip_internal\":\"$ASSET_IP\",\"ports\":[2222]}" | j id)
curl -fsS "${A[@]}" -X POST http://localhost:8089/api/v1/assets/$AID/credentials -d '{"ssh_user":"ops-yusui","auth_kind":"password","secret":"hunter2"}' >/dev/null
for _ in $(seq 1 30); do grep -qi 'awaiting admin approval' "$AGTLOG" 2>/dev/null || docker logs ovl-yusui-agt 2>&1 | grep -qi 'awaiting admin approval' && break; sleep 1; done
AGID=$(curl -fsS "${A[@]}" http://localhost:8089/api/v1/agents | python3 -c 'import sys,json;a=json.load(sys.stdin);print(a[0]["id"] if a else "")')
ENR=$(curl -fsS "${A[@]}" -X POST http://localhost:8089/api/v1/agents/$AGID/approve | j enrollment)
echo "agent id=$AGID approved=$ENR"
for _ in $(seq 1 40); do docker logs ovl-yusui-agt 2>&1 | grep -q 'control stream open' && break; sleep 1; done

echo "== ticket =="
REQ=$(curl -fsS -X POST http://localhost:8089/api/v1/auth/login -H 'content-type: application/json' -d '{"username":"req1","password":"Req12345!@xy"}' | j access_token)
TID=$(curl -fsS -H "Authorization: Bearer $REQ" -H 'content-type: application/json' -X POST http://localhost:8089/api/v1/tickets -d "{\"project_id\":$PID,\"asset_ids\":[$AID],\"ports\":[2222],\"reason\":\"overlay e2e\",\"duration_sec\":600}" | j id)
APR=$(curl -fsS -X POST http://localhost:8089/api/v1/auth/login -H 'content-type: application/json' -d '{"username":"appr1","password":"Appr12345!@xy"}' | j access_token)
ST=$(curl -fsS -H "Authorization: Bearer $APR" -H 'content-type: application/json' -X POST http://localhost:8089/api/v1/tickets/$TID/approve | j status)
echo "ticket $TID status=$ST"

echo "== drive Web Shell over the overlay =="
docker run --rm --network "$NET" -v /tmp/ovl-wstest:/wstest:ro "$BASE" /wstest "ws://$SRV_NET_IP:8089/api/v1/ws/tickets/$TID/terminal?access_token=$ADM" "$ADM" >"$WSTOUT" 2>&1
cat "$WSTOUT"
docker logs ovl-yusui-srv > "$SRVLOG" 2>&1; docker logs ovl-yusui-agt > "$AGTLOG" 2>&1

echo "===== ASSERTIONS ====="
pass=1; check() { if eval "$2"; then echo "  PASS: $1"; else echo "  FAIL: $1"; pass=0; fi; }
check "agent approved"                                 "[ '$ENR' = approved ]"
check "ticket active"                                  "[ '$ST' = active ]"
check "agent forwarder bound on its OVERLAY IP"        "grep -qi 'forwarder up' '$AGTLOG' && grep -q '$AGT_OIP' '$AGTLOG'"
check "server dialed asset via the forwarder (overlay)" "grep -qi 'dialing asset via agent forwarder' '$SRVLOG'"
check "whoami relayed over the overlay"                "grep -q 'OUTPUT_HAS_USER=true' '$WSTOUT'"
check "command filter blocked rm -rf / over overlay"   "grep -q 'GOT_BLOCK=true' '$WSTOUT'"
if [ "$pass" = 1 ]; then echo "OVERLAY E2E: PASS"; else echo "OVERLAY E2E: FAIL"; exit 1; fi
