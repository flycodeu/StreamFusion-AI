<script setup lang="ts">
defineProps<{ initialLoading?: boolean }>()
</script>

<template>
  <section class="table-panel">
    <div class="table-panel__toolbar">
      <div class="table-panel__actions"><slot name="actions" /></div>
      <div class="table-panel__tools"><slot name="tools" /></div>
    </div>
    <div v-if="initialLoading" class="table-skeleton" role="status" aria-label="正在加载数据">
      <div v-for="row in 6" :key="row" class="table-skeleton__row" aria-hidden="true">
        <span /><span /><span /><span />
      </div>
    </div>
    <slot v-else />
    <div v-if="$slots.footer" class="table-panel__footer"><slot name="footer" /></div>
  </section>
</template>

<style scoped>
.table-panel {
  min-width: 0;
  padding: 16px;
  background: var(--el-bg-color);
  border: 1px solid var(--el-border-color-light);
  border-radius: 4px;
}
.table-skeleton {
  padding: 8px 0;
}
.table-skeleton__row {
  display: grid;
  grid-template-columns: 2fr 3fr 2fr 1fr;
  gap: 24px;
  padding: 16px 12px;
  border-bottom: 1px solid var(--el-border-color-lighter);
}
.table-skeleton__row span {
  height: 16px;
  border-radius: 4px;
  background: var(--el-fill-color);
  animation: skeleton-pulse 1.6s ease-in-out infinite alternate;
}
@keyframes skeleton-pulse {
  to {
    opacity: 0.4;
  }
}
@media (prefers-reduced-motion: reduce) {
  .table-skeleton__row span {
    animation: none;
  }
}
.table-panel__toolbar {
  display: flex;
  align-items: center;
  justify-content: space-between;
  flex-wrap: wrap;
  gap: 12px;
  margin-bottom: 16px;
}
.table-panel__actions,
.table-panel__tools {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 8px;
}
.table-panel__actions :deep(.el-button + .el-button),
.table-panel__tools :deep(.el-button + .el-button) {
  margin-left: 0;
}
.table-panel__footer {
  display: flex;
  justify-content: flex-end;
  margin-top: 16px;
  overflow-x: auto;
}
</style>
