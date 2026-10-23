<script setup lang="ts">
import { computed, onBeforeUnmount, ref, watch } from 'vue'
import {
  ElDialog,
  ElUpload,
  ElButton,
  ElAlert,
  ElProgress,
  ElMessage,
  type UploadUserFile,
} from 'element-plus'
import type { StorageInstance, StorageScope } from '../../api/storage/types'
import { uploadFile } from '../../api/storage/api'
import RequestError from '../../components/feedback/RequestError.vue'

const visible = defineModel<boolean>({ required: true })
const props = defineProps<{ instance: StorageInstance; scope: StorageScope; prefix: string }>()
const emit = defineEmits<{ updated: [] }>()
const files = ref<UploadUserFile[]>([])
const uploading = ref(false),
  stopAfterCurrent = ref(false),
  processed = ref(0)
const error = ref<unknown>(null),
  succeeded = ref(0),
  failed = ref(0)
const maxBytes = 20 * 1024 * 1024
onBeforeUnmount(() => {
  stopAfterCurrent.value = true
})
const destination = computed(() => `${props.scope.bucket}/${props.scope.prefix}${props.prefix}`)
watch(visible, (open) => {
  if (open) {
    files.value = []
    processed.value = 0
    error.value = null
    succeeded.value = 0
    failed.value = 0
    stopAfterCurrent.value = false
  }
})
async function submit() {
  if (uploading.value) return
  const pending = files.value.filter((file) => file.status === 'ready')
  if (!pending.length) return
  uploading.value = true
  stopAfterCurrent.value = false
  error.value = null
  try {
    for (const file of pending) {
      if (stopAfterCurrent.value) break
      file.status = 'uploading'
      if (!file.raw || file.raw.size > maxBytes) {
        file.status = 'fail'
        failed.value++
        processed.value++
        ElMessage.warning(`“${file.name}”超过单文件 20 MiB 限制`)
        continue
      }
      try {
        await uploadFile(
          props.scope.scopeId,
          file.raw,
          props.prefix,
          props.instance.version,
          props.scope.version,
          crypto.randomUUID(),
        )
        file.status = 'success'
        succeeded.value++
      } catch (e) {
        file.status = 'fail'
        failed.value++
        error.value = e
      }
      processed.value++
    }
  } finally {
    uploading.value = false
    emit('updated')
  }
}
</script>

<template>
  <ElDialog
    v-model="visible"
    title="上传文件"
    width="620px"
    align-center
    class="management-dialog storage-upload-dialog"
    :close-on-click-modal="false"
    :close-on-press-escape="!uploading"
    :show-close="!uploading"
    destroy-on-close
  >
    <p class="destination">上传到：{{ destination }}</p>
    <ElUpload
      v-model:file-list="files"
      :auto-upload="false"
      multiple
      :limit="10"
      :disabled="uploading"
      :on-exceed="() => ElMessage.warning('每次最多选择 10 个文件')"
    >
      <ElButton :disabled="uploading">选择文件</ElButton>
      <template #tip
        ><div class="upload-tip">
          每次最多 10 个文件，单文件不超过 20 MiB；同名文件不会被覆盖。
        </div></template
      >
    </ElUpload>
    <ElProgress
      v-if="processed"
      :percentage="Math.min(100, Math.round((processed / Math.max(files.length, 1)) * 100))"
    />
    <ElAlert
      v-if="processed"
      :title="`成功 ${succeeded} 个，失败 ${failed} 个`"
      :type="failed ? 'warning' : 'success'"
      :closable="false"
    />
    <RequestError v-if="error" :error="error" />
    <template #footer>
      <ElButton v-if="uploading" :disabled="stopAfterCurrent" @click="stopAfterCurrent = true">{{
        stopAfterCurrent ? '当前文件结束后停止' : '停止后续上传'
      }}</ElButton>
      <ElButton v-else @click="visible = false">{{ processed ? '完成' : '取消' }}</ElButton>
      <ElButton
        type="primary"
        :loading="uploading"
        :disabled="!files.some((file) => file.status === 'ready')"
        @click="submit"
        >开始上传</ElButton
      >
    </template>
  </ElDialog>
</template>

<style scoped>
:global(.el-dialog.storage-upload-dialog) {
  margin: auto;
}
.destination {
  margin-top: 0;
  overflow-wrap: anywhere;
}
.upload-tip {
  color: var(--el-text-color-secondary);
  font-size: 13px;
  margin-top: 8px;
}
.el-alert,
.el-progress {
  margin-top: 16px;
}
</style>
