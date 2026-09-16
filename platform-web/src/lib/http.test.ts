import { afterEach, describe, expect, it, vi } from 'vitest'
import { getJson } from './http'

afterEach(() => {
  vi.unstubAllGlobals()
  vi.restoreAllMocks()
})

describe('HTTP request foundation', () => {
  it('recognizes a timeout signal when body reading rejects with AbortError', async () => {
    const signal = AbortSignal.abort(new DOMException('timeout', 'TimeoutError'))
    vi.spyOn(AbortSignal, 'timeout').mockReturnValue(signal)
    const response = new Response('{}')
    vi.spyOn(response, 'json').mockRejectedValue(new DOMException('aborted', 'AbortError'))
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(response))
    await expect(getJson('/health')).rejects.toMatchObject({ code: 'REQUEST_TIMEOUT' })
  })

  it.each([
    [new DOMException('timeout', 'TimeoutError'), 'REQUEST_TIMEOUT'],
    [new TypeError('stream interrupted'), 'NETWORK_ERROR'],
    [new SyntaxError('invalid json'), 'INVALID_RESPONSE'],
  ])('classifies response body failures: %s', async (error, code) => {
    const response = new Response('{}', { headers: { 'X-Trace-Id': 'b'.repeat(32) } })
    vi.spyOn(response, 'json').mockRejectedValue(error)
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(response))
    await expect(getJson('/health')).rejects.toMatchObject({
      code,
      traceId: 'b'.repeat(32),
      status: 200,
    })
  })

  it('passes successful responses through and sends a trace ID', async () => {
    const fetchMock = vi.fn().mockResolvedValue(new Response('{"status":"UP"}'))
    vi.stubGlobal('fetch', fetchMock)
    expect(await getJson('/actuator/health')).toEqual({ status: 'UP' })
    expect(fetchMock.mock.calls[0]?.[1].headers['X-Trace-Id']).toMatch(/^[a-f0-9]{32}$/)
  })

  it('preserves error status, code and server trace ID', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn().mockResolvedValue(
        new Response('{"code":"NOT_FOUND"}', {
          status: 404,
          headers: { 'X-Trace-Id': 'a'.repeat(32) },
        }),
      ),
    )
    await expect(getJson('/missing')).rejects.toMatchObject({
      status: 404,
      code: 'NOT_FOUND',
      traceId: 'a'.repeat(32),
    })
  })

  it('handles non-JSON proxy errors without retrying', async () => {
    const fetchMock = vi.fn().mockResolvedValue(new Response('Bad gateway', { status: 502 }))
    vi.stubGlobal('fetch', fetchMock)
    await expect(getJson('/health')).rejects.toMatchObject({ status: 502, code: 'HTTP_502' })
    expect(fetchMock).toHaveBeenCalledTimes(1)
  })

  it('distinguishes timeout from network failure', async () => {
    vi.stubGlobal('fetch', vi.fn().mockRejectedValue(new DOMException('timeout', 'TimeoutError')))
    await expect(getJson('/health')).rejects.toMatchObject({ code: 'REQUEST_TIMEOUT' })
    vi.stubGlobal('fetch', vi.fn().mockRejectedValue(new TypeError('Failed to fetch')))
    await expect(getJson('/health')).rejects.toMatchObject({ code: 'NETWORK_ERROR' })
  })
})
