import { ref, watch } from 'vue'

export function useColumns(
  key: string,
  defaults: readonly string[],
  supported: readonly string[] = defaults,
) {
  const storageKey = `sf-columns-${key}`
  const supportedKeys = new Set(supported)
  const normalize = (value: unknown[]): string[] =>
    [...new Set(value)].filter(
      (item): item is string => typeof item === 'string' && supportedKeys.has(item),
    )
  let initial = normalize([...defaults])
  try {
    const saved: unknown = JSON.parse(localStorage.getItem(storageKey) || 'null')
    if (Array.isArray(saved)) initial = normalize(saved)
  } catch {
    /* Column preferences are optional when storage is unavailable or damaged. */
  }
  const columns = ref<string[]>(initial)
  watch(
    columns,
    (value) => {
      try {
        localStorage.setItem(storageKey, JSON.stringify(normalize(value)))
      } catch {
        /* Keep the current selection usable if storage is blocked or full. */
      }
    },
    { deep: true },
  )
  return columns
}
