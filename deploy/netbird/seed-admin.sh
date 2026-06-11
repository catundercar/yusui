#!/usr/bin/env bash
# Seed a known admin into the combined server's embedded Dex IdP, so you can log
# into the dashboard with fixed credentials (→ create a PAT + setup keys) instead
# of figuring out the embedded IdP's sign-up. LOCAL DEV ONLY.
#
# The embedded IdP stores static-password users in Dex's `password` table inside
# /var/lib/netbird/idp.db. There's no CLI for it, so this stops the server, edits
# the SQLite file, and restarts. Requires: docker, sqlite3, htpasswd.
#
#   ./seed-admin.sh [email] [password]      # defaults: admin@yusui.local / YuSuiAdmin123!
set -euo pipefail
cd "$(dirname "${BASH_SOURCE[0]}")"

EMAIL="${1:-admin@yusui.local}"
PASSWORD="${2:-YuSuiAdmin123!}"
CT=yusui-nb-server

for bin in sqlite3 htpasswd docker; do
  command -v "$bin" >/dev/null 2>&1 || { echo "need '$bin' on PATH" >&2; exit 1; }
done

# Dex expects a bcrypt hash; htpasswd -B emits $2y$, normalize to the canonical $2a$.
HASH=$(htpasswd -bnBC 10 "" "$PASSWORD" | tr -d ':\n' | sed 's/^\$2y/\$2a/')

TMP=$(mktemp /tmp/nb-idp.XXXXXX.db)
trap 'rm -f "$TMP"' EXIT

docker compose stop netbird-server >/dev/null
docker cp "$CT":/var/lib/netbird/idp.db "$TMP"
sqlite3 "$TMP" "INSERT OR REPLACE INTO password
  (email,hash,username,user_id,preferred_username,groups,name,email_verified)
  VALUES ('$EMAIL', CAST('$HASH' AS BLOB), 'admin','yusui-admin','admin','[]','YuSui Admin',1);"
docker cp "$TMP" "$CT":/var/lib/netbird/idp.db
docker compose start netbird-server >/dev/null

echo "seeded admin: $EMAIL / $PASSWORD"
echo "log in at the dashboard, then create a Personal Access Token (see README.md)."
