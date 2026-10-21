<script setup lang="ts">
import { computed } from "vue";
import { hostTime } from "./hosts";
import { observationPending, type ObservationBatch } from "./observation";
const props = defineProps<{ batch?: ObservationBatch; busy: boolean }>();
const emit = defineEmits<{ cancel: [] }>();
const completed = computed(
  () =>
    props.batch?.items.filter((i) => !["QUEUED", "RUNNING"].includes(i.status))
      .length || 0,
);
const failed = computed(
  () => props.batch?.items.filter((i) => i.status === "FAILED").length || 0,
);
const skipped = computed(
  () => props.batch?.items.filter((i) => i.status === "SKIPPED").length || 0,
);
const labels: Record<string, string> = {
  QUEUED: "排队中",
  RUNNING: "检测中",
  SUCCEEDED: "已确认",
  FAILED: "未确认",
  SKIPPED: "已跳过",
  CANCELLED: "已取消",
};
</script>
<template>
  <details v-if="batch" class="observation-batch">
    <summary>
      <span>最近检测</span>
      <strong>{{ completed }} / {{ batch.items.length }}</strong>
      <span>{{ observationPending(batch) ? "进行中" : "已结束" }}</span>
      <span v-if="failed" class="batch-warning">未确认 {{ failed }}</span>
      <span v-if="skipped">跳过 {{ skipped }}</span>
      <span class="spacer"></span>
      <small>{{ hostTime(batch.createdAt) }}</small>
    </summary>
    <div class="batch-detail-head">
      <code :title="batch.traceId">批次 {{ batch.id.slice(0, 8) }}</code>
      <button
        v-if="observationPending(batch)"
        class="quiet"
        :disabled="busy"
        @click="emit('cancel')"
      >
        取消排队
      </button>
    </div>
    <div class="batch-items">
      <div v-for="item in batch.items" :key="item.objectId" class="batch-item">
        <strong>{{ item.name }}</strong>
        <span>{{ labels[item.status] || item.status }}</span>
        <code v-if="item.errorCode">{{ item.errorCode }}</code>
        <small v-if="item.observedAt">{{ hostTime(item.observedAt) }}</small>
      </div>
    </div>
  </details>
</template>
<style scoped>
.observation-batch {
  border: 1px solid var(--border, #dce3ec);
  border-radius: 8px;
  margin: 0 0 18px;
  background: var(--panel, #fff);
}
summary {
  cursor: pointer;
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 12px;
  padding: 12px 16px;
  font-size: 13px;
}
summary strong {
  font-variant-numeric: tabular-nums;
}
.batch-warning {
  color: #a16616;
}
.batch-detail-head {
  display: flex;
  justify-content: space-between;
  padding: 8px 16px;
  border-top: 1px solid #e7ebf0;
}
.batch-items {
  max-height: 280px;
  overflow: auto;
  padding: 0 16px 12px;
}
.batch-item {
  display: grid;
  grid-template-columns: minmax(120px, 1fr) 80px minmax(80px, 1fr);
  gap: 8px;
  padding: 10px 0;
  border-top: 1px solid #edf0f4;
  font-size: 12px;
}
.batch-item code {
  overflow-wrap: anywhere;
}
.batch-item small {
  color: #788599;
}
@media (max-width: 600px) {
  summary {
    gap: 8px;
  }
  summary .spacer {
    display: none;
  }
  summary small {
    flex-basis: 100%;
  }
  .batch-item {
    grid-template-columns: 1fr 70px;
  }
}
</style>
