---
layout: home

hero:
  name: "YuSui 语隧"
  text: "工单驱动的零信任运维访问"
  tagline: 运维只用浏览器,每一次对生产资产的访问都默认拒绝,凭一张获批的工单临时放行、到期自动回收,全程录像审计。
  actions:
    - theme: brand
      text: 接入一台 Agent
      link: /deploy/agent
    - theme: alt
      text: 部署主服务
      link: /deploy/server
    - theme: alt
      text: 它是什么
      link: /guide/what-is-yusui

features:
  - icon: 🎫
    title: 工单即权限
    details: 提工单 → 审批 → 临时放行 → 到期自动断开。每次访问都有明确的申请人、时间窗、范围与自动吊销;审批人 ≠ 申请人,硬约束无自审批。
  - icon: 🌐
    title: 浏览器即用,零客户端
    details: 运维不装任何网络客户端,浏览器经 HTTPS 连到主服务;只有主服务和各项目 Agent 是 NetBird Peer,资产藏在 Agent 后面的私有子网。
  - icon: 🧱
    title: 单层 ACL,事实在 Agent
    details: NetBird 只装一条常驻策略;每次工单的访问由 Agent 的"按工单用户态 L4 转发器"放行——固定目标 + 源白名单 + 到期即拆,这就是访问事实本身。
  - icon: 🤖
    title: 人 ± AI 协作终端
    details: 服务端持有 PTY,人和 AI 工具可同时 attach 到同一会话;每个字节标注来源(web/api/observer),危险命令按行拦截,AI 来源规则更严。
  - icon: 🛡️
    title: 危险命令过滤
    details: 行缓冲、可配置、分 block/confirm/warn 三级;全局 ∪ 项目 ∪ 资产 ∪ AI 规则取严。诚实声明:防误操作,不防有 shell 的恶意用户。
  - icon: 📼
    title: 全程审计与录像
    details: 一切皆审计,含系统触发动作(到期、对账、冻结、命令拦截、AI 输入);audit_logs 只追加,录像是 asciinema 文本流、每帧带来源。
---

## 为什么是 YuSui

YuSui 的价值是 **编排 + 业务闭环 + 对 AI 友好的 Web 终端**,不是网络层、也不是全协议代理。
它**不重造轮子**:NetBird(server↔agent overlay)、JumpServer(v0.2 可选)、Prometheus 都是上游依赖。

它要打通的关键路径只有一条,且必须端到端可用:

> **提工单 → 审批 → 浏览器开 Web SSH(人 ± AI attach + 危险命令拦截)→ 到期自动断开 → 录像与审计可查**

## 三类读者,从这里开始

- **想先跑起来看效果** → [主服务部署](/deploy/server)(本地 compose 一把起)。
- **要把一台机器接入** → [Agent 部署](/deploy/agent):目标是 **一条命令** 装好并接入。
- **想理解它怎么设计** → [核心概念与架构](/guide/architecture)。
