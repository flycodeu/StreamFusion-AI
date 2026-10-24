<script setup lang="ts">
import type { PreviewNodeStatus } from '../../api/preview-node/api'
import { formatDateTime } from '../../utils/dateTime'

defineProps<{ load: PreviewNodeStatus['zlmLoad'] }>()
</script>

<template>
  <details class="zlm-network-load">
    <summary>网络线程负载</summary>
    <div v-if="load" class="load-values">
      <span>平均 {{ load.averageNetworkThreadLoadPercent.toFixed(1) }}%</span>
      <span>峰值 {{ load.peakNetworkThreadLoadPercent.toFixed(1) }}%</span>
      <span>{{ load.networkThreadCount }} 个线程</span>
      <time :datetime="load.observedAt">采集于 {{ formatDateTime(load.observedAt) }}</time>
    </div>
    <p v-else>暂无数据</p>
    <small>仅为网络线程观测，不代表可接纳预览数。</small>
  </details>
</template>

<style scoped>
.zlm-network-load {
  margin: 10px 0;
  padding: 8px 12px;
  border: 1px solid #e2e8f0;
  border-radius: 8px;
  background: #f8fafc;
  color: #64748b;
  font-size: 12px;
  line-height: 1.5;
}
.zlm-network-load summary {
  width: fit-content;
  cursor: pointer;
  color: #0f172a;
  font-weight: 600;
}
.load-values {
  display: flex;
  flex-wrap: wrap;
  gap: 6px 10px;
  margin-top: 6px;
  font-variant-numeric: tabular-nums;
}
.load-values > span,
.load-values > time {
  display: inline-flex;
  align-items: center;
  padding: 2px 8px;
  border: 1px solid #e2e8f0;
  border-radius: 6px;
  background: #ffffff;
  color: #334155;
  font-weight: 500;
}
.zlm-network-load p {
  margin: 6px 0 0;
}
.zlm-network-load small {
  display: block;
  margin-top: 6px;
  font-size: 11px;
  color: #94a3b8;
}
</style>
