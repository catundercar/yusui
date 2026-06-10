# YuSui 设计文档索引

> 总览见 [../DESIGN.md](../DESIGN.md)。本目录是分模块的详细设计。

| # | 文档 | 主要回答 |
|---|---|---|
| 01 | [架构](01-architecture.md) | 系统由哪些组件组成？流量怎么走？部署什么样？ |
| 02 | [Agent 设计](02-agent-design.md) | Agent 内部长什么样？规则怎么执行？HA 怎么做？ |
| 03 | [Agent ↔ Server 协议](03-agent-protocol.md) | 两边怎么对话？消息格式？错误语义？ |
| 04 | [NetBird Adapter](04-netbird-adapter.md) | 怎么调 NetBird？怎么映射概念？版本怎么兼容？ |
| 05 | [Policy Engine](05-policy-engine.md) | 工单状态机？Agent 单层 Apply/Revoke？怎么对账？ |
| 06 | [数据模型](06-data-model.md) | 完整 schema、索引、迁移、约束 |
| 07 | [安全模型](07-security.md) | 信任边界？威胁清单？密钥怎么管？审计怎么保证不可篡改？ |
| 08 | [JumpServer 集成](08-jumpserver-integration.md) | v0.2 怎么接？部署拓扑？工单 → 授权映射？录像聚合？降级？ |
| 09 | [Web Shell](09-web-shell.md) | v0.1 默认入口：服务端 SSH 代理、AI attach、危险命令拦截 |
| 10 | [TODO / 未实现项](10-todo.md) | 还有什么没做？已设计/规划但未落地(或简化)的实现欠账单一事实表 |

## 阅读顺序建议

- **第一次读 / 新 contributor**：DESIGN.md → 01 → 02 → 05
- **要写 Agent 代码**：02 → 03 → 07
- **要写 Server 代码**：05 → 09 → 04 → 06 → 03
- **要做安全评审**：07 → 01 → 03

## 文档写作约定

- 用决策记录的口吻：**做什么 + 为什么这样 + 不这样会怎样**。
- 不写"教程"，不抄上游文档；只写 YuSui 特有的部分。
- 接口契约用 Protobuf / SQL DDL 表达，不用散文描述。
- 每篇末尾给"未决问题"列表。
