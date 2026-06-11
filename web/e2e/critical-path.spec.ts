import { test, expect, request as playwrightRequest, Page } from "@playwright/test"
import { ADMIN, TICKET_REASON, seedActiveTicket } from "./helpers"

// Read the visible terminal content from xterm's buffer (renderer-agnostic — the
// WebGL/canvas renderer leaves no text in the DOM, unlike the old DOM renderer).
// Terminal.vue exposes the instance on window.__yusuiTerm for tests.
function termText(page: Page): Promise<string> {
  return page.evaluate(() => {
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    const t = (window as any).__yusuiTerm
    if (!t) return ""
    const buf = t.buffer.active
    let s = ""
    for (let i = 0; i < buf.length; i++) s += (buf.getLine(i)?.translateToString(true) ?? "") + "\n"
    return s
  })
}
async function expectTerm(page: Page, text: string, timeout = 15_000) {
  await expect.poll(() => termText(page), { timeout }).toContain(text)
}

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
  await expect(row.locator('[data-status="active"]')).toBeVisible() // status label is i18n'd; assert the stable enum
  await row.getByRole("button", { name: "打开终端" }).click()
  await expect(page).toHaveURL(/\/tickets\/\d+\/terminal$/)

  await expectTerm(page, "已连接")
  await expectTerm(page, "$") // asset shell prompt is ready

  // Dangerous command: filtered on Enter, never reaches the shell.
  await page.getByRole("textbox", { name: "Terminal input" }).click()
  await page.keyboard.type("rm -rf /")
  await page.keyboard.press("Enter")
  await expectTerm(page, "已拦截", 10_000)

  // Normal command still executes (proves the shell is live, not killed).
  await page.keyboard.type("whoami")
  await page.keyboard.press("Enter")
  await expectTerm(page, "ops-yusui", 10_000)
})
