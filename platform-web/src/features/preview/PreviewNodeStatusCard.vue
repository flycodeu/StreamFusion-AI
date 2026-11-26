<script setup lang="ts">
import { ElTag } from 'element-plus'
import type { MediaRuntimeCandidate } from '../../api/media-runtime/types'
import type { PreviewNodeStatus } from '../../api/preview-node/api'
import { formatDateTime } from '../../utils/dateTime'
import { labelOf } from '../../utils/display'
import { previewReason } from './model'
import { nodeUsageMetrics as usageMetrics, previewNodeLabels as names } from './nodePresentation'
import ZlmNetworkLoad from './ZlmNetworkLoad.vue'

defineProps<{
  node: PreviewNodeStatus
  localZlm: MediaRuntimeCandidate | null
  zlmLoad: NonNullable<PreviewNodeStatus['zlmLoad']> | null
}>()
</script>

<template>
  <article class="node-card" :style="{ order: node.nodeId === 'local' ? -1 : 0 }">
    <div class="node-heading">
      <div>
        <h3>{{ node.name }}</h3>
        <small>{{ node.nodeId }} · 优先级 {{ node.priority }}</small>
      </div>
      <ElTag round :type="node.mode === 'REMOTE' ? 'primary' : 'info'">{{
        labelOf(names, node.mode)
      }}</ElTag>
    </div>
    <div class="node-status-bar" :class="{ ready: node.state === 'READY' }">
      <p class="node-state">
        <span class="state-dot" aria-hidden="true"></span> 节点检查：{{ labelOf(names, node.state)
        }}<span v-if="node.observedAt"> · {{ formatDateTime(node.observedAt) }}</span>
      </p>
      <dl class="node-identity">
        <div>
          <dt>
            {{
              node.kind === 'ZLM_DIRECT'
                ? 'ZLM API 地址'
                : node.nodeId === 'local'
                  ? 'ZLM 地址'
                  : '节点控制地址'
            }}
          </dt>
          <dd>
            {{
              node.kind === 'ZLM_DIRECT'
                ? node.zlmApiAddress || '未登记'
                : node.nodeId === 'local'
                  ? localZlm?.apiBase || '本机'
                  : node.controlUri || '未登记'
            }}
          </dd>
        </div>
        <div v-if="node.nodeId !== 'local'">
          <dt>媒体地址</dt>
          <dd>{{ node.mediaAddress || '未登记' }}</dd>
        </div>
        <div v-if="node.opsSource">
          <dt>平台登记的 Ops 发布版本</dt>
          <dd>
            <code>{{ node.opsSource.releaseId }}</code> ·
            {{ formatDateTime(node.opsSource.syncedAt) }}
          </dd>
        </div>
        <div v-if="node.kind !== 'ZLM_DIRECT'">
          <dt>{{ node.nodeId === 'local' ? 'ZLM 版本' : '节点程序版本' }}</dt>
          <dd>
            {{
              node.nodeId === 'local'
                ? localZlm?.softwareVersion || '未检测'
                : node.observation?.version || '未上报'
            }}
          </dd>
        </div>
        <div v-if="node.kind === 'MANAGED' || (node.nodeId !== 'local' && !node.kind)">
          <dt>ZLM 版本</dt>
          <dd>节点未上报</dd>
        </div>
        <div v-if="node.nodeId === 'local' && localZlm?.ports.length">
          <dt>监听端口</dt>
          <dd class="node-ports">
            <span v-for="port in localZlm.ports" :key="`${port.protocol}-${port.port}`">
              {{ port.protocol }} {{ port.port }}
            </span>
          </dd>
        </div>
      </dl>
    </div>
    <div class="node-operational">
      <dl class="node-metrics">
        <div v-for="metric in usageMetrics" :key="metric.usage">
          <dt>{{ metric.label }}</dt>
          <dd>{{ node.usage[metric.usage] }} / {{ node.budget[metric.budget] }}</dd>
          <dd class="metric-track">
            <progress
              :value="node.usage[metric.usage]"
              :max="Math.max(1, node.budget[metric.budget])"
              :aria-label="`${metric.label}预算使用率`"
            ></progress>
          </dd>
        </div>
      </dl>
      <div class="node-bandwidth">
        <div>
          <div class="bandwidth-label">
            <span>预留入站</span
            ><strong
              >{{ (node.usage.ingressKbps / 1000).toFixed(1) }} /
              {{ node.budget.ingressKbps / 1000 }} Mbps</strong
            >
          </div>
          <progress
            :value="node.usage.ingressKbps"
            :max="Math.max(1, node.budget.ingressKbps)"
            aria-label="预留入站带宽使用率"
          ></progress>
        </div>
        <div>
          <div class="bandwidth-label">
            <span>预留出口</span
            ><strong
              >{{ (node.usage.egressKbps / 1000).toFixed(1) }} /
              {{ node.budget.egressKbps / 1000 }} Mbps</strong
            >
          </div>
          <progress
            :value="node.usage.egressKbps"
            :max="Math.max(1, node.budget.egressKbps)"
            aria-label="预留出口带宽使用率"
          ></progress>
        </div>
      </div>
      <ZlmNetworkLoad v-if="node.kind === 'ZLM_DIRECT'" :load="zlmLoad" />
      <p v-if="node.reasonCode" class="node-reason">
        {{ previewReason(node.reasonCode) }} <code>{{ node.reasonCode }}</code>
      </p>
      <slot />
    </div>
  </article>
</template>
