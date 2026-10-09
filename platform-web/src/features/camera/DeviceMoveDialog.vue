<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { ElButton, ElDialog, vLoading } from 'element-plus'
import { getGroupTree, previewDeviceMove, moveDevice } from '../../api/camera-groups/api'
import type { CameraGroup } from '../../api/camera-groups/types'
import type { CameraDeviceGroup } from '../../api/camera/types'
import GroupTree from '../camera-groups/GroupTree.vue'
import ImpactSummary from '../camera-groups/ImpactSummary.vue'
import RequestError from '../../components/feedback/RequestError.vue'
import { usePageScope } from '../../composables/usePageScope'
import { formError } from './form'

const props = defineProps<{ device: CameraDeviceGroup }>()
const emit = defineEmits<{ close: []; saved: [] }>()
const groups = ref<CameraGroup[]>([]),
  target = ref<string | null>(null),
  loading = ref(false),
  saving = ref(false),
  error = ref<unknown>(null)
const preview = ref<Awaited<ReturnType<typeof previewDeviceMove>> | null>(null)
const captureScope = usePageScope()
async function load() {
  const active = captureScope()
  loading.value = true
  error.value = null
  try {
    const result = await getGroupTree()
    if (active()) groups.value = result
  } catch (cause) {
    if (active()) error.value = cause
  } finally {
    if (active()) loading.value = false
  }
}
async function save() {
  if (saving.value || loading.value) return
  if (!target.value) {
    error.value = formError('请选择目标分组。')
    return
  }
  const active = captureScope()
  saving.value = true
  error.value = null
  try {
    if (!preview.value) {
      const result = await previewDeviceMove(props.device.groupKey, target.value)
      if (active()) preview.value = result
      return
    }
    await moveDevice(props.device.groupKey, target.value, preview.value.impact.confirmation)
    if (active()) emit('saved')
  } catch (cause) {
    if (active()) {
      error.value = cause
      preview.value = null
    }
  } finally {
    if (active()) saving.value = false
  }
}
onMounted(load)
</script>
<template>
  <ElDialog
    :model-value="true"
    title="移动整台相机"
    class="management-dialog"
    width="640px"
    :close-on-click-modal="!saving"
    :close-on-press-escape="!saving"
    :show-close="!saving"
    @close="emit('close')"
  >
    <div class="move-device-name">{{ device.name }}</div>
    <p class="move-note">全部通道一起移动，包含当前列表筛选未显示的通道。</p>
    <RequestError :error="error" />
    <div v-if="!preview" v-loading="loading">
      <GroupTree
        v-model="target"
        :groups="groups"
        :show-all="false"
        :counts="false"
        :disabled="saving || loading"
      />
      <ElButton v-if="error" link @click="load">刷新分组</ElButton>
    </div>
    <template v-else>
      <dl class="move-origins">
        <div v-for="place in preview.placements" :key="place.groupPath">
          <dt>{{ place.groupPath }}</dt>
          <dd>{{ place.channelCount }} 个通道</dd>
        </div>
      </dl>
      <ImpactSummary :impact="preview.impact" />
    </template>
    <template #footer>
      <ElButton v-if="preview" :disabled="saving" @click="preview = null">重新选择</ElButton>
      <ElButton :disabled="saving" @click="emit('close')">取消</ElButton>
      <ElButton type="primary" :loading="saving" :disabled="loading || !target" @click="save">{{
        preview ? '确认移动全部通道' : '下一步：确认影响'
      }}</ElButton>
    </template>
  </ElDialog>
</template>
<style scoped>
.move-device-name {
  font-size: 16px;
  font-weight: 600;
  overflow-wrap: anywhere;
}
.move-note {
  color: var(--text-secondary);
}
.move-origins > div {
  display: flex;
  justify-content: space-between;
  gap: 16px;
  margin: 10px 0;
}
.move-origins dt {
  overflow-wrap: anywhere;
}
.move-origins dd {
  flex-shrink: 0;
  margin: 0;
}
</style>
