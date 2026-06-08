import { test, expect } from "@playwright/test"
import { ADMIN } from "./helpers"

// Regression guard for the bug behind "还有很多报错": an expired access token
// used to 401 every request with no recovery, breaking every page at once and
// even trapping the user off /login. api.ts now refreshes once on 401 and
// retries; only if refresh fails does it drop to /login.
test("expired access token is silently refreshed instead of cascading to logout", async ({ page }) => {
  await page.goto("/login")
  await page.getByPlaceholder("密码").fill(ADMIN.password)
  await page.getByRole("button", { name: "登录" }).click()
  await expect(page).toHaveURL(/\/tickets$/)

  // Simulate access-token expiry: clobber the access token, keep the refresh
  // token. On reload the SPA re-inits session.token from this bad value.
  await page.evaluate(() => localStorage.setItem("yusui_token", "expired.invalid.token"))

  const refreshed = page.waitForResponse(
    (r) => r.url().includes("/api/v1/auth/refresh") && r.request().method() === "POST",
  )

  await page.reload() // Tickets.vue → listTickets → 401 → refresh → retry
  expect((await refreshed).ok()).toBeTruthy()

  // The decisive assertions: we stayed authenticated (refresh succeeded) and
  // did NOT cascade to /login.
  await expect(page).toHaveURL(/\/tickets$/)
  await expect(page.getByRole("heading", { name: "工单" })).toBeVisible()
})
