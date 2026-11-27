<script setup lang="ts">
defineProps<{ compact?: boolean }>()
</script>

<template>
  <div class="table-actions" :class="{ 'is-compact': compact }" role="group" aria-label="行操作">
    <slot />
  </div>
</template>

<style scoped>
.table-actions {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 8px;
  min-width: 0;
  padding-block: 2px;
}

.table-actions :deep(.el-button) {
  flex: 0 0 auto;
  min-width: 60px;
  min-height: 30px;
  height: auto;
  margin: 0;
  padding: 6px 12px;
  font-size: var(--sf-fs-body);
  line-height: 18px;
}

.table-actions :deep(.table-action-end) {
  margin-inline-start: auto;
}

.table-actions :deep(.el-switch) {
  min-height: 30px;
}

.table-actions.is-compact {
  gap: 12px;
}

.table-actions.is-compact :deep(.el-button.is-link) {
  min-width: 32px;
  padding: 4px 0;
  font-size: var(--sf-fs-sub);
  font-weight: 500;
}

@media (pointer: coarse) {
  .table-actions :deep(.el-button) {
    min-height: 44px;
  }
}
</style>
