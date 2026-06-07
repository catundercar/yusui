# YuSui (monorepo)

工单驱动的零信任运维接入平台。设计文档见 [DESIGN.md](DESIGN.md) 与 [docs/](docs/)。

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

状态：**M0（地基）**。里程碑路线见 `DESIGN.md §7` 与计划文件。
