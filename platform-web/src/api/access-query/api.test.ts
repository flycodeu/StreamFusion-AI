import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import {
  getPageExcel,
  getPageOverview,
  getPageRoles,
  getPageUsers,
  getPages,
  getRoleOverview,
  getRolePages,
  getRoleUsers,
  getUserPages,
  getUsers,
} from './api'
import { clearIdentity } from '../../session/state'

const empty = {
  items: [],
  total: 0,
  page: 2,
  size: 20,
  checkedAt: '2026-10-18T08:00:00Z',
  subjectUser: null,
  subjectPage: null,
  subjectAccess: null,
}
function response(value: unknown) {
  return new Response(
    JSON.stringify({
      code: 'SUCCESS',
      msg: '查询完成',
      data: value,
      traceId: 'a'.repeat(32),
      timestamp: '2026-10-18T08:00:00Z',
    }),
    {
      status: 200,
      headers: { 'Content-Type': 'application/json', 'X-Trace-Id': 'a'.repeat(32) },
    },
  )
}
beforeEach(() => {
  vi.stubEnv('DEV', true)
  clearIdentity()
})
afterEach(() => {
  clearIdentity()
  vi.unstubAllGlobals()
  vi.unstubAllEnvs()
})

describe('permission query uses only dedicated read endpoints', () => {
  it('downloads all pages matching the keyword through the authenticated file route', async () => {
    const fetchMock = vi.fn().mockResolvedValue(
      new Response('PK', {
        status: 200,
        headers: {
          'Content-Type': 'application/vnd.openxmlformats-officedocument.spreadsheetml.sheet',
          'Content-Disposition': 'attachment; filename="access-pages-20261018-204500.xlsx"',
          'X-Trace-Id': 'a'.repeat(32),
        },
      }),
    )
    vi.stubGlobal('fetch', fetchMock)
    const file = await getPageExcel(' 相机 ')
    expect(file.name).toBe('access-pages-20261018-204500.xlsx')
    const [url, init] = fetchMock.mock.calls[0]!
    expect(new URL(url, 'http://localhost').pathname).toBe('/api/access-query/pages/export.xlsx')
    expect(new URL(url, 'http://localhost').searchParams.get('keyword')).toBe('相机')
    expect(new URL(url, 'http://localhost').searchParams.has('page')).toBe(false)
    expect(init.method).toBe('GET')
  })
  it('sends dedicated GET requests with bounded filters and no mutation credentials', async () => {
    const fetchMock = vi.fn().mockImplementation(async () => response(empty))
    vi.stubGlobal('fetch', fetchMock)
    const query = { page: 2, size: 20, keyword: '炉 前_%', includeInactive: true }
    await getUsers(query)
    await getPages(query)
    await getRoleOverview(query)
    await getRolePages('8', query)
    await getRoleUsers('8', query)
    await getPageOverview(query)
    await getUserPages('9007199254740993', { ...query, moduleKey: 'camera' })
    await getPageUsers('1101', { ...query, userId: '9007199254740993' })
    await getPageRoles('1101', { ...query, userId: '9007199254740993' })
    const paths = fetchMock.mock.calls.map((call) => new URL(call[0], 'http://localhost').pathname)
    expect(paths).toEqual([
      '/api/access-query/users/page',
      '/api/access-query/pages/page',
      '/api/access-query/roles/page',
      '/api/access-query/roles/8/pages',
      '/api/access-query/roles/8/users',
      '/api/access-query/pages/overview',
      '/api/access-query/users/9007199254740993/pages',
      '/api/access-query/pages/1101/users',
      '/api/access-query/pages/1101/roles',
    ])
    for (const [url, init] of fetchMock.mock.calls) {
      expect(init.method).toBe('GET')
      expect(init.body).toBeUndefined()
      expect(init.headers.has('X-CSRF-TOKEN')).toBe(false)
      expect(new URL(url, 'http://localhost').searchParams.get('keyword')).toBe('炉 前_%')
    }
    expect(
      new URL(fetchMock.mock.calls[7]![0], 'http://localhost').searchParams.get('userId'),
    ).toBe('9007199254740993')
  })
  it('does not replace missing grant fields with permissive defaults', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn().mockResolvedValue(response({ ...empty, items: [{ user: {} }], total: 1 })),
    )
    await expect(getPageUsers('1101', {})).rejects.toMatchObject({ code: 'INVALID_RESPONSE' })
  })
  it('forwards cancellation and never replays a cancelled query', async () => {
    const fetchMock = vi.fn()
    vi.stubGlobal('fetch', fetchMock)
    const cancelled = new AbortController()
    cancelled.abort()
    await expect(getPages({}, cancelled.signal)).rejects.toMatchObject({
      code: 'REQUEST_CANCELLED',
    })
    expect(fetchMock).not.toHaveBeenCalled()
  })
  it('rejects malformed path identities before sending', () => {
    const fetchMock = vi.fn()
    vi.stubGlobal('fetch', fetchMock)
    expect(() => getUserPages('../users', {})).toThrow()
    expect(() => getPageUsers('1101/roles', {})).toThrow()
    expect(() => getPageRoles('1?extra=true', {})).toThrow()
    expect(fetchMock).not.toHaveBeenCalled()
  })
})
