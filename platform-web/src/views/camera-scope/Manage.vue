<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import {
  ElButton,
  ElDialog,
  ElEmpty,
  ElForm,
  ElFormItem,
  ElInput,
  ElMessage,
  ElMessageBox,
  ElPagination,
  ElTable,
  ElTableColumn,
  ElTag,
  vLoading,
} from 'element-plus'
import * as api from '../../api/camera-scopes/api'
import type { CameraScope, ScopeUser } from '../../api/camera-scopes/types'
import TablePanel from '../../components/table/TablePanel.vue'
import TableActions from '../../components/table/TableActions.vue'
import RequestError from '../../components/feedback/RequestError.vue'
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
  deviceIds = ref<string[]>([])
const dirty = computed(
  () =>
    scope.value?.mode === 'CUSTOM' &&
    (!sameIds(groupIds.value, scope.value.groupIds) ||
      !sameIds(cameraIds.value, scope.value.cameraIds) ||
      !sameIds(deviceIds.value, scope.value.deviceIds ?? [])),
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
  deviceIds.value = [...(result.deviceIds ?? [])]
}
function resetDraft() {
  if (!scope.value || saving.value) return
  groupIds.value = [...scope.value.groupIds]
  cameraIds.value = [...scope.value.cameraIds]
  deviceIds.value = [...(scope.value.deviceIds ?? [])]
}
function clearAllSelected() {
  if (saving.value) return
  groupIds.value = []
  cameraIds.value = []
  deviceIds.value = []
}
async function open(user: ScopeUser) {
  if (saving.value) return
  const current = ++detailSequence,
    active = captureScope()
  target.value = user
  scope.value = null
  formError.value = null
  detailLoading.value = true
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
      deviceIds: [...deviceIds.value],
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
onMounted(load)
</script>

<template>
  <div class="content-page">
    <ElForm class="search-form scope-search-form" inline @submit.prevent="search">
      <ElFormItem label="账户">
        <ElInput v-model="name" class="search-field" clearable placeholder="账号、昵称或部门" />
      </ElFormItem>
      <ElFormItem>
        <ElButton type="primary" native-type="submit" :loading="loading">查询</ElButton>
        <ElButton @click="resetSearch">重置</ElButton>
      </ElFormItem>
    </ElForm>
    <RequestError :error="error" />
    <TablePanel :initial-loading="loading && !rows.length">
      <template #tools>
        <ElButton :loading="loading" @click="load">刷新</ElButton>
      </template>
      <ElTable
        class="scope-user-table"
        v-loading="loading"
        :data="rows"
        row-key="userId"
        border
        empty-text="暂无账户"
      >
        <template #empty>
          <ElEmpty
            :image-size="60"
            :description="error ? '账户列表读取失败' : name ? '未找到匹配的账户' : '暂无账户'"
          >
            <ElButton v-if="error" @click="load">重新加载</ElButton>
            <ElButton v-else-if="name" @click="resetSearch">重置筛选</ElButton>
          </ElEmpty>
        </template>
        <ElTableColumn prop="username" label="账号" min-width="160">
          <template #default="{ row }">
            <span class="scope-user-account">{{ row.username }}</span>
          </template>
        </ElTableColumn>
        <ElTableColumn prop="nickname" label="昵称" min-width="150">
          <template #default="{ row }">
            <span>{{ row.nickname || '—' }}</span>
          </template>
        </ElTableColumn>
        <ElTableColumn label="所属部门" min-width="220">
          <template #default="{ row }">
            <div class="scope-departments">
              <ElTag
                v-for="department in row.departments"
                :key="department.id"
                type="info"
                effect="plain"
                :title="department.name"
                >{{ department.name }}</ElTag
              >
              <span v-if="!row.departments.length" class="table-secondary">未关联部门</span>
            </div>
          </template>
        </ElTableColumn>
        <ElTableColumn label="账号状态" min-width="120">
          <template #default="{ row }">
            <ElTag :type="row.enabled ? 'success' : 'info'" effect="plain">
              {{ row.enabled ? '已启用' : '已停用' }}
            </ElTag>
          </template>
        </ElTableColumn>
        <ElTableColumn label="相机范围" min-width="140">
          <template #default="{ row }">
            <ElTag :type="row.isSuperAdmin ? 'warning' : 'primary'" effect="plain">
              {{ row.isSuperAdmin ? '全部资源' : '按账户授权' }}
            </ElTag>
          </template>
        </ElTableColumn>
        <ElTableColumn
          label="操作"
          min-width="144"
          class-name="table-actions-column"
          :resizable="false"
        >
          <template #default="{ row }">
            <TableActions compact>
              <ElButton link type="primary" @click="open(asUser(row))">
                {{ row.isSuperAdmin ? '查看范围' : '配置范围' }}
              </ElButton>
            </TableActions>
          </template>
        </ElTableColumn>
      </ElTable>
      <div class="scope-user-cards" v-loading="loading">
        <article v-for="user in rows" :key="user.userId" class="scope-user-card">
          <header>
            <strong>{{ user.nickname || user.username }}</strong>
            <ElTag :type="user.enabled ? 'success' : 'info'" effect="plain">
              {{ user.enabled ? '已启用' : '已停用' }}
            </ElTag>
          </header>
          <span class="table-secondary">{{ user.username }}</span>
          <div class="scope-departments">
            <ElTag
              v-for="department in user.departments"
              :key="department.id"
              type="info"
              effect="plain"
              :title="department.name"
              >{{ department.name }}</ElTag
            >
            <span v-if="!user.departments.length" class="table-secondary">未关联部门</span>
          </div>
          <footer>
            <ElTag :type="user.isSuperAdmin ? 'warning' : 'primary'" effect="plain">
              {{ user.isSuperAdmin ? '全部资源' : '按账户授权' }}
            </ElTag>
            <ElButton link type="primary" @click="open(user)">
              {{ user.isSuperAdmin ? '查看范围' : '配置范围' }}
            </ElButton>
          </footer>
        </article>
        <p v-if="!loading && !rows.length">暂无账户</p>
      </div>
      <template #footer>
        <ElPagination
          v-model:current-page="page"
          :page-size="20"
          :total="total"
          layout="total, prev, pager, next"
          @current-change="load"
        />
      </template>
    </TablePanel>
    <ElDialog
      class="management-dialog camera-scope-dialog"
      v-model="dialog"
      :title="`相机范围 · ${target?.nickname || target?.username || ''}`"
      width="900px"
      destroy-on-close
      :close-on-click-modal="!saving"
      :close-on-press-escape="!saving"
      :show-close="!saving"
      :before-close="close"
      @closed="clearDialog"
    >
      <RequestError :error="formError" />
      <div v-if="target" class="scope-header-card">
        <div class="scope-account">
          <div class="scope-account__identity">
            <strong class="scope-account__name">{{ target.nickname || target.username }}</strong>
            <span class="scope-account__username">{{ target.username }}</span>
            <ElTag v-if="!target.enabled" type="info" effect="plain">账号已停用</ElTag>
          </div>
          <div class="scope-departments">
            <ElTag
              v-for="department in target.departments"
              :key="department.id"
              type="info"
              effect="plain"
              >{{ department.name }}</ElTag
            >
            <span v-if="!target.departments.length" class="table-secondary">未关联部门</span>
          </div>
        </div>
        <div
          v-if="scope"
          class="scope-summary"
          :title="`统计时间：${formatDateTime(scope.effectiveSummary.computedAt)}`"
        >
          <ElTag v-if="scope.mode === 'ALL'" type="warning" effect="plain">全部相机</ElTag>
          <span class="scope-summary__metric">
            已授权 <strong>{{ scope.effectiveSummary.effectiveDeviceCount ?? '—' }}</strong> 台相机
          </span>
          <span class="scope-summary__metric">
            已启用 <strong>{{ scope.effectiveSummary.enabledDeviceCount ?? '—' }}</strong> 台相机
          </span>
          <template v-if="scope.mode === 'CUSTOM'">
            <ElButton
              v-if="groupIds.length || cameraIds.length || deviceIds.length"
              link
              type="danger"
              :disabled="saving"
              @click="clearAllSelected"
              >清空已选</ElButton
            >
            <ElButton v-if="dirty" link type="primary" :disabled="saving" @click="resetDraft"
              >还原修改</ElButton
            >
          </template>
        </div>
      </div>
      <div v-loading="detailLoading" class="scope-detail">
        <ElButton v-if="!scope && formError && target" link type="primary" @click="open(target)"
          >重新读取授权</ElButton
        >
        <section v-if="scope" class="scope-tree-panel" aria-label="选择授权相机">
          <GroupGrantTree
            :key="scope.userId"
            v-model="groupIds"
            v-model:camera-ids="cameraIds"
            v-model:device-ids="deviceIds"
            :initial-cameras="[...scope.cameraGrants, ...(scope.deviceGrants ?? [])]"
            :all-mode="scope.mode === 'ALL'"
            :disabled="saving || scope.mode === 'ALL'"
          />
        </section>
      </div>
      <template #footer>
        <ElButton :disabled="saving" @click="close">关闭</ElButton>
        <ElButton
          v-if="scope?.mode === 'CUSTOM'"
          type="primary"
          :loading="saving"
          :disabled="!dirty"
          @click="save"
          >保存相机范围</ElButton
        >
      </template>
    </ElDialog>
  </div>
</template>
<style scoped>
.scope-search-form {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
}
.scope-user-account {
  font-weight: 500;
  color: var(--text-primary);
}
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
.scope-header-card {
  display: flex;
  align-items: center;
  justify-content: space-between;
  flex-wrap: wrap;
  gap: 12px 20px;
  padding: 12px 16px;
  border: 1px solid var(--border-subtle);
  border-radius: 6px;
  background: var(--surface-muted);
}
.scope-account {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 8px 14px;
  min-width: 0;
  overflow-wrap: anywhere;
}
.scope-account__identity {
  display: flex;
  align-items: center;
  gap: 8px;
}
.scope-account__name {
  font-size: 15px;
  color: var(--text-primary);
}
.scope-account__username {
  padding: 1px 8px;
  border-radius: 4px;
  background: #fff;
  border: 1px solid var(--border-subtle);
  color: var(--text-secondary);
  font-size: 12px;
}
.scope-detail {
  min-height: 180px;
  margin-top: 14px;
}
.scope-summary {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 8px 12px;
  color: var(--text-secondary);
  font-size: 13px;
}
.scope-summary__metric {
  padding: 3px 10px;
  border-radius: 4px;
  background: #fff;
  border: 1px solid var(--border-subtle);
}
.scope-summary strong {
  color: var(--text-primary);
  font-weight: 600;
}
.scope-tree-panel {
  min-width: 0;
}
:global(.el-dialog.camera-scope-dialog > .el-dialog__body) {
  max-height: min(68dvh, 680px);
  overflow: hidden;
}
.scope-user-cards {
  display: none;
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
