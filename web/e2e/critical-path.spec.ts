import { test, expect, request as playwrightRequest } from "@playwright/test"
import { ADMIN, TICKET_REASON, seedActiveTicket } from "./helpers"

// The MVP's critical path, solidified from the manual browser walkthrough:
// submit → approve → open Web SSH → dangerous-command filter → normal command.
// Catalog + active ticket are seeded over REST; the browser drives the rest.
test.beforeAll(async () => {
  const ctx = await playwrightRequest.newContext()
  await seedActiveTicket(ctx)
  await ctx.dispose()
})

test("submit→approve→Web SSH: rm -rf / is blocked, whoami runs", async ({ page }) => {
  // UI login as admin (admin may open any ticket's terminal).
  await page.goto("/login")
  await page.getByPlaceholder("密码").fill(ADMIN.password)
  await page.getByRole("button", { name: "登录" }).click()
  await expect(page).toHaveURL(/\/tickets$/)

  // Open the terminal for the seeded active ticket.
  const row = page.getByRole("row").filter({ hasText: TICKET_REASON })
  await expect(row).toContainText("active")
  await row.getByRole("button", { name: "打开终端" }).click()
  await expect(page).toHaveURL(/\/tickets\/\d+\/terminal$/)

  // xterm uses the DOM renderer here (only FitAddon is loaded), so terminal
  // text is real DOM under .xterm-rows.
  const screen = page.locator(".xterm-rows")
  await expect(screen).toContainText("已连接", { timeout: 15_000 })
  await expect(screen).toContainText("$", { timeout: 15_000 }) // asset shell prompt is ready

  // Dangerous command: filtered on Enter, never reaches the shell.
  await page.getByRole("textbox", { name: "Terminal input" }).click()
  await page.keyboard.type("rm -rf /")
  await page.keyboard.press("Enter")
  await expect(screen).toContainText("已拦截", { timeout: 10_000 })

  // Normal command still executes (proves the shell is live, not killed).
  await page.keyboard.type("whoami")
  await page.keyboard.press("Enter")
  await expect(screen).toContainText("ops-yusui", { timeout: 10_000 })
})
