<script setup lang="ts">
import { computed } from "vue"
import { useRouter, useRoute } from "vue-router"
import { useI18n } from "vue-i18n"
import zhCn from "element-plus/es/locale/lang/zh-cn"
import enLocale from "element-plus/es/locale/lang/en"
import { Moon, Sunny } from "@element-plus/icons-vue"
import { session, logout } from "./auth"
import { setLang, type Lang } from "./i18n"
import { theme, toggleTheme } from "./theme"

const router = useRouter()
const route = useRoute()
const { t, locale } = useI18n()

const elLocale = computed(() => (locale.value === "en" ? enLocale : zhCn))
const showChrome = computed(() => !!session.user && route.path !== "/login")
const isTerminal = computed(() => route.path.endsWith("/terminal"))
const roleLabel = computed(() => (session.user ? t(`roles.${session.user.role}`) : ""))
const initial = computed(() => (session.user?.username || "?").charAt(0).toUpperCase())

function doLogout() {
  logout()
  router.push("/login")
}
function switchLang(l: Lang) {
  setLang(l)
}
</script>

<template>
  <el-config-provider :locale="elLocale">
    <div class="ys-shell">
      <header v-if="showChrome" class="ys-topbar">
        <div class="ys-brand">
          <span class="ys-mark ys-mono">YS</span>
          <span class="ys-brand-name">{{ t("app.brand") }}</span>
        </div>

        <el-menu class="ys-nav" mode="horizontal" router :default-active="route.path" :ellipsis="false">
          <el-menu-item index="/tickets">{{ t("app.navTickets") }}</el-menu-item>
          <el-menu-item v-if="session.user!.role === 'admin'" index="/admin">{{ t("app.navAdmin") }}</el-menu-item>
        </el-menu>

        <div class="ys-actions">
          <button class="ys-theme" :aria-label="theme === 'dark' ? 'light' : 'dark'" @click="toggleTheme">
            <el-icon :size="17">
              <Moon v-if="theme === 'light'" />
              <Sunny v-else />
            </el-icon>
          </button>
          <div class="ys-lang" role="group" :aria-label="t('app.language')">
            <button :class="{ on: locale === 'zh' }" @click="switchLang('zh')">中</button>
            <button :class="{ on: locale === 'en' }" @click="switchLang('en')">EN</button>
          </div>
          <div class="ys-user">
            <span class="ys-avatar">{{ initial }}</span>
            <span class="ys-user-meta">
              <span class="ys-user-name">{{ session.user!.username }}</span>
              <span class="ys-user-role">{{ roleLabel }}</span>
            </span>
          </div>
          <el-button text size="small" @click="doLogout">{{ t("app.logout") }}</el-button>
        </div>
      </header>

      <main class="ys-main" :class="{ 'ys-main--flush': isTerminal }">
        <router-view v-slot="{ Component }">
          <transition :name="isTerminal ? '' : 'ys-view'" mode="out-in">
            <component :is="Component" :key="route.path" />
          </transition>
        </router-view>
      </main>
    </div>
  </el-config-provider>
</template>

<style scoped>
.ys-shell {
  height: 100vh;
  display: flex;
  flex-direction: column;
  background: var(--ys-canvas);
}

/* top bar */
.ys-topbar {
  flex: none;
  height: 56px;
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 0 20px;
  background: var(--ys-surface);
  border-bottom: 1px solid var(--ys-border);
}
.ys-brand {
  display: flex;
  align-items: center;
  gap: 10px;
  padding-right: 8px;
}
.ys-mark {
  display: grid;
  place-items: center;
  width: 28px;
  height: 28px;
  border-radius: 7px;
  background: var(--ys-accent);
  color: #fff;
  font-size: 13px;
  font-weight: 600;
  letter-spacing: 0.02em;
  box-shadow: 0 1px 2px rgba(79, 70, 229, 0.35);
}
.ys-brand-name {
  font-weight: 600;
  font-size: 15px;
  letter-spacing: -0.01em;
}

.ys-nav {
  flex: 1;
  border-bottom: none !important;
  background: transparent;
  margin-left: 12px;
}

.ys-actions {
  display: flex;
  align-items: center;
  gap: 14px;
}

/* theme toggle */
.ys-theme {
  display: grid;
  place-items: center;
  width: 30px;
  height: 30px;
  border: 1px solid var(--ys-border);
  border-radius: var(--ys-radius);
  background: var(--ys-canvas);
  color: var(--ys-muted);
  cursor: pointer;
  transition: color 0.12s var(--ys-ease), border-color 0.12s var(--ys-ease);
}
.ys-theme:hover {
  color: var(--ys-accent);
  border-color: var(--ys-border-strong);
}

/* language segmented control */
.ys-lang {
  display: inline-flex;
  border: 1px solid var(--ys-border);
  border-radius: var(--ys-radius);
  overflow: hidden;
  background: var(--ys-canvas);
}
.ys-lang button {
  appearance: none;
  border: none;
  background: transparent;
  padding: 4px 10px;
  font-size: 12px;
  font-weight: 500;
  color: var(--ys-muted);
  cursor: pointer;
  transition: background-color 0.12s var(--ys-ease), color 0.12s var(--ys-ease);
}
.ys-lang button.on {
  background: var(--ys-surface);
  color: var(--ys-accent);
  box-shadow: var(--ys-shadow-sm);
}

/* user chip */
.ys-user {
  display: flex;
  align-items: center;
  gap: 8px;
}
.ys-avatar {
  display: grid;
  place-items: center;
  width: 28px;
  height: 28px;
  border-radius: 999px;
  background: var(--ys-accent-soft);
  color: var(--ys-accent);
  font-weight: 600;
  font-size: 13px;
}
.ys-user-meta {
  display: flex;
  flex-direction: column;
  line-height: 1.15;
}
.ys-user-name {
  font-size: 13px;
  font-weight: 500;
}
.ys-user-role {
  font-size: 11px;
  color: var(--ys-muted);
}

/* main content */
.ys-main {
  flex: 1;
  min-height: 0;
  overflow: auto;
  padding: 28px 24px 48px;
}
.ys-main--flush {
  padding: 0;
  overflow: hidden;
}

@media (max-width: 640px) {
  .ys-topbar {
    padding: 0 10px;
    gap: 2px;
  }
  .ys-brand {
    padding-right: 0;
  }
  .ys-brand-name {
    display: none;
  }
  .ys-nav {
    margin-left: 2px;
  }
  .ys-nav :deep(.el-menu-item) {
    padding: 0 10px;
  }
  .ys-actions {
    gap: 8px;
  }
  .ys-avatar {
    display: none;
  }
  .ys-user-meta {
    display: none;
  }
  .ys-main {
    padding: 16px 12px 32px;
  }
}
</style>
