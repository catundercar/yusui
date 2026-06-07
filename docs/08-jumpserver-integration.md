# 08 · JumpServer 集成（v0.2 可选扩展）

> **本节定位变更**（v0.1-draft6）：YuSui v0.1 自研 Web SSH（[09](09-web-shell.md)）已经做了 SSH 协议的应用层审计、录像与命令拦截，**v0.2 接 JumpServer 不再是审计闭环唯一来源**。
>
> JumpServer 的作用收敛为：**为 SSH 以外协议（RDP / MySQL / PostgreSQL / Redis / K8s exec 等）提供 Web 客户端 + 录屏**，弥补 YuSui 自研代理在 SSH 单一协议下的能力空缺。
>
> 部署中 **JumpServer 是可选启用** 的，不启用 JumpServer 时 YuSui 仍能完整工作，但只能放行 SSH 协议工单。

## 8.1 为什么需要它

YuSui 自研 Web SSH 只支持 SSH 协议。但很多场景需要：
- RDP（Windows Server 桌面）
- MySQL/PostgreSQL/Redis 的图形化客户端（Web SQL Workbench）
- K8s exec / log 的 Web 终端
- VNC / SPICE 等

要么自研，要么复用。自研全协议代理工程量巨大且与 JumpServer 同质，所以选择复用：**用 JumpServer 补全 SSH 以外的协议接入**。

集成目标：**用户对 YuSui 提工单（access_kind=jumpserver）→ 自动在 JumpServer 获授权 → 用户从 YuSui Web 一键跳转 JumpServer Web 进入 → 录像与命令日志反链到 YuSui 工单页**。

> 即使审计入口分到两处（YuSui SSH 自研 / JumpServer 其它协议），YuSui 工单页统一聚合两边的会话与录像链接。用户视角只看 YuSui，不需要切换。

## 8.2 部署拓扑

JumpServer 部署在 YuSui Server 同侧，作为 NetBird 中又一个 Peer（与 Server 类似）经 Agent 访问资产：

```
┌─ Server 侧 ──────────────────────────────────────────────┐
│   YuSui Server │ Postgres │ JumpServer (Core+Koko)       │
│   (v0.3+ Keycloak)              │                         │
│                                 │  录像 → S3              │
│                                 └─ NetBird Peer            │
└──────────────────────────────────────────────────────────┘
                              │ NetBird Overlay
                ┌─────────────┼─────────────┐
                ▼             ▼             ▼
           Agent-α       Agent-β       Agent-γ
                │             │             │
            项目α资产      项目β资产      项目γ资产
```

JumpServer 在 NetBird 里也是一个 Peer，YuSui 内部用 `system:jumpserver` 标识。它访问任意项目资产时，**YuSui 在 Agent 上下"jumpserver-peer-ip → asset_ip:port"规则**——与 Web Shell 走同一条 Agent nftables 通道，无非来源 IP 不同。

### 用户流向

```
1. 用户在 YuSui Web 提工单 (asset=win-srv-1, protocol=rdp, access_kind=jumpserver, 2h)
2. YuSui 审批通过：
   ├─ Agent Controller → ApplyRule(rule_id=tk:42, 
   │      src=jumpserver-peer-ip, dst=10.20.3.7:3389)
   ├─ JumpServer Adapter → 给 user 创建 yusui:tk:42 临时授权
   └─ 通知用户：在 YuSui 工单页点"打开终端"将跳转 JumpServer Web
3. 用户在 YuSui 工单页点"打开终端"→ SSO 跳到 JumpServer Web
4. JumpServer 后端经 NetBird 隧道连接 10.20.3.7:3389
5. 操作录像，存对象存储
6. YuSui Audit 页面：工单详情聚合"会话与录像"列表，含 JumpServer 的录像链接
7. 到期：YuSui 撤销 Agent 规则 + JumpServer 授权（顺序见 §8.4.2）
```

JumpServer 实例数默认 1（HA 一对）。Per-project 模式作为大客户可选项，v0.3 提供 Helm chart。

### Web Shell 与 JumpServer 的协议分工

| 协议 | v0.1 谁负责 | v0.2 谁负责 |
|---|---|---|
| SSH | YuSui 自研 Web Shell | YuSui 自研 Web Shell（不退） |
| RDP | 不支持 | JumpServer |
| MySQL / PG / Redis | 不支持（运维只能用 SSH 连资产再连 DB CLI）| JumpServer Web 客户端 |
| K8s exec / log | 不支持 | JumpServer 或直接 kubectl over Web Shell |
| SFTP / 文件传输 | 不支持（v0.3 评估）| JumpServer |
| VNC / Telnet | 不支持 | JumpServer |

工单 `access_kind` 取值（v0.2 起）：
- `web_shell`（v0.1 仅有的）：协议必须是 SSH，走自研 Web Shell。
- `jumpserver`（v0.2 起）：其它协议或客户特定要求走 JS。

asset 表可加 `default_access_kind` 帮助 UI 推断默认值。

## 8.3 集成契约（API 视角）

### 8.3.1 JumpServer 我们用到的 API

JumpServer 提供 REST API（JWT / Token auth）。集成只用以下少量端点：

| 用途 | API | 频次 |
|---|---|---|
| 创建/同步用户 | `POST /api/v1/users/users/` | 每个新 YuSui 用户首次出现 |
| 创建/同步资产 | `POST /api/v1/assets/hosts/` | 每个 YuSui 资产首次出现 + 变更 |
| 创建临时授权 | `POST /api/v1/perms/asset-permissions/` | 每次工单批准 |
| 撤销授权 | `DELETE /api/v1/perms/asset-permissions/{id}/` | 工单到期/撤销 |
| 查询会话录像 | `GET /api/v1/terminal/sessions/?asset_id=...&user_id=...` | 审计页展示 |

不调用 JumpServer 的工单 / 审批 / 资产树管理 API——这些 YuSui 自己做。

### 8.3.2 JumpServer Adapter 接口

```go
package jumpserveradapter

type Adapter interface {
  // 身份
  EnsureUser(ctx, oidcSub, username, email string) (jsUserID string, err error)
  DeactivateUser(ctx, jsUserID string) error

  // 资产
  EnsureAsset(ctx, req EnsureAssetReq) (jsAssetID string, err error)
  RemoveAsset(ctx, jsAssetID string) error

  // 授权（工单驱动）
  GrantPermission(ctx, req GrantReq) (jsPermID string, err error)
  RevokePermission(ctx, jsPermID string) error

  // 审计
  ListSessions(ctx, q SessionQuery) ([]Session, error)
  GetReplayURL(ctx, sessionID string) (string, error)
}

type EnsureAssetReq struct {
  YuSuiAssetID int64
  Name         string
  IP           string
  Platform     string  // "linux" / "windows" / "mysql" / ...
  Protocols    []ProtoSpec  // [{name:"ssh", port:22}, {name:"mysql", port:3306}]
  // 资产凭证：YuSui 不持有，由 JumpServer 自己管（账号集）
}

type GrantReq struct {
  TicketID    int64
  YuSuiUserID int64
  JSUserID    string
  JSAssetIDs  []string
  Protocols   []string  // 子集：["ssh"] 或 ["mysql"]
  ExpiresAt   time.Time
  Reason      string
}
```

**幂等键**：与 NetBird Adapter 一致，所有 JumpServer 资源的 `name` 字段都打 `yusui:` 前缀，反查无歧义。例如：
- User: `yusui:<oidc_sub>`
- Asset: `yusui:asset:<yusui_asset_id>`
- Permission: `yusui:tk:<ticket_id>`

## 8.4 工单状态机扩展（仅 access_kind=jumpserver 时）

[05](05-policy-engine.md) 的状态机不变。`access_kind=jumpserver` 的工单在 Apply / Revoke 时多一步 JumpServer 调用。`access_kind=web_shell` 的工单保持单层不变。

### 8.4.1 Apply（access_kind=jumpserver）

```
T1: Agent Controller（放行 JS-peer-ip → asset-ip:port）
T2: JumpServer Adapter（给用户授权访问 asset）
T3: Transition ACTIVE，通知用户点"打开终端"跳 JS Web
```

**顺序"先严后松"**：T2 在 T1 后——网络层先通了再开 JS 入口，避免"JS 显示能连但实际网络不通"造成用户困惑。

T2 失败 → 回滚 T1，标 APPLY_FAILED。注意 v0.1-draft6 起取消了 NetBird 双写，所以这里只回滚 Agent。

### 8.4.2 Revoke（access_kind=jumpserver）

```
T1: SessionSvc.ForceCloseByTicket（如果是 web_shell 同时也存在会话）
T2: JumpServer Adapter（撤授权，正在连的 JS 会话立即断）
T3: Agent Controller（撤 Agent nftables）
```

**先撤 JS** —— 即使用户还连着，JumpServer 自己有"权限变更立即生效"的语义，会主动断会话。这比等 nftables 撤销更快终止应用层操作。

## 8.5 数据模型增量（v0.2 migration）

```sql
-- 新表：JumpServer 实例（支持未来多实例 / per-project）
CREATE TABLE jumpserver_instances (
  id            BIGSERIAL PRIMARY KEY,
  code          TEXT UNIQUE NOT NULL,    -- 'central' / 'project-alpha'
  base_url      TEXT NOT NULL,
  auth_token_enc BYTEA NOT NULL,         -- KMS 加密
  netbird_peer_id TEXT,                  -- JS 作为 NetBird Peer 的 ID
  scope         TEXT NOT NULL CHECK (scope IN ('global','project')),
  project_id    BIGINT REFERENCES projects(id),  -- scope=project 时必填
  is_active     BOOLEAN NOT NULL DEFAULT TRUE
);

-- 用户与资产的 JS 侧 ID
ALTER TABLE users ADD COLUMN js_user_id TEXT UNIQUE;
ALTER TABLE assets ADD COLUMN js_asset_id TEXT UNIQUE;
ALTER TABLE assets ADD COLUMN default_access_kind TEXT
  CHECK (default_access_kind IN ('web_shell','jumpserver'));   -- UI 默认推断

-- policy_bindings 扩展（仅 access_kind=jumpserver 时填）
ALTER TABLE policy_bindings ADD COLUMN js_instance_id BIGINT REFERENCES jumpserver_instances(id);
ALTER TABLE policy_bindings ADD COLUMN js_permission_id TEXT;
ALTER TABLE policy_bindings ADD COLUMN js_applied_at TIMESTAMPTZ;

-- tickets.access_kind 在 v0.1 已存在；v0.2 起 CHECK 集合解锁 'jumpserver'
-- 已在 [06 §6.2] 定义，无需重复 ALTER

-- 工单提交时校验：
--   若 protocol=ssh → access_kind 必须 = web_shell
--   若 protocol IN (rdp/mysql/...) → access_kind 必须 = jumpserver
--   （在应用层 PolicyEngine 的 ValidateTicketCreate 中实现）
```

## 8.6 资产 / 用户同步策略

**用户同步（v0.2，无 OIDC）**：
- 触发：YuSui 用户首次提交（或被列为审批人的）工单时，按需 `EnsureUser`。
- 不做全量预同步——避免 JumpServer 用户列表膨胀。
- **不假设 SSO**：YuSui 调 JumpServer API 创建账号时生成一次性密码（强口令），通过站内信/邮件发给用户，用户首登 JumpServer 时强制改密。YuSui 与 JumpServer 各自维护用户密码，存在"双密码"成本。
- 用户名规则：JumpServer 用户名 = YuSui 用户名（带 `yusui-` 前缀避免冲突）；email 一致便于后续对账。
- **v0.3 + OIDC 后**：两边共用同一个 IdP，做真正的 SSO，本节降级为旧逻辑兼容路径。

**资产同步**：
- 触发：YuSui 创建资产时立即 `EnsureAsset`。
- 资产凭证（root 密码 / SSH key）**YuSui 不持有**——由 JumpServer 的"账号集"管理，运维在 JumpServer 一次性录入。这是边界划得最清楚的一条。
- 资产删除：YuSui 软删 → JumpServer 标 disabled，30 天后真删（保留录像可追溯）。

## 8.7 审计聚合

YuSui 工单详情页：

```
工单 #42
├─ 申请人 / 审批人 / 时间 / 原因
├─ 涉及资产：mysql-prod-1 (10.20.3.7:3306)
├─ 网络层：
│   ├─ NetBird Policy yusui:tk:42  ✓
│   └─ Agent rule on alpha-agent  ✓
├─ 应用层（v0.2）：
│   ├─ JumpServer Permission yusui:tk:42  ✓
│   └─ 会话录像 (2 个)：
│       · 2026-06-05 10:23-10:31  [▶ 播放] [⬇ 下载]
│       · 2026-06-05 10:45-10:52  [▶ 播放] [⬇ 下载]
└─ 审计事件：12 条 [展开]
```

**录像存储**：JumpServer 默认本地，生产部署强制配 S3-compatible（MinIO / 阿里 OSS / 腾讯 COS）。YuSui 只存"播放链接"，录像本体不复制。

**保留期**：JumpServer 侧配 ≥180 天，符合常见合规要求。

## 8.8 降级与失败模式

| 场景 | YuSui 行为 |
|---|---|
| JumpServer 临时不可达（Apply 时） | 工单标 `apply_failed`，告警；若该资产同时支持 SSH（v0.1 默认），用户可改提 `access_kind=web_shell` 工单走自研 Web Shell |
| JumpServer 临时不可达（Revoke 时） | 状态保持 `revoking`，持续重试；NetBird/Agent 该撤照撤——网络层先断，应用层最终一致 |
| JumpServer 长期宕机 | YuSui Web 顶部红条提示"JumpServer 协议代理不可用"；新 ticket 默认拒绝 `access_kind=jumpserver`；`access_kind=web_shell`（SSH）不受影响 |
| 已有的活跃会话 JumpServer 重启 | JumpServer 自身负责会话恢复；YuSui 不介入 |
| YuSui 宕机 | JumpServer 已批的授权继续有效到原定过期时间；YuSui 恢复后通过对账校验 |

## 8.9 与 v0.1 的兼容性检查

回顾 v0.1 设计有没有给 v0.2 挖坑：

✓ `policy_bindings` 表行级粒度足够，加列即可。
✓ Policy Engine 状态机已是分阶段写入；v0.1 单层 Agent，v0.2 加 JumpServer 仅在 `access_kind=jumpserver` 时多一步。
✓ Agent 不感知 JumpServer——JumpServer 在 Agent 视角只是另一个允许的源 IP（NetBird Peer），下规则同样按 `src=js-peer-ip`。
✓ 安全模型 JumpServer 沿用本地账号（v0.1/v0.2）/ OIDC（v0.3+），不引入新信任边界。
✓ v0.1 自研 Web SSH 与 v0.2 JumpServer **协议互补不冲突**：SSH 永远走自研，其它永远走 JumpServer。审计页统一聚合。
✓ asset 表的 `default_access_kind` 字段提前设计好，v0.2 加列即生效。

## 8.10 工作量预估（v0.2 增量）

| 模块 | 工作量 |
|---|---|
| JumpServer Adapter（5 个接口） | 1 周 |
| JumpServer 接入 NetBird Peer + Agent 规则模板 | 0.5 周 |
| 两阶段 Apply/Revoke 改造（仅 access_kind=jumpserver 路径） | 0.5 周 |
| UI：工单按协议自动选 kind + 跳转 JS Web + 审计页聚合录像 | 1 周 |
| Migration + 用户/资产同步 | 0.5 周 |
| 集成测试（含 JumpServer docker） | 1 周 |
| 文档（部署、运维 runbook、协议分工） | 0.5 周 |
| **合计** | **5 周** |

与 [DESIGN.md v0.2 路线图（5-6 周）](../DESIGN.md#v02--协议扩展--审计深化5-6-周) 相符。

## 8.11 未决问题

- v0.2 双密码体验：是否在 YuSui Web 工单详情页内嵌"获取 JumpServer 一次性密码"按钮？还是只在通知里给一次？倾向只给一次（避免长期可见）。
- v0.3 引入 OIDC 后，存量的本地+JS双账号怎么平滑迁移到 SSO？需要一条 admin 工具批量绑定。
- JumpServer 自身的 OIDC 集成需要 enterprise 版还是开源版即可？开源版若无 OIDC，要走 LDAP 桥接。v0.2 不依赖此能力，所以暂不阻塞。
- JumpServer API 在不同版本（v3 vs v4）字段有差异，需要 pin 版本。
- 录像加密：JumpServer 自带 vs 依赖对象存储 SSE？倾向后者（统一钥匙圈）。
- Per-project 模式下 JumpServer 实例的注册流程：是不是也用一次性 token？v0.3 设计。
- 高峰期单 JumpServer 实例的并发会话数瓶颈，需要做压测，决定何时拆 per-project。
- v0.2 之前是否提供"过渡 stub"——直接在工单页贴用户的 SSH 命令模板（无审计）？倾向不做，避免依赖固化。
