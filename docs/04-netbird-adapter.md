# 04 · NetBird Adapter

## 4.1 职责

把 YuSui 的领域概念翻译为 NetBird API 操作，并屏蔽 NetBird 的实现细节与版本变更。

**输入**：Server 内部领域事件（项目创建、Agent 注册、Server 启动）。**v0.1-draft6 起，工单 Apply/Revoke 不再调用本 Adapter**。
**输出**：NetBird Mgmt REST 调用结果 + YuSui 内部映射记录（external_id ↔ domain_id）。

**严格只调 Mgmt REST API**。不直接读 NetBird 数据库、不解析 NetBird 内部协议、不 patch NetBird 源码。

## 4.2 概念映射

| YuSui 领域 | NetBird 概念 | 关系 |
|---|---|---|
| YuSui Server | Peer + Group（`yusui:server-peers`）| Server 启动时自动注册自己为 Peer，所属 Group 用于常驻策略的 source |
| Project | Group（`yusui:project:<code>:agents`）| 1:1，Project 创建时一并建 Group |
| Agent | Peer | 1:1，Peer 加入 `yusui:project:<code>:agents` Group |
| Asset | （无对应概念） | YuSui 私有，NetBird 不感知 |
| Ticket | （无对应概念）| **v0.1-draft6 起 NetBird 不感知 ticket**；细粒度 ACL 在 Agent nftables |
| Project CIDR | Network Route | 挂在 Agent Peer 上 |
| 常驻策略 | Policy（`yusui:builtin:server-to-agents`）| 单例，Server 启动期建立；不再 per-ticket |

## 4.3 接口

```go
package netbirdadapter

type Adapter interface {
  // 启动期：一次性
  EnsureBuiltinPolicy(ctx) (policyID string, err error)
  EnsureServerPeer(ctx, hostname string, setupKey string) (peerID string, err error)
  EnsureServerGroup(ctx, peerID string) (groupID string, err error)

  // 项目 / Agent（运维触发）
  EnsureProjectGroup(ctx, projectCode string) (groupID string, err error)
  AddAgentToProjectGroup(ctx, agentPeerID, projectGroupID string) error
  AssignNetworkRoute(ctx, peerID string, cidrs []netip.Prefix) (routeID string, err error)
  RemoveAgent(ctx, peerID string) error

  // 对账
  GetBuiltinPolicy(ctx) (Policy, error)              // 验证 yusui:builtin:server-to-agents 仍存且未被改
  ListYuSuiGroups(ctx) ([]Group, error)              // 用于项目对账（孤儿 group）
  ListAgentPeers(ctx) ([]Peer, error)                // 验证 Agent peer 在 NetBird 侧存在
}
```

历史：v0.1-draft1~5 的 `ApplyTicketPolicy` / `RevokeTicketPolicy` / `EnsureUserPeer` 接口在 draft6 移除。临时 Group 概念也随之消失（不再有 per-ticket 临时 Group）。

## 4.4 常驻策略的 NetBird JSON

Server 启动期一次性写入（首次安装或对账发现缺失时）：

```json
{
  "name": "yusui:builtin:server-to-agents",
  "description": "YuSui v0.1+ 常驻策略：允许 server-peer 到所有 agent，端口由 Agent 本地 nftables 限制",
  "enabled": true,
  "rules": [{
    "name": "yusui:builtin:server-to-agents:r0",
    "sources": ["<group_id_of_yusui:server-peers>"],
    "destinations": ["<group_id_of_each_yusui:project:*:agents>"],
    "protocol": "all",
    "ports": [],
    "action": "accept",
    "bidirectional": false
  }]
}
```

注意：destination 是**所有 yusui-project 的 Agent Group 列表**，会随项目数量增长。新项目创建时同步 PATCH 这条 Policy 加入新 Group。删除项目时反之。

**为什么不一条 Policy / 一条 destination Group**？因为 NetBird 不允许"通配 group"。我们要么每项目独立 Policy（n 条），要么单 Policy + PATCH destinations（1 条）。后者更易维护，所以选择 PATCH 模式。

## 4.5 幂等性

所有 `Ensure*` / `Add*` 必须幂等：
- `EnsureBuiltinPolicy`：以 `name=yusui:builtin:server-to-agents` 反查，存在则校验关键字段（sources/action/enabled）并修正，不存在才 POST。
- `EnsureProjectGroup`：先 List 找同名 Group，存在直接返回 ID。
- `EnsureServerPeer`：先用 setupKey 自己加入；启动后通过 `GET /api/peers` 找到自己的 peer_id 缓存到 DB。
- `RemoveAgent`：DELETE 时把 404 视作成功；同时把对应 group 从常驻策略 destinations 中移除。

幂等键统一是 `yusui:<scope>:<domain_id>`，且写到 NetBird 资源的 `name` 字段——这是失联恢复时唯一的锚点。

## 4.6 调用量级（v0.1-draft6 后大幅缩减）

| 时机 | NetBird API 调用数 |
|---|---|
| Server 启动 | ≤ 10（自我注册 + 常驻策略校验/创建 + 全量项目 Group 对账） |
| 项目创建 | 2-3（建 Group + PATCH 常驻策略 destinations + 建 Route 占位） |
| Agent 注册 | 2（建 Peer / 加入 Group / 挂 Route） |
| 项目删除 | 2-3（PATCH 常驻策略移除 destinations + 删 Group + 删 Route） |
| 工单 Apply | **0** |
| 工单 Revoke | **0** |
| 周期对账 | 4-5（GET 常驻策略 / GET 所有 yusui-group / GET 所有 yusui-peer / 比对） |

相对 draft5（每张 ticket 至少 4-6 次调用 + 复杂的回滚链），调用规模降低 1-2 个数量级，对 NetBird 实例的压力可忽略。

## 4.7 版本兼容

NetBird 当前 API 在 minor 版本之间偶有字段变更。Adapter 应对：

- 锁定支持的 NetBird minor 版本范围（例如 v0.30.x），在启动时调 `/api/health` 拿版本号比对，超出范围则告警且只读运行。
- 对响应解析使用宽容模式：未知字段忽略；缺失非必需字段用默认值。
- 集成测试用 **NetBird API contract tests**：在 CI 启一个真实 NetBird Mgmt，跑一遍 Adapter 的所有方法，pin 住期望响应字段集合。

## 4.8 错误分类

```go
type ErrClass int
const (
  ErrTransient ErrClass = iota  // 网络抖动、5xx → 上层重试
  ErrConflict                    // 409 → 拉最新状态后调和
  ErrAuth                        // 401/403 → 立即告警，停止下发
  ErrSchema                      // 4xx 字段不识别 → 版本不兼容
  ErrPermanent                   // 4xx 业务错误 → 标项目/Agent 失败
)
```

每个 Adapter 方法返回的 error 都实现 `Class() ErrClass`，Reconciler 据此决定重试 vs 告警。

## 4.9 速率限制

NetBird Mgmt API 没有公开限流。Adapter 内置：
- 全局令牌桶：默认 20 req/s（v0.1-draft6 后大幅低于 draft5 的 100，因为调用量小）。
- 同一对象的写操作串行化（按 resource path 上锁），避免 409。

## 4.10 缓存

Adapter 维护一个进程内只读缓存（TTL 60s）：
- Group ID by name
- Peer ID by hostname

避免重复 List。写操作发生时**主动失效**对应条目。重启冷启动不依赖缓存，全部回源。

## 4.11 启动期对账流程（详细）

Server 启动后立即执行：

```
1. GET /api/health → 校验 NetBird 版本兼容
2. GET /api/groups?name_prefix=yusui:
   ├─ 检查 yusui:server-peers 存在；不存在则 POST
   ├─ 对照 DB.projects，检查每个项目的 agents Group 存在；缺失则补
   └─ 如果有 yusui: 前缀但 DB 没记录的 Group → 报告，admin 手动决定删/保留
3. GET /api/peers?group=yusui:server-peers
   ├─ 当前 Server 进程的 hostname 没在里面 → 调 netbird up --setup-key 加入
   └─ 拿到自己的 peer_id，缓存到 netbird_global_settings.server_peer_id
4. GET /api/policies?name=yusui:builtin:server-to-agents
   ├─ 不存在 → POST 新建
   ├─ 存在但字段不符（destinations、action 等）→ PUT 修正 + audit 一条 external_tamper
   └─ 一致 → 仅更新 last_reconciled_at
5. 完成，Server 转入正常运行
```

启动期间不接受任何工单批准请求。

## 4.12 测试策略

- 单元测试：mock NetBird HTTP，验证调用顺序与幂等键。
- 契约测试：CI 中起真实 NetBird Mgmt（docker），跑端到端流程（项目创建 → Agent 注册 → 工单生命周期 → 项目删除）。
- 混沌测试（v0.3）：注入 5xx / 超时 / 网络分区，验证启动期重试与对账正确收敛。

## 4.13 未决问题

- 常驻策略 `destinations` 列表过长（>50 项目）时 PATCH 性能：评估单 Policy 切分为多条（按项目分桶）。
- 多 NetBird Mgmt 实例（不同地域）联邦？v1.0 之前不考虑，单 Mgmt 单实例。
- NetBird Setup Key 的生命周期管理：YuSui 自己签 vs 用 NetBird 接口生成？倾向后者（少存敏感物）。
- v0.3 Server 水平扩展时，多个 Server 副本都注册自己为 Peer？还是共用一个 server Peer + 内部分流？前者简单但 Peer 数翻倍；后者复杂但语义清晰。倾向前者。
