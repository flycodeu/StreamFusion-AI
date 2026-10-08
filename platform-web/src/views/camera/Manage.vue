<script setup lang="ts">
import { onMounted, ref } from 'vue'
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
import type { Camera, Lifecycle } from '../../api/camera/types'
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
import DetailDialog from '../../features/camera/DetailDialog.vue'
import PlacementDialog from '../../features/camera/PlacementDialog.vue'
import GroupSelect from '../../features/camera-groups/GroupSelect.vue'

defineOptions({ name: 'CameraManage' })
const captureScope = usePageScope(),
  rows = ref<Camera[]>([]),
  page = ref(1),
  total = ref(0),
  name = ref(''),
  lifecycle = ref<Lifecycle | null>(null)
const loading = ref(false),
  deleting = ref(false),
  error = ref<unknown>(null),
  canManage = ref(false)
const groupId = ref<string | null>(null),
  includeDescendants = ref(false)
const creating = ref<'manual' | 'scan' | 'platform' | 'rtsp' | null>(null),
  importTasksOpen = ref(false),
  storageReady = ref(true),
  detailId = ref<string | null>(null),
  placement = ref<{ cameraId: string; action: 'move' | 'enable' | 'disable' } | null>(null)
const columns = useColumns('cameras', ['name', 'groupPath', 'source', 'lifecycle'])
let sequence = 0
const asCamera = (value: unknown) => value as Camera
async function load() {
  const current = ++sequence,
    active = captureScope()
  loading.value = true
  error.value = null
  try {
    const [result, options] = await Promise.all([
      api.getCameras({
        page: page.value,
        size: 20,
        name: name.value.trim() || undefined,
        lifecycle: lifecycle.value || undefined,
        groupId: groupId.value || undefined,
        includeDescendants: includeDescendants.value,
      }),
      api.getCameraOptions(),
    ])
    if (!active() || current !== sequence) return
    rows.value = result.items
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
async function placementSaved() {
  placement.value = null
  await load()
}
async function remove(camera: Camera) {
  if (deleting.value || !canManage.value) return
  const active = captureScope()
  deleting.value = true
  try {
    await ElMessageBox.confirm(
      `删除相机“${camera.name}”及其码流档案？存在授权或业务引用时不能删除。`,
      '确认删除相机',
      { type: 'warning' },
    )
    if (!active()) return
    await api.deleteCamera(camera)
    if (active()) await load()
  } catch (cause) {
    if (active() && cause !== 'cancel' && cause !== 'close') error.value = cause
  } finally {
    deleting.value = false
  }
}
onMounted(load)
</script>
<template>
  <div class="content-page">
    <ElForm class="search-form camera-search-form" inline @submit.prevent="search"
      ><ElFormItem label="相机名称"
        ><ElInput
          v-model="name"
          class="search-field"
          placeholder="搜索相机名称"
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
          ><ElButton type="primary" :disabled="deleting" @click="creating = 'manual'"
            >手动添加</ElButton
          ><ElButton :disabled="deleting" @click="creating = 'scan'">网段搜索</ElButton
          ><ElButton :disabled="deleting" @click="creating = 'platform'">平台导入</ElButton
          ><ElButton :disabled="deleting" @click="importTasksOpen = true"
            >导入任务</ElButton
          ></template
        ></template
      >
      <template #tools
        ><ElButton :loading="loading" @click="load">刷新</ElButton
        ><ColumnPicker
          v-model="columns"
          :options="[
            { key: 'name', label: '相机名称' },
            { key: 'groupPath', label: '视频分组' },
            { key: 'source', label: '接入来源' },
            { key: 'lifecycle', label: '配置状态' },
          ]"
      /></template>
      <ElTable
        v-loading="loading"
        :data="rows"
        row-key="cameraId"
        border
        empty-text="暂无可访问相机"
      >
        <ElTableColumn
          v-if="columns.includes('name')"
          prop="name"
          label="相机名称"
          min-width="200"
          show-overflow-tooltip
        />
        <ElTableColumn
          v-if="columns.includes('groupPath')"
          label="视频分组"
          min-width="160"
          show-overflow-tooltip
          ><template #default="{ row }">{{ row.groupPath || '待归档' }}</template></ElTableColumn
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
        <ElTableColumn v-if="columns.includes('lifecycle')" label="配置状态" width="110"
          ><template #default="{ row }"
            ><ElTag :type="row.lifecycle === 'ENABLED' ? 'success' : 'info'">{{
              lifecycleLabels[asCamera(row).lifecycle]
            }}</ElTag></template
          ></ElTableColumn
        >
        <ElTableColumn
          label="操作"
          :width="canManage ? 244 : 116"
          fixed="right"
          class-name="table-actions-column"
          :resizable="false"
          ><template #default="{ row }"
            ><TableActions compact
              ><ElButton link type="primary" @click="detailId = row.cameraId">详情 / 编辑</ElButton
              ><template v-if="canManage"
                ><ElButton
                  link
                  type="primary"
                  :disabled="deleting"
                  @click="placement = { cameraId: row.cameraId, action: 'move' }"
                  >{{ row.lifecycle === 'PENDING_ASSIGNMENT' ? '归档' : '移组' }}</ElButton
                ><ElButton
                  v-if="row.lifecycle !== 'PENDING_ASSIGNMENT'"
                  link
                  type="primary"
                  :disabled="deleting"
                  @click="
                    placement = {
                      cameraId: row.cameraId,
                      action: row.lifecycle === 'ENABLED' ? 'disable' : 'enable',
                    }
                  "
                  >{{ row.lifecycle === 'ENABLED' ? '停用' : '启用' }}</ElButton
                ><ElButton link type="danger" :disabled="deleting" @click="remove(asCamera(row))"
                  >删除</ElButton
                ></template
              ></TableActions
            ></template
          ></ElTableColumn
        >
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
    <DetailDialog
      v-if="detailId"
      :key="detailId"
      :camera-id="detailId"
      :can-manage="canManage"
      @close="detailId = null"
      @changed="load"
    />
    <PlacementDialog
      v-if="placement"
      :camera-id="placement.cameraId"
      :action="placement.action"
      @close="placement = null"
      @saved="placementSaved"
    />
  </div>
</template>
<style scoped>
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
