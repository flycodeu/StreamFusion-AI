<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { ElButton, ElDialog } from 'element-plus'
import { deleteCameraDeviceAsset, getDeviceDeleteImpact } from '../../api/camera/api'
import type { CameraDeviceGroup } from '../../api/camera/types'
import RequestError from '../../components/feedback/RequestError.vue'
import { usePageScope } from '../../composables/usePageScope'
import { ApiRequestError } from '../../lib/http/error'

type Impact = Awaited<ReturnType<typeof getDeviceDeleteImpact>>
interface Entry {
  target: CameraDeviceGroup
  stage: 'checking' | 'ready' | 'blocked' | 'deleted' | 'rejected' | 'unconfirmed'
  impact?: Impact
  removedSource?: boolean
  error?: unknown
}

const props = defineProps<{ targets: CameraDeviceGroup[] }>()
const emit = defineEmits<{ close: []; changed: [groupKeys: string[]] }>()
const active = usePageScope()()
const entries = ref<Entry[]>(props.targets.map((target) => ({ target, stage: 'checking' })))
const busy = ref(true)
const submitted = ref(false)
const deleting = computed(() => submitted.value && busy.value)
const readyCount = computed(() => entries.value.filter((entry) => entry.stage === 'ready').length)
const removedCount = computed(
  () => entries.value.filter((entry) => entry.stage === 'deleted').length,
)

onMounted(async () => {
  async function check(entry: Entry) {
    try {
      const impact = await getDeviceDeleteImpact(entry.target.groupKey)
      if (!active()) return
      entry.impact = impact
      entry.stage = 'ready'
    } catch (error) {
      if (!active()) return
      entry.error = error
      entry.stage = 'blocked'
    }
  }
  for (let offset = 0; offset < entries.value.length && active(); offset += 4)
    await Promise.all(entries.value.slice(offset, offset + 4).map(check))
  if (active()) busy.value = false
})

async function confirm() {
  if (!active() || busy.value || submitted.value || !readyCount.value) return
  busy.value = true
  submitted.value = true
  const deleted: string[] = []
  for (const entry of entries.value) {
    if (!entry.impact) continue
    const { groupKey, revision } = entry.impact
    try {
      const removedSource = await deleteCameraDeviceAsset(groupKey, revision)
      if (!active()) return
      entry.removedSource = removedSource
      entry.stage = 'deleted'
      deleted.push(groupKey)
    } catch (error) {
      if (!active()) return
      entry.error = error
      entry.stage =
        error instanceof ApiRequestError &&
        [400, 401, 403, 409, 412, 428, 429].includes(error.status ?? 0)
          ? 'rejected'
          : 'unconfirmed'
    }
  }
  emit('changed', deleted)
  busy.value = false
}
</script>

<template>
  <ElDialog
    :model-value="true"
    title="删除相机"
    width="min(600px, calc(100vw - 32px))"
    :show-close="!deleting"
    :close-on-click-modal="false"
    :close-on-press-escape="!deleting"
    @update:model-value="emit('close')"
  >
    <p v-if="!submitted">删除会移除相机及其全部通道和码流档案。检查历史按原保留期限清理。</p>
    <p v-else role="status">已确认删除 {{ removedCount }} 台，共 {{ entries.length }} 台。</p>
    <ul class="device-delete-results">
      <li v-for="entry in entries" :key="entry.target.groupKey">
        <strong>{{ entry.target.name }}</strong>
        <small> · {{ entry.target.groupKey }}</small>
        <span v-if="entry.stage === 'checking'">正在核对…</span>
        <span v-else-if="entry.stage === 'ready' && entry.impact">
          {{ entry.impact.channelCount }} 个通道；
          {{ entry.impact.removesSource ? '连接配置一并清理' : '保留连接配置' }}
        </span>
        <span v-else-if="entry.stage === 'deleted'">
          已删除；{{ entry.removedSource ? '已清理连接配置' : '已保留连接配置' }}
        </span>
        <span v-else-if="entry.stage === 'blocked'">未提交删除</span>
        <span v-else-if="entry.stage === 'rejected'">未删除</span>
        <span v-else>删除未确认，请刷新列表核对</span>
        <RequestError :error="entry.error" />
      </li>
    </ul>
    <template #footer>
      <ElButton :disabled="deleting" @click="emit('close')">
        {{ submitted ? '关闭' : '取消' }}
      </ElButton>
      <ElButton
        v-if="!submitted"
        type="danger"
        :loading="busy"
        :disabled="!readyCount"
        @click="confirm"
        >确认删除 {{ readyCount }} 台</ElButton
      >
    </template>
  </ElDialog>
</template>

<style scoped>
.device-delete-results {
  list-style: none;
  max-height: 55vh;
  overflow-y: auto;
  padding: 0;
}
.device-delete-results li {
  padding: 12px 0;
  border-bottom: 1px solid var(--el-border-color-lighter);
}
.device-delete-results span {
  display: block;
  margin-top: 4px;
  color: var(--el-text-color-secondary);
}
</style>
