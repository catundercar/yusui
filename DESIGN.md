# YuSui 设计文档 v0.1（MVP）—— 总览

> 状态：草案 · 2026-06-01
> 作者：catundercar
> 目标读者：自己（未来的我）+ 早期 contributor

> **本文为总览**。详细设计在 [`docs/`](docs/README.md)：架构 / Agent / 协议 / NetBird Adapter / Policy Engine / 数据模型 / 安全。

---

## 0. 一句话定位

**YuSui = 基于 NetBird 的"工单驱动零信任运维接入平台"。运维人员只用浏览器，所有终端操作经服务端代理；项目内资产由 per-project Agent 守门。**

把 CMDB、工单审批、临时网络放行、Web 终端、命令拦截、操作审计串成一个闭环，让"运维人员访问生产资产"这件事默认为零，每一次访问都有明确的发起人、时间窗、范围和回收。

> **三条核心架构约束（v0.1 即生效）**
>
> 1. **资产不装 NetBird**：所有真实资产（RDS、MySQL、K8s API、Windows Server、IoT 等）位于项目 Agent 后方私网。Agent 是该项目唯一的 NetBird Peer（draft10：普通 Peer，不广播 Route）。
> 2. **运维端不装 NetBird**：取消"用户 Peer"概念。运维人员只与 YuSui 服务端用浏览器通信，由服务端作为 NetBird Peer 经 Agent 转接到资产。
> 3. **NetBird 拓扑只剩 server↔agent**：用户 Peer 与 Mgmt UI 都对终端用户不可见；NetBird 退化为 YuSui 自用的传输层。

---

## 1. 边界：做什么 / 不做什么

| 做 | 不做（至少 MVP 不做） |
|---|---|
| 资产清单（CMDB lite） | 通用 CMDB（不替代 iTop / CMDBuild） |
| 工单审批（运维场景定制） | 通用 ITSM（不替代 Jira / ServiceNow） |
| 调度 NetBird Route + Agent 本地 ACL | 重写 NetBird 协议层 |
| 多云内网打通编排 | 自研 mesh VPN |
| **自研 Web SSH + AI 友好 attach + 命令拦截** | 重新发明 JumpServer 全协议代理（RDP / DB Web 客户端等留给 v0.2 JumpServer） |
| **服务端代理 + 录像（asciinema）** | 视频录屏（v0.2+ 通过 JumpServer 获取） |
| Prometheus 指标导出 | 自研监控系统 |

**核心信念**：YuSui 的价值在"编排 + 业务闭环 + 统一入口"，不在底层网络协议、也不在全协议应用代理。NetBird、JumpServer、Prometheus 是上游依赖，不重复造轮子；但**SSH 这一最常用协议**的服务端代理由 YuSui v0.1 自研，确保闭环不留尾巴。

---

## 2. 与现有方案的差异

| 维度 | Teleport | JumpServer | YuSui |
|---|---|---|---|
| 网络层 | 反向隧道（自己实现） | 依赖外部 VPN | NetBird（server↔agent） |
| 用户接入 | CLI（tsh）+ Web | Web（强） | **仅 Web**（v0.3 加端口转发模式） |
| SSH 代理 + 审计 | 强（自研） | 强（自研） | **v0.1 自研（含 AI attach + 命令拦截）** |
| RDP / DB 协议 | 强（自研） | 强（自研） | 借 JumpServer（v0.2） |
| 工单/审批 | 弱（API 集成） | 中（内置） | **强（核心卖点）** |
| AI 协作（人 + AI 同终端） | 无 | 无 | **有（v0.1 即支持）** |
| 多云内网穿透 | 弱 | 无 | 强（NetBird 网关） |
| 国内生态 | 弱 | 强 | 强（中文优先） |

YuSui 不和 Teleport 抢开发者市场，瞄准**"国内多云 + 强合规审批 + 传统运维团队 + AI 时代的人机协同运维"**。最后一条是 v0.1 即落地的差异化。

---

## 3. 架构

### 3.1 分层

```
┌──────────────────────────────────────────────────────────────────┐
│  浏览器（运维 / 审批 / admin）                                    │
│   xterm.js 终端                                                   │
│   ┌── AI 工具（Claude Code / Codex）─ WebSocket attach ────────┐ │
└───┴───────────────────────────────────────────────────────────┴─┘
            │ HTTPS（REST + WebSocket）
┌──────────────────────────────────────────────────────────────────┐
│  YuSui Server (Go)  —— 唯一入口 + 唯一控制面 + 代理              │
│   ├─ Web UI / API                                                │
│   ├─ Project / Asset / Ticket Service                            │
│   ├─ Policy Engine（单层：Agent 本地 ACL）                       │
│   ├─ Agent Controller (gRPC)                                     │
│   ├─ Web Shell Service（PTY 代理 / AI attach / 命令拦截 / 录像）  │
│   ├─ NetBird Adapter（常驻一条 server↔agent 放行）               │
│   ├─ Identity Adapter（v0.1: 本地账号 / v0.3: + OIDC）           │
│   └─ Audit Logger（写 Postgres 仅 INSERT）                       │
│                                                                  │
│   ↑ Server 本身也是 NetBird Peer，与所有 Agent 在 Overlay 里     │
└──────────────────────────────────────────────────────────────────┘
       │              │                │                │
       ▼              ▼                ▼                ▼
  ┌────────┐    ┌──────────┐    ┌──────────────┐  ┌──────────┐
  │Postgres│    │ NetBird  │    │ JumpServer   │  │Prometheus│
  │        │    │ Mgmt API │    │ (v0.2，可选， │  │ /Loki    │
  │        │    │          │    │  RDP/DB 协议) │  │          │
  └────────┘    └──────────┘    └──────────────┘  └──────────┘
                      │
   ╔══════════════════│═══════════════════════════════════════════╗
   ║                  ▼  NetBird Overlay (WireGuard)              ║
   ║   ┌──────────────────────┐                                    ║
   ║   │ YuSui Server (Peer)  │── SSH 客户端 ──┐                   ║
   ║   └──────────────────────┘                │                   ║
   ║                                            ▼                   ║
   ║                            ┌──────────────────────┐            ║
   ║                            │ YuSui Agent (Peer)   │            ║
   ║                            │ = NetBird Routing    │            ║
   ║                            │ + 本地 ACL（nftables）│            ║
   ║                            │ + 持久 gRPC 上行 Server│            ║
   ║                            └──────────┬───────────┘            ║
   ╚═══════════════════════════════════════│════════════════════════╝
                                           │ 项目私网 (10.x / 172.x)
                  ┌────────────────────────┼───────────────────────┐
                  ▼                        ▼                       ▼
            ┌──────────┐             ┌──────────┐           ┌──────────┐
            │ MySQL RDS│             │  K8s API │           │ Win 业务 │
            └──────────┘             └──────────┘           └──────────┘
                            项目 A 资产（不装 NetBird）
```

**两条关键路径**：
- **运维操作流**：浏览器 → YuSui Server（PTY + 命令过滤 + 录像）→ Server-Peer → Agent → 资产 sshd
- **控制流**：浏览器 → Server REST/WebSocket → Postgres / NetBird API / Agent Controller

每个**项目**部署 1 个（v0.3 起 2+）**YuSui Agent**。它是该项目唯一的 NetBird Peer，所有进入该项目的流量都从这里穿过。（draft10：Agent 不再把项目私网注册为 Network Route；Server 直连 Agent 的 overlay IP，由 Agent 用户态 per-ticket L4 转发到资产——详见 [docs/02 §2.1/§2.3](docs/02-agent-design.md)、[docs/04 §4.1](docs/04-netbird-adapter.md)。）

> 详细架构（数据流路径、Server 内部依赖、部署拓扑）见 **[docs/01-architecture.md](docs/01-architecture.md)**；Agent 内部模块与规则引擎见 **[docs/02-agent-design.md](docs/02-agent-design.md)**。

### 3.2 组件职责

**YuSui Server**
- 唯一面向用户的入口与代理。
- NetBird Management 对运维人员**透明**——不允许直接登 NetBird UI 改 ACL。
- 控制**单层** ACL：Agent 本地规则（资产/端口级 + 来源为 server-peer IP，v0.1 单副本 1 个、v0.3 多副本一组）。NetBird 侧只有一条常驻的"server-peer ↔ all-agent-group"放行（destination 限项目 Agent group + 任意端口），工单创建时**不再写 NetBird**。
- **承接所有终端会话**：自研 Web Shell（SSH 协议），WebSocket 接入 + AI attach + 命令拦截 + asciinema 录像。
- 所有动作落审计表，包括"自动过期回收"也算一条审计。

**Web Shell Service**（隶属 Server，单独章节见 [docs/09](docs/09-web-shell.md)）
- 每个会话由 server 持有 PTY；多挂接者（human / AI / observer）通过 WebSocket attach。
- 单 primary 控制权 + 转交 + human 抢夺。
- 按行缓冲危险命令规则（block/confirm/warn 三档；项目级/资产级/AI源 分别配置）。
- 录 asciinema v2，每段输入打来源标签。

**NetBird Adapter**
- 启动期一次性建立常驻策略（server-peer 到 agent group 放行）。
- 项目/Agent 注册时分配 Group，运行期基本不动。（draft10：不再分配 Network Route。）
- 调用面收敛——不再每张工单 CRUD Policy，调用量与工单数解耦。

**Agent Controller**
- 与各项目 Agent 维持长连接（gRPC stream + mTLS）。
- 下发**临时 ACL** 规则：来源为 `server-peer-ip`（v0.1 单副本 1 个，v0.3 多副本为一组），目标 `asset_ip:port`，TTL = ticket 时长。
- 接收 Agent 健康心跳、Route 状态、流量计数、(v0.2) 连接级日志。

**YuSui Agent（v0.1 即必需，draft10：纯 Go `agent.exe`，Windows 原生）**
- **NetBird Peer + daemon 监管**：**管理**官方 NetBird daemon（经其本地 gRPC API）入网拿 overlay IP；不内嵌、不 fork；**不广播 Network Route**。
- **per-ticket 用户态 L4 转发器（即 ACL）**：每张工单一个 overlay listener → 资产 `ip:port`。NetBird 一条放行只到 "server-peer 可以到 agent"；具体 "可以到哪台资产的哪个端口" 由转发器（listener 存在 + 源白名单 + 固定目标）控制。（旧：nftables，draft10 起降为可选 Linux 实现。）
- **资产发现**：根据配置的网段做被动发现（ARP/扫描），上报候选资产给 Server。MVP 仅做"已知 IP 列表上报"，不做主动扫描。
- **运行环境**：纯 Go 单二进制（draft10 标准为 Windows 原生 `agent.exe` + Windows 服务；Linux 可选），依赖官方 NetBird daemon（由独立 installer 安装）。
- **不做的事**：不解析应用层、不录屏、不执行用户命令——录像与命令拦截在 Server 侧的 Web Shell 完成。

---

## 4. 核心闭环：工单 → Agent 单层 ACL → Web 终端

```
[运维] 浏览器提交工单                                       [YuSui Server]
   │ "访问 project=alpha 的 mysql(10.20.3.7:3306) 2h"        │
   ├───────────────────────────────────────────────────────▶│
   │                                                        创建 Ticket (pending)
[审批人] 浏览器审批 (step-up 重认证)                          │
   ├───────────────────────────────────────────────────────▶│
   │                                                        标记 approved
   │                                                         │
   │                       ┌── Agent Controller ────────┐    │
   │                       │ gRPC push 到 alpha-agent: │     │
   │                       │ ApplyRule{               │     │
   │                       │   rule_id: tk-42,        │     │
   │                       │   src: server-peer-ip,   │     │
   │                       │   dst: 10.20.3.7:3306,   │     │
   │                       │   expires: T+2h }        │     │
   │                       └──────────────────────────┘     │
   │                                              ◀── ack ──│
   │   通知运维：可在工单页打开终端                            │
[运维] 点"打开终端" → 浏览器 WebSocket ─attach─▶ Web Shell  │
   │                                              │           │
   │                              Server 建 SSH ▼            │
   │           流量：Server-Peer ─P2P─▶ Agent ─L4转发─▶ mysql │
   │                                                          │
[AI] (可选) Claude Code attach 同会话, source=api            │
   │                                                          │
   │           每条 stdin 经命令过滤器 (block/confirm/warn)    │
   │           每帧输出 + 输入写 asciinema 录像                │
   │                                                          │
   │   ⏰ T+2h 或主动归还：                                    │
   │   ① Server 关闭所有 attach，结束 PTY                     │
   │   ② Agent Controller → RevokeRule(tk-42)                │
   │   ③ Ticket=closed，录像归档，写审计                       │
```

**关键设计点**
- **NetBird 一条常驻**：server-peer-group → all-agent-group 一条永久 accept（destination 限 agent group + 任意端口）。新工单**不写 NetBird**，工作量大幅简化。
- **真正的 ACL 在 Agent**：per-ticket 用户态转发器（draft10），listener 绑 TTL、只接受 server-peer overlay 源 IP（v0.1 1 个 / v0.3 多副本一组）、固定转发到资产 IP:port。这就是唯一的"谁能进哪里"的事实表。（旧 draft1~9：nftables TTL set。）
- **状态机不再有双写双滚**：工单 Apply 只调一次 Agent；失败重试即可，不需要在 NetBird 侧回滚。
- **失联恢复**：Server 启动时与所有 Agent 对账规则集合即可。NetBird 侧的"常驻策略"按 marker 校验存在性。
- **审批人 ≠ 申请人**：硬编码。
- **紧急撤销**：管理员可一键吊销工单 → 自动 force-close 关联会话 + Revoke Agent 规则。

> 状态机详细、对账算法、SLA 目标见 **[docs/05-policy-engine.md](docs/05-policy-engine.md)**；Agent ↔ Server 的 gRPC 协议契约见 **[docs/03-agent-protocol.md](docs/03-agent-protocol.md)**；NetBird 调用细节见 **[docs/04-netbird-adapter.md](docs/04-netbird-adapter.md)**；Web 终端、AI attach、命令拦截见 **[docs/09-web-shell.md](docs/09-web-shell.md)**。

---

## 5. 数据模型（MVP）

v0.1 共 13 张表：
- 主表：`users` `projects` `agents` `assets` `asset_credentials` `tickets` `policy_bindings` `audit_logs` `netbird_global_settings`
- Web Shell 相关：`sessions` `session_attachers` `command_filter_events` `command_policies`

核心要点：
- `users` 表**无 `netbird_peer_id`**——运维不再是 NetBird Peer。
- `assets.project_id` 决定经由哪个 Agent；资产**不**持有 `netbird_peer_id`。
- 每项目唯一 `netbird_group_id`（Agent 所在 Group）。
- `policy_bindings` 只记录 Agent 侧外部 ID（v0.1 之后不再双写 NetBird）。
- `audit_logs` 仅 INSERT 权限，v0.3 启用链式哈希。
- `sessions` + `command_policies` 是 Web Shell 模型核心。

完整 DDL、约束、索引、迁移策略见 **[docs/06-data-model.md](docs/06-data-model.md)**。

---

## 6. 技术选型

| 层 | 选型 | 理由 |
|---|---|---|
| 后端 | Go 1.23+ | 与 NetBird 同语言，类型友好，运维侧亲切 |
| 框架 | chi 或 fiber | 轻量，符合 Google AIP API 设计 |
| ORM | sqlc + pgx | 不引入大 ORM，SQL 透明 |
| 数据库 | PostgreSQL 16 | 唯一持久化，JSONB 用于灵活 tag |
| 队列/定时 | river（Postgres-backed） | 不引入 Redis，过期回收够用 |
| 前端 | Vue 3 + Vite + Element Plus | 国内运维生态熟，开发快 |
| 鉴权 | **v0.1：本地账号（bcrypt + 可选 TOTP）**；OIDC 列入后续规划 | v0.1 不引入外部 IdP 依赖，降低部署门槛；接口预留 OIDC 入口 |
| 部署 | Docker Compose（v0.1）→ Helm（v0.3） | 渐进 |
| 监控 | Prometheus exporter | NetBird tunnel 指标 + YuSui 自身指标 |

**不选**：Java（重）、Node（运维侧不爱）、Rust（团队曲线陡 + NetBird 生态非 Rust）。

---

## 7. MVP 路线图

### v0.1 —— 闭环跑通（目标 9-11 周）

**Goal**: 一个真实运维场景能走完"申请 → 审批 → 浏览器打开终端（人 + 可选 AI attach）→ 命令拦截 → 到期断开 → 录像与审计可查"。

| 模块 | 任务 | 工作量 |
|---|---|---|
| 基建 | Repo / CI / Docker Compose / Postgres | 0.5 周 |
| 鉴权 | 本地账号（注册/登录/改密/可选 TOTP） + Identity Adapter 接口预留 | 0.5 周 |
| 项目/资产 | Project + Agent + Asset CRUD，手动录入 | 1 周 |
| **YuSui Agent v0.1** | 纯 Go `agent.exe`（Windows 原生），管理 NetBird daemon，gRPC 上行，per-ticket 用户态 L4 转发下发 | **2 周** |
| Agent Controller | 长连接、心跳、规则下发、对账协议 | 1.5 周 |
| 工单 | 提交 / 审批 / 状态机（**单层** Apply/Revoke） | 0.5 周 |
| NetBird Adapter | 启动期一次性建立 server↔agent 常驻策略；Group/Route 注册 | 0.5 周 |
| **Web Shell Service** | PTY 代理 + WebSocket attach + 控制权 + asciinema 录像 + 内置命令规则 | **1.5 周** |
| **命令拦截引擎** | 按行缓冲 + 规则匹配 + raw mode 探测 + paste 处理 + 实时热加载 | 0.5 周 |
| 回收 | river 定时任务 + 启动时对账 + force-close 联动 | 0.5 周 |
| 审计 | 写入 + UI 查询 + 录像回放（asciinema-player） | 0.5 周 |
| UI | 项目 / 资产 / Agent / 工单 / 终端 / 审计 / 命令策略 七页面 | 2 周 |
| 文档 | 部署文档 + demo 场景 + 内置命令规则说明 | 0.5 周 |

**显式不做**：HA（Agent + Server 单点）、多租户、视频录屏（只录文本流）、RDP/DB Web 客户端、Webhook 通知、移动端、Agent 自动扫描、eBPF、端口转发模式。

**MVP 验收**：单机部署 Server + 2 个项目各 1 个 Agent + 每项目 2 台 Linux 资产，演示完整链路：浏览器提工单 → 审批 → 打开终端 → 执行命令（含被 block 的 `rm -rf /` 与 confirm 的 `dd`）→ AI 工具 attach 旁观 + 接管 → 到期自动断 + 录像回放。

---

### v0.2 —— 协议扩展 + 审计深化 + 项目级权限（6-7 周）

- **项目级 approver 作用域**（draft7 提级到 v0.2）：新增 `user_project_memberships` 表 + UI；approver 仅能审批所属项目工单。是 v0.1 已知缺口的修复。
- **Web Shell Service 拆进程**（draft7 新增）：独立二进制 + UDS 通信 + 最小权限 namespace；为 §7.8.2 的所有安全缓解措施实际生效奠定基础。
- **JumpServer 集成（可选启用）**：覆盖 RDP / MySQL / PostgreSQL / Redis / K8s exec 等 SSH 以外协议；YuSui 工单批准时联动写 JumpServer 临时授权；JumpServer 部署在 Server 同侧，作为 NetBird Peer 经 Agent 访问资产
- Agent 转发连接级日志（基于 eBPF 或 conntrack）到 Server
- Agent 资产被动发现（ARP/网段扫描）+ 人工确认入库
- Agent 自动化部署：Ansible playbook + 一键安装脚本
- 通知集成：飞书/钉钉/企业微信 webhook
- **工单内命令白名单**：审批人可在审批时圈定允许的命令 prefix，会话内 server 临时叠加 block-all-except 规则——把"零信任"延伸到命令层

---

### v0.3 —— 多云、高可用、能力扩展（7-9 周）

- **OIDC 接入**：通过预留的 Identity Adapter 增加 Keycloak/Authentik provider；本地账号与 OIDC 用户共存，可双轨运行；OIDC 用户绑定时校验邮箱一致
- **Server 水平扩展**：Session 亲和性路由，多副本部署
- **Agent 双活**：每项目 ≥2 Agent，Server 向两个 Agent 双写转发器并按 `forward_addr` 故障切换（draft10：不再用 NetBird Routing Peer HA Group）
- **端口转发模式**：发一次性 token，运维本地 ssh 经 Server 转到资产；牺牲应用层审计换 IDE 工作流
- Agent eBPF 模式（替代 nftables，连接级日志 + 更细粒度）
- **会话级独立审计背书**：YuSui CA 为每次 SSH 会话签发短期证书（或一次性密钥对），资产 sshd 把 cert key-id / 指纹写进自身日志——"会话确实发生过、用的哪张凭证"由 sshd 产生、不经 server PTY 流，独立于 server 是否被攻陷（见 [docs/07 §7.12](docs/07-security.md)）。副作用：简化资产凭证轮换（资产侧只信 CA，不再每台存长期 SSH key）
- TURN/STUN 分布式部署文档与监控
- PostgreSQL HA（Patroni）
- Helm Chart
- P2P 成功率指标 + 回落 TURN 告警（国内 UDP 坑）
- Agent 失联熔断：超过阈值自动通知 + 工单暂停下发
- **AI 协作模式**：长任务独立 token、多 primary 并发（结对/教学）

---

### v1.0 —— 生产可用（开放时间）

- 多租户（一个实例服务多个部门）
- 细粒度 RBAC（审批人按资产范围授权）
- 策略模板与批量审批
- 合规报告导出（等保 2.0 友好）
- 一键迁移工具（从 Teleport / 传统跳板机迁移资产）

---

## 8. 关键风险与未解决问题

| 风险 | 影响 | 应对 |
|---|---|---|
| **Server 单点故障**（v0.1） | 整个 YuSui 全员不可用 | v0.1 明确告知"实验/小规模可用"；v0.3 水平扩展 |
| **Server 流量瓶颈 / 数据面集中化**（draft7 点名） | 自研代理后所有 SSH 字节穿 server；同进程做 WireGuard + SSH + 逐字节过滤 + 录像。200 会话 × 1MB/s ≈ 1.6Gbps。这是 draft6 取消运维端 NetBird 后的合理代价：把 P2P 想省的中心化又请了回来。 | v0.1 单 server 目标 200 并发 session；上线前必须做带宽/CPU 压测；v0.3 水平扩展；大文件传输强制走 SFTP/独立通道（v0.3） |
| **Server 进程被劫持** | 攻击者接管所有终端会话 | **v0.2 起** Web Shell Service 独立进程/容器、最小权限、CA 私钥不在该进程（v0.1 同进程，见 [docs/07 §7.8.2](docs/07-security.md)）；mTLS + 严格 SCM |
| **Agent 单点故障**（v0.1） | 整个项目全部资产不可达 | v0.1 明确告知"实验/小规模可用"；v0.3 双活 |
| **Agent 与 Server 失联但 NetBird 通** | nftables 没更新 → 旧规则继续或新规则进不去 | Agent 本地持久化规则 + nftables TTL 自动清 + 心跳超时熔断 |
| **Agent 主机被攻陷** | 攻击者获得整个项目网段访问 | Agent 主机最小化；nftables 只接 server-peer-ip 入站；v0.3 加 eBPF 进程白名单 |
| **危险命令拦截被绕过** | 用户用 alias/encode/heredoc 绕过 | 文档明示限制；v0.2 加工单内命令白名单；OS 侧 auditd 作为最后一道 |
| **AI attacher 失控大量发命令** | 资产被误操作 | per-attacher RPS 限流；AI 来源叠加更严规则；human 始终可一键夺权 |
| Agent ↔ Server 协议演进 | 升级时 schema 不兼容 | gRPC + 显式版本字段；Agent 兼容 N-1 版本 Server |
| NetBird API 不稳定 / 破坏性变更 | Adapter 反复重写 | Adapter 单独模块、契约测试、锁定 NetBird minor 版本；v0.1 后调用量已大幅缩减 |
| 国内 UDP QoS 差，P2P 失败率高 | TURN 带宽爆炸 | v0.3 必须做 P2P 成功率监控；提供"运营商优选 TURN" |
| 审批流过于简单不符合国内合规 | 客户不买账 | v1.0 引入可配置审批流（串签/会签/抄送） |
| YuSui 自身权限被滥用 | "撤销审计 + 私改 ACL" | 审计表 append-only + 数据库账号最小权限 |

**未解决**
- 应急访问（break-glass）流程：审批人都不在怎么办？v1.0 之前用"二级管理员手动覆盖 + 强通知"过渡。
- NetBird 自身宕机时，已建立的 P2P 隧道不受影响——但 YuSui 无法新发或回收策略。需要写运维 runbook。
- 是否要 fork NetBird？目前结论：**不**。一旦 fork 就要承担合并上游的成本，MVP 阶段所有需求 API 都能满足。
- 本地 IDE/DBeaver 用户的需求：v0.3 端口转发模式落地前，让步还是另写一段"非生产场景临时方案"？
- 危险命令的"override 工单"流程：申请人想跑被拦的命令时，是不是该有一条"emergency override"工单走 admin 实时审批？v0.2 评估。

---

## 9. 下一步

1. 建仓库骨架：`yusui-server`（Go，含 Web Shell Service）、`yusui-agent`（Go）、`yusui-web`（Vue）、`yusui-deploy`（compose/helm）。
2. 写 NetBird Adapter 的接口契约 + mock，先于业务逻辑。
3. 找一个真实场景做 design partner（自己的家庭实验室 / 一家友好公司）。
4. v0.1 完成后再写 README 和对外宣传，避免过早曝光。

---

*本文档随实现演进，每个版本结束后更新对应小节并标注变更日期。*

---

## 10. 详细设计文档

| # | 文档 | 简介 |
|---|---|---|
| 01 | [架构](docs/01-architecture.md) | 分层 / 数据流 / 部署拓扑 / Server 内部依赖 |
| 02 | [Agent 设计](docs/02-agent-design.md) | 进程模型 / nftables 规则引擎 / HA / 资源占用 |
| 03 | [Agent ↔ Server 协议](docs/03-agent-protocol.md) | gRPC + mTLS / 完整 proto / 错误与重试 |
| 04 | [NetBird Adapter](docs/04-netbird-adapter.md) | 概念映射 / 幂等 / 版本兼容 / 错误分类 |
| 05 | [Policy Engine](docs/05-policy-engine.md) | 工单状态机 / Agent 单层 Apply/Revoke / 对账 / 并发 |
| 06 | [数据模型](docs/06-data-model.md) | 完整 DDL / 约束 / 索引 / 迁移 / 保留策略 |
| 07 | [安全模型](docs/07-security.md) | 信任边界 / STRIDE / 密钥管理 / 等保对齐 |
| 08 | [JumpServer 集成](docs/08-jumpserver-integration.md) | v0.2 接入：拓扑 / API 契约 / 三阶段双写 / 录像聚合 |

阅读顺序见 [docs/README.md](docs/README.md)。

---

## 变更日志

| 日期 | 版本 | 说明 |
|---|---|---|
| 2026-06-01 | v0.1-draft1 | 首版总览 |
| 2026-06-01 | v0.1-draft2 | 引入 Agent-as-sub-router 硬约束（资产不装 NetBird） |
| 2026-06-01 | v0.1-draft3 | 拆分 docs/，DESIGN.md 收敛为总览 |
| 2026-06-05 | v0.1-draft4 | 补 docs/08 JumpServer 集成设计；v0.2 工作量调至 5-6 周 |
| 2026-06-05 | v0.1-draft5 | OIDC 推迟到 v0.3；v0.1 改用本地账号（bcrypt + 可选 TOTP），Identity Adapter 接口预留 |
| 2026-06-05 | v0.1-draft6 | 重大架构转向：取消运维端 NetBird 客户端，自研 Web SSH 作为 v0.1 唯一入口；NetBird 拓扑收敛为 server↔agent，双层 ACL 简化为单层 Agent；新增 docs/09 Web Shell（AI attach + 危险命令拦截）；JumpServer 降为 v0.2 可选扩展（覆盖 SSH 外协议）；v0.1 工作量 9-11 周，v0.2 5-6 周 |
| 2026-06-06 | v0.1-draft7 | 评审修正 17 条：FROZEN→DEGRADED 解耦（不撤工单）；AI per-attach capability token；命令过滤改远端 prompt 周期解析（不再拦 client stdin）；ApplyRule 多源 IP repeated；§7.8.2 honesty（v0.1 同进程，缓解 v0.2 起）；asset_credentials 表补齐；Agent SNAT/masquerade 显式；资产 syslog session sentinel；PG 失联本地 WAL + fail-closed；合并 PolicySvc → Policy Engine；v0.1 approver 全局诚实化；per-agent reconcile 锁；nft element comment 自描述；setup_key vs register_token 术语澄清；target_selector 审批时冻结；WORM 措辞收缩；数据面集中化风险点名。v0.2 工作量 6-7 周。 |
| 2026-06-06 | v0.1-draft8 | 评审第二轮一致性清扫（应用第 4–10 项）：§5.2 状态图 `agent freeze`→`≥30min`；§5.5.2 删除"全局停批"表述（统一 per-agent 锁）；§1.5 写审计归 Audit Sink（消除 Engine→Service 回边）；多源 `src_peer_ips` 表述对齐 03 proto；表数量统一为 13（补 `asset_credentials`/`netbird_global_settings`）；§9.9 与本文 §8 风险表 Web Shell 隔离 v0.1 诚实化 + 命令过滤改远端解析对齐；register token 术语澄清。**第 1–3 项（§7.2 STRIDE 残留、§7.12 哨兵独立性、命令 block 时序）保留待决。** |
| 2026-06-06 | v0.1-draft9 | 落定评审第 1–3 项：§7.2 STRIDE 改引 capability token（不再称 source 强制标记）；§7.12 哨兵诚实降级为"独立存储锚点、非对抗会话期攻陷"，并给出 v0.3 sshd 级独立背书（per-session SSH 证书 key-id）升级路径（已登记入 §7 v0.3 路线图）；§9.7.2 明确 block 为"回车键同步闸控"+ echo-off 失明，修正 §9.7.1.1 事后检测表述。附带修掉 §9.5 误引的 `session_recordings`（改指 `sessions`/`session_attachers`）。 |
| 2026-06-09 | v0.1-draft10 | Agent 平台 + 执行机制 + 路由模型重构（起因：① 跨项目可重叠私网，广播 CIDR 为 Route 会在 overlay 路由冲突；② 运维 Agent 基本是 Windows，nftables 不可用）：**取消 Network Route 广播**，Server 直连 Agent overlay IP；per-ticket 访问从 nftables 改为 **Agent 进程内用户态 L4 转发器**（listener 生命周期 = 工单 TTL，源白名单 + 固定目标即 ACL），nftables 降为可选 Linux 实现（`Enforcer` 接口）；Agent 改 **纯 Go `agent.exe`（Windows 原生）**，**管理官方 NetBird daemon**（经其本地 gRPC API，不内嵌/不 fork），首次安装由独立 installer 完成；`ApplyRule` 经 `AckCommand.forward_addr` 回传监听地址，Web Shell 连该地址而非裸资产 IP；`projects.cidrs` 退化为作用域/校验元数据；Server 侧 NetBird 走 sidecar 容器。改动 CLAUDE.md 不变量 #1/#2/#9 + docs/02/03/04。**纯文档，未动现有可跑代码（M3 nft 实现待后续按 `Enforcer` 接口迁移）。** |
| 2026-06-10 | v0.1-draft11 | draft10 执行模型已实现并 CI 守门(用户态转发器 + `forward_addr` + Web Shell 连转发器 + Windows agent.exe 交叉编译 + `e2e-grpc` 真 agent 回归)。新增 **[docs/10 TODO](docs/10-todo.md)** 作为实现欠账单一事实表,记录 draft10 收尾项(`overlay.Netbird` 真守护进程管理、Windows installer、server 重启重建 `forward_addr`、多 target 转发、Agent BoltDB 持久化)与核实出的偏离:**调度器是进程内 ticker 非 river、gRPC 是 insecure 非 mTLS、Agent 无 BoltDB(纯内存)、server 重启未重建 forward_addr**。 |
