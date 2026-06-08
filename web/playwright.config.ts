import { defineConfig, devices } from "@playwright/test"

// e2e runs against an already-running stack (PG + sshd asset + server :8088 +
// web preview :5173), brought up by scripts/e2e-stack.sh locally or the `e2e`
// CI job. Playwright is purely the test runner here — no webServer block, so a
// flaky bring-up fails loudly in the stack step, not as an opaque test timeout.
const BASE_URL = process.env.E2E_BASE_URL || "http://127.0.0.1:5173"

export default defineConfig({
  testDir: "./e2e",
  fullyParallel: false, // specs share one seeded backend; keep them ordered
  workers: 1,
  forbidOnly: !!process.env.CI,
  retries: process.env.CI ? 1 : 0,
  timeout: 30_000,
  expect: { timeout: 10_000 },
  reporter: process.env.CI ? [["github"], ["list"]] : [["list"]],
  use: {
    baseURL: BASE_URL,
    trace: "on-first-retry",
    screenshot: "only-on-failure",
  },
  projects: [{ name: "chromium", use: { ...devices["Desktop Chrome"] } }],
})
