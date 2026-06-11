# YuSui demo environment (OrbStack)

An end-to-end demo on local OrbStack: a **real agent VM**, a **real NetBird
overlay**, real SSH assets behind the agent. It reproduces YuSui's core idea:

> **Invariant #1** — the **Agent** is the project's *only* NetBird peer; the
> **assets** are machines *behind* the agent. Assets never run NetBird, and in
> production the Server **cannot reach them directly** — only via the agent's
> per-ticket forwarder over the overlay.

This is not the mock `deploy/docker-compose.yml` stack — here a real agent on a
real VM fronts the assets and the Web SSH traffic crosses an actual WireGuard
tunnel between two NetBird peers.

## Topology

```
  OrbStack VM (arm64)            assetnet (the machines behind the agent)
  ┌──────────────┐   ┌── demo-prod-db   "prod-db"   sshd  (reached only via the agent)
  │ yusui-agent  │───┤
  │ netbird peer │   └── demo-prod-app  "prod-app"  sshd
  │ + yusui-agent│            ▲
  └──────┬───────┘            │  agent forwarder relays here per ticket
         │  real WireGuard overlay (100.x)  ── NetBird control plane: host :8081
  ┌──────┴──────────────────────────────────────────────────────────┐
  │ host docker  (network: demonet)                                   │
  │   demo-server   netbird peer (overlay IP) ── named "server"       │
  │   demo-yusui-srv yusui-server (grpc gateway) in the peer's netns  │
  │   demo-pg       postgres                                          │
  │   demo-web      nginx SPA + /api proxy  ──────────▶ browser :8091 │
  └───────────────────────────────────────────────────────────────────┘
```

The **server is a NetBird peer too** (so it can dial the agent's overlay IP). The
`yusui-server` process shares the `demo-server` netbird container's network
namespace, so it binds/dials on the peer's overlay IP — the same pattern as
`scripts/e2e-overlay.sh`, made persistent and given a web UI.

### Why the assets are containers (and the agent is a VM)

The **agent is a real VM** (the project's gateway machine). The **assets are
containers**, not VMs, for one reason: **OrbStack's VM↔VM virtual NIC batches
~25% of small interactive packets by ~200ms** (occasionally up to 1–2s), which
makes the Web terminal feel laggy when typing. This is a local-virtualization
artifact — **not YuSui, not the overlay, not the forwarder** (measured: server's
SSH path direct-dial = sub-10ms; agent→container = sub-10ms; agent→**VM** = the
~200ms bimodal stall). In a real deployment the agent↔asset link is a real
network and this doesn't happen. Containers keep the terminal snappy while the
agent stays a real VM peer.

## Prerequisites

- OrbStack (running), `orb` + `docker` CLIs, `curl`, `python3`.
- The YuSui images built: `docker compose -f deploy/docker-compose.yml build`
  (produces `yusui-server` / `yusui-web` / `yusui-migrate`).
- The agent linux/arm64 binary: `make agent-dist` (up.sh rebuilds it if missing).
- The **NetBird control plane up + an admin seeded**:
  ```bash
  cd deploy/netbird && ./gen-config.sh && docker compose up -d && ./seed-admin.sh
  ```

## Bring it up

```bash
cd deploy/demo
./up.sh
```

`up.sh` (idempotent-ish — re-run recreates the docker side + assets, reuses the agent VM):

| step | what it does |
|---|---|
| 0 | mints a NetBird **setup key** from the control plane (via `bootstrap-token.sh`) |
| 1 | creates the **asset machines** (`sshd` containers `demo-prod-db`/`demo-prod-app` on `assetnet`) |
| 2 | creates the **agent VM**, installs NetBird, joins the overlay (gets `wt0`) |
| 3 | isolation note (the access path is server → overlay → agent forwarder → asset) |
| 4 | host-docker **server side**: postgres + migrate + the `server` netbird peer + `yusui-server` (grpc gateway) + the web UI |
| 5 | pushes + runs the **yusui-agent** in the VM as a systemd service (`YUSUI_OVERLAY=netbird` → dials the server's overlay IP:9091) |
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
agent's *overlay* IP — `docker logs demo-yusui-srv | grep "dialing asset via agent forwarder"`
shows the `100.x` overlay address, and `journalctl -u yusui-agent | grep "forwarder up"`
(in the VM) shows it relaying to the asset. (On a single OrbStack host the
underlying net is flat, so the server *can* also reach the asset at L3 — the
overlay + a real private subnet enforce the hard isolation in production.)

### See the enrollment flow
`up.sh` auto-approves the agent. To watch the draft12 flow yourself:
`orb -m yusui-agent sudo systemctl restart yusui-agent` and watch the agent
re-register in **Admin → Agents**.

## Ports & names

| | |
|---|---|
| Web UI (demo) | `http://localhost:8091` |
| NetBird control plane | `http://<host-ip>:8081` (dashboard `:8090`) |
| Mock stack (separate, optional) | `http://localhost:8088` (`deploy/docker-compose.yml`) |
| agent VM | `yusui-agent` (netbird peer + yusui-agent) |
| docker | `demo-server` (netbird peer), `demo-yusui-srv`, `demo-pg`, `demo-web`, `demo-prod-db`, `demo-prod-app` |

## Tear down

```bash
./down.sh              # remove docker side + asset containers + stop the agent (keep the agent VM)
./down.sh --vms        # also delete the agent VM
./down.sh --netbird    # also stop the NetBird control plane
```

## Notes / honesty

- Assets are **containers** (the agent stays a VM). On a single OrbStack host the
  underlying network is flat, so the server *can* reach the asset containers at
  L3 — the demo's value is that YuSui *routes* via the agent forwarder over the
  overlay; in production the overlay + a genuinely private subnet enforce the
  hard "server can't reach assets directly" isolation. (Asset machines are
  containers rather than VMs to dodge OrbStack's VM↔VM ~200ms packet batching —
  see "Why the assets are containers" above.)
- The agent manages NetBird via the `netbird` CLI (overlay.Netbird v1); the
  direct daemon-gRPC client is a follow-up (docs/10).
- Secrets here are demo defaults (`demosecret`/`demokey`/passwords) — the server
  logs a weak-secret warning on purpose. Never use these beyond local dev.
