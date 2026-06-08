import { session, setToken, logout } from "./auth"
import { i18n } from "./i18n"

// errText maps a backend error `code` to a localized message. Codes with a
// single stable meaning are translated; the rest (e.g. validation) fall back to
// the server's English `error` text, then to a generic message.
export function errText(e: any): string {
  const c = e?.code
  if (c && i18n.global.te(`errors.${c}`)) return i18n.global.t(`errors.${c}`) as string
  return e?.message || (i18n.global.t("errors.internal") as string)
}

// tryRefresh swaps an expired access token for a new one using the refresh
// token. Returns false if there is no/invalid refresh token.
async function tryRefresh(): Promise<boolean> {
  if (!session.refresh) return false
  try {
    const res = await fetch("/api/v1/auth/refresh", {
      method: "POST",
      headers: { "content-type": "application/json" },
      body: JSON.stringify({ refresh_token: session.refresh }),
    })
    if (!res.ok) return false
    const d = await res.json()
    setToken(d.access_token)
    return true
  } catch {
    return false
  }
}

async function req(method: string, path: string, body?: any, allowRetry = true): Promise<any> {
  const headers: Record<string, string> = { "content-type": "application/json" }
  if (session.token) headers["authorization"] = "Bearer " + session.token
  const res = await fetch("/api/v1" + path, {
    method,
    headers,
    body: body ? JSON.stringify(body) : undefined,
  })

  // Expired/invalid access token: refresh once and retry; else drop to login.
  if (res.status === 401 && allowRetry && path !== "/auth/login" && path !== "/auth/refresh") {
    if (await tryRefresh()) {
      return req(method, path, body, false)
    }
    logout()
    if (location.pathname !== "/login") location.href = "/login"
    const e: any = new Error("登录已过期，请重新登录")
    e.status = 401
    e.code = "unauthorized"
    throw e
  }

  const text = await res.text()
  const data = text ? JSON.parse(text) : null
  if (!res.ok) {
    const e: any = new Error(data?.error || res.statusText)
    e.status = res.status
    e.code = data?.code
    throw e
  }
  return data
}

export const api = {
  login: (username: string, password: string, mfa_code = "") =>
    req("POST", "/auth/login", { username, password, mfa_code }),
  stepup: (password: string) => req("POST", "/auth/stepup", { password }),
  me: () => req("GET", "/me"),

  listTickets: () => req("GET", "/tickets"),
  getTicket: (id: number) => req("GET", "/tickets/" + id),
  submitTicket: (b: any) => req("POST", "/tickets", b),
  approve: (id: number) => req("POST", `/tickets/${id}/approve`),
  reject: (id: number, reason: string) => req("POST", `/tickets/${id}/reject`, { reason }),
  revoke: (id: number, reason: string) => req("POST", `/tickets/${id}/revoke`, { reason }),

  listProjects: () => req("GET", "/projects"),
  listAgents: () => req("GET", "/agents"),
  listAssets: () => req("GET", "/assets"),
  listUsers: () => req("GET", "/users"),
  createProject: (b: any) => req("POST", "/projects", b),
  createAgent: (b: any) => req("POST", "/agents", b),
  createAsset: (b: any) => req("POST", "/assets", b),
  createCredential: (assetId: number, b: any) => req("POST", `/assets/${assetId}/credentials`, b),
  createUser: (b: any) => req("POST", "/users", b),
}

// withStepUp retries a sensitive action once after re-auth on a step-up 403.
export async function withStepUp(action: () => Promise<any>, getPassword: () => Promise<string>) {
  try {
    return await action()
  } catch (e: any) {
    if (e.status === 403 && e.code === "stepup_required") {
      const pw = await getPassword()
      const d = await api.stepup(pw)
      setToken(d.access_token)
      return await action()
    }
    throw e
  }
}
