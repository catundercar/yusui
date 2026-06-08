import { createI18n } from "vue-i18n"
import zh from "./locales/zh"
import en from "./locales/en"

export type Lang = "zh" | "en"
const KEY = "yusui_lang"

// Default is Chinese (product decision). zh message values are kept identical to
// the original hardcoded strings so e2e selectors that match on visible text
// stay valid; language-variant text in tests is pinned via data-* attributes.
const saved = (localStorage.getItem(KEY) as Lang) || "zh"

export const i18n = createI18n({
  legacy: false,
  locale: saved,
  fallbackLocale: "zh",
  messages: { zh, en },
})

export function currentLang(): Lang {
  return i18n.global.locale.value as Lang
}

export function setLang(l: Lang) {
  i18n.global.locale.value = l
  localStorage.setItem(KEY, l)
  document.documentElement.lang = l === "en" ? "en" : "zh-CN"
}

// apply <html lang> on boot
document.documentElement.lang = saved === "en" ? "en" : "zh-CN"
