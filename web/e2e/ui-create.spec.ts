import { test, expect, Page, Locator } from "@playwright/test"
import { loginUI } from "./helpers"

// Coverage for the CREATION paths the API-seeded critical-path deliberately
// skips: every Admin create dialog + the ticket dialog, driven entirely through
// the UI. Names are distinct from critical-path's "alpha"/"sshd" so the two
// specs can share one (un-reset) backend without colliding on unique constraints.
const P = { code: "uitest", name: "UI Test Proj", cidr: "10.9.0.0/16" }
const HOST = { name: "ui-host", ip: "10.9.0.5", port: "22" }
const NEWUSER = { username: "uiuser", password: "UiUser12345!@" }

// Element Plus el-select renders no <label>; target the .el-select by the
// placeholder text it shows while closed, then pick the teleported option.
// el-select eagerly renders dropdowns into <body> and the open/close zoom can
// briefly overlap, so serialize: wait until no dropdown is open, open this one,
// assert exactly one open popper, then pick the option inside the open popper.
async function pickSelect(scope: Locator, placeholder: string, optionName: string, page: Page) {
  const open = page.locator('.el-select__popper[aria-hidden="false"]')
  await expect(open).toHaveCount(0)
  await scope.locator(".el-select").filter({ hasText: placeholder }).click()
  await expect(open).toHaveCount(1)
  await open.locator(".el-select-dropdown__item", { hasText: optionName }).click()
}

// Creation is now a per-tab "新建" button opening one labeled dialog. Switch
// tab, open the dialog, return it scoped by title.
async function openCreate(page: Page, tabName: string, title: string): Promise<Locator> {
  await page.getByRole("tab", { name: tabName }).click()
  await page.locator(".el-tab-pane:visible").getByRole("button", { name: "新建" }).click()
  const d = page.getByRole("dialog", { name: title })
  await expect(d).toBeVisible()
  return d
}

const visibleRows = (page: Page) => page.locator(".el-tab-pane:visible").getByRole("row")

test("create the entire catalog + a ticket through the UI (no API seeding)", async ({ page }) => {
  await loginUI(page)
  await page.goto("/admin")

  // --- project ---
  let d = await openCreate(page, "项目", "添加项目")
  await d.getByPlaceholder("code").fill(P.code)
  await d.getByPlaceholder("name").fill(P.name)
  await d.getByPlaceholder("cidrs (逗号分隔)").fill(P.cidr)
  await d.getByRole("button", { name: "提交" }).click()
  await expect(visibleRows(page).filter({ hasText: P.code })).toContainText(P.name)

  // --- agent (project via cascading select; role defaults to primary) ---
  d = await openCreate(page, "Agent", "添加 Agent")
  await pickSelect(d, "项目", P.code, page)
  await d.getByPlaceholder("hostname").fill("ui-agent")
  await d.getByRole("button", { name: "提交" }).click()
  await expect(visibleRows(page).filter({ hasText: "ui-agent" })).toContainText(P.code)

  // --- asset ---
  d = await openCreate(page, "资产", "添加资产")
  await pickSelect(d, "项目", P.code, page)
  await d.getByPlaceholder("name").fill(HOST.name)
  await d.getByPlaceholder("10.20.3.7").fill(HOST.ip)
  await d.getByPlaceholder("22").fill(HOST.port)
  await d.getByRole("button", { name: "提交" }).click()
  await expect(visibleRows(page).filter({ hasText: HOST.name })).toContainText(HOST.ip)

  // --- credential (no row by design: secrets never echo). The dialog closes
  // only on success, so an invisible dialog + no error toast proves it landed. ---
  d = await openCreate(page, "凭证", "保存凭证")
  await pickSelect(d, "资产", HOST.name, page)
  await d.getByPlaceholder("ssh user").fill("ops-yusui")
  await d.getByPlaceholder("secret / 私钥").fill("s3cretpw1234")
  await d.getByRole("button", { name: "提交" }).click()
  await expect(d).toBeHidden()
  await expect(page.locator(".el-message--error")).toHaveCount(0)

  // --- user ---
  d = await openCreate(page, "用户", "添加用户")
  await d.getByPlaceholder("username").fill(NEWUSER.username)
  await d.getByPlaceholder("密码 (≥12, 3 类字符)").fill(NEWUSER.password)
  await d.getByRole("button", { name: "提交" }).click()
  // role label is i18n'd; assert the stable enum via data-role
  await expect(visibleRows(page).filter({ hasText: NEWUSER.username }).locator('[data-role="requester"]')).toBeVisible()

  // --- ticket via the 提单 dialog (project→asset cascade, unchanged) ---
  await page.goto("/tickets")
  await page.getByRole("button", { name: "提工单" }).click()
  const dialog = page.getByRole("dialog", { name: "提交访问工单" })
  await pickSelect(dialog, "选择项目", P.code, page)
  await pickSelect(dialog, "选择资产", HOST.name, page) // multi-select stays open...
  await dialog.getByPlaceholder("22,3306").fill("22") // ...clicking the port field closes it
  await dialog.getByPlaceholder("访问原因").fill("ui created ticket")
  await dialog.getByRole("button", { name: "提交" }).click()
  await expect(page.getByRole("row").filter({ hasText: "ui created ticket" }).locator('[data-status="pending"]')).toBeVisible()
})
