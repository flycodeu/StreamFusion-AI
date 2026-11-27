<script setup lang="ts">
import { ElButton, ElOption, ElSelect } from 'element-plus'

withDefaults(
  defineProps<{
    page: number
    pageSize: number
    pageSizes?: number[]
    hasPrev: boolean
    hasNext: boolean
    disabled?: boolean
    summary?: string
    pageSummary?: string
    showPageSize?: boolean
  }>(),
  {
    pageSizes: () => [20, 50, 100],
    disabled: false,
    summary: '',
    pageSummary: '',
    showPageSize: true,
  },
)

const emit = defineEmits<{
  'update:pageSize': [size: number]
  sizeChange: [size: number]
  prev: []
  next: []
}>()
</script>

<template>
  <div class="sf-cursor-pagination">
    <span v-if="summary" class="sf-cursor-pagination__summary">{{ summary }}</span>
    <div class="sf-cursor-pagination__controls">
      <span class="sf-cursor-pagination__page">{{ pageSummary || `第 ${page} 页` }}</span>
      <ElSelect
        v-if="showPageSize"
        :model-value="pageSize"
        aria-label="每页条数"
        class="sf-cursor-pagination__size"
        :disabled="disabled"
        @update:model-value="emit('update:pageSize', $event)"
        @change="emit('sizeChange', $event)"
      >
        <ElOption v-for="size in pageSizes" :key="size" :value="size" :label="`${size} 条/页`" />
      </ElSelect>
      <ElButton :disabled="disabled || !hasPrev" @click="emit('prev')">上一页</ElButton>
      <ElButton :disabled="disabled || !hasNext" @click="emit('next')">下一页</ElButton>
    </div>
  </div>
</template>

<style scoped>
.sf-cursor-pagination {
  display: flex;
  align-items: center;
  justify-content: space-between;
  flex-wrap: wrap;
  gap: 10px 14px;
  width: 100%;
  color: var(--sf-text-secondary);
  font-size: var(--sf-fs-sub);
  font-variant-numeric: tabular-nums;
}

.sf-cursor-pagination__summary {
  margin-right: auto;
}

.sf-cursor-pagination__controls {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 8px;
  margin-left: auto;
}

.sf-cursor-pagination__controls :deep(.el-button + .el-button) {
  margin-left: 0;
}

.sf-cursor-pagination__page {
  white-space: nowrap;
}

.sf-cursor-pagination__size {
  width: 112px;
}
</style>
