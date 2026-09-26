<template>
  <section class="table-panel">
    <div class="table-panel__toolbar">
      <div class="table-panel__actions"><slot name="actions" /></div>
      <div class="table-panel__tools"><slot name="tools" /></div>
    </div>
    <slot />
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
