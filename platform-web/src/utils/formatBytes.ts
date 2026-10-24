const units = ['B', 'KiB', 'MiB', 'GiB', 'TiB', 'PiB', 'EiB'] as const

/** Byte counts may arrive as decimal strings; retain integer precision before rounding. */
export function formatBytes(
  value: number | string | bigint | null | undefined,
  options: { unit?: (typeof units)[number]; precision?: number; empty?: string } = {},
): string {
  const empty = options.empty ?? '—'
  if (value == null || value === '') return empty
  if (typeof value === 'number' && (!Number.isSafeInteger(value) || value < 0)) return empty
  if (typeof value === 'string' && !/^\d+$/.test(value)) return empty
  const bytes = BigInt(value)
  if (bytes < 0n) return empty
  let index = options.unit ? units.indexOf(options.unit) : 0
  if (!options.unit) while (index < units.length - 1 && bytes >= 1024n ** BigInt(index + 1)) ++index
  const requested = options.precision ?? 2
  const precision = Number.isFinite(requested) ? Math.max(0, Math.min(3, Math.trunc(requested))) : 2
  const scale = 10n ** BigInt(precision)
  const divisor = 1024n ** BigInt(index)
  const rounded = (bytes * scale + divisor / 2n) / divisor
  const fraction = precision ? `.${(rounded % scale).toString().padStart(precision, '0')}` : ''
  return `${rounded / scale}${fraction} ${units[index]}`
}
