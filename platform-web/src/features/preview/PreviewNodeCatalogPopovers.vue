<script setup lang="ts">
import { ElButton, ElTag } from 'element-plus'
import type {
  DirectZlmInstances,
  OpsPreviewInstances,
  OpsZlmInstances,
  PreviewCluster,
  PreviewNodeStatus,
} from '../../api/preview-node/api'
import RequestError from '../../components/feedback/RequestError.vue'
import { previewReason } from './model'

const props = defineProps<{
  snapshot: PreviewCluster
  opsInstances: OpsPreviewInstances | null
  opsBusy: boolean
  opsError: unknown
  opsZlmInstances: OpsZlmInstances | null
  opsZlmBusy: boolean
  opsZlmError: unknown
  directInstances: DirectZlmInstances | null
  busy: boolean
  configuredConnections: {
    nodeId: string
    name: string
    registered?: PreviewNodeStatus
  }[]
}>()

const emit = defineEmits<{
  refreshOpsInstances: []
  importOpsInstance: [instanceId: string, releaseId: string]
  registerNode: [nodeId: string]
  openConnection: []
  refreshOpsZlmInstances: []
  importOpsZlm: [instanceId: string, releaseId: string]
}>()

function opsZlmStatus(item: OpsZlmInstances['items'][number]): 'linked' | 'occupied' | 'available' {
  const linked = props.snapshot.nodes.some(
    (node) =>
      node.opsSource?.controllerId === item.controllerId &&
      node.opsSource?.targetId === item.targetId &&
      node.opsSource?.instanceId === item.instanceId,
  )
  if (linked) return 'linked'
  const occupied = (props.directInstances?.items ?? []).some(
    (node) => `http://${node.apiHost}:${node.apiPort}` === item.apiUrl,
  )
  return occupied ? 'occupied' : 'available'
}
</script>

<template>
  <section
    id="preview-package-panel"
    popover="auto"
    class="guide-popover node-package-guide"
    aria-label="接入预览节点"
  >
    <header class="popover-heading">
      <div>
        <h3>接入节点包</h3>
      </div>
      <ElButton popovertarget="preview-package-panel" popovertargetaction="hide">关闭</ElButton>
    </header>
    <div class="connection-heading">
      <h3>Ops 已安装</h3>
      <div class="node-actions">
        <ElButton :loading="opsBusy" @click="emit('refreshOpsInstances')">刷新实例</ElButton>
      </div>
    </div>
    <RequestError
      :error="opsError"
      advice="检查 Platform 与 Ops 的连接及 Ops 中预览实例的部署状态。"
    />
    <p v-if="opsBusy && !opsInstances" class="connection-empty">正在读取 Ops 实例…</p>
    <p v-else-if="opsInstances?.reasonCode" class="connection-empty">
      {{ previewReason(opsInstances.reasonCode) }}
      <code>{{ opsInstances.reasonCode }}</code>
    </p>
    <p v-else-if="opsInstances && !opsInstances.available" class="connection-empty">
      Ops 目录未配置（可选）。
    </p>
    <p v-else-if="opsInstances && !opsInstances.items.length" class="connection-empty">
      Ops 暂无可引入的预览实例。
    </p>
    <div v-if="opsInstances?.items.length" class="connection-list">
      <div v-for="item in opsInstances.items" :key="item.instanceId" class="connection-row">
        <div class="connection-detail">
          <div class="connection-name">
            <strong>{{ item.name }}</strong>
            <ElTag
              :type="
                snapshot.nodes.some((node) => node.nodeId === item.nodeId) ? 'info' : 'warning'
              "
              effect="plain"
              >{{
                snapshot.nodes.some((node) => node.nodeId === item.nodeId) ? '已登记' : '可引入'
              }}</ElTag
            >
          </div>
          <div class="connection-meta">
            <span>服务器：{{ item.targetName }}</span>
            <span v-if="item.targetAddress">地址：{{ item.targetAddress }}</span>
          </div>
          <div class="connection-meta">
            <span>安装位置：{{ item.installRoot }}</span>
            <span>发布版本：{{ item.releaseId }}</span>
            <span
              >源流 {{ item.maxSources }} · 观看 {{ item.maxViewers }} · 转码
              {{ item.maxTranscodes }}</span
            >
          </div>
        </div>
        <ElButton
          :disabled="
            busy ||
            opsBusy ||
            !item.controllerId ||
            !item.targetId ||
            snapshot.nodes.some((node) => node.nodeId === item.nodeId)
          "
          @click="emit('importOpsInstance', item.instanceId, item.releaseId)"
          >{{
            snapshot.nodes.some((node) => node.nodeId === item.nodeId)
              ? '请从节点卡同步'
              : '引入节点'
          }}</ElButton
        >
      </div>
    </div>
    <details v-if="configuredConnections.length" class="connection-extra">
      <summary>登记文件中的节点（{{ configuredConnections.length }}）</summary>
      <div class="connection-list">
        <div v-for="item in configuredConnections" :key="item.nodeId" class="connection-row">
          <div class="connection-name">
            <strong>{{ item.name }}</strong>
            <code>{{ item.nodeId }}</code>
            <ElTag :type="item.registered ? 'info' : 'warning'" effect="plain">{{
              item.registered ? '已登记' : '待导入'
            }}</ElTag>
          </div>
          <ElButton
            :disabled="busy || item.registered?.mode === 'REMOTE' || !!item.registered?.opsSource"
            @click="emit('registerNode', item.nodeId)"
            >{{ item.registered ? '重新导入' : '导入节点' }}</ElButton
          >
        </div>
      </div>
    </details>
    <details class="connection-extra">
      <summary>手工登记节点包</summary>
      <p v-if="!snapshot.directRegistrationAvailable" class="connection-empty">
        节点包需要预置机器令牌；可先从 Ops 引入，或由管理员配置节点包凭据目录。
      </p>
      <ElButton
        :disabled="busy || !snapshot.directRegistrationAvailable"
        @click="emit('openConnection')"
        >手工登记</ElButton
      >
    </details>
  </section>

  <section
    id="preview-ops-zlm-panel"
    popover="auto"
    class="guide-popover ops-zlm-catalog"
    aria-label="从 Ops 引入 ZLM"
  >
    <header class="popover-heading">
      <div>
        <h3>从 Ops 引入 ZLM</h3>
      </div>
      <ElButton popovertarget="preview-ops-zlm-panel" popovertargetaction="hide">关闭</ElButton>
    </header>
    <div class="connection-heading">
      <h4>已发布实例</h4>
      <ElButton :loading="opsZlmBusy" @click="emit('refreshOpsZlmInstances')">刷新</ElButton>
    </div>
    <RequestError
      :error="opsZlmError"
      advice="检查 Platform 与 Ops 的连接，以及 Ops 中 ZLM 实例的发布状态。"
    />
    <p v-if="opsZlmBusy && !opsZlmInstances" class="connection-empty">正在读取 Ops 的 ZLM 实例…</p>
    <p v-else-if="opsZlmInstances?.reasonCode" class="connection-empty">
      {{ previewReason(opsZlmInstances.reasonCode) }}
      <code>{{ opsZlmInstances.reasonCode }}</code>
    </p>
    <p v-else-if="opsZlmInstances && !opsZlmInstances.available" class="connection-empty">
      Ops 目录未配置；可继续手工接入。
    </p>
    <p v-else-if="opsZlmInstances && !opsZlmInstances.items.length" class="connection-empty">
      Ops 暂无已发布的 ZLM 实例。
    </p>
    <div v-if="opsZlmInstances?.items.length" class="connection-list">
      <article v-for="item in opsZlmInstances.items" :key="item.instanceId" class="connection-row">
        <div class="connection-detail">
          <div class="connection-name">
            <strong>{{ item.name }}</strong>
            <ElTag :type="opsZlmStatus(item) === 'available' ? 'warning' : 'info'" effect="plain">
              {{
                opsZlmStatus(item) === 'linked'
                  ? '已引入平台'
                  : opsZlmStatus(item) === 'occupied'
                    ? '地址已登记'
                    : '未引入平台'
              }}
            </ElTag>
          </div>
          <div class="connection-meta">
            <span>服务器：{{ item.targetName }}</span>
            <span v-if="item.targetAddress">服务器地址：{{ item.targetAddress }}</span>
            <span>媒体地址：{{ item.mediaAddress }}</span>
          </div>
          <div class="connection-meta">
            <span>ZLM HTTP API：{{ item.apiUrl }}</span>
            <span>HTTP 端口：{{ item.apiPort }}</span>
            <span>RTC 端口：{{ item.rtcPort }}</span>
            <span>Ops 目标根目录：{{ item.installRoot }}</span>
            <span v-if="item.installPath">ZLM 发布目录：{{ item.installPath }}</span>
            <span>发布版本：{{ item.releaseId }}</span>
          </div>
        </div>
        <ElButton
          :disabled="
            busy ||
            opsZlmBusy ||
            !item.controllerId ||
            !item.targetId ||
            !directInstances?.registrationAvailable ||
            opsZlmStatus(item) !== 'available'
          "
          @click="emit('importOpsZlm', item.instanceId, item.releaseId)"
          >引入平台</ElButton
        >
      </article>
    </div>
  </section>
</template>
