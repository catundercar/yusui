#!/usr/bin/env bash
# Build the YuSui DEMO environment on local OrbStack: the Agent is the project's
# only NetBird peer (a real Linux VM); the assets are sshd machines *behind* the
# agent, reached only via the agent's per-ticket forwarder over a real overlay.
#
#   ┌──────────────┐ assetnet (sshd containers — the machines behind the agent)
#   │ yusui-agent  │──┬─ demo-prod-db   (prod-db)
#   │  VM: netbird │  └─ demo-prod-app  (prod-app)
#   │  + agent     │            ▲ reached only through the agent forwarder
#   └─────┬────────┘            │
#         │ real WireGuard overlay (100.x)
#   ┌─────┴───────────────────────────────────────────────┐
#   │ host docker: demo-server(netbird peer) + yusui-server│  ← browser :8091
#   │              + postgres + web ; netbird ctrl :8081   │
#   └──────────────────────────────────────────────────────┘
#
# Assets are containers (not VMs) for a snappy terminal: OrbStack's VM<->VM
# virtual NIC batches ~25% of small interactive packets ~200ms; the agent reaches
# containers cleanly. Faithfulness note in README. The agent stays a real VM.
#
# Prereqs: orb, docker, and the NetBird control plane up + admin seeded
#   (cd deploy/netbird && ./gen-config.sh && docker compose up -d && ./seed-admin.sh)
# Idempotent-ish: re-running recreates the docker side + assets and re-seeds; the
# agent VM is reused.
set -uo pipefail
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
NB_IP="${NETBIRD_HOST_IP:-$(ipconfig getifaddr en0 2>/dev/null || hostname -I | awk '{print $1}')}"
NB_URL="http://${NB_IP}:8081"
WEB_PORT="${DEMO_WEB_PORT:-8091}"
REGTOK=demotok
NET=demonet
PROJECT=demo
ADMIN_PW='Admin12345!@'
ASSET_USER=ops-yusui ; ASSET_PW=hunter2

say() { printf '\n\033[1;36m== %s ==\033[0m\n' "$*"; }
need() { command -v "$1" >/dev/null || { echo "missing: $1"; exit 1; }; }
need orb; need docker; need curl

say "0/7  setup key from the NetBird control plane ($NB_URL)"
PAT=$("$ROOT/deploy/netbird/bootstrap-token.sh" 2>/dev/null) || { echo "NetBird control plane not up? run deploy/netbird first"; exit 1; }
SK=$(curl -s -H "Authorization: Token $PAT" -H 'content-type: application/json' -X POST "$NB_URL/api/setup-keys" \
  -d '{"name":"demo","type":"reusable","expires_in":604800,"usage_limit":0,"ephemeral":false}' \
  | python3 -c 'import sys,json;print(json.load(sys.stdin).get("key",""))')
[ -n "$SK" ] || { echo "could not mint a setup key"; exit 1; }

# ---- VMs ---------------------------------------------------------------------
have_vm() { orb list 2>/dev/null | awk '{print $1}' | grep -qx "$1"; }
vm_ip() { orb -m "$1" bash -c "ip -4 -o addr show eth0 2>/dev/null | awk '{print \$4}' | cut -d/ -f1" 2>/dev/null; }

say "1/7  asset machines behind the agent (sshd containers on isolated assetnet)"
# Asset machines are CONTAINERS, not VMs: OrbStack's VM<->VM virtual NIC batches
# ~25% of small interactive packets ~200ms, which made the terminal feel laggy
# (a local-virtualization artifact, not YuSui — see README). The agent reaches
# containers cleanly. They sit on their own 'assetnet'.
ASSET_PORT=2222
docker network rm assetnet >/dev/null 2>&1; docker network create assetnet >/dev/null
mkasset_ctr() { # $1 container  $2 hostname
  docker rm -f "$1" >/dev/null 2>&1
  docker run -d --name "$1" --hostname "$2" --network assetnet \
    -e PASSWORD_ACCESS=true -e USER_NAME="$ASSET_USER" -e USER_PASSWORD="$ASSET_PW" -e PUID=1000 -e PGID=1000 \
    linuxserver/openssh-server >/dev/null
}
mkasset_ctr demo-prod-db  prod-db
mkasset_ctr demo-prod-app prod-app
for c in demo-prod-db demo-prod-app; do for _ in $(seq 1 30); do docker exec "$c" sh -c 'nc -z localhost 2222' 2>/dev/null && break; sleep 1; done; done
A1_IP=$(docker inspect demo-prod-db  --format '{{(index .NetworkSettings.Networks "assetnet").IPAddress}}')
A2_IP=$(docker inspect demo-prod-app --format '{{(index .NetworkSettings.Networks "assetnet").IPAddress}}')
echo "  prod-db=$A1_IP  prod-app=$A2_IP  (sshd :$ASSET_PORT)"

say "2/7  agent VM (netbird daemon joins the overlay)"
have_vm yusui-agent || orb create ubuntu:noble yusui-agent >/dev/null 2>&1
orb -m yusui-agent sudo bash -c "
  command -v netbird >/dev/null || curl -fsSL https://pkgs.netbird.io/install.sh | sh >/dev/null 2>&1
  systemctl enable netbird >/dev/null 2>&1
  netbird up --setup-key '$SK' --management-url $NB_URL --hostname yusui-agent >/dev/null 2>&1
  for _ in \$(seq 1 20); do ip -4 addr show wt0 2>/dev/null | grep -q inet && break; sleep 2; done" 2>&1 | tail -0
AGT_OIP=$(orb -m yusui-agent bash -c "ip -4 -o addr show wt0 | awk '{print \$4}' | cut -d/ -f1")
AGT_IP=$(vm_ip yusui-agent)
echo "  agent overlay IP=$AGT_OIP  private IP=$AGT_IP"

say "3/7  isolation note"
# On a single OrbStack host the underlying network is flat, so the server can
# also reach the asset containers at L3. The DEMO's access fact is that YuSui
# routes via the agent's per-ticket forwarder over the overlay (forward_addr =
# the agent's overlay IP). In production the overlay + a genuinely private subnet
# enforce "the server cannot reach assets directly" (invariant #1) — see README.
echo "  assets on assetnet; access path = server -> overlay -> agent forwarder -> asset"

# ---- server side (host docker; netbird peer + grpc gateway + web) -----------
say "4/7  server side (postgres + netbird peer + yusui-server + web)"
for c in demo-web demo-yusui-srv demo-server demo-pg; do docker rm -f "$c" >/dev/null 2>&1; done
docker network rm "$NET" >/dev/null 2>&1; docker network create "$NET" >/dev/null
docker run -d --name demo-pg --network "$NET" -e POSTGRES_PASSWORD=postgres -e POSTGRES_DB=yusui \
  -e YUSUI_MIGRATE_PASSWORD=migratesecret -e YUSUI_APP_PASSWORD=appsecret \
  -v "$ROOT/deploy/postgres/init:/docker-entrypoint-initdb.d:ro" postgres:16 >/dev/null
for _ in $(seq 1 30); do docker exec demo-pg pg_isready -U postgres >/dev/null 2>&1 && break; sleep 1; done
docker run --rm --network "$NET" -e DATABASE_URL="postgres://yusui_migrate:migratesecret@demo-pg:5432/yusui?sslmode=disable" yusui-server:latest migrate >/dev/null 2>&1
# the netbird peer is named 'server' so the stock web image's nginx (proxy_pass http://server:8080) resolves it
docker run -d --name demo-server --network "$NET" --network-alias server --cap-add NET_ADMIN --device /dev/net/tun \
  -e NB_SETUP_KEY="$SK" -e NB_MANAGEMENT_URL="$NB_URL" -e NB_HOSTNAME=yusui-server netbirdio/netbird:latest >/dev/null
for _ in $(seq 1 30); do docker exec demo-server sh -c 'ip -4 addr show wt0 2>/dev/null | grep -q inet' && break; sleep 2; done
SRV_OIP=$(docker exec demo-server sh -c "ip -4 -o addr show wt0 | awk '{print \$4}' | cut -d/ -f1")
docker run -d --name demo-yusui-srv --network "container:demo-server" \
  -e DATABASE_URL="postgres://yusui_app:appsecret@demo-pg:5432/yusui?sslmode=disable" -e HTTP_ADDR=":8080" \
  -e JWT_SECRET=demosecret -e ADMIN_PASSWORD="$ADMIN_PW" -e CREDENTIAL_KEY=demokey -e RECORDINGS_DIR=/dev/shm/rec \
  -e AGENT_GATEWAY=grpc -e AGENT_GRPC_ADDR="0.0.0.0:9091" -e AGENT_REGISTER_TOKEN="$REGTOK" -e SERVER_PEER_IPS="$SRV_OIP" \
  yusui-server:latest serve >/dev/null
docker run -d --name demo-web --network "$NET" -p "$WEB_PORT:80" yusui-web:latest >/dev/null
for _ in $(seq 1 30); do curl -fsS "http://localhost:$WEB_PORT/" >/dev/null 2>&1 && break; sleep 1; done
echo "  server overlay IP=$SRV_OIP  web=http://localhost:$WEB_PORT"

say "5/7  run the yusui-agent in the agent VM (overlay mode → server $SRV_OIP:9091)"
orb -m yusui-agent sudo systemctl stop yusui-agent 2>/dev/null  # release the binary (Text file busy on re-run)
cat /tmp/yusui-dist/yusui-agent-linux-arm64 2>/dev/null | orb -m yusui-agent sudo bash -c 'cat > /usr/local/bin/yusui-agent && chmod +x /usr/local/bin/yusui-agent' 2>/dev/null \
  || { (cd "$ROOT/agent" && CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -o /tmp/yusui-agent-arm64 ./cmd/yusui-agent) && cat /tmp/yusui-agent-arm64 | orb -m yusui-agent sudo bash -c 'cat > /usr/local/bin/yusui-agent && chmod +x /usr/local/bin/yusui-agent'; }
orb -m yusui-agent sudo bash -c "cat > /etc/systemd/system/yusui-agent.service <<UNIT
[Unit]
Description=YuSui Agent
After=netbird.service network-online.target
Wants=netbird.service
[Service]
Environment=YUSUI_SERVER_GRPC=$SRV_OIP:9091
Environment=YUSUI_PROJECT=$PROJECT
Environment=YUSUI_REGISTER_TOKEN=$REGTOK
Environment=YUSUI_HOSTNAME=yusui-agent
Environment=YUSUI_ENFORCER=forward
Environment=YUSUI_OVERLAY=netbird
Environment=YUSUI_NB_IFACE=wt0
ExecStart=/usr/local/bin/yusui-agent
Restart=always
RestartSec=3
[Install]
WantedBy=multi-user.target
UNIT
systemctl daemon-reload; systemctl enable --now yusui-agent >/dev/null 2>&1; systemctl restart yusui-agent" 2>&1 | tail -0

say "6/7  seed the catalog (project, approve the agent, assets, credentials)"
API="http://localhost:$WEB_PORT"; j() { python3 -c 'import sys,json;print(json.load(sys.stdin)[sys.argv[1]])' "$1"; }
ADM=$(curl -s -X POST $API/api/v1/auth/login -H 'content-type: application/json' -d "{\"username\":\"admin\",\"password\":\"$ADMIN_PW\"}" | j access_token)
H=(-H "Authorization: Bearer $ADM" -H 'content-type: application/json')
curl -s "${H[@]}" -X POST $API/api/v1/users -d '{"username":"req1","role":"requester","password":"Req12345!@xy"}' >/dev/null
curl -s "${H[@]}" -X POST $API/api/v1/users -d '{"username":"appr1","role":"approver","password":"Appr12345!@xy"}' >/dev/null
PID=$(curl -s "${H[@]}" -X POST $API/api/v1/projects -d '{"code":"'"$PROJECT"'","name":"Demo Project","cidrs":["192.168.139.0/24"]}' | j id 2>/dev/null) || \
  PID=$(curl -s "${H[@]}" $API/api/v1/projects | python3 -c 'import sys,json;print([p["id"] for p in json.load(sys.stdin) if p["code"]=="'"$PROJECT"'"][0])')
for _ in $(seq 1 40); do n=$(curl -s "${H[@]}" $API/api/v1/agents | python3 -c 'import sys,json;print(len(json.load(sys.stdin)))'); [ "$n" -ge 1 ] && break; sleep 2; done
AGID=$(curl -s "${H[@]}" $API/api/v1/agents | python3 -c 'import sys,json;a=json.load(sys.stdin);print(a[0]["id"])')
curl -s "${H[@]}" -X POST $API/api/v1/agents/$AGID/approve >/dev/null
mkasset() { local id; id=$(curl -s "${H[@]}" -X POST $API/api/v1/assets -d "{\"project_id\":$PID,\"name\":\"$1\",\"ip_internal\":\"$2\",\"ports\":[$ASSET_PORT]}" | j id 2>/dev/null); [ -n "$id" ] && curl -s "${H[@]}" -X POST $API/api/v1/assets/$id/credentials -d "{\"ssh_user\":\"$ASSET_USER\",\"auth_kind\":\"password\",\"secret\":\"$ASSET_PW\"}" >/dev/null; }
mkasset prod-db "$A1_IP"; mkasset prod-app "$A2_IP"
for _ in $(seq 1 20); do docker logs demo-yusui-srv 2>&1 | grep -q 'agent stream connected' && break; sleep 2; done

say "7/7  ready"
cat <<EOF

  Open YuSui:   http://localhost:$WEB_PORT
  Login:        admin / $ADMIN_PW   (also req1 / Req12345!@xy, appr1 / Appr12345!@xy)
  Project:      $PROJECT   Assets: prod-db ($A1_IP), prod-app ($A2_IP)  · SSH $ASSET_USER/$ASSET_PW
  Overlay:      server=$SRV_OIP  agent=$AGT_OIP   (NetBird ctrl: $NB_URL)

  Demo: submit a ticket for prod-db → approve (login as appr1) → open the Web
  terminal. The server reaches prod-db only via the agent's forwarder over the
  overlay; \`rm -rf /\` is blocked, \`whoami\` returns $ASSET_USER. Tear down: ./down.sh
EOF
