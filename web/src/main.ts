import { createApp } from "vue"
import ElementPlus from "element-plus"
import "element-plus/dist/index.css"
import "@fontsource-variable/geist"
import "@fontsource-variable/geist-mono"
import "./styles/theme.css"
import App from "./App.vue"
import { router } from "./router"
import { i18n } from "./i18n"

createApp(App).use(ElementPlus).use(i18n).use(router).mount("#app")
