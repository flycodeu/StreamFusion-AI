<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { ElTreeSelect } from 'element-plus'
import { getGroupTree } from '../../api/camera-groups/api'
import type { CameraGroup } from '../../api/camera-groups/types'
import RequestError from '../../components/feedback/RequestError.vue'
import { usePageScope } from '../../composables/usePageScope'
import { groupTree } from './tree'

const props = defineProps<{
  selectedLabel?: string
  excludeId?: string
  clearable?: boolean
  disabled?: boolean
}>()
const model = defineModel<string | null>({ required: true, set: (value) => value || null })
const groups = ref<CameraGroup[]>([]),
  loading = ref(false),
  error = ref<unknown>(null)
const captureScope = usePageScope()
const nodes = computed(() => groupTree(groups.value, props.excludeId))
onMounted(async () => {
  const active = captureScope()
  loading.value = true
  try {
    const result = await getGroupTree()
    if (active()) groups.value = result
  } catch (cause) {
    if (active()) error.value = cause
  } finally {
    if (active()) loading.value = false
  }
})
</script>
<template>
  <div class="group-select">
    <ElTreeSelect
      v-model="model"
      :data="nodes"
      node-key="groupId"
      check-strictly
      filterable
      :props="{ label: 'name', children: 'children' }"
      :render-after-expand="false"
      :clearable="clearable"
      :disabled="disabled || loading"
      placeholder="选择视频分组"
    />
    <RequestError :error="error" />
  </div>
</template>
<style scoped>
.group-select {
  width: 100%;
  min-width: 0;
}
</style>
