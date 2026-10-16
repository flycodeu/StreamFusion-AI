<script setup lang="ts">
import { onMounted, ref } from 'vue'
import {
  ElButton,
  ElCheckbox,
  ElInput,
  ElPagination,
  ElTable,
  ElTableColumn,
  vLoading,
} from 'element-plus'
import { getCameraDevices } from '../../api/camera/api'
import type { CameraDeviceGroup } from '../../api/camera/types'
import { addressText } from '../camera/address'
import RequestError from '../../components/feedback/RequestError.vue'
import { usePageScope } from '../../composables/usePageScope'

const props = defineProps<{ disabled: boolean }>()
const selected = defineModel<string[]>({ required: true })
const rows = ref<CameraDeviceGroup[]>([])
const name = ref(''),
  page = ref(1),
  total = ref(0),
  loading = ref(false),
  error = ref<unknown>(null)
const capture = usePageScope()
let sequence = 0
async function load(reset = false) {
  if (reset) page.value = 1
  const requestId = ++sequence,
    active = capture()
  loading.value = true
  error.value = null
  try {
    const result = await getCameraDevices({
      page: page.value,
      size: 10,
      name: name.value.trim() || undefined,
      lifecycle: 'ENABLED',
    })
    if (!active() || requestId !== sequence) return
    rows.value = result.items
    total.value = result.total
  } catch (cause) {
    if (active() && requestId === sequence) error.value = cause
  } finally {
    if (active() && requestId === sequence) loading.value = false
  }
}
function toggle(key: string, value: unknown) {
  if (props.disabled) return
  const keys = new Set(selected.value)
  if (value && (keys.has(key) || keys.size < 2000)) keys.add(key)
  else keys.delete(key)
  selected.value = [...keys]
}
onMounted(() => load())
</script>
<template>
  <div class="camera-picker">
    <div class="picker-search" @keydown.enter.prevent="load(true)">
      <ElInput
        v-model="name"
        placeholder="搜索相机名称"
        aria-label="搜索巡检相机"
        clearable
        :disabled="disabled"
      />
      <ElButton :loading="loading" :disabled="disabled" @click="load(true)">查询</ElButton>
      <span>已选 {{ selected.length }} 台</span>
    </div>
    <RequestError :error="error" />
    <ElTable
      v-loading="loading"
      :data="rows"
      row-key="groupKey"
      border
      max-height="360"
      empty-text="暂无可选相机"
    >
      <ElTableColumn width="48"
        ><template #default="{ row }"
          ><ElCheckbox
            :model-value="selected.includes(row.groupKey)"
            :disabled="disabled || (!selected.includes(row.groupKey) && selected.length >= 2000)"
            :aria-label="`选择 ${row.name}`"
            @update:model-value="toggle(row.groupKey, $event)" /></template
      ></ElTableColumn>
      <ElTableColumn prop="name" label="相机" min-width="180" show-overflow-tooltip />
      <ElTableColumn label="地址" min-width="150" show-overflow-tooltip
        ><template #default="{ row }">{{ addressText(row.accessAddress) }}</template></ElTableColumn
      >
      <ElTableColumn prop="groupPath" label="分组" min-width="140" show-overflow-tooltip />
    </ElTable>
    <ElPagination
      v-model:current-page="page"
      :page-size="10"
      :total="total"
      :disabled="disabled || loading"
      layout="total, prev, pager, next"
      @current-change="load()"
    />
  </div>
</template>
<style scoped>
.picker-search {
  display: flex;
  align-items: center;
  gap: 10px;
  flex-wrap: wrap;
  margin-bottom: 14px;
}
.picker-search .el-input {
  width: min(280px, 100%);
}
.picker-search span {
  margin-left: auto;
  color: var(--el-text-color-secondary);
  font-size: 13px;
}
.el-pagination {
  margin-top: 14px;
  overflow-x: auto;
}
</style>
