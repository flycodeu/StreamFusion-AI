import { afterEach, describe, expect, it, vi } from 'vitest'
import { createApiClient } from './client'
import { object, string } from '../../api/parse'
afterEach(() => {
  vi.unstubAllGlobals()
  vi.unstubAllEnvs()
})
const client = () =>
  createApiClient({
    getCsrf: () => null,
    getIdentityEpoch: () => 1,
    onAuthFailure: vi.fn(),
    onForbidden: vi.fn(),
  })
const input = {
  path: '/audit/exports/123/download',
  method: 'GET' as const,
  responseType: 'file' as const,
  successStatus: 200 as const,
  decode: (value: unknown) => {
    const r = object(value)
    return { blob: r.blob, name: string(r.name) }
  },
}
describe('authenticated file responses', () => {
  it('accepts the expected attachment without a JSON parse', async () => {
    vi.stubEnv('DEV', true)
    const fetcher = vi.fn().mockResolvedValue(
      new Response('列名\r\n记录', {
        status: 200,
        headers: {
          'Content-Type': 'text/csv;charset=UTF-8',
          'Content-Disposition': 'attachment; filename="audit-123.csv"',
          'X-Trace-Id': 'a'.repeat(32),
        },
      }),
    )
    vi.stubGlobal('fetch', fetcher)
    const result = await client()(input)
    expect(result.data.name).toBe('audit-123.csv')
    expect(result.data.blob).toBeInstanceOf(Blob)
    expect(fetcher.mock.calls[0]?.[1].credentials).toBe('same-origin')
  })
  it('retains normal authorization handling for JSON download errors', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn().mockResolvedValue(
        new Response(
          JSON.stringify({
            code: 'FORBIDDEN',
            msg: '无权限',
            data: null,
            traceId: 'a'.repeat(32),
            timestamp: '2026-10-15T00:00:00Z',
          }),
          {
            status: 403,
            headers: { 'Content-Type': 'application/json', 'X-Trace-Id': 'a'.repeat(32) },
          },
        ),
      ),
    )
    await expect(client()(input)).rejects.toMatchObject({ code: 'FORBIDDEN', status: 403 })
  })
  it('rejects unexpected filenames and content types', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn().mockResolvedValue(
        new Response('<script>', {
          status: 200,
          headers: {
            'Content-Type': 'text/html',
            'Content-Disposition': 'attachment; filename="../../unsafe.html"',
          },
        }),
      ),
    )
    await expect(client()(input)).rejects.toMatchObject({ code: 'INVALID_RESPONSE' })
  })
})
