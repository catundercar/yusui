# 核心概念与架构

## 拓扑:谁是 Peer,资产藏在哪

```
  浏览器(运维)                         项目私有子网(资产,不装 NetBird)
   │ HTTPS                              ┌── RDS / MySQL / K8s API / Windows / IoT
   ▼                                    │        ▲ 只能经 Agent 的按工单转发器到达
  ┌───────────────┐   真 WireGuard      ┌────────┴───────┐
  │ YuSui Server  │◄───── overlay ─────►│  项目 Agent     │  ← 该项目唯一的 Peer
  │ (NetBird Peer)│   (100.x, 仅两类)   │  (NetBird Peer) │
  │  SSH 代理/编排 │                     │  按工单 L4 转发  │
  └───────────────┘                     └────────────────┘
```

- **只有 Server 与各 Agent 是 Peer。** 浏览器、资产都不是。
- Server 要碰资产,只能 **拨 Agent 的 overlay IP**,由 Agent 按工单起一个**用户态转发器**中转到 `asset_ip:port`。
- 不同项目的私有子网可以重叠——因为 Agent 是**普通 Peer**(不向 NetBird 通告子网路由),靠转发器做 L4 中转。

## 两个口令,两件事(部署时最易混)

| 口令 | 属于谁 | 作用 |
|---|---|---|
| **NetBird setup key** | 网络层 | 让一台机器**入 overlay**(`netbird up --setup-key`) |
| **YuSui register token** | 业务层 | 让 Agent **接入 YuSui**(注册成 pending) |

Agent 自己用 setup key 把 NetBird 拉起来(管它的生命周期),再用 register token 向 Server 注册。详见 [Agent 部署](/deploy/agent)。

## 接入状态机(draft12 enrollment)

```
装好二进制 ──► Agent 启动:自管 netbird up 入网 + 用 register token 注册
                       │
                       ▼
              自动注册成 pending(Server 不下发任何规则)
                       │  管理员在页面【资源管理 → Agent → 批准】(step-up)
                       ▼
                    approved ──► 此后工单才能经它放行
```

## Server 内部分层(代码落地后)

```
API Gateway
  → Services (Project / Asset / Ticket / Agent / Session / Audit)
  → Engines (Policy Engine / Reconciler / Scheduler / Command Filter)
  → Proxies (Web Shell Service)
  → Adapters (NetBird / Agent Controller / Identity / Audit Sink / JumpServer v0.2+)
```

- **Adapter 不调 Service;Service 之间不互调**——通过 Engine 协调,避免环依赖。
- **Policy Engine 是工单状态机 `Transition(ticket, from, to, reason)` 的唯一入口**,审计与状态变更同事务。
- **Web Shell Service 只能经 SessionSvc 触达**,不直连 API handler。

## 失败语义:降级,不放行

- Agent 与 Server 失联超过冻结阈值(默认 60s)→ **Frozen**:拒绝新转发器,但进程内定时器仍按 `expires_at` 关闭已有转发器;已建立的 TCP 连接不受影响。
- Server 与 NetBird Mgmt 失联 → 阻断新项目/Agent 注册,运行中会话不受影响(常驻策略已就位)。

> 更深的设计记录在仓库 `DESIGN.md` 与 `docs/01..09`(中文,decision-record 体例)。本站点聚焦**怎么用、怎么部署**。
