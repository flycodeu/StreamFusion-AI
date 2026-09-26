import { ApiRequestError, isTraceId } from './error'
import type { RawHttpResponse, TransportOptions } from './types'

function invalid(): never {
  throw new ApiRequestError('INVALID_REQUEST', '请求参数不合法')
}

function prepare(options: TransportOptions): { url: string; headers: Headers; body?: string } {
  const { path, method, body, params, ifMatch, csrf } = options
  if (!['GET', 'POST', 'PUT', 'DELETE'].includes(method)) invalid()
  if (typeof path !== 'string' || !path.startsWith('/') || /[?#\\\s]/.test(path)) invalid()
  let parsed: URL
  try {
    parsed = new URL(path, 'http://same-origin.invalid')
  } catch {
    invalid()
  }
  if (parsed.origin !== 'http://same-origin.invalid' || parsed.pathname !== path) invalid()
  if (!(
    /^\/(?:user|auth|roles|menus|departments)(?:\/|$)/.test(path) || path === '/actuator/health'
  ))
    invalid()
  // Encoded separators/dot segments cannot escape the declared API boundary.
  if (/%(?:2e|2f|5c|25)/i.test(path)) invalid()
  if (method === 'GET' && (body !== undefined || ifMatch !== undefined)) invalid()
  if (ifMatch !== undefined && !/^"(?:0|[1-9]\d*)"$/.test(ifMatch)) invalid()

  const query = new URLSearchParams()
  for (const [key, value] of Object.entries(params ?? {})) {
    if (value === null || value === undefined) continue
    if (!['string', 'number', 'boolean'].includes(typeof value)) invalid()
    if (typeof value === 'number' && !Number.isFinite(value)) invalid()
    query.set(key, String(value))
  }
  const headers = new Headers({ Accept: 'application/json' })
  if (ifMatch !== undefined) headers.set('If-Match', ifMatch)
  if (csrf !== undefined) {
    // Only dedicated CSRF headers may come from the session coordinator.
    if (
      typeof csrf.headerName !== 'string' ||
      typeof csrf.token !== 'string' ||
      !/^X-[A-Za-z0-9-]*CSRF[A-Za-z0-9-]*$/i.test(csrf.headerName) ||
      !csrf.token ||
      /[\r\n]/.test(csrf.token)
    )
      invalid()
    try {
      headers.set(csrf.headerName, csrf.token)
    } catch {
      invalid()
    }
  }
  let serialized: string | undefined
  if (body !== undefined) {
    try {
      serialized = JSON.stringify(body, (_key, value: unknown) => {
        if (typeof value === 'bigint' || typeof value === 'function' || typeof value === 'symbol')
          invalid()
        if (typeof value === 'number' && !Number.isFinite(value)) invalid()
        return value
      })
      if (serialized === undefined) invalid()
    } catch {
      invalid()
    }
    headers.set('Content-Type', 'application/json')
  }
  const suffix = query.toString()
  return { url: path + (suffix ? `?${suffix}` : ''), headers, body: serialized }
}

/** The only fetch call. Deadline includes consuming the response body; never retries. */
export async function send(options: TransportOptions): Promise<RawHttpResponse> {
  const timeoutMs = options.timeoutMs ?? 5000
  if (!Number.isSafeInteger(timeoutMs) || timeoutMs <= 0 || timeoutMs > 2_147_483_647) invalid()
  const prepared = prepare(options)
  const requestTraceId = crypto.randomUUID().replaceAll('-', '')
  prepared.headers.set('X-Trace-Id', requestTraceId)
  const controller = new AbortController()
  let reason: 'REQUEST_CANCELLED' | 'REQUEST_TIMEOUT' | undefined
  const abort = (code: typeof reason) => {
    if (reason === undefined) {
      reason = code
      controller.abort()
    }
  }
  const cancel = () => abort('REQUEST_CANCELLED')
  options.signal?.addEventListener('abort', cancel, { once: true })
  if (options.signal?.aborted) cancel()
  const timer = setTimeout(() => abort('REQUEST_TIMEOUT'), timeoutMs)
  let status: number | undefined
  let traceId = requestTraceId
  try {
    controller.signal.throwIfAborted()
    const response = await fetch(prepared.url, {
      method: options.method,
      headers: prepared.headers,
      body: prepared.body,
      credentials: 'same-origin',
      redirect: 'error',
      cache: 'no-store',
      signal: controller.signal,
    })
    status = response.status
    const responseTrace = response.headers.get('X-Trace-Id')
    if (isTraceId(responseTrace)) traceId = responseTrace
    const bodyText = response.status === 204 || response.status === 304 ? '' : await response.text()
    controller.signal.throwIfAborted()
    return {
      status,
      headers: {
        contentType: response.headers.get('Content-Type'),
        traceId: responseTrace,
        etag: response.headers.get('ETag'),
        location: response.headers.get('Location'),
        retryAfter: response.headers.get('Retry-After'),
      },
      bodyText,
      requestTraceId,
      receivedAt: new Date().toISOString(),
    }
  } catch {
    const code = reason ?? 'NETWORK_ERROR'
    const message =
      code === 'REQUEST_TIMEOUT'
        ? '请求超时'
        : code === 'REQUEST_CANCELLED'
          ? '请求已取消'
          : '网络连接失败'
    throw new ApiRequestError(code, message, { status, traceId })
  } finally {
    clearTimeout(timer)
    options.signal?.removeEventListener('abort', cancel)
  }
}
