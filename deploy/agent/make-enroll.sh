#!/usr/bin/env bash
# Admin-side: produce ONE enrollment token + a ready-to-paste install command for
# a new agent. It mints a reusable NetBird setup key from the control plane and
# bundles it with the YuSui gRPC address, project, and register token, so the
# operator on the target machine runs a single command.
#
#   ./make-enroll.sh --server 100.x:9091 --project demo --register-token <tok> \
#       --mgmt-url https://nb.example --pat nbp_xxx [--hostname web-1]
#
# Get the NetBird PAT from your control plane (self-hosted: deploy/netbird/bootstrap-token.sh).
set -euo pipefail

SERVER="" PROJECT="" REGTOKEN="" MGMTURL="" PAT="" AGENT_HOST=""
RAW_INSTALL="https://raw.githubusercontent.com/catundercar/yusui/main/deploy/agent/install-agent.sh"
die() { echo "error: $*" >&2; exit 1; }

while [ $# -gt 0 ]; do
  case "$1" in
    --server) SERVER="${2:-}"; shift 2;;
    --project) PROJECT="${2:-}"; shift 2;;
    --register-token) REGTOKEN="${2:-}"; shift 2;;
    --mgmt-url) MGMTURL="${2:-}"; shift 2;;
    --pat) PAT="${2:-}"; shift 2;;
    --hostname) AGENT_HOST="${2:-}"; shift 2;;
    *) die "unknown flag: $1";;
  esac
done
for v in SERVER:--server PROJECT:--project REGTOKEN:--register-token MGMTURL:--mgmt-url PAT:--pat; do
  name=${v%%:*}; flag=${v#*:}; [ -n "${!name}" ] || die "missing $flag"
done
command -v python3 >/dev/null || die "python3 required"

# Mint a reusable setup key so this token can enroll the agent (and survive re-installs).
KEY=$(curl -fsS -H "Authorization: Token $PAT" -H 'content-type: application/json' \
  -X POST "$MGMTURL/api/setup-keys" \
  -d '{"name":"yusui-'"$PROJECT"'","type":"reusable","expires_in":604800,"usage_limit":0,"ephemeral":false}' \
  | python3 -c 'import sys,json;print(json.load(sys.stdin).get("key",""))') || die "setup-key API call failed"
[ -n "$KEY" ] || die "could not mint setup key (check --pat / --mgmt-url)"

bundle="server=$SERVER;project=$PROJECT;register_token=$REGTOKEN;netbird_key=$KEY;mgmt_url=$MGMTURL"
[ -n "$AGENT_HOST" ] && bundle="$bundle;hostname=$AGENT_HOST"
TOKEN=$(printf '%s' "$bundle" | base64 | tr -d '\n')

cat <<OUT

Enrollment token (valid while the setup key lives):
  $TOKEN

Run this on the target machine (Linux, as root):
  curl -fsSL $RAW_INSTALL | sudo bash -s -- --enroll $TOKEN

Then approve the agent in the YuSui UI → 资源管理 → Agent → 批准.
OUT
