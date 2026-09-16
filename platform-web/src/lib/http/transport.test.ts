import { afterEach, describe, expect, it, vi } from 'vitest'
import { send } from './transport'

afterEach(() => {
  vi.unstubAllGlobals()
  vi.restoreAllMocks()
  vi.useRealTimers()
})

describe('HTTP transport', () => {
  it('encodes query parameters, preserves false/zero, and uses same-origin credentials', async () => {
    const fetchMock = vi.fn().mockResolvedValue(new Response('{}'))
    vi.stubGlobal('fetch', fetchMock)
    await send({
      path: '/api/v1/users',
      method: 'GET',
      params: { enabled: false, page: 0, q: 'a&b', skip: null },
    })
    expect(fetchMock.mock.calls[0]?.[0]).toBe('/api/v1/users?enabled=false&page=0&q=a%26b')
    const options = fetchMock.mock.calls[0]?.[1]
    expect(options.credentials).toBe('same-origin')
    expect(options.redirect).toBe('error')
    expect(options.headers.get('X-Trace-Id')).toMatch(/^[a-f0-9]{32}$/)
    expect(options.headers.has('Content-Type')).toBe(false)
  })

  it('preserves JSON null and empty arrays, version precision, CSRF and If-Match', async () => {
    const fetchMock = vi.fn().mockResolvedValue(new Response('{}'))
    vi.stubGlobal('fetch', fetchMock)
    await send({
      path: '/api/v1/users/9007199254740993',
      method: 'PUT',
      body: { deptId: null, roleIds: [], version: '9007199254740993' },
      ifMatch: '"7"',
      csrf: { headerName: 'X-CSRF-TOKEN', token: 'csrf' },
    })
    const options = fetchMock.mock.calls[0]?.[1]
    expect(JSON.parse(options.body)).toEqual({
      deptId: null,
      roleIds: [],
      version: '9007199254740993',
    })
    expect(options.headers.get('If-Match')).toBe('"7"')
    expect(options.headers.get('X-CSRF-TOKEN')).toBe('csrf')
  })

  it.each([
    'https://example.invalid/api/v1/users',
    '//example.invalid/api/v1/users',
    '/api/v1/../private',
    '/api/v1/%2e%2e/private',
    '/api/v1/%252e%252e/private',
    '/api/v1/users?q=secret',
    '/private',
    '/api/v1/users#fragment',
  ])('rejects unsafe path before sending: %s', async (path) => {
    const fetchMock = vi.fn()
    vi.stubGlobal('fetch', fetchMock)
    await expect(send({ path, method: 'GET' })).rejects.toMatchObject({ code: 'INVALID_REQUEST' })
    expect(fetchMock).not.toHaveBeenCalled()
  })

  it('rejects unserializable inputs without leaking them or sending', async () => {
    const fetchMock = vi.fn()
    vi.stubGlobal('fetch', fetchMock)
    const circular: Record<string, unknown> = { password: 'secret' }
    circular.self = circular
    for (const body of [circular, { id: 1n }, { n: Infinity }, { callback: () => 1 }]) {
      await expect(send({ path: '/api/v1/users', method: 'POST', body })).rejects.toMatchObject({
        code: 'INVALID_REQUEST',
        message: '请求参数不合法',
      })
    }
    await expect(
      send({
        path: '/api/v1/users',
        method: 'POST',
        csrf: { headerName: 'Authorization', token: 'secret' },
      }),
    ).rejects.toMatchObject({ code: 'INVALID_REQUEST' })
    expect(fetchMock).not.toHaveBeenCalled()
  })

  it('does not send an already cancelled request', async () => {
    const fetchMock = vi.fn()
    vi.stubGlobal('fetch', fetchMock)
    await expect(
      send({ path: '/api/v1/users', method: 'GET', signal: AbortSignal.abort() }),
    ).rejects.toMatchObject({ code: 'REQUEST_CANCELLED' })
    expect(fetchMock).not.toHaveBeenCalled()
  })

  it.each(['connection', 'body'])(
    'deadline includes %s and never retries writes',
    async (stage) => {
      vi.useFakeTimers()
      const response = new Response('{}', { headers: { 'X-Trace-Id': 'b'.repeat(32) } })
      const fetchMock = vi.fn((_path, options) => {
        const pending = () =>
          new Promise<string>((_resolve, reject) =>
            options.signal.addEventListener('abort', () =>
              reject(new DOMException('abort', 'AbortError')),
            ),
          )
        if (stage === 'connection') return pending()
        vi.spyOn(response, 'text').mockImplementation(pending)
        return Promise.resolve(response)
      })
      vi.stubGlobal('fetch', fetchMock)
      const pending = send({ path: '/api/v1/users', method: 'POST', body: {}, timeoutMs: 50 })
      const assertion = expect(pending).rejects.toMatchObject({
        code: 'REQUEST_TIMEOUT',
        status: stage === 'body' ? 200 : undefined,
      })
      await vi.advanceTimersByTimeAsync(50)
      await assertion
      expect(fetchMock).toHaveBeenCalledTimes(1)
      expect(vi.getTimerCount()).toBe(0)
    },
  )

  it('classifies stream failure and cancellation separately while preserving received headers', async () => {
    const response = new Response('{}', { status: 503, headers: { 'X-Trace-Id': 'a'.repeat(32) } })
    vi.spyOn(response, 'text').mockRejectedValue(new TypeError('broken stream'))
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(response))
    await expect(send({ path: '/api/v1/users', method: 'GET' })).rejects.toMatchObject({
      code: 'NETWORK_ERROR',
      status: 503,
      traceId: 'a'.repeat(32),
    })
  })
})
