import { afterEach, describe, expect, it, vi } from 'vitest'
import { createApiClient } from './client'
import type { ClientHooks } from './client'
import type { RequestOptions } from './types'

afterEach(() => {
  vi.unstubAllGlobals()
  vi.restoreAllMocks()
})

function response(data: unknown = null, status = 200, code = 'SUCCESS') {
  return new Response(
    JSON.stringify({
      code,
      msg: '提示',
      data,
      traceId: 'a'.repeat(32),
      timestamp: '2026-09-16T07:00:00Z',
    }),
    { status, headers: { 'Content-Type': 'application/json', 'X-Trace-Id': 'a'.repeat(32) } },
  )
}
function hooks(): ClientHooks {
  return {
    getCsrf: () => ({ headerName: 'X-CSRF-TOKEN', token: 'test' }),
    getIdentityEpoch: () => 1,
    onAuthFailure: vi.fn(),
  }
}
const options = (): RequestOptions<unknown> => ({
  path: '/user/page',
  method: 'GET',
  successStatus: 200,
  decode: (value) => value,
})

describe('business HTTP client', () => {
  it('requires explicit session hooks instead of permissive defaults', () => {
    expect(() => createApiClient({} as ClientHooks)).toThrow(
      expect.objectContaining({ code: 'INVALID_REQUEST' }),
    )
  })

  it.each([
    '/user',
    '/user/page',
    '/user/9007199254740993',
    '/auth',
    '/auth/me',
    '/roles',
    '/roles/page',
    '/roles/9007199254740993/menus',
    '/menus',
    '/menus/9007199254740993',
    '/departments',
    '/departments/9007199254740993',
  ])('accepts the declared business module path: %s', async (path) => {
    const fetchMock = vi.fn().mockResolvedValue(response())
    vi.stubGlobal('fetch', fetchMock)
    await expect(createApiClient(hooks())({ ...options(), path })).resolves.toMatchObject({
      data: null,
    })
    expect(fetchMock.mock.calls[0]?.[0]).toBe(path)
  })

  it.each([
    '/userland',
    '/users',
    '/authentication',
    '/authentic',
    '/role',
    '/roles-admin',
    '/menu',
    '/menus-extra',
    '/department',
    '/departments-private',
    '/api',
    '/private',
  ])('rejects paths outside the declared modules: %s', async (path) => {
    const fetchMock = vi.fn()
    vi.stubGlobal('fetch', fetchMock)
    await expect(createApiClient(hooks())({ ...options(), path })).rejects.toMatchObject({
      code: 'INVALID_REQUEST',
    })
    expect(fetchMock).not.toHaveBeenCalled()
  })

  it('notifies again if a refreshed CSRF token is rejected within the same identity', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn().mockImplementation(() => Promise.resolve(response(null, 403, 'CSRF_INVALID'))),
    )
    const state = hooks()
    const request = createApiClient(state)
    await expect(request({ ...options(), method: 'POST' })).rejects.toMatchObject({
      code: 'CSRF_INVALID',
    })
    await expect(request({ ...options(), method: 'POST' })).rejects.toMatchObject({
      code: 'CSRF_INVALID',
    })
    expect(state.onAuthFailure).toHaveBeenCalledTimes(2)
  })

  it('decodes only the data once and returns typed data to the API function', async () => {
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(response({ id: '12' })))
    const decode = vi.fn((value) => String((value as { id: string }).id))
    const result = await createApiClient(hooks())({ ...options(), decode })
    expect(decode).toHaveBeenCalledExactlyOnceWith({ id: '12' })
    expect(result.data).toBe('12')
  })
  it('does not treat a successful HTTP response with an invalid DTO as a network failure', async () => {
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(response({ id: 12 })))
    await expect(
      createApiClient(hooks())({
        ...options(),
        decode: () => {
          throw new Error('private DTO')
        },
      }),
    ).rejects.toMatchObject({ code: 'INVALID_RESPONSE', status: 200, traceId: 'a'.repeat(32) })
  })
  it('rejects unexpected success status before decoding', async () => {
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(response(null, 201)))
    const decode = vi.fn()
    await expect(createApiClient(hooks())({ ...options(), decode })).rejects.toMatchObject({
      code: 'INVALID_RESPONSE',
      status: 201,
    })
    expect(decode).not.toHaveBeenCalled()
  })
  it('refuses writes without CSRF and never sends an implicit token request', async () => {
    const fetchMock = vi.fn()
    vi.stubGlobal('fetch', fetchMock)
    await expect(
      createApiClient({ ...hooks(), getCsrf: () => null })({ ...options(), method: 'POST' }),
    ).rejects.toMatchObject({ code: 'CSRF_UNAVAILABLE', status: undefined })
    expect(fetchMock).not.toHaveBeenCalled()
  })
  it('coalesces concurrent 401 notifications for one identity', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn().mockImplementation(() => Promise.resolve(response(null, 401, 'UNAUTHORIZED'))),
    )
    const state = hooks()
    const request = createApiClient(state)
    const results = await Promise.allSettled([request(options()), request(options())])
    expect(results.every((result) => result.status === 'rejected')).toBe(true)
    expect(state.onAuthFailure).toHaveBeenCalledTimes(1)
  })
  it.each([200, 401])(
    'discards a late %s from an older identity without clearing a new login',
    async (status) => {
      let complete!: (value: Response) => void
      vi.stubGlobal(
        'fetch',
        vi.fn().mockImplementation(
          () =>
            new Promise<Response>((resolve) => {
              complete = resolve
            }),
        ),
      )
      let epoch = 1
      const state = { ...hooks(), getIdentityEpoch: () => epoch }
      const pending = createApiClient(state)(options())
      epoch = 2
      complete(response(null, status, status === 401 ? 'UNAUTHORIZED' : 'SUCCESS'))
      await expect(pending).rejects.toMatchObject({ code: 'REQUEST_CANCELLED' })
      expect(state.onAuthFailure).not.toHaveBeenCalled()
    },
  )
  it('keeps login failure local, and notifies for a malformed 401', async () => {
    const fetchMock = vi
      .fn()
      .mockResolvedValueOnce(response(null, 401, 'LOGIN_FAILED'))
      .mockResolvedValueOnce(new Response('invalid', { status: 401 }))
    vi.stubGlobal('fetch', fetchMock)
    const state = hooks()
    const request = createApiClient(state)
    await expect(request(options())).rejects.toMatchObject({ code: 'LOGIN_FAILED' })
    expect(state.onAuthFailure).not.toHaveBeenCalled()
    await expect(request(options())).rejects.toMatchObject({ code: 'HTTP_401' })
    expect(state.onAuthFailure).toHaveBeenCalledTimes(1)
  })
  it('preserves the original error even if the auth callback fails', async () => {
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(response(null, 401, 'UNAUTHORIZED')))
    const log = vi.spyOn(console, 'error').mockImplementation(() => undefined)
    await expect(
      createApiClient({
        ...hooks(),
        onAuthFailure: () => {
          throw new Error('private')
        },
      })(options()),
    ).rejects.toMatchObject({ code: 'UNAUTHORIZED' })
    expect(log).toHaveBeenCalledExactlyOnceWith('Authentication failure callback failed')
  })
})
