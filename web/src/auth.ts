import { reactive } from "vue"

const TK = "yusui_token"
const RK = "yusui_refresh"
const UK = "yusui_user"

export interface User {
  id: number
  username: string
  role: string
}

export const session = reactive({
  token: localStorage.getItem(TK) || "",
  refresh: localStorage.getItem(RK) || "",
  user: JSON.parse(localStorage.getItem(UK) || "null") as User | null,
})

export function setSession(token: string, refresh: string, user: User) {
  session.token = token
  session.refresh = refresh
  session.user = user
  localStorage.setItem(TK, token)
  localStorage.setItem(RK, refresh)
  localStorage.setItem(UK, JSON.stringify(user))
}

export function setToken(token: string) {
  session.token = token
  localStorage.setItem(TK, token)
}

export function logout() {
  session.token = ""
  session.refresh = ""
  session.user = null
  localStorage.removeItem(TK)
  localStorage.removeItem(RK)
  localStorage.removeItem(UK)
}
