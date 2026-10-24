<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useRouter } from 'vue-router'
import { sessionState } from '../../session/state'
import { findPreviewPath } from '../../features/preview/model'
import { deviceStatus } from '../../features/camera-status/deviceStatus'
import { useDeviceRefresh } from '../../features/camera-status/useDeviceRefresh'
import PlanDialog from '../../features/camera-status/PlanDialog.vue'
import {
  ElButton,
  ElDrawer,
  ElDropdown,
  ElDropdownMenu,
  ElDropdownItem,
  ElEmpty,
  ElCheckbox,
  ElForm,
  ElFormItem,
  ElInput,
  ElMessage,
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
import { deleteSource, getSource } from '../../api/camera-sources/api'
import type { CameraDeviceGroup, Lifecycle } from '../../api/camera/types'
import RequestError from '../../components/feedback/RequestError.vue'
import TablePanel from '../../components/table/TablePanel.vue'
import TableActions from '../../components/table/TableActions.vue'
import ColumnPicker from '../../components/table/ColumnPicker.vue'
import { useColumns } from '../../composables/table/useColumns'
import { useTableSelection } from '../../composables/table/useTableSelection'
import { usePageScope } from '../../composables/usePageScope'
import { lifecycleLabels, sourceBrandLabel } from '../../features/camera/form'
import { addressLabel, addressText } from '../../features/camera/address'
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
const router = useRouter()
const previewPath = computed(() => findPreviewPath(sessionState.me?.routes || []))
const captureScope = usePageScope(),
  rows = ref<CameraDeviceGroup[]>([]),
  page = ref(1),
  size = ref(20),
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
  groupDeleting = ref(false),
  deviceDeleting = ref(false)
const { selectedRows, setSelection, clearSelection } = useTableSelection<CameraDeviceGroup>()
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
const plansOpen = ref(false)
const editingDevice = ref<CameraDeviceGroup | null>(null)
function loadVendorHints(): Record<string, string | null> {
  if (import.meta.env.MODE === 'test') return {}
  try {
    const raw = globalThis.localStorage?.getItem('camera-device-vendor-hints-v1')
    return raw ? (JSON.parse(raw) as Record<string, string | null>) : {}
  } catch {
    return {}
  }
}
const vendorHints = ref<Record<string, string | null>>(loadVendorHints())
function updateDeviceVendor(groupKey: string | undefined, vendorHint: string | null) {
  if (!groupKey) return
  vendorHints.value = { ...vendorHints.value, [groupKey]: vendorHint }
  if (import.meta.env.MODE !== 'test') {
    try {
      globalThis.localStorage?.setItem(
        'camera-device-vendor-hints-v1',
        JSON.stringify(vendorHints.value),
      )
    } catch {
      // Ignore storage errors
    }
  }
}
function withVendorHint(row: CameraDeviceGroup): CameraDeviceGroup {
  const hint = row.groupKey in vendorHints.value ? vendorHints.value[row.groupKey] : row.vendorHint
  return hint !== undefined ? { ...row, vendorHint: hint } : row
}
function deviceBrand(row: CameraDeviceGroup): string {
  return sourceBrandLabel(withVendorHint(row))
}
async function hydrateMissingVendors(items: CameraDeviceGroup[], active: () => boolean) {
  if (import.meta.env.MODE === 'test') return
  const missing = items.filter(
    (item) =>
      item.vendorHint === undefined &&
      !(item.groupKey in vendorHints.value) &&
      sourceBrandLabel(item) === '未识别品牌',
  )
  for (const item of missing) {
    try {
      const res = await api.getDeviceChannels(item.groupKey, { page: 1, size: 1 })
      if (!active()) return
      updateDeviceVendor(item.groupKey, res.items[0]?.vendorHint ?? null)
    } catch {
      // Ignore background hydration errors
    }
  }
}
async function deviceSaved(name: string) {
  if (selectedDevice.value?.groupKey === editingDevice.value?.groupKey && selectedDevice.value)
    selectedDevice.value = { ...selectedDevice.value, name }
  editingDevice.value = null
  await refresh()
}
const columns = useColumns(
  'camera-device-workspace-v4',
  ['name', 'source', 'model', 'connection'],
  ['name', 'channels', 'source', 'model', 'connection', 'lifecycle'],
)
let sequence = 0,
  treeSequence = 0
const asDevice = (value: unknown) => value as CameraDeviceGroup
function query(): api.CameraSearch {
  return {
    page: page.value,
    size: size.value,
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
    const lastPage = Math.max(1, Math.ceil(result.total / requestQuery.size))
    if (page.value > lastPage) {
      page.value = lastPage
      await load()
      return
    }
    rows.value = result.items
    clearSelection()
    void hydrateMissingVendors(result.items, active)
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
  resetAutoRefresh()
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
function openPreview(device: CameraDeviceGroup) {
  if (previewPath.value)
    void router.push({
      path: previewPath.value,
      query: { mode: 'device', device: device.groupKey },
    })
}
const resetAutoRefresh = useDeviceRefresh(async () => {
  if (!loading.value && !plansOpen.value) {
    await load()
    if (error.value) throw error.value
  }
})
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
      {
        type: 'warning',
        confirmButtonClass: 'el-button--danger',
        confirmButtonText: '确认删除',
        cancelButtonText: '取消',
      },
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
async function deleteSingleDeviceCascade(target: CameraDeviceGroup) {
  let sourceId: string | null = null
  const firstPage = await api.getDeviceChannels(target.groupKey, { page: 1, size: 100 })
  for (const ch of firstPage.items) {
    if (!sourceId && ch.sourceId) sourceId = ch.sourceId
    await api.deleteCamera({ cameraId: ch.cameraId, version: ch.version })
  }
  if (target.identified) {
    const deviceId = target.groupKey.slice(1)
    try {
      const dev = await api.getCameraDevice(deviceId)
      if (!sourceId && dev.sourceId) sourceId = dev.sourceId
      await api.deleteCameraDevice(dev.deviceId, dev.version)
    } catch {
      // Ignore if device record was already removed
    }
  }
  if (sourceId) {
    try {
      const src = await getSource(sourceId)
      await deleteSource(src.sourceId, src.version)
    } catch {
      // Ignore conflict when the connection source is still shared by other cameras
    }
  }
}
async function removeDevices(targets: CameraDeviceGroup[]) {
  if (!targets.length || deviceDeleting.value || !canManage.value) return
  const active = captureScope()
  const isBatch = targets.length > 1
  const names = targets
    .slice(0, 3)
    .map((item) => `“${item.name}”`)
    .join('、')
  const message = isBatch
    ? `确认批量删除已选的 ${targets.length} 台相机（${names}${targets.length > 3 ? ' 等' : ''}）？将同步删除关联通道、码流档案、状态记录及独占连接配置。`
    : `确认删除相机“${targets[0]!.name}”？将同步删除其通道、码流档案、状态记录及独占连接配置。`
  deviceDeleting.value = true
  error.value = null
  try {
    await ElMessageBox.confirm(message, isBatch ? '确认批量删除相机' : '确认删除相机', {
      type: 'warning',
      confirmButtonClass: 'el-button--danger',
      confirmButtonText: '确认删除',
      cancelButtonText: '取消',
    })
    if (!active()) return
    const deletedKeys = new Set(targets.map((item) => item.groupKey))
    for (const item of targets) {
      await deleteSingleDeviceCascade(item)
      if (!active()) return
    }
    if (selectedDevice.value && deletedKeys.has(selectedDevice.value.groupKey)) {
      selectedDevice.value = null
    }
    clearSelection()
    ElMessage.success(
      isBatch ? `已删除 ${targets.length} 台相机及关联配置` : '相机及关联配置已删除',
    )
    await refresh()
  } catch (cause) {
    if (active() && cause !== 'cancel' && cause !== 'close') error.value = cause
  } finally {
    if (active()) deviceDeleting.value = false
  }
}
onMounted(refresh)
</script>
<template>
  <div class="content-page camera-workspace">
    <ElButton class="mobile-group-toggle" :aria-expanded="treeOpen" @click="treeOpen = !treeOpen"
      >分组：{{ groupPath }}</ElButton
    >
    <aside v-loading="treeLoading" class="camera-group-pane" aria-label="视频分组">
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
    <ElDrawer
      v-model="treeOpen"
      title="视频分组"
      direction="ltr"
      size="min(360px, 90vw)"
      destroy-on-close
    >
      <template #header>
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
      </template>
      <div v-loading="treeLoading">
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
      </div>
    </ElDrawer>
    <section class="camera-list-pane" aria-label="相机管理">
      <div class="workspace-filter-bar">
        <div class="scope-indicator">
          <span class="scope-label">当前视图</span>
          <span class="scope-name" :title="groupPath">{{ groupPath }}</span>
          <span class="scope-count">共 {{ total }} 台</span>
        </div>
        <ElForm class="search-form camera-search-form" inline @submit.prevent="search"
          ><ElFormItem label="相机"
            ><ElInput
              v-model="name"
              class="search-field"
              placeholder="搜索相机名称 / IP"
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
      </div>
      <RequestError :error="error" />
      <TablePanel class="camera-table-panel" :initial-loading="loading && !rows.length">
        <template #actions
          ><template v-if="canManage"
            ><ElDropdown split-button type="primary" trigger="click" @click="creating = 'manual'">
              添加相机
              <template #dropdown
                ><ElDropdownMenu>
                  <ElDropdownItem @click="creating = 'manual'">手动添加</ElDropdownItem>
                  <ElDropdownItem @click="creating = 'scan'">网段搜索</ElDropdownItem>
                  <ElDropdownItem @click="creating = 'platform'">平台导入</ElDropdownItem>
                </ElDropdownMenu></template
              > </ElDropdown
            ><ElButton @click="importTasksOpen = true">导入任务</ElButton></template
          ></template
        >
        <template #tools
          ><ElButton
            v-if="canManage"
            type="danger"
            plain
            :loading="deviceDeleting"
            :disabled="!selectedRows.length || loading || deviceDeleting"
            @click="removeDevices(selectedRows)"
            >{{ selectedRows.length ? `批量删除 (${selectedRows.length})` : '批量删除' }}</ElButton
          ><ElButton @click="plansOpen = true">巡检计划</ElButton
          ><ElButton :loading="loading || treeLoading" @click="refresh">刷新</ElButton
          ><span class="desktop-column-picker"
            ><ColumnPicker
              v-model="columns"
              :options="[
                { key: 'name', label: '相机名称' },
                { key: 'source', label: '品牌' },
                { key: 'model', label: '型号' },
                { key: 'channels', label: '通道数量' },
                { key: 'connection', label: '连接状态' },
                { key: 'lifecycle', label: '配置状态' },
              ]" /></span
        ></template>
        <ElTable
          class="device-table"
          v-loading="loading"
          :data="rows"
          row-key="groupKey"
          border
          empty-text="暂无可访问相机"
          @selection-change="setSelection"
        >
          <template #empty>
            <ElEmpty
              :image-size="64"
              :description="
                error
                  ? '相机列表读取失败'
                  : name || lifecycle || groupId || pendingOnly
                    ? '未找到符合条件的相机'
                    : '暂无可访问相机'
              "
            >
              <ElButton v-if="error" @click="load">重新加载</ElButton>
              <ElButton v-else-if="name || lifecycle || groupId || pendingOnly" @click="resetSearch"
                >重置筛选</ElButton
              >
              <ElButton v-else-if="canManage" type="primary" @click="creating = 'manual'"
                >添加相机</ElButton
              >
            </ElEmpty>
          </template>
          <ElTableColumn
            v-if="canManage"
            type="selection"
            width="48"
            align="center"
            :selectable="() => !deviceDeleting"
          />
          <ElTableColumn v-if="columns.includes('name')" label="相机名称" min-width="180"
            ><template #default="{ row }">
              <div class="device-name" :title="row.name">
                {{ row.name
                }}<ElTag v-if="!row.identified" size="small" type="info">待关联设备</ElTag>
              </div>
            </template></ElTableColumn
          >
          <ElTableColumn
            v-if="columns.includes('source')"
            label="品牌"
            min-width="95"
            show-overflow-tooltip
          >
            <template #default="{ row }">{{ deviceBrand(asDevice(row)) }}</template>
          </ElTableColumn>
          <ElTableColumn
            v-if="columns.includes('model')"
            label="型号"
            min-width="125"
            show-overflow-tooltip
          >
            <template #default="{ row }">{{ row.model || '—' }}</template>
          </ElTableColumn>
          <ElTableColumn label="IP / 主机" min-width="135">
            <template #default="{ row }"
              ><span class="address-host">{{ row.accessAddress?.host || '未获取' }}</span>
              <span v-if="row.accessAddress?.kind === 'PLATFORM'" class="table-secondary"
                >平台地址</span
              >
            </template>
          </ElTableColumn>
          <ElTableColumn label="端口" width="80"
            ><template #default="{ row }">{{
              row.accessAddress?.port ?? '—'
            }}</template></ElTableColumn
          >
          <ElTableColumn v-if="columns.includes('connection')" label="连接状态" min-width="150">
            <template #default="{ row }">
              <div class="device-statuses">
                <ElButton link @click="selectedDevice = asDevice(row)">
                  <ElTag :type="deviceStatus(asDevice(row)).tone">{{
                    deviceStatus(asDevice(row)).label
                  }}</ElTag>
                </ElButton>
              </div>
            </template>
          </ElTableColumn>
          <ElTableColumn v-if="columns.includes('channels')" label="可见通道" width="110">
            <template #default="{ row }">{{ row.channelCount }} 个</template>
          </ElTableColumn>
          <ElTableColumn v-if="columns.includes('lifecycle')" label="配置状态" min-width="180"
            ><template #default="{ row }"
              ><div class="device-statuses">
                <ElTag v-if="row.enabledCount" type="success">{{ row.enabledCount }} 启用</ElTag>
                <ElTag v-if="row.disabledCount" type="danger">{{ row.disabledCount }} 停用</ElTag>
                <ElTag v-if="row.pendingCount" type="warning">{{ row.pendingCount }} 待归档</ElTag>
              </div></template
            ></ElTableColumn
          >
          <ElTableColumn
            label="操作"
            :width="canManage ? 270 : 150"
            fixed="right"
            class-name="table-actions-column"
            :resizable="false"
          >
            <template #default="{ row }">
              <TableActions compact>
                <ElButton v-if="previewPath" link type="primary" @click="openPreview(asDevice(row))"
                  >预览</ElButton
                >
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
                <ElButton
                  v-if="canManage"
                  link
                  type="danger"
                  :disabled="deviceDeleting"
                  @click="removeDevices([asDevice(row)])"
                  >删除</ElButton
                >
              </TableActions>
            </template>
          </ElTableColumn>
        </ElTable>
        <div v-loading="loading" class="mobile-device-list">
          <article v-for="row in rows" :key="row.groupKey" class="mobile-device">
            <h3>{{ row.name }}</h3>
            <p>
              {{ [deviceBrand(row), row.model].filter(Boolean).join(' · ') }}
            </p>
            <dl>
              <div>
                <dt>{{ addressLabel(row.accessAddress) }}</dt>
                <dd>{{ addressText(row.accessAddress) }}</dd>
              </div>
              <div>
                <dt>连接状态</dt>
                <dd class="device-statuses">
                  <ElButton link @click="selectedDevice = row"
                    ><ElTag :type="deviceStatus(row).tone">{{
                      deviceStatus(row).label
                    }}</ElTag></ElButton
                  >
                </dd>
              </div>
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
            </dl>
            <TableActions compact>
              <ElButton v-if="previewPath" type="primary" @click="openPreview(row)">预览</ElButton>
              <ElButton @click="selectedDevice = row">详情</ElButton>
              <ElDropdown v-if="canManage" trigger="click">
                <ElButton :aria-label="`更多操作：${row.name}`">更多 ▾</ElButton>
                <template #dropdown
                  ><ElDropdownMenu>
                    <ElDropdownItem @click="movingDevice = row">移动分组</ElDropdownItem>
                    <ElDropdownItem @click="editingDevice = row">编辑</ElDropdownItem>
                    <ElDropdownItem divided @click="removeDevices([row])">删除</ElDropdownItem>
                  </ElDropdownMenu></template
                >
              </ElDropdown>
            </TableActions>
          </article>
          <p v-if="!loading && !rows.length" class="mobile-empty">暂无可访问相机</p>
        </div>
        <template #footer
          ><ElPagination
            v-model:current-page="page"
            v-model:page-size="size"
            :page-sizes="[20, 50, 100]"
            :total="total"
            layout="total, sizes, prev, pager, next, jumper"
            @size-change="page = 1"
            @change="load"
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
      :device="withVendorHint(editingDevice)"
      @close="editingDevice = null"
      @saved="deviceSaved"
      @vendor-changed="(hint) => updateDeviceVendor(editingDevice?.groupKey, hint)"
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
      :device="withVendorHint(selectedDevice)"
      :can-manage="canManage"
      @close="selectedDevice = null"
      @changed="refresh"
      @vendor-changed="(hint) => updateDeviceVendor(selectedDevice?.groupKey, hint)"
    />
    <PlanDialog v-if="plansOpen" @close="plansOpen = false" @changed="refresh" />
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
  --pane-border: #e5e8ef;
  --text-main: #1d2129;
  --text-sub: #4e5969;
  --text-muted: #86909c;

  min-height: calc(100dvh - var(--header-height, 56px));
  display: grid;
  grid-template-columns: clamp(248px, 17vw, 310px) minmax(0, 1fr);
  gap: clamp(14px, 1.2vw, 20px);
  align-items: stretch;
  padding-top: clamp(12px, 1.4dvh, 20px);
  padding-bottom: clamp(12px, 1.4dvh, 20px);
  box-sizing: border-box;
}

.camera-group-pane {
  min-width: 0;
  padding: 16px 14px;
  border: 1px solid var(--pane-border);
  border-radius: 6px;
  background: var(--el-bg-color);
  display: flex;
  flex-direction: column;
}

.group-pane-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  padding-bottom: 12px;
  margin-bottom: 12px;
  border-bottom: 1px solid #f0f2f5;
  flex-shrink: 0;
}

.group-pane-header h2 {
  font-size: 15px;
  font-weight: 600;
  color: var(--text-main);
  margin: 0;
}

.camera-list-pane {
  min-width: 0;
  display: flex;
  flex-direction: column;
  gap: 14px;
}

.workspace-filter-bar {
  background: #ffffff;
  border: 1px solid var(--pane-border);
  border-radius: 6px;
  padding: 12px 18px;
  display: flex;
  align-items: center;
  justify-content: space-between;
  flex-wrap: wrap;
  gap: 14px;
}

.scope-indicator {
  display: flex;
  align-items: center;
  gap: 8px;
  min-width: 0;
}

.scope-label {
  font-size: 12px;
  color: var(--text-muted);
  background: #f7f8fa;
  border: 1px solid var(--pane-border);
  padding: 2px 8px;
  border-radius: 4px;
  flex-shrink: 0;
}

.scope-name {
  font-size: 14px;
  font-weight: 600;
  color: var(--text-main);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  max-width: 260px;
}

.scope-count {
  font-size: 12px;
  color: var(--text-muted);
  font-variant-numeric: tabular-nums;
  padding-left: 4px;
  flex-shrink: 0;
}

.camera-search-form {
  margin: 0;
  padding: 0;
  border: 0;
  background: transparent;
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 14px;
}

.camera-search-form :deep(.el-form-item) {
  margin: 0;
}

.camera-search-form :deep(.search-field) {
  width: clamp(190px, 14vw, 240px);
}

.camera-table-panel {
  flex: 1;
  display: flex;
  flex-direction: column;
  border: 1px solid var(--pane-border);
  border-radius: 6px;
  padding: 16px 18px;
}

.camera-table-panel :deep(.el-table) {
  flex: 1;
}

.camera-table-panel :deep(.table-panel__footer) {
  margin-top: auto;
  padding-top: 16px;
}

.mobile-group-toggle {
  display: none;
}

.mobile-device-list {
  display: none;
}

@media (max-width: 1100px) {
  .camera-workspace {
    min-height: auto;
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
}

.device-name {
  display: flex;
  align-items: center;
  gap: 8px;
  font-weight: 600;
  color: var(--text-main);
}

.device-statuses {
  display: flex;
  flex-wrap: wrap;
  gap: 6px;
}

.device-table .table-secondary {
  display: block;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.address-host {
  display: block;
  overflow-wrap: anywhere;
  font-variant-numeric: tabular-nums;
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
  .workspace-filter-bar {
    flex-direction: column;
    align-items: stretch;
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
