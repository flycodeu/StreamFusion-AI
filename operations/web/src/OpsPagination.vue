<script setup lang="ts">
import { computed } from "vue";
import { ElButton, ElConfigProvider, ElOption, ElPagination, ElSelect } from "element-plus";
import zhCn from "element-plus/es/locale/lang/zh-cn";
import { pageSizes } from "./pagination";

const props = withDefaults(defineProps<{
  page?: number;
  pageSize?: number;
  total?: number;
  hasNext?: boolean;
  disabled?: boolean;
  label?: string;
}>(), { page: 1, pageSize: 10, disabled: false, label: "列表分页" });
const emit = defineEmits<{ "update:page": [value: number]; "update:pageSize": [value: number] }>();
const canPrevious = computed(() => props.page > 1);
function changePage(value: number) {
  if (props.disabled || value === props.page) return;
  if (props.total === undefined && (value < props.page ? !canPrevious.value : !props.hasNext)) return;
  const last = props.total === undefined ? value : Math.max(1, Math.ceil(props.total / props.pageSize));
  const next = Math.max(1, Math.min(value, last));
  if (next !== props.page) emit("update:page", next);
}
function changeSize(value: number) {
  if (!props.disabled && value !== props.pageSize && pageSizes.includes(value)) emit("update:pageSize", value);
}
</script>

<template>
  <nav class="ops-pagination" :aria-label="label">
    <ElConfigProvider :locale="zhCn" size="small">
      <ElPagination
        v-if="total !== undefined"
        :current-page="page"
        :page-size="pageSize"
        :page-sizes="pageSizes"
        :total="total"
        :disabled="disabled"
        :pager-count="5"
        layout="total, sizes, prev, pager, next"
        size="small"
        background
        @update:current-page="changePage"
        @update:page-size="changeSize"
      />
      <div v-else class="cursor-pagination">
        <ElSelect :model-value="pageSize" :disabled="disabled" :aria-label="`${label}每页条数`" @update:model-value="changeSize">
          <ElOption v-for="size in pageSizes" :key="size" :label="`${size} 条/页`" :value="size" />
        </ElSelect>
        <span>第 {{ page }} 页</span>
        <ElButton :disabled="disabled || !canPrevious" @click="changePage(page - 1)">上一页</ElButton>
        <ElButton :disabled="disabled || !hasNext" @click="changePage(page + 1)">下一页</ElButton>
      </div>
    </ElConfigProvider>
  </nav>
</template>

<style scoped>
.ops-pagination {
  display: flex;
  justify-content: flex-end;
  max-width: 100%;
  margin-top: 18px;
  color: var(--muted);
  font-size: 12px;
  --el-pagination-font-size: 12px;
  --el-pagination-button-height: 30px;
  --el-component-size-small: 30px;
}
.ops-pagination :deep(.el-pagination) { flex-wrap: wrap; justify-content: flex-end; gap: 8px 0; }
.ops-pagination :deep(.el-pagination__total), .ops-pagination :deep(.el-select), .cursor-pagination { font-size: 12px; }
.cursor-pagination { display: flex; flex-wrap: wrap; align-items: center; justify-content: flex-end; gap: 8px; }
.cursor-pagination .el-select { width: 106px; }
.cursor-pagination .el-button { height: 30px; margin-left: 0; padding: 0 10px; font-size: 12px; }
</style>
