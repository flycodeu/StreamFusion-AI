<script setup lang="ts">
import { formError as validationError } from './form'
import { computed, onMounted, reactive, ref } from 'vue'
import {
  ElButton,
  ElDescriptions,
  ElDescriptionsItem,
  ElDialog,
  ElForm,
  ElFormItem,
  ElInput,
  ElMessage,
  ElMessageBox,
  ElOption,
  ElSelect,
  ElTable,
  ElTableColumn,
} from 'element-plus'
import * as api from '../../api/camera/api'
import type { Camera, CameraProfile } from '../../api/camera/types'
import RequestError from '../../components/feedback/RequestError.vue'
import TableActions from '../../components/table/TableActions.vue'
import ProfileEditor from './ProfileEditor.vue'
import SourceEditor from '../camera-sources/SourceEditor.vue'
import CreateDialog from './CreateDialog.vue'
import { connectionLabel, lifecycleLabels, usageLabel } from './form'
import { usePageScope } from '../../composables/usePageScope'
import { formatDateTime } from '../../utils/dateTime'

const props = defineProps<{ cameraId: string; canManage: boolean }>()
const emit = defineEmits<{ close: []; changed: [] }>()
const captureScope = usePageScope(),
  camera = ref<Camera | null>(null),
  error = ref<unknown>(null),
  saving = ref(false),
  loading = ref(false)
const profileEditor = ref<{ profileId: string | null } | null>(null)
const connectionEditor = ref(false),
  identifying = ref(false)
const childOpen = computed(
  () => !!profileEditor.value || connectionEditor.value || identifying.value,
)
const form = reactive({ name: '', remark: '', defaultPreviewProfileId: null as string | null })
let sequence = 0
const asProfile = (value: unknown) => value as CameraProfile
async function load() {
  const current = ++sequence,
    active = captureScope()
  loading.value = true
  error.value = null
  try {
    const result = await api.getCamera(props.cameraId)
    if (!active() || current !== sequence) return
    camera.value = result
    Object.assign(form, {
      name: result.name,
      remark: result.remark ?? '',
      defaultPreviewProfileId: result.defaultPreviewProfileId,
    })
  } catch (cause) {
    if (active() && current === sequence) error.value = cause
  } finally {
    if (active() && current === sequence) loading.value = false
  }
}
async function save() {
  if (saving.value || !camera.value) return
  if (!form.name.trim()) {
    error.value = validationError('请填写相机名称。')
    return
  }
  const active = captureScope()
  saving.value = true
  error.value = null
  try {
    const result = await api.updateCamera(props.cameraId, {
      version: camera.value.version,
      name: form.name.trim(),
      remark: form.remark.trim() || null,
      ...((form.defaultPreviewProfileId || null) !== camera.value.defaultPreviewProfileId
        ? { defaultPreviewProfileId: form.defaultPreviewProfileId || null }
        : {}),
    })
    if (!active()) return
    camera.value = result
    ElMessage.success('相机资料已保存')
    emit('changed')
  } catch (cause) {
    if (active()) error.value = cause
  } finally {
    saving.value = false
  }
}
async function removeProfile(profile: CameraProfile) {
  if (saving.value || !camera.value || !props.canManage) return
  const active = captureScope()
  saving.value = true
  try {
    await ElMessageBox.confirm(
      `删除码流“${profile.label}”？此操作只删除本地档案。`,
      '确认删除码流',
      { type: 'warning' },
    )
    if (!active()) return
    await api.deleteProfile(props.cameraId, profile.streamProfileId, profile.version)
    if (active()) {
      await load()
      emit('changed')
    }
  } catch (cause) {
    if (active() && cause !== 'cancel' && cause !== 'close') error.value = cause
  } finally {
    saving.value = false
  }
}
async function profileSaved() {
  const active = captureScope()
  profileEditor.value = null
  await load()
  if (active()) emit('changed')
}
async function connectionSaved() {
  connectionEditor.value = false
  await profileSaved()
}
async function identified() {
  identifying.value = false
  await profileSaved()
}
onMounted(load)
</script>
<template>
  <ElDialog
    :model-value="true"
    :title="
      connectionEditor
        ? '连接设置'
        : identifying
          ? '读取设备资料'
          : profileEditor
            ? '码流设置'
            : '相机详情'
    "
    width="960px"
    :close-on-click-modal="!saving && !childOpen"
    :close-on-press-escape="!saving && !childOpen"
    :show-close="!saving && !childOpen"
    @update:model-value="emit('close')"
  >
    <RequestError :error="error" />
    <template v-if="camera && !childOpen">
      <ElDescriptions :column="2" border
        ><ElDescriptionsItem label="相机 ID">{{ camera.cameraId }}</ElDescriptionsItem
        ><ElDescriptionsItem label="配置状态">{{
          lifecycleLabels[camera.lifecycle]
        }}</ElDescriptionsItem
        ><ElDescriptionsItem label="接入来源"
          >{{ camera.sourceDisplayName }}（{{
            connectionLabel(camera.sourceType, camera.connectionCategory)
          }}）</ElDescriptionsItem
        ><ElDescriptionsItem label="分组">{{ camera.groupPath || '待归档' }}</ElDescriptionsItem
        ><ElDescriptionsItem label="识别厂商">{{
          camera.deviceSummary?.manufacturer || '未获取'
        }}</ElDescriptionsItem
        ><ElDescriptionsItem label="识别型号">{{
          camera.deviceSummary?.model || '未获取'
        }}</ElDescriptionsItem
        ><ElDescriptionsItem v-if="camera.vendorHint" label="品牌备注（人工）">{{
          camera.vendorHint
        }}</ElDescriptionsItem
        ><ElDescriptionsItem label="创建时间">{{
          formatDateTime(camera.createdAt)
        }}</ElDescriptionsItem
        ><ElDescriptionsItem label="更新时间">{{
          formatDateTime(camera.updatedAt)
        }}</ElDescriptionsItem></ElDescriptions
      >
      <div v-if="canManage && camera.sourceId" class="profile-toolbar">
        <ElButton :disabled="saving || loading" @click="connectionEditor = true">连接设置</ElButton>
        <ElButton
          v-if="
            camera.sourceVersion &&
            (camera.connectionCategory === 'DEVICE' ||
              ['ONVIF', 'HIKVISION', 'DAHUA'].includes(camera.sourceType || ''))
          "
          :disabled="saving || loading"
          @click="identifying = true"
          >读取设备资料</ElButton
        >
      </div>
      <ElForm
        class="camera-details-form"
        label-width="110px"
        :disabled="saving || loading || childOpen"
        @submit.prevent="save"
      >
        <ElFormItem label="相机名称" required
          ><ElInput v-model="form.name" maxlength="128" /></ElFormItem
        ><ElFormItem label="备注"
          ><ElInput v-model="form.remark" type="textarea" maxlength="500"
        /></ElFormItem>
        <ElFormItem v-if="camera.profiles.length" label="默认码流"
          ><ElSelect v-model="form.defaultPreviewProfileId" clearable placeholder="不设置默认码流"
            ><ElOption
              v-for="profile in camera.profiles.filter(
                (row) => row.enabled && row.locatorSummary.configured,
              )"
              :key="profile.streamProfileId"
              :value="profile.streamProfileId"
              :label="profile.label" /></ElSelect
        ></ElFormItem>
      </ElForm>
      <div class="profile-toolbar">
        <strong>码流档案</strong
        ><ElButton
          v-if="canManage && camera.sourceType === 'RTSP'"
          :disabled="saving || loading"
          @click="profileEditor = { profileId: null }"
          >增加码流</ElButton
        >
      </div>
      <ElTable :data="camera.profiles" row-key="streamProfileId" border empty-text="暂无码流档案">
        <ElTableColumn prop="label" label="码流标签" min-width="140" /><ElTableColumn
          label="用途"
          min-width="100"
          ><template #default="{ row }">{{ usageLabel(row.usageHint) }}</template></ElTableColumn
        >
        <ElTableColumn label="配置状态" min-width="110"
          ><template #default="{ row }"
            >{{ row.enabled ? '已启用' : '已停用'
            }}{{
              row.streamProfileId === camera.defaultPreviewProfileId ? ' · 默认' : ''
            }}</template
          ></ElTableColumn
        >
        <ElTableColumn label="编码 / 分辨率" min-width="170"
          ><template #default="{ row }"
            >{{ row.videoCodec || '未获取' }} /
            {{ row.width && row.height ? `${row.width} × ${row.height}` : '未获取' }}</template
          ></ElTableColumn
        >
        <ElTableColumn label="帧率 / 码率" min-width="170"
          ><template #default="{ row }"
            >{{ row.frameRate == null ? '未获取' : `${row.frameRate} fps` }} /
            {{ row.bitrateKbps == null ? '未获取' : `${row.bitrateKbps} kbps` }}</template
          ></ElTableColumn
        >
        <ElTableColumn
          v-if="canManage"
          label="操作"
          min-width="174"
          class-name="table-actions-column"
          :resizable="false"
          ><template #default="{ row }"
            ><TableActions
              ><ElButton
                plain
                type="primary"
                :disabled="saving"
                @click="profileEditor = { profileId: row.streamProfileId }"
                >编辑</ElButton
              ><ElButton
                plain
                type="danger"
                :disabled="saving || row.streamProfileId === camera.defaultPreviewProfileId"
                @click="removeProfile(asProfile(row))"
                >删除</ElButton
              ></TableActions
            ></template
          ></ElTableColumn
        >
      </ElTable>
    </template>
    <template v-if="!childOpen" #footer
      ><ElButton :disabled="saving || childOpen" :loading="loading" @click="load">刷新资料</ElButton
      ><ElButton :disabled="saving || childOpen" @click="emit('close')">关闭</ElButton
      ><ElButton
        type="primary"
        :loading="saving"
        :disabled="loading || !camera || childOpen"
        @click="save"
        >保存本地资料</ElButton
      ></template
    >
    <ProfileEditor
      embedded
      v-if="profileEditor"
      :camera-id="cameraId"
      :profile-id="profileEditor.profileId"
      @close="profileEditor = null"
      @saved="profileSaved"
    />
    <SourceEditor
      embedded
      v-if="connectionEditor && camera?.sourceId"
      :source-id="camera.sourceId"
      @close="connectionEditor = false"
      @saved="connectionSaved"
    />
    <CreateDialog
      embedded
      v-if="identifying && camera?.sourceId && camera.sourceVersion"
      :initial-connection="{
        sourceId: camera.sourceId,
        sourceVersion: camera.sourceVersion,
        method: camera.sourceType || 'AUTO',
      }"
      :target-camera="
        !camera.deviceId && !camera.profiles.length
          ? { cameraId: camera.cameraId, version: camera.version, name: camera.name }
          : undefined
      "
      @close="identifying = false"
      @saved="identified"
    />
  </ElDialog>
</template>
<style scoped>
.camera-details-form {
  margin-top: 20px;
}
.profile-toolbar {
  display: flex;
  justify-content: space-between;
  align-items: center;
  margin: 16px 0;
}
</style>
