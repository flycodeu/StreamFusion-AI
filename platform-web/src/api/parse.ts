export type ObjectValue = Record<string, unknown>

export function object(value: unknown): ObjectValue {
  if (typeof value !== 'object' || value === null || Array.isArray(value)) throw new Error('object')
  return value as ObjectValue
}

export function string(value: unknown): string {
  if (typeof value !== 'string') throw new Error('string')
  return value
}

export function optionalString(value: unknown): string | null {
  return value === null || value === undefined ? null : string(value)
}

export function id(value: unknown): string {
  const result = string(value)
  if (!/^(0|[1-9]\d*)$/.test(result)) throw new Error('id')
  return result
}

export function integer(value: unknown): number {
  if (typeof value !== 'number' || !Number.isSafeInteger(value)) throw new Error('integer')
  return value
}

export function boolean(value: unknown): boolean {
  if (typeof value !== 'boolean') throw new Error('boolean')
  return value
}

export function list<T>(value: unknown, parse: (entry: unknown) => T): T[] {
  if (!Array.isArray(value) || value.length > 1000) throw new Error('list')
  return value.map((entry) => parse(entry))
}

export function page<T>(value: unknown, parse: (entry: unknown) => T): Page<T> {
  const row = object(value)
  const result = {
    items: list(row.items, parse),
    page: integer(row.page),
    size: integer(row.size),
    total: integer(row.total),
  }
  if (result.page < 1 || result.size < 1 || result.size > 100 || result.total < 0)
    throw new Error('page')
  return result
}

export interface Page<T> {
  items: T[]
  page: number
  size: number
  total: number
}

export function empty(value: unknown): null {
  if (value !== null) throw new Error('empty')
  return null
}
