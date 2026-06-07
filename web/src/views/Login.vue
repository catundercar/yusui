<script setup lang="ts">
import { ref } from "vue"
import { useRouter } from "vue-router"
import { ElMessage } from "element-plus"
import { api } from "../api"
import { setSession } from "../auth"

const router = useRouter()
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
    ElMessage.error(e.message || "登录失败")
  } finally {
    loading.value = false
  }
}
</script>

<template>
  <div style="max-width: 360px; margin: 80px auto">
    <h2 style="text-align: center; margin-bottom: 24px">YuSui 登录</h2>
    <el-card>
      <el-form label-position="top" @submit.prevent="submit">
        <el-form-item label="用户名">
          <el-input v-model="username" placeholder="用户名" />
        </el-form-item>
        <el-form-item label="密码">
          <el-input v-model="password" type="password" show-password placeholder="密码" @keyup.enter="submit" />
        </el-form-item>
        <el-button type="primary" :loading="loading" style="width: 100%" @click="submit">登录</el-button>
      </el-form>
    </el-card>
  </div>
</template>
