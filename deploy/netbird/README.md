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
`/api` endpoints reject). Seed a known admin so the dashboard login is trivial:

```bash
./seed-admin.sh                 # admin@yusui.local / YuSuiAdmin123! (override via args)
```

Then mint a PAT — either in the browser (dashboard `http://<host-ip>:8090` →
Settings → **Personal Access Tokens**), or fully non-interactively:

```bash
PAT=$(./bootstrap-token.sh)      # scripts the Dex auth-code flow → mints nbp_…
```

Feed it to the YuSui server adapter:
```
NETBIRD_ENABLED=true
NETBIRD_MGMT_URL=http://<host-ip>:8081
NETBIRD_TOKEN=$PAT
```
The adapter installs the one permanent policy `yusui:builtin:server-to-agents`
at startup (docs/04). Also create a **setup key** (dashboard → Setup Keys) for
enrolling the Server + Agent peers.

## Adapter contract test (docs/04 §4.12)

With the stack up + a PAT, the Adapter is verified against the **real**
Management API (request shaping + idempotency), not a mock:

```bash
NETBIRD_MGMT_URL=http://<host-ip>:8081 NETBIRD_TOKEN=$(./bootstrap-token.sh) \
  go test ./server/internal/netbird -run TestLiveContract -v
```

The test skips when those env vars are unset, so it's inert in normal CI but
runnable wherever a NetBird stack exists.

## Tear down

```bash
docker compose down        # keep the store
docker compose down -v     # wipe the store (fresh install next time)
```

## Status / scope

- ✅ Control plane verified up locally: embedded Dex OIDC discovery + `/api`
  (401 without auth) respond.
- ✅ Admin bootstrap + PAT fully scripted (`seed-admin.sh` + `bootstrap-token.sh`,
  no browser).
- ✅ Adapter contract test green against the real Management API
  (`TestLiveContract`: `EnsureGroup` + `EnsureBuiltinPolicy` create + idempotent).
- ✅ Setup-key issuance (adapter `CreateSetupKey`, enrollment P2) — contract test green.
- ✅ Data plane / **full overlay e2e** green: `scripts/e2e-overlay.sh` runs the
  whole draft10 path over a real WireGuard overlay (Server + Agent each a NetBird
  peer; Server dials the Agent's forwarder at its overlay IP → relays to the
  asset, command filter active). Viable on macOS/OrbStack (NET_ADMIN + /dev/net/tun).
- ⏳ Direct daemon-gRPC client (vs the `netbird` CLI) + mint-on-approve wiring (P2b).
