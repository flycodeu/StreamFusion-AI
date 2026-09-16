import { ApiRequestError, invalidResponse, isTraceId, isUtcTimestamp } from './error'
import type { ErrorContext } from './error'
import type { ApiResult, RawHttpResponse } from './types'

const record = (value: unknown): value is Record<string, unknown> =>
  typeof value === 'object' && value !== null && !Array.isArray(value)

export function responseContext(raw: RawHttpResponse): ErrorContext {
  return {
    status: raw.status,
    traceId: isTraceId(raw.headers.traceId) ? raw.headers.traceId : raw.requestTraceId,
    receivedAt: raw.receivedAt,
  }
}

function parseJson(raw: RawHttpResponse): unknown {
  try {
    return JSON.parse(raw.bodyText) as unknown
  } catch {
    return undefined
  }
}

function retrySeconds(raw: RawHttpResponse): number | undefined {
  const value = raw.headers.retryAfter
  if (!value) return undefined
  if (/^\d+$/.test(value)) {
    const seconds = Number(value)
    return Number.isSafeInteger(seconds) ? seconds : undefined
  }
  // HTTP-date, not arbitrary Date.parse-compatible numbers or local dates.
  if (
    !/^(Mon|Tue|Wed|Thu|Fri|Sat|Sun), \d{2} [A-Z][a-z]{2} \d{4} \d{2}:\d{2}:\d{2} GMT$/.test(value)
  )
    return undefined
  const deadline = Date.parse(value)
  return Number.isFinite(deadline)
    ? Math.max(0, Math.ceil((deadline - Date.parse(raw.receivedAt)) / 1000))
    : undefined
}

interface Envelope {
  code: string
  msg: string
  data: unknown
  traceId: string
  timestamp: string
}

function isEnvelope(body: unknown): body is Envelope {
  return (
    record(body) &&
    typeof body.code === 'string' &&
    /^[A-Z][A-Z0-9_]{0,63}$/.test(body.code) &&
    typeof body.msg === 'string' &&
    'data' in body &&
    isTraceId(body.traceId) &&
    isUtcTimestamp(body.timestamp)
  )
}

export function parseEnvelope(raw: RawHttpResponse): ApiResult<unknown> {
  const context = responseContext(raw)
  const body = parseJson(raw)
  const ok = raw.status >= 200 && raw.status < 300
  const jsonType =
    raw.headers.contentType?.split(';')[0]?.trim().toLowerCase() === 'application/json'
  if (!jsonType || !isEnvelope(body)) {
    if (ok) throw invalidResponse(context)
    throw new ApiRequestError(`HTTP_${raw.status}`, `请求失败（HTTP ${raw.status}）`, context)
  }
  const details = { ...context, traceId: body.traceId, timestamp: body.timestamp }
  if (raw.headers.traceId !== null && raw.headers.traceId !== body.traceId)
    throw invalidResponse(context)
  if (ok !== (body.code === 'SUCCESS')) throw invalidResponse(details)
  if (!ok) {
    // The body data stays unknown; a feature-specific consumer must validate before displaying it.
    throw new ApiRequestError(body.code, body.msg, {
      ...details,
      data: body.data,
      retryAfter: retrySeconds(raw),
    })
  }
  return {
    data: body.data,
    meta: {
      status: raw.status,
      traceId: body.traceId,
      timestamp: body.timestamp,
      etag: raw.headers.etag,
      location: raw.headers.location,
    },
  }
}

/** Only for existing raw consumers, such as Actuator, never a business API option. */
export function parseRawJson(raw: RawHttpResponse): unknown {
  const context = responseContext(raw)
  const body = parseJson(raw)
  if (raw.status < 200 || raw.status >= 300) {
    const code =
      record(body) && typeof body.code === 'string' && /^[A-Z][A-Z0-9_]{0,63}$/.test(body.code)
        ? body.code
        : `HTTP_${raw.status}`
    throw new ApiRequestError(code, `请求失败（HTTP ${raw.status}）`, context)
  }
  if (body === undefined) throw invalidResponse(context)
  return body
}
