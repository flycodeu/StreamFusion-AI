export type Parser<T> = (value: unknown) => T

export interface ApiResult<T> {
  data: T
  meta: {
    status: number
    traceId: string
    timestamp: string
    etag: string | null
    location: string | null
  }
}

export interface RequestOptions<T> {
  path: string
  method: 'GET' | 'POST' | 'PUT' | 'DELETE'
  params?: Record<string, string | number | boolean | null | undefined>
  body?: unknown
  ifMatch?: string
  successStatus: 200 | 201
  decode: Parser<T>
  signal?: AbortSignal
  timeoutMs?: number
}

export interface RawHttpResponse {
  status: number
  headers: {
    contentType: string | null
    traceId: string | null
    etag: string | null
    location: string | null
    retryAfter: string | null
  }
  bodyText: string
  requestTraceId: string
  receivedAt: string
}

export interface CsrfToken {
  headerName: string
  token: string
}

export interface TransportOptions {
  path: string
  method: RequestOptions<unknown>['method']
  params?: RequestOptions<unknown>['params']
  body?: unknown
  ifMatch?: string
  csrf?: CsrfToken
  signal?: AbortSignal
  timeoutMs?: number
}
