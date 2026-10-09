<script setup lang="ts">
import { onMounted, ref } from 'vue'
import {
  ElButton,
  ElCheckbox,
  ElForm,
  ElFormItem,
  ElInput,
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
import GroupSelect from '../../features/camera-groups/GroupSelect.vue'

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
  includeDescendants = ref(false)
const creating = ref<'manual' | 'scan' | 'platform' | 'rtsp' | null>(null),
  importTasksOpen = ref(false),
  storageReady = ref(true)
const selectedDevice = ref<CameraDeviceGroup | null>(null)
const columns = useColumns('camera-devices', ['name', 'channels', 'source', 'lifecycle'])
let sequence = 0
const asDevice = (value: unknown) => value as CameraDeviceGroup
function query(): api.CameraSearch {
  return {
    page: page.value,
    size: 20,
    name: name.value.trim() || undefined,
    lifecycle: lifecycle.value || undefined,
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
  includeDescendants.value = false
  search()
}
function search() {
  page.value = 1
  void load()
}
async function created() {
  creating.value = null
  await load()
}
function closeImportTasks() {
  importTasksOpen.value = false
  void load()
}
onMounted(load)
</script>
<template>
  <div class="content-page">
    <ElForm class="search-form camera-search-form" inline @submit.prevent="search"
      ><ElFormItem label="设备 / 通道"
        ><ElInput
          v-model="name"
          class="search-field"
          placeholder="搜索设备或通道名称"
          clearable
          maxlength="128" /></ElFormItem
      ><ElFormItem label="视频分组"
        ><GroupSelect v-model="groupId" class="search-field" clearable
      /></ElFormItem>
      <ElFormItem><ElCheckbox v-model="includeDescendants">包含下级</ElCheckbox></ElFormItem>
      <ElFormItem label="配置状态"
        ><ElSelect v-model="lifecycle" class="status-field" clearable placeholder="全部状态"
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
        ><ElButton :loading="loading" @click="load">刷新</ElButton
        ><ColumnPicker
          v-model="columns"
          :options="[
            { key: 'name', label: '相机设备' },
            { key: 'channels', label: '通道数量' },
            { key: 'source', label: '接入来源' },
            { key: 'lifecycle', label: '配置状态' },
          ]"
      /></template>
      <ElTable
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
          width="100"
          fixed="right"
          class-name="table-actions-column"
          :resizable="false"
        >
          <template #default="{ row }">
            <TableActions compact>
              <ElButton link type="primary" @click="selectedDevice = asDevice(row)">详情</ElButton>
            </TableActions>
          </template>
        </ElTableColumn>
      </ElTable>
      <template #footer
        ><ElPagination
          v-model:current-page="page"
          :page-size="20"
          :total="total"
          layout="total, prev, pager, next"
          @current-change="load"
      /></template>
    </TablePanel>
    <DeviceDetailPanel
      v-if="selectedDevice"
      :key="selectedDevice.groupKey"
      :device="selectedDevice"
      :can-manage="canManage"
      @close="selectedDevice = null"
      @changed="load"
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
  .camera-search-form {
    flex-direction: column;
    align-items: stretch;
  }
  .camera-search-form :deep(.search-field) {
    width: 100%;
  }
}
</style>
