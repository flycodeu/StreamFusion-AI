<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import {
  ElButton,
  ElCheckbox,
  ElForm,
  ElFormItem,
  ElInput,
  ElMessageBox,
  ElOption,
  ElPagination,
  ElSelect,
  ElTable,
  ElTableColumn,
  ElTag,
  vLoading,
} from 'element-plus'
import * as api from '../../api/camera/api'
import type { CameraDeviceGroup, Lifecycle } from '../../api/camera/types'
import RequestError from '../../components/feedback/RequestError.vue'
import TablePanel from '../../components/table/TablePanel.vue'
import TableActions from '../../components/table/TableActions.vue'
import ColumnPicker from '../../components/table/ColumnPicker.vue'
import { useColumns } from '../../composables/table/useColumns'
import { usePageScope } from '../../composables/usePageScope'
import { connectionLabel, lifecycleLabels } from '../../features/camera/form'
import CreateDialog from '../../features/camera/CreateDialog.vue'
import ManualCreateDialog from '../../features/camera/ManualCreateDialog.vue'
import ScanDialog from '../../features/camera-access/ScanDialog.vue'
import ImportTasksDialog from '../../features/camera-access/ImportTasksDialog.vue'
import DeviceDetailPanel from '../../features/camera/DeviceDetailPanel.vue'
import GroupTree from '../../features/camera-groups/GroupTree.vue'
import GroupEditor from '../../features/camera-groups/GroupEditor.vue'
import DeviceMoveDialog from '../../features/camera/DeviceMoveDialog.vue'
import DeviceEditor from '../../features/camera/DeviceEditor.vue'
import { getGroupTree, deleteGroup } from '../../api/camera-groups/api'
import type { CameraGroup } from '../../api/camera-groups/types'

defineOptions({ name: 'CameraManage' })
const captureScope = usePageScope(),
  rows = ref<CameraDeviceGroup[]>([]),
  page = ref(1),
  total = ref(0),
  name = ref(''),
  lifecycle = ref<Lifecycle | null>(null)
const loading = ref(false),
  error = ref<unknown>(null),
  canManage = ref(false)
const groupId = ref<string | null>(null),
  includeDescendants = ref(true)
const groups = ref<CameraGroup[]>([]),
  treeLoading = ref(false),
  treeError = ref<unknown>(null),
  treeOpen = ref(false)
const pendingOnly = ref(false),
  movingDevice = ref<CameraDeviceGroup | null>(null)
const groupEditor = ref<{ group: CameraGroup | null; parentId: string | null } | null>(null),
  groupDeleting = ref(false)
const selectedGroup = computed(() => groups.value.find((g) => g.groupId === groupId.value))
const groupPath = computed(() => {
  if (pendingOnly.value) return '待归档'
  const names: string[] = []
  let group = selectedGroup.value
  for (let depth = 0; group && depth < 16; depth++) {
    names.unshift(group.name)
    group = groups.value.find((g) => g.groupId === group?.parentId)
  }
  return names.join(' / ') || '全部相机'
})
const creating = ref<'manual' | 'scan' | 'platform' | 'rtsp' | null>(null),
  importTasksOpen = ref(false),
  storageReady = ref(true)
const selectedDevice = ref<CameraDeviceGroup | null>(null)
const editingDevice = ref<CameraDeviceGroup | null>(null)
async function deviceSaved(name: string) {
  if (selectedDevice.value?.groupKey === editingDevice.value?.groupKey && selectedDevice.value)
    selectedDevice.value = { ...selectedDevice.value, name }
  editingDevice.value = null
  await refresh()
}
const columns = useColumns('camera-device-workspace', [
  'name',
  'group',
  'channels',
  'source',
  'lifecycle',
])
let sequence = 0,
  treeSequence = 0
const asDevice = (value: unknown) => value as CameraDeviceGroup
function query(): api.CameraSearch {
  return {
    page: page.value,
    size: 20,
    name: name.value.trim() || undefined,
    lifecycle: pendingOnly.value ? 'PENDING_ASSIGNMENT' : lifecycle.value || undefined,
    groupId: groupId.value || undefined,
    includeDescendants: includeDescendants.value,
  }
}
async function load() {
  const current = ++sequence,
    active = captureScope()
  loading.value = true
  error.value = null
  const requestQuery = query()
  try {
    const [result, options] = await Promise.all([
      api.getCameraDevices(requestQuery),
      api.getCameraOptions(),
    ])
    if (!active() || current !== sequence) return
    const lastPage = Math.max(1, Math.ceil(result.total / 20))
    if (page.value > lastPage) {
      page.value = lastPage
      await load()
      return
    }
    rows.value = result.items
    if (selectedDevice.value) {
      selectedDevice.value =
        result.items.find((item) => item.groupKey === selectedDevice.value?.groupKey) ??
        selectedDevice.value
    }
    total.value = result.total
    canManage.value = options.canManageShared
    storageReady.value = options.manualStorageReady
  } catch (cause) {
    if (active() && current === sequence) error.value = cause
  } finally {
    if (active() && current === sequence) loading.value = false
  }
}
function resetSearch() {
  name.value = ''
  lifecycle.value = null
  groupId.value = null
  pendingOnly.value = false
  includeDescendants.value = true
  search()
}
function search() {
  page.value = 1
  void load()
}
async function created() {
  creating.value = null
  await refresh()
}
function closeImportTasks() {
  importTasksOpen.value = false
  void refresh()
}
async function loadTree() {
  const active = captureScope(),
    current = ++treeSequence
  treeLoading.value = true
  treeError.value = null
  try {
    const result = await getGroupTree()
    if (!active() || current !== treeSequence) return
    groups.value = result
    if (groupId.value && !result.some((g) => g.groupId === groupId.value)) groupId.value = null
  } catch (cause) {
    if (active() && current === treeSequence) treeError.value = cause
  } finally {
    if (active() && current === treeSequence) treeLoading.value = false
  }
}
async function refresh() {
  const active = captureScope()
  await loadTree()
  if (active()) await load()
}
function selectGroup(id: string | null) {
  groupId.value = id
  pendingOnly.value = false
  treeOpen.value = false
  search()
}
function selectPending() {
  groupId.value = null
  pendingOnly.value = true
  treeOpen.value = false
  search()
}
async function groupsChanged() {
  groupEditor.value = null
  await refresh()
}
async function moved() {
  movingDevice.value = null
  selectedDevice.value = null
  await refresh()
}
function groupAction(action: 'create' | 'edit' | 'delete', group: CameraGroup) {
  if (!canManage.value || groupDeleting.value) return
  if (action === 'delete') void removeGroup(group)
  else
    groupEditor.value =
      action === 'create'
        ? { group: null, parentId: group.groupId }
        : { group, parentId: group.parentId }
}
async function removeGroup(group: CameraGroup) {
  if (!group || groupDeleting.value || !canManage.value) return
  const active = captureScope()
  groupDeleting.value = true
  try {
    await ElMessageBox.confirm(
      `删除分组“${group.name}”？有子组、通道或授权引用时不能删除。`,
      '确认删除分组',
      { type: 'warning' },
    )
    if (!active()) return
    await deleteGroup(group)
    if (active()) await refresh()
  } catch (cause) {
    if (active() && cause !== 'cancel' && cause !== 'close') treeError.value = cause
  } finally {
    if (active()) groupDeleting.value = false
  }
}
onMounted(refresh)
</script>
<template>
  <div class="content-page camera-workspace">
    <ElButton class="mobile-group-toggle" :aria-expanded="treeOpen" @click="treeOpen = !treeOpen"
      >分组：{{ groupPath }}</ElButton
    >
    <aside
      v-loading="treeLoading"
      class="camera-group-pane"
      :class="{ 'is-open': treeOpen }"
      aria-label="视频分组"
    >
      <header class="group-pane-header">
        <h2>视频分组</h2>
        <ElButton
          v-if="canManage"
          link
          type="primary"
          @click="groupEditor = { group: null, parentId: null }"
          >新建</ElButton
        >
      </header>
      <RequestError :error="treeError" />
      <ElButton v-if="treeError" link @click="loadTree">重新加载分组</ElButton>
      <GroupTree
        :model-value="groupId"
        :groups="groups"
        show-pending
        :pending="pendingOnly"
        :manageable="canManage"
        :disabled="groupDeleting"
        @action="groupAction"
        @update:model-value="selectGroup"
        @pending="selectPending"
        @all="selectGroup(null)"
      />
    </aside>
    <section class="camera-list-pane" aria-label="相机管理">
      <header class="camera-list-heading">
        <div>
          <h2>相机管理</h2>
          <p>{{ groupPath }}</p>
        </div>
        <span>{{ total }} 台相机</span>
      </header>
      <ElForm class="search-form camera-search-form" inline @submit.prevent="search"
        ><ElFormItem label="设备 / 通道"
          ><ElInput
            v-model="name"
            class="search-field"
            placeholder="搜索设备或通道名称"
            clearable
            maxlength="128"
        /></ElFormItem>
        <ElFormItem v-if="groupId"
          ><ElCheckbox v-model="includeDescendants" @change="search"
            >包含下级</ElCheckbox
          ></ElFormItem
        >
        <ElFormItem label="配置状态"
          ><ElSelect
            v-model="lifecycle"
            class="status-field"
            clearable
            placeholder="全部状态"
            :disabled="pendingOnly"
            ><ElOption
              v-for="(label, value) in lifecycleLabels"
              :key="value"
              :value="value"
              :label="label" /></ElSelect></ElFormItem
        ><ElFormItem
          ><ElButton type="primary" native-type="submit" :loading="loading">查询</ElButton
          ><ElButton @click="resetSearch">重置</ElButton></ElFormItem
        ></ElForm
      >
      <RequestError :error="error" />
      <TablePanel>
        <template #actions
          ><template v-if="canManage"
            ><ElButton type="primary" @click="creating = 'manual'">手动添加</ElButton
            ><ElButton @click="creating = 'scan'">网段搜索</ElButton
            ><ElButton @click="creating = 'platform'">平台导入</ElButton
            ><ElButton @click="importTasksOpen = true">导入任务</ElButton></template
          ></template
        >
        <template #tools
          ><ElButton :loading="loading || treeLoading" @click="refresh">刷新</ElButton
          ><span class="desktop-column-picker"
            ><ColumnPicker
              v-model="columns"
              :options="[
                { key: 'name', label: '相机设备' },
                { key: 'group', label: '所属分组' },
                { key: 'channels', label: '通道数量' },
                { key: 'source', label: '接入来源' },
                { key: 'lifecycle', label: '配置状态' },
              ]" /></span
        ></template>
        <ElTable
          class="device-table"
          v-loading="loading"
          :data="rows"
          row-key="groupKey"
          border
          empty-text="暂无可访问设备"
        >
          <ElTableColumn v-if="columns.includes('name')" label="相机设备" min-width="240"
            ><template #default="{ row }">
              <div class="device-name">
                {{ row.name
                }}<ElTag v-if="!row.identified" size="small" type="info">待关联设备</ElTag>
              </div>
              <span v-if="row.manufacturer || row.model" class="table-secondary">{{
                [row.manufacturer, row.model].filter(Boolean).join(' · ')
              }}</span>
            </template></ElTableColumn
          >
          <ElTableColumn
            v-if="columns.includes('group')"
            label="所属分组"
            min-width="170"
            show-overflow-tooltip
          >
            <template #default="{ row }">{{
              row.groupCount == null
                ? '未获取'
                : row.groupCount > 1
                  ? `多个分组（${row.groupCount}）`
                  : row.groupPath || '待归档'
            }}</template>
          </ElTableColumn>
          <ElTableColumn v-if="columns.includes('channels')" label="可见通道" width="110"
            ><template #default="{ row }">{{ row.channelCount }} 个</template></ElTableColumn
          >
          <ElTableColumn
            v-if="columns.includes('source')"
            label="接入来源"
            min-width="200"
            show-overflow-tooltip
            ><template #default="{ row }"
              ><div>{{ row.sourceDisplayName }}</div>
              <span class="table-secondary">{{
                connectionLabel(row.sourceType, row.connectionCategory)
              }}</span></template
            ></ElTableColumn
          >
          <ElTableColumn v-if="columns.includes('lifecycle')" label="通道状态" min-width="180"
            ><template #default="{ row }"
              ><div class="device-statuses">
                <ElTag v-if="row.enabledCount" type="success">{{ row.enabledCount }} 启用</ElTag>
                <ElTag v-if="row.disabledCount" type="info">{{ row.disabledCount }} 停用</ElTag>
                <ElTag v-if="row.pendingCount" type="info">{{ row.pendingCount }} 待归档</ElTag>
              </div></template
            ></ElTableColumn
          >
          <ElTableColumn
            label="操作"
            :width="canManage ? 220 : 100"
            fixed="right"
            class-name="table-actions-column"
            :resizable="false"
          >
            <template #default="{ row }">
              <TableActions compact>
                <ElButton link type="primary" @click="selectedDevice = asDevice(row)"
                  >详情</ElButton
                >
                <ElButton v-if="canManage" link type="primary" @click="movingDevice = asDevice(row)"
                  >移动分组</ElButton
                >
                <ElButton
                  v-if="canManage"
                  link
                  type="primary"
                  @click="editingDevice = asDevice(row)"
                  >编辑</ElButton
                >
              </TableActions>
            </template>
          </ElTableColumn>
        </ElTable>
        <div v-loading="loading" class="mobile-device-list">
          <article v-for="row in rows" :key="row.groupKey" class="mobile-device">
            <h3>{{ row.name }}</h3>
            <p>
              {{
                [row.manufacturer, row.model].filter(Boolean).join(' · ') || row.sourceDisplayName
              }}
            </p>
            <dl>
              <div>
                <dt>分组</dt>
                <dd>
                  {{
                    row.groupCount == null
                      ? '未获取'
                      : row.groupCount > 1
                        ? `多个分组（${row.groupCount}）`
                        : row.groupPath || '待归档'
                  }}
                </dd>
              </div>
              <div>
                <dt>通道</dt>
                <dd>{{ row.channelCount }} 个可见通道</dd>
              </div>
            </dl>
            <TableActions compact>
              <ElButton link type="primary" @click="selectedDevice = row">详情</ElButton>
              <ElButton v-if="canManage" link type="primary" @click="movingDevice = row"
                >移动分组</ElButton
              >
              <ElButton v-if="canManage" link type="primary" @click="editingDevice = row"
                >编辑</ElButton
              >
            </TableActions>
          </article>
          <p v-if="!loading && !rows.length" class="mobile-empty">暂无可访问设备</p>
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
    </section>
    <GroupEditor
      v-if="groupEditor"
      :group="groupEditor.group"
      :parent-id="groupEditor.parentId"
      @close="groupEditor = null"
      @saved="groupsChanged"
    />
    <DeviceEditor
      v-if="editingDevice"
      :device="editingDevice"
      @close="editingDevice = null"
      @saved="deviceSaved"
    />
    <DeviceMoveDialog
      v-if="movingDevice"
      :device="movingDevice"
      @close="movingDevice = null"
      @saved="moved"
    />
    <DeviceDetailPanel
      v-if="selectedDevice"
      :key="selectedDevice.groupKey"
      :device="selectedDevice"
      :can-manage="canManage"
      @close="selectedDevice = null"
      @changed="refresh"
    />
    <ImportTasksDialog v-if="importTasksOpen" @close="closeImportTasks" />
    <ManualCreateDialog
      v-if="creating === 'manual'"
      :storage-ready="storageReady"
      @close="creating = null"
      @saved="created"
      @rtsp="creating = 'rtsp'"
    />
    <ScanDialog
      v-else-if="creating === 'scan'"
      :storage-ready="storageReady"
      @close="creating = null"
      @saved="created"
    />
    <CreateDialog
      v-else-if="creating === 'platform' || creating === 'rtsp'"
      :mode="creating"
      @close="creating = null"
      @saved="created"
    />
  </div>
</template>
<style scoped>
.camera-workspace {
  display: grid;
  grid-template-columns: clamp(240px, 18vw, 320px) minmax(0, 1fr);
  gap: 20px;
  align-items: start;
}
.camera-group-pane {
  min-width: 0;
  padding: 18px 14px;
  border: 1px solid var(--border-subtle);
  border-radius: 6px;
  background: var(--el-bg-color);
}
.group-pane-header,
.camera-list-heading {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
}
.group-pane-header {
  margin-bottom: 16px;
}
.group-pane-header h2,
.camera-list-heading h2 {
  font-size: 16px;
  margin: 0;
}
.camera-list-pane {
  min-width: 0;
}
.camera-list-heading {
  margin-bottom: 16px;
}
.camera-list-heading p {
  margin: 6px 0 0;
  color: var(--text-secondary);
  font-size: 13px;
  overflow-wrap: anywhere;
}
.camera-list-heading > span {
  flex-shrink: 0;
  color: var(--text-secondary);
  font-size: 13px;
}
.mobile-group-toggle {
  display: none;
}
.mobile-device-list {
  display: none;
}
@media (max-width: 1100px) {
  .camera-workspace {
    grid-template-columns: minmax(0, 1fr);
    gap: 12px;
  }
  .mobile-group-toggle {
    display: flex;
    justify-content: flex-start;
    min-height: 40px;
    height: auto;
    white-space: normal;
    text-align: left;
  }
  .mobile-group-toggle :deep(span) {
    white-space: normal;
    overflow-wrap: anywhere;
  }
  .camera-group-pane {
    display: none;
  }
  .camera-group-pane.is-open {
    display: block;
  }
}
.device-name {
  display: flex;
  align-items: center;
  gap: 8px;
  font-weight: 600;
}
.device-statuses {
  display: flex;
  flex-wrap: wrap;
  gap: 6px;
}
.camera-search-form {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 16px;
  padding: 16px 18px;
}
.camera-search-form :deep(.el-form-item) {
  margin: 0;
}
.camera-search-form :deep(.search-field) {
  width: 200px;
}
@media (max-width: 680px) {
  .device-table,
  .desktop-column-picker {
    display: none;
  }
  .mobile-device-list {
    display: block;
  }
  .mobile-device {
    padding: 16px 0;
    border-top: 1px solid var(--border-subtle);
  }
  .mobile-device:first-child {
    border-top: 0;
  }
  .mobile-device h3 {
    font-size: 15px;
    margin: 0;
    overflow-wrap: anywhere;
  }
  .mobile-device p,
  .mobile-device dl {
    font-size: 13px;
    color: var(--text-secondary);
    margin: 8px 0 12px;
  }
  .mobile-device dl > div {
    display: flex;
    gap: 12px;
    margin: 6px 0;
  }
  .mobile-device dt {
    flex-shrink: 0;
  }
  .mobile-device dd {
    margin: 0;
    overflow-wrap: anywhere;
  }
  .mobile-empty {
    text-align: center;
    color: var(--text-secondary);
  }
  .camera-search-form {
    flex-direction: column;
    align-items: stretch;
  }
  .camera-search-form :deep(.search-field) {
    width: 100%;
  }
}
</style>
