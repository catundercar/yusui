import { defineConfig } from "vite"
import vue from "@vitejs/plugin-vue"

// Same proxy for dev (`vite`) and preview (`vite preview`): REST + WebSocket
// forward to the server, so the BUILT bundle served by `preview` behaves like
// dev. e2e (Playwright) runs against `preview` to exercise the production build.
const proxy = {
  // ws:true upgrades /api/v1/ws/* (the Web Shell terminal stream).
  "/api": { target: "http://localhost:8088", changeOrigin: true, ws: true },
}

export default defineConfig({
  plugins: [vue()],
  server: { port: 5173, proxy },
  preview: { port: 5173, proxy },
})
