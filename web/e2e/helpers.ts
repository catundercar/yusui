import { APIRequestContext, Page, expect } from "@playwright/test"

// e2e seeds the backend directly over REST (fast + deterministic), then drives
// the UI in the browser. API base hits the server directly (not via the web
// proxy) so setup never depends on the SPA being up.
export const API = process.env.E2E_API_URL || "http://localhost:8088"

export const ADMIN = { username: "admin", password: "Admin12345!@" }
export const REQUESTER = { username: "req1", password: "Req12345!@xy" }
export const APPROVER = { username: "appr1", password: "Appr12345!@xy" }

// A stable marker so a spec can locate "its" ticket row in a shared backend.
export const TICKET_REASON = "e2e critical path"

// loginUI signs in through the actual login form (username defaults to admin in
// the UI, but we set it explicitly so the helper works for any role).
export async function loginUI(page: Page, u = ADMIN): Promise<void> {
  await page.goto("/login")
  await page.getByPlaceholder("用户名").fill(u.username)
  await page.getByPlaceholder("密码").fill(u.password)
  await page.getByRole("button", { name: "登录" }).click()
  await page.waitForURL(/\/tickets$/)
}

export async function login(request: APIRequestContext, u: { username: string; password: string }): Promise<string> {
  const res = await request.post(`${API}/api/v1/auth/login`, { data: u })
  expect(res.ok(), `login ${u.username}: ${res.status()}`).toBeTruthy()
  return (await res.json()).access_token as string
}

// ok treats 409 (already exists) as success so re-running specs against a
// non-reset backend stays idempotent (mirrors the Ensure* idempotency rule).
async function ok(p: Promise<import("@playwright/test").APIResponse>, what: string) {
  const res = await p
  if (res.status() === 409) return null
  expect(res.ok(), `${what}: ${res.status()} ${await res.text()}`).toBeTruthy()
  return res.json()
}

function auth(token: string) {
  return { Authorization: `Bearer ${token}`, "content-type": "application/json" }
}

// seedActiveTicket builds the full catalog and returns an APPROVED (active)
// ticket the browser can open a terminal on: project → primary agent → sshd
// asset (127.0.0.1:2222) → credential → req1/appr1 users → submit → approve.
export async function seedActiveTicket(request: APIRequestContext): Promise<{ ticketId: number }> {
  const admin = await login(request, ADMIN)
  const h = auth(admin)

  await ok(request.post(`${API}/api/v1/users`, { headers: h, data: { username: REQUESTER.username, role: "requester", password: REQUESTER.password } }), "create requester")
  await ok(request.post(`${API}/api/v1/users`, { headers: h, data: { username: APPROVER.username, role: "approver", password: APPROVER.password } }), "create approver")

  let projectId: number
  const proj = await ok(request.post(`${API}/api/v1/projects`, { headers: h, data: { code: "alpha", name: "Alpha Prod", cidrs: ["127.0.0.0/8"] } }), "create project")
  if (proj) {
    projectId = proj.id
  } else {
    const list = await (await request.get(`${API}/api/v1/projects`, { headers: h })).json()
    projectId = (list || []).find((p: any) => p.code === "alpha").id
  }

  await ok(request.post(`${API}/api/v1/agents`, { headers: h, data: { project_id: projectId, role: "primary", hostname: "alpha-agent" } }), "create agent")

  let assetId: number
  const asset = await ok(request.post(`${API}/api/v1/assets`, { headers: h, data: { project_id: projectId, name: "sshd", ip_internal: "127.0.0.1", ports: [2222] } }), "create asset")
  if (asset) {
    assetId = asset.id
  } else {
    const list = await (await request.get(`${API}/api/v1/assets`, { headers: h })).json()
    assetId = (list || []).find((a: any) => a.name === "sshd").id
  }
  await ok(request.post(`${API}/api/v1/assets/${assetId}/credentials`, { headers: h, data: { ssh_user: "ops-yusui", auth_kind: "password", secret: "hunter2" } }), "create credential")

  const req = await login(request, REQUESTER)
  const ticket = await (await request.post(`${API}/api/v1/tickets`, {
    headers: auth(req),
    data: { project_id: projectId, asset_ids: [assetId], ports: [2222], reason: TICKET_REASON, duration_sec: 3600 },
  })).json()

  const appr = await login(request, APPROVER)
  const approved = await request.post(`${API}/api/v1/tickets/${ticket.id}/approve`, { headers: auth(appr) })
  expect(approved.ok(), `approve: ${approved.status()} ${await approved.text()}`).toBeTruthy()
  expect((await approved.json()).status).toBe("active")

  return { ticketId: ticket.id }
}
