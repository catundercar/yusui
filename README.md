# YuSui (monorepo)

工单驱动的零信任运维接入平台。设计文档见 [DESIGN.md](DESIGN.md) 与 [docs/](docs/)。

📖 **文档站(部署 / 接入指南)**:<https://catundercar.github.io/yusui/> — 主服务部署、**Agent 一条命令接入**、概念与架构。源码在 [`site/`](site/)。

## 结构
- `server/` — YuSui Server (Go)：API / Policy Engine / Web Shell / Adapters
- `agent/`  — YuSui Agent (Go)：routing peer + 本地 nftables ACL（M3 起）
- `proto/`  — Agent↔Server gRPC 契约（M3 起）
- `web/`    — Vue3 前端（M2 起）
- `deploy/` — docker-compose / 部署
- `docs/`   — 详细设计（01..09）

## 本地起步（M0）
```sh
cp deploy/.env.example deploy/.env   # 可选，覆盖默认弱口令
make up                              # postgres + migrate(one-shot) + server
curl localhost:8080/healthz          # ok
curl localhost:8080/readyz           # ready
```

## 常用 make
- `make up` / `make down` / `make logs`
- `make sqlc` 生成 store 查询代码
- `make build` / `make test` / `make lint` / `make tidy`

## 已实现（v0.1 MVP）

| 里程碑 | 内容 | 验证 |
|---|---|---|
| M0 | monorepo / 13 表迁移 / chi server / compose / CI | ✅ Docker + 本地 PG |
| M1 | 工单状态机 · Policy Engine（审计同事务）· 自动到期 · RBAC/step-up | ✅ e2e |
| M2 | 自研 Web SSH（SSH 代理 + 命令拦截 + asciinema 录像 + 撤销 force-close） | ✅ e2e（真 sshd） |
| M5 | Vue3 + Element Plus + xterm.js 浏览器 UI | ✅ 真浏览器 |
| M3 | 真 Agent（nftables 网关 + gRPC 控制面，替换 mock） | ✅ e2e（容器内 nft） |
| M4 | NetBird Adapter（REST + 幂等 + 错误分类）+ overlay runbook | ✅ 单测（mock）· ⏳ 真 NetBird 契约测试见 [deploy/NETBIRD.md](deploy/NETBIRD.md) |

关键路径**端到端可用**：提工单 → 审批 → 浏览器开 Web SSH（危险命令拦截）→ 到期/撤销自动断 → 录像 + 审计可查。
里程碑路线见 `DESIGN.md §7`。
