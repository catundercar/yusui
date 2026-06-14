#!/usr/bin/env bash
# YuSui agent installer — one command to install + enroll an agent on a Linux host.
#
#   curl -fsSL <raw-url>/install-agent.sh | sudo bash -s -- --enroll <TOKEN>
#   # or with explicit flags:
#   sudo ./install-agent.sh --server 100.x:9091 --project demo \
#        --register-token <tok> --netbird-key <K> --mgmt-url https://nb.example
#
# It installs the NetBird daemon (if missing) and the yusui-agent binary, writes
# /etc/yusui/agent.env + a systemd unit, and starts it. The agent itself brings
# NetBird up (netbird up --setup-key) and registers as PENDING — an admin then
# approves it in the YuSui UI. Idempotent (safe to re-run).
set -euo pipefail

SERVER="" PROJECT="" REGTOKEN="" NBKEY="" MGMTURL="" AGENT_HOST="" VERSION="latest" BINARY="" ENROLL=""
REPO="catundercar/yusui"

die() { echo "error: $*" >&2; exit 1; }
usage() {
  cat >&2 <<'U'
usage: install-agent.sh (--enroll <token> | explicit flags)
  --enroll <b64>          one bundled token from `make-enroll.sh` (recommended)
  --server <ip:9091>      YuSui gRPC address (the server's overlay IP)
  --project <code>        project this agent belongs to (must already exist)
  --register-token <tok>  YuSui register token (= server AGENT_REGISTER_TOKEN)
  --netbird-key <K>       NetBird setup key (the agent uses it to join the overlay)
  --mgmt-url <url>        NetBird management URL
  --hostname <h>          display name (default: this host's hostname)
  --version <v>           release tag to download (default: latest)
  --binary <path>         use a local yusui-agent binary instead of downloading
U
  exit "${1:-1}"
}

while [ $# -gt 0 ]; do
  case "$1" in
    --enroll) ENROLL="${2:-}"; shift 2;;
    --server) SERVER="${2:-}"; shift 2;;
    --project) PROJECT="${2:-}"; shift 2;;
    --register-token) REGTOKEN="${2:-}"; shift 2;;
    --netbird-key) NBKEY="${2:-}"; shift 2;;
    --mgmt-url) MGMTURL="${2:-}"; shift 2;;
    --hostname) AGENT_HOST="${2:-}"; shift 2;;
    --version) VERSION="${2:-}"; shift 2;;
    --binary) BINARY="${2:-}"; shift 2;;
    -h|--help) usage 0;;
    *) echo "unknown flag: $1" >&2; usage 1;;
  esac
done

# A bundled --enroll token expands to the same fields (base64 of key=val;key=val).
if [ -n "$ENROLL" ]; then
  decoded=$(printf '%s' "$ENROLL" | base64 -d 2>/dev/null) || die "invalid --enroll token"
  IFS=';' read -ra _kvs <<< "$decoded"
  for kv in "${_kvs[@]}"; do
    case "${kv%%=*}" in
      server) SERVER="${kv#*=}";; project) PROJECT="${kv#*=}";;
      register_token) REGTOKEN="${kv#*=}";; netbird_key) NBKEY="${kv#*=}";;
      mgmt_url) MGMTURL="${kv#*=}";; hostname) AGENT_HOST="${kv#*=}";;
    esac
  done
fi

[ "$(id -u)" = 0 ] || die "run as root (sudo)"
[ -n "$SERVER" ]   || { echo "missing --server" >&2; usage 1; }
[ -n "$PROJECT" ]  || { echo "missing --project" >&2; usage 1; }
[ -n "$REGTOKEN" ] || { echo "missing --register-token" >&2; usage 1; }
[ -n "$NBKEY" ]    || { echo "missing --netbird-key" >&2; usage 1; }
[ -n "$MGMTURL" ]  || { echo "missing --mgmt-url" >&2; usage 1; }
[ -n "$AGENT_HOST" ] || AGENT_HOST=$(hostname)

case "$(uname -m)" in
  x86_64|amd64) ARCH=amd64;;
  aarch64|arm64) ARCH=arm64;;
  *) die "unsupported arch: $(uname -m)";;
esac

echo "==> NetBird daemon"
if command -v netbird >/dev/null 2>&1; then
  echo "    already installed"
else
  echo "    installing via pkgs.netbird.io"
  curl -fsSL https://pkgs.netbird.io/install.sh | sh
fi

echo "==> yusui-agent binary"
if [ -n "$BINARY" ]; then
  install -m 0755 "$BINARY" /usr/local/bin/yusui-agent
  echo "    installed from $BINARY"
else
  if [ "$VERSION" = latest ]; then rel="latest/download"; else rel="download/$VERSION"; fi
  url="https://github.com/$REPO/releases/$rel/yusui-agent-linux-$ARCH"
  echo "    downloading $url"
  tmp=$(mktemp)
  curl -fSL "$url" -o "$tmp" \
    || die "download failed — publish a release (make agent-dist && gh release create ...) or pass --binary <path>"
  install -m 0755 "$tmp" /usr/local/bin/yusui-agent; rm -f "$tmp"
fi

echo "==> /etc/yusui/agent.env"
install -d -m 0750 /etc/yusui
cat > /etc/yusui/agent.env <<ENV
YUSUI_SERVER_GRPC=$SERVER
YUSUI_PROJECT=$PROJECT
YUSUI_REGISTER_TOKEN=$REGTOKEN
YUSUI_HOSTNAME=$AGENT_HOST
YUSUI_ENFORCER=forward
YUSUI_OVERLAY=netbird
YUSUI_NB_IFACE=wt0
YUSUI_NB_SETUP_KEY=$NBKEY
YUSUI_NB_MGMT_URL=$MGMTURL
ENV
chmod 0640 /etc/yusui/agent.env

echo "==> systemd unit"
cat > /etc/systemd/system/yusui-agent.service <<'UNIT'
[Unit]
Description=YuSui Agent
After=network-online.target netbird.service
Wants=network-online.target
[Service]
EnvironmentFile=/etc/yusui/agent.env
ExecStart=/usr/local/bin/yusui-agent
Restart=always
RestartSec=3
[Install]
WantedBy=multi-user.target
UNIT

systemctl daemon-reload
systemctl enable --now yusui-agent

cat <<DONE

✓ yusui-agent installed and started
    hostname : $AGENT_HOST
    project  : $PROJECT
    logs     : journalctl -u yusui-agent -f
    next     : approve it in the YuSui UI → 资源管理 → Agent → 批准
DONE
