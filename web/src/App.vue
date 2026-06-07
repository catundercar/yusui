<script setup lang="ts">
import { useRouter, useRoute } from "vue-router"
import { session, logout } from "./auth"

const router = useRouter()
const route = useRoute()
function doLogout() {
  logout()
  router.push("/login")
}
</script>

<template>
  <el-container style="height: 100vh">
    <el-header
      v-if="session.user && route.path !== '/login'"
      style="display: flex; align-items: center; border-bottom: 1px solid #ebeef5; background: #fff"
    >
      <div style="font-weight: 600; font-size: 16px; white-space: nowrap">YuSui · 零信任运维接入</div>
      <el-menu mode="horizontal" router :default-active="route.path" style="flex: 1; margin: 0 24px; border: none">
        <el-menu-item index="/tickets">工单</el-menu-item>
        <el-menu-item v-if="session.user.role === 'admin'" index="/admin">资源管理</el-menu-item>
      </el-menu>
      <div style="white-space: nowrap">
        <span style="margin-right: 12px; color: #909399">{{ session.user.username }} · {{ session.user.role }}</span>
        <el-button size="small" @click="doLogout">登出</el-button>
      </div>
    </el-header>
    <el-main :style="route.path.endsWith('/terminal') ? 'padding:0' : ''">
      <router-view />
    </el-main>
  </el-container>
</template>
