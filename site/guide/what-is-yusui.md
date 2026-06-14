# YuSui 是什么

YuSui(语隧)是一个**基于 NetBird 的、工单驱动的零信任运维访问平台**。

运维人员**只用浏览器**:主服务(YuSui Server)充当 SSH 代理(v0.1)与编排器。对生产资产的**每一次访问默认拒绝**;每次访问都由一张**获批的工单**临时授予——明确的申请人、时间窗、范围,以及到期自动吊销。

## 它做什么、不做什么

| YuSui 负责 | YuSui 不碰(交给上游) |
|---|---|
| 工单 / 审批 / 状态机 / 到期回收 | overlay 组网 → **NetBird** |
| Web SSH 终端(人 ± AI attach) | RDP/数据库等全协议代理 → **JumpServer**(v0.2 可选) |
| 危险命令过滤、录像、审计闭环 | 监控指标 → **Prometheus** |
| 凭据托管、单点写入 / 控制面 | — 不 fork、不重造 CMDB/ITSM/监控 |

一句话:**编排 + 业务闭环 + 对 AI 友好的 Web 终端**。

## 关键路径(MVP 必须端到端可用)

```
提工单 ──► 审批 ──► 浏览器开 Web SSH ──► 到期自动断开 ──► 录像 + 审计可查
              (人 ± AI attach + 危险命令拦截)
```

## 几条不可违背的架构不变量

1. **只有 Server 和各项目 Agent 是 NetBird Peer。** 终端用户不装 NetBird,浏览器经 HTTPS 连 Server;Server 用自己的 SSH 客户端(经 overlay)到资产。资产藏在某项目 Agent 后面的私有子网,**Agent 是该项目唯一的 Peer**。
2. **单层 ACL,事实在 Agent。** NetBird 只有一条常驻策略;每张工单的访问由 Agent 的**按工单用户态 L4 转发器**放行(绑 Agent overlay IP、只收 `src_peer_ips`、转发到一个 `asset_ip:port`、生命周期 = 工单到期)。它的存在 + 固定目标 + 源白名单**就是访问事实**。
3. **Server 是 SSH 代理。** Server 持有 PTY、跑 SSH 客户端到资产,广播给多个 WebSocket attacher(人 + AI);每个字节标来源。录像是 asciinema v2 文本流。
4. **危险命令过滤是行缓冲、可配置、从不号称万无一失。** 防误操作,不防有 shell 的恶意用户。
5. **审批人 ≠ 申请人(硬约束)。** 审批/撤销/管理动作需要 step-up 二次认证。
6. **一切皆审计,含系统触发动作。** `audit_logs` 只追加。
7. **失败降级,绝不失败放行。** Agent 失联超阈值进入 Frozen:拒新转发器,但已有转发器仍按到期自行关闭。

详见 [核心概念与架构](/guide/architecture)。
