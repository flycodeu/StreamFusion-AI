import { afterEach, describe, expect, it, vi } from 'vitest'
import { getHealth } from './api'

afterEach(() => {
  vi.unstubAllGlobals()
  vi.restoreAllMocks()
})

describe('existing raw health consumer', () => {
  it('accepts Actuator JSON without requiring an R envelope', async () => {
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(new Response('{"status":"UP"}')))
    expect(await getHealth()).toEqual({ status: 'UP' })
  })
  it('rejects malformed success instead of inventing UP', async () => {
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(new Response('{}')))
    await expect(getHealth()).rejects.toMatchObject({ code: 'INVALID_RESPONSE', status: 200 })
  })
  it('preserves existing error code, status, trace and safe proxy fallback', async () => {
    vi.stubGlobal(
      'fetch',
      vi
        .fn()
        .mockResolvedValueOnce(
          new Response('{"code":"NOT_FOUND"}', {
            status: 404,
            headers: { 'X-Trace-Id': 'b'.repeat(32) },
          }),
        )
        .mockResolvedValueOnce(new Response('Bad gateway', { status: 502 })),
    )
    await expect(getHealth()).rejects.toMatchObject({
      code: 'NOT_FOUND',
      status: 404,
      traceId: 'b'.repeat(32),
    })
    await expect(getHealth()).rejects.toMatchObject({ code: 'HTTP_502', status: 502 })
  })
})
