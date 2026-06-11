import { test, expect, Page } from "@playwright/test"
import { loginUI } from "./helpers"

// docs/11 §11.2: an auto-registered agent lands as `pending` and must be
// approved by an admin (step-up) before it can join the overlay or receive any
// ticket rules. The backend seeds two pending agents (scripts/e2e-stack.sh) —
// two so this stays green under Playwright's CI retry (each run approves one).
const visibleRows = (page: Page) => page.locator(".el-tab-pane:visible").getByRole("row")

test("admin approves a pending agent through the UI", async ({ page }) => {
  await loginUI(page)
  await page.goto("/admin")
  await page.getByRole("tab", { name: "Agent" }).click()

  // Pick the first still-pending agent (retry-safe: a prior run may have already
  // approved an earlier seed). Capture its hostname so we can re-find the row
  // after the table re-renders post-approval.
  const pendingRow = visibleRows(page).filter({ has: page.locator('[data-enrollment="pending"]') }).first()
  await expect(pendingRow).toBeVisible()
  const host = (await pendingRow.locator(".ys-mono").first().innerText()).trim()

  await pendingRow.locator('[data-act="approve-agent"]').click()

  // A fresh admin login already satisfies the step-up window, so no re-auth
  // prompt appears; the row flips to approved and loses its action buttons.
  const namedRow = visibleRows(page).filter({ hasText: host })
  await expect(namedRow.locator('[data-enrollment="approved"]')).toBeVisible()
  await expect(namedRow.locator('[data-act="approve-agent"]')).toHaveCount(0)
})
