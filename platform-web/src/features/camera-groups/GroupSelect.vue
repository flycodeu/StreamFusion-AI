<script setup lang="ts">
import { formError as validationError } from '../camera/form'
import { computed, ref } from 'vue'
import { ElTreeSelect } from 'element-plus'
import { getGroups } from '../../api/camera-groups/api'
import RequestError from '../../components/feedback/RequestError.vue'
import { usePageScope } from '../../composables/usePageScope'

const props = defineProps<{
  selectedLabel?: string
  excludeId?: string
  clearable?: boolean
  disabled?: boolean
}>()
const model = defineModel<string | null>({ required: true, set: (value) => value || null })
const initialId = model.value
const error = ref<unknown>(null),
  captureScope = usePageScope()
const cached = computed(() =>
  model.value && model.value === initialId
    ? [{ value: model.value, label: props.selectedLabel || model.value }]
    : [],
)
interface TreeNode {
  level: number
  data: { value?: string }
}
interface TreeOption {
  value: string
  label: string
  leaf: boolean
}
async function load(node: TreeNode, resolve: (items: TreeOption[]) => void) {
  const active = captureScope()
  error.value = null
  try {
    const items: TreeOption[] = []
    for (let page = 1; page <= 10; page++) {
      const result = await getGroups({
        parentId: node.level ? node.data.value : undefined,
        page,
        size: 100,
      })
      if (!active()) return resolve([])
      items.push(
        ...result.items
          .filter((row) => row.groupId !== props.excludeId)
          .map((row) => ({ value: row.groupId, label: row.name, leaf: !row.hasChildren })),
      )
      if (page * result.size >= result.total) return resolve(items)
    }
    throw validationError('分组选项超出单层加载范围，请收窄分组层级。')
  } catch (cause) {
    if (active()) error.value = cause
    resolve([])
  }
}
</script>

<template>
  <div class="group-select">
    <ElTreeSelect
      v-model="model"
      lazy
      :load="load"
      node-key="value"
      check-strictly
      :props="{ label: 'label', children: 'children', isLeaf: 'leaf' }"
      :cache-data="cached"
      :clearable="clearable"
      :disabled="disabled"
      placeholder="选择视频分组"
    />
    <RequestError :error="error" />
  </div>
</template>

<style scoped>
.group-select {
  width: 100%;
  min-width: 200px;
}
</style>
