<script setup lang="ts">
import { computed, onBeforeUnmount, ref, watch } from 'vue'
import { ElButton, ElDialog, ElInput, ElTag } from 'element-plus'
import * as api from '../../api/storage/api'
import type { StorageInstance, StorageScope, StorageTask } from '../../api/storage/types'
import RequestError from '../../components/feedback/RequestError.vue'
import { bytes, label, time } from './display'
import { folderName } from './file-browser'

const props = defineProps<{ instance: StorageInstance; scope: StorageScope; folder: string }>()
const emit = defineEmits<{ close: []; updated: [task: StorageTask] }>()
const plan = ref<Record<string, unknown> | null>(null),
  task = ref<StorageTask | null>(null),
  error = ref<unknown>(null),
  typed = ref(''),
  pending = ref(false),
  submitting = ref(false)
const live = computed(() => !!task.value && ['QUEUED', 'RUNNING'].includes(task.value.status))
const deleting = computed(() => submitting.value || (live.value && pending.value))
const ready = computed(
  () =>
    plan.value?.status === 'READY' &&
    typeof plan.value.confirmationToken === 'string' &&
    !task.value,
)
let generation = 0,
  timer: ReturnType<typeof globalThis.setTimeout> | undefined
function stop() {
  generation++
  globalThis.clearTimeout(timer)
}
onBeforeUnmount(stop)
async function poll(id: string, current: number, remaining: number, execution = false) {
  try {
    const value = await api.getTask(id)
    if (current !== generation) return
    error.value = null
    if (execution) task.value = value
    if (['QUEUED', 'RUNNING'].includes(value.status)) {
      if (remaining)
        timer = globalThis.setTimeout(() => void poll(id, current, remaining - 1, execution), 2000)
      else pending.value = false
    } else {
      pending.value = false
      if (execution) emit('updated', value)
      else {
        const detail = await api.getFolderDeletion(id)
        if (current !== generation) return
        plan.value = detail
        if (value.status !== 'SUCCEEDED') task.value = value
      }
    }
  } catch (e) {
    if (current === generation) {
      error.value = e
      pending.value = false
    }
  }
}
watch(
  () => props.folder,
  async () => {
    stop()
    const current = generation
    plan.value = null
    task.value = null
    error.value = null
    typed.value = ''
    pending.value = true
    try {
      const receipt = await api.previewFolderDeletion(props.scope.scopeId, {
        instanceVersion: props.instance.version,
        scopeVersion: props.scope.version,
        prefix: props.folder.slice(props.scope.prefix.length),
        idempotencyKey: crypto.randomUUID(),
      })
      if (current === generation) await poll(String(receipt.taskId), current, 90)
    } catch (e) {
      if (current === generation) {
        error.value = e
        pending.value = false
      }
    }
  },
  { immediate: true },
)
async function execute() {
  if (!ready.value || pending.value || typed.value !== folderName(props.folder)) return
  const current = generation
  pending.value = true
  submitting.value = true
  error.value = null
  try {
    task.value = await api.executeFolderDeletion(String(plan.value!.planId), {
      planVersion: plan.value!.version,
      confirmationToken: plan.value!.confirmationToken,
      idempotencyKey: crypto.randomUUID(),
    })
    if (current === generation) await poll(task.value.taskId, current, 360, true)
  } catch (e) {
    if (current === generation) {
      error.value = e
      pending.value = false
      plan.value = null
    }
  } finally {
    submitting.value = false
  }
}
async function cancel() {
  if (!task.value) return
  try {
    task.value = await api.cancelTask(task.value.taskId, task.value.version)
  } catch (e) {
    error.value = e
  }
}
function refreshExecution() {
  if (!task.value || pending.value) return
  stop()
  pending.value = true
  void poll(task.value.taskId, generation, 90, true)
}
</script>
<template>
  <ElDialog
    :model-value="true"
    class="management-dialog"
    title="删除文件夹及内容"
    width="min(560px,94vw)"
    align-center
    :close-on-click-modal="false"
    :close-on-press-escape="!deleting"
    :show-close="!deleting"
    @update:model-value="emit('close')"
  >
    <div class="delete-body">
      <div class="delete-intro">
        <span class="delete-symbol" aria-hidden="true"
          ><svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.7">
            <path d="M3 7h18M9 7V4h6v3M5 7l1 13h12l1-13M10 10v7m4-7v7" /></svg
        ></span>
        <div>
          <strong>删除 {{ folderName(folder) }}</strong>
          <p>文件夹及其内容将永久删除，无法撤销。</p>
        </div>
      </div>
      <div class="delete-summary">
        <div class="delete-target">
          <strong>{{ instance.name }}</strong
          ><br />{{ scope.bucket }}/{{ folder }}
        </div>
        <dl v-if="ready" class="delete-stats">
          <div>
            <dt>文件及目录项</dt>
            <dd>{{ plan!.candidateCount }}</dd>
          </div>
          <div>
            <dt>合计大小</dt>
            <dd>{{ bytes(plan!.candidateBytes) }}</dd>
          </div>
        </dl>
      </div>
      <RequestError v-if="error" :error="error" />
      <div v-if="ready" class="delete-confirm">
        <label for="confirm-folder-name"
          >输入 <code>{{ folderName(folder) }}</code> 确认删除</label
        >
        <ElInput
          id="confirm-folder-name"
          v-model="typed"
          placeholder="请输入文件夹名称"
          autocomplete="off"
          @keyup.enter="execute"
        />
      </div>
      <p v-else-if="!task && pending" class="delete-status">正在统计文件夹内容…</p>
      <template v-if="task"
        ><p class="delete-status">
          <ElTag>{{ label(task.status) }}</ElTag> 已删除 {{ task.result.deleted ?? 0 }} /
          {{ task.result.total ?? plan?.candidateCount ?? '—' }}
        </p>
        <p v-if="live">正在删除，可停止剩余删除。</p>
        <p v-if="task.result.reasonCode && task.status !== 'SUCCEEDED'" class="result-code">
          任务 {{ task.taskId }} · {{ time(task.finishedAt || task.acceptedAt) }}<br />
          {{ task.result.reasonCode }}<br />{{ task.result.suggestion }}
        </p>
      </template>
    </div>
    <template #footer
      ><div class="delete-footer">
        <ElButton :disabled="deleting" @click="emit('close')">{{ task ? '关闭' : '取消' }}</ElButton
        ><ElButton v-if="live && !pending" @click="refreshExecution">刷新删除结果</ElButton
        ><ElButton v-if="live" :disabled="task?.cancelRequested" @click="cancel"
          >停止剩余删除</ElButton
        ><ElButton
          v-if="ready"
          type="danger"
          :loading="pending"
          :disabled="typed !== folderName(folder)"
          @click="execute"
          >删除文件夹</ElButton
        >
      </div></template
    >
  </ElDialog>
</template>
<style scoped src="./delete-dialog.css"></style>
<style scoped>
p {
  line-height: 1.7;
}
.result-code {
  overflow-wrap: anywhere;
  color: var(--el-text-color-secondary);
  font-size: 12px;
}
</style>
