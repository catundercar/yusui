import { createRouter, createWebHistory } from "vue-router"
import { session } from "./auth"
import Login from "./views/Login.vue"
import Tickets from "./views/Tickets.vue"
import Admin from "./views/Admin.vue"
import Terminal from "./views/Terminal.vue"

export const router = createRouter({
  history: createWebHistory(),
  routes: [
    { path: "/login", component: Login },
    { path: "/", redirect: "/tickets" },
    { path: "/tickets", component: Tickets, meta: { auth: true } },
    { path: "/admin", component: Admin, meta: { auth: true } },
    { path: "/tickets/:id/terminal", component: Terminal, meta: { auth: true } },
  ],
})

router.beforeEach((to) => {
  if (to.meta.auth && !session.token) return "/login"
  if (to.path === "/login" && session.token) return "/tickets"
})
