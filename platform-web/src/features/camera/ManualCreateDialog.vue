<script setup lang="ts">
import { computed, onBeforeUnmount, ref } from 'vue'
import { ElAlert, ElButton, ElDialog, ElForm, ElFormItem, ElInput } from 'element-plus'
import { createCamera } from '../../api/camera/api'
import type { ManualCameraCreate } from '../../api/camera/types'
import { usePageScope } from '../../composables/usePageScope'
import RequestError from '../../components/feedback/RequestError.vue'
import GroupSelect from '../camera-groups/GroupSelect.vue'
import DeviceConnectionFields from './DeviceConnectionFields.vue'
import PlacementContent from './PlacementContent.vue'
import { formError, isRequestRejected, useCreateRequest } from './form'
import { manualCameraError, manualCameraInput, newManualCamera } from './manual'
import { useDeviceAdapters } from '../camera-access/useDeviceAdapters'

const props = withDefaults(defineProps<{ storageReady?: boolean }>(), { storageReady: true })
const adapters = useDeviceAdapters()
const emit = defineEmits<{ close: []; saved: [cameraIds: string[]]; rtsp: [] }>()
const captureScope = usePageScope(),
  form = ref(newManualCamera()),
  groupId = ref<string | null>(null)
const error = ref<unknown>(null),
  saving = ref(false),
  cameraId = ref<string | null>(null),
  placing = ref(false),
  assigned = ref(false)
const creation = useCreateRequest<Omit<ManualCameraCreate, 'clientRequestId'>>()
const frozen = computed(() => !!creation.pending.value)
async function save() {
  if (saving.value || cameraId.value) return
  error.value = null
  const message = frozen.value ? '' : manualCameraError(form.value)
  if (message) {
    error.value = formError(message)
    return
  }
  if (!frozen.value && !props.storageReady) {
    error.value = formError('部署密钥尚未配置，请联系系统管理员。')
    return
  }
  const active = captureScope()
  saving.value = true
  try {
    const result = await createCamera(creation.capture(manualCameraInput(form.value)))
    if (!active()) return
    cameraId.value = result.cameraId
    creation.reset()
    form.value.username = ''
    form.value.password = ''
    placing.value = !!groupId.value
  } catch (cause) {
    if (active()) {
      error.value = cause
      if (isRequestRejected(cause)) creation.reset()
    }
  } finally {
    if (active()) saving.value = false
  }
}
function close() {
  if (cameraId.value) emit('saved', [cameraId.value])
  else emit('close')
}
function placed() {
  placing.value = false
  assigned.value = true
}
onBeforeUnmount(() => {
  creation.reset()
  form.value.username = ''
  form.value.password = ''
})
</script>
<template>
  <ElDialog
    :model-value="true"
    title="手动添加相机"
    width="min(680px, calc(100vw - 32px))"
    :close-on-click-modal="false"
    :show-close="!saving && !placing"
    :close-on-press-escape="!saving && !placing"
    @update:model-value="close"
  >
    <PlacementContent
      v-if="cameraId && placing"
      :camera-id="cameraId"
      action="move"
      :initial-group-id="groupId || undefined"
      @close="placing = false"
      @saved="placed"
    />
    <template v-else>
      <RequestError :error="error" />
      <template v-if="cameraId">
        <ElAlert
          type="success"
          :closable="false"
          :title="assigned ? '已保存并归档' : '已保存为待归档'"
        />
      </template>
      <template v-else>
        <ElAlert v-if="frozen" type="warning" :closable="false" title="保存结果未确认，请重试。" />
        <ElForm label-width="110px" :disabled="saving || frozen" @submit.prevent="save">
          <ElFormItem label="相机名称" required
            ><ElInput v-model="form.name" maxlength="128" placeholder="例如：高炉东侧"
          /></ElFormItem>
          <DeviceConnectionFields v-model="form" :adapters="adapters" />
          <ElFormItem label="视频分组"
            ><GroupSelect v-model="groupId" clearable /><span class="field-note"
              >可留空，稍后分组。</span
            ></ElFormItem
          >
          <ElFormItem label="备注"
            ><ElInput v-model="form.remark" type="textarea" maxlength="500"
          /></ElFormItem>
        </ElForm>
        <ElButton link type="primary" :disabled="saving || frozen" @click="emit('rtsp')"
          >已有完整 RTSP 地址？按地址登记</ElButton
        >
      </template>
    </template>
    <template v-if="!placing" #footer>
      <ElButton :disabled="saving || placing" @click="close">{{
        cameraId ? '完成' : '关闭'
      }}</ElButton>
      <ElButton v-if="cameraId && !assigned" type="primary" @click="placing = true"
        >选择分组并归档</ElButton
      >
      <ElButton v-else-if="!cameraId" type="primary" :loading="saving" @click="save">{{
        frozen ? '重试原保存' : '保存相机档案'
      }}</ElButton>
    </template>
  </ElDialog>
</template>
<style scoped>
.field-note {
  color: var(--el-text-color-secondary);
  font-size: 13px;
  line-height: 1.6;
}
</style>
