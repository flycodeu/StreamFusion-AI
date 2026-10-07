<script setup lang="ts">
import { ElDialog } from 'element-plus'

defineOptions({ inheritAttrs: false })
defineProps<{ embedded?: boolean }>()
</script>
<template>
  <section v-if="embedded" class="camera-inline-panel">
    <slot />
    <div v-if="$slots.footer" class="camera-inline-actions"><slot name="footer" /></div>
  </section>
  <ElDialog v-else v-bind="$attrs">
    <slot />
    <template v-if="$slots.footer" #footer><slot name="footer" /></template>
  </ElDialog>
</template>
<style scoped>
.camera-inline-actions {
  display: flex;
  justify-content: flex-end;
  gap: 8px;
  padding-top: 18px;
  margin-top: 20px;
  border-top: 1px solid var(--el-border-color-lighter);
}
.camera-inline-actions :deep(.el-button + .el-button) {
  margin-left: 0;
}
</style>
