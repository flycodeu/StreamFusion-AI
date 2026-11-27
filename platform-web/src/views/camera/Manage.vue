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
  ElFormItem,
  ElInput,
  ElMessage,
  ElMessageBox,
  ElOption,
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
import StandardPagination from '../../components/table/StandardPagination.vue'
import FilterBar from '../../components/ui/FilterBar.vue'
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
import DeviceDeleteDialog from '../../features/camera/DeviceDeleteDialog.vue'
import { getGroupTree, deleteGroup } from '../../api/camera-groups/api'
import type { CameraGroup } from '../../api/camera-groups/types'
import './camera-manage.css'

defineOptions({ name: 'CameraManage' })
const props = withDefaults(defineProps<{ focusGroups?: boolean }>(), { focusGroups: false })
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
  groupDeleting = ref(false)
const deleteTargets = ref<CameraDeviceGroup[]>([])
const deviceDeleting = computed(() => deleteTargets.value.length > 0)
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
function updateDeviceVendor(groupKey: string | undefined, vendorHint: string | null) {
  if (!groupKey) return
  rows.value = rows.value.map((item) =>
    item.groupKey === groupKey ? { ...item, vendorHint } : item,
  )
  if (selectedDevice.value?.groupKey === groupKey) {
    selectedDevice.value = { ...selectedDevice.value, vendorHint }
  }
  if (editingDevice.value?.groupKey === groupKey) {
    editingDevice.value = { ...editingDevice.value, vendorHint }
  }
}
function deviceBrand(row: CameraDeviceGroup): string {
  return sourceBrandLabel(row)
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
async function previewChannel(cameraId: string) {
  if (!previewPath.value) return
  const active = captureScope()
  try {
    const saved = await api.getCamera(cameraId)
    if (!active()) return
    if (saved.lifecycle !== 'ENABLED') {
      await created()
      if (active()) ElMessage.info('请先启用这路通道，再试播')
      return
    }
    creating.value = null
    void router.push({
      path: previewPath.value,
      query: {
        mode: 'device',
        device: saved.deviceId ? `d${saved.deviceId}` : `c${cameraId}`,
        camera: cameraId,
      },
    })
  } catch (cause) {
    if (!active()) return
    await created()
    if (active()) error.value = cause
  }
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
  if (!loading.value && !plansOpen.value && !deviceDeleting.value && !selectedRows.value.length) {
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
function removeDevices(targets: CameraDeviceGroup[]) {
  if (!targets.length || deviceDeleting.value || !canManage.value) return
  error.value = null
  deleteTargets.value = [...targets]
}
async function devicesRemoved(keys: string[]) {
  if (selectedDevice.value && keys.includes(selectedDevice.value.groupKey)) {
    selectedDevice.value = null
  }
  await refresh()
}
onMounted(() => {
  if (
    props.focusGroups &&
    typeof globalThis.innerWidth === 'number' &&
    globalThis.innerWidth <= 1100
  ) {
    treeOpen.value = true
  }
  void refresh()
})
</script>
<template>
  <div class="content-page camera-workspace">
    <ElButton class="mobile-group-toggle" :aria-expanded="treeOpen" @click="treeOpen = !treeOpen"
      >分组：{{ groupPath }}</ElButton
    >
    <aside
      v-loading="treeLoading"
      class="camera-group-pane"
      :class="{ 'is-group-focused': focusGroups }"
      aria-label="视频分组"
    >
      <header class="group-pane-header">
        <h2>视频分组</h2>
        <span v-if="focusGroups" class="group-count-badge">{{ groups.length }} 组</span>
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
          <span class="scope-label">{{ focusGroups ? '分组视图' : '当前视图' }}</span>
          <span class="scope-name" :title="groupPath">{{ groupPath }}</span>
          <span class="sf-count-pill">共 {{ total }} 台</span>
        </div>
        <FilterBar
          embedded
          class="camera-search-form"
          :loading="loading"
          @search="search"
          @reset="resetSearch"
        >
          <ElFormItem label="相机"
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
                :label="label" /></ElSelect
          ></ElFormItem>
        </FilterBar>
      </div>
      <RequestError :error="error" />
      <TablePanel class="camera-table-panel" :initial-loading="loading && !rows.length">
        <template #actions
          ><template v-if="canManage"
            ><ElButton
              v-if="focusGroups"
              type="primary"
              plain
              @click="groupEditor = { group: null, parentId: groupId }"
              >新建分组</ElButton
            ><ElDropdown split-button type="primary" trigger="click" @click="creating = 'rtsp'">
              添加 RTSP 相机
              <template #dropdown
                ><ElDropdownMenu>
                  <ElDropdownItem @click="creating = 'rtsp'">填写 RTSP 地址</ElDropdownItem>
                  <ElDropdownItem @click="creating = 'scan'">搜索设备</ElDropdownItem>
                  <ElDropdownItem @click="creating = 'platform'">从平台导入</ElDropdownItem>
                  <ElDropdownItem @click="creating = 'manual'">仅登记设备档案</ElDropdownItem>
                </ElDropdownMenu></template
              > </ElDropdown
            ><ElButton @click="importTasksOpen = true">导入任务</ElButton
            ><ElButton
              type="danger"
              plain
              :loading="deviceDeleting"
              :disabled="!selectedRows.length || loading || deviceDeleting"
              @click="removeDevices(selectedRows)"
              >{{
                selectedRows.length ? `批量删除 (${selectedRows.length})` : '批量删除'
              }}</ElButton
            ></template
          ></template
        >
        <template #tools
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
              <ElButton v-else-if="canManage" type="primary" @click="creating = 'rtsp'"
                >添加 RTSP 相机</ElButton
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
          <ElTableColumn
            v-if="columns.includes('name')"
            label="相机名称"
            min-width="280"
            show-overflow-tooltip
            ><template #default="{ row }">
              <div class="device-name" :title="row.name">
                {{ row.name }}
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
                  <ElTag :type="deviceStatus(asDevice(row)).tone" effect="plain">{{
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
                <ElTag v-if="row.enabledCount" type="success" effect="plain"
                  >{{ row.enabledCount }} 启用</ElTag
                >
                <ElTag v-if="row.disabledCount" type="danger" effect="plain"
                  >{{ row.disabledCount }} 停用</ElTag
                >
                <ElTag v-if="row.pendingCount" type="warning" effect="plain"
                  >{{ row.pendingCount }} 待归档</ElTag
                >
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
        <template #footer>
          <StandardPagination
            v-model:page="page"
            v-model:size="size"
            :total="total"
            @change="load"
          />
        </template>
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
      :device="selectedDevice"
      :can-manage="canManage"
      @close="selectedDevice = null"
      @changed="refresh"
      @vendor-changed="(hint) => updateDeviceVendor(selectedDevice?.groupKey, hint)"
    />
    <PlanDialog v-if="plansOpen" @close="plansOpen = false" @changed="refresh" />
    <ImportTasksDialog v-if="importTasksOpen" @close="closeImportTasks" />
    <DeviceDeleteDialog
      v-if="deleteTargets.length"
      :targets="deleteTargets"
      @close="deleteTargets = []"
      @changed="devicesRemoved"
    />
    <ManualCreateDialog
      v-if="creating === 'manual'"
      :storage-ready="storageReady"
      :initial-group-id="groupId"
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
      :initial-group-id="groupId"
      :can-preview="Boolean(previewPath)"
      @close="creating = null"
      @saved="created"
      @preview="previewChannel"
    />
  </div>
</template>
