export interface ErrorContext {
  status?: number
  traceId?: string
  timestamp?: string
  receivedAt?: string
  data?: unknown
  retryAfter?: number
}

/** Safe diagnostic metadata; never includes request bodies, cookies or raw response text. */
export class ApiRequestError extends Error {
  readonly code: string
  readonly status?: number
  readonly traceId: string
  readonly timestamp?: string
  readonly receivedAt: string
  readonly data: unknown
  readonly retryAfter?: number

  constructor(code: string, message: string, context: ErrorContext = {}) {
    super(
      Array.from(message, (char) =>
        char.charCodeAt(0) < 32 || char.charCodeAt(0) === 127 ? ' ' : char,
      )
        .join('')
        .slice(0, 200),
    )
    this.name = 'ApiRequestError'
    this.code = code
    this.status = context.status
    this.traceId = context.traceId ?? ''
    this.timestamp = context.timestamp
    this.receivedAt = context.receivedAt ?? new Date().toISOString()
    this.data = context.data ?? null
    this.retryAfter = context.retryAfter
  }
}

export function invalidResponse(context: ErrorContext = {}): ApiRequestError {
  return new ApiRequestError(
    'INVALID_RESPONSE',
    '服务响应格式不正确，请提供请求标识以便排查',
    context,
  )
}

export const isTraceId = (value: unknown): value is string =>
  typeof value === 'string' && /^[a-f0-9]{32}$/.test(value)

export function isUtcTimestamp(value: unknown): value is string {
  if (
    typeof value !== 'string' ||
    !/^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(?:\.\d{1,9})?Z$/.test(value)
  ) {
    return false
  }
  const parsed = new Date(value)
  return (
    Number.isFinite(parsed.getTime()) && parsed.toISOString().slice(0, 19) === value.slice(0, 19)
  )
}
