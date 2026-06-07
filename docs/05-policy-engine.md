# 05 · Policy Engine（Agent 单层 ACL 编排）

## 5.1 它解决的问题

YuSui 的"放行"在 v0.1（draft6 起）是个**单写事务**：只需要 Agent 一侧的 nftables 规则生效，连接就能通——因为 NetBird 在启动期一次性放行了"server-peer ↔ 所有 agent-group"，工单不再触碰 NetBird。

Policy Engine 做四件事：
1. 把 Ticket 状态机推到下一格。
2. 通过 Agent Controller 给目标项目 Agent 下规则 / 撤规则。
3. 处理失败的重试与对账。
4. 触发到期回收并联动 Web Shell 关闭活跃会话。

> 历史背景：v0.1-draft1~5 设计过"NetBird Policy + Agent nftables 双层 ACL 双写双滚"。draft6 取消运维端 NetBird 客户端后，NetBird 一侧只剩 server-peer 一个客户端 Peer，工单不需要在 NetBird 上做 per-ticket 操作。本文档反映 draft6 后的简化方案。

## 5.2 工单状态机

```
                 submit
   ┌────────┐   ──────▶  ┌──────────┐
   │ DRAFT  │             │ PENDING  │
   └────────┘             └────┬─────┘
                               │ approve / reject
              ┌────────────────┼──────────────────┐
              ▼                ▼                  ▼
        ┌──────────┐    ┌─────────────┐    ┌──────────┐
        │ REJECTED │    │  APPROVED   │    │ EXPIRED  │
        └──────────┘    │ (待下发)     │    │（未审批超时）│
                        └──────┬──────┘    └──────────┘
                               │ apply ok（Agent 单层）
                               ▼
                        ┌─────────────┐
                        │   ACTIVE    │
                        └──────┬──────┘
                               │ expires / admin revoke / agent freeze≥30min / user release
                               ▼
                        ┌─────────────┐
                        │  REVOKING   │
                        └──────┬──────┘
                               │ revoke ok（Agent 单层 + 会话 force-close）
                               ▼
                        ┌─────────────┐
                        │   CLOSED    │
                        └─────────────┘

  失败分支：
  APPROVED ──apply 失败 N 次──▶ APPLY_FAILED（人工介入）
  REVOKING ──revoke 失败──▶ REVOKE_PENDING（持续重试 + 告警；同时 force-close 会话以阻断访问）
```

所有状态变更必须经过 `PolicyEngine.Transition(ticket_id, from, to, reason)`，写 audit_logs 同事务。

## 5.3 Apply 流程（APPROVED → ACTIVE）

```
T0: 校验前置（user / agent / asset / project agent online）
T1: AgentController:
      ApplyRule(rule_id=tk:N, 
                src=<server-peer-ips[]>,      # v0.1=1，v0.3 多副本=多个（见 03 §3.2）
                dst=<asset.ip>:<port>,
                proto=tcp, 
                expires_at=<now+duration>)
      → 等待 Agent ack OK
      → 写 policy_bindings.agent_rule_id / applied_at
T2: PolicyEngine.Transition(N, APPROVED, ACTIVE)
T3: 通知用户：可在工单页打开终端
```

**失败处理**：
- Agent 拒绝（ack=FAILED） → 重试 3 次（指数退避）→ 仍失败 → 标 APPLY_FAILED，告警；不进 T2。
- Agent 离线 → 写 binding=apply_pending，进入"Agent 上线即下发"队列；超 5 min 告警。
- ack 丢失（Server 重启） → 重启对账自动续跑（rule_id 幂等）。

**注意**：不需要回滚 NetBird（没改它），所以失败处理简单很多。

## 5.4 Revoke 流程（ACTIVE → CLOSED）

```
T0: PolicyEngine.Transition(N, ACTIVE, REVOKING)
T1: SessionSvc.ForceCloseByTicket(N)
      → Web Shell Service 对所有 attacher 发 closed{reason:ticket_revoked}
      → 关闭 PTY 与 SSH 客户端
T2: AgentController:
      RevokeRule(rule_id=tk:N)
      → 等待 ack OK（或 SKIPPED：本地已不存在）
T3: PolicyEngine.Transition(N, REVOKING, CLOSED)
```

**顺序选择**：**先关会话再撤规则**。理由：
- 会话关闭立即终止已有 TCP 连接，应用层操作马上停止；
- 规则撤销让"新建连接"不再可能；
- 若反过来，已有连接可能在 SSH client 池中保活几秒，给攻击窗口。

**Agent 撤规则失败 → 状态保持 REVOKING，进入 revoke_pending 队列，每 30s 重试；同时 force-close 已经做过，访问已断**。

### 触发源

| 触发 | 处理 |
|---|---|
| **到期**（`expires_at`） | river 定时任务触发；正常 REVOKE |
| **管理员撤销** | UI/API；正常 REVOKE |
| **用户提前归还** | UI"我用完了"按钮；正常 REVOKE |
| **用户被禁用 / 角色变化** | Identity 同步发现 → REVOKE 该用户所有 ACTIVE ticket |
| **Agent 短时失联**（< `revoke_after_freeze_sec`，默认 30 min） | **不 REVOKE**。ticket 主状态保持 ACTIVE，子状态打 DEGRADED；force-close 关联会话（用户连不上但不丢工单）；Agent 恢复后对账自愈，子状态清回 ACTIVE，用户可重开终端 |
| **Agent 超长失联**（≥ `revoke_after_freeze_sec`） | 才真正 REVOKE；avoid 永远卡 DEGRADED |

**为什么 Agent 短时失联不撤工单**：国内 UDP QoS 抖动 + WireGuard 重协商常见，60s 心跳超时不代表"该工单要被剥夺"。一次抖动撤掉项目里所有进行中工单 = 全员重新走审批 = 运营事故。draft6-rev1 起把 freeze 与 revoke 解耦：Agent freeze 影响"现在能不能用"，不影响"权限本身"。

历史：draft6 原写法把 `agent freeze` 列为 REVOKE 触发，与 §5.8 "Agent 宕机标 DEGRADED" 自相矛盾。本节是修正后的正式定义。

## 5.5 对账（Reconciler）

### 5.5.1 触发

- 进程启动后立即一次
- 周期触发（默认 5 min）
- Agent reconnect 时针对该 Agent 单独触发

### 5.5.2 与 Agent 对账（核心）

```
expected = SELECT rule_id 
           FROM policy_bindings JOIN tickets USING (ticket_id)
           WHERE agent_id = X AND tickets.status = 'ACTIVE'
agent_reported = ReconcileRequest(X) → 返回 [rule_id...]

to_apply  = expected - agent_reported   # Server 觉得该有，Agent 没有
to_revoke = agent_reported - expected   # Agent 有，Server 觉得不该有

for r in to_apply:  ApplyRule(r)
for r in to_revoke: RevokeRule(r)
```

对账只持 `agent:<id>` 锁（见 §5.6.1），不全局暂停审批；目标为其他 Agent 的新批准不受影响，单 Agent 对账通常 < 2s。

### 5.5.3 NetBird 侧的极简对账

仅校验"常驻策略仍然存在且未被改动"：

```
SELECT count(*) FROM netbird /api/policies WHERE name = 'yusui:builtin:server-to-agents'
expected: 1

字段比对（src group, dst group, action, enabled）逐项校验，发现差异：
  - 缺失 → 重建
  - 字段被改 → 重置回 YuSui 期望值 + audit alert "external_tamper"
```

频率与 Agent 对账一致。这是廉价操作。

### 5.5.4 异常发现

- **expected 有但 Agent 上没有**：自动 Apply 一次；仍失败 → APPLY_FAILED + 告警。
- **Agent 上有规则但 DB 完全没记录**：立即 Revoke，audit 一条 `unexpected_rule_removed`（疑似手动改动）。

## 5.6 并发与一致性

### 5.6.1 锁

| 锁 | 粒度 | 用途 |
|---|---|---|
| `ticket:<id>` | 单 ticket | 状态机变更串行化 |
| `agent:<id>` | 单 agent | 对账与下发不重叠 |
| ~~`reconcile-global`~~ | ~~全局~~ | ~~大对账时阻止新批准~~（draft7 取消）|

实现：Postgres advisory lock（pg_advisory_xact_lock），Server 多副本（v0.3）天然协作。

> **draft7 修正**：draft6 引入"对账期间全局暂停审批"是过保守。Agent 频繁 reconnect（国内网络）会触发频繁全局停批，体验差。修法：**per-agent reconcile 只持 `agent:<id>` 锁**——足够保证该 Agent 的下发与对账不重叠；新工单若 target 是其他 Agent 不受影响；若 target 正是该 Agent，自然阻塞在 `agent:<id>` 上，毫秒级。
> 仅当 admin 主动触发"全局对账"（运维工具，少见）才持 `reconcile-global` 锁。

### 5.6.2 状态原子性

每次状态转移用单事务：
```sql
BEGIN;
  SELECT * FROM tickets WHERE id=N FOR UPDATE;
  UPDATE tickets SET status=..., updated_at=now() WHERE id=N AND status=expected;
  -- 若 affected_rows=0 → 状态已被别人改，回滚
  INSERT INTO audit_logs (...) VALUES (...);
  UPDATE policy_bindings SET ... WHERE ticket_id=N;
COMMIT;
-- 然后才调外部 API（Agent gRPC）
```

外部 API 调用不进事务（避免长事务持锁），结果回写时再开一个小事务。

## 5.7 时间预算（SLA 目标）

| 阶段 | p50 | p99 |
|---|---|---|
| 审批 → ACTIVE | 0.5s | 3s（NetBird 不再参与，更快） |
| ACTIVE → CLOSED（到期）| 0.8s（含会话关闭）| 4s |
| 紧急撤销端到端 | 1s | 5s |
| 对账完成 | 3s | 15s |

超 p99 写指标 + 告警。

## 5.8 边界场景

**用户在 ACTIVE 期间被禁用**：IdP/本地 admin 把用户标 inactive → IdentitySvc 发事件 → PolicySvc 批量 REVOKE 该用户的所有 ACTIVE ticket。

**Agent 在 ACTIVE 期间宕机**：Heartbeat 超时 → 子状态 DEGRADED（主状态机仍 ACTIVE）。Server 同时 force-close 该 Agent 上所有 attach 中的 Web Shell session（用户的浏览器会看到 `closed{reason:agent_degraded}`），避免半开 TCP 让用户误以为命令在跑。Agent 恢复 → 自动对账 → 子状态清回 → 用户可重开终端，工单全程不需要重新审批。

仅当失联超过 `revoke_after_freeze_sec`（默认 30 min）才升级为 REVOKE。这条阈值在风险表里另列。

**Project 删除**：拒绝删除若有 ACTIVE ticket；强制删除则触发批量 REVOKE，全部 CLOSED 后才真正删除项目并清理 NetBird Route / Group。

**同用户对同资产多张 ticket**：各自独立 Agent rule（rule_id 不同），互不影响。审计清晰。

**Agent 双活（v0.3）**：同一规则下发到两个 Agent；revoke 时两个都撤。任一失败重试，期间访问仍可由另一 Agent 提供。

## 5.9 未决问题

- 同一用户对同一资产的多张 ticket：合并为一条 nftables 规则还是各自独立？v0.1 独立；v0.3 视规模评估合并。
- 是否支持"会签"（多审批人都同意才生效）？v1.0 引入。
- 流量审计上 Agent 报上来的 packet_count 在 Server 怎么存？v0.1 只存最终值；v0.2 改 time-series（Loki / Prometheus pushgateway）。
- 工单到期前 5 min 是否主动通知？避免运维操作中突然断线。v0.2 加。
- 用户在会话中主动 `release_primary` 但不归还工单：会话关 vs 工单 ACTIVE 继续？倾向会话关、工单仍 ACTIVE 可重开。
