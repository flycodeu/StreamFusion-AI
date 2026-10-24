<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { ElButton, ElDialog } from 'element-plus'
import {
  getOpsPreviewInstances,
  getOpsZlmInstances,
  synchronizeOpsNode,
  type PreviewNodeStatus,
  type OpsSyncInput,
  type OpsSyncReview,
} from '../../api/preview-node/api'
import RequestError from '../../components/feedback/RequestError.vue'
import { usePageScope } from '../../composables/usePageScope'
import { formatDateTime } from '../../utils/dateTime'
import { previewReason } from './model'

const props = defineProps<{ node: PreviewNodeStatus }>()
const emit = defineEmits<{ close: []; applied: [] }>()
const captureScope = usePageScope()
const busy = ref(false),
  loading = ref(true),
  error = ref<unknown>(null)
const catalogReason = ref<string | null>(null),
  available = ref(false)
const options = ref<
  {
    controllerId: string | null
    targetId: string | null
    instanceId: string
    name: string
    targetName: string
    releaseId: string
  }[]
>([])
const selected = ref(''),
  review = ref<OpsSyncReview | null>(null)
const selectedOption = computed(() =>
  options.value.find((item) => item.instanceId === selected.value),
)
const bound = computed(() => props.node.opsSource)
const compatible = computed(() =>
  options.value.filter(
    (item) =>
      !bound.value ||
      (item.controllerId === bound.value.controllerId &&
        item.targetId === bound.value.targetId &&
        item.instanceId === bound.value.instanceId),
  ),
)
const canCheck = computed(
  () =>
    props.node.mode === 'DRAINING' &&
    !!selectedOption.value?.controllerId &&
    !!selectedOption.value?.targetId &&
    !busy.value &&
    !loading.value,
)

onMounted(async () => {
  const active = captureScope()
  try {
    const listing =
      props.node.kind === 'ZLM_DIRECT' ? await getOpsZlmInstances() : await getOpsPreviewInstances()
    if (!active()) return
    options.value = listing.items
    available.value = listing.available
    catalogReason.value = listing.reasonCode
    if (bound.value) selected.value = compatible.value[0]?.instanceId ?? ''
  } catch (cause) {
    if (active()) error.value = cause
  } finally {
    if (active()) loading.value = false
  }
})

function input(): OpsSyncInput | null {
  const item = selectedOption.value
  if (!item?.controllerId || !item.targetId) return null
  return {
    controllerId: item.controllerId,
    targetId: item.targetId,
    instanceId: item.instanceId,
    releaseId: item.releaseId,
    version: props.node.version,
  }
}

async function check(apply = false) {
  const value = input()
  if (!value || busy.value || (apply && !review.value)) return
  const active = captureScope()
  busy.value = true
  error.value = null
  try {
    const result = await synchronizeOpsNode(
      props.node.nodeId,
      { ...value, digest: apply ? review.value!.digest : undefined },
      apply,
    )
    if (!active()) return
    review.value = result
    if (apply) emit('applied')
  } catch (cause) {
    if (active()) {
      error.value = cause
      review.value = null
    }
  } finally {
    if (active()) busy.value = false
  }
}
</script>

<template>
  <ElDialog
    :model-value="true"
    class="management-dialog"
    :title="bound ? '同步 Ops 配置' : '关联现有 Ops 实例'"
    width="min(680px, calc(100vw - 32px))"
    align-center
    append-to-body
    destroy-on-close
    :close-on-click-modal="!busy"
    :close-on-press-escape="!busy"
    :show-close="!busy"
    @close="emit('close')"
  >
    <div class="ops-sync">
      <p class="sync-node-banner">
        <strong>{{ node.name }}</strong> · <code>{{ node.nodeId }}</code>
      </p>
      <p v-if="node.mode !== 'DRAINING'" class="sync-status-warn" role="status">
        请先停止接新预览、排空资源，再打开此入口。
      </p>
      <p v-if="loading" role="status">正在核对 Ops 已发布实例…</p>
      <p v-else-if="catalogReason">
        {{ previewReason(catalogReason) }} <code>{{ catalogReason }}</code>
      </p>
      <p v-else-if="!available">Platform 尚未配置 Ops 实例目录连接。</p>
      <p v-else-if="!compatible.length">
        当前目录没有可选的已发布实例，请在 Ops 核对实例与当前作业后刷新。
      </p>
      <label v-else
        >Ops 实例
        <select
          v-model="selected"
          :disabled="busy || !!bound"
          aria-label="选择关联的 Ops 实例"
          @change="review = null"
        >
          <option value="" disabled>请选择实例，不按地址自动关联</option>
          <option v-for="item in compatible" :key="item.instanceId" :value="item.instanceId">
            {{ item.name }} · {{ item.targetName }} · {{ item.instanceId }}
          </option>
        </select>
      </label>
      <dl v-if="selectedOption" class="sync-summary">
        <div>
          <dt>Ops 来源</dt>
          <dd>
            {{ selectedOption.controllerId || '旧目录缺少身份' }} /
            {{ selectedOption.targetId || '—' }}
          </dd>
        </div>
        <div>
          <dt>平台已同步 Release</dt>
          <dd>
            <code>{{ bound?.releaseId || '尚未关联' }}</code>
          </dd>
        </div>
        <div>
          <dt>Ops 当前 Release</dt>
          <dd>
            <code>{{ selectedOption.releaseId }}</code>
          </dd>
        </div>
      </dl>
      <p v-if="selectedOption && (!selectedOption.controllerId || !selectedOption.targetId)">
        Ops 目录缺少稳定身份，请先升级 Ops；本次不会从名称或 IP 推断归属。
      </p>
      <RequestError
        :error="error"
        advice="保留排空状态。核对 Ops 原作业、待确认 ACK、实际资源和预算，刷新后重新预检。"
      />
      <div v-if="review" class="sync-review" aria-label="同步预检结果">
        <h4>预检通过 · {{ formatDateTime(review.checkedAt) }}</h4>
        <dl class="sync-summary">
          <div>
            <dt>名称</dt>
            <dd>{{ review.previousName }} → {{ review.candidateName }}</dd>
          </div>
          <div v-if="review.previousInstallPath || review.candidateInstallPath">
            <dt>ZLM 安装位置</dt>
            <dd>
              {{ review.previousInstallPath || '未登记' }} →
              {{ review.candidateInstallPath || '未登记' }}
            </dd>
          </div>
          <div>
            <dt>节点配置版本</dt>
            <dd>
              <code>{{ review.previousConfigVersion }}</code> →
              <code>{{ review.candidateConfigVersion }}</code>
            </dd>
          </div>
          <div>
            <dt>平台预算</dt>
            <dd>
              保持：源 {{ review.retainedBudget.maxSources }} / 观看
              {{ review.retainedBudget.maxViewers }} / 转码
              {{ review.retainedBudget.maxTranscodes }}
            </dd>
          </div>
          <div>
            <dt>Ops 配额上限</dt>
            <dd>
              源 {{ review.candidateLimit.maxSources }} / 观看
              {{ review.candidateLimit.maxViewers }} / 转码
              {{ review.candidateLimit.maxTranscodes }}
            </dd>
          </div>
          <div v-if="review.credentialChanged">
            <dt>API 凭据</dt>
            <dd>消费当前 Release 的受保护凭据修订；不显示内容</dd>
          </div>
        </dl>
      </div>
      <p class="sync-note">
        只同步已核实配置和来源，保留节点编号、预算、优先级及排空状态。应用时会再次预检。
      </p>
      <p v-if="node.kind === 'ZLM_DIRECT'" class="sync-note">
        独立 ZLM 仅检查当前媒体为空，没有持久维护许可；请确保其他操作者没有同时发布或拉流。
      </p>
      <div class="sync-actions">
        <ElButton :disabled="busy" @click="emit('close')">取消</ElButton>
        <ElButton :loading="busy" :disabled="!canCheck" @click="check()">预检</ElButton>
        <ElButton type="primary" :disabled="!review || busy || !canCheck" @click="check(true)"
          >应用同步</ElButton
        >
      </div>
    </div>
  </ElDialog>
</template>

<style scoped>
.ops-sync {
  display: grid;
  gap: 14px;
}
.ops-sync p,
.ops-sync h4 {
  margin: 0;
}
.sync-node-banner {
  padding: 8px 12px;
  border: 1px solid #e2e8f0;
  border-radius: 8px;
  background: #f8fafc;
  color: #0f172a;
}
.sync-status-warn {
  padding: 8px 12px;
  border: 1px solid #fde68a;
  border-radius: 8px;
  background: #fffbeb;
  color: #92400e;
  font-size: 13px;
}
.ops-sync label {
  display: grid;
  gap: 6px;
  font-size: 13px;
  font-weight: 500;
  color: #334155;
}
.ops-sync select {
  width: 100%;
  min-height: 36px;
  border: 1px solid #cbd5e1;
  border-radius: 8px;
  background: #ffffff;
  padding: 6px 12px;
  color: #0f172a;
  font-size: 13px;
}
.ops-sync select:focus {
  border-color: #2563eb;
  box-shadow: 0 0 0 3px rgba(37, 99, 235, 0.12);
  outline: none;
}
.sync-summary {
  display: grid;
  gap: 8px;
  margin: 0;
  padding: 12px 14px;
  border: 1px solid #e2e8f0;
  border-radius: 8px;
  background: #f8fafc;
  font-size: 13px;
}
.sync-summary > div {
  display: grid;
  grid-template-columns: 145px minmax(0, 1fr);
  gap: 12px;
}
.sync-summary dt {
  color: #64748b;
  font-weight: 500;
}
.sync-summary dd {
  margin: 0;
  color: #0f172a;
  font-weight: 500;
  overflow-wrap: anywhere;
}
.sync-review {
  padding: 14px;
  border: 1px solid #bbf7d0;
  background: #f0fdf4;
  border-radius: 10px;
  display: grid;
  gap: 12px;
}
.sync-review h4 {
  color: #166534;
  font-size: 13.5px;
}
.sync-review .sync-summary {
  background: #ffffff;
}
.sync-note {
  color: #64748b;
  font-size: 12.5px;
  line-height: 1.6;
}
.sync-actions {
  display: flex;
  gap: 8px;
  justify-content: flex-end;
  flex-wrap: wrap;
  padding-top: 4px;
}
@media (max-width: 600px) {
  .sync-summary > div {
    grid-template-columns: 1fr;
    gap: 4px;
  }
}
</style>
