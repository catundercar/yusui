#!/usr/bin/env bash
# Render config.yaml + dashboard.env from the .tmpl files for a LOCAL NetBird.
#
# The embedded Dex OIDC issuer URL must resolve identically from the host and
# from peer containers, so we use the host's LAN IP (not 127.0.0.1) + the
# published management port (8081). Secrets are generated once and preserved on
# re-run. Both rendered files are gitignored (host-specific + secret).
set -euo pipefail
cd "$(dirname "${BASH_SOURCE[0]}")"

MGMT_PORT="${NETBIRD_MGMT_PORT:-8081}"
DASH_PORT="${NETBIRD_DASHBOARD_PORT:-8090}"

# Host IP: explicit override, else first non-loopback IPv4.
if [ -n "${NETBIRD_HOST_IP:-}" ]; then
  IP="$NETBIRD_HOST_IP"
elif command -v ipconfig >/dev/null 2>&1; then
  IP="$(ipconfig getifaddr en0 2>/dev/null || ipconfig getifaddr en1 2>/dev/null)"
else
  IP="$(hostname -I 2>/dev/null | awk '{print $1}')"
fi
[ -n "$IP" ] || { echo "could not detect host IP; set NETBIRD_HOST_IP" >&2; exit 1; }

NB_DOMAIN="${IP}:${MGMT_PORT}"
NB_DASHBOARD="${IP}:${DASH_PORT}"

# Preserve existing secrets so re-rendering doesn't invalidate the store.
if [ -f config.yaml ]; then
  RELAY_SECRET="$(grep -E '^\s*authSecret:' config.yaml | sed -E 's/.*"(.*)".*/\1/')"
  ENC_KEY="$(grep -E '^\s*encryptionKey:' config.yaml | sed -E 's/.*"(.*)".*/\1/')"
fi
RELAY_SECRET="${RELAY_SECRET:-$(openssl rand -base64 32 | tr -d '=')}"
ENC_KEY="${ENC_KEY:-$(openssl rand -base64 32)}"

render() { # $1=tmpl $2=out
  sed -e "s|__NB_DOMAIN__|${NB_DOMAIN}|g" \
      -e "s|__NB_DASHBOARD__|${NB_DASHBOARD}|g" \
      -e "s|__RELAY_SECRET__|${RELAY_SECRET}|g" \
      -e "s|__ENC_KEY__|${ENC_KEY}|g" \
      "$1" > "$2"
}
render config.yaml.tmpl config.yaml
render dashboard.env.tmpl dashboard.env

echo "rendered config.yaml + dashboard.env for ${NB_DOMAIN} (dashboard ${NB_DASHBOARD})"
echo "next: docker compose up -d  (see README.md)"
