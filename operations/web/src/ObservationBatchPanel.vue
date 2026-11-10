<script setup lang="ts">
import { computed, ref } from "vue";
import { ElPopover } from "element-plus";
import { hostTime } from "./hosts";
import { observationPending, type ObservationBatch } from "./observation";
import { findErrorReference } from "./errorReference";
const props = defineProps<{ batch?: ObservationBatch; busy: boolean }>();
const emit = defineEmits<{ cancel: [] }>();
const open = ref(false);
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
  <ElPopover
    v-if="batch"
    v-model:visible="open"
    trigger="click"
    placement="bottom-end"
    :width="'min(520px, calc(100vw - 32px))'"
    :popper-options="{ modifiers: [{ name: 'preventOverflow', options: { padding: 16 } }] }"
    :show-arrow="false"
  >
    <template #reference>
      <button class="secondary detection-trigger" aria-label="检测记录" :aria-expanded="open">
        <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.7" aria-hidden="true">
          <circle cx="12" cy="12" r="9" /><path d="M12 7v5l3 2" />
        </svg>
        {{ observationPending(batch) ? `检测中 ${completed} / ${batch.items.length}` : "检测记录" }}
      </button>
    </template>
    <section class="observation-batch" aria-label="最近检测结果">
      <header class="batch-heading">
        <strong>检测记录</strong>
        <button class="quiet" aria-label="关闭检测记录" @click="open = false">×</button>
      </header>
      <div class="batch-summary">
      <strong>{{ completed }} / {{ batch.items.length }}</strong>
      <span>{{ observationPending(batch) ? "进行中" : "已结束" }}</span>
      <span v-if="failed" class="batch-warning">未确认 {{ failed }}</span>
      <span v-if="skipped">跳过 {{ skipped }}</span>
      </div>
      <small class="batch-time">{{ hostTime(batch.createdAt) }}</small>
    <div v-if="observationPending(batch)" class="batch-detail-head">
      <button
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
        <span>{{ labels[item.status] || "状态待确认" }}</span>
        <span v-if="item.errorCode">{{ findErrorReference(item.errorCode)?.name || "检测未完成，请重新检查" }}</span>
        <small v-if="item.observedAt">{{ hostTime(item.observedAt) }}</small>
      </div>
    </div>
    </section>
  </ElPopover>
</template>
<style scoped>
.detection-trigger {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  gap: 6px;
  min-height: 38px;
  margin: 0;
  white-space: nowrap;
}
.batch-heading {
  display: flex;
  align-items: center;
  justify-content: space-between;
  margin-bottom: 8px;
  color: var(--text);
}
.batch-heading button {
  padding: 2px 6px;
  font-size: 20px;
}
.batch-summary {
  display: flex;
  flex-wrap: wrap;
  gap: 10px;
  font-size: 13px;
}
.batch-summary strong {
  font-variant-numeric: tabular-nums;
}
.batch-time {
  display: block;
  margin: 8px 0 12px;
  color: var(--muted);
}
.batch-warning {
  color: #a16616;
}
.batch-detail-head {
  display: flex;
  justify-content: flex-end;
  padding: 8px 0;
  border-top: 1px solid #e7ebf0;
}
.batch-items {
  max-height: min(360px, 50vh);
  overflow: auto;
}
.batch-item {
  display: grid;
  grid-template-columns: minmax(0, 1fr) auto;
  gap: 8px;
  padding: 10px 0;
  border-top: 1px solid #edf0f4;
  font-size: 12px;
}
.batch-item > * {
  overflow-wrap: anywhere;
}
.batch-item > span:nth-of-type(2) {
  grid-column: 1 / -1;
}
.batch-item small {
  grid-column: 1 / -1;
  color: #788599;
}
</style>
