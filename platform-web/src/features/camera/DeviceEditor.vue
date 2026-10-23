<script setup lang="ts">
import { computed, onMounted, reactive, ref } from 'vue'
import {
  ElButton,
  ElDialog,
  ElForm,
  ElFormItem,
  ElInput,
  ElMessage,
  ElMessageBox,
  ElTabs,
  ElTabPane,
} from 'element-plus'
import * as api from '../../api/camera/api'
import type { CameraDeviceGroup } from '../../api/camera/types'
import RequestError from '../../components/feedback/RequestError.vue'
import { usePageScope } from '../../composables/usePageScope'
import { formError } from './form'
import SourceEditor from '../camera-sources/SourceEditor.vue'

const props = defineProps<{ device: CameraDeviceGroup }>()
const emit = defineEmits<{ close: []; saved: [name: string] }>()
const form = reactive({ name: '', remark: '' })
const initial = ref<{ name: string; remark: string; version: string } | null>(null)
const sourceId = ref<string | null>(null)
const tab = ref('basic')
const connectionEditor = ref<InstanceType<typeof SourceEditor> | null>(null)
const loading = ref(true),
  saving = ref(false),
  error = ref<unknown>(null)
const captureScope = usePageScope()
const dirty = computed(
  () => initial.value && (form.name !== initial.value.name || form.remark !== initial.value.remark),
)
async function load() {
  const active = captureScope()
  loading.value = true
  error.value = null
  try {
    const id = props.device.groupKey.slice(1)
    const value = props.device.identified ? await api.getCameraDevice(id) : await api.getCamera(id)
    if (!active()) return
    initial.value = { name: value.name, remark: value.remark ?? '', version: value.version }
    sourceId.value = value.sourceId
    Object.assign(form, { name: value.name, remark: value.remark ?? '' })
  } catch (cause) {
    if (active()) error.value = cause
  } finally {
    if (active()) loading.value = false
  }
}
async function discardBasic() {
  if (dirty.value) {
    try {
      await ElMessageBox.confirm('相机资料尚未保存，关闭会放弃这些修改。', '放弃未保存的修改？', {
        type: 'warning',
      })
    } catch {
      return false
    }
  }
  return true
}
async function beforeTabLeave(value: string | number) {
  if (saving.value) return false
  if (value === tab.value) return true
  const active = captureScope()
  if (tab.value === 'connection' && connectionEditor.value) {
    if (!(await connectionEditor.value.confirmLeave())) return false
  } else {
    if (!(await discardBasic())) return false
    if (initial.value) Object.assign(form, initial.value)
  }
  return active()
}
async function close() {
  if (saving.value) return
  const active = captureScope()
  if (!(await discardBasic())) return
  if (connectionEditor.value && !(await connectionEditor.value.confirmLeave())) return
  if (active()) emit('close')
}
function connectionSaved() {
  if (initial.value) emit('saved', initial.value.name)
}
async function save() {
  if (!initial.value || loading.value || saving.value) return
  if (!form.name.trim()) {
    error.value = formError('请填写相机名称。')
    return
  }
  const active = captureScope(),
    id = props.device.groupKey.slice(1)
  saving.value = true
  error.value = null
  try {
    const name = form.name.trim()
    const changes = {
      version: initial.value.version,
      ...(form.remark !== initial.value.remark ? { remark: form.remark.trim() || null } : {}),
    }
    const result = props.device.identified
      ? await api.updateCameraDevice(id, {
          ...changes,
          ...(name !== initial.value.name ? { localName: name } : {}),
        })
      : await api.updateCamera(id, { ...changes, ...(name !== initial.value.name ? { name } : {}) })
    if (active()) {
      ElMessage.success('相机资料已保存')
      emit('saved', result.name)
    }
  } catch (cause) {
    if (active()) error.value = cause
  } finally {
    if (active()) saving.value = false
  }
}
onMounted(load)
</script>
<template>
  <ElDialog
    :model-value="true"
    title="编辑相机"
    width="760px"
    class="management-dialog camera-edit-dialog"
    :before-close="close"
    :close-on-click-modal="!saving && !connectionEditor?.saving"
    :close-on-press-escape="!saving && !connectionEditor?.saving"
    :show-close="!saving && !connectionEditor?.saving"
  >
    <ElTabs v-model="tab" :before-leave="beforeTabLeave">
      <ElTabPane label="基本资料" name="basic" />
      <ElTabPane label="连接设置" name="connection" :disabled="loading || !sourceId || !initial" />
    </ElTabs>
    <template v-if="tab === 'basic'">
      <RequestError :error="error" />
      <ElButton v-if="!initial && error" link @click="load">重新读取相机资料</ElButton>
      <ElForm label-position="top" :disabled="loading || saving || !initial" @submit.prevent="save">
        <ElFormItem label="相机名称" required
          ><ElInput v-model="form.name" maxlength="128" show-word-limit
        /></ElFormItem>
        <ElFormItem label="备注"
          ><ElInput v-model="form.remark" type="textarea" :rows="3" maxlength="500" show-word-limit
        /></ElFormItem>
        <div v-if="device.manufacturer || device.model" class="device-observed">
          <span v-if="device.manufacturer">识别品牌：{{ device.manufacturer }}</span>
          <span v-if="device.model">型号：{{ device.model }}</span>
        </div>
      </ElForm>
    </template>
    <SourceEditor
      v-if="tab === 'connection' && sourceId"
      ref="connectionEditor"
      :source-id="sourceId"
      :manufacturer="device.manufacturer"
      embedded
      @close="tab = 'basic'"
      @saved="connectionSaved"
    />
    <template v-if="tab === 'basic'" #footer
      ><ElButton :disabled="saving" @click="close">取消</ElButton>
      <ElButton
        type="primary"
        :loading="saving"
        :disabled="loading || !initial || !dirty"
        @click="save"
        >保存</ElButton
      ></template
    >
  </ElDialog>
</template>
<style scoped>
.device-observed {
  display: flex;
  flex-wrap: wrap;
  gap: 8px 24px;
  color: var(--el-text-color-secondary);
  font-size: 13px;
  line-height: 1.6;
}
</style>
