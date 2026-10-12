<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import {
  ElButton,
  ElDialog,
  ElForm,
  ElFormItem,
  ElInput,
  ElMessage,
  ElMessageBox,
  ElPagination,
  ElTable,
  ElTableColumn,
  ElTag,
  ElTabs,
  ElTabPane,
  vLoading,
} from 'element-plus'
import * as api from '../../api/camera-scopes/api'
import type { CameraScope, ScopeUser } from '../../api/camera-scopes/types'
import TablePanel from '../../components/table/TablePanel.vue'
import TableActions from '../../components/table/TableActions.vue'
import RequestError from '../../components/feedback/RequestError.vue'
import CameraGrantPicker from '../../features/camera-scopes/CameraGrantPicker.vue'
import GroupGrantTree from '../../features/camera-scopes/GroupGrantTree.vue'
import { usePageScope } from '../../composables/usePageScope'
import { formatDateTime } from '../../utils/dateTime'

defineOptions({ name: 'CameraScopeManage' })
const captureScope = usePageScope(),
  rows = ref<ScopeUser[]>([]),
  page = ref(1),
  total = ref(0),
  name = ref('')
const loading = ref(false),
  detailLoading = ref(false),
  saving = ref(false),
  error = ref<unknown>(null),
  formError = ref<unknown>(null)
const dialog = ref(false),
  target = ref<ScopeUser | null>(null),
  scope = ref<CameraScope | null>(null)
const groupIds = ref<string[]>([]),
  cameraIds = ref<string[]>([]),
  groupNames = ref<Record<string, string>>({}),
  cameraNames = ref<Record<string, string>>({})
const tab = ref('group'),
  selectedSearch = ref('')
const dirty = computed(
  () =>
    scope.value?.mode === 'CUSTOM' &&
    (!sameIds(groupIds.value, scope.value.groupIds) ||
      !sameIds(cameraIds.value, scope.value.cameraIds)),
)
let sequence = 0,
  detailSequence = 0
const asUser = (value: unknown) => value as ScopeUser
function sameIds(left: string[], right: string[]) {
  const ids = new Set(right)
  return left.length === right.length && left.every((id) => ids.has(id))
}
async function load() {
  const current = ++sequence,
    active = captureScope()
  loading.value = true
  error.value = null
  try {
    const result = await api.getScopeUsers({
      page: page.value,
      size: 20,
      name: name.value.trim() || undefined,
    })
    if (active() && current === sequence) {
      rows.value = result.items
      total.value = result.total
    }
  } catch (cause) {
    if (active() && current === sequence) error.value = cause
  } finally {
    if (active() && current === sequence) loading.value = false
  }
}
function resetSearch() {
  name.value = ''
  search()
}
function search() {
  page.value = 1
  void load()
}
function apply(result: CameraScope) {
  scope.value = result
  groupIds.value = [...result.groupIds]
  cameraIds.value = [...result.cameraIds]
  groupNames.value = Object.fromEntries(
    result.groupGrants.map((row) => [row.groupId, row.path || row.name]),
  )
  cameraNames.value = Object.fromEntries(
    result.cameraGrants.map((row) => [
      row.cameraId,
      `${row.path ? `${row.path} / ` : ''}${row.name}${row.lifecycle === 'DISABLED' ? '（已停用，保留授权）' : ''}`,
    ]),
  )
}
async function open(user: ScopeUser) {
  if (saving.value) return
  const current = ++detailSequence,
    active = captureScope()
  target.value = user
  scope.value = null
  formError.value = null
  detailLoading.value = true
  selectedSearch.value = ''
  tab.value = 'group'
  dialog.value = true
  try {
    const result = await api.getCameraScope(user.userId)
    if (!active() || current !== detailSequence || !dialog.value) return
    apply(result)
  } catch (cause) {
    if (active() && current === detailSequence && dialog.value) formError.value = cause
  } finally {
    if (active() && current === detailSequence) detailLoading.value = false
  }
}
async function close() {
  if (saving.value) return
  const active = captureScope(),
    current = detailSequence
  if (dirty.value) {
    try {
      await ElMessageBox.confirm('关闭将放弃未保存的授权修改。', '放弃修改？', { type: 'warning' })
    } catch {
      return
    }
  }
  if (active() && current === detailSequence) {
    dialog.value = false
    detailSequence++
    detailLoading.value = false
  }
}
function clearDialog() {
  if (!dialog.value) {
    scope.value = null
    target.value = null
  }
}
async function save() {
  if (saving.value || !scope.value || !dirty.value) return
  const active = captureScope(),
    current = detailSequence,
    userId = scope.value.userId
  saving.value = true
  formError.value = null
  try {
    const result = await api.updateCameraScope(userId, {
      version: scope.value.version,
      groupIds: [...groupIds.value],
      cameraIds: [...cameraIds.value],
    })
    if (!active() || current !== detailSequence || !dialog.value || scope.value?.userId !== userId)
      return
    apply(result)
    ElMessage.success('相机范围已保存')
  } catch (cause) {
    if (active() && current === detailSequence) formError.value = cause
  } finally {
    saving.value = false
  }
}
function visibleSelected(ids: string[], labels: Record<string, string>) {
  const keyword = selectedSearch.value.trim().toLocaleLowerCase()
  return ids.filter(
    (id) => !keyword || `${labels[id] || ''} ${id}`.toLocaleLowerCase().includes(keyword),
  )
}
onMounted(load)
</script>

<template>
  <div class="content-page">
    <ElForm class="search-form" inline @submit.prevent="search"
      ><ElFormItem label="账户"
        ><ElInput
          v-model="name"
          class="search-field"
          clearable
          placeholder="账号、昵称或部门" /></ElFormItem
      ><ElFormItem
        ><ElButton type="primary" native-type="submit" :loading="loading">查询</ElButton
        ><ElButton @click="resetSearch">重置</ElButton></ElFormItem
      ></ElForm
    >
    <RequestError :error="error" />
    <TablePanel
      ><template #tools><ElButton :loading="loading" @click="load">刷新</ElButton></template>
      <ElTable
        class="scope-user-table"
        v-loading="loading"
        :data="rows"
        row-key="userId"
        border
        empty-text="暂无账户"
      >
        <ElTableColumn prop="username" label="账号" min-width="180" /><ElTableColumn
          prop="nickname"
          label="昵称"
          min-width="180"
        />
        <ElTableColumn label="所属部门" min-width="220">
          <template #default="{ row }">
            <div class="scope-departments">
              <ElTag
                v-for="department in row.departments"
                :key="department.id"
                type="info"
                :title="department.name"
                >{{ department.name }}</ElTag
              >
              <span v-if="!row.departments.length" class="table-secondary">未关联部门</span>
            </div>
          </template>
        </ElTableColumn>
        <ElTableColumn label="账号状态" min-width="120"
          ><template #default="{ row }">{{
            row.enabled ? '已启用' : '已停用'
          }}</template></ElTableColumn
        >
        <ElTableColumn label="相机范围" min-width="140"
          ><template #default="{ row }">{{
            row.isSuperAdmin ? '全部资源' : '按账户授权'
          }}</template></ElTableColumn
        >
        <ElTableColumn
          label="操作"
          min-width="144"
          class-name="table-actions-column"
          :resizable="false"
          ><template #default="{ row }"
            ><TableActions compact
              ><ElButton link type="primary" @click="open(asUser(row))">{{
                row.isSuperAdmin ? '查看范围' : '配置范围'
              }}</ElButton></TableActions
            ></template
          ></ElTableColumn
        >
      </ElTable>
      <div class="scope-user-cards" v-loading="loading">
        <article v-for="user in rows" :key="user.userId" class="scope-user-card">
          <header>
            <strong>{{ user.nickname || user.username }}</strong
            ><ElTag v-if="!user.enabled" type="info">已停用</ElTag>
          </header>
          <span class="table-secondary">{{ user.username }}</span>
          <div class="scope-departments">
            <ElTag
              v-for="department in user.departments"
              :key="department.id"
              type="info"
              :title="department.name"
              >{{ department.name }}</ElTag
            >
            <span v-if="!user.departments.length" class="table-secondary">未关联部门</span>
          </div>
          <footer>
            <span>{{ user.isSuperAdmin ? '全部资源' : '按账户授权' }}</span
            ><ElButton link type="primary" @click="open(user)">{{
              user.isSuperAdmin ? '查看范围' : '配置范围'
            }}</ElButton>
          </footer>
        </article>
        <p v-if="!loading && !rows.length">暂无账户</p>
      </div>
      <template #footer
        ><ElPagination
          v-model:current-page="page"
          :page-size="20"
          :total="total"
          layout="total, prev, pager, next"
          @current-change="load"
      /></template>
    </TablePanel>
    <ElDialog
      class="management-dialog camera-scope-dialog"
      v-model="dialog"
      :title="`相机范围 · ${target?.nickname || target?.username || ''}`"
      width="1100px"
      destroy-on-close
      :close-on-click-modal="!saving"
      :close-on-press-escape="!saving"
      :show-close="!saving"
      :before-close="close"
      @closed="clearDialog"
    >
      <RequestError :error="formError" />
      <div v-if="target" class="scope-account">
        <span>{{ target.username }}</span>
        <div class="scope-departments">
          <ElTag v-for="department in target.departments" :key="department.id" type="info">{{
            department.name
          }}</ElTag>
          <span v-if="!target.departments.length" class="table-secondary">未关联部门</span>
        </div>
        <ElTag v-if="!target.enabled" type="info">账号已停用</ElTag>
      </div>
      <div v-loading="detailLoading" class="scope-detail">
        <ElButton v-if="!scope && formError && target" link @click="open(target)"
          >重新读取授权</ElButton
        >
        <template v-if="scope">
          <div
            class="scope-summary"
            :title="`统计时间：${formatDateTime(scope.effectiveSummary.computedAt)}`"
          >
            <ElTag v-if="scope.mode === 'ALL'">全部相机</ElTag>
            <span
              >已授权
              <strong>{{ scope.effectiveSummary.effectiveCameraCount }}</strong> 个通道</span
            >
            <span
              >已启用 <strong>{{ scope.effectiveSummary.enabledCameraCount }}</strong> 个通道</span
            >
          </div>
          <div v-if="scope.mode === 'CUSTOM'" class="scope-columns">
            <aside class="selected-resources">
              <h2>已选资源</h2>
              <ElInput v-model="selectedSearch" clearable placeholder="筛选已选资源" />
              <section class="selected-grants">
                <strong>已授分组 {{ groupIds.length }} / 200</strong>
                <div>
                  <ElTag
                    v-for="id in visibleSelected(groupIds, groupNames)"
                    :key="id"
                    :title="groupNames[id] || id"
                    :closable="!saving"
                    @close="groupIds = groupIds.filter((item) => item !== id)"
                    >{{ groupNames[id] || id }}</ElTag
                  >
                </div>
                <strong>直接通道 {{ cameraIds.length }} / 2000</strong>
                <div>
                  <ElTag
                    v-for="id in visibleSelected(cameraIds, cameraNames)"
                    :key="id"
                    :title="cameraNames[id] || id"
                    :closable="!saving"
                    @close="cameraIds = cameraIds.filter((item) => item !== id)"
                    >{{ cameraNames[id] || id }}</ElTag
                  >
                </div>
              </section>
            </aside>
            <section class="scope-candidates" aria-label="选择授权资源">
              <ElTabs v-model="tab"
                ><ElTabPane label="选择分组" name="group"
                  ><GroupGrantTree
                    v-if="tab === 'group'"
                    v-model="groupIds"
                    :disabled="saving"
                    @label="(id, label) => (groupNames[id] = label)" /></ElTabPane
                ><ElTabPane label="选择单独通道" name="camera"
                  ><CameraGrantPicker
                    v-if="tab === 'camera'"
                    v-model="cameraIds"
                    :disabled="saving"
                    @label="(id, label) => (cameraNames[id] = label)" /></ElTabPane
              ></ElTabs>
            </section>
          </div>
        </template>
      </div>
      <template #footer
        ><ElButton :disabled="saving" @click="close">关闭</ElButton
        ><ElButton
          v-if="scope?.mode === 'CUSTOM'"
          type="primary"
          :loading="saving"
          :disabled="!dirty"
          @click="save"
          >保存相机范围</ElButton
        ></template
      >
    </ElDialog>
  </div>
</template>
<style scoped>
.scope-departments {
  display: flex;
  flex-wrap: wrap;
  gap: 6px;
  min-width: 0;
}
.scope-departments :deep(.el-tag) {
  max-width: 100%;
}
.scope-departments :deep(.el-tag__content) {
  overflow: hidden;
  text-overflow: ellipsis;
}
.scope-account {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 8px 16px;
  padding-bottom: 16px;
  border-bottom: 1px solid var(--border-subtle);
  overflow-wrap: anywhere;
}
.scope-detail {
  min-height: 100px;
}
.scope-summary {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 8px 20px;
  margin-top: 16px;
  color: var(--text-secondary);
}
.scope-summary strong {
  color: var(--text-primary);
}
.scope-user-cards {
  display: none;
}
.scope-columns {
  display: grid;
  grid-template-columns: minmax(240px, 1fr) minmax(0, 2fr);
  gap: 24px;
  margin-top: 24px;
}
.selected-resources {
  min-width: 0;
  padding: 16px;
  border: 1px solid var(--border-subtle);
  border-radius: 4px;
  background: var(--surface-muted);
}
.selected-resources h2 {
  font-size: 15px;
  margin: 0 0 16px;
}
.scope-candidates {
  min-width: 0;
}
.selected-grants {
  display: grid;
  gap: 10px;
  margin: 16px 0;
}
.selected-grants > div {
  display: flex;
  flex-wrap: wrap;
  gap: 6px;
  max-height: 180px;
  overflow: auto;
}
.selected-grants :deep(.el-tag) {
  max-width: 100%;
}
.selected-grants :deep(.el-tag__content) {
  overflow: hidden;
  text-overflow: ellipsis;
}
@media (max-width: 800px) {
  .scope-columns {
    grid-template-columns: minmax(0, 1fr);
    gap: 16px;
  }
}
@media (max-width: 680px) {
  .scope-user-table {
    display: none;
  }
  .scope-user-cards {
    display: grid;
    gap: 12px;
  }
  .scope-user-card {
    display: grid;
    gap: 12px;
    padding: 16px;
    border: 1px solid var(--border-subtle);
    border-radius: 6px;
    overflow-wrap: anywhere;
  }
  .scope-user-card header,
  .scope-user-card footer {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 12px;
  }
}
</style>
