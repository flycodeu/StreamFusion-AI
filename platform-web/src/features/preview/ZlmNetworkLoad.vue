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
  color: var(--text-secondary);
  font-size: 12px;
  line-height: 1.6;
}
.zlm-network-load summary {
  width: fit-content;
  cursor: pointer;
  color: var(--el-text-color-primary);
}
.load-values {
  display: flex;
  flex-wrap: wrap;
  column-gap: 14px;
  margin-top: 4px;
  font-variant-numeric: tabular-nums;
}
.zlm-network-load p {
  margin: 4px 0 0;
}
.zlm-network-load small {
  display: block;
  margin-top: 2px;
  font-size: 11px;
}
</style>
