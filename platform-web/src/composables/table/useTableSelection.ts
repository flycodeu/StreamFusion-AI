import { computed, shallowRef } from 'vue'

export function useTableSelection<Row>() {
  const selectedRows = shallowRef<Row[]>([])
  const singleSelected = computed(() =>
    selectedRows.value.length === 1 ? selectedRows.value[0] : null,
  )

  function setSelection(rows: unknown[]): void {
    selectedRows.value = rows as Row[]
  }

  function clearSelection(): void {
    selectedRows.value = []
  }

  return { selectedRows, singleSelected, setSelection, clearSelection }
}
