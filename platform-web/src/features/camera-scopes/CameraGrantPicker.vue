<script setup lang="ts">
import { onMounted, ref } from 'vue'
import {
  ElButton,
  ElCheckbox,
  ElForm,
  ElFormItem,
  ElInput,
  ElPagination,
  ElTable,
  ElTableColumn,
  vLoading,
} from 'element-plus'
import * as api from '../../api/camera-scopes/api'
import RequestError from '../../components/feedback/RequestError.vue'
import { usePageScope } from '../../composables/usePageScope'

const props = defineProps<{ disabled: boolean }>()
const selected = defineModel<string[]>({ required: true })
const emit = defineEmits<{ label: [id: string, label: string] }>()
interface Choice {
  id: string
  name: string
}
const rows = ref<Choice[]>([]),
  page = ref(1),
  total = ref(0),
  name = ref(''),
  loading = ref(false),
  error = ref<unknown>(null)
const captureScope = usePageScope()
let sequence = 0
async function load() {
  const current = ++sequence,
    active = captureScope()
  loading.value = true
  error.value = null
  try {
    const query = { page: page.value, size: 20, name: name.value.trim() || undefined }
    const result = await api.getScopeCameras(query)
    if (!active() || current !== sequence) return
    rows.value = result.items.map((row) => ({
      id: row.cameraId,
      name: row.path ? `${row.path} / ${row.name}` : row.name,
    }))
    total.value = result.total
    for (const row of rows.value) emit('label', row.id, row.name)
  } catch (cause) {
    if (active() && current === sequence) error.value = cause
  } finally {
    if (active() && current === sequence) loading.value = false
  }
}
function search() {
  page.value = 1
  void load()
}
function toggle(id: string, checked: unknown) {
  if (props.disabled) return
  const ids = new Set(selected.value)
  if (checked) {
    if (ids.size >= 2000 && !ids.has(id)) return
    ids.add(id)
  } else ids.delete(id)
  selected.value = [...ids]
}
onMounted(load)
</script>
<template>
  <div>
    <ElForm inline @submit.prevent="search"
      ><ElFormItem><ElInput v-model="name" placeholder="搜索已启用通道" clearable /></ElFormItem
      ><ElFormItem
        ><ElButton native-type="submit" :loading="loading">查询</ElButton></ElFormItem
      ></ElForm
    >
    <RequestError :error="error" />
    <ElTable v-loading="loading" :data="rows" row-key="id" border empty-text="暂无可选资源">
      <ElTableColumn width="56"
        ><template #default="{ row }"
          ><ElCheckbox
            :model-value="selected.includes(row.id)"
            :disabled="disabled || (!selected.includes(row.id) && selected.length >= 2000)"
            :aria-label="`选择 ${row.name}`"
            @update:model-value="toggle(row.id, $event)" /></template
      ></ElTableColumn>
      <ElTableColumn prop="name" label="资源名称" min-width="240" />
    </ElTable>
    <ElPagination
      v-model:current-page="page"
      :page-size="20"
      :total="total"
      layout="total, prev, pager, next"
      @current-change="load"
    />
  </div>
</template>
