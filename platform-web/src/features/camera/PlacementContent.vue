<script setup lang="ts">
import { formError as validationError } from './form'
import { onMounted, ref, watch } from 'vue'
import { ElButton, ElForm, ElFormItem } from 'element-plus'
import { getCamera, updateCamera } from '../../api/camera/api'
import { previewCameraLifecycle, previewCameraMove } from '../../api/camera-groups/api'
import type { Camera } from '../../api/camera/types'
import type { Impact } from '../../api/camera-groups/types'
import GroupSelect from '../camera-groups/GroupSelect.vue'
import ImpactSummary from '../camera-groups/ImpactSummary.vue'
import RequestError from '../../components/feedback/RequestError.vue'
import { usePageScope } from '../../composables/usePageScope'

const props = defineProps<{
  cameraId: string
  action: 'move' | 'enable' | 'disable'
  initialGroupId?: string
}>()
const emit = defineEmits<{ close: []; saved: []; busy: [value: boolean] }>()
const captureScope = usePageScope(),
  camera = ref<Camera | null>(null),
  targetGroupId = ref<string | null>(null)
const saving = ref(false),
  error = ref<unknown>(null),
  impact = ref<Impact | null>(null)
watch(saving, (value) => emit('busy', value))
onMounted(async () => {
  const active = captureScope()
  try {
    const result = await getCamera(props.cameraId)
    if (active()) {
      camera.value = result
      targetGroupId.value = props.initialGroupId ?? result.groupId
    }
  } catch (cause) {
    if (active()) error.value = cause
  }
})
async function save() {
  if (saving.value || !camera.value) return
  if (props.action === 'move' && !targetGroupId.value) {
    error.value = validationError('请选择目标视频分组。')
    return
  }
  const active = captureScope()
  saving.value = true
  error.value = null
  try {
    if (!impact.value) {
      const result =
        props.action === 'move'
          ? await previewCameraMove(props.cameraId, camera.value.version, targetGroupId.value!)
          : await previewCameraLifecycle(
              props.cameraId,
              camera.value.version,
              props.action === 'enable' ? 'ENABLED' : 'DISABLED',
            )
      if (active()) impact.value = result
      return
    }
    await updateCamera(props.cameraId, {
      version: camera.value.version,
      confirmation: impact.value.confirmation,
      ...(props.action === 'move' ? { groupId: targetGroupId.value! } : {}),
      ...(impact.value.targetLifecycle ? { lifecycle: impact.value.targetLifecycle } : {}),
    })
    if (active()) emit('saved')
  } catch (cause) {
    if (active()) {
      error.value = cause
      impact.value = null
    }
  } finally {
    saving.value = false
  }
}
</script>
<template>
  <section class="placement-content">
    <RequestError :error="error" />
    <p v-if="camera">{{ camera.name }} · 当前分组：{{ camera.groupPath || '待归档' }}</p>
    <ElForm v-if="action === 'move'" :disabled="saving || !!impact" label-width="100px"
      ><ElFormItem label="目标分组" required
        ><GroupSelect
          v-model="targetGroupId"
          :selected-label="camera?.groupPath || undefined" /></ElFormItem
    ></ElForm>
    <ImpactSummary v-if="impact" :impact="impact" />
    <div class="placement-actions">
      <ElButton v-if="impact" :disabled="saving" @click="impact = null">重新选择</ElButton
      ><ElButton :disabled="saving" @click="emit('close')">取消</ElButton
      ><ElButton type="primary" :loading="saving" :disabled="!camera" @click="save">{{
        impact ? '确认并应用' : '查看影响'
      }}</ElButton>
    </div>
  </section>
</template>

<style scoped>
.placement-actions {
  display: flex;
  justify-content: flex-end;
  gap: 8px;
  margin-top: 24px;
}
.placement-actions :deep(.el-button + .el-button) {
  margin-left: 0;
}
</style>
