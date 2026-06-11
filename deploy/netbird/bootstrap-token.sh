#!/usr/bin/env bash
# Mint a NetBird admin PAT (nbp_…) for the YuSui adapter — fully non-interactive,
# no browser. Drives the embedded Dex auth-code flow with the seeded admin
# (run ./seed-admin.sh first), exchanges the code for a JWT, then creates a PAT
# via the Management API. Prints the PAT on stdout.
#
#   PAT=$(./bootstrap-token.sh)
#   NETBIRD_MGMT_URL=http://<ip>:8081 NETBIRD_TOKEN=$PAT \
#     go test ./server/internal/netbird -run TestLiveContract -v
#
# Requires: curl, python3. LOCAL DEV ONLY.
set -euo pipefail
cd "$(dirname "${BASH_SOURCE[0]}")"

IP="${NETBIRD_HOST_IP:-$(ipconfig getifaddr en0 2>/dev/null || ipconfig getifaddr en1 2>/dev/null || hostname -I 2>/dev/null | awk '{print $1}')}"
PORT="${NETBIRD_MGMT_PORT:-8081}"
B="http://${IP}:${PORT}"
EMAIL="${ADMIN_EMAIL:-admin@yusui.local}"
PASSWORD="${ADMIN_PASSWORD:-YuSuiAdmin123!}"
RU="http://localhost:53000/"
CJ="$(mktemp)"; trap 'rm -f "$CJ"' EXIT

jget() { python3 -c 'import sys,json;print(json.load(sys.stdin).get(sys.argv[1],""))' "$1"; }
abs() { case "$1" in http*) echo "$1";; *) echo "$B$1";; esac; }  # redirect_url is absolute via IP, relative via localhost

# auth-code flow against the embedded Dex local connector
L1=$(curl -s -c "$CJ" -o /dev/null -w '%{redirect_url}' "$B/oauth2/auth?client_id=netbird-cli&response_type=code&scope=openid+email+profile&redirect_uri=$RU&state=s1&nonce=nn1")
LOGIN=$(curl -s -c "$CJ" -b "$CJ" -o /dev/null -w '%{redirect_url}' "$(abs "$L1")")
ST=$(echo "$LOGIN" | sed -n 's/.*state=\([^&]*\).*/\1/p')
curl -s -c "$CJ" -b "$CJ" "$(abs "$LOGIN")" -o /dev/null
CODELOC=$(curl -s -c "$CJ" -b "$CJ" -o /dev/null -w '%{redirect_url}' \
  --data-urlencode "login=$EMAIL" --data-urlencode "password=$PASSWORD" \
  "$B/oauth2/auth/local/login?back=&state=$ST")
CODE=$(echo "$CODELOC" | sed -n 's/.*[?&]code=\([^&]*\).*/\1/p')
[ -n "$CODE" ] || { echo "login failed (run ./seed-admin.sh?)" >&2; exit 1; }

JWT=$(curl -s -b "$CJ" -X POST "$B/oauth2/token" \
  -d grant_type=authorization_code -d "code=$CODE" -d "redirect_uri=$RU" -d client_id=netbird-cli | jget access_token)
[ -n "$JWT" ] || { echo "token exchange failed" >&2; exit 1; }

USER=$(curl -s -H "Authorization: Bearer $JWT" "$B/api/users" | python3 -c 'import sys,json;u=json.load(sys.stdin);print(u[0]["id"] if u else "")')
PAT=$(curl -s -H "Authorization: Bearer $JWT" -H 'content-type: application/json' \
  -X POST "$B/api/users/$USER/tokens" -d '{"name":"yusui-adapter","expires_in":365}' | jget plain_token)
[ -n "$PAT" ] || { echo "PAT creation failed" >&2; exit 1; }
echo "$PAT"
