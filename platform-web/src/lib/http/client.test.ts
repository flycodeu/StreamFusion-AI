import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createApiClient } from './client'
import type { ClientHooks } from './client'
import type { RequestOptions } from './types'

beforeEach(() => vi.stubEnv('DEV', true))

afterEach(() => {
  vi.unstubAllEnvs()
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
    onForbidden: vi.fn().mockResolvedValue(undefined),
  }
}
const options = (): RequestOptions<unknown> => ({
  path: '/user/page',
  method: 'GET',
  successStatus: 200,
  decode: (value) => value,
})

describe('business HTTP client', () => {
  it('routes IP blocks to session handling without treating them as missing page authorization', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn().mockImplementation(() => Promise.resolve(response(null, 403, 'IP_BLOCKED'))),
    )
    const state = hooks()
    const request = createApiClient(state)
    const results = await Promise.allSettled([request(options()), request(options())])
    expect(results.every((result) => result.status === 'rejected')).toBe(true)
    expect(state.onAuthFailure).toHaveBeenCalledExactlyOnceWith(
      expect.objectContaining({ code: 'IP_BLOCKED' }),
      1,
    )
    expect(state.onForbidden).not.toHaveBeenCalled()
  })
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
    '/camera',
    '/camera/devices/9007199254740993',
    '/audit-log/events',
  ])('accepts business API paths without a module registry: %s', async (path) => {
    const fetchMock = vi.fn().mockResolvedValue(response())
    vi.stubGlobal('fetch', fetchMock)
    await expect(createApiClient(hooks())({ ...options(), path })).resolves.toMatchObject({
      data: null,
    })
    expect(fetchMock.mock.calls[0]?.[0]).toBe(`/api${path}`)
  })

  it.each([
    'https://example.invalid/camera',
    '//example.invalid/camera',
    '/camera/../private',
    '/camera/%2e%2e/private',
    '/camera/%252fprivate',
    '/camera/%5cprivate',
    '/camera?query=value',
    '/camera#fragment',
    '/camera/manage.vue',
    '/index.html',
    '/assets/main',
    '/src/main',
    '/public/config',
    '/node_modules/vue',
    '/@vite/client',
    '/api',
    '/api/camera',
    '/actuator/health',
  ])('rejects unsafe or reserved resource paths: %s', async (path) => {
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

  it('synchronizes a business FORBIDDEN once without retrying the failed request', async () => {
    const fetchMock = vi.fn().mockResolvedValue(response(null, 403, 'FORBIDDEN'))
    vi.stubGlobal('fetch', fetchMock)
    const state = hooks()
    await expect(createApiClient(state)(options())).rejects.toMatchObject({
      code: 'FORBIDDEN',
      status: 403,
      traceId: 'a'.repeat(32),
    })
    expect(state.onForbidden).toHaveBeenCalledExactlyOnceWith(
      expect.objectContaining({ code: 'FORBIDDEN' }),
      1,
    )
    expect(fetchMock).toHaveBeenCalledTimes(1)
    expect(state.onAuthFailure).not.toHaveBeenCalled()
  })

  it.each([
    ['/auth/me', 403, 'FORBIDDEN'],
    ['/auth/password', 403, 'FORBIDDEN'],
    ['/auth', 403, 'FORBIDDEN'],
    ['/user/page', 403, 'CSRF_INVALID'],
    ['/user/page', 403, 'PASSWORD_CHANGE_REQUIRED'],
    ['/user/page', 403, 'OBJECT_PROTECTED'],
    ['/user/page', 500, 'FORBIDDEN'],
  ])('does not synchronize authorization for %s / %s / %s', async (path, status, code) => {
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(response(null, status, code)))
    const state = hooks()
    await expect(createApiClient(state)({ ...options(), path })).rejects.toMatchObject({ code })
    expect(state.onForbidden).not.toHaveBeenCalled()
  })

  it('coalesces concurrent refusals and late responses until a new request batch begins', async () => {
    const pending: ((value: Response) => void)[] = []
    vi.stubGlobal(
      'fetch',
      vi.fn(() => new Promise<Response>((resolve) => pending.push(resolve))),
    )
    let finish!: () => void
    const state = hooks()
    state.onForbidden = vi.fn(() => new Promise<void>((resolve) => (finish = resolve)))
    const request = createApiClient(state)
    const first = request(options()).catch((error: unknown) => error)
    const second = request(options()).catch((error: unknown) => error)
    const late = request(options()).catch((error: unknown) => error)
    pending[0]!(response(null, 403, 'FORBIDDEN'))
    pending[1]!(response(null, 403, 'FORBIDDEN'))
    await vi.waitFor(() => expect(state.onForbidden).toHaveBeenCalledTimes(1))
    const during = request(options()).catch((error: unknown) => error)
    finish()
    await Promise.all([first, second])
    pending[2]!(response(null, 403, 'FORBIDDEN'))
    pending[3]!(response(null, 403, 'FORBIDDEN'))
    await Promise.all([late, during])
    expect(state.onForbidden).toHaveBeenCalledTimes(1)

    const next = request(options()).catch((error: unknown) => error)
    pending[4]!(response(null, 403, 'FORBIDDEN'))
    await vi.waitFor(() => expect(state.onForbidden).toHaveBeenCalledTimes(2))
    finish()
    expect(await next).toMatchObject({ code: 'FORBIDDEN' })
  })

  it('preserves FORBIDDEN when synchronization fails and can synchronize the next request', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn(() => Promise.resolve(response(null, 403, 'FORBIDDEN'))),
    )
    const state = hooks()
    state.onForbidden = vi.fn().mockRejectedValue(new Error('dependency unavailable'))
    const request = createApiClient(state)
    await expect(request(options())).rejects.toMatchObject({ code: 'FORBIDDEN' })
    await expect(request(options())).rejects.toMatchObject({ code: 'FORBIDDEN' })
    expect(state.onForbidden).toHaveBeenCalledTimes(2)
  })

  it('does not recurse if the authorization identity endpoint itself returns FORBIDDEN', async () => {
    const fetchMock = vi.fn(() => Promise.resolve(response(null, 403, 'FORBIDDEN')))
    vi.stubGlobal('fetch', fetchMock)
    const state = hooks()
    const request = createApiClient(state)
    state.onForbidden = vi.fn(async () => {
      await request({ ...options(), path: '/auth/me' })
    })
    await expect(request(options())).rejects.toMatchObject({ code: 'FORBIDDEN' })
    expect(state.onForbidden).toHaveBeenCalledTimes(1)
    expect(fetchMock).toHaveBeenCalledTimes(2)
  })

  it('cancels an old refusal without suppressing synchronization for a new identity', async () => {
    let epoch = 1
    const completions: (() => void)[] = []
    vi.stubGlobal(
      'fetch',
      vi.fn(() => Promise.resolve(response(null, 403, 'FORBIDDEN'))),
    )
    const state = {
      ...hooks(),
      getIdentityEpoch: () => epoch,
      onForbidden: vi.fn(() => new Promise<void>((resolve) => completions.push(resolve))),
    }
    const request = createApiClient(state)
    const previous = request(options()).catch((error: unknown) => error)
    await vi.waitFor(() => expect(state.onForbidden).toHaveBeenCalledTimes(1))
    epoch++
    const next = request(options()).catch((error: unknown) => error)
    await vi.waitFor(() => expect(state.onForbidden).toHaveBeenCalledTimes(2))
    completions[0]!()
    expect(await previous).toMatchObject({ code: 'REQUEST_CANCELLED' })
    completions[1]!()
    expect(await next).toMatchObject({ code: 'FORBIDDEN' })
  })
})
