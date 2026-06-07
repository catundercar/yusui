<script setup lang="ts">
import { ref, onMounted, computed } from "vue"
import { ElMessage } from "element-plus"
import { api } from "../api"

const tab = ref("projects")
const projects = ref<any[]>([])
const agents = ref<any[]>([])
const assets = ref<any[]>([])
const users = ref<any[]>([])
const projMap = computed(() => Object.fromEntries(projects.value.map((p) => [p.id, p.code])))

const pf = ref<any>({ code: "", name: "", cidrs: "10.0.0.0/8" })
const af = ref<any>({ project_id: null, role: "primary", hostname: "" })
const sf = ref<any>({ project_id: null, name: "", ip_internal: "", ports: "22" })
const uf = ref<any>({ username: "", role: "requester", password: "", display_name: "" })
const cf = ref<any>({ asset_id: null, ssh_user: "ops-yusui", auth_kind: "password", secret: "" })

async function loadAll() {
  try {
    projects.value = (await api.listProjects()) || []
    agents.value = (await api.listAgents()) || []
    assets.value = (await api.listAssets()) || []
    users.value = (await api.listUsers()) || []
  } catch (e: any) {
    ElMessage.error(e.message)
  }
}
onMounted(loadAll)

const wrap = (fn: () => Promise<any>) => async () => {
  try {
    await fn()
    ElMessage.success("已添加")
    await loadAll()
  } catch (e: any) {
    ElMessage.error(e.message)
  }
}
const addProject = wrap(() => api.createProject({ code: pf.value.code, name: pf.value.name, cidrs: pf.value.cidrs.split(",").map((s: string) => s.trim()) }))
const addAgent = wrap(() => api.createAgent({ ...af.value }))
const addAsset = wrap(() => api.createAsset({ project_id: sf.value.project_id, name: sf.value.name, ip_internal: sf.value.ip_internal, ports: sf.value.ports.split(",").map((x: string) => parseInt(x.trim(), 10)).filter((n: number) => n > 0) }))
const addUser = wrap(() => api.createUser({ ...uf.value }))
const addCred = wrap(() => api.createCredential(cf.value.asset_id, { ssh_user: cf.value.ssh_user, auth_kind: cf.value.auth_kind, secret: cf.value.secret }))
</script>

<template>
  <el-tabs v-model="tab">
    <el-tab-pane label="项目" name="projects">
      <el-form :inline="true" style="margin-bottom: 12px">
        <el-form-item><el-input v-model="pf.code" placeholder="code" /></el-form-item>
        <el-form-item><el-input v-model="pf.name" placeholder="name" /></el-form-item>
        <el-form-item><el-input v-model="pf.cidrs" placeholder="cidrs (逗号分隔)" /></el-form-item>
        <el-button type="primary" @click="addProject">添加项目</el-button>
      </el-form>
      <el-table :data="projects" border>
        <el-table-column prop="id" label="ID" width="60" />
        <el-table-column prop="code" label="code" />
        <el-table-column prop="name" label="name" />
        <el-table-column label="cidrs"><template #default="{ row }">{{ (row.cidrs || []).join(", ") }}</template></el-table-column>
      </el-table>
    </el-tab-pane>

    <el-tab-pane label="Agent" name="agents">
      <el-form :inline="true" style="margin-bottom: 12px">
        <el-form-item><el-select v-model="af.project_id" placeholder="项目"><el-option v-for="p in projects" :key="p.id" :label="p.code" :value="p.id" /></el-select></el-form-item>
        <el-form-item><el-select v-model="af.role" style="width: 130px"><el-option label="primary" value="primary" /><el-option label="secondary" value="secondary" /></el-select></el-form-item>
        <el-form-item><el-input v-model="af.hostname" placeholder="hostname" /></el-form-item>
        <el-button type="primary" @click="addAgent">添加 Agent</el-button>
      </el-form>
      <el-table :data="agents" border>
        <el-table-column prop="id" label="ID" width="60" />
        <el-table-column label="项目"><template #default="{ row }">{{ projMap[row.project_id] }}</template></el-table-column>
        <el-table-column prop="role" label="role" />
        <el-table-column prop="hostname" label="hostname" />
        <el-table-column prop="status" label="status" />
      </el-table>
    </el-tab-pane>

    <el-tab-pane label="资产" name="assets">
      <el-form :inline="true" style="margin-bottom: 12px">
        <el-form-item><el-select v-model="sf.project_id" placeholder="项目"><el-option v-for="p in projects" :key="p.id" :label="p.code" :value="p.id" /></el-select></el-form-item>
        <el-form-item><el-input v-model="sf.name" placeholder="name" /></el-form-item>
        <el-form-item><el-input v-model="sf.ip_internal" placeholder="10.20.3.7" /></el-form-item>
        <el-form-item><el-input v-model="sf.ports" placeholder="22" style="width: 120px" /></el-form-item>
        <el-button type="primary" @click="addAsset">添加资产</el-button>
      </el-form>
      <el-table :data="assets" border>
        <el-table-column prop="id" label="ID" width="60" />
        <el-table-column label="项目"><template #default="{ row }">{{ projMap[row.project_id] }}</template></el-table-column>
        <el-table-column prop="name" label="name" />
        <el-table-column prop="ip_internal" label="ip" />
        <el-table-column label="ports"><template #default="{ row }">{{ (row.ports || []).join(",") }}</template></el-table-column>
      </el-table>
    </el-tab-pane>

    <el-tab-pane label="凭证" name="creds">
      <el-form :inline="true" style="margin-bottom: 12px">
        <el-form-item><el-select v-model="cf.asset_id" placeholder="资产"><el-option v-for="a in assets" :key="a.id" :label="a.name + ' (' + a.ip_internal + ')'" :value="a.id" /></el-select></el-form-item>
        <el-form-item><el-input v-model="cf.ssh_user" placeholder="ssh user" /></el-form-item>
        <el-form-item><el-select v-model="cf.auth_kind" style="width: 120px"><el-option label="password" value="password" /><el-option label="key" value="key" /></el-select></el-form-item>
        <el-form-item><el-input v-model="cf.secret" type="password" show-password placeholder="secret / 私钥" /></el-form-item>
        <el-button type="primary" @click="addCred">保存凭证</el-button>
      </el-form>
      <el-alert type="info" :closable="false" title="凭证以 AES-256-GCM 信封加密入库;列表与响应永不回显密文。" />
    </el-tab-pane>

    <el-tab-pane label="用户" name="users">
      <el-form :inline="true" style="margin-bottom: 12px">
        <el-form-item><el-input v-model="uf.username" placeholder="username" /></el-form-item>
        <el-form-item><el-select v-model="uf.role" style="width: 140px"><el-option label="requester" value="requester" /><el-option label="approver" value="approver" /><el-option label="admin" value="admin" /></el-select></el-form-item>
        <el-form-item><el-input v-model="uf.password" type="password" show-password placeholder="密码 (≥12, 3 类字符)" /></el-form-item>
        <el-button type="primary" @click="addUser">添加用户</el-button>
      </el-form>
      <el-table :data="users" border>
        <el-table-column prop="id" label="ID" width="60" />
        <el-table-column prop="username" label="username" />
        <el-table-column prop="role" label="role" />
        <el-table-column prop="is_active" label="active" />
      </el-table>
    </el-tab-pane>
  </el-tabs>
</template>
