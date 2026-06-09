<script setup lang="ts">
import { ref, onMounted, computed } from "vue"
import { useI18n } from "vue-i18n"
import { ElMessage } from "element-plus"
import { api, errText } from "../api"

const { t } = useI18n()
const tab = ref("projects")
const projects = ref<any[]>([])
const agents = ref<any[]>([])
const assets = ref<any[]>([])
const users = ref<any[]>([])
const projMap = computed(() => Object.fromEntries(projects.value.map((p) => [p.id, p.code])))

const blank = {
  projects: () => ({ code: "", name: "", cidrs: "10.0.0.0/8" }),
  agents: () => ({ project_id: null, role: "primary", hostname: "" }),
  assets: () => ({ project_id: null, name: "", ip_internal: "", ports: "22" }),
  creds: () => ({ asset_id: null, ssh_user: "ops-yusui", auth_kind: "password", secret: "" }),
  users: () => ({ username: "", role: "requester", password: "" }),
} as const

const pf = ref<any>(blank.projects())
const af = ref<any>(blank.agents())
const sf = ref<any>(blank.assets())
const cf = ref<any>(blank.creds())
const uf = ref<any>(blank.users())

async function loadAll() {
  try {
    projects.value = (await api.listProjects()) || []
    agents.value = (await api.listAgents()) || []
    assets.value = (await api.listAssets()) || []
    users.value = (await api.listUsers()) || []
  } catch (e: any) {
    ElMessage.error(errText(e))
  }
}
onMounted(loadAll)

// Creation is a deliberate action: a per-tab "新建" button opens one labeled
// dialog (same interaction as the ticket submit dialog), keeping the table the
// primary surface instead of a permanent inline form.
const dialog = ref(false)
const saving = ref(false)

const dialogTitle = computed(
  () => ({ projects: t("admin.addProject"), agents: t("admin.addAgent"), assets: t("admin.addAsset"), creds: t("admin.saveCred"), users: t("admin.addUser") }[tab.value]),
)

function openCreate() {
  if (tab.value === "projects") pf.value = blank.projects()
  else if (tab.value === "agents") af.value = blank.agents()
  else if (tab.value === "assets") sf.value = blank.assets()
  else if (tab.value === "creds") cf.value = blank.creds()
  else if (tab.value === "users") uf.value = blank.users()
  dialog.value = true
}

const creators: Record<string, () => Promise<any>> = {
  projects: () => api.createProject({ code: pf.value.code, name: pf.value.name, cidrs: pf.value.cidrs.split(",").map((s: string) => s.trim()) }),
  agents: () => api.createAgent({ ...af.value }),
  assets: () => api.createAsset({ project_id: sf.value.project_id, name: sf.value.name, ip_internal: sf.value.ip_internal, ports: sf.value.ports.split(",").map((x: string) => parseInt(x.trim(), 10)).filter((n: number) => n > 0) }),
  creds: () => api.createCredential(cf.value.asset_id, { ssh_user: cf.value.ssh_user, auth_kind: cf.value.auth_kind, secret: cf.value.secret }),
  users: () => api.createUser({ ...uf.value }),
}

async function submitCreate() {
  saving.value = true
  try {
    await creators[tab.value]()
    ElMessage.success(t("common.added"))
    dialog.value = false
    await loadAll()
  } catch (e: any) {
    ElMessage.error(errText(e))
  } finally {
    saving.value = false
  }
}
</script>

<template>
  <div class="page">
    <h2 class="page-title">{{ t("app.navAdmin") }}</h2>
    <el-tabs v-model="tab" class="admin-tabs">
      <el-tab-pane :label="t('admin.tabProjects')" name="projects">
        <div class="tab-bar">
          <span class="tab-count ys-muted">{{ t("admin.count", { n: projects.length }) }}</span>
          <el-button type="primary" @click="openCreate">{{ t("admin.new") }}</el-button>
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

      <el-tab-pane :label="t('admin.tabAgents')" name="agents">
        <div class="tab-bar">
          <span class="tab-count ys-muted">{{ t("admin.count", { n: agents.length }) }}</span>
          <el-button type="primary" @click="openCreate">{{ t("admin.new") }}</el-button>
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

      <el-tab-pane :label="t('admin.tabAssets')" name="assets">
        <div class="tab-bar">
          <span class="tab-count ys-muted">{{ t("admin.count", { n: assets.length }) }}</span>
          <el-button type="primary" @click="openCreate">{{ t("admin.new") }}</el-button>
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

      <el-tab-pane :label="t('admin.tabCreds')" name="creds">
        <div class="tab-bar">
          <span class="tab-count ys-muted">{{ t("admin.credNote") }}</span>
          <el-button type="primary" @click="openCreate">{{ t("admin.new") }}</el-button>
        </div>
        <div class="panel">
          <el-table :data="assets" style="width: 100%">
            <el-table-column prop="id" :label="t('admin.colId')" width="64" />
            <el-table-column prop="name" :label="t('admin.colName')" />
            <el-table-column :label="t('admin.colIp')"><template #default="{ row }"><span class="ys-mono ys-tabular">{{ row.ip_internal }}</span></template></el-table-column>
            <el-table-column :label="t('admin.fSshUser')"><template #default><span class="ys-muted">••••••</span></template></el-table-column>
            <template #empty><div class="empty">{{ t("admin.emptyAssets") }}</div></template>
          </el-table>
        </div>
      </el-tab-pane>

      <el-tab-pane :label="t('admin.tabUsers')" name="users">
        <div class="tab-bar">
          <span class="tab-count ys-muted">{{ t("admin.count", { n: users.length }) }}</span>
          <el-button type="primary" @click="openCreate">{{ t("admin.new") }}</el-button>
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

    <!-- one labeled creation dialog, driven by the active tab -->
    <el-dialog v-model="dialog" :title="dialogTitle" width="460" :close-on-click-modal="false">
      <el-form v-if="tab === 'projects'" label-width="96px">
        <el-form-item :label="t('admin.fCode')"><el-input v-model="pf.code" :placeholder="t('admin.phCode')" /></el-form-item>
        <el-form-item :label="t('admin.fName')"><el-input v-model="pf.name" :placeholder="t('admin.phName')" /></el-form-item>
        <el-form-item :label="t('admin.fCidrs')"><el-input v-model="pf.cidrs" :placeholder="t('admin.phCidrs')" /></el-form-item>
      </el-form>

      <el-form v-else-if="tab === 'agents'" label-width="96px">
        <el-form-item :label="t('admin.fProject')">
          <el-select v-model="af.project_id" :placeholder="t('admin.phProject')" style="width: 100%"><el-option v-for="p in projects" :key="p.id" :label="p.code" :value="p.id" /></el-select>
        </el-form-item>
        <el-form-item :label="t('admin.fRole')">
          <el-select v-model="af.role" style="width: 100%"><el-option label="primary" value="primary" /><el-option label="secondary" value="secondary" /></el-select>
        </el-form-item>
        <el-form-item :label="t('admin.fHostname')"><el-input v-model="af.hostname" :placeholder="t('admin.phHostname')" /></el-form-item>
      </el-form>

      <el-form v-else-if="tab === 'assets'" label-width="96px">
        <el-form-item :label="t('admin.fProject')">
          <el-select v-model="sf.project_id" :placeholder="t('admin.phProject')" style="width: 100%"><el-option v-for="p in projects" :key="p.id" :label="p.code" :value="p.id" /></el-select>
        </el-form-item>
        <el-form-item :label="t('admin.fName')"><el-input v-model="sf.name" :placeholder="t('admin.phName')" /></el-form-item>
        <el-form-item :label="t('admin.fIp')"><el-input v-model="sf.ip_internal" :placeholder="t('admin.phIp')" /></el-form-item>
        <el-form-item :label="t('admin.fPorts')"><el-input v-model="sf.ports" :placeholder="t('admin.phPort')" /></el-form-item>
      </el-form>

      <el-form v-else-if="tab === 'creds'" label-width="96px">
        <el-form-item :label="t('admin.fAsset')">
          <el-select v-model="cf.asset_id" :placeholder="t('admin.phAsset')" style="width: 100%"><el-option v-for="a in assets" :key="a.id" :label="a.name + ' (' + a.ip_internal + ')'" :value="a.id" /></el-select>
        </el-form-item>
        <el-form-item :label="t('admin.fSshUser')"><el-input v-model="cf.ssh_user" :placeholder="t('admin.phSshUser')" /></el-form-item>
        <el-form-item :label="t('admin.fAuthKind')">
          <el-select v-model="cf.auth_kind" style="width: 100%"><el-option label="password" value="password" /><el-option label="key" value="key" /></el-select>
        </el-form-item>
        <el-form-item :label="t('admin.fSecret')"><el-input v-model="cf.secret" type="password" show-password :placeholder="t('admin.phSecret')" /></el-form-item>
        <el-alert type="info" :closable="false" :title="t('admin.credNote')" />
      </el-form>

      <el-form v-else-if="tab === 'users'" label-width="96px">
        <el-form-item :label="t('admin.fUsername')"><el-input v-model="uf.username" :placeholder="t('admin.phUsername')" /></el-form-item>
        <el-form-item :label="t('admin.fRole')">
          <el-select v-model="uf.role" style="width: 100%"><el-option :label="t('roles.requester')" value="requester" /><el-option :label="t('roles.approver')" value="approver" /><el-option :label="t('roles.admin')" value="admin" /></el-select>
        </el-form-item>
        <el-form-item :label="t('admin.fPassword')"><el-input v-model="uf.password" type="password" show-password :placeholder="t('admin.phPassword')" /></el-form-item>
      </el-form>

      <template #footer>
        <el-button @click="dialog = false">{{ t("common.cancel") }}</el-button>
        <el-button type="primary" :loading="saving" @click="submitCreate">{{ t("common.submit") }}</el-button>
      </template>
    </el-dialog>
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

.tab-bar {
  display: flex;
  align-items: center;
  justify-content: space-between;
  margin-bottom: 12px;
  min-height: 32px;
}
.tab-count {
  font-size: 13px;
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
