import { computed, ref, watch, type WatchSource } from "vue";

export const pageSizes = [10, 20, 50, 100];

export function usePagination<T>(
  items: () => readonly T[],
  resetSources: WatchSource[] = [],
) {
  const page = ref(1), pageSize = ref(10);
  const total = computed(() => items().length);
  const pageCount = computed(() => Math.max(1, Math.ceil(total.value / pageSize.value)));
  watch([pageSize, ...resetSources], () => { page.value = 1; }, { flush: "sync" });
  watch(pageCount, count => { page.value = Math.min(page.value, count); }, { flush: "sync" });
  const pageItems = computed(() => {
    const start = (Math.max(1, Math.min(page.value, pageCount.value)) - 1) * pageSize.value;
    return items().slice(start, start + pageSize.value);
  });
  return { page, pageSize, total, pageCount, pageItems };
}
