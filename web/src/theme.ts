import { ref } from "vue"

export type Theme = "light" | "dark"
const KEY = "yusui_theme"

// Light by default (decision); dark is opt-in and persisted. Dark is applied by
// toggling `.dark` on <html>, which switches Element Plus dark css-vars plus our
// own html.dark token overrides in styles/theme.css.
export const theme = ref<Theme>((localStorage.getItem(KEY) as Theme) || "light")

function apply(t: Theme) {
  document.documentElement.classList.toggle("dark", t === "dark")
}

export function setTheme(t: Theme) {
  theme.value = t
  localStorage.setItem(KEY, t)
  apply(t)
}

export function toggleTheme() {
  setTheme(theme.value === "dark" ? "light" : "dark")
}

// apply on boot (before mount, so there is no light->dark flash)
apply(theme.value)
