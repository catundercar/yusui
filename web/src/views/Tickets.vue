<script setup lang="ts">
import { ref, onMounted, computed } from "vue"
import { useRouter } from "vue-router"
import { ElMessage, ElMessageBox } from "element-plus"
import { api, withStepUp } from "../api"
import { session } from "../auth"

const router = useRouter()
const tickets = ref<any[]>([])
const projects = ref<any[]>([])
const assets = ref<any[]>([])
const loading = ref(false)
const dialog = ref(false)
const form = ref<any>({ project_id: null, asset_ids: [], ports: "22", duration_sec: 3600, reason: "" })

const projMap = computed(() => Object.fromEntries(projects.value.map((p) => [p.id, p.code])))
const assetsForProject = computed(() => assets.value.filter((a) => a.project_id === form.value.project_id))
const isApprover = computed(() => ["approver", "admin"].includes(session.user?.role || ""))

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

function tagType(s: string) {
  return ({ pending: "warning", approved: "warning", active: "success", revoking: "warning", closed: "info" } as any)[s] || "danger"
}

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
    ElMessage.success("工单已提交")
    dialog.value = false
    await load()
  } catch (e: any) {
    ElMessage.error(e.message)
  }
}

function askPassword(): Promise<string> {
  return ElMessageBox.prompt("敏感操作需重新输入密码确认", "二次认证", { inputType: "password", inputPlaceholder: "密码" }).then((r: any) => r.value)
}
async function doApprove(id: number) {
  try {
    await withStepUp(() => api.approve(id), askPassword)
    ElMessage.success("已审批并放行")
    await load()
  } catch (e: any) {
    if (e !== "cancel") ElMessage.error(e.message || "审批失败")
  }
}
async function doReject(id: number) {
  try {
    const r: any = await ElMessageBox.prompt("驳回理由", "驳回工单")
    await api.reject(id, r.value || "rejected")
    await load()
  } catch { /* cancelled */ }
}
async function doRevoke(id: number) {
  try {
    await withStepUp(() => api.revoke(id, "manual revoke"), askPassword)
    ElMessage.success("已撤销")
    await load()
  } catch (e: any) {
    if (e !== "cancel") ElMessage.error(e.message || "撤销失败")
  }
}
</script>

<template>
  <div>
    <div style="display: flex; justify-content: space-between; margin-bottom: 16px">
      <h3 style="margin: 0">工单</h3>
      <div>
        <el-button @click="load">刷新</el-button>
        <el-button type="primary" @click="dialog = true">提工单</el-button>
      </div>
    </div>
    <el-table :data="tickets" v-loading="loading" border style="width: 100%">
      <el-table-column label="工单号" width="110">
        <template #default="{ row }">{{ row.pub_id.slice(-8) }}</template>
      </el-table-column>
      <el-table-column label="项目" width="120">
        <template #default="{ row }">{{ projMap[row.project_id] || row.project_id }}</template>
      </el-table-column>
      <el-table-column label="状态" width="100">
        <template #default="{ row }"><el-tag :type="tagType(row.status)">{{ row.status }}</el-tag></template>
      </el-table-column>
      <el-table-column prop="reason" label="原因" show-overflow-tooltip />
      <el-table-column label="端口" width="90">
        <template #default="{ row }">{{ (row.ports || []).join(",") }}</template>
      </el-table-column>
      <el-table-column label="到期" width="180">
        <template #default="{ row }">{{ row.expires_at ? new Date(row.expires_at).toLocaleString() : "-" }}</template>
      </el-table-column>
      <el-table-column label="操作" width="300">
        <template #default="{ row }">
          <el-button v-if="isApprover && row.status === 'pending'" size="small" type="success" @click="doApprove(row.id)">审批</el-button>
          <el-button v-if="isApprover && row.status === 'pending'" size="small" @click="doReject(row.id)">驳回</el-button>
          <el-button v-if="row.status === 'active'" size="small" type="primary" @click="router.push(`/tickets/${row.id}/terminal`)">打开终端</el-button>
          <el-button v-if="row.status === 'active'" size="small" type="danger" @click="doRevoke(row.id)">撤销</el-button>
        </template>
      </el-table-column>
    </el-table>

    <el-dialog v-model="dialog" title="提交访问工单" width="540">
      <el-form label-width="90px">
        <el-form-item label="项目">
          <el-select v-model="form.project_id" placeholder="选择项目" style="width: 100%" @change="form.asset_ids = []">
            <el-option v-for="p in projects" :key="p.id" :label="p.code + ' · ' + p.name" :value="p.id" />
          </el-select>
        </el-form-item>
        <el-form-item label="资产">
          <el-select v-model="form.asset_ids" multiple placeholder="选择资产" style="width: 100%">
            <el-option v-for="a in assetsForProject" :key="a.id" :label="a.name + ' (' + a.ip_internal + ')'" :value="a.id" />
          </el-select>
        </el-form-item>
        <el-form-item label="端口"><el-input v-model="form.ports" placeholder="22,3306" /></el-form-item>
        <el-form-item label="时长(秒)"><el-input-number v-model="form.duration_sec" :min="60" :max="86400" :step="600" /></el-form-item>
        <el-form-item label="原因"><el-input v-model="form.reason" type="textarea" placeholder="访问原因" /></el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="dialog = false">取消</el-button>
        <el-button type="primary" @click="submit">提交</el-button>
      </template>
    </el-dialog>
  </div>
</template>
