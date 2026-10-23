<script setup lang="ts">
import { ElButton, ElTag } from 'element-plus'
import * as api from '../../api/storage/api'
import type { StorageInstance } from '../../api/storage/types'
import RequestError from '../../components/feedback/RequestError.vue'
import { useStorageJob } from './useStorageJob'
import { bytes, label, time } from './display'
const props = defineProps<{ instance: StorageInstance }>()
const emit = defineEmits<{ updated: [] }>()
const { task, error, busy, run } = useStorageJob(
  () => props.instance.instanceId,
  () => emit('updated'),
)
</script>
<template>
  <section class="resource-page">
    <div class="section-heading">
      <div>
        <h2>资源概览</h2>
        <span class="secondary">{{
          instance.usage.finishedAt ? time(instance.usage.finishedAt) : '未同步'
        }}</span>
      </div>
      <ElButton
        type="primary"
        :loading="busy"
        @click="run(() => api.scanUsage(instance.instanceId, instance.version))"
        >更新统计</ElButton
      >
    </div>
    <RequestError v-if="error" :error="error" />
    <div v-if="task" class="job-state">
      <ElTag>{{ label(task.type) }} · {{ label(task.status) }}</ElTag
      ><span>{{ task.taskId }}</span>
    </div>
    <div class="resource-stats">
      <article>
        <span class="metric-icon" aria-hidden="true">▧</span>
        <span>存储对象数</span
        ><strong>{{ instance.usage.complete ? instance.usage.visibleObjectCount : '—' }}</strong
        ><span>{{
          instance.usage.stale ? '待更新' : instance.usage.complete ? '最近完整统计' : '未同步'
        }}</span>
      </article>
      <article>
        <span class="metric-icon" aria-hidden="true">▤</span>
        <span>文件逻辑大小</span
        ><strong>{{ instance.usage.complete ? bytes(instance.usage.logicalBytes) : '—' }}</strong
        ><span>当前登记存储</span>
      </article>
      <article>
        <span class="metric-icon" aria-hidden="true">◴</span><span>磁盘已用 / 总量</span
        ><strong class="empty-value">—</strong><span>未同步</span>
      </article>
    </div>
    <div class="resource-sections">
      <section>
        <h3>存储资源</h3>
        <dl>
          <div>
            <dt>磁盘剩余</dt>
            <dd class="empty-value">—</dd>
          </div>
          <div>
            <dt>存储节点 CPU</dt>
            <dd class="empty-value">—</dd>
          </div>
          <div>
            <dt>存储节点内存</dt>
            <dd class="empty-value">—</dd>
          </div>
        </dl>
      </section>
      <section>
        <h3>统计快照</h3>
        <dl>
          <div>
            <dt>存储入口</dt>
            <dd>{{ instance.scopes.length }} 个</dd>
          </div>
          <div>
            <dt>开始时间</dt>
            <dd>{{ instance.usage.startedAt ? time(instance.usage.startedAt) : '未同步' }}</dd>
          </div>
          <div>
            <dt>完成时间</dt>
            <dd>{{ instance.usage.finishedAt ? time(instance.usage.finishedAt) : '未同步' }}</dd>
          </div>
        </dl>
      </section>
    </div>
    <details v-if="Object.keys(instance.metrics).length" class="metric-details">
      <summary>指标采样详情</summary>
      <pre>{{ JSON.stringify(instance.metrics, null, 2) }}</pre>
    </details>
  </section>
</template>
<style scoped>
.resource-page {
  padding: 20px 24px;
}
.section-heading {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 16px;
  margin-bottom: 24px;
}
h2 {
  margin: 0 0 6px;
  font-size: 18px;
  font-weight: 500;
}
h3 {
  font-size: 15px;
  font-weight: 500;
}
.secondary,
.job-state {
  color: var(--el-text-color-secondary);
  font-size: 12px;
}
.job-state {
  display: flex;
  flex-wrap: wrap;
  gap: 12px;
  margin-bottom: 16px;
}
.resource-stats {
  display: grid;
  grid-template-columns: repeat(3, minmax(0, 1fr));
  gap: 16px;
}
article {
  padding: 22px;
  border: 1px solid var(--el-border-color-lighter);
  border-radius: 12px;
  position: relative;
  background: #fff;
  display: flex;
  flex-direction: column;
  gap: 12px;
}
article span {
  font-size: 13px;
  color: var(--el-text-color-secondary);
}
article strong {
  font-size: 28px;
  font-weight: 500;
  font-variant-numeric: tabular-nums;
  overflow-wrap: anywhere;
}
.resource-sections {
  display: grid;
  grid-template-columns: 1fr 1fr;
  gap: 16px;
  margin-top: 16px;
}
dl {
  margin: 0;
}
dl div {
  display: flex;
  justify-content: space-between;
  gap: 14px;
  padding: 12px 0;
  min-height: 48px;
  box-sizing: border-box;
  align-items: center;
  border-bottom: 1px solid var(--el-border-color-lighter);
}
dt {
  color: var(--el-text-color-secondary);
}
dd {
  margin: 0;
  text-align: right;
}
.resource-sections > section {
  border: 1px solid #e2e8f0;
  border-radius: 12px;
  padding: 6px 20px 12px;
}
article .metric-icon {
  position: absolute;
  right: 20px;
  top: 20px;
  display: grid;
  place-items: center;
  width: 32px;
  height: 32px;
  border-radius: 8px;
  background: #eff6ff;
  color: #3b82f6;
  font-size: 20px;
}
.empty-value {
  color: #94a3b8;
}
.metric-details {
  margin-top: 24px;
}
pre {
  white-space: pre-wrap;
  overflow-wrap: anywhere;
}
@media (max-width: 700px) {
  .resource-page {
    padding: 16px;
  }
  .resource-stats,
  .resource-sections {
    grid-template-columns: 1fr;
  }
  .section-heading {
    flex-wrap: wrap;
  }
}
</style>
