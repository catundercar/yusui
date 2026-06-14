# 部署总览

YuSui 一套环境由三部分组成:

```
┌─────────────────────────────────────────────────────────────┐
│ ① NetBird 控制面   management + signal + relay + IdP          │  自托管或云
├─────────────────────────────────────────────────────────────┤
│ ② YuSui 主服务     postgres + server(NetBird Peer)+ web SPA  │  一处
├─────────────────────────────────────────────────────────────┤
│ ③ 各项目 Agent     netbird daemon + yusui-agent              │  每项目 ≥1 台
└─────────────────────────────────────────────────────────────┘
```

按这个顺序部署:

1. **NetBird 控制面** —— overlay 的基础设施。自托管见仓库 [`deploy/netbird/`](https://github.com/catundercar/yusui/tree/main/deploy/netbird),或用 NetBird 云。它产出两样东西给后面用:**setup key**(给机器入网)和 **PAT**(给 Server 适配器)。
2. **[主服务部署 →](/deploy/server)** —— postgres + server + web。本地体验可用 mock 网关一把起;生产则让 server 成为一个 NetBird Peer 并开 gRPC 网关。
3. **[Agent 部署 →](/deploy/agent)** —— 目标是 **一条命令** 把目标机器装好并接入,然后在页面批准。

> 想先在本机看效果而不碰真 agent:见 [主服务部署 · 本地 mock 栈](/deploy/server#本地体验-mock-栈)。
> 想看真 overlay + 真 agent 的端到端 demo:仓库 [`deploy/demo/`](https://github.com/catundercar/yusui/tree/main/deploy/demo)。
