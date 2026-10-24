<script setup lang="ts">
import { computed, onBeforeUnmount, ref } from 'vue'
import { ElButton, ElDialog, ElInput, ElTable, ElTableColumn, ElTag } from 'element-plus'
import * as api from '../../api/storage/api'
import type { StorageInstance, StorageScope, StorageObject } from '../../api/storage/types'
import RequestError from '../../components/feedback/RequestError.vue'
import { deleteFileSelection, type FileDeleteProgress } from './file-selection'
import { bytes } from './display'

const props = defineProps<{
  instance: StorageInstance
  scope: StorageScope
  files: StorageObject[]
}>()
const emit = defineEmits<{ close: []; updated: [] }>()
// Freeze ownership, versions and metadata for the entire confirmation, including a parent refresh.
const selection = props.files.map((file) => ({ ...file }))
const target = {
  scopeId: props.scope.scopeId,
  bucket: props.scope.bucket,
  instanceName: props.instance.name,
  instanceVersion: props.instance.version,
  scopeVersion: props.scope.version,
}
const typed = ref(''),
  running = ref(false),
  attempted = ref(false),
  stopping = ref(false)
const result = ref<FileDeleteProgress>({ deleted: [], current: null, stopped: false })
const totalBytes = computed(() =>
  selection.reduce((sum, file) => sum + BigInt(file.sizeBytes), 0n).toString(),
)
let disposed = false
onBeforeUnmount(() => {
  disposed = true
  stopping.value = true
})
async function execute() {
  if (running.value || attempted.value || typed.value !== '删除') return
  running.value = true
  attempted.value = true
  result.value = await deleteFileSelection(
    selection,
    (key) => api.getObject(target.scopeId, key),
    (file) =>
      api.deleteFile(target.scopeId, {
        key: file.objectKey,
        objectToken: file.objectToken,
        instanceVersion: target.instanceVersion,
        scopeVersion: target.scopeVersion,
        idempotencyKey: crypto.randomUUID(),
      }),
    () => disposed || stopping.value,
    (value) => {
      if (!disposed) result.value = value
    },
  )
  running.value = false
  if (!disposed) emit('updated')
}
function state(file: StorageObject) {
  return result.value.deleted.includes(file.objectKey)
    ? '已删除'
    : result.value.failed === file.objectKey
      ? '需核对'
      : result.value.current === file.objectKey
        ? '处理中'
        : attempted.value
          ? '未处理'
          : '待确认'
}
</script>
<template>
  <ElDialog
    :model-value="true"
    class="management-dialog"
    :title="`删除 ${selection.length} 个文件`"
    width="min(660px,94vw)"
    align-center
    :close-on-click-modal="false"
    :close-on-press-escape="!running"
    :show-close="!running"
    @update:model-value="emit('close')"
  >
    <div class="delete-body">
      <div class="delete-intro">
        <span class="delete-symbol" aria-hidden="true"
          ><svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.7">
            <path d="M3 7h18M9 7V4h6v3M5 7l1 13h12l1-13M10 10v7m4-7v7" /></svg
        ></span>
        <div>
          <strong>{{ selection.length }} 个文件 · {{ bytes(totalBytes) }}</strong>
          <p>文件将永久删除，无法撤销。</p>
        </div>
      </div>
      <div class="delete-summary delete-target">
        <strong>{{ target.instanceName }}</strong> / {{ target.bucket }}
      </div>
      <ElTable :data="selection" row-key="objectKey" max-height="260"
        ><ElTableColumn label="文件" min-width="190"
          ><template #default="{ row }"
            ><span class="delete-file-name">{{ row.name }}</span>
            <div class="delete-file-path" :title="row.objectKey">{{ row.objectKey }}</div></template
          ></ElTableColumn
        ><ElTableColumn label="大小" width="110"
          ><template #default="{ row }">{{ bytes(row.sizeBytes) }}</template></ElTableColumn
        ><ElTableColumn v-if="attempted" label="结果" width="90"
          ><template #default="{ row }">{{ state(row as StorageObject) }}</template></ElTableColumn
        ></ElTable
      >
      <RequestError
        v-if="result.error"
        :error="result.error"
        advice="已停止后续删除。请刷新文件库，并到连接检测的操作记录核对结果；不会自动重试。"
      />
      <div v-if="!attempted" class="delete-confirm">
        <label for="confirm-file-delete">输入 <code>删除</code> 确认操作</label>
        <ElInput
          id="confirm-file-delete"
          v-model="typed"
          placeholder="请输入“删除”"
          autocomplete="off"
          @keyup.enter="execute"
        />
      </div>
      <p v-else class="delete-progress">
        <ElTag :type="result.error || result.stopped ? 'warning' : running ? 'info' : 'success'">{{
          running
            ? stopping
              ? '正在停止'
              : '处理中'
            : result.error
              ? '已停止'
              : result.stopped
                ? '已取消剩余操作'
                : '已完成'
        }}</ElTag
        ><span>已删除 {{ result.deleted.length }} / {{ selection.length }}</span>
      </p>
    </div>
    <template #footer
      ><div class="delete-footer">
        <ElButton v-if="running" :disabled="stopping" @click="stopping = true"
          >停止剩余删除</ElButton
        ><ElButton v-else @click="emit('close')">{{ attempted ? '关闭' : '取消' }}</ElButton
        ><ElButton v-if="!attempted" type="danger" :disabled="typed !== '删除'" @click="execute"
          >确认删除 {{ selection.length }} 个文件</ElButton
        >
      </div></template
    >
  </ElDialog>
</template>
<style scoped src="./delete-dialog.css"></style>
<style scoped>
.delete-file-name {
  font-weight: 500;
  overflow-wrap: anywhere;
}
.delete-file-path {
  color: var(--el-text-color-secondary);
  font-size: 12px;
  overflow: hidden;
  white-space: nowrap;
  text-overflow: ellipsis;
}
p {
  margin: 18px 0 10px;
}
.delete-progress {
  display: flex;
  align-items: center;
  gap: 12px;
}
</style>
