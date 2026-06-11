import { test, expect, request as playwrightRequest, APIRequestContext } from "@playwright/test"
import { API, ADMIN, REQUESTER, APPROVER, login, loginUI } from "./helpers"

// Guards the security-critical approval path through the UI (docs/10): an
// approver approves a requester's ticket, and self-approval (invariant #8,
// approver ≠ requester) is rejected. Distinct project/reasons from the other
// specs so the shared backend doesn't collide. (Step-up's PASSWORD PROMPT is
// not covered — a fresh login already satisfies the window and it can't be
// expired within a fast e2e; the backend enforces it, web/api withStepUp wraps it.)
const PROJ = "apprtest"
const NORMAL = "e2e approve via UI"
const SELF = "e2e self-approve blocked"

async function seed(req: APIRequestContext) {
  const admin = await login(req, ADMIN)
  const h = (tok: string) => ({ Authorization: `Bearer ${tok}`, "content-type": "application/json" })
  const post = (path: string, tok: string, data: any) => req.post(`${API}${path}`, { headers: h(tok), data })
  const findId = async (path: string, pred: (x: any) => boolean) =>
    ((await (await req.get(`${API}${path}`, { headers: h(admin) })).json()) || []).find(pred).id

  await post("/api/v1/users", admin, { username: REQUESTER.username, role: "requester", password: REQUESTER.password })
  await post("/api/v1/users", admin, { username: APPROVER.username, role: "approver", password: APPROVER.password })

  const pr = await post("/api/v1/projects", admin, { code: PROJ, name: "Approval Test", cidrs: ["127.0.0.0/8"] })
  const projectId = pr.ok() ? (await pr.json()).id : await findId("/api/v1/projects", (p) => p.code === PROJ)
  // Apply needs an APPROVED primary agent (admin-created agents default approved).
  await post("/api/v1/agents", admin, { project_id: projectId, role: "primary", hostname: "appr-agent" })
  const as = await post("/api/v1/assets", admin, { project_id: projectId, name: "appr-sshd", ip_internal: "127.0.0.1", ports: [2222] })
  const assetId = as.ok() ? (await as.json()).id : await findId("/api/v1/assets", (a) => a.name === "appr-sshd")

  const ticket = (tok: string, reason: string) =>
    post("/api/v1/tickets", tok, { project_id: projectId, asset_ids: [assetId], ports: [2222], reason, duration_sec: 600 })
  await ticket(await login(req, REQUESTER), NORMAL) // pending, by a requester
  await ticket(await login(req, APPROVER), SELF) // pending, by the approver themselves
}

test.beforeAll(async () => {
  const ctx = await playwrightRequest.newContext()
  await seed(ctx)
  await ctx.dispose()
})

test("approver approves a requester's ticket; self-approval is blocked", async ({ page }) => {
  await loginUI(page, APPROVER)

  // Normal approval: a requester's pending ticket → active (mock gateway applies).
  const rowA = page.getByRole("row").filter({ hasText: NORMAL })
  await expect(rowA.locator('[data-status="pending"]')).toBeVisible()
  await rowA.getByRole("button", { name: "审批" }).click()
  await expect(rowA.locator('[data-status="active"]')).toBeVisible({ timeout: 10_000 })

  // Self-approval: the approver's OWN ticket is rejected (approver ≠ requester),
  // an error toast shows and the ticket stays pending.
  const rowB = page.getByRole("row").filter({ hasText: SELF })
  await expect(rowB.locator('[data-status="pending"]')).toBeVisible()
  await rowB.getByRole("button", { name: "审批" }).click()
  await expect(page.locator(".el-message--error")).toContainText("审批人不能与申请人相同")
  await expect(rowB.locator('[data-status="pending"]')).toBeVisible()
})
