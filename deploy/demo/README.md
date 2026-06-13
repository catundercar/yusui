# YuSui demo environment (Docker)

An end-to-end demo on local Docker: a **real NetBird overlay**, a dedicated
**agent peer**, real SSH assets behind that agent. It reproduces YuSui's core idea:

> **Invariant #1** — the **Agent** is the project's *only* NetBird peer; the
> **assets** are machines *behind* the agent. Assets never run NetBird, and in
> production the Server **cannot reach them directly** — only via the agent's
> per-ticket forwarder over the overlay.

This is not the mock `deploy/docker-compose.yml` stack — here a dedicated agent
peer fronts the assets and the Web SSH traffic crosses an actual WireGuard tunnel
between two NetBird peers.

> **Why everything is a container (no OrbStack VM).** An earlier version ran the
> agent on a real OrbStack VM. The terminal felt laggy, and we traced it: the
> browser is on the host, so every keystroke round-trips host → … → asset → back,
> and **any OrbStack VM on that path stalls it** — OrbStack's host↔VM virtual NIC
> batches ~5% of small interactive packets by up to ~1.9s (measured). Pure
> container↔container is sub-ms with zero stalls. So the agent runs as a peer
> **container**; the overlay between the two peer containers is still real
> WireGuard. Full troubleshooting record: [`diagnosis-terminal-latency.md`](diagnosis-terminal-latency.md).

## Topology

```
  assetnet (the machines behind the agent)
  ┌── demo-prod-db   "prod-db"   sshd 192.168.155.2:2222  (reached only via the agent)
  │
  ├── demo-prod-app  "prod-app"  sshd 192.168.155.3:2222
  │        ▲  agent's per-ticket forwarder relays here
  │        │
  ├── demo-agent       netbird peer (overlay IP 100.x) ── the assets' only way in
  └── demo-yusui-agent yusui-agent, in demo-agent's netns (reads its wt0)
           │
           │  real WireGuard overlay (100.x)  ── NetBird control plane: host :8081
  ┌────────┴──────────────────────────────────────────────────────────┐
  │ host docker  (network: demonet)                                     │
  │   demo-server    netbird peer (overlay IP) ── named "server"        │
  │   demo-yusui-srv yusui-server (grpc gateway), in the peer's netns   │
  │   demo-pg        postgres                                           │
  │   demo-web       nginx SPA + /api proxy  ──────────────▶ browser :8091 │
  └─────────────────────────────────────────────────────────────────────┘
```

Both the **server and the agent are NetBird peers** (containers), so the server
dials the agent's overlay IP to reach the assets. Each `yusui-*` process shares
its peer container's network namespace (`--network container:demo-server` /
`container:demo-agent`), so it binds/dials on that peer's overlay IP — the same
pattern as `scripts/e2e-overlay.sh`, made persistent and given a web UI.

## Prerequisites

- Docker (running), `docker` CLI, `curl`, `python3`.
- The YuSui images built:
  - `docker compose -f deploy/docker-compose.yml build` → `yusui-server` / `yusui-web` / `yusui-migrate`.
  - `docker build -f agent/Dockerfile -t yusui-agent:latest .` (context = repo root) → `yusui-agent`.
- The **NetBird control plane up + an admin seeded**:
  ```bash
  cd deploy/netbird && ./gen-config.sh && docker compose up -d && ./seed-admin.sh
  ```

## Bring it up

```bash
cd deploy/demo
./up.sh
```

`up.sh` (idempotent-ish — re-running recreates all containers + assets and re-seeds):

| step | what it does |
|---|---|
| 0 | mints a NetBird **setup key** from the control plane (via `bootstrap-token.sh`) |
| 1 | creates the **asset machines** (`sshd` containers `demo-prod-db`/`demo-prod-app` on `assetnet`) |
| 2 | creates the **agent peer** (`demo-agent`, a netbird container on `assetnet`; joins the overlay, gets `wt0`) |
| 3 | isolation note (access path = server → overlay → agent forwarder → asset) |
| 4 | host-docker **server side**: postgres + migrate + the `server` netbird peer + `yusui-server` (grpc gateway) + the web UI |
| 5 | runs **`demo-yusui-agent`** in `demo-agent`'s netns (`YUSUI_OVERLAY=netbird` reads its `wt0`; dials the server's overlay IP:9091) |
| 6 | **seeds** the catalog: project `demo`, **approves** the auto-registered agent, assets `prod-db`/`prod-app` + SSH creds |
| 7 | prints the URL + credentials |

It ends by printing everything you need (URL, logins, overlay IPs).

## Use it (the demo)

1. Open **http://localhost:8091** and log in as **admin / `Admin12345!@`**
   (or `req1 / Req12345!@xy`, `appr1 / Appr12345!@xy`).
2. **Tickets → 提工单**: project `demo`, asset `prod-db`, port 2222, a reason, a duration.
3. Log in as **appr1** and **审批** the ticket (step-up password = `Appr12345!@xy`;
   note: an approver can't approve their *own* ticket).
4. **打开终端** on the active ticket. You're now SSH'd into the `prod-db` machine —
   **through the agent's forwarder, over the WireGuard overlay**:
   - `whoami` → `ops-yusui` (the asset account; the operator never sees the credential).
   - `rm -rf /` → **blocked** by the command filter; `sudo shutdown` → asks to **confirm**.
5. Let the ticket expire (or 撤销) — the session is force-closed and the agent's
   forwarder is torn down.

The **access fact**: the Server reaches `prod-db` via the agent's forwarder at the
agent's *overlay* IP — `docker logs demo-yusui-srv | grep "agent forward address"`
shows the `100.x` overlay address, and `docker logs demo-yusui-agent | grep "forwarder up"`
shows it relaying to the asset. (On a single Docker host the underlying net is
flat, so the server *can* also reach the asset at L3 — the overlay + a real
private subnet enforce the hard isolation in production.)

### See the enrollment flow
`up.sh` auto-approves the agent. To watch the draft12 flow yourself:
`docker restart demo-yusui-agent` and watch it re-register in **Admin → Agents**.

## Ports & names

| | |
|---|---|
| Web UI (demo) | `http://localhost:8091` |
| NetBird control plane | `http://<host-ip>:8081` (dashboard `:8090`) |
| Mock stack (separate, optional) | `http://localhost:8088` (`deploy/docker-compose.yml`) |
| agent peer | `demo-agent` (netbird peer) + `demo-yusui-agent` (yusui-agent, on assetnet) |
| server side | `demo-server` (netbird peer), `demo-yusui-srv`, `demo-pg`, `demo-web` |
| assets | `demo-prod-db`, `demo-prod-app` (sshd, behind the agent) |

## Tear down

```bash
./down.sh              # remove all demo containers + networks
./down.sh --vms        # also delete the leftover agent VM (from the older VM-based demo, if any)
./down.sh --netbird    # also stop the NetBird control plane
```

## Notes / honesty

- **Everything is a container, including the agent.** This is a deliberate choice
  for a snappy terminal — any OrbStack VM on the keystroke path stalls it (see
  "Why everything is a container" above and [`diagnosis-terminal-latency.md`](diagnosis-terminal-latency.md)).
  The trade-off: an agent *container* is less faithful than an agent *VM* to "a
  real gateway machine," but the architecture it demonstrates is identical — the
  agent is the only peer, the assets are behind it, and the server reaches them
  only via the per-ticket forwarder over a real WireGuard overlay.
- On a single Docker host the underlying network is flat, so the server *can*
  reach the asset containers at L3 — the demo's value is that YuSui *routes* via
  the agent forwarder over the overlay; in production the overlay + a genuinely
  private subnet enforce the hard "server can't reach assets directly" isolation.
- The agent manages NetBird via the `netbird` daemon (overlay.Netbird, netbird
  mode reading `wt0`); the direct daemon-gRPC client is a follow-up (docs/10).
- Secrets here are demo defaults (`demosecret`/`demokey`/passwords) — the server
  logs a weak-secret warning on purpose. Never use these beyond local dev.
