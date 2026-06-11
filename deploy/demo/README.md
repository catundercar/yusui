# YuSui demo environment (OrbStack)

A realistic, end-to-end demo on local OrbStack — **real VMs**, a **real NetBird
overlay**, real SSH assets. It faithfully reproduces YuSui's core invariant:

> **Invariant #1** — the **Agent** is the project's *only* NetBird peer; the
> **assets** are real machines in a private subnet *behind* the agent. Assets
> never run NetBird, and the Server **cannot reach them directly** — only via the
> agent's per-ticket forwarder over the overlay.

This is not the mock `deploy/docker-compose.yml` stack — here a real agent on a
real VM fronts real asset VMs, and the Web SSH traffic crosses an actual
WireGuard tunnel.

## Topology

```
  OrbStack VMs (arm64)                              host docker
  ┌──────────────┐   private LAN 192.168.139.0/24
  │ yusui-agent  │──┬── yusui-asset1  "prod-db"   (sshd; nft: only the agent may reach :22)
  │ netbird peer │  └── yusui-asset2  "prod-app"  (sshd; nft: only the agent may reach :22)
  │ + yusui-agent│            ▲  the server is firewalled out of these
  └──────┬───────┘            │
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

`up.sh` (idempotent-ish — re-run recreates the docker side, reuses the VMs):

| step | what it does |
|---|---|
| 0 | mints a NetBird **setup key** from the control plane (via `bootstrap-token.sh`) |
| 1 | creates/provisions the **asset VMs** (`openssh-server` + an `ops-yusui` account) |
| 2 | creates the **agent VM**, installs NetBird, joins the overlay (gets `wt0`) |
| 3 | **firewalls** the assets (nftables: `:22` reachable only from the agent's IP) |
| 4 | host-docker **server side**: postgres + migrate + the `server` netbird peer + `yusui-server` (grpc gateway) + the web UI |
| 5 | pushes + runs the **yusui-agent** in the VM as a systemd service (`YUSUI_OVERLAY=netbird` → dials the server's overlay IP:9091) |
| 6 | **seeds** the catalog: project `demo`, **approves** the auto-registered agent, assets `prod-db`/`prod-app` + SSH creds |
| 7 | prints the URL + credentials |

It ends by printing everything you need (URL, logins, overlay IPs).

## Use it (the demo)

1. Open **http://localhost:8091** and log in as **admin / `Admin12345!@`**
   (or `req1 / Req12345!@xy`, `appr1 / Appr12345!@xy`).
2. **Tickets → 提工单**: project `demo`, asset `prod-db`, port 22, a reason, a duration.
3. Log in as **appr1** and **审批** the ticket (step-up password = `Appr12345!@xy`;
   note: an approver can't approve their *own* ticket).
4. **打开终端** on the active ticket. You're now SSH'd into the `prod-db` VM —
   **through the agent's forwarder, over the WireGuard overlay**:
   - `whoami` → `ops-yusui` (the asset account; the operator never sees the credential).
   - `rm -rf /` → **blocked** by the command filter; `sudo shutdown` → asks to **confirm**.
5. Let the ticket expire (or 撤销) — the session is force-closed and the agent's
   forwarder is torn down.

To prove the isolation: the server **cannot** SSH `prod-db` directly —
```bash
docker exec demo-server nc -z -w3 192.168.139.151 22   # connection refused/blocked
orb -m yusui-agent      nc -z -w3 192.168.139.151 22   # the agent can (it is the gateway)
```

### See the enrollment flow
`up.sh` auto-approves the agent. To watch the draft12 flow yourself: in the Admin
page delete nothing — instead `orb -m yusui-agent sudo systemctl restart yusui-agent`
and watch the agent re-register; or create a second agent VM and approve it from
**Admin → Agents** (the `pending` row gets 通过/拒绝 buttons).

## Ports & names

| | |
|---|---|
| Web UI (demo) | `http://localhost:8091` |
| NetBird control plane | `http://<host-ip>:8081` (dashboard `:8090`) |
| Mock stack (separate, optional) | `http://localhost:8088` (`deploy/docker-compose.yml`) |
| VMs | `yusui-agent`, `yusui-asset1` (prod-db), `yusui-asset2` (prod-app) |
| docker | `demo-server` (netbird peer), `demo-yusui-srv`, `demo-pg`, `demo-web` |

## Tear down

```bash
./down.sh              # remove docker side + stop the agent (keep VMs for a fast re-run)
./down.sh --vms        # also delete the OrbStack VMs
./down.sh --netbird    # also stop the NetBird control plane
```

## Notes / honesty

- The asset VMs are isolated with **nftables** (`:22` allow-from-agent-only) to
  emulate the private subnet — because on a single OrbStack instance the docker
  server and the VMs share an L3 net, so without the firewall the server *could*
  reach the assets directly. In production the overlay + a genuinely private
  subnet provide this isolation; here the firewall stands in for it.
- The agent manages NetBird via the `netbird` CLI (overlay.Netbird v1); the
  direct daemon-gRPC client is a follow-up (docs/10).
- Secrets here are demo defaults (`demosecret`/`demokey`/passwords) — the server
  logs a weak-secret warning on purpose. Never use these beyond local dev.
