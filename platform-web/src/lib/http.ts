export class ApiRequestError extends Error {
  constructor(
    message: string,
    readonly code: string,
    readonly traceId: string,
    readonly status?: number,
  ) {
    super(message)
    this.name = 'ApiRequestError'
  }
}

/** One request, bounded timeout, no implicit retries for future state-changing APIs. */
export async function getJson(path: string, timeoutMs = 5000): Promise<unknown> {
  const traceId = crypto.randomUUID().replaceAll('-', '')
  const signal = AbortSignal.timeout(timeoutMs)
  let responseTrace = traceId
  let status: number | undefined
  try {
    const response = await fetch(path, {
      headers: { 'X-Trace-Id': traceId },
      signal,
    })
    responseTrace = response.headers.get('X-Trace-Id') || traceId
    status = response.status
    let body: unknown
    try {
      body = await response.json()
    } catch (error: unknown) {
      // Only malformed JSON may be ignored for a non-JSON proxy error.
      // Timeouts and broken response streams belong to the outer request handler.
      if (!(error instanceof SyntaxError) || response.ok) throw error
    }
    if (!response.ok) {
      let code = `HTTP_${response.status}`
      if (
        typeof body === 'object' &&
        body !== null &&
        'code' in body &&
        typeof body.code === 'string'
      ) {
        code = body.code
      }
      throw new ApiRequestError(`HTTP ${response.status}`, code, responseTrace, response.status)
    }
    return body
  } catch (error: unknown) {
    if (error instanceof ApiRequestError) throw error
    const timeout =
      (error instanceof Error && error.name === 'TimeoutError') ||
      (signal.aborted && signal.reason instanceof Error && signal.reason.name === 'TimeoutError')
    const invalidJson = error instanceof SyntaxError
    throw new ApiRequestError(
      timeout ? '请求超时' : invalidJson ? '响应不是有效 JSON' : '网络连接失败',
      timeout ? 'REQUEST_TIMEOUT' : invalidJson ? 'INVALID_RESPONSE' : 'NETWORK_ERROR',
      responseTrace,
      status,
    )
  }
}
