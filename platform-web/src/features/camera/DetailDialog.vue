<script setup lang="ts">
import { formError as validationError } from './form'
import { computed, onMounted, reactive, ref } from 'vue'
import {
  ElButton,
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
  ElTabs,
  ElTabPane,
  ElTag,
} from 'element-plus'
import * as api from '../../api/camera/api'
import type { Camera, CameraProfile } from '../../api/camera/types'
import RequestError from '../../components/feedback/RequestError.vue'
import TableActions from '../../components/table/TableActions.vue'
import ProfileEditor from './ProfileEditor.vue'
import SourceEditor from '../camera-sources/SourceEditor.vue'
import CreateDialog from './CreateDialog.vue'
import { connectionLabel, lifecycleLabels, usageLabel, effectiveUsage, streamLabel } from './form'
import { usePageScope } from '../../composables/usePageScope'
import { formatDateTime } from '../../utils/dateTime'

const props = defineProps<{ cameraId: string; canManage: boolean; deviceName?: string }>()
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
const tab = ref('basic')
const dirty = computed(
  () =>
    !!camera.value &&
    (form.name !== camera.value.name ||
      form.remark !== (camera.value.remark ?? '') ||
      form.defaultPreviewProfileId !== camera.value.defaultPreviewProfileId),
)
let sequence = 0
const asProfile = (value: unknown) => value as CameraProfile
function resetDraft() {
  if (!camera.value) return
  Object.assign(form, {
    name: camera.value.name,
    remark: camera.value.remark ?? '',
    defaultPreviewProfileId: camera.value.defaultPreviewProfileId,
  })
}
async function close() {
  if (saving.value || childOpen.value) return
  const active = captureScope()
  if (dirty.value) {
    try {
      await ElMessageBox.confirm('本地资料尚未保存，关闭会放弃这些修改。', '放弃未保存的修改？', {
        confirmButtonText: '放弃并关闭',
        cancelButtonText: '继续编辑',
        type: 'warning',
      })
    } catch {
      return
    }
  }
  if (active()) emit('close')
}
async function load() {
  // Shared settings reload the camera; finish the local draft before starting them.
  if (dirty.value) return
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
    error.value = validationError('请填写通道名称。')
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
    resetDraft()
    ElMessage.success('通道资料已保存')
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
    append-to-body
    :title="
      connectionEditor
        ? '连接设置'
        : identifying
          ? '读取设备资料'
          : profileEditor
            ? '码流设置'
            : '通道详情'
    "
    width="900px"
    class="management-dialog camera-detail-dialog"
    :before-close="close"
    :close-on-click-modal="!saving && !childOpen"
    :close-on-press-escape="!saving && !childOpen"
    :show-close="!saving && !childOpen"
  >
    <RequestError :error="error" />
    <section v-if="camera && !childOpen" class="camera-detail-workspace">
      <header class="camera-summary">
        <div>
          <h2>{{ camera.name }}</h2>
          <p v-if="(deviceName || camera.sourceDisplayName) !== camera.name">
            {{ deviceName || camera.sourceDisplayName }}
          </p>
        </div>
        <ElTag :type="camera.lifecycle === 'ENABLED' ? 'success' : 'info'">
          {{ lifecycleLabels[camera.lifecycle] }}
        </ElTag>
      </header>
      <ElTabs v-model="tab" class="camera-detail-tabs">
        <ElTabPane label="基本资料" name="basic">
          <ElForm
            class="camera-details-form"
            label-position="top"
            :disabled="saving || loading"
            @submit.prevent="save"
          >
            <ElFormItem label="通道名称" required
              ><ElInput v-model="form.name" maxlength="128"
            /></ElFormItem>
            <ElFormItem label="备注"
              ><ElInput v-model="form.remark" type="textarea" :rows="2" maxlength="500"
            /></ElFormItem>
          </ElForm>
          <dl class="detail-facts">
            <div>
              <dt>视频分组</dt>
              <dd>{{ camera.groupPath || '待归档' }}</dd>
            </div>
            <div>
              <dt>接入类型</dt>
              <dd>{{ connectionLabel(camera.sourceType, camera.connectionCategory) }}</dd>
            </div>
          </dl>
          <details class="camera-record-meta">
            <summary>档案信息</summary>
            <dl class="detail-facts">
              <div>
                <dt>相机 ID</dt>
                <dd>{{ camera.cameraId }}</dd>
              </div>
              <div>
                <dt>创建时间</dt>
                <dd>{{ formatDateTime(camera.createdAt) }}</dd>
              </div>
              <div>
                <dt>更新时间</dt>
                <dd>{{ formatDateTime(camera.updatedAt) }}</dd>
              </div>
            </dl>
          </details>
        </ElTabPane>
        <ElTabPane label="码流档案" name="profiles">
          <div v-if="canManage && camera.sourceType === 'RTSP'" class="detail-section-heading">
            <ElButton
              :disabled="saving || loading || dirty"
              @click="profileEditor = { profileId: null }"
              >增加码流</ElButton
            >
          </div>
          <ElForm label-position="top" :disabled="saving || loading" @submit.prevent="save">
            <ElFormItem v-if="camera.profiles.length" label="默认码流">
              <ElSelect
                v-model="form.defaultPreviewProfileId"
                clearable
                placeholder="不设置默认码流"
              >
                <ElOption
                  v-for="profile in camera.profiles.filter(
                    (row) => row.enabled && row.locatorSummary.configured,
                  )"
                  :key="profile.streamProfileId"
                  :value="profile.streamProfileId"
                  :label="streamLabel(profile)"
                />
              </ElSelect>
            </ElFormItem>
          </ElForm>
          <ElTable
            :data="camera.profiles"
            row-key="streamProfileId"
            border
            empty-text="暂无码流档案"
          >
            <ElTableColumn label="码流" min-width="200"
              ><template #default="{ row }"
                ><div>{{ streamLabel(asProfile(row)) }}</div>
                <span
                  v-if="streamLabel(asProfile(row)) !== row.label"
                  class="table-secondary stream-source-label"
                  >{{ row.label }}</span
                ></template
              ></ElTableColumn
            >
            <ElTableColumn label="用途" width="90"
              ><template #default="{ row }">{{
                usageLabel(effectiveUsage(asProfile(row)))
              }}</template></ElTableColumn
            >
            <ElTableColumn label="配置状态" width="120"
              ><template #default="{ row }"
                >{{ row.enabled ? '已启用' : '已停用'
                }}{{
                  row.streamProfileId === camera.defaultPreviewProfileId ? ' · 默认' : ''
                }}</template
              ></ElTableColumn
            >
            <ElTableColumn label="编码 / 分辨率" min-width="150"
              ><template #default="{ row }"
                ><div>{{ row.videoCodec || '未获取' }}</div>
                <span class="table-secondary">{{
                  row.width && row.height ? `${row.width} × ${row.height}` : '未获取'
                }}</span></template
              ></ElTableColumn
            >
            <ElTableColumn label="帧率 / 码率" min-width="150"
              ><template #default="{ row }"
                ><div>{{ row.frameRate == null ? '未获取' : `${row.frameRate} fps` }}</div>
                <span class="table-secondary">{{
                  row.bitrateKbps == null ? '未获取' : `${row.bitrateKbps} kbps`
                }}</span></template
              ></ElTableColumn
            >
            <ElTableColumn
              v-if="canManage"
              label="操作"
              width="126"
              class-name="table-actions-column"
              :resizable="false"
            >
              <template #default="{ row }"
                ><TableActions compact>
                  <ElButton
                    link
                    type="primary"
                    :disabled="saving || loading || dirty"
                    @click="profileEditor = { profileId: row.streamProfileId }"
                    >编辑</ElButton
                  >
                  <ElButton
                    link
                    type="danger"
                    :disabled="
                      saving ||
                      loading ||
                      dirty ||
                      row.streamProfileId === camera.defaultPreviewProfileId
                    "
                    @click="removeProfile(asProfile(row))"
                    >删除</ElButton
                  >
                </TableActions></template
              >
            </ElTableColumn>
          </ElTable>
        </ElTabPane>
        <ElTabPane label="设备连接" name="connection">
          <dl class="detail-facts">
            <div>
              <dt>接入来源</dt>
              <dd>{{ camera.sourceDisplayName }}</dd>
            </div>
            <div>
              <dt>接入类型</dt>
              <dd>{{ connectionLabel(camera.sourceType, camera.connectionCategory) }}</dd>
            </div>
            <div>
              <dt>识别厂商</dt>
              <dd>{{ camera.deviceSummary?.manufacturer || '未获取' }}</dd>
            </div>
            <div>
              <dt>识别型号</dt>
              <dd>{{ camera.deviceSummary?.model || '未获取' }}</dd>
            </div>
            <div v-if="camera.vendorHint">
              <dt>品牌备注（人工）</dt>
              <dd>{{ camera.vendorHint }}</dd>
            </div>
          </dl>
          <div v-if="canManage && camera.sourceId" class="connection-actions">
            <div class="detail-action-buttons">
              <ElButton :disabled="saving || loading || dirty" @click="connectionEditor = true"
                >连接设置</ElButton
              >
              <ElButton
                v-if="
                  camera.sourceVersion &&
                  (camera.connectionCategory === 'DEVICE' ||
                    ['ONVIF', 'HIKVISION', 'DAHUA'].includes(camera.sourceType || ''))
                "
                :disabled="saving || loading || dirty"
                @click="identifying = true"
                >读取设备资料</ElButton
              >
            </div>
          </div>
        </ElTabPane>
      </ElTabs>
    </section>
    <template v-if="!childOpen" #footer
      ><p v-if="dirty" class="draft-note" role="status">请先保存或撤销修改，再维护码流或连接。</p>
      <div class="detail-footer-actions">
        <ElButton :disabled="saving || dirty" :loading="loading" @click="load">刷新资料</ElButton>
        <ElButton v-if="dirty" :disabled="saving" @click="resetDraft">撤销修改</ElButton>
        <span class="footer-spacer" />
        <ElButton :disabled="saving" @click="close">关闭</ElButton
        ><ElButton
          type="primary"
          :loading="saving"
          :disabled="loading || !camera || childOpen"
          @click="save"
          >保存本地资料</ElButton
        >
      </div></template
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
.stream-source-label {
  display: block;
  overflow-wrap: anywhere;
  line-height: 1.4;
}
.camera-detail-workspace {
  display: flex;
  flex-direction: column;
  min-height: 0;
  flex: 1;
}
.camera-summary {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 16px;
  padding-bottom: 12px;
}
.camera-summary > div {
  min-width: 0;
}
.camera-summary h2 {
  margin: 0 0 8px;
  font-size: 18px;
  color: var(--text-primary);
  overflow-wrap: anywhere;
}
.camera-summary p {
  margin: 0;
  line-height: 1.6;
  color: var(--text-secondary);
  overflow-wrap: anywhere;
}
.camera-summary .el-tag {
  flex-shrink: 0;
}
.camera-detail-tabs {
  display: flex;
  flex-direction: column;
  flex: 1;
  min-height: 0;
}
.camera-detail-tabs :deep(.el-tabs__header) {
  flex-shrink: 0;
  margin-bottom: 0;
}
.camera-detail-tabs :deep(.el-tabs__content) {
  overflow: auto;
  min-height: 0;
  padding-top: 16px;
}
.camera-details-form {
  max-width: none;
}
.detail-facts {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: 14px 24px;
  margin: 0;
  padding: 12px 0;
}
.detail-facts dt {
  margin-bottom: 8px;
  font-size: 13px;
  color: var(--text-secondary);
}
.detail-facts dd {
  margin: 0;
  line-height: 1.6;
  overflow-wrap: anywhere;
}
.camera-record-meta {
  margin-top: 12px;
  border-top: 1px solid var(--border-subtle);
  padding-top: 16px;
}
.camera-record-meta summary {
  cursor: pointer;
  color: var(--text-secondary);
}
.detail-section-heading,
.detail-action-buttons,
.detail-footer-actions {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 8px;
}
.detail-section-heading {
  justify-content: space-between;
  margin-bottom: 16px;
}
.connection-actions {
  margin-top: 12px;
  padding-top: 8px;
  border-top: 1px solid var(--border-subtle);
}
.draft-note {
  font-size: 13px;
  line-height: 1.6;
  color: var(--text-secondary);
}
.draft-note {
  margin: 0 0 12px;
  text-align: left;
}
.detail-action-buttons :deep(.el-button + .el-button),
.detail-footer-actions :deep(.el-button + .el-button) {
  margin-left: 0;
}
.footer-spacer {
  flex: 1;
}
@media (max-width: 680px) {
  .camera-summary {
    align-items: flex-start;
    padding-bottom: 12px;
  }
  .camera-summary h2 {
    font-size: 18px;
  }
  .detail-facts {
    grid-template-columns: minmax(0, 1fr);
    gap: 16px;
  }
  .footer-spacer {
    display: none;
  }
  .detail-footer-actions {
    justify-content: flex-end;
  }
}
</style>
