# 09 · Web Shell（服务端 SSH 代理 + AI 协作 attach + 危险命令拦截）

## 9.1 这篇文档之于整个架构

这是 v0.1 的**唯一接入入口**。在采纳"服务端代理"决策（见 [feedback memory](#) / DESIGN.md v0.1-draft6）之后：

- 运维人员**不装**任何客户端，只用浏览器
- 所有终端会话由 YuSui 服务端**承接**（建立 SSH 客户端到资产）
- AI 工具（Claude Code / Codex / 自研 LLM agent）可通过 **WebSocket attach API** 接入同一会话，读输出 + 写命令
- 危险命令在服务端按规则**拦截 / 提示确认 / 警告**
- 全程录像（asciinema 文本流），输入字节按来源标签存证

不在本文档内的事：
- SSH 之外协议（MySQL / RDP / K8s exec）—— v0.2 借 JumpServer
- 文件传输（scp/sftp）—— v0.2，独立审计通道
- 端口转发（运维本地工具→资产）—— v0.3，独立 token 模式

## 9.2 组件与数据流

```
┌─ 浏览器（运维人员）────────────────────────────────────────┐
│  xterm.js                                                  │
│     │  WebSocket（/api/v1/sessions/{sid}/attach?role=primary)│
└─────┼──────────────────────────────────────────────────────┘
      ▼
┌─ YuSui Server ─────────────────────────────────────────────┐
│  Web Shell Service                                          │
│   ├─ Session Manager（生命周期、控制权、录像）              │
│   ├─ Attach Hub（多 attacher 广播 / 路由 stdin）            │
│   ├─ Command Filter（按行缓冲 + 规则引擎 + 终端模式探测）   │
│   ├─ Recorder（asciinema v2 + 字节来源标签）                │
│   └─ SSH Client Pool（golang.org/x/crypto/ssh）             │
│        │                                                    │
│        │ TCP 22 over NetBird Overlay                        │
└────────┼────────────────────────────────────────────────────┘
         ▼
   ┌─ Agent ──┐
   │ nftables │ ← Policy Engine 为本 ticket 下的临时放行规则
   └──────────┘
         │
         ▼
    资产 sshd
```

同一 SSH 会话上可同时挂多个 attacher：

```
                        ┌─ 浏览器(human, primary) ─ WebSocket ─┐
PTY (server 持有) ◀──── │                                       │ ─▶ Session Hub
   ▲                    ├─ AI tool (api, primary-handoff) ──────┤
   │  stdin/stdout      │                                       │
   ▼                    ├─ Admin (observer, 只读) ──────────────┤
   SSH Client           └─ Recorder (内部 attacher，写文件) ────┘
```

## 9.3 会话生命周期

```
┌─────────┐  ticket=ACTIVE     ┌──────────┐  primary attach   ┌─────────┐
│ NotInit │ ─────────────────▶ │ Allocated │ ───────────────▶ │ Running │
└─────────┘                    └──────────┘                    └────┬────┘
                                                                    │ all primary detach >2min
                                                                    │ OR ticket expires
                                                                    │ OR admin force-close
                                                                    ▼
                                                              ┌──────────┐
                                                              │  Closed  │
                                                              └──────────┘
```

**Allocated → Running** 的触发：第一个 primary attach 成功，server 建 SSH 客户端连接到资产，分配 PTY。

**Running 期间**：primary 可以来回切换（disconnect 重连），SSH 连接保持。无 primary 超 2 分钟则关闭。

**Closed 之后**：录像与元数据存档，PTY/SSH 释放。再次访问要新工单（同一 ticket 在有效期内可重开新会话）。

## 9.4 WebSocket attach 协议

### 9.4.1 endpoint

```
GET wss://yusui.example/api/v1/sessions/{session_id}/attach
  ?role=primary | observer
  &source=web | api | cli       # 自报来源标签
  Authorization: Bearer <session_token>
```

### 9.4.2 鉴权（draft7 修正：human / AI 用不同 token）

> draft6 误设计：AI 工具复用 human 的会话 JWT，仅靠 `source=api` 客户端自报区分。这意味着 server 实际**无法**强制分辨"人敲的 stdin"与"AI 发的 stdin"——两边携带同一 token、走同一 endpoint、source 字段客户端想填什么填什么。所谓 "AI 来源更严的命令规则"与"human 永远可夺权"都只是君子协定，token 一旦外泄等价于全站 human 权限。draft7 修法如下。

**Human 的 attach（无变化）**：
- 用登录后的 YuSui Web JWT。必须对该 ticket 拥有 `attach:primary` 或 `attach:observer` 权限。
- 工单的 **requester** 默认拥有 primary；admin 角色拥有 observer；其它人无权。

**AI 的 attach（draft7 新机制）—— per-attach capability token**：
- AI 不持有 human 的 JWT，也无法自报 source。
- human 在浏览器 UI 上显式点"邀请 AI 接入本会话"按钮 → server 现签一枚 **capability token**（短期 JWT），claim 至少包括：
  ```
  {
    "iss": "yusui",
    "sub": "ai-attacher",        // 固定值，与 human 区分
    "session_id": "<sid>",       // 锁定到当前会话
    "ticket_id": "<tid>",
    "source": "api",             // 服务端烤死
    "role": "primary"|"observer",// 由 human 选择
    "ai_label": "claude-code/0.5",
    "exp": now+2h,               // 不超过 ticket.expires_at
    "jti": "<uuid>",             // 可吊销
    "max_commands": 200,          // 可选硬上限
  }
  ```
- human UI 把 token 一次性显示（含一次性二维码 / 复制按钮），AI 工具粘贴使用。
- attach endpoint 校验 token：sub=ai-attacher → 严格走 AI 分支；source 字段**忽略 query string，以 token 为准**。
- token jti 入 `session_attachers` 表 `cap_jti`；human 在 UI 上随时可"撤销 AI"一键作废（server 标 jti 黑名单 → 下次心跳即断）。

**为什么这么改**：
1. server 真正能区分 human / AI——是密码学上的区分，不是字符串约定。
2. token 范围最小化：只能挂这一个 session，不能去别的，不能改身份，过期即废。
3. token 外泄不等于"全站权限"，只等于"该会话 AI 权限"且能立即吊销。
4. `force_primary` 不再是君子协定——human 的 JWT 在协议上就比 AI 的 capability token 优先级高（policy 校验时硬编码）。

**为什么 human 不用 capability token**：human 的 JWT 已经走 OAuth/本地账号鉴权流程，刷新机制完整；引入 capability token 反而增复杂度。AI 的特殊在"非人类用户挂接一次性资源"。

### 9.4.3 消息格式

帧采用 length-prefixed JSON（v0.1 简单）；v0.2 可切换 protobuf。

```jsonc
// ⬆ 上行（attacher → server）
{ "t": "stdin", "data": "ls -la\n", "src": "web" }
{ "t": "resize", "cols": 120, "rows": 40 }
{ "t": "signal", "sig": "SIGINT" }
{ "t": "request_primary" }    // observer 请求升级；server 通知当前 primary
{ "t": "release_primary" }    // 主动让权
{ "t": "ping" }

// ⬇ 下行（server → attacher）
{ "t": "stdout", "data": "drwx... \r\n" }
{ "t": "state", "phase": "running", "primary": "user:12", "attachers": 3 }
{ "t": "control_change", "from":"user:12", "to":"ai:claude-code" }
{ "t": "filter_block", "rule":"prevent-rm-rf-root", "msg":"...","line":"rm -rf /var/log/*" }
{ "t": "filter_confirm", "rule":"warn-dd", "line":"dd if=/dev/zero of=/dev/sda", "token":"cfm_xxx" }
{ "t": "error", "code":"primary_taken", "msg":"another attacher is primary" }
{ "t": "closed", "reason":"ticket_expired" }
{ "t": "pong" }
```

### 9.4.4 控制权（primary）模型

- 同一会话同时只有 1 个 primary，可写 stdin。
- 其他 attacher 一律 observer，只读 stdout。
- **转交**：当前 primary 发 `release_primary` 后，下一个 `request_primary` 成功；或显式 `transfer_to` 指定 attacher_id。
- **抢夺（draft7 强制）**：human attacher（token `sub != ai-attacher`）始终可以发 `force_primary`，server 立即把控制权切给 human，AI 端收到 `control_change` 通知。**AI 不能反过来 force_primary**（policy 引擎硬拒）——这是协议级保证，不是君子协定。
- **AI 一键作废**：human 在 UI 上点"踢出 AI" → server 把 AI 的 capability token jti 加入黑名单 + 立即关闭其 WebSocket。
- 控制权变更**异步、原子**：server 在切换瞬间清空尚未发送的 stdin 缓冲，避免错位。

### 9.4.5 终端原始模式（raw mode）感知

Server 持续跟踪 PTY 模式（通过观察 sshd 发来的 ECH/ICANON 等 termios 变化、以及 escape sequence 中的 `ESC[?1h`、`smcup` 等切换）。

- **cooked mode**（默认 shell）：命令过滤、按行缓冲、按 `\n` 切分。
- **raw mode**（vim / less / htop 等）：命令过滤**自动暂停**；提示 attacher 端 `state.filter=paused`。退出 raw mode 后自动恢复。

这条诚实写在文档里：raw mode 下的安全完全交给上游（资产侧 OS）。

## 9.5 录像（Recorder）

- 格式：**asciinema v2**（JSONL）。每帧含时间戳、流（stdin/stdout/stderr）、数据、**来源**（web/api/observer/system）。
- 体积：典型 1h 终端会话 ≈ 1-5MB。
- 落盘：v0.1 本地 `var/recordings/`；v0.2 起强制对象存储（S3/MinIO/OSS）。
- 加密：对象存储侧 SSE-KMS；元数据落库——录像本体 URI 存 `sessions.recording_uri`、挂接者列表存 `session_attachers`（不另立 `session_recordings` 表）。
- 回放：YuSui Web 工单详情页内嵌 asciinema-player；admin 可下载原始 JSONL 取证。

为何 asciinema 不录视频：体积小 10–100 倍；可全文搜索；对 AI 回顾友好（LLM 读 JSONL 直接还原会话）。

## 9.6 来源（source）与审计

每一段 stdin 都打三个标签存证：
- `attacher_id`：哪个 attacher（user:12 / api:claude-code/xxxx）
- `source`：web / api / observer-forbidden / system
- `via`：直接键入 / paste（来自 `bracketed paste` 标记）/ filter-confirmed

审计页可按来源过滤。例如查"该会话里 AI 一共敲了哪些命令"。

## 9.7 危险命令拦截（Command Filter）

### 9.7.1 立场（draft7 重写）

这是**防误删 / 防误炸**的安全网，**不是**对抗恶意用户的边界。

**为什么过滤点必须放远端，而不是 client stdin**：

PTY 交互式 SSH 是**逐字符字节流**，不是行流。客户端不能一次性把整行发出去——否则 tab 补全、`^R` 历史搜索、`^C`、vim 都没法工作。"将被执行的命令行"是**远端 shell 的 readline / line discipline 拼出来的**，client 看到的字节里不存在这个概念。

最常见的失效（非恶意）：
```
用户敲：rm -rf /tmp <^W> <^W> /<回车>
传输字节：rm -rf /tmp\x17\x17/\n
朴素行缓冲拼成：rm -rf /tmp^W^W/   → 正则不匹配
实际执行：rm -rf /                  → 灾难
```

`^U`（kill line）、`^W`（word erase）、↑/↓（历史调用）、`^R`（增量搜索）、tab 补全（远端往返）、复制粘贴、unicode 组合字符全都会让 client-side 行复原失真。这种失效**比 alias/encode/heredoc 更常见**——是日常 vim 用户的肌肉记忆。

**因此 draft7 起，Command Filter 的主防线移到远端**：

- **主防线（draft7 新增）**：解析远端 prompt → execute 周期，从 **stdout 流**里抓回显出来的命令行（也就是 shell 在按下回车时回显的内容），对这条"真实即将执行的命令"做规则匹配。这是 JumpServer / Teleport 走的路。
- **副防线**：stdin 侧只保留极保守的早期 block：
  - 单次 paste（bracketed paste，`ESC[200~ … ESC[201~`）大于阈值 → 整块匹配 + confirm/block
  - 字面匹配到"立即危险"的字面子串（`:(){:|:&};:`、`rm -rf --no-preserve-root /` 等无歧义模式）→ 直接 drop 整个 paste
  - 不再对单字符流做行复原匹配

仍然拦不住：alias / shell function / 编码 / heredoc / `:!rm` (vim) / `curl|bash` / 远端脚本里的命令。这些是远端解释器特性，YuSui 看到的只是回显出来的最外层命令行。文档与 UI 必须明示。

### 9.7.1.1 远端 prompt 周期解析详解

实现要点：

1. **学习 prompt**：会话建立后第一段 stdout 通常是 shell prompt（`$ ` / `# ` / 自定义 PS1）。server 用启发式学习一次：登录后第 2 秒、首次空闲态的最末一行作为 prompt 模板。失败回退到通用正则 `\n[^\n]*[\$#>]\s*$`。
2. **判定 execute 时刻**：当 stdin 收到 `\n` / `\r` 时**暂扣回车**，取本次 prompt 到回车之间、stdout 已回显的整行做匹配（回显已反映远端对行编辑/历史/补全的最终结果，故准确），匹配通过才放行回车。**这是 block 能成立的前提：过滤是回车前的同步闸控，不是看到下一个 prompt 后的事后检测**（拦截动作与 echo-off 失明边界见 §9.7.2）。
3. **alternate-screen 自动暂停**（vim/less）：观察到 `ESC[?1049h` → 暂停过滤；`ESC[?1049l` → 恢复并强制重新学 prompt。
4. **后台 job / pipeline 输出干扰**：用启发式只取"在 prompt 后、回车前"的文本段；如果 prompt 学习失败或环境异常，过滤进入 fail-safe（仅记录 `filter_degraded` 事件，不阻断）+ UI 提示用户切换为"严格 paste 守护模式"。
5. **诚实性能边界**：解析需要保存 ~16KB 滑动窗口；正则匹配并发性能 < 5ms / 命令。复杂 PS1 + 远端 prompt 异步刷新可能漏抓 1-2% 命令——监控 `filter_observation_loss_rate` 指标。

### 9.7.1.2 已知限制清单（v0.1 必须列在 UI 与文档）

| 类型 | 例 | 是否能拦 |
|---|---|---|
| 字面危险命令 | `rm -rf /` | ✅ |
| 行编辑/历史/补全失真 | `rm -rf /tmp ^W ^W /` | ✅（远端解析正确，client 解析会错；draft7 用前者） |
| 大 paste 危险脚本 | 一次粘 100 行 shell | ✅（早期 confirm/block） |
| alias / shell function | `alias rm='/bin/rm'`后`rm` | ❌ |
| base64/编码 | `$(echo cm0... \| base64 -d)` | ❌ |
| heredoc / `eval` | `eval "$(...)"` | ❌ |
| 编辑器内 `:!cmd` | vim 内 `:!rm -rf /` | ❌（alt-screen 暂停） |
| 远端脚本 `curl \| bash` | ✅（拦外层）但远端 fetch 的内容看不到 |
| 在 sudo 子 shell / docker exec 内 | ❌（嵌套 prompt 学习失败） |
| 回显关闭（echo-off）期输入 | sudo 口令、`stty -echo` | ❌（无回显可复原，放行 + 记 `filter_blind`） |

UI 必须在终端首屏放一行小字提示：**"命令过滤是防误删辅助，不防恶意；请同时遵守资产侧 sudo 与 auditd 控制"**。

### 9.7.2 拦截动作

每条规则可选三档：

| severity | 行为 |
|---|---|
| `warn`  | 通过，但 attacher 收到 `filter_warn`；审计记一条 |
| `confirm` | 暂停发送，server 下发 `filter_confirm` + token；attacher 必须显式回 `{t:"confirm_token","token":"cfm_xxx"}` 才放行；超时（默认 10s）丢弃 |
| `block` | 命中即拦：丢弃该命令行、向 PTY 注入 `^U`(kill-line) 清空已输入内容，且**不放行回车**；发 `filter_block`；审计记一条（机制见下"闸控时机"） |

**闸控时机（draft9 明确）**：过滤是**对回车键的同步闸控**，不是事后检测。server 收到 stdin 的 `\n`/`\r` 时暂扣该回车，用"本次提示符到回车之间、stdout 已回显的那一行"复原即将执行的命令（回显已反映远端对 `^W`/`^U`/↑/tab 的最终结果，故准确，见 §9.7.1.1），匹配后再决定：`warn` 立即放行 + 推 `filter_warn`；`confirm` 持续暂扣、推 `filter_confirm`+token，确认才放行、超时丢弃；`block` 丢弃整行 + 注入 `^U` 清行、回车不放行。闸控前等回显静默几毫秒（确保末尾按键回显都已到达）再快照，匹配 < 5ms，用户基本无感。

**回显关闭（echo-off）必然失明**：远端关闭回显（sudo/ssh 口令、`stty -echo`、TUI）时没有回显可复原，过滤对这段输入**放行且不匹配**——这本就该放行（多为口令，不应拦也不应记原文），server 记一条 `filter_blind` 事件备查。这是固有边界，已列入 §9.7.1.2 与 UI 提示。

### 9.7.3 规则格式

每条规则是一个 YAML 项（admin 在 YuSui Web 编辑，DB 存 JSON）：

```yaml
- id: prevent-rm-rf-absolute
  pattern: '^\s*(sudo\s+)?rm\s+(-[a-zA-Z]*[rRfF][a-zA-Z]*\s+)+(/(\s|$)|/(etc|var|usr|home|root|opt)(/|\s|$))'
  severity: block
  message: "禁止 rm -rf 系统关键路径。如确需，工单备注后联系 admin override。"

- id: confirm-dd-of-dev
  pattern: '\bdd\b[^|]*\bof=/dev/'
  severity: confirm
  message: "dd 写入块设备会破坏数据，确认继续？"

- id: block-forkbomb
  pattern: ':\s*\(\s*\)\s*\{[^}]*:\s*\|\s*:[^}]*\}\s*;\s*:'
  severity: block
  message: "Fork bomb 模式。"

- id: confirm-shutdown
  pattern: '\b(shutdown|reboot|poweroff|halt|init\s+0|init\s+6)\b'
  severity: confirm
  message: "停机命令需确认。"

- id: warn-curl-pipe-bash
  pattern: '\bcurl\b[^|]*\|\s*(sudo\s+)?(bash|sh|zsh)\b'
  severity: warn
  message: "下载即执行存在风险，已记录。"
```

匹配语义：**正则在按行缓冲的整行上匹配**（含前导空格、sudo 前缀）。多语句一行（用 `;`、`&&`、`||` 分隔）整体匹配。pipe 内子命令也一并参与（粗粒度，宁可多拦）。

### 9.7.4 规则作用域与优先级

一条 attach 会话的有效规则集合 = **全局默认 ∪ 项目级 ∪ 资产级**，按以下顺序解析：

```
1. system defaults  （YuSui 内置，不可关闭）
2. project policy   （project.command_policy_id → rules 集）
3. asset policy     （asset.command_policy_id 可 override 项目级）
4. source modifier  （source=api 时叠加更严格的 ai-rules 集）
```

冲突解决：同一规则 id 的 severity 取**最严**（block > confirm > warn）。规则禁止反向"放宽"——admin 不能用项目策略把内置 block 降级为 warn；要绕只能针对"具体资产 + 具体规则 id"做白名单。

### 9.7.5 内置规则集（v0.1 默认开启）

| id | 模式 | 默认 severity |
|---|---|---|
| prevent-rm-rf-absolute | 见上 | block |
| prevent-rm-rf-tilde-home | `rm -rf ~/` 系列 | confirm |
| confirm-dd-of-dev | dd 写块设备 | confirm |
| block-mkfs | `mkfs.*` | block |
| block-fdisk-parted | `fdisk|parted` 交互 | confirm |
| block-forkbomb | 见上 | block |
| confirm-shutdown | 见上 | confirm |
| confirm-systemctl-stop-critical | `systemctl (stop|disable) (sshd|networking|nftables|firewalld|chronyd)` | confirm |
| confirm-iptables-flush | `iptables -F` / `nft flush` | confirm |
| warn-curl-pipe-bash | 见上 | warn |
| warn-chmod-777-recursive | `chmod -R 777` | warn |

### 9.7.6 AI 来源叠加规则集（source=api）

AI attacher 默认更严格：

| id | 行为 |
|---|---|
| ai-confirm-any-rm | 任何 `rm` 都 confirm |
| ai-confirm-any-sudo | 任何 `sudo` 都 confirm |
| ai-block-no-tty-pkg-mgr | `apt|yum|dnf|pip|npm install` 阻断（防止 AI 偷偷装东西） |
| ai-block-network-config | `ip|ifconfig|route|nmcli` 写操作阻断 |

admin 可全局关闭"AI 严格集"或针对项目放宽。

### 9.7.7 raw mode / alternate screen 下的过滤

raw mode（vim / less / htop 等使用 alt-screen 的 TUI）时过滤暂停（详见 [§9.7.1.1](#9711-远端-prompt-周期解析详解)）。退出 alt-screen 后强制重学 prompt 再恢复匹配。
若误判（少数没切 alt-screen 但走 raw 模式的 TUI），attacher 会看到 `state.filter=paused` 提示。

### 9.7.8 配置 UI 与 API

- Web: admin 进 `策略中心 → 命令规则`，新建 policy 集（默认/严格/自定义），分配给项目或资产。
- API: `GET/POST/PUT /api/v1/command-policies`，rules 字段为 JSON。
- 变更**实时生效**：server 监听 policy 变化，活跃会话热加载；变更本身落审计。

### 9.7.9 与工单的关系

工单不直接控制命令规则。但 v0.2 可以引入"工单内命令白名单"——审批人在审批时填一组允许的命令 prefix，session 启动后 server 加一条临时 block-all-except 规则。这把"零信任"从网络层延伸到命令层，是 v0.2 的差异化卖点之一。

## 9.8 数据模型增量

```sql
-- 会话
CREATE TABLE sessions (
  id              BIGSERIAL PRIMARY KEY,
  pub_id          TEXT NOT NULL UNIQUE,
  ticket_id       BIGINT NOT NULL REFERENCES tickets(id),
  asset_id        BIGINT NOT NULL REFERENCES assets(id),
  agent_id        BIGINT NOT NULL REFERENCES agents(id),
  ssh_user        TEXT NOT NULL,
  status          TEXT NOT NULL CHECK (status IN ('allocated','running','closed')),
  opened_at       TIMESTAMPTZ,
  closed_at       TIMESTAMPTZ,
  closed_reason   TEXT,
  recording_uri   TEXT,
  command_policy_snapshot JSONB NOT NULL   -- 会话起始时的规则快照，便于回放
);
CREATE INDEX ON sessions(ticket_id);
CREATE INDEX ON sessions(asset_id);

-- 挂接者历史（多对多 with 时间窗）
CREATE TABLE session_attachers (
  id              BIGSERIAL PRIMARY KEY,
  session_id      BIGINT NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
  user_id         BIGINT REFERENCES users(id),   -- AI 工具可为 NULL，由 source+label 标识
  source          TEXT NOT NULL CHECK (source IN ('web','api','observer','system')),
  label           TEXT,                           -- AI 工具自报名，如 "claude-code/0.5"
  role            TEXT NOT NULL CHECK (role IN ('primary','observer')),
  attached_at     TIMESTAMPTZ NOT NULL,
  detached_at     TIMESTAMPTZ
);

-- 命令过滤事件（也写 audit_logs，但单独索引便于报表）
CREATE TABLE command_filter_events (
  id              BIGSERIAL PRIMARY KEY,
  session_id      BIGINT NOT NULL REFERENCES sessions(id),
  ts              TIMESTAMPTZ NOT NULL DEFAULT now(),
  rule_id         TEXT NOT NULL,
  severity        TEXT NOT NULL,
  action_taken    TEXT NOT NULL CHECK (action_taken IN ('warned','blocked','confirmed','confirm_timeout')),
  source          TEXT NOT NULL,
  attacher_label  TEXT,
  raw_line        TEXT NOT NULL                   -- 原始命令文本，敏感字段脱敏后落库
);
CREATE INDEX ON command_filter_events(session_id);
CREATE INDEX ON command_filter_events(rule_id);

-- 命令策略（admin 配置）
CREATE TABLE command_policies (
  id           BIGSERIAL PRIMARY KEY,
  code         TEXT NOT NULL UNIQUE,
  name         TEXT NOT NULL,
  is_builtin   BOOLEAN NOT NULL DEFAULT FALSE,
  rules        JSONB NOT NULL,                    -- [{id,pattern,severity,message,sources}]
  created_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);

ALTER TABLE projects ADD COLUMN command_policy_id BIGINT REFERENCES command_policies(id);
ALTER TABLE assets ADD COLUMN command_policy_id BIGINT REFERENCES command_policies(id);
```

## 9.9 安全注意点

| 风险 | 缓解 |
|---|---|
| 服务端被劫持 → 接管所有会话 | 服务端高强度加固；**v0.2 起** Web Shell 独立进程/容器、最小权限、CA 私钥不在该进程；**v0.1 为同进程，等同主进程被攻陷（见 [07 §7.8.2](07-security.md)）** |
| AI attacher 失控大量发命令 | per-attacher RPS 限流；危险命令拦截；human force-primary 一键夺权 |
| 录像泄露 | 对象存储 SSE-KMS；下载链接短期签名；admin 操作落审计 |
| 命令过滤被绕过（alias/encode） | 文档明示限制；危险时段加 v0.2 工单内白名单；OS 侧 auditd 作为最后一道 |
| paste 大块脚本绕过过滤 | 主防线为远端 prompt 周期解析（§9.7.1）；paste 侧检测 bracketed paste 时整块套用规则；超阈值（默认 64KB）默认 confirm |
| 会话被多人观摩泄露敏感数据 | 只有 admin 角色可加 observer；observer 列表实时显示在 attacher 状态条 |

## 9.10 性能与容量

| 指标 | v0.1 目标 |
|---|---|
| 单 server 并发活跃 session | 200 |
| 单 session 吞吐 | 1MB/s（足够 SSH 文本流；大文件传输不走这里） |
| attach WebSocket 端到端延迟 | < 80ms p99 |
| 录像写入开销 | < 3% CPU @200 session |
| 命令过滤额外延迟 | < 5ms |

资源不够时**优先扩容服务端而非单进程加压**，server 在 v0.3 设计为可水平扩展（session 亲和性路由）。

## 9.11 与其它模块的接口

- **Policy Engine**：会话 Allocated 时校验 ticket 仍 ACTIVE；ticket 过期/撤销时 Policy Engine 发 `force_close(session_id)`。
- **Agent Controller**：会话建立前确保对应 nftables 规则已下发到 Agent；这是会话能拨号到资产的前提。
- **Audit**：每个 attach/detach/filter 事件落 audit_logs（除 stdout 之外，stdout 走录像）。

## 9.12 未决问题

- AI attacher 是否需要单独 token？复用 human session 简单，但失去"人下线但 AI 继续跑"的能力。v0.2 评估"长任务 token"。
- raw mode 误判时用户体验：是否提供"强制启用过滤"按钮，让用户主动开/关？倾向有。
- 多 primary 并发输入是否完全禁止？v0.1 是。但有"结对运维 / 教学"场景，v0.3 可能加"协作模式"（双 primary 输入合并，每段附来源标记）。
- 工单内命令白名单（v0.2）的 UI 体验：审批人怎么知道该白名单哪些？候选：审批时显示"requester 申请了哪些命令"。
- 命令规则集的国际化：内置消息走 i18n 资源包还是 admin 自填？v0.1 admin 自填。
- 危险命令的"override 工单"流程：申请人想跑被拦的命令时，是不是该有一条"emergency override"工单走 admin 实时审批？v0.2 评估。
