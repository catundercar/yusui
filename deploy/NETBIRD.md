# NetBird overlay (M4) — deployment runbook

YuSui uses NetBird purely as transport (docs §0): only the **Server** and each
project **Agent** are NetBird peers; end users and assets are not. The per-ticket
access decision is enforced by the **Agent's nftables** (verified in M3) — NetBird
carries exactly one permanent policy.

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

## Make Server + Agents peers
- **Server**: run the NetBird client on the server host/container: `netbird up
  --setup-key <KEY> --management-url $NETBIRD_MGMT_URL`. Note its overlay IP →
  `SERVER_PEER_IPS`.
- **Agent**: the `yusui-agent` container additionally runs `netbird up
  --setup-key <KEY> ...` to join the overlay (the agent is the project's only
  peer / routing peer). The agent then advertises the project CIDR as a NetBird
  Network Route (assigned by the server via the adapter).
- The server reaches assets as: `Server-Peer → (overlay) → Agent → nftables → asset`.

## Verification status
- ✅ Adapter logic (idempotency, request shaping, error classes) — unit tests.
- ⏳ **Live contract tests against a real NetBird Mgmt** (docs/04 §4.12) and the
  full overlay forward-path are the remaining integration step — they require a
  running NetBird stack and were not exercised in the dev environment. The
  access-enforcement core (per-ticket nftables gating over gRPC) is verified
  end-to-end in M3 without the overlay; NetBird only adds the encrypted transport
  + the one permanent policy on top.
