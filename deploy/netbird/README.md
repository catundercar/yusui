# Local self-hosted NetBird (control plane) for YuSui

A minimal, **local-dev-only** NetBird control plane to exercise YuSui's NetBird
Adapter and `overlay.Netbird` against a real Management API — no public domain,
no TLS. Production NetBird must run behind TLS (docs/04); this is for integration
testing on one machine.

## What it runs

One combined `netbirdio/netbird-server` container (Management + Signal + Relay +
STUN + **embedded Dex IdP**) on host **:8081**, plus the dashboard on **:8090**
(8080 is the YuSui server). All of management, `/api`, and `/oauth2` (Dex) are
served on the one port — no reverse proxy.

## Bring up

```bash
cd deploy/netbird
./gen-config.sh                 # renders config.yaml + dashboard.env for your host LAN IP
docker compose up -d
# verify: OIDC discovery + API (401 == alive)
curl -fsS  http://<host-ip>:8081/oauth2/.well-known/openid-configuration | jq .issuer
curl -s -o /dev/null -w '%{http_code}\n' http://<host-ip>:8081/api/groups   # 401
```

`gen-config.sh` uses the host LAN IP (so the OIDC issuer resolves identically
from the host and from peer containers) and generates the relay/store secrets
once. The rendered `config.yaml` / `dashboard.env` are gitignored (host-specific
+ secret).

## Get an admin API token (PAT) for the YuSui adapter

The embedded Dex has no default user and no CLI to mint a **user** PAT (the
`netbird-server token create` CLI only makes service/proxy tokens, which the
`/api` endpoints reject). So, once:

1. Open the dashboard `http://<host-ip>:8090`, sign in via the embedded IdP
   (first login provisions the admin account).
2. Settings → **Personal Access Tokens** → create one (`nbp_…`).
3. Feed it to the YuSui server adapter:
   ```
   NETBIRD_ENABLED=true
   NETBIRD_MGMT_URL=http://<host-ip>:8081
   NETBIRD_TOKEN=nbp_xxx
   ```
   The adapter installs the one permanent policy `yusui:builtin:server-to-agents`
   at startup (docs/04). Also create a **setup key** (Setup Keys tab) for
   enrolling the Server + Agent peers.

## Tear down

```bash
docker compose down        # keep the store
docker compose down -v     # wipe the store (fresh install next time)
```

## Status / scope

- ✅ Control plane verified up locally: embedded Dex OIDC discovery + `/api`
  (401 without auth) respond.
- ⏳ Adapter contract test (real `EnsureGroup`/`EnsureBuiltinPolicy` + setup-key
  issuance) needs the one-time PAT above.
- ⏳ Data plane (`overlay.Netbird`: Server + Agent as real WireGuard peers) needs
  the netbird client daemon with a TUN device in a container — viability on
  macOS/OrbStack is the open risk.
