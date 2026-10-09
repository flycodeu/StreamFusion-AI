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
} from 'element-plus'
import * as api from '../../api/camera/api'
import type { CameraDeviceGroup } from '../../api/camera/types'
import RequestError from '../../components/feedback/RequestError.vue'
import { usePageScope } from '../../composables/usePageScope'
import { formError } from './form'

const props = defineProps<{ device: CameraDeviceGroup }>()
const emit = defineEmits<{ close: []; saved: [name: string] }>()
const form = reactive({ name: '', remark: '' })
const initial = ref<{ name: string; remark: string; version: string } | null>(null)
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
    Object.assign(form, { name: value.name, remark: value.remark ?? '' })
  } catch (cause) {
    if (active()) error.value = cause
  } finally {
    if (active()) loading.value = false
  }
}
async function close() {
  if (saving.value) return
  const active = captureScope()
  if (dirty.value) {
    try {
      await ElMessageBox.confirm('相机资料尚未保存，关闭会放弃这些修改。', '放弃未保存的修改？', {
        type: 'warning',
      })
    } catch {
      return
    }
  }
  if (active()) emit('close')
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
    width="600px"
    class="management-dialog"
    :before-close="close"
    :close-on-click-modal="!saving"
    :close-on-press-escape="!saving"
    :show-close="!saving"
  >
    <RequestError :error="error" />
    <ElButton v-if="!initial && error" link @click="load">重新读取相机资料</ElButton>
    <ElForm label-position="top" :disabled="loading || saving || !initial" @submit.prevent="save">
      <ElFormItem label="相机名称" required
        ><ElInput v-model="form.name" maxlength="128" show-word-limit
      /></ElFormItem>
      <ElFormItem label="备注"
        ><ElInput v-model="form.remark" type="textarea" :rows="3" maxlength="500" show-word-limit
      /></ElFormItem>
      <p class="device-edit-note">名称与备注保存在本平台，后续同步会保留。</p>
    </ElForm>
    <template #footer
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
.device-edit-note {
  color: var(--text-secondary);
  font-size: 13px;
  line-height: 1.6;
}
</style>
