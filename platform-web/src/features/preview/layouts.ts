interface LayoutDefinition {
  value: string
  label: string
  columns: number
  rows: number
  leadSpan?: number
}

export const MAX_PREVIEW_SLOTS = 64
export const previewLayouts = [
  { value: '1', label: '单屏', columns: 1, rows: 1 },
  { value: '2', label: '双屏', columns: 2, rows: 1 },
  { value: '4', label: '四分屏', columns: 2, rows: 2 },
  { value: '6', label: '六分屏', columns: 3, rows: 2 },
  { value: '8', label: '八分屏', columns: 4, rows: 2 },
  { value: '9', label: '九分屏', columns: 3, rows: 3 },
  { value: '12', label: '十二分屏', columns: 4, rows: 3 },
  { value: '16', label: '十六分屏', columns: 4, rows: 4 },
  { value: '25', label: '二十五分屏', columns: 5, rows: 5 },
  { value: '36', label: '三十六分屏', columns: 6, rows: 6 },
  { value: '49', label: '四十九分屏', columns: 7, rows: 7 },
  { value: '64', label: '六十四分屏', columns: 8, rows: 8 },
  { value: '1+5', label: '一大五小', columns: 3, rows: 3, leadSpan: 2 },
  { value: '1+7', label: '一大七小', columns: 4, rows: 4, leadSpan: 3 },
  { value: '1+12', label: '一大十二小', columns: 4, rows: 4, leadSpan: 2 },
] as const satisfies readonly LayoutDefinition[]

export type PreviewLayout = (typeof previewLayouts)[number]['value']
export function getPreviewLayout(value: PreviewLayout): LayoutDefinition {
  return previewLayouts.find((layout) => layout.value === value)!
}
export function layoutCount(value: PreviewLayout): number {
  const { columns, rows, leadSpan = 1 } = getPreviewLayout(value)
  return columns * rows - leadSpan * leadSpan + 1
}
export function deviceLayout(count: number): PreviewLayout {
  return (
    previewLayouts.find(
      (layout) => !layout.value.includes('+') && layoutCount(layout.value) >= count,
    )?.value || '64'
  )
}
