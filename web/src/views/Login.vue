<script setup lang="ts">
import { ref } from "vue"
import { useRouter } from "vue-router"
import { useI18n } from "vue-i18n"
import { ElMessage } from "element-plus"
import { Moon, Sunny } from "@element-plus/icons-vue"
import { api, errText } from "../api"
import { setSession } from "../auth"
import { setLang, type Lang } from "../i18n"
import { theme, toggleTheme } from "../theme"

const router = useRouter()
const { t, locale } = useI18n()
const username = ref("admin")
const password = ref("")
const loading = ref(false)

async function submit() {
  if (!username.value || !password.value) return
  loading.value = true
  try {
    const d = await api.login(username.value, password.value)
    setSession(d.access_token, d.refresh_token, d.user)
    router.push("/tickets")
  } catch (e: any) {
    ElMessage.error(errText(e))
  } finally {
    loading.value = false
  }
}
function switchLang(l: Lang) {
  setLang(l)
}
</script>

<template>
  <div class="login">
    <div class="login-top">
      <button class="login-theme" :aria-label="theme === 'dark' ? 'light' : 'dark'" @click="toggleTheme">
        <el-icon :size="16">
          <Moon v-if="theme === 'light'" />
          <Sunny v-else />
        </el-icon>
      </button>
      <div class="login-lang" role="group" :aria-label="t('app.language')">
        <button :class="{ on: locale === 'zh' }" @click="switchLang('zh')">中</button>
        <button :class="{ on: locale === 'en' }" @click="switchLang('en')">EN</button>
      </div>
    </div>

    <div class="login-box">
      <div class="login-brand">
        <span class="login-mark ys-mono">YS</span>
        <div class="login-titles">
          <h1>{{ t("app.brand") }}</h1>
          <p>{{ t("login.subtitle") }}</p>
        </div>
      </div>

      <el-card class="login-card">
        <el-form label-position="top" @submit.prevent="submit">
          <el-form-item :label="t('login.username')">
            <el-input v-model="username" :placeholder="t('login.username')" autocomplete="username" />
          </el-form-item>
          <el-form-item :label="t('login.password')">
            <el-input
              v-model="password"
              type="password"
              show-password
              :placeholder="t('login.password')"
              autocomplete="current-password"
              @keyup.enter="submit"
            />
          </el-form-item>
          <el-button type="primary" :loading="loading" class="login-submit" @click="submit">
            {{ t("login.signIn") }}
          </el-button>
        </el-form>
      </el-card>

      <p class="login-foot ys-muted">{{ t("app.brandFull") }}</p>
    </div>
  </div>
</template>

<style scoped>
.login {
  position: relative;
  min-height: 100%;
  display: grid;
  place-items: center;
  padding: 24px;
  /* a single, very faint indigo wash from the top: orientation, not decoration */
  background:
    radial-gradient(120% 60% at 50% -10%, rgba(79, 70, 229, 0.06), transparent 60%),
    var(--ys-canvas);
}
.login-top {
  position: absolute;
  top: 20px;
  right: 20px;
  display: flex;
  align-items: center;
  gap: 8px;
}
.login-theme {
  display: grid;
  place-items: center;
  width: 30px;
  height: 30px;
  border: 1px solid var(--ys-border);
  border-radius: var(--ys-radius);
  background: var(--ys-surface);
  color: var(--ys-muted);
  cursor: pointer;
}
.login-theme:hover {
  color: var(--ys-accent);
}
.login-lang {
  display: inline-flex;
  border: 1px solid var(--ys-border);
  border-radius: var(--ys-radius);
  overflow: hidden;
  background: var(--ys-surface);
}
.login-lang button {
  appearance: none;
  border: none;
  background: transparent;
  padding: 4px 10px;
  font-size: 12px;
  font-weight: 500;
  color: var(--ys-muted);
  cursor: pointer;
}
.login-lang button.on {
  color: var(--ys-accent);
  background: var(--ys-accent-soft);
}

.login-box {
  width: 100%;
  max-width: 360px;
}
.login-brand {
  display: flex;
  align-items: center;
  gap: 12px;
  margin-bottom: 20px;
}
.login-mark {
  display: grid;
  place-items: center;
  width: 44px;
  height: 44px;
  border-radius: 11px;
  background: var(--ys-accent);
  color: #fff;
  font-size: 18px;
  font-weight: 600;
  box-shadow: 0 2px 8px rgba(79, 70, 229, 0.35);
}
.login-titles h1 {
  margin: 0;
  font-size: 20px;
  font-weight: 600;
  letter-spacing: -0.012em;
}
.login-titles p {
  margin: 2px 0 0;
  font-size: 13px;
  color: var(--ys-muted);
}
.login-card {
  border-radius: var(--ys-radius-lg);
}
.login-submit {
  width: 100%;
  margin-top: 4px;
}
.login-foot {
  text-align: center;
  font-size: 12px;
  margin: 18px 0 0;
}
</style>
