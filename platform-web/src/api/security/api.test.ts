import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { getIpBlocks, unblockIp } from './api'
import { clearIdentity, sessionState } from '../../session/state'

const blocked = {
  id: '9007199254740993',
  sourceIp: '2001:db8::2',
  status: 'BLOCKED',
  reasonCode: 'LOGIN_FAILURE_THRESHOLD',
  failedAttempts: 20,
  windowSeconds: 600,
  blockedAt: '2026-09-26T00:00:00Z',
  unblockedAt: null,
  unblockedBy: null,
  unblockedByReference: null,
  version: '9007199254740994',
}

function response(
  data: unknown,
  status = 200,
  code = 'SUCCESS',
  headers: Record<string, string> = {},
) {
  return new Response(
    JSON.stringify({
      code,
      msg: '请求结果',
      data,
      traceId: 'a'.repeat(32),
      timestamp: '2026-09-26T00:00:00Z',
    }),
    {
      status,
      headers: { 'Content-Type': 'application/json', 'X-Trace-Id': 'a'.repeat(32), ...headers },
    },
  )
}

beforeEach(() => {
  vi.stubEnv('DEV', true)
  clearIdentity()
  sessionState.csrf = { headerName: 'X-CSRF-TOKEN', token: 'test-csrf' }
})

afterEach(() => {
  clearIdentity()
  vi.unstubAllEnvs()
  vi.unstubAllGlobals()
})

describe('IP block API requests through the public HTTP client', () => {
  it('sends an exact IP and status filter as encoded query parameters and decodes a page', async () => {
    const data = { items: [blocked], page: 2, size: 20, total: 21 }
    const fetchMock = vi.fn().mockResolvedValue(response(data))
    vi.stubGlobal('fetch', fetchMock)
    await expect(
      getIpBlocks({ page: 2, size: 20, sourceIp: '2001:db8::2', status: 'BLOCKED' }),
    ).resolves.toEqual(data)
    const [path, init] = fetchMock.mock.calls[0]!
    const url = new URL(path, 'http://localhost')
    expect(url.pathname).toBe('/api/audit/ip-blocks/page')
    expect(Object.fromEntries(url.searchParams)).toEqual({
      page: '2',
      size: '20',
      sourceIp: '2001:db8::2',
      status: 'BLOCKED',
    })
    expect(init).toMatchObject({ method: 'GET', body: undefined, credentials: 'same-origin' })
  })

  it('omits cleared optional filters and rejects an invalid response page', async () => {
    const fetchMock = vi
      .fn()
      .mockResolvedValue(response({ items: [blocked], page: 1, size: 20, total: -1 }))
    vi.stubGlobal('fetch', fetchMock)
    await expect(getIpBlocks({ page: 1, size: 20 })).rejects.toMatchObject({
      code: 'INVALID_RESPONSE',
    })
    expect(fetchMock.mock.calls[0]![0]).toBe('/api/audit/ip-blocks/page?page=1&size=20')
  })

  it('releases by string ID with If-Match and CSRF, no body, and the new response version', async () => {
    const released = {
      ...blocked,
      status: 'RELEASED',
      unblockedAt: '2026-09-26T00:15:00Z',
      unblockedBy: '1001',
      version: '9007199254740995',
    }
    const fetchMock = vi
      .fn()
      .mockResolvedValue(response(released, 200, 'SUCCESS', { ETag: '"9007199254740995"' }))
    vi.stubGlobal('fetch', fetchMock)
    await expect(unblockIp(blocked)).resolves.toEqual(released)
    const [path, init] = fetchMock.mock.calls[0]!
    expect(path).toBe('/api/audit/ip-blocks/9007199254740993/unblock')
    expect(init).toMatchObject({ method: 'PUT', body: undefined })
    expect(init.headers.get('If-Match')).toBe('"9007199254740994"')
    expect(init.headers.get('X-CSRF-TOKEN')).toBe('test-csrf')
    expect(init.headers.has('Content-Type')).toBe(false)
  })

  it.each([
    { id: '../page', version: '1' },
    { id: '10', version: '1"\r\nBad: true' },
  ])('rejects malformed mutation IDs and versions before sending: %j', async (input) => {
    const fetchMock = vi.fn()
    vi.stubGlobal('fetch', fetchMock)
    await expect(unblockIp(input)).rejects.toThrow()
    expect(fetchMock).not.toHaveBeenCalled()
  })

  it.each([
    [412, 'VERSION_CONFLICT'],
    [403, 'IP_BLOCKED'],
    [429, 'RATE_LIMITED'],
  ])('preserves %s %s failures without retrying the mutation', async (status, code) => {
    const fetchMock = vi
      .fn()
      .mockResolvedValue(response(null, status, code, { 'Retry-After': '10' }))
    vi.stubGlobal('fetch', fetchMock)
    await expect(unblockIp(blocked)).rejects.toMatchObject({
      status,
      code,
      traceId: 'a'.repeat(32),
    })
    expect(fetchMock).toHaveBeenCalledTimes(1)
  })
})
