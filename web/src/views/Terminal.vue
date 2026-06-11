<script setup lang="ts">
import { ref, onMounted, onBeforeUnmount } from "vue"
import { useRoute, useRouter } from "vue-router"
import { useI18n } from "vue-i18n"
import { ElMessageBox, ElNotification } from "element-plus"
import { Terminal } from "@xterm/xterm"
import { FitAddon } from "@xterm/addon-fit"
import { WebglAddon } from "@xterm/addon-webgl"
import "@xterm/xterm/css/xterm.css"
import { session } from "../auth"

const route = useRoute()
const router = useRouter()
const { t } = useI18n()
const termEl = ref<HTMLDivElement>()
const connState = ref<"connecting" | "connected" | "closed">("connecting")
let term: Terminal
let fit: FitAddon
let ws: WebSocket
let onResize: () => void

const b64encode = (s: string) => btoa(String.fromCharCode(...new TextEncoder().encode(s)))
const b64decode = (b: string) => Uint8Array.from(atob(b), (c) => c.charCodeAt(0))

onMounted(() => {
  term = new Terminal({
    cursorBlink: true,
    fontSize: 13,
    fontFamily: '"Geist Mono Variable", ui-monospace, Menlo, monospace',
    theme: { background: "#0e1016", foreground: "#d6dae2", cursor: "#8b85f5", selectionBackground: "#2b2f55" },
  })
  fit = new FitAddon()
  term.loadAddon(fit)
  term.open(termEl.value!)
  // GPU-accelerated rendering — the default DOM renderer janks on every
  // keystroke when the browser is busy (many tabs), which reads as terminal
  // lag even though the round-trip is a few ms. Fall back to DOM if WebGL is
  // unavailable or its context is lost.
  try {
    const webgl = new WebglAddon()
    webgl.onContextLoss(() => webgl.dispose())
    term.loadAddon(webgl)
  } catch {
    /* no WebGL — keep the DOM renderer */
  }
  fit.fit()
  // Test hook: e2e reads terminal content from the buffer (renderer-agnostic —
  // the WebGL/canvas renderer leaves no text in the DOM).
  ;(window as unknown as { __yusuiTerm?: Terminal }).__yusuiTerm = term

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
        connState.value = "connected"
        term.writeln(`\x1b[90m${t("terminal.connected", { id: m.session })}\x1b[0m`)
        break
      case "filter_block":
        term.writeln(`\r\n\x1b[31m${t("terminal.blocked")} ${m.msg || m.rule}: ${m.line}\x1b[0m`)
        ElNotification.error({ title: t("terminal.blockTitle"), message: m.line })
        break
      case "filter_warn":
        ElNotification.warning({ title: t("terminal.warnTitle"), message: m.line })
        break
      case "filter_confirm":
        ElMessageBox.confirm(`${m.msg || m.rule}\n\n${m.line}`, t("terminal.confirmTitle"), { type: "warning", confirmButtonText: t("terminal.confirmOk"), cancelButtonText: t("terminal.confirmCancel") })
          .then(() => ws.send(JSON.stringify({ t: "confirm_token", token: m.token })))
          .catch(() => { /* let it time out / clear */ })
        break
      case "closed":
        connState.value = "closed"
        term.writeln(`\r\n\x1b[33m${t("terminal.closed", { reason: m.reason })}\x1b[0m`)
        break
      case "error":
        term.writeln(`\r\n\x1b[31m${t("terminal.error")} ${m.msg}\x1b[0m`)
        break
    }
  }
  ws.onclose = () => {
    if (connState.value !== "closed") connState.value = "closed"
    term.writeln(`\r\n\x1b[90m${t("terminal.disconnected")}\x1b[0m`)
  }

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
  <div class="term">
    <div class="term-bar">
      <div class="term-id">
        <span class="term-dot" :class="connState" />
        <span>{{ t("terminal.header", { id: route.params.id }) }}</span>
      </div>
      <el-button size="small" @click="router.push('/tickets')">{{ t("terminal.back") }}</el-button>
    </div>
    <div ref="termEl" class="term-screen" />
  </div>
</template>

<style scoped>
.term {
  height: 100%;
  display: flex;
  flex-direction: column;
  background: var(--ys-term-bg);
}
.term-bar {
  flex: none;
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 8px 14px;
  background: var(--ys-term-bar);
  border-bottom: 1px solid rgba(255, 255, 255, 0.07);
  color: rgba(255, 255, 255, 0.82);
}
.term-id {
  display: flex;
  align-items: center;
  gap: 9px;
  font-size: 13px;
  font-family: var(--ys-font-mono);
}
.term-dot {
  width: 8px;
  height: 8px;
  border-radius: 999px;
  background: #d9a441;
  flex: none;
}
.term-dot.connected {
  background: #2ecc71;
  box-shadow: 0 0 0 0 rgba(46, 204, 113, 0.55);
  animation: term-pulse 2s var(--ys-ease) infinite;
}
.term-dot.closed {
  background: #e05656;
  animation: none;
}
@keyframes term-pulse {
  0% { box-shadow: 0 0 0 0 rgba(46, 204, 113, 0.5); }
  70% { box-shadow: 0 0 0 6px rgba(46, 204, 113, 0); }
  100% { box-shadow: 0 0 0 0 rgba(46, 204, 113, 0); }
}
@media (prefers-reduced-motion: reduce) {
  .term-dot.connected { animation: none; }
}
.term-screen {
  flex: 1;
  min-height: 0;
  padding: 8px 10px;
  overflow: hidden;
}
</style>
