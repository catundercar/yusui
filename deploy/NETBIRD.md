# NetBird overlay — deployment runbook

> **draft10 update + verified.** For a runnable LOCAL stack and the verified
> path, see [deploy/netbird/](netbird/) (control plane + adapter contract test +
> `scripts/e2e-overlay.sh`). This file is the conceptual runbook.

YuSui uses NetBird purely as transport (docs §0): only the **Server** and each
project **Agent** are NetBird peers; end users and assets are not. **draft10:** the
Agent is a *plain* peer (it does NOT advertise the project CIDR as a NetBird
route); the Server dials the Agent's overlay IP and the Agent's **userspace L4
forwarder** relays per ticket to the asset. NetBird carries exactly one permanent
policy (`yusui:builtin:server-to-agents`); the per-ticket decision is the Agent
forwarder's existence + source-allowlist, not nftables (nftables is an optional
Linux-only enforcer).

## What the code does
- `server/internal/netbird` (NetBird Adapter, docs/04): REST-only client with
  idempotency-by-`name` and error classification (`ErrTransient/Conflict/Auth/Schema/Permanent`).
  Unit-tested against a mock NetBird API (`adapter_test.go`).
- At startup, when `NETBIRD_ENABLED=true`, the server ensures the permanent
  policy `yusui:builtin:server-to-agents` (server-peers group → agent groups,
  `action=accept`). Best-effort: failure degrades (blocks new onboarding) but
  does not stop the server (docs/01 §1.3). The per-ticket path never calls NetBird.

## Server env
```
NETBIRD_ENABLED=true
NETBIRD_MGMT_URL=https://netbird.example      # NetBird Management base URL
NETBIRD_TOKEN=nbp_xxx                          # NetBird PAT (sent as "Token ...")
SERVER_PEER_IPS=100.92.0.5                      # the Server's overlay IP (ACL source)
AGENT_GATEWAY=grpc
```

## Bring up NetBird (self-host)
Use NetBird's official self-hosted compose (management + signal + coturn +
dashboard + IdP). See https://docs.netbird.io/selfhosted/selfhosted-guide.
Create a setup key (for agents) and a PAT (for the YuSui server adapter).

## Make Server + Agents peers (draft10)
- **Server**: runs the NetBird client (its overlay IP → `SERVER_PEER_IPS`, the
  ACL source). The Server dials Agents at their overlay IPs.
- **Agent**: `overlay.Netbird` brings the daemon up (`netbird up --setup-key`)
  and reads the overlay IP from the WireGuard interface; the per-ticket userspace
  forwarder binds on it. The Agent is a **plain peer** — no route advertisement.
- The Server reaches assets as: `Server-Peer → (overlay) → Agent forwarder → asset`.

## Verification status
- ✅ Adapter logic (idempotency, request shaping, error classes) — unit tests.
- ✅ **Live contract test against a real NetBird Mgmt** (docs/04 §4.12):
  `TestLiveContract` (group + policy + setup key, idempotent) — green against a
  local netbird-server v0.72.x.
- ✅ **Full overlay forward-path** (`scripts/e2e-overlay.sh`): Server + Agent each
  a real NetBird peer; Server dials the Agent's forwarder at its overlay IP over
  WireGuard → relays to the asset, command filter active. 6/6 assertions green.
- The non-overlay access-enforcement core is also verified independently in
  `scripts/e2e-grpc.sh` (forwarder + command filter on loopback).
