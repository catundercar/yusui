import { defineConfig } from "vitepress"

// Project Pages site → served at https://catundercar.github.io/yusui/
export default defineConfig({
  lang: "zh-CN",
  title: "YuSui 语隧",
  description: "工单驱动的零信任运维访问平台 — 浏览器即用,人 ± AI 协作终端,全程审计录像。",
  base: "/yusui/",
  lastUpdated: true,
  cleanUrls: true,
  ignoreDeadLinks: true,
  head: [["meta", { name: "theme-color", content: "#6c5ce7" }]],
  themeConfig: {
    nav: [
      { text: "概览", link: "/guide/what-is-yusui" },
      { text: "部署", link: "/deploy/server" },
      {
        text: "接入 Agent",
        link: "/deploy/agent",
      },
      { text: "GitHub", link: "https://github.com/catundercar/yusui" },
    ],
    sidebar: {
      "/guide/": [
        {
          text: "概览",
          items: [
            { text: "YuSui 是什么", link: "/guide/what-is-yusui" },
            { text: "核心概念与架构", link: "/guide/architecture" },
          ],
        },
      ],
      "/deploy/": [
        {
          text: "部署",
          items: [
            { text: "总览", link: "/deploy/" },
            { text: "主服务部署", link: "/deploy/server" },
            { text: "Agent 部署(一条命令)", link: "/deploy/agent" },
          ],
        },
      ],
    },
    socialLinks: [{ icon: "github", link: "https://github.com/catundercar/yusui" }],
    outline: { level: [2, 3], label: "本页目录" },
    docFooter: { prev: "上一页", next: "下一页" },
    search: { provider: "local" },
    footer: {
      message: "YuSui · NetBird 之上的工单驱动零信任运维访问",
      copyright: "浏览器即用 · 人 ± AI 协作 · 全程审计录像",
    },
  },
})
