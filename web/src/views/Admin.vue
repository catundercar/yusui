<script setup lang="ts">
import { ref, onMounted, computed } from "vue"
import { useI18n } from "vue-i18n"
import { ElMessage } from "element-plus"
import { api } from "../api"

const { t } = useI18n()
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
    ElMessage.success(t("common.added"))
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
  <div class="page">
    <h2 class="page-title">{{ t("app.navAdmin") }}</h2>
    <el-tabs v-model="tab" class="admin-tabs">
      <!-- projects -->
      <el-tab-pane :label="t('admin.tabProjects')" name="projects">
        <div class="form-card">
          <el-form :inline="true">
            <el-form-item><el-input v-model="pf.code" :placeholder="t('admin.phCode')" /></el-form-item>
            <el-form-item><el-input v-model="pf.name" :placeholder="t('admin.phName')" /></el-form-item>
            <el-form-item><el-input v-model="pf.cidrs" :placeholder="t('admin.phCidrs')" /></el-form-item>
            <el-button type="primary" @click="addProject">{{ t("admin.addProject") }}</el-button>
          </el-form>
        </div>
        <div class="panel">
          <el-table :data="projects" style="width: 100%">
            <el-table-column prop="id" :label="t('admin.colId')" width="64" />
            <el-table-column prop="code" :label="t('admin.colCode')" />
            <el-table-column prop="name" :label="t('admin.colName')" />
            <el-table-column :label="t('admin.colCidrs')">
              <template #default="{ row }"><span class="ys-mono">{{ (row.cidrs || []).join(", ") }}</span></template>
            </el-table-column>
            <template #empty><div class="empty">{{ t("admin.emptyProjects") }}</div></template>
          </el-table>
        </div>
      </el-tab-pane>

      <!-- agents -->
      <el-tab-pane :label="t('admin.tabAgents')" name="agents">
        <div class="form-card">
          <el-form :inline="true">
            <el-form-item><el-select v-model="af.project_id" :placeholder="t('admin.phProject')"><el-option v-for="p in projects" :key="p.id" :label="p.code" :value="p.id" /></el-select></el-form-item>
            <el-form-item><el-select v-model="af.role" style="width: 130px"><el-option label="primary" value="primary" /><el-option label="secondary" value="secondary" /></el-select></el-form-item>
            <el-form-item><el-input v-model="af.hostname" :placeholder="t('admin.phHostname')" /></el-form-item>
            <el-button type="primary" @click="addAgent">{{ t("admin.addAgent") }}</el-button>
          </el-form>
        </div>
        <div class="panel">
          <el-table :data="agents" style="width: 100%">
            <el-table-column prop="id" :label="t('admin.colId')" width="64" />
            <el-table-column :label="t('admin.colProject')"><template #default="{ row }">{{ projMap[row.project_id] }}</template></el-table-column>
            <el-table-column prop="role" :label="t('admin.colRole')" />
            <el-table-column prop="hostname" :label="t('admin.colHostname')"><template #default="{ row }"><span class="ys-mono">{{ row.hostname }}</span></template></el-table-column>
            <el-table-column prop="status" :label="t('admin.colStatus')" />
            <template #empty><div class="empty">{{ t("admin.emptyAgents") }}</div></template>
          </el-table>
        </div>
      </el-tab-pane>

      <!-- assets -->
      <el-tab-pane :label="t('admin.tabAssets')" name="assets">
        <div class="form-card">
          <el-form :inline="true">
            <el-form-item><el-select v-model="sf.project_id" :placeholder="t('admin.phProject')"><el-option v-for="p in projects" :key="p.id" :label="p.code" :value="p.id" /></el-select></el-form-item>
            <el-form-item><el-input v-model="sf.name" :placeholder="t('admin.phName')" /></el-form-item>
            <el-form-item><el-input v-model="sf.ip_internal" :placeholder="t('admin.phIp')" /></el-form-item>
            <el-form-item><el-input v-model="sf.ports" :placeholder="t('admin.phPort')" style="width: 120px" /></el-form-item>
            <el-button type="primary" @click="addAsset">{{ t("admin.addAsset") }}</el-button>
          </el-form>
        </div>
        <div class="panel">
          <el-table :data="assets" style="width: 100%">
            <el-table-column prop="id" :label="t('admin.colId')" width="64" />
            <el-table-column :label="t('admin.colProject')"><template #default="{ row }">{{ projMap[row.project_id] }}</template></el-table-column>
            <el-table-column prop="name" :label="t('admin.colName')" />
            <el-table-column :label="t('admin.colIp')"><template #default="{ row }"><span class="ys-mono ys-tabular">{{ row.ip_internal }}</span></template></el-table-column>
            <el-table-column :label="t('admin.colPorts')"><template #default="{ row }"><span class="ys-mono ys-tabular">{{ (row.ports || []).join(",") }}</span></template></el-table-column>
            <template #empty><div class="empty">{{ t("admin.emptyAssets") }}</div></template>
          </el-table>
        </div>
      </el-tab-pane>

      <!-- credentials -->
      <el-tab-pane :label="t('admin.tabCreds')" name="creds">
        <div class="form-card">
          <el-form :inline="true">
            <el-form-item><el-select v-model="cf.asset_id" :placeholder="t('admin.phAsset')"><el-option v-for="a in assets" :key="a.id" :label="a.name + ' (' + a.ip_internal + ')'" :value="a.id" /></el-select></el-form-item>
            <el-form-item><el-input v-model="cf.ssh_user" :placeholder="t('admin.phSshUser')" /></el-form-item>
            <el-form-item><el-select v-model="cf.auth_kind" style="width: 120px"><el-option label="password" value="password" /><el-option label="key" value="key" /></el-select></el-form-item>
            <el-form-item><el-input v-model="cf.secret" type="password" show-password :placeholder="t('admin.phSecret')" /></el-form-item>
            <el-button type="primary" @click="addCred">{{ t("admin.saveCred") }}</el-button>
          </el-form>
        </div>
        <el-alert type="info" :closable="false" :title="t('admin.credNote')" />
      </el-tab-pane>

      <!-- users -->
      <el-tab-pane :label="t('admin.tabUsers')" name="users">
        <div class="form-card">
          <el-form :inline="true">
            <el-form-item><el-input v-model="uf.username" :placeholder="t('admin.phUsername')" /></el-form-item>
            <el-form-item><el-select v-model="uf.role" style="width: 140px"><el-option :label="t('roles.requester')" value="requester" /><el-option :label="t('roles.approver')" value="approver" /><el-option :label="t('roles.admin')" value="admin" /></el-select></el-form-item>
            <el-form-item><el-input v-model="uf.password" type="password" show-password :placeholder="t('admin.phPassword')" /></el-form-item>
            <el-button type="primary" @click="addUser">{{ t("admin.addUser") }}</el-button>
          </el-form>
        </div>
        <div class="panel">
          <el-table :data="users" style="width: 100%">
            <el-table-column prop="id" :label="t('admin.colId')" width="64" />
            <el-table-column prop="username" :label="t('admin.colUsername')" />
            <el-table-column :label="t('admin.colRole')">
              <template #default="{ row }"><span :data-role="row.role">{{ t(`roles.${row.role}`) }}</span></template>
            </el-table-column>
            <el-table-column :label="t('admin.colActive')">
              <template #default="{ row }">
                <span class="ys-status" :class="row.is_active ? 'ok' : 'muted'"><i class="ys-dot" />{{ row.is_active }}</span>
              </template>
            </el-table-column>
            <template #empty><div class="empty">{{ t("admin.emptyUsers") }}</div></template>
          </el-table>
        </div>
      </el-tab-pane>
    </el-tabs>
  </div>
</template>

<style scoped>
.page {
  max-width: 1180px;
  margin: 0 auto;
}
.page-title {
  margin: 0 0 12px;
  font-size: 20px;
  font-weight: 600;
  letter-spacing: -0.012em;
}

.form-card {
  background: var(--ys-surface);
  border: 1px solid var(--ys-border);
  border-radius: var(--ys-radius-lg);
  box-shadow: var(--ys-shadow-card);
  padding: 16px 16px 2px;
  margin-bottom: 16px;
}
.form-card :deep(.el-form--inline .el-form-item) {
  margin-right: 12px;
}

.panel {
  background: var(--ys-surface);
  border: 1px solid var(--ys-border);
  border-radius: var(--ys-radius-lg);
  box-shadow: var(--ys-shadow-card);
  overflow: hidden;
}

.ys-status {
  display: inline-flex;
  align-items: center;
  font-weight: 500;
  font-size: 13px;
}
.ys-status.ok {
  color: var(--el-color-success);
}
.ys-status.muted {
  color: var(--ys-muted);
}

.empty {
  padding: 28px 0;
  color: var(--ys-muted);
  font-size: 13px;
}
</style>
