<script setup lang="ts">
import { ref, onMounted, onBeforeUnmount } from "vue"
import { useRoute, useRouter } from "vue-router"
import { ElMessageBox, ElNotification } from "element-plus"
import { Terminal } from "@xterm/xterm"
import { FitAddon } from "@xterm/addon-fit"
import "@xterm/xterm/css/xterm.css"
import { session } from "../auth"

const route = useRoute()
const router = useRouter()
const termEl = ref<HTMLDivElement>()
let term: Terminal
let fit: FitAddon
let ws: WebSocket
let onResize: () => void

const b64encode = (s: string) => btoa(String.fromCharCode(...new TextEncoder().encode(s)))
const b64decode = (b: string) => Uint8Array.from(atob(b), (c) => c.charCodeAt(0))

onMounted(() => {
  term = new Terminal({ cursorBlink: true, fontSize: 13, fontFamily: "Menlo, monospace", theme: { background: "#1e1e1e" } })
  fit = new FitAddon()
  term.loadAddon(fit)
  term.open(termEl.value!)
  fit.fit()

  const id = route.params.id
  const proto = location.protocol === "https:" ? "wss" : "ws"
  ws = new WebSocket(`${proto}://${location.host}/api/v1/ws/tickets/${id}/terminal?access_token=${encodeURIComponent(session.token)}`)

  ws.onmessage = (ev) => {
    const m = JSON.parse(ev.data)
    switch (m.t) {
      case "stdout":
        term.write(b64decode(m.data))
        break
      case "state":
        term.writeln(`\x1b[90m[已连接 session ${m.session}]\x1b[0m`)
        break
      case "filter_block":
        term.writeln(`\r\n\x1b[31m[已拦截] ${m.msg || m.rule}: ${m.line}\x1b[0m`)
        ElNotification.error({ title: "危险命令已拦截", message: m.line })
        break
      case "filter_warn":
        ElNotification.warning({ title: "警告", message: m.line })
        break
      case "filter_confirm":
        ElMessageBox.confirm(`${m.msg || m.rule}\n\n${m.line}`, "确认执行此命令?", { type: "warning", confirmButtonText: "确认执行", cancelButtonText: "取消" })
          .then(() => ws.send(JSON.stringify({ t: "confirm_token", token: m.token })))
          .catch(() => { /* let it time out / clear */ })
        break
      case "closed":
        term.writeln(`\r\n\x1b[33m[会话已关闭: ${m.reason}]\x1b[0m`)
        break
      case "error":
        term.writeln(`\r\n\x1b[31m[错误] ${m.msg}\x1b[0m`)
        break
    }
  }
  ws.onclose = () => term.writeln("\r\n\x1b[90m[连接断开]\x1b[0m")

  term.onData((d) => {
    if (ws.readyState === WebSocket.OPEN) ws.send(JSON.stringify({ t: "stdin", data: b64encode(d) }))
  })
  onResize = () => {
    fit.fit()
    if (ws.readyState === WebSocket.OPEN) ws.send(JSON.stringify({ t: "resize", cols: term.cols, rows: term.rows }))
  }
  window.addEventListener("resize", onResize)
  setTimeout(onResize, 100)
})

onBeforeUnmount(() => {
  if (onResize) window.removeEventListener("resize", onResize)
  try { ws && ws.close() } catch { /* noop */ }
  if (term) term.dispose()
})
</script>

<template>
  <div style="height: 100%; display: flex; flex-direction: column; background: #1e1e1e">
    <div style="padding: 6px 12px; color: #ccc; background: #2d2d2d; display: flex; justify-content: space-between; align-items: center">
      <span>工单 #{{ route.params.id }} · Web SSH（命令过滤生效中）</span>
      <el-button size="small" @click="router.push('/tickets')">返回工单</el-button>
    </div>
    <div ref="termEl" style="flex: 1; padding: 6px; overflow: hidden"></div>
  </div>
</template>
