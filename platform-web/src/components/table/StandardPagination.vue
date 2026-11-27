<script setup lang="ts">
import { ElPagination } from 'element-plus'

const props = withDefaults(
  defineProps<{
    page: number
    size: number
    total: number
    pageSizes?: number[]
    disabled?: boolean
    small?: boolean
    layout?: string
    pagerCount?: number
  }>(),
  {
    pageSizes: () => [20, 50, 100],
    disabled: false,
    small: false,
    layout: 'total, sizes, prev, pager, next, jumper',
  },
)

const emit = defineEmits<{
  'update:page': [page: number]
  'update:size': [size: number]
  change: [page: number, size: number]
}>()

function onPageUpdate(nextPage: number): void {
  emit('update:page', nextPage)
}

function onSizeUpdate(nextSize: number): void {
  emit('update:size', nextSize)
  if (props.page !== 1) emit('update:page', 1)
}

function onSizeChange(): void {
  if (props.page !== 1) emit('update:page', 1)
}

function onChange(nextPage: number, nextSize: number): void {
  const targetPage = nextSize !== props.size ? 1 : nextPage
  emit('change', targetPage, nextSize)
}
</script>

<template>
  <ElPagination
    class="sf-standard-pagination"
    :current-page="page"
    :page-size="size"
    :page-sizes="pageSizes"
    :total="total"
    :disabled="disabled"
    :size="small ? 'small' : 'default'"
    :layout="layout"
    :pager-count="pagerCount"
    @update:current-page="onPageUpdate"
    @update:page-size="onSizeUpdate"
    @size-change="onSizeChange"
    @change="onChange"
  />
</template>

<style scoped>
.sf-standard-pagination {
  justify-content: flex-end;
  flex-wrap: wrap;
  row-gap: 8px;
  font-variant-numeric: tabular-nums;
}
</style>
