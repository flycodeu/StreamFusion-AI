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
  margin-bottom: 20px;
}
h2 {
  margin: 0 0 4px;
  font-size: var(--sf-fs-section);
  font-weight: 600;
  color: var(--sf-text-primary);
}
h3 {
  margin: 12px 0 8px;
  font-size: var(--sf-fs-body);
  font-weight: 600;
  color: var(--sf-text-primary);
}
.secondary,
.job-state {
  color: var(--sf-text-secondary);
  font-size: var(--sf-fs-caption);
}
.job-state {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 10px;
  margin-bottom: 16px;
}
.resource-stats {
  display: grid;
  grid-template-columns: repeat(3, minmax(0, 1fr));
  gap: 14px;
}
article {
  padding: 18px 20px;
  border: 1px solid var(--sf-border);
  border-radius: var(--sf-radius-lg);
  position: relative;
  background: var(--sf-surface-subtle);
  display: flex;
  flex-direction: column;
  gap: 8px;
}
article span {
  font-size: var(--sf-fs-sub);
  font-weight: 500;
  color: var(--sf-text-secondary);
}
article strong {
  font-size: var(--sf-fs-kpi);
  font-weight: 700;
  color: var(--sf-text-primary);
  font-variant-numeric: tabular-nums;
  overflow-wrap: anywhere;
}
.resource-sections {
  display: grid;
  grid-template-columns: 1fr 1fr;
  gap: 14px;
  margin-top: 14px;
}
dl {
  margin: 0;
}
dl div {
  display: flex;
  justify-content: space-between;
  gap: 14px;
  padding: 10px 0;
  min-height: 44px;
  box-sizing: border-box;
  align-items: center;
  border-bottom: 1px solid var(--sf-border-light);
  font-size: var(--sf-fs-sub);
}
dl div:last-child {
  border-bottom: 0;
}
dt {
  color: var(--sf-text-secondary);
  font-weight: 500;
}
dd {
  margin: 0;
  color: var(--sf-text-primary);
  font-weight: 500;
  font-variant-numeric: tabular-nums;
  text-align: right;
}
.resource-sections > section {
  border: 1px solid var(--sf-border);
  border-radius: var(--sf-radius-lg);
  background: var(--sf-surface);
  padding: 6px 20px 10px;
}
article .metric-icon {
  position: absolute;
  right: 18px;
  top: 18px;
  display: grid;
  place-items: center;
  width: 32px;
  height: 32px;
  border-radius: var(--sf-radius-md);
  background: var(--sf-primary-soft);
  color: var(--sf-primary);
  font-size: 16px;
}
.empty-value {
  color: #94a3b8;
}
.metric-details {
  margin-top: 20px;
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
