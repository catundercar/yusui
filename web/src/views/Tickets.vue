<script setup lang="ts">
import { ref, onMounted, computed } from "vue"
import { useRouter } from "vue-router"
import { useI18n } from "vue-i18n"
import { ElMessage, ElMessageBox } from "element-plus"
import { api, withStepUp } from "../api"
import { session } from "../auth"

const router = useRouter()
const { t } = useI18n()
const tickets = ref<any[]>([])
const projects = ref<any[]>([])
const assets = ref<any[]>([])
const loading = ref(false)
const dialog = ref(false)
const form = ref<any>({ project_id: null, asset_ids: [], ports: "22", duration_sec: 3600, reason: "" })

const projMap = computed(() => Object.fromEntries(projects.value.map((p) => [p.id, p.code])))
const assetsForProject = computed(() => assets.value.filter((a) => a.project_id === form.value.project_id))
const isApprover = computed(() => ["approver", "admin"].includes(session.user?.role || ""))

// status enum (raw from backend) -> color class. The label is localized; the
// raw value lives in data-status so tests stay language-independent.
const statusClass: Record<string, string> = {
  pending: "warn",
  approved: "accent",
  active: "ok",
  revoking: "warn",
  closed: "muted",
}

async function load() {
  loading.value = true
  try {
    tickets.value = (await api.listTickets()) || []
    projects.value = (await api.listProjects()) || []
    assets.value = (await api.listAssets()) || []
  } catch (e: any) {
    ElMessage.error(e.message)
  } finally {
    loading.value = false
  }
}
onMounted(load)

async function submit() {
  const ports = String(form.value.ports).split(",").map((x: string) => parseInt(x.trim(), 10)).filter((n: number) => n > 0)
  try {
    await api.submitTicket({
      project_id: form.value.project_id,
      asset_ids: form.value.asset_ids,
      ports,
      reason: form.value.reason,
      duration_sec: form.value.duration_sec,
    })
    ElMessage.success(t("tickets.submitted"))
    dialog.value = false
    await load()
  } catch (e: any) {
    ElMessage.error(e.message)
  }
}

function askPassword(): Promise<string> {
  return ElMessageBox.prompt(t("tickets.stepupMsg"), t("tickets.stepupTitle"), {
    inputType: "password",
    inputPlaceholder: t("tickets.stepupPh"),
  }).then((r: any) => r.value)
}
async function doApprove(id: number) {
  try {
    await withStepUp(() => api.approve(id), askPassword)
    ElMessage.success(t("tickets.approvedOk"))
    await load()
  } catch (e: any) {
    if (e !== "cancel") ElMessage.error(e.message || t("tickets.approveFail"))
  }
}
async function doReject(id: number) {
  try {
    const r: any = await ElMessageBox.prompt(t("tickets.rejectMsg"), t("tickets.rejectTitle"))
    await api.reject(id, r.value || "rejected")
    await load()
  } catch { /* cancelled */ }
}
async function doRevoke(id: number) {
  try {
    await withStepUp(() => api.revoke(id, "manual revoke"), askPassword)
    ElMessage.success(t("tickets.revokedOk"))
    await load()
  } catch (e: any) {
    if (e !== "cancel") ElMessage.error(e.message || t("tickets.revokeFail"))
  }
}
</script>

<template>
  <div class="page">
    <div class="page-head">
      <h2 class="page-title">{{ t("tickets.title") }}</h2>
      <div class="page-actions">
        <el-button @click="load">{{ t("common.refresh") }}</el-button>
        <el-button type="primary" @click="dialog = true">{{ t("tickets.new") }}</el-button>
      </div>
    </div>

    <div class="panel">
      <el-table :data="tickets" v-loading="loading" style="width: 100%">
        <el-table-column :label="t('tickets.colId')" width="120">
          <template #default="{ row }"><span class="ys-mono">{{ row.pub_id.slice(-8) }}</span></template>
        </el-table-column>
        <el-table-column :label="t('tickets.colProject')" width="130">
          <template #default="{ row }">{{ projMap[row.project_id] || row.project_id }}</template>
        </el-table-column>
        <el-table-column :label="t('tickets.colStatus')" width="120">
          <template #default="{ row }">
            <span class="ys-status" :class="statusClass[row.status] || 'muted'" :data-status="row.status">
              <i class="ys-dot" />{{ t(`status.${row.status}`) }}
            </span>
          </template>
        </el-table-column>
        <el-table-column prop="reason" :label="t('tickets.colReason')" show-overflow-tooltip min-width="160" />
        <el-table-column :label="t('tickets.colPorts')" width="100">
          <template #default="{ row }"><span class="ys-mono ys-tabular">{{ (row.ports || []).join(",") }}</span></template>
        </el-table-column>
        <el-table-column :label="t('tickets.colExpires')" width="180">
          <template #default="{ row }">
            <span class="ys-mono ys-tabular ys-muted">{{ row.expires_at ? new Date(row.expires_at).toLocaleString() : "—" }}</span>
          </template>
        </el-table-column>
        <el-table-column :label="t('tickets.colActions')" width="280">
          <template #default="{ row }">
            <el-button v-if="isApprover && row.status === 'pending'" size="small" type="success" @click="doApprove(row.id)">{{ t("tickets.approve") }}</el-button>
            <el-button v-if="isApprover && row.status === 'pending'" size="small" @click="doReject(row.id)">{{ t("tickets.reject") }}</el-button>
            <el-button v-if="row.status === 'active'" size="small" type="primary" @click="router.push(`/tickets/${row.id}/terminal`)">{{ t("tickets.openTerminal") }}</el-button>
            <el-button v-if="row.status === 'active'" size="small" type="danger" plain @click="doRevoke(row.id)">{{ t("tickets.revoke") }}</el-button>
          </template>
        </el-table-column>
        <template #empty>
          <div class="empty">{{ t("tickets.empty") }}</div>
        </template>
      </el-table>
    </div>

    <el-dialog v-model="dialog" :title="t('tickets.dialogTitle')" width="540">
      <el-form label-width="90px">
        <el-form-item :label="t('tickets.fProject')">
          <el-select v-model="form.project_id" :placeholder="t('tickets.phProject')" style="width: 100%" @change="form.asset_ids = []">
            <el-option v-for="p in projects" :key="p.id" :label="p.code + ' · ' + p.name" :value="p.id" />
          </el-select>
        </el-form-item>
        <el-form-item :label="t('tickets.fAsset')">
          <el-select v-model="form.asset_ids" multiple :placeholder="t('tickets.phAsset')" style="width: 100%">
            <el-option v-for="a in assetsForProject" :key="a.id" :label="a.name + ' (' + a.ip_internal + ')'" :value="a.id" />
          </el-select>
        </el-form-item>
        <el-form-item :label="t('tickets.fPorts')"><el-input v-model="form.ports" :placeholder="t('tickets.phPorts')" /></el-form-item>
        <el-form-item :label="t('tickets.fDuration')"><el-input-number v-model="form.duration_sec" :min="60" :max="86400" :step="600" /></el-form-item>
        <el-form-item :label="t('tickets.fReason')"><el-input v-model="form.reason" type="textarea" :placeholder="t('tickets.phReason')" /></el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="dialog = false">{{ t("common.cancel") }}</el-button>
        <el-button type="primary" @click="submit">{{ t("common.submit") }}</el-button>
      </template>
    </el-dialog>
  </div>
</template>

<style scoped>
.page {
  max-width: 1180px;
  margin: 0 auto;
}
.page-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  margin-bottom: 18px;
}
.page-title {
  margin: 0;
  font-size: 20px;
  font-weight: 600;
  letter-spacing: -0.012em;
}
.page-actions {
  display: flex;
  gap: 8px;
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
.ys-status.warn {
  color: var(--el-color-warning);
}
.ys-status.accent {
  color: var(--ys-accent);
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
