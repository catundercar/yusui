# 07 · 安全模型

## 7.1 信任边界

```
┌─────────────── 完全可信 ───────────────┐
│  Server 数据库 (Postgres)               │
│  Server 进程主体（API、Policy Engine）   │
│  Server CA 私钥 / KMS                  │
└─────────────────────────────────────────┘
                     ▲ 内部 IPC
                     │
┌─────────────── 关键代理（隔离）────────┐
│  Web Shell Service                     │
│  （独立进程/容器，CAP 最小，可重启）     │
│  持有：SSH 客户端凭证、PTY、录像流       │
└─────────────────────────────────────────┘
                     ▲ mTLS + session token
                     │
┌─────────────── 半信任 ─────────────────┐
│  YuSui Agent（项目运维负责加固）         │
└─────────────────────────────────────────┘
                     ▲ NetBird WireGuard
                     │
┌─────────────── 不可信 ─────────────────┐
│  浏览器（HTTP session）                  │
│  AI 工具（WebSocket attach）             │
│  网络链路（Internet）                    │
│  目标资产之间的横向移动                  │
└─────────────────────────────────────────┘
```

**关键判断**：
- **Agent 部分可信**：在客户私网里，可能被低权限误用，但不能让"Agent 被攻陷 = 整个 YuSui 被攻陷"。Agent 拿到的最高权限只是"控制本项目的 nftables + 上报到 Server"，**不能反过来攻陷 Server**。
- **Web Shell Service 是新的高价值目标**：持有所有活跃 PTY 与 SSH 客户端凭证；必须作为独立进程（甚至容器）部署，权限最小化，不可访问 CA 私钥 / KMS 密钥；崩溃可重启不影响主 Server。
- **运维终端（浏览器）+ AI 工具不可信**：通过会话 token 鉴权，能挂接 attach API 但无法越权读写 DB。
- **资产端凭证（v0.1 SSH key/password）的存储**：v0.1 由 Server 持有（独立加密的 credentials 表 + KMS envelope）；v0.2 后倾向移交 JumpServer 的账号集，YuSui 不再持有。

## 7.2 威胁模型（STRIDE）

| 威胁 | 场景 | 缓解 |
|---|---|---|
| **S**poofing | 假 Agent 接入 Server | mTLS + 一次性 register token（proto 字段名 setup_token）；CA 私钥保护 |
| | 假 Server 骗 Agent | Agent 内置 CA 指纹，Pin CA |
| | 用户冒充别人提工单 | 本地账号登录 + bcrypt + 可选 TOTP；session 校验；audit 记 IP/UA |
| | **AI 工具冒充人挂接** | AI 不复用 human JWT；human 在 UI 邀请后由 server 现签 per-attach capability token（`sub=ai-attacher`、`source=api`、锁定 session_id、`jti` 可吊销，见 [09 §9.4.2](09-web-shell.md)）；source 以 token 为准、客户端无法自报；`force_primary` 协议级仅 human 可用；admin/human 可一键作废 AI |
| **T**ampering | 改 audit | DB role 仅 INSERT；v0.3 链式哈希 |
| | 改 NetBird 常驻策略绕过 YuSui | NetBird UI 不开放给运维；Reconciler 周期扫描，差异即恢复并告警 `external_tamper` |
| | 改 Agent nftables | nftables 操作权限只给 Agent 进程；本地手动改通过对账被发现并复原 |
| | **改录像** | 录像本体写对象存储 SSE-KMS + 写后不可改；元数据落 audit；下载链接短期签名 |
| | **改命令规则集** | 规则集变更落 audit；内置规则不可关闭；admin 修改需 step-up 重认证 |
| **R**epudiation | "我没批" | 审批必须重新认证（step-up：密码 + TOTP）；审计存会话 ID 与 IP/UA |
| | **"那条命令不是我敲的"** | 每段 stdin 标 attacher 来源；录像含原始字节流；session_attachers 记录历史 |
| **I**nfo disclosure | DB 拖库 | 字段级敏感字段加密（reason / payload）；密钥在 Vault/KMS |
| | Agent 进程读取业务流量 | Agent 不解包；仅做 L3/L4 转发 |
| | **Web Shell 进程被劫持，看所有终端** | 独立进程 + 最小权限 + 严格审计；任何长连接异常增长触发告警；v0.3 用单独 namespace |
| | **观察者偷看敏感会话** | observer 必须 admin 角色；观察者列表实时显示在 attacher 状态条 |
| **D**oS | 工单暴刷 | UI/API 限流；同用户并发 ticket 上限 |
| | 登录密码爆破（v0.1 本地账号） | bcrypt cost=12 + 失败 5 次锁定 + 全局登录 RPS 限流 |
| | NetBird API 暴刷 | Adapter 内部令牌桶；启动期后调用量极小 |
| | Agent 上报暴刷 | gRPC 流量控制 + 单 Agent 心跳频率上限 |
| | **AI attacher 暴刷 stdin** | per-attacher RPS 限流；超阈值自动降级为 observer |
| | **大量 paste 流氓脚本** | paste 块大小阈值（默认 64KB）触发 confirm；超 1MB 直接 block |
| **E**oP | 普通用户拿 admin | 角色字段 DB CHECK 约束 + UI/API 双校验；定期回扫；v0.3 起 OIDC group claim 自动同步 |
| | Agent 提权到 root 后扩大攻击面 | Agent 跑 nft 用 capabilities (CAP_NET_ADMIN) 而非 root |
| | **AI 借工单期间执行高危操作** | source=api 叠加更严的命令规则集；危险命令强制 confirm 由 human 二次确认 |

## 7.3 密钥与证书管理

| 密钥 | 用途 | 存储 | 轮换 |
|---|---|---|---|
| YuSui CA 私钥 | 签 Agent 证书 | Vault / KMS（生产）；文件 + 文件权限 600（v0.1） | 5 年 |
| Agent 客户端证书 | Control stream mTLS | Agent 本地，文件 600 | 90 天，自动续 |
| NetBird Mgmt API Token | Server 调 NetBird | Vault / 环境变量 | 90 天 |
| Register Token（Agent 注册；proto 字段 setup_token） | Register 一次性，换 mTLS 证书 | 颁发后只显示一次，DB 存 hash | 1h |
| OIDC client_secret | OIDC（v0.3+） | Vault | 跟随 IdP |
| 用户密码 hash（v0.1） | 本地登录 | DB `users.password_hash`（bcrypt） | 用户自行 90 天提示 |
| 用户 TOTP secret（v0.1） | 二因素 | DB `users.mfa_secret_enc`（KMS envelope） | 重置时换 |
| Postgres 密码 | Server 连库 | Vault / sealed-secret | 90 天 |
| Audit 哈希链锚 | 防篡改 | 每小时写一份到只读 S3 | - |

**v0.1 简化**：所有密钥放在 docker-compose 的 env 文件，但文档明确告知"非生产用"。v0.3 上 Vault / SOPS。

## 7.4 网络隔离

- **Server**：浏览器入口 UI/WebSocket 走反代（Nginx + WAF），仅 443；gRPC 监听 NetBird Overlay 网段（Server 自己是 Peer）。**没有面向公网的 gRPC 或其他端口**。
- **Web Shell Service**：与主 Server 进程通过 Unix Domain Socket 或 localhost gRPC 通信；不暴露任何对外端口。
- **NetBird Mgmt**：内网访问 + IP 白名单；不接受公网 HTTP，HTTPS 仅供 Agent setup-key 拉取。
- **Agent**：仅 outbound 到 NetBird Mgmt / Signal / TURN + Overlay。inbound 仅来自 Overlay 的特定源（**server-peer IP**，v0.1 唯一合法源）。
- **资产**：仅监听项目私网段；OS 防火墙额外拒绝来自非 Agent IP 的流量。

## 7.5 鉴权与授权

**鉴权（认证）—— v0.1 本地账号方案**

- 仅本地账号；密码用 **bcrypt(cost=12)** 散列存储；不接受明文 / SHA / MD5。
- 首次登录强制改密；密码策略：≥12 位、字母数字符号至少三类（admin 可配）。
- **可选 TOTP**（RFC 6238）：用户在个人页启用后强制要求；admin 角色**默认强制**开启。
- 失败 5 次锁定 15 分钟（`failed_login_count` + `locked_until`），告警。
- 会话：服务端签 JWT（短 15min 访问 token + 长 7d 刷新 token），刷新可吊销。
- **关键操作 step-up**：审批、撤销、加 Agent、加管理员、轮证书时，要求 30 分钟内重新输入密码（若启用 MFA 则要 TOTP）。

**Identity Adapter 接口预留**

```go
type IdentityAdapter interface {
  Login(ctx, username, password, mfaCode string) (User, error)
  StepUp(ctx, user User, password, mfaCode string) error
  CreateUser(ctx, req CreateUserReq) (User, error)
  // v0.3 实现：OIDCProvider 也实现这个接口
}
```

v0.1 只有 `LocalProvider`；v0.3 增加 `OIDCProvider`，多 provider 并存，登录界面按邮箱后缀路由或让用户选。

**为什么 v0.1 不上 OIDC**：
- 部署门槛：小客户没有 Keycloak/Authentik，强依赖会拖慢 POC。
- 开发节奏：OIDC 调通需要 IdP 配合、callback 路由、token 交换、注销同步，至少占 1 周；MVP 资源花在核心闭环更值。
- 不锁死未来：Identity Adapter 接口提前抽象，v0.3 加 OIDC 不影响业务层。

**已知缺口（v0.1）**：
- 无企业 SSO，部署在已有 IdP 的客户处会被 IT 部门挑战 → v0.1 仅定位为 POC / 小团队，营销话术不能宣称"企业级身份打通"。
- 无设备指纹 / 风控；密码爆破靠速率限制 + 锁定兜底。

**授权（RBAC）**

| 角色 | 能做 |
|---|---|
| requester | 提工单、撤自己未批准的工单、查自己的工单 |
| approver | 审批别人的工单（非自己提交的）；**v0.1 作用域为全局**（任一 approver 可审批任一项目的工单）|
| admin | 项目/Agent/资产 CRUD、强制撤销、查全部审计、轮证书 |

授权策略集中在 `Authorize(actor, action, target)`（由 Policy Engine 暴露给 API handler），所有 API handler 必经过。

> **v0.1 已知缺口（draft7 诚实化）**：v0.1 没有 user↔project 成员关系表，`users.role` 是全局字段。因此 v0.1 实际语义是"任何 approver 角色的用户可以审批任意项目的工单"。文档与销售话术不能暗示"按项目分配审批人"。
>
> 这是阶段限制，不是设计缺陷——但对一个零信任产品，需要明确告知客户**v0.1 仅适合"信任所有审批人到全公司范围"的场景**（小团队 / POC）。需要项目级隔离的客户请等 v0.2。
>
> **v0.2 修复**：新增 `user_project_memberships` 表（user_id, project_id, role_in_project），approver 仅能审批所属项目工单。Authorize 引入 project-scope 校验。Migration + UI 工作量约 0.5-1 周，纳入 v0.2 路线图。
>
> v1.0 进一步引入资源级权限：approver 可限定到项目内的资产子集 / tag 子集。

## 7.6 审计完整性

**v0.1**：append-only（DB role 仅 INSERT）。
**v0.3**：链式哈希。

链式哈希设计：
```
hash[i] = SHA256(prev_hash || canonical_json(row[i]))
```
每小时把最新 `hash[n]` 写入只读对象存储（S3 Object Lock 或本地 WORM）。
取证时：从锚点验证整条链，能精确指出篡改位置（哪一行的 hash 不连续）。

**不做的事**：不上区块链。成本远大于收益。

## 7.7 数据脱敏

| 字段 | 处理 |
|---|---|
| `tickets.reason` | 默认明文；可选项目级开启加密（KMS envelope） |
| `audit_logs.payload` | 写入前过滤敏感 key（如 password、token） |
| 错误信息回显 | 用户看到的错误不含内部 path / SQL；详细错误进 audit |
| 日志（运行日志） | 结构化 + 字段白名单输出，token/cert 类字段全替换为 `<redacted>` |

## 7.8 关键组件被攻陷的最坏情况

### 7.8.1 Agent 被攻陷（root）

| 能做 | 缓解 |
|---|---|
| 看到所有经过 nftables 的流量元信息 | Agent 的设计能力；应用层敏感数据靠 TLS（如 MySQL TLS） |
| 任意改 nftables | 通过对账被 Server 发现并强制复原；同时告警 |
| 用 Agent 的证书冒充自己 | 仅能伪造心跳/事件；改不动 Server DB；管理员发现后立即吊销 |
| 通过 Agent 反向连 Server | gRPC stream 只允许 client→server 的 `AgentToCore` 消息；Server 不暴露其他端口 |
| 横向移动到资产 | Agent 主机不应有资产凭证 |

**关键设计选择**：Agent 不持有任何"主动连资产"的凭证；它只放行流量，不发起业务连接。

### 7.8.2 Web Shell Service 进程被劫持

> **诚实声明（v0.1-draft7 修正）**：本节描述的所有缓解措施**自 v0.2 起**生效（届时 Web Shell 拆为独立进程/容器，详见 [09 §9.10](09-web-shell.md)）。**v0.1 阶段 Web Shell 在主 Server 进程内**（同二进制 / 同地址空间），因此 v0.1 的"Web Shell 被劫持"等同于 [§7.8.3](#783-server-主进程被攻陷) 的最坏情形——本节的缓解列表对 v0.1 客户不成立。
>
> v0.1 客户必须在文档、UI、合规问卷中接受这条事实：**v0.1 不提供 Web Shell 对主 Server 的进程级隔离**。这是产品阶段限制，不是承诺缺失。

v0.2 起 Web Shell Service 独立部署后，本节假设场景。Web Shell 持有所有活跃 PTY、SSH 客户端，以及（v0.1/v0.2）资产凭证。

| 能做 | 缓解（v0.2+） |
|---|---|
| 看见所有活跃会话的明文 stdin/stdout | 实时录像本就如此；命中后告警依赖外部审计监控（异常 PTY 数量增长 / Recorder 流量突变） |
| 静默注入命令到任意 PTY | 每段 stdin 必须带 attacher_id；进程被改后该约束失效 → 防御靠"独立进程 + 启动期校验镜像签名 + 运行时 IMA"（v0.3） |
| 偷资产凭证 | v0.1/v0.2 凭证 KMS envelope 解密后驻 Web Shell 进程内存；v0.3 起搬到 JumpServer 账号集，Web Shell 不再持 |
| 用 SSH 隧道横向到资产 | 资产侧 sshd 强制公钥认证、disable password、记录 last login；admin 看到异常立即吊销 ticket |
| 销毁已写录像 | 录像写对象存储 SSE-KMS + S3 Object Lock；进程能停止录但不能删已写部分 |
| **选择性不录 / 写假字节**（draft7 新增坦白） | WORM **不防源头造假**——录像就是这个进程写的。缓解只能靠"边写边算 hash 推一份到独立审计存档"（v0.3 链式哈希）+ 多源验证（JumpServer v0.2 录像 + Web Shell 录像在交叉路径上可互证）。**对 v0.1 客户必须明示**：录像可信度等同 Web Shell 进程可信度。 |

**关键设计选择（v0.2+）**：Web Shell Service **独立进程/容器**，与主 Server 用 UDS/localhost 通信；不持 CA 私钥、不持 DB 写权（只能 INSERT 录像元数据与命令事件）；崩溃可重启不影响主 Server。v0.3 加 cgroup 隔离 + AppArmor profile + dm-verity 镜像。

**v0.1 落地建议**：
1. 主 Server 二进制冷启动后 root 释放，runtime 用非特权用户。
2. SSH 客户端连接资产时携带 sentinel（见 §7.12），把 session_id 与 ticket_id 烙到资产 syslog，形成"YuSui 之外的独立证据链"。
3. 路线图明确写 v0.2 拆进程作为前置条件，避免合规客户误判。

### 7.8.3 Server 主进程被攻陷

最坏情形。攻击者拿到主进程 = 拿到 DB、CA、所有授权。缓解：
- Server 主进程不暴露公网；仅在内部网络运行
- 容器化 + 镜像签名 + 启动期校验
- Postgres 使用最小权限 role（业务 INSERT/SELECT，admin role 仅迁移用）
- 审计独立写副本（v0.3 单向 logical replication 到只读机）
- 任何 admin 操作发飞书/钉钉强通知，让"批"和"看见"分离

## 7.9 合规对齐

针对**等保 2.0 三级**与常见监管要点：

| 要求 | YuSui 应对 |
|---|---|
| 身份鉴别（双因素） | v0.1：本地账号 + TOTP（admin 强制）；v0.3+：OIDC + IdP MFA |
| 访问控制（最小权限） | RBAC + 工单审批 + 时间窗 |
| 安全审计（不可篡改） | append-only + 链式哈希 |
| 入侵防范（最小服务） | 默认拒绝 + 临时放行 |
| 可信验证 | mTLS + 证书 pin |
| 日志保留 ≥6 个月 | 默认永久保留 + 冷归档 |

v0.3 提供"等保三级自查报告"导出，列出每条要求对应的 YuSui 配置项。

## 7.10 资产凭证（v0.1 SSH）

YuSui v0.1 需要为每台资产存一份 SSH 凭证（Server 主动 SSH 拨号到资产）。设计原则：

- **凭证模型**：每资产一份"操作账号"凭证（SSH key 优先；password 仅 fallback）。资产侧建议建独立的 `ops-yusui` 账号，不复用 root 或个人账号。
- **存储**：DB 表 `asset_credentials`（v0.1 新增），字段 `secret_enc BYTEA`，KMS envelope 加密；解密在 Web Shell Service 进程内完成，不落明文日志。
- **使用**：会话建立时 Web Shell 从 DB 读取 → 解密 → ssh.ClientConfig → 用完即弃。
- **审计**：凭证读取动作落 audit（"who decrypted credential X at when"）。
- **轮换**：v0.1 admin 手动；v0.3 集成 Vault dynamic credentials。
- **v0.2 起的演进路径**：JumpServer 集成后，YuSui 倾向不持有资产凭证，所有协议代理由 JumpServer 承担，YuSui 只做"工单 → JumpServer 临时授权"映射。

## 7.12 资产侧 session 哨兵（v0.1-draft7 新增）

为缓解 [§7.8.2](#782-web-shell-service-进程被劫持) 中"录像可信度 = Web Shell 可信度"以及 #8 中"操作者归属三重单点"的问题，YuSui Server 在每次建立 SSH 会话时，立即在资产上写入一条 sentinel 日志：

```bash
# 通过 SSH 在登录后第一条命令（Hidden in env or via SSH_ORIGINAL_COMMAND wrapper）
logger -t yusui -p auth.notice \
  "yusui-session sid=<session_pub_id> ticket=<ticket_pub_id> user=<requester_username> agent=<agent_id> proto=ssh"
```

效果：
- 资产侧 `/var/log/auth.log` / `journalctl` / SIEM 都能看到一条与 YuSui 工单可对照的标签。
- 资产侧的 last/auditd/sudo 日志里"那条命令是谁敲的"虽然显示成 `ops-yusui` 账号，但可通过时间窗回查 sentinel 找到工单与申请人。
- **诚实边界（draft9）**：sentinel 由 server 经 PTY 注入，只能在"会话期 server 可信"的前提下作为独立*存储*锚点——价值在于：若日后 YuSui 的 DB/录像被篡改，资产 OS 日志里仍留有原始 sid/ticket 可交叉比对。它**不能对抗"会话期 server 已被攻陷"**（被控 server 可不注入或注入伪造的 user/ticket），也**无法独立证明"哪个人"**（资产只见 `ops-yusui` + agent IP，人←→会话映射不可约地属于 YuSui）。真正的会话级独立背书见下方 v0.3 升级。

实现细节：
- sentinel 由 Server 在 SSH `session-open` 后立刻通过 PTY 注入（带 escape 防止误注入），登录脚本可见。
- 若资产 syslog 转发到 SIEM（rsyslog/journald-remote），sentinel 自动入网。
- 失败不阻断会话（fire-and-forget + 记一条 audit_warn）。
- 资产侧无需安装任何 YuSui 软件；`logger` 是标准 util-linux 自带。

**v0.3 升级到 sshd 级独立背书**：改用 YuSui CA 为每次会话签发短期 SSH 证书（或一次性密钥对），资产 sshd 认证时把 cert key-id / 指纹写进自身日志。这条记录由 sshd 产生、不经 server 的 PTY 流，因此"某会话确实发生过、用的哪张凭证"独立于 server 是否被攻陷。仍无法独立绑定"哪个人"（资产只认 `ops-yusui`），但已把"会话存在性 + 凭证身份"提升为 sshd 级证据，可与 YuSui 工单记录互证。

未决：Windows 资产（v0.3+）的等价方案——倾向使用 `eventcreate` 写到 Windows Event Log。

## 7.11 未决问题

- KMS 接入：v0.3 选 Vault 还是直接对接云厂商 KMS（阿里 KMS、AWS KMS）？倾向 Vault（中立）。
- Agent 主机基线加固：是否提供 CIS 自动化脚本？v0.2 评估。
- 红蓝对抗 / pentest 节奏：v0.3 GA 前必须做一轮外部 pentest。
- 审计字段加密的密钥粒度：全局 / 项目级 / 工单级？工单级最安全但密管复杂，倾向项目级。
- 资产凭证 v0.1 是否做"按工单 just-in-time 解密"？即只在 ticket=ACTIVE 时凭证才可解密。能进一步限制泄露窗口，但实现复杂。倾向先简单（启动期解密一次 + 进程内 zero-on-revoke）。
- Web Shell 进程的"独立"程度：v0.1 单二进制内独立 goroutine 即可？还是直接拆出独立进程？倾向 v0.1 先在同进程内做接口隔离，v0.2 真正拆进程。
