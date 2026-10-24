<script setup lang="ts">
import { computed, ref } from 'vue'
import { ElButton, ElTag, ElTable, ElTableColumn, ElDialog } from 'element-plus'
import * as api from '../../api/storage/api'
import type { StorageInstance } from '../../api/storage/types'
import RequestError from '../../components/feedback/RequestError.vue'
import JsonInspector from '../../components/feedback/JsonInspector.vue'
import StorageRecords from './StorageRecords.vue'
import { useStorageJob } from './useStorageJob'
import { label, time, checkStageLabel, checkResultLabel } from './display'
const props = defineProps<{ instance: StorageInstance }>()
const emit = defineEmits<{ updated: [] }>()
const detailsOpen = ref(false)
const { task, error, busy, run } = useStorageJob(
  () => props.instance.instanceId,
  () => emit('updated'),
)
const observations = computed(() =>
  props.instance.scopes.flatMap((scope) => {
    const steps = scope.observation.steps
    return Array.isArray(steps)
      ? steps
          .filter((s) => s && typeof s === 'object')
          .map((step) => ({
            storage: scope.displayName || scope.bucket,
            stage: checkStageLabel(String(step.stage)),
            status: String(step.status),
          }))
      : []
  }),
)
</script>
<template>
  <section class="checks-page">
    <div class="section-heading">
      <div>
        <h2>连接检测</h2>
        <span
          >{{ label(instance.accessStatus) }} · {{ label(instance.freshness) }} ·
          {{ time(instance.checkedAt) }}</span
        >
      </div>
      <div class="check-actions">
        <ElButton @click="detailsOpen = true">检测详情 JSON</ElButton>
        <ElButton
          type="primary"
          :loading="busy"
          :disabled="!instance.scopes.length"
          @click="run(() => api.checkInstance(instance.instanceId, instance.version))"
          >立即检测</ElButton
        >
      </div>
    </div>
    <RequestError v-if="error" :error="error" />
    <ElTag v-if="task" class="job-state">{{ label(task.status) }} · {{ task.taskId }}</ElTag>
    <ElTable :data="observations" empty-text="尚无检测结果"
      ><ElTableColumn prop="storage" label="存储" min-width="170" /><ElTableColumn
        prop="stage"
        label="检测项"
        min-width="170"
      /><ElTableColumn prop="status" label="结果" width="130"
        ><template #default="{ row }"
          ><span
            class="check-state"
            :class="{ passed: row.status === 'PASSED', failed: row.status === 'FAILED' }"
            ><span aria-hidden="true">{{
              row.status === 'PASSED' ? '✓' : row.status === 'FAILED' ? '!' : '○'
            }}</span
            >{{ checkResultLabel(row.status) }}</span
          ></template
        ></ElTableColumn
      ></ElTable
    >
    <ElDialog
      v-model="detailsOpen"
      class="management-dialog"
      title="连接检测详情"
      width="min(820px, 94vw)"
      align-center
      destroy-on-close
    >
      <JsonInspector :value="instance.observation" />
    </ElDialog>
    <StorageRecords
      :key="task?.status"
      :instance-id="instance.instanceId"
      :active="true"
      @updated="emit('updated')"
    />
  </section>
</template>
<style scoped>
.checks-page {
  padding: 20px 24px;
}
.section-heading {
  display: flex;
  justify-content: space-between;
  align-items: center;
  gap: 18px;
  flex-wrap: wrap;
  margin-bottom: 18px;
}
h2 {
  margin: 0 0 4px;
  font-size: 17px;
  font-weight: 700;
  color: #0f172a;
}
.section-heading span {
  color: #64748b;
  font-size: 12.5px;
}
.job-state {
  margin-bottom: 14px;
  max-width: 100%;
}
.check-actions {
  display: flex;
  flex-wrap: wrap;
  gap: 8px;
}
.check-actions :deep(.el-button + .el-button) {
  margin-left: 0;
}
.checks-page :deep(.el-table__header .el-table__cell) {
  background: #f8fafc;
  color: #475569;
  font-weight: 600;
}
.checks-page :deep(.el-table__cell) {
  padding: 12px 0;
}
.check-state {
  display: inline-flex;
  align-items: center;
  gap: 8px;
  color: #64748b;
  font-weight: 500;
}
.check-state > span {
  display: grid;
  place-items: center;
  width: 20px;
  height: 20px;
  border-radius: 50%;
  background: #f1f5f9;
  font-weight: 600;
}
.check-state.passed {
  color: #16a34a;
}
.check-state.passed > span {
  background: #dcfce7;
}
.check-state.failed {
  color: #dc2626;
}
.check-state.failed > span {
  background: #fee2e2;
}
@media (max-width: 700px) {
  .checks-page {
    padding: 16px;
  }
}
</style>
