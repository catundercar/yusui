# 11 · Agent 注册 / 本地 daemon 与客户端

> draft11 设计。把"项目上的 agent"做成 **NetBird 式本地 daemon**:常驻服务 + 本地控制 API + 多客户端(CLI 先做,GUI 延后),**零接触自动注册 + 人工审核准入 + Server 下发 NetBird setup key 自动入网**。落地 docs/10 里门控未实现的 `overlay.Netbird`,并补客户端层。

## 11.1 形态与边界

```
[Windows GUI(延后)]─┐                          ┌─ 管理 ─▶ netbird daemon ─▶ NetBird overlay
[CLI(yusui-agent …)]─┼─(本地 gRPC·UDS/命名管道)─▶ yusui-agent daemon ─┤
                     ┘   status / up / down / logs │            └─ per-ticket 用户态 L4 转发器(已实现)
                                                    └─(gRPC over overlay)─▶ YuSui Server
```

**边界(守住现有不变量):**
- **客户端只做运维/状态,不碰策略**(不变量 #5):CLI/GUI 能看状态、触发入网/退网、看日志;**绝不编辑访问规则**(规则只来自 Server)。
- **daemon 管理官方 NetBird daemon,不内嵌**(draft10)。拓扑是两个 daemon:`yusui-agent`(管 netbird + 转发器 + 连 server)+ `netbird`(管 overlay)。
- **本地 API 不对外**:gRPC over Unix socket(Linux)/ 命名管道(Windows),仅本机客户端可达。
- **认证先不做 = 无密码学认证(暂无注册 token / mTLS),用人工审核当闸**。这是**临时态**,生产必须加回 token + mTLS(见 [10](10-todo.md))。

## 11.2 注册与审核流程(核心)

```
daemon 启动
  │ 1. 向 Server 注册(project_code + hostname,无 token)
  ▼
Server: 未知 agent → 自动建行,enrollment=pending(项目须已存在)
  │ 2. 返回 {agent_id, enrollment:"pending"};daemon 进入"待审核"轮询
  ▼
admin 在 UI 看到 pending agent → 审核 → 通过
  │ 3. Server 置 enrollment=approved,签发/绑定一个 NetBird setup key
  ▼
daemon 轮询/收到 approved + setup_key
  │ 4. overlay.Netbird:用 setup key 让 netbird daemon 入网,拿 overlay IP
  ▼
daemon 进入控制流(转发器绑 overlay IP),status=online
```

**为什么是"自动注册 + 人工审核"而不是 auto-accept**:auth 关掉后若直接接受,任何人都能往已知项目塞 agent 假冒边界。人工审核是 auth 缺位时的准入闸:admin 核对 hostname / 项目 / 来源后才放行并下发 setup key。**未审核的 agent 拿不到 setup key、不入网、不收任何工单规则**。

**角色分配**:daemon 不自己挑角色;Server 在自动建行时:项目无 primary → 建 primary;已有 primary 且无 secondary → 建 secondary;都满 → 拒绝("项目 agent 已满")。admin 在 UI 手建的 agent 默认 `enrollment=approved`(可信)。

## 11.3 状态模型(DDL)

`agents` 现有 `status`(运行期 liveness)不动;新增**正交的**注册状态列:

```sql
ALTER TABLE yusui.agents
  ADD COLUMN enrollment TEXT NOT NULL DEFAULT 'approved'
    CHECK (enrollment IN ('pending','approved','rejected'));
-- 默认 approved:已存量(admin 手建)的 agent 保持可用,向后兼容。
-- 自动注册新建的行显式写 enrollment='pending'。
ALTER TABLE yusui.agents
  ADD COLUMN netbird_setup_key TEXT;  -- 审核通过时绑定,下发给 daemon 入网;敏感,响应里按需脱敏
```

> `status`(unknown/online/offline/degraded/frozen)= 跑起来后活没活;`enrollment`(pending/approved/rejected)= 准不准进来。两者正交。

## 11.4 协议增量(docs/03,字段只增)

```proto
// Register:去掉对预建 primary 的硬依赖;无 token 时进入 pending。
message RegisterResponse {
  // ... 现有字段 ...
  string enrollment = 6;     // "pending" / "approved"
  string netbird_setup_key = 7; // 仅 approved 时非空
}

// daemon 在 pending 期轮询(或复用 Control 流的下行)审核结果:
service AgentControl {
  rpc PollEnrollment(PollEnrollmentRequest) returns (RegisterResponse); // 新增
}
```

Server 侧管理 API(admin):`GET /api/v1/agents?enrollment=pending`、`POST /api/v1/agents/{id}/approve`、`POST /api/v1/agents/{id}/reject`(approve/reject 需 step-up,走 Policy Engine 审计)。

## 11.5 本地 daemon API 与 CLI

```proto
// 本地控制(UDS / 命名管道,仅本机)。operational-only,不含策略。
service AgentLocal {
  rpc Status(Empty) returns (StatusReply);   // enrollment / netbird / 活动转发器数 / server 连接
  rpc Up(Empty) returns (Empty);             // 触发入网(已 approved 时)
  rpc Down(Empty) returns (Empty);           // 退网
  rpc Logs(LogsRequest) returns (stream LogLine);
}
```

CLI 形态:`yusui-agent run`(daemon)/ `yusui-agent status|up|down|logs`(客户端,连本地 socket)。**GUI 延后**(选定:先只做 CLI;GUI 以后做本地 Web UI 或原生托盘再议)。

## 11.6 实现分期(每期可验证;不破坏现有 mock / e2e)

- **P1 注册+审核(服务端,纯 Go+DB,可单测/CI)**:迁移 `0002`(enrollment + setup_key 列);`Register` 改为未知 agent 自动建 pending 行(项目须存在)、不再硬要预建 primary;控制流/下发对 `enrollment!=approved` 拒绝;admin `approve/reject` API(走 Policy Engine 审计 + step-up)。
- **P2 setup key 下发**:审核通过时绑定 setup key(来源:admin 配置的 key,或经 NetBird Adapter 生成);`Register`/`PollEnrollment` 在 approved 后返回它。
- **P3 `overlay.Netbird`**:daemon 用 setup key 管理官方 netbird daemon 入网(需 NetBird 环境验证)。
- **P4 本地 API + CLI**:`AgentLocal` over UDS/命名管道 + `yusui-agent status|up|down|logs`。
- **P5 Admin UI**:pending agent 列表 + 审核按钮。
- **P6(延后)**:Windows GUI。

## 11.7 安全债(记 [10](10-todo.md))

- **auth-off**:控制面无密码学认证,靠 11.2 的人工审核当临时闸;生产必须加回注册 token + mTLS。
- **自动建行 + auth-off**:配合人工审核才安全;审核前 agent 无 setup key、无规则、无 overlay。
- `netbird_setup_key` 入库:敏感,列表/响应按需脱敏,生产应短期 key + 用后失效。

## 未决问题

- setup key 来源:admin 在项目上预配一个 key,还是 Server 经 NetBird Mgmt API 为每个 agent 生成一次性 key?倾向后者(少存敏感物、可吊销),但依赖 NetBird Adapter 落地。
- pending 期 daemon 用 `PollEnrollment` 轮询,还是先建 Control 流(未 approved 只允许心跳、不下发规则)?倾向后者(复用现有流,少一个 RPC)。
- 审核拒绝/吊销后:daemon 应退网 + 自毁本地状态;吊销已入网的 agent 如何联动 NetBird(删 peer)。
- 角色"项目已满"后第三个 agent:拒绝,还是允许 admin 改判角色?
