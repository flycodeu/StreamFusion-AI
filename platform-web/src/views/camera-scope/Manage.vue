<script setup lang="ts">
import { onMounted, ref } from 'vue'
import {
  ElAlert,
  ElButton,
  ElDialog,
  ElForm,
  ElFormItem,
  ElInput,
  ElMessage,
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
import GrantPicker from '../../features/camera-scopes/GrantPicker.vue'
import { usePageScope } from '../../composables/usePageScope'
import { formatDateTime } from '../../utils/dateTime'

defineOptions({ name: 'CameraScopeManage' })
const captureScope = usePageScope(),
  rows = ref<ScopeUser[]>([]),
  page = ref(1),
  total = ref(0),
  name = ref('')
const loading = ref(false),
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
let sequence = 0,
  detailSequence = 0
const asUser = (value: unknown) => value as ScopeUser
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
  const current = ++detailSequence,
    active = captureScope()
  error.value = null
  try {
    const result = await api.getCameraScope(user.userId)
    if (!active() || current !== detailSequence) return
    target.value = user
    apply(result)
    formError.value = null
    selectedSearch.value = ''
    tab.value = 'group'
    dialog.value = true
  } catch (cause) {
    if (active() && current === detailSequence) error.value = cause
  }
}
async function save() {
  if (saving.value || !scope.value || scope.value.mode !== 'CUSTOM') return
  const active = captureScope()
  saving.value = true
  formError.value = null
  try {
    const result = await api.updateCameraScope(scope.value.userId, {
      version: scope.value.version,
      groupIds: groupIds.value,
      cameraIds: cameraIds.value,
    })
    if (!active()) return
    apply(result)
    ElMessage.success('相机范围已保存')
  } catch (cause) {
    if (active()) formError.value = cause
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
        ><ElInput v-model="name" clearable placeholder="账号或昵称" /></ElFormItem
      ><ElFormItem
        ><ElButton type="primary" native-type="submit" :loading="loading">查询</ElButton
        ><ElButton @click="resetSearch">重置</ElButton></ElFormItem
      ></ElForm
    >
    <RequestError :error="error" />
    <TablePanel
      ><template #tools><ElButton :loading="loading" @click="load">刷新</ElButton></template>
      <ElTable v-loading="loading" :data="rows" row-key="userId" border empty-text="暂无账户">
        <ElTableColumn prop="username" label="账号" min-width="180" /><ElTableColumn
          prop="nickname"
          label="昵称"
          min-width="180"
        />
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
            ><TableActions
              ><ElButton plain type="primary" @click="open(asUser(row))">{{
                row.isSuperAdmin ? '查看范围' : '配置范围'
              }}</ElButton></TableActions
            ></template
          ></ElTableColumn
        > </ElTable
      ><template #footer
        ><ElPagination
          v-model:current-page="page"
          :page-size="20"
          :total="total"
          layout="total, prev, pager, next"
          @current-change="load"
      /></template>
    </TablePanel>
    <ElDialog
      v-model="dialog"
      :title="`相机范围 · ${target?.nickname || target?.username || ''}`"
      width="900px"
      destroy-on-close
      :close-on-click-modal="!saving"
      :close-on-press-escape="!saving"
      :show-close="!saving"
    >
      <RequestError :error="formError" />
      <template v-if="scope">
        <ElAlert
          :type="scope.mode === 'ALL' ? 'info' : 'warning'"
          :closable="false"
          :title="
            scope.mode === 'ALL'
              ? '超级管理员使用全部相机范围，不能手工裁剪；页面资格仍需有效。'
              : '分组授权包含当前及以后进入的后代相机；直接授权与分组授权取并集。'
          "
        />
        <ElAlert
          v-if="target && !target.enabled"
          type="info"
          :closable="false"
          title="此账户已停用；保存授权不会使账户恢复访问。"
        />
        <p>
          当前范围 {{ scope.effectiveSummary.effectiveCameraCount }} 路，其中通道已启用
          {{ scope.effectiveSummary.enabledCameraCount }} 路。统计于
          {{ formatDateTime(scope.effectiveSummary.computedAt) }}，不代表可播放数量。
        </p>
        <template v-if="scope.mode === 'CUSTOM'">
          <ElInput v-model="selectedSearch" clearable placeholder="筛选已选资源，便于定位和移除" />
          <section class="selected-grants">
            <strong>已授分组 {{ groupIds.length }} / 200</strong>
            <div>
              <ElTag
                v-for="id in visibleSelected(groupIds, groupNames)"
                :key="id"
                :closable="!saving"
                @close="groupIds = groupIds.filter((item) => item !== id)"
                >{{ groupNames[id] || id }}</ElTag
              >
            </div>
            <strong>直接相机 {{ cameraIds.length }} / 2000</strong>
            <div>
              <ElTag
                v-for="id in visibleSelected(cameraIds, cameraNames)"
                :key="id"
                :closable="!saving"
                @close="cameraIds = cameraIds.filter((item) => item !== id)"
                >{{ cameraNames[id] || id }}</ElTag
              >
            </div>
          </section>
          <ElTabs v-model="tab"
            ><ElTabPane label="选择分组" name="group"
              ><GrantPicker
                v-if="tab === 'group'"
                v-model="groupIds"
                kind="group"
                :disabled="saving"
                @label="(id, label) => (groupNames[id] = label)" /></ElTabPane
            ><ElTabPane label="选择单独相机" name="camera"
              ><GrantPicker
                v-if="tab === 'camera'"
                v-model="cameraIds"
                kind="camera"
                :disabled="saving"
                @label="(id, label) => (cameraNames[id] = label)" /></ElTabPane
          ></ElTabs>
        </template>
      </template>
      <template #footer
        ><ElButton :disabled="saving" @click="dialog = false">关闭</ElButton
        ><ElButton v-if="scope?.mode === 'CUSTOM'" type="primary" :loading="saving" @click="save"
          >保存相机范围</ElButton
        ></template
      >
    </ElDialog>
  </div>
</template>
<style scoped>
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
</style>
