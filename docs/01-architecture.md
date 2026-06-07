# 01 · 架构

## 1.1 分层模型

YuSui 是一个**三层架构 + 一条窄的网络数据面 + 一条 PTY 代理路径**：

```
┌──────────────────────── 用户层 ────────────────────────┐
│  浏览器 Web UI（Vue3 + xterm.js）│ AI 工具（attach API） │
└────────────────────┬──────────────────────────────────┘
                     │ HTTPS（REST + WebSocket）
┌────────────────────▼──────────────────────────────────┐
│  YuSui Server（Go 单体二进制，v0.3 起水平扩展）         │
│  ┌──────────────────────────────────────────────────┐ │
│  │ API Gateway（chi router + 鉴权 middleware）       │ │
│  ├──────────────────────────────────────────────────┤ │
│  │ Services（无状态、可水平扩展）                    │ │
│  │  · ProjectSvc  · AssetSvc   · TicketSvc          │ │
│  │  · AgentSvc    · AuditSvc                         │ │
│  │  · SessionSvc（会话 + attach + 录像元数据）       │ │
│  │  （注意：无 PolicySvc — 状态机统一在 Policy Engine）│ │
│  ├──────────────────────────────────────────────────┤ │
│  │ Engines（持状态机、对账、调度）                   │ │
│  │  · Policy Engine（工单状态机 + Agent 编排，       │ │
│  │      唯一持 `Transition(ticket,from,to,reason)`） │ │
│  │  · Reconciler（对账，周期 + 事件触发）            │ │
│  │  · Scheduler（river，过期回收）                   │ │
│  │  · Command Filter Engine（远端 prompt 解析 + 规则）│ │
│  ├──────────────────────────────────────────────────┤ │
│  │ Proxies                                           │ │
│  │  · Web Shell Service                              │ │
│  │      ├─ Attach Hub（多挂接者广播）                │ │
│  │      ├─ SSH Client Pool（拨号到资产）             │ │
│  │      └─ Recorder（asciinema v2）                  │ │
│  ├──────────────────────────────────────────────────┤ │
│  │ Adapters                                          │ │
│  │  · NetBird Adapter（REST，调用量极低）            │ │
│  │  · Agent Controller（gRPC server）                │ │
│  │  · Identity Adapter（v0.1: 本地 / v0.3: +OIDC）   │ │
│  │  · Audit Sink（Postgres + 可选 Loki/S3）          │ │
│  └──────────────────────────────────────────────────┘ │
└────────────────────┬──────────────────────────────────┘
                     │
        ┌────────────┼──────────────┬────────────┐
        ▼            ▼              ▼            ▼
   ┌────────┐  ┌──────────┐  ┌──────────────┐  ┌──────────┐
   │PG 16+  │  │ NetBird  │  │ JumpServer   │  │Prometheus│
   │主存 +  │  │ Mgmt API │  │ v0.2 可选    │  │ /Loki    │
   │本地账号│  │ 启动期写 │  │ 用于 RDP/DB  │  │ 录像→S3  │
   └────────┘  └─────┬────┘  └──────────────┘  └──────────┘

           外部 OIDC IdP（Keycloak / Authentik）—— v0.3+ 才接入
                     │
              ┌──────▼──────────────────────────────────────┐
              │   NetBird Overlay (WireGuard)               │
              │                                              │
              │   [YuSui Server Peer] ─── [Agent Peer × N]  │
              │     (唯一的客户端 Peer)        │             │
              └────────────────────────────────│─────────────┘
                                               │
                                      ┌────────▼────────┐
                                      │  项目 A 私网    │
                                      │  MySQL / K8s /  │
                                      │  Win Server …   │
                                      └─────────────────┘
```

**关键点**：NetBird 网络里**只有两类 Peer**——YuSui Server 一个 + 各项目 Agent 一台/项目。运维终端、AI 工具都不是 Peer，只通过浏览器/WebSocket 与 Server 通信。

## 1.2 数据流：四种典型路径

**路径 A：控制流（运维提交工单 → 放行）**

```
Browser ──HTTPS──▶ Server API ──┬──▶ Postgres（落 ticket + binding + audit）
                                │
                                └──▶ Agent Controller ─gRPC─▶ Agent（写 nftables TTL 元素）

  ⚠ 不再调用 NetBird API。NetBird 一侧的 server↔agent 放行是启动期一次性建立的常驻策略。
```

**路径 B：运维业务流（浏览器打开终端 → 操作资产）**

```
Browser ──WebSocket(attach)──▶ Server Web Shell ─SSH client─▶ NetBird Overlay
                                      │                              │
                                      │                              ▼
                                      │                          Agent (nftables 放行)
                                      │                              │
                                      │                              ▼
                                      │                          资产 sshd
                                      │
                                      └─ 每帧 in/out 写 asciinema 录像 + 命令过滤 + audit
```

**路径 B'：AI 协作流**

```
Local AI Tool（Claude Code / Codex）──WebSocket(attach, role=primary)──▶
                                  Server Web Shell（同一 PTY 多挂接者）
```

人和 AI 共享同一个 PTY，server 按"控制权"规则路由 stdin，每段输入打来源标签。

**路径 C：协调流（持续运行）**

```
Agent ──gRPC stream──▶ Server
  ├─ Heartbeat（每 10s）
  ├─ RuleState（周期 + 变更时）
  └─ Events（规则命中/失败、连接日志 v0.2+）

Server ──gRPC stream──▶ Agent
  ├─ ApplyRule
  ├─ RevokeRule
  ├─ ReconcileRequest（启动/恢复时）
  └─ ForceClose（紧急下线某条规则相关连接）
```

## 1.3 关键设计原则

**P1 · 编排层不下沉**
Server 不实现 mesh 协议、不实现 RDP/DB 客户端、不录视频（录文本流 asciinema）。这些交给 NetBird、JumpServer（v0.2+）。**例外**：SSH 这一最常用协议的服务端代理由 Server v0.1 自研，确保 v0.1 闭环完整。

**P2 · NetBird 网络里只有 Server 和 Agent**
运维终端、AI 工具、最终资产**都不是** NetBird Peer。这极大简化了拓扑、ACL 和秘钥分发。

**P3 · 单层 ACL：Agent nftables 是事实上的访问控制**
NetBird 一条常驻策略（server-peer → agent-group，目标端口任意）启动期建立后不再改动。所有"谁能进哪里"的细粒度控制都在 Agent 的 nftables TTL set 里，由 Policy Engine 单层下发。

**P4 · 一切动作均落审计**
包括系统自动触发（过期、对账、熔断、命令拦截）。审计表 append-only，仅 INSERT 权限。终端会话内每段 stdin 还附 attacher 来源标签，回放可定位。

**P5 · 失联即降级，但权限不丢**
- **Agent 短时失联**（< `revoke_after_freeze_sec`，默认 30 min）：Agent 进入 Frozen，拒新规则；server 把该 Agent 上活跃 Web Shell session 全部 force-close（用户连不上）；但**工单保持 ACTIVE**（仅子状态 DEGRADED），恢复后用户可重开终端，无需重新审批。nftables TTL 仍自然过期。详见 [05 §5.4](05-policy-engine.md)。
- **Agent 超长失联**（≥ `revoke_after_freeze_sec`）：升级为 REVOKE，工单关闭。
- **Server ↔ NetBird Mgmt 失联**：不影响运行中隧道；阻止新项目/Agent 上线。
- **Server ↔ Postgres 失联**（关键不变量补丁）：**审计不可 fail-open**。Server 把审计与命令事件写本地 append-only WAL（文件 + fsync），PG 恢复后 replay。WAL 容量阈值（默认 50MB / 5 min）若被打穿，**主动 force-close 所有活跃 session**（fail-closed），UI 大红条提示。绝不让"PG 挂着、用户继续敲、命令没留痕"发生。详见 §1.8。

## 1.4 部署拓扑（参考）

**v0.1 单机演示**
```
┌─ docker-compose ─────────────────────────┐
│  yusui-server │ postgres                  │
│  netbird-management │ netbird-signal      │
│  coturn（单点，演示用）                    │
└──────────────────────────────────────────┘
        │ NetBird Overlay
        ├──▶ project-alpha-agent（独立 VM/容器，装 NetBird+yusui-agent）
        └──▶ project-beta-agent
        
（运维浏览器 → yusui-server，不接入 NetBird）
```

**v0.3 生产形态**
```
┌─ K8s Namespace: yusui ────────────────────┐
│  Server × 3（StatefulSet，session 亲和路由）│
│  Postgres HA（Patroni × 3）               │
│  Recordings → S3 / MinIO                  │
└───────────────────────────────────────────┘
        │
        ├──▶ 各项目 Agent × 2（双活 primary/secondary）
        ├──▶ NetBird Mgmt HA + Signal HA
        ├──▶ JumpServer（可选，覆盖 RDP/DB）
        └──▶ 多地 TURN（北京 / 上海 / 香港 / 海外）
```

## 1.5 Server 内部依赖图

```
API Gateway
   │
   ├─ TicketSvc ────▶ Policy Engine ────▶ AgentController
   │                       │
   │                       ├─▶ Reconciler
   │                       └─▶ Audit Sink（写审计，Adapter）
   │
   ├─ ProjectSvc ────▶ NetBirdAdapter（建项目 Group + Route，一次性）
   │              └─▶ AgentController（注册新 Agent）
   ├─ AssetSvc    ────▶ AgentController（同步资产快照）
   ├─ SessionSvc  ────▶ Web Shell Service
   │                       ├─▶ Command Filter Engine
   │                       └─▶ Recorder
   └─ Audit Sink  ────▶ Postgres（主路径） / 本地 WAL（降级，见 §1.8）/ (可选 Loki)
```

**分层铁律**：
- **Service ⇒ Engine ⇒ Adapter**，单向。
- Adapter 不能调 Service（避免循环）。
- **Service 之间不直接互调**——通过 Engine 协调状态机；只读审计视图经 AuditSvc（查询侧）共享。**写审计统一走 Audit Sink（Adapter）**，故 Policy Engine 等 Engine 写审计属 E⇒A，不构成 Engine→Service 回边。
- Web Shell Service 仅通过 SessionSvc 暴露，不被其他 Service 直接访问。

历史：draft6 早期把 `PolicySvc`（Service 层）与 `Policy Engine`（Engine 层）作为两层概念并存，导致 [05](05-policy-engine.md) 一直写 "PolicySvc.Transition"。draft7 合并：**状态机唯一实现在 Policy Engine**；[05](05-policy-engine.md) 中 "PolicySvc.Transition" 一律等价为 "PolicyEngine.Transition"。

## 1.6 与外部系统的契约边界

| 外部 | 接口 | 调用方向 | 频次 |
|---|---|---|---|
| NetBird Mgmt | REST | Server → NetBird | 启动期 + 项目/Agent 注册时；不再每张工单调用 |
| NetBird Peers | WireGuard | Server 自己是 Peer | 持续（Server 内置 NetBird 客户端） |
| Agent | gRPC（mTLS over Overlay） | 双向流 | 长连接 |
| 资产 sshd | TCP 22（经 Overlay） | Server → 资产 | 每会话建一次 SSH |
| JumpServer（v0.2+） | REST | Server → JS | 每次涉及非 SSH 协议的工单 |
| OIDC IdP（v0.3+） | OIDC | 用户登录时 | v0.1/v0.2 不接，使用本地账号 |
| Prometheus | /metrics | Prom 拉 Server/Agent | 每 15s |
| 对象存储 S3-compat | S3 API | Server 写录像 | 会话结束时 |

## 1.8 审计完整性的失联协议（新增 v0.1-draft7）

YuSui 的不变量"一切动作均落审计"必须在 Postgres 故障时仍成立——否则就是 fail-open。设计如下：

**正常路径**：所有 audit_logs / command_filter_events 等写入走 `AuditSink.Write(ev)` 接口，主路径是 PG。

**降级路径（PG 写失败 N 秒）**：
1. AuditSink 切到本地 append-only WAL 文件（`var/audit-wal/YYYY-MM-DDTHH.jsonl`，每条 fsync）；每条事件含 `wal_seq` 自增。
2. 后台 replay 协程持续重试 PG，一旦恢复，**按 seq 顺序** replay WAL → PG，replay 完成的文件 rename 加 `.replayed` 后缀，admin 工具可清理。
3. 同时 Web UI 顶部红条：`审计降级中 — 已缓冲 X 条事件，待 PG 恢复`。

**熔断（fail-closed）**：
- WAL 容量阈值（默认 `audit_wal_max_bytes=50MB` 或 `audit_wal_max_age=5min` 哪个先到）被打穿
- 或 PG 失败时长超过 `audit_wal_fail_closed_after=10min`

→ 触发 fail-closed：
- 拒绝所有新工单批准
- **force-close 所有活跃 Web Shell session**（attacher 收到 `closed{reason:audit_unavailable}`）
- 持续告警；运维介入

**审计 WAL 的安全**：
- 文件权限 600
- 写入流程与录像分离（录像本来就在文件系统/对象存储，PG 不挂）
- WAL 自己不再写 PG，避免循环依赖

历史：draft6 §1.3 P5 写"PG 失联 session 继续操作"是 fail-open 错误。draft7 补丁明确 WAL + fail-closed 路径。

## 1.7 未决问题

- Server 多副本时（v0.3），同一 session 必须固定到一个 server 副本（session 亲和性）。负载均衡选 sticky session（基于 cookie）还是网关层路由？倾向网关层基于 `session_id` 一致性哈希。
- Server 与 NetBird Mgmt 跨网络区域：gRPC 走 Overlay 还是公网？倾向 Overlay。
- 是否引入 NATS / Redis Stream 解耦 Service 与 Adapter？v0.1 不引入，v1.0 视规模评估。
- 录像本体存对象存储 vs DB Large Object？v0.1 文件系统、v0.2+ 强制对象存储。
- 审计 WAL 是否与录像复用同一磁盘？两者都不能丢；倾向独立磁盘卷（容器化时独立 PVC）。
- WAL replay 失败（PG 长时间起不来）的数据保留：超 `audit_wal_max_age` 后该不该归档到对象存储而非删？倾向归档 + audit alert。
