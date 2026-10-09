<script setup lang="ts">
import { formError as validationError } from './form'
import { computed, onBeforeUnmount, onMounted, reactive, ref } from 'vue'
import {
  ElAlert,
  ElButton,
  ElForm,
  ElFormItem,
  ElInput,
  ElMessage,
  ElOption,
  ElSelect,
  ElSwitch,
} from 'element-plus'
import * as api from '../../api/camera/api'
import type { Camera, CameraProfile, InitialProfile, ProfileUpdate } from '../../api/camera/types'
import CameraDialogFrame from './CameraDialogFrame.vue'
import RequestError from '../../components/feedback/RequestError.vue'
import {
  isRequestRejected,
  usageLabel,
  usageOptions,
  useCreateRequest,
  effectiveUsage,
} from './form'
import type { UsageHint } from './form'
import { usePageScope } from '../../composables/usePageScope'

const props = defineProps<{ cameraId: string; profileId: string | null; embedded?: boolean }>()
const emit = defineEmits<{ close: []; saved: [] }>()
const captureScope = usePageScope(),
  camera = ref<Camera | null>(null),
  profile = ref<CameraProfile | null>(null),
  error = ref<unknown>(null),
  loading = ref(true),
  saving = ref(false)
const form = reactive({
  label: '',
  usageHint: 'UNKNOWN' as UsageHint,
  enabled: true,
  replacementDefaultProfileId: null as string | null,
})
const fullUrl = ref(''),
  replaceUrl = ref(!props.profileId)
type CreateProfile = Omit<InitialProfile, 'clientKey'> & { cameraVersion: string }
const creation = useCreateRequest<CreateProfile>()
const frozen = computed(() => !!creation.pending.value)
const replacingDefault = computed(
  () =>
    !!profile.value &&
    camera.value?.defaultPreviewProfileId === profile.value.streamProfileId &&
    !form.enabled,
)
const manual = computed(
  () =>
    !profile.value ||
    (profile.value.locatorSummary.editable ?? profile.value.locatorSummary.locatorKind === 'RTSP'),
)
onMounted(async () => {
  const active = captureScope()
  try {
    const current = await api.getCamera(props.cameraId)
    if (!active()) return
    camera.value = current
    if (props.profileId) {
      const selected = current.profiles.find((row) => row.streamProfileId === props.profileId)
      if (!selected) throw validationError('码流已不存在，请刷新相机详情。')
      profile.value = selected
      Object.assign(form, {
        label: selected.label,
        usageHint: selected.usageHint,
        enabled: selected.enabled,
      })
    }
  } catch (cause) {
    if (active()) error.value = cause
  } finally {
    if (active()) loading.value = false
  }
})
async function save() {
  if (saving.value || !camera.value || loading.value) return
  error.value = null
  const validation =
    props.profileId && !form.label.trim()
      ? '请填写码流标签。'
      : manual.value && replaceUrl.value && !fullUrl.value.trim()
        ? '请填写完整的 RTSP 地址。'
        : ''
  if (validation && !frozen.value) {
    error.value = validationError(validation)
    return
  }
  const active = captureScope()
  saving.value = true
  try {
    if (profile.value) {
      const input: ProfileUpdate = {
        version: profile.value.version,
        ...(form.label.trim() !== profile.value.label ? { label: form.label.trim() } : {}),
        ...(form.usageHint !== profile.value.usageHint ? { usageHint: form.usageHint } : {}),
        ...(form.enabled !== profile.value.enabled ? { enabled: form.enabled } : {}),
        ...(manual.value && replaceUrl.value
          ? { locator: { fullUrl: fullUrl.value.trim(), transport: 'TCP' as const } }
          : {}),
        ...(replacingDefault.value
          ? {
              cameraVersion: camera.value.version,
              replacementDefaultProfileId: form.replacementDefaultProfileId || null,
            }
          : {}),
      }
      await api.updateProfile(props.cameraId, profile.value.streamProfileId, input)
    } else
      await api.createProfile(
        props.cameraId,
        creation.capture({
          cameraVersion: camera.value.version,
          label:
            form.label.trim() ||
            (form.usageHint === 'UNKNOWN'
              ? `码流 ${camera.value.profiles.length + 1}`
              : usageLabel(form.usageHint)),
          usageHint: form.usageHint,
          enabled: form.enabled,
          locatorKind: 'RTSP',
          locator: { fullUrl: fullUrl.value.trim(), transport: 'TCP' },
        }),
      )
    if (!active()) return
    creation.reset()
    fullUrl.value = ''
    ElMessage.success('码流档案已保存，尚未验证媒体')
    emit('saved')
  } catch (cause) {
    if (active()) {
      if (isRequestRejected(cause)) creation.reset()
      error.value = cause
    }
  } finally {
    saving.value = false
  }
}
onBeforeUnmount(() => {
  fullUrl.value = ''
  creation.reset()
})
</script>

<template>
  <CameraDialogFrame
    :embedded="embedded"
    :model-value="true"
    :title="profileId ? '编辑码流档案' : '增加 RTSP 码流'"
    width="660px"
    destroy-on-close
    :close-on-click-modal="!saving"
    :close-on-press-escape="!saving"
    :show-close="!saving"
    @update:model-value="emit('close')"
  >
    <RequestError :error="error" />
    <ElAlert
      v-if="frozen && !saving"
      type="warning"
      :closable="false"
      title="保存结果未确认，请重试。"
    />
    <ElForm label-width="120px" :disabled="loading || saving || frozen" @submit.prevent="save">
      <ElFormItem label="码流标签" :required="!!profileId"
        ><ElInput
          v-model="form.label"
          maxlength="64"
          :placeholder="profileId ? '' : '留空则自动命名'"
      /></ElFormItem>
      <p v-if="profile?.classification?.origin === 'NAME_RULE'" class="classification-note">
        已自动识别为{{ usageLabel(effectiveUsage(profile)) }}；用途保持“未指定”时沿用自动识别。
      </p>
      <ElFormItem label="用途"
        ><ElSelect v-model="form.usageHint"
          ><ElOption
            v-for="option in usageOptions"
            :key="option.value"
            :value="option.value"
            :label="option.label" /></ElSelect
      ></ElFormItem>
      <ElFormItem label="允许新使用"><ElSwitch v-model="form.enabled" /></ElFormItem>
      <ElFormItem v-if="replacingDefault" label="新的默认码流"
        ><ElSelect v-model="form.replacementDefaultProfileId" clearable placeholder="清空默认码流"
          ><ElOption
            v-for="item in camera?.profiles.filter(
              (row) => row.enabled && row.streamProfileId !== profileId,
            )"
            :key="item.streamProfileId"
            :value="item.streamProfileId"
            :label="item.label" /></ElSelect
        ><span>当前默认码流将停用，需明确换选或清空。</span></ElFormItem
      >
      <template v-if="manual">
        <ElFormItem v-if="profileId" label="视频地址"
          ><ElSelect v-model="replaceUrl"
            ><ElOption :value="false" label="保留已保存地址" /><ElOption
              :value="true"
              label="替换视频地址" /></ElSelect
        ></ElFormItem>
        <ElFormItem v-if="replaceUrl" label="RTSP 地址" required
          ><ElInput
            v-model="fullUrl"
            type="password"
            show-password
            autocomplete="off"
            maxlength="8192"
            placeholder="rtsp://192.168.1.10:554/实际视频路径"
        /></ElFormItem>
        <p>认证信息沿用连接设置。</p>
      </template>
      <p v-else>地址由设备或平台提供。</p>
    </ElForm>
    <template #footer
      ><ElButton :disabled="saving" @click="emit('close')">关闭</ElButton
      ><ElButton type="primary" :loading="saving" :disabled="loading || !camera" @click="save">{{
        frozen ? '重试原请求' : '保存'
      }}</ElButton></template
    >
  </CameraDialogFrame>
</template>
