/** Preserve unknown codes for diagnostics without reading inherited object properties. */
export function labelOf(
  labels: Readonly<Record<string, string>>,
  value: string | number | null | undefined,
  empty = '—',
): string {
  if (value == null || value === '') return empty
  const key = String(value)
  return Object.hasOwn(labels, key) ? labels[key]! : key
}
