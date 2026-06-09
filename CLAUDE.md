# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Repository status

This repo is **design-stage**: it currently contains only design documents (`DESIGN.md` + `docs/01..09`). There is **no code, no build system, no tests, and no git repo yet**. Do not invent build/lint/test commands — none exist. The first coding task (per `DESIGN.md` §9) is to scaffold three repos: `yusui-server` (Go), `yusui-web` (Vue), `yusui-deploy` (compose/helm).

Docs are written in **Chinese**; match that language and the decision-record tone (see "Doc conventions" below) when editing them.

## What YuSui is

A NetBird-based, **ticket-driven zero-trust ops-access platform**. Ops users only use a browser; YuSui Server acts as the SSH proxy (v0.1) and orchestrator. Every access to a production asset defaults to *denied*; each access is granted via an approved ticket with an explicit requester, time window, scope, and automatic revocation. YuSui's value is **orchestration + business closed-loop + AI-friendly Web terminal**, not the network layer or full-protocol proxy — NetBird (server↔agent overlay only), JumpServer (v0.2 optional, RDP/DB/etc), and Prometheus are upstream dependencies that are never reimplemented or forked. The single SSH proxy is built in-house in v0.1 to close the loop.

The critical path the MVP must make work end-to-end: `submit ticket → approve → open Web SSH (human ± AI attach, with dangerous-command filtering) → auto-expire/disconnect → recording + audit available`.

## Architectural invariants (these recur across every doc — never violate them)

1. **Only YuSui Server and project Agents are NetBird Peers.** End users do NOT install NetBird; their browsers talk to Server over HTTPS, and Server uses its own SSH client (over NetBird overlay) to reach assets. Real assets (RDS, MySQL, K8s API, Windows Server, IoT) live in a private subnet *behind* a per-project Agent — the Agent is that project's **only** Peer. Assets never run the NetBird client. All cross-project traffic passes through that project's Agent. (draft10: the Agent is a **plain** Peer that does NOT advertise the private subnet as a NetBird route; Server reaches assets by dialing the Agent's overlay IP and the Agent L4-forwards per ticket — see #2. This is what lets different projects reuse/overlap private subnets without an overlay routing collision.)

2. **Single-layer ACL — the Agent's per-ticket forwarder is the fact.** NetBird has exactly one permanent policy installed at startup: `src=server-peer-group, dst=all-agents-group, action=accept`. Per-ticket access is enforced solely by the Agent's **per-ticket userspace L4 forwarder** (draft10): a listener bound to the Agent's overlay IP that accepts only `src_peer_ips` and forwards to one `asset_ip:port`, with lifetime = `ticket.expires_at`. Its existence + fixed target + source allowlist IS the access fact; Server's Web Shell dials the forwarder's returned address, not the raw asset IP. Policy Engine writes Agent only; **no per-ticket NetBird call**. (Historical: drafts 1-5 double-layer ACL, removed draft6; drafts 1-9 used a Linux **nftables** TTL set as the mechanism — draft10 replaced it with the cross-platform userspace forwarder because Agents are **Windows**; nftables survives only as an optional Linux enforcer behind an `Enforcer` interface.)

3. **Server is the SSH proxy.** v0.1 in-house Web Shell Service holds the PTY, runs an SSH client to the asset, and broadcasts to multiple WebSocket attachers (human + AI). Source-of-input is tagged per byte (`web` / `api` / `observer`). Recording is asciinema v2 text streams, not video.

4. **Dangerous-command filter is line-buffered, configurable, never claimed bullet-proof.** Rules merge from global defaults ∪ project policy ∪ asset policy ∪ AI-source-stricter set; conflicts resolve to stricter severity. Three severities: `block` / `confirm` / `warn`. Raw-mode (vim/less) auto-pauses filtering. Paste blocks >64KB force confirm. Docs and UI must say: this defends against typos, not malicious users with shell access (alias/encode/heredoc bypass is out of scope).

5. **Server is the only control point / write path.** NetBird Management is invisible to ops users — they never log into the NetBird UI. Asset SSH credentials are stored in Server-only encrypted DB (v0.1) or delegated to JumpServer account set (v0.2+); operators do not directly touch credentials.

6. **Idempotency key = `yusui:<scope>:<domain_id>`, written into the external resource's `name` field** (the permanent NetBird policy: `yusui:builtin:server-to-agents`; NetBird group: `yusui:project:<code>-agents`; Agent rule_id: `yusui:tk:<id>`; v0.2 JumpServer resources: `yusui:tk:<id>`). This `name` prefix is the *only* anchor for crash/disconnect recovery and reconciliation. Every `Ensure*`/`Apply*`/`Revoke*` must be idempotent (treat DELETE 404 as success; re-Apply means "ensure exists").

7. **Everything is audited, including system-triggered actions** (expiry, reconcile, freeze, command-filter events, AI inputs). `audit_logs` is append-only — the DB role has INSERT only. Every ticket state transition goes through `PolicyEngine.Transition(ticket, from, to, reason)` and writes the audit row *in the same transaction*. Recording streams (asciinema) include attacher_id and source per frame.

8. **Approver ≠ requester is hardcoded** (DB CHECK constraint + app logic). No self-approval. Approve/revoke/admin actions require step-up re-auth (password + TOTP if enabled).

9. **Fail-to-degraded, never fail-open.** If an Agent loses contact with Server past the freeze threshold (default 60s) it enters Frozen mode: rejects new forwarders, but the Agent's in-process timers still close existing ones at `expires_at` (and a netbird-daemon "overlay down" report also trips Frozen), established TCP connections are unaffected. Active Web Shell sessions to that project become unusable; Server force-closes attachers with a clear error. If Server loses contact with NetBird Mgmt, new project/Agent registration is blocked but running sessions are unaffected (the permanent policy is already in place).

## Component boundaries (when code exists)

- **Server internal layering** (`docs/01-architecture.md` §1.5): `API Gateway → Services (Project/Asset/Ticket/Agent/Session/Audit; NO PolicySvc) → Engines (Policy Engine, Reconciler, Scheduler, Command Filter) → Proxies (Web Shell Service) → Adapters (NetBird, Agent Controller, Identity, Audit Sink, JumpServer v0.2+)`. **Adapters must not call Services. Services must not call each other directly — they coordinate through Engines** (avoids circular deps). **Web Shell Service is reachable only through SessionSvc, not directly from API handlers.** State machine ownership: `Policy Engine` is the only owner of `Transition(ticket, from, to, reason)`; the old name `PolicySvc.Transition` is an alias retained in [05](docs/05-policy-engine.md) headings only.
- **NetBird Adapter** strictly calls only the NetBird Management REST API — never reads NetBird's DB, parses its protocol, or patches its source. In v0.1 it is called at startup (install permanent policy) and on project/agent register; **never per ticket**. Errors are classified (`ErrTransient/ErrConflict/ErrAuth/ErrSchema/ErrPermanent`) so the Reconciler can decide retry vs. alert (`docs/04`).
- **Agent** (`agent.exe`, **pure-Go, Windows-native single binary**; draft10) does per-ticket **userspace L4 forwarding** + local ACL only, and **manages (does not embed/fork) the official NetBird daemon** via the daemon's local gRPC API (first install of the daemon + wintun driver is done by a dedicated installer). Enforcement is cross-platform Go `net.Listen`/`net.Dial`, **not nftables** (nftables is an optional Linux-only enforcer). It does **not** parse application traffic, store business data, hold asset credentials, face end users, or self-configure per-ticket policy (all rules come from Server; no local rule-editing CLI). Application-layer audit is the Server's Web Shell (SSH) or JumpServer's job (other protocols, v0.2+). Server-side NetBird runs as a sidecar container (not the manage-daemon pattern).
- **Web Shell Service** is an internal component of Server but **must be designed for isolation** (v0.1 separate goroutine pool + interface boundary; v0.2 separate process/container). It must not hold CA private key, must not access Postgres beyond INSERT into recording metadata / command events / audit. It holds asset SSH credentials in memory only.
- **Agent ↔ Server protocol**: gRPC bidirectional stream over mTLS (private CA) over the NetBird Overlay — Server exposes no public gRPC port. proto is in `docs/03` (`package yusui.agent.v1`); proto fields are **add-only, never removed** (mark `[deprecated=true]`), and Server/Agent must interoperate across N-1 versions.
- **Browser ↔ Web Shell protocol** (`docs/09`): WebSocket with length-prefixed JSON messages (`stdin`/`stdout`/`resize`/`signal`/`request_primary`/`release_primary`/...). Same protocol used by AI tools; differentiated via `source=api` query parameter.

## Planned tech stack (decided in `DESIGN.md` §6, not yet implemented)

| Layer | Choice |
|---|---|
| Backend | Go 1.23+, chi or fiber router (Google AIP-style API design) |
| DB access | sqlc + pgx (no heavy ORM — SQL stays transparent) |
| Database | PostgreSQL 16, single DB, all tables in schema `yusui` |
| Migrations | goose, starting `migrations/0001_init.sql`; all ALTERs backward-compatible; no destructive DOWN in prod |
| Jobs/scheduling | river (Postgres-backed — no Redis), used for expiry/revocation |
| Frontend | Vue 3 + Vite + Element Plus |
| Auth | **v0.1: local accounts (bcrypt + optional TOTP)**; OIDC (Keycloak/Authentik) deferred to v0.3; Identity Adapter interface stubbed; step-up re-auth required for approve/revoke/admin actions |
| Deploy | Docker Compose (v0.1) → Helm (v0.3) |

**Explicitly rejected**: Java, Node (backend), Rust, forking NetBird, self-built CMDB/ITSM/SSH-proxy/monitoring.

## Data model notes (`docs/06` + `docs/09` §9.8)

13 tables in v0.1:
- Main: `users` `projects` `agents` `assets` `asset_credentials` `tickets` `policy_bindings` `audit_logs` `netbird_global_settings`
- Web Shell: `sessions` `session_attachers` `command_filter_events` `command_policies`

Key points future code must honor:
- Users **do not** carry `netbird_peer_id`; only Server (in `netbird_global_settings`) and Agents do.
- Assets carry `project_id` (which decides their Agent) but **never** a `netbird_peer_id`.
- `policy_bindings` records **only** Agent external ID (`agent_rule_id`); no NetBird per-ticket fields (those existed in draft 1-5 schemas and were removed in draft6).
- `netbird_global_settings` is a single-row table (CHECK id=1) holding the permanent server-peer/group/policy IDs installed at startup.
- Constraints are enforced **in DDL**, not the app layer (e.g. `tickets.duration_sec BETWEEN 60 AND 86400`, `requester_id <> approver_id`, `UNIQUE(project_id, role)` on agents, `tickets.access_kind CHECK` set to `web_shell` in v0.1).
- UI-facing IDs use a ULID `pub_id`, not the internal `BIGSERIAL` — this reserves room for v1.0 multi-tenancy (`tenant_id`) without changing URLs.

## Doc conventions (from `docs/README.md` §writing-conventions)

When editing the design docs:
- Use a **decision-record voice**: *what + why this way + what breaks if not*.
- No tutorials, no copying upstream docs — only YuSui-specific content.
- Express interface contracts as Protobuf / SQL DDL, not prose.
- End each doc with an "未决问题" (open questions) list.
- `DESIGN.md` is the overview only; detail lives in `docs/`. Keep the two in sync (e.g. roadmap week estimates, the "do / don't" table) and add a dated changelog entry for substantive changes.

Reading order for newcomers: `DESIGN.md → docs/01 → 09 → 05`. For Agent work: `02 → 03 → 07`. For Server work: `05 → 09 → 04 → 06 → 03`. For security review: `07 → 09 → 01 → 03`.
