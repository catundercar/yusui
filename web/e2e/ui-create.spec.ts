import { test, expect, Page } from "@playwright/test"
import { loginUI } from "./helpers"

// Coverage for the CREATION paths the API-seeded critical-path deliberately
// skips: every Admin form + the ticket dialog, driven entirely through the UI.
// Names are distinct from critical-path's "alpha"/"sshd" so the two specs can
// share one (un-reset) backend without colliding on unique constraints.
const P = { code: "uitest", name: "UI Test Proj", cidr: "10.9.0.0/16" }
const HOST = { name: "ui-host", ip: "10.9.0.5", port: "22" }
const NEWUSER = { username: "uiuser", password: "UiUser12345!@" }

// Element Plus el-select renders no <label>; target the .el-select by the
// placeholder text it shows while closed, then pick the teleported option.
// el-select eagerly renders EVERY select's dropdown into <body>, so options from
// other selects (e.g. another "uitest") linger, and a closing popper's zoom
// transition can briefly overlap the next one's open. Serialize: wait until no
// dropdown is open, open this one, assert exactly one open popper, pick within it.
async function pickSelect(scope: ReturnType<Page["locator"]>, placeholder: string, optionName: string, page: Page) {
  const open = page.locator('.el-select__popper[aria-hidden="false"]')
  await expect(open).toHaveCount(0)
  await scope.locator(".el-select").filter({ hasText: placeholder }).click()
  await expect(open).toHaveCount(1)
  await open.locator(".el-select-dropdown__item", { hasText: optionName }).click()
}

test("create the entire catalog + a ticket through the UI (no API seeding)", async ({ page }) => {
  await loginUI(page)
  await page.goto("/admin")

  // --- project ---
  let pane = page.locator(".el-tab-pane:visible")
  await pane.getByPlaceholder("code").fill(P.code)
  await pane.getByPlaceholder("name").fill(P.name)
  await pane.getByPlaceholder("cidrs (逗号分隔)").fill(P.cidr)
  await pane.getByRole("button", { name: "添加项目" }).click()
  await expect(pane.getByRole("row").filter({ hasText: P.code })).toContainText(P.name)

  // --- agent (project picked via cascading select; role defaults to primary) ---
  await page.getByRole("tab", { name: "Agent" }).click()
  pane = page.locator(".el-tab-pane:visible")
  await pickSelect(pane, "项目", P.code, page)
  await pane.getByPlaceholder("hostname").fill("ui-agent")
  await pane.getByRole("button", { name: "添加 Agent" }).click()
  await expect(pane.getByRole("row").filter({ hasText: "ui-agent" })).toContainText(P.code)

  // --- asset ---
  await page.getByRole("tab", { name: "资产" }).click()
  pane = page.locator(".el-tab-pane:visible")
  await pickSelect(pane, "项目", P.code, page)
  await pane.getByPlaceholder("name").fill(HOST.name)
  await pane.getByPlaceholder("10.20.3.7").fill(HOST.ip)
  await pane.getByPlaceholder("22").fill(HOST.port)
  await pane.getByRole("button", { name: "添加资产" }).click()
  await expect(pane.getByRole("row").filter({ hasText: HOST.name })).toContainText(HOST.ip)

  // --- credential (no row by design: secrets never echo; assert success toast) ---
  await page.getByRole("tab", { name: "凭证" }).click()
  pane = page.locator(".el-tab-pane:visible")
  await pickSelect(pane, "资产", HOST.name, page)
  await pane.getByPlaceholder("ssh user").fill("ops-yusui")
  await pane.getByPlaceholder("secret / 私钥").fill("s3cretpw1234")
  // Let the earlier "已添加" toasts auto-dismiss so the credential's own result
  // toast is unambiguous (no table row to assert — secrets never echo).
  await expect(page.locator(".el-message")).toHaveCount(0)
  await pane.getByRole("button", { name: "保存凭证" }).click()
  await expect(page.locator(".el-message--success")).toBeVisible()
  await expect(page.locator(".el-message--error")).toHaveCount(0)

  // --- user ---
  await page.getByRole("tab", { name: "用户" }).click()
  pane = page.locator(".el-tab-pane:visible")
  await pane.getByPlaceholder("username").fill(NEWUSER.username)
  await pane.getByPlaceholder("密码 (≥12, 3 类字符)").fill(NEWUSER.password)
  await pane.getByRole("button", { name: "添加用户" }).click()
  await expect(pane.getByRole("row").filter({ hasText: NEWUSER.username })).toContainText("requester")

  // --- ticket via the 提单 dialog (project→asset cascade) ---
  await page.goto("/tickets")
  await page.getByRole("button", { name: "提工单" }).click()
  const dialog = page.getByRole("dialog", { name: "提交访问工单" })
  await pickSelect(dialog, "选择项目", P.code, page)
  await pickSelect(dialog, "选择资产", HOST.name, page) // multi-select stays open...
  await dialog.getByPlaceholder("22,3306").fill("22") // ...clicking the port field closes it
  await dialog.getByPlaceholder("访问原因").fill("ui created ticket")
  await dialog.getByRole("button", { name: "提交" }).click()
  await expect(page.getByRole("row").filter({ hasText: "ui created ticket" })).toContainText("pending")
})
