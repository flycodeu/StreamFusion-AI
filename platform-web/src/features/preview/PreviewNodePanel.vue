<script setup lang="ts">
import { computed, onBeforeUnmount, ref } from 'vue'
import { ElButton, ElDialog, ElTag } from 'element-plus'
import { RouterLink } from 'vue-router'
import {
  getPreviewNodes,
  previewNodeAction,
  registerPreviewNode,
  savePreviewNode,
  type PreviewCluster,
  type PreviewNodeStatus,
  previewMaintenance,
  type PreviewMaintenance,
} from '../../api/preview-node/api'
import RequestError from '../../components/feedback/RequestError.vue'
import { usePageScope } from '../../composables/usePageScope'
import { sessionState } from '../../session/state'
import { formatDateTime } from '../../utils/dateTime'
import { previewReason } from './model'

const captureScope = usePageScope()
const snapshot = ref<PreviewCluster | null>(null),
  busy = ref(false),
  error = ref<unknown>(null)
const diagnostic = ref<PreviewNodeStatus | null>(null),
  editing = ref<PreviewNodeStatus | null>(null)
const administrator = computed(() => !!sessionState.me?.isSuperAdmin)
const maintaining = ref<PreviewNodeStatus | null>(null)
const maintenance = ref<PreviewMaintenance | null>(null)
const names: Record<string, string> = {
  LOCAL: '不接新预览',
  REMOTE: '参与调度',
  DRAINING: '排空中',
  READY: '就绪',
  MAINTENANCE: '维护封闭中',
  RECONCILING: '资源待核对',
  UNKNOWN: '观测已过期或未检查',
  DEGRADED: '媒体环境异常',
  ZLM_NOT_READY: '本机媒体未配置',
  NODE_HOST_OVERLOADED: '节点持续高负载，暂停新预览',
  NODE_BITRATE_EXCEEDED: '实测码率超出预算，暂停新预览',
}
const fields: {
  key: keyof PreviewNodeStatus['budget']
  label: string
  min: number
  max: number
}[] = [
  { key: 'maxSources', label: '源流上限', min: 1, max: 64 },
  { key: 'maxViewers', label: '观看上限', min: 1, max: 64 },
  { key: 'maxTranscodes', label: '共享转码上限', min: 0, max: 64 },
  { key: 'maxStarting', label: '同时启动上限', min: 1, max: 4 },
  { key: 'ingressKbps', label: '入站预算（kbps）', min: 1, max: 100000000 },
  { key: 'egressKbps', label: '出口预算（kbps）', min: 1, max: 100000000 },
  { key: 'sourceUpperKbps', label: '每源码率上界（kbps）', min: 1, max: 1000000 },
  { key: 'outputUpperKbps', label: '每观看码率上界（kbps）', min: 1, max: 1000000 },
]
let controller: globalThis.AbortController | undefined
onBeforeUnmount(() => controller?.abort())
async function run(operation?: () => Promise<unknown>) {
  if (busy.value) return
  const active = captureScope()
  controller = new globalThis.AbortController()
  busy.value = true
  error.value = null
  try {
    if (operation) await operation()
    const value = await getPreviewNodes(controller.signal)
    if (active()) {
      snapshot.value = value
      if (maintaining.value) maintaining.value = value.nodes.find(n => n.nodeId === maintaining.value?.nodeId) ?? null
      editing.value = null
    }
  } catch (cause) {
    if (active()) error.value = cause
  } finally {
    if (active()) busy.value = false
  }
}
function expanded(event: globalThis.Event) {
  if ((event.target as globalThis.HTMLDetailsElement).open) void run()
}
function edit(node: PreviewNodeStatus) {
  editing.value = { ...node, budget: { ...node.budget } }
}
function mode(node: PreviewNodeStatus, value: string) {
  void run(() => savePreviewNode({ ...node, mode: value }))
}
function save() {
  if (editing.value) {
    const value = editing.value
    void run(() => savePreviewNode(value))
  }
}
function openMaintenance(node: PreviewNodeStatus) {
  maintaining.value = node
  maintenance.value = null
  void maintain()
}
function maintain(action?: 'PREPARE' | 'RELEASE') {
  const node = maintaining.value
  if (!node) return
  const active = captureScope()
  void run(async () => {
    const value = await previewMaintenance(node, action,
      action === 'PREPARE' ? globalThis.crypto.randomUUID() : maintenance.value?.permitId ?? undefined)
    if (active() && maintaining.value?.nodeId === node.nodeId) maintenance.value = value
  })
}
</script>

<template>
  <details class="preview-node-panel" @toggle="expanded">
    <summary>预览执行节点 <span>同源复用 · 容量不足分配新源 · 已有观看保持原节点</span></summary>
    <RequestError
      :error="error"
      advice="核对节点原因和预算；连接异常时检查证书、凭据与节点日志，资源未确认关闭前保留占用。"
    />
    <div v-if="snapshot" class="node-content">
      <div class="node-actions">
        <ElButton :loading="busy" @click="run(() => previewNodeAction('check'))">检查节点</ElButton>
        <ElButton
          v-if="administrator"
          :disabled="busy"
          @click="run(() => previewNodeAction('reconcile'))"
          >重新核对资源</ElButton
        >
        <RouterLink to="/preview/center">前往预览</RouterLink>
      </div>
      <div class="node-list">
        <article v-for="node in snapshot.nodes" :key="node.nodeId" class="node-card">
          <div class="node-heading">
            <div>
              <h3>{{ node.name }}</h3>
              <small>{{ node.nodeId }} · 优先级 {{ node.priority }}</small>
            </div>
            <ElTag effect="plain">{{ names[node.mode] }}</ElTag>
          </div>
          <p class="node-state">
            实际状态：{{ names[node.state] || node.state
            }}<span v-if="node.observedAt"> · {{ formatDateTime(node.observedAt) }}</span>
          </p>
          <dl class="node-metrics">
            <div>
              <dt>源流</dt>
              <dd>{{ node.usage.sources }} / {{ node.budget.maxSources }}</dd>
            </div>
            <div>
              <dt>观看</dt>
              <dd>{{ node.usage.viewers }} / {{ node.budget.maxViewers }}</dd>
            </div>
            <div>
              <dt>转码</dt>
              <dd>{{ node.usage.transcodes }} / {{ node.budget.maxTranscodes }}</dd>
            </div>
            <div>
              <dt>启动中</dt>
              <dd>{{ node.usage.starting }} / {{ node.budget.maxStarting }}</dd>
            </div>
          </dl>
          <p class="node-bandwidth">
            预留带宽：入站 {{ (node.usage.ingressKbps / 1000).toFixed(1) }} /
            {{ node.budget.ingressKbps / 1000 }} Mbps · 出口
            {{ (node.usage.egressKbps / 1000).toFixed(1) }} /
            {{ node.budget.egressKbps / 1000 }} Mbps
          </p>
          <p v-if="node.reasonCode" class="node-reason">
            {{ previewReason(node.reasonCode) }} <code>{{ node.reasonCode }}</code>
          </p>
          <div class="node-actions">
            <ElButton @click="diagnostic = node">诊断 JSON</ElButton>
            <template v-if="administrator">
              <ElButton :disabled="busy" @click="edit(node)">预算与优先级</ElButton>
              <ElButton v-if="node.nodeId !== 'local'" :disabled="busy" @click="openMaintenance(node)">维护许可</ElButton>
              <ElButton v-if="node.mode !== 'REMOTE'" :disabled="busy" @click="mode(node, 'REMOTE')"
                >参与调度</ElButton
              >
              <ElButton v-else :disabled="busy" @click="mode(node, 'DRAINING')"
                >停止接新预览</ElButton
              >
            </template>
          </div>
        </article>
      </div>
      <details class="node-guide">
        <summary>添加节点与容量说明</summary>
        <p>
          仅使用本机时无需部署其他节点。需要扩容时，部署预览节点包，将 A/B
          的登记配置放入平台受保护的连接配置文件，然后逐个导入并启用。
        </p>
        <p>
          优先级数值越小越先使用。预算包含启动中和待清理资源；64
          是每节点配置上限，实际容量需按码率和目标机器验证。码率上界按 1.25 倍预留，默认 8192 kbps
          是可调整配置。
        </p>
        <div v-if="administrator" class="node-actions">
          <ElButton
            v-for="item in snapshot.registrationOptions"
            :key="item.nodeId"
            :disabled="busy"
            @click="run(() => registerPreviewNode(item.nodeId))"
            >导入 {{ item.name }}</ElButton
          >
        </div>
      </details>
    </div>
    <ElDialog :model-value="!!maintaining" title="预览节点维护" width="min(680px, calc(100vw - 32px))"
      append-to-body destroy-on-close @close="maintaining = null">
      <div v-if="maintaining" class="maintenance-body">
        <p>{{ maintaining.name }} · {{ names[maintaining.mode] }}</p>
        <ol><li>停止接新预览，等待已有观看关闭及资源释放。</li>
          <li>获取维护许可，在 Ops 对应预览实例的升级或回退计划中填写许可编号。</li>
          <li>Ops 作业完成后检查节点；配置发生变化时重新导入登记，解除维护，再明确参与调度。</li></ol>
        <RequestError :error="error" advice="未决 Ops 作业须先核对或恢复。许可过期不会自动解除维护。" />
        <p v-if="maintenance">维护状态：{{ ({OPEN:'未封闭',SEALED:'已签发，等待 Ops 领取',CLAIMED:'Ops 执行中',COMPLETE:'Ops 已完成，等待解除'} as Record<string,string>)[maintenance.state] || maintenance.state }}</p>
        <template v-if="maintenance?.permitId">
          <label>维护许可编号<input :value="maintenance.permitId" readonly aria-label="维护许可编号" /></label>
          <p>领取期限：{{ formatDateTime(new Date(maintenance.expiresAt).toISOString()) }}</p>
          <p v-if="maintenance.jobId">Ops 作业：<code>{{ maintenance.jobId }}</code></p>
        </template>
        <div class="node-actions">
          <ElButton :disabled="busy" @click="maintain()">刷新维护状态</ElButton>
          <ElButton :disabled="busy || maintaining.mode !== 'DRAINING' || maintenance?.state === 'CLAIMED'" @click="maintain('PREPARE')">获取维护许可</ElButton>
          <ElButton :disabled="busy || maintaining.mode !== 'DRAINING' || !maintenance?.permitId || maintenance.state === 'CLAIMED'" @click="maintain('RELEASE')">解除维护</ElButton>
        </div>
      </div>
    </ElDialog>
    <ElDialog
      :model-value="!!diagnostic"
      title="预览节点诊断"
      width="min(760px, calc(100vw - 32px))"
      append-to-body
      destroy-on-close
      @close="diagnostic = null"
    >
      <pre class="node-json">{{ JSON.stringify(diagnostic, null, 2) }}</pre>
    </ElDialog>
    <ElDialog
      :model-value="!!editing"
      title="节点预算与优先级"
      width="min(620px, calc(100vw - 32px))"
      append-to-body
      destroy-on-close
      @close="editing = null"
    >
      <form v-if="editing" class="budget-form" @submit.prevent="save">
        <label
          >优先级<input
            v-model.number="editing.priority"
            type="number"
            min="0"
            max="10000"
            required
        /></label>
        <label v-for="field in fields" :key="field.key"
          >{{ field.label
          }}<input
            v-model.number="editing.budget[field.key]"
            type="number"
            :min="field.min"
            :max="field.max"
            required
        /></label>
        <p>降低预算只影响新申请。修改每路码率上界须先排空；远程预算不得超过节点已应用配置。</p>
        <ElButton native-type="submit" :loading="busy">保存</ElButton>
      </form>
    </ElDialog>
  </details>
</template>

<style scoped>
.preview-node-panel {
  margin-top: 24px;
  padding: 20px 24px;
  border: 1px solid var(--border-subtle);
  border-radius: 6px;
  background: var(--el-bg-color);
}
summary {
  cursor: pointer;
  font-weight: 600;
  line-height: 1.6;
}
summary > span {
  margin-left: 12px;
  font-size: 13px;
  font-weight: 400;
  color: var(--text-secondary);
}
.node-content {
  margin-top: 20px;
}
.node-list {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(min(100%, 360px), 1fr));
  gap: 16px;
  margin-top: 16px;
}
.node-card {
  min-width: 0;
  padding: 18px;
  border: 1px solid var(--border-subtle);
  border-radius: 6px;
}
.node-heading {
  display: flex;
  justify-content: space-between;
  align-items: center;
  gap: 12px;
}
h3 {
  font-size: 16px;
  margin: 0 0 4px;
}
small,
.node-state,
.node-bandwidth,
.node-guide {
  color: var(--text-secondary);
  font-size: 13px;
  line-height: 1.8;
}
.node-metrics {
  display: grid;
  grid-template-columns: repeat(4, 1fr);
  gap: 10px;
  margin: 16px 0;
}
dt {
  color: var(--text-secondary);
  font-size: 12px;
}
dd {
  margin: 4px 0 0;
  font-variant-numeric: tabular-nums;
}
.node-actions {
  display: flex;
  gap: 8px;
  align-items: center;
  flex-wrap: wrap;
}
.node-actions :deep(.el-button + .el-button) {
  margin-left: 0;
}
.node-actions a {
  font-size: 14px;
  color: var(--el-color-primary);
}
.node-guide {
  margin-top: 20px;
  border-top: 1px solid var(--border-subtle);
  padding-top: 16px;
}
.node-reason {
  font-size: 13px;
  color: var(--el-color-warning-dark-2);
  overflow-wrap: anywhere;
}
.node-json {
  margin: 0;
  max-height: 60vh;
  overflow: auto;
  white-space: pre-wrap;
  overflow-wrap: anywhere;
  font-size: 12px;
  line-height: 1.7;
}
.budget-form {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: 16px;
}
.maintenance-body { font-size: 14px; line-height: 1.8; overflow-wrap: anywhere; }
.maintenance-body ol { padding-left: 22px; }
.maintenance-body label { display: grid; gap: 6px; }
.maintenance-body input { width: 100%; box-sizing: border-box; padding: 8px; color: var(--el-text-color-primary); background: var(--el-bg-color); border: 1px solid var(--border-subtle); }
.budget-form label {
  display: grid;
  gap: 6px;
  font-size: 13px;
}
.budget-form input {
  min-width: 0;
  width: 100%;
  box-sizing: border-box;
  padding: 8px;
  border: 1px solid var(--border-subtle);
  border-radius: 4px;
  background: var(--el-bg-color);
  color: var(--el-text-color-primary);
}
.budget-form p {
  grid-column: 1 / -1;
  font-size: 13px;
  line-height: 1.7;
  color: var(--text-secondary);
}
@media (max-width: 560px) {
  .preview-node-panel {
    padding: 16px;
  }
  summary > span {
    display: block;
    margin-left: 0;
  }
  .node-metrics {
    grid-template-columns: repeat(2, 1fr);
  }
  .budget-form {
    grid-template-columns: 1fr;
  }
}
</style>
