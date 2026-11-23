import { computed, onMounted, onUnmounted, ref, watch, type WatchSource } from "vue";

export const pageSizes = [10, 20, 50, 100];

export function usePagination<T>(
  items: () => readonly T[],
  resetSources: WatchSource[] = [],
  initialPageSize = 10,
) {
  const page = ref(1), pageSize = ref(initialPageSize);
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

function useAdaptivePageSizes<T>(
  items: () => readonly T[], resetSources: WatchSource[], initialOptions: number[], initialSize: number,
) {
  const pager = usePagination(items, resetSources, initialSize);
  const pageSizeOptions = ref(initialOptions);

  function updateOptions(options: number[]) {
    const previous = pageSizeOptions.value;
    if (options.length === previous.length && options.every((size, index) => size === previous[index])) return;
    const preference = previous.indexOf(pager.pageSize.value);
    const firstIndex = (pager.page.value - 1) * pager.pageSize.value;
    pageSizeOptions.value = options;
    pager.pageSize.value = options[preference] ?? options[1]!;
    pager.page.value = Math.min(pager.pageCount.value, Math.floor(firstIndex / pager.pageSize.value) + 1);
  }

  return { ...pager, pageSizeOptions, updateOptions };
}

export function useGridPagination<T>(items: () => readonly T[], resetSources: WatchSource[] = []) {
  const rows = [2, 3, 5];
  const { updateOptions, ...pager } = useAdaptivePageSizes(items, resetSources, rows, 3);
  const grid = ref<HTMLElement | null>(null);

  watch(grid, (element, _, onCleanup) => {
    if (!element) return;
    const measure = () => {
      if (!element.clientWidth) return;
      // Resolved CSS grid tracks also include the empty columns on the last page.
      const columns = getComputedStyle(element).gridTemplateColumns.trim().split(/\s+/).length;
      updateOptions(rows.map(rowCount => rowCount * columns));
    };
    const observer = new ResizeObserver(measure);
    observer.observe(element);
    measure();
    onCleanup(() => observer.disconnect());
  }, { flush: "post" });

  return { ...pager, grid };
}

export function useCompactListPagination<T>(items: () => readonly T[], resetSources: WatchSource[] = [], compactWidth = 710) {
  const desktop = [5, ...pageSizes];
  const compact = [3, 5, 10, 25, 50];
  const { updateOptions, ...pager } = useAdaptivePageSizes(items, resetSources, desktop, 10);
  let media: MediaQueryList | undefined;
  const sync = () => updateOptions(media?.matches ? compact : desktop);
  onMounted(() => {
    media = window.matchMedia(`(max-width: ${compactWidth}px)`);
    media.addEventListener("change", sync);
    sync();
  });
  onUnmounted(() => media?.removeEventListener("change", sync));
  return pager;
}
