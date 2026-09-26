const dateTimeFormatter = new Intl.DateTimeFormat('zh-CN', {
  timeZone: 'Asia/Shanghai',
  dateStyle: 'short',
  timeStyle: 'medium',
  hour12: false,
})

/** All management timestamps use the project timezone, independent of browser settings. */
export function formatDateTime(value: string | Date | null | undefined): string {
  if (!value) return '—'
  const date = value instanceof Date ? value : new Date(value)
  return Number.isFinite(date.getTime()) ? dateTimeFormatter.format(date) : '—'
}
