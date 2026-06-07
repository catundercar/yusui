import { defineConfig } from "vite"
import vue from "@vitejs/plugin-vue"

export default defineConfig({
  plugins: [vue()],
  server: {
    port: 5173,
    proxy: {
      // REST + WebSocket both proxy to the server; ws:true upgrades /api/v1/ws/*.
      "/api": { target: "http://localhost:8088", changeOrigin: true, ws: true },
    },
  },
})
