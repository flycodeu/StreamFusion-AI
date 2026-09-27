import { afterEach, beforeEach, expect, it, vi } from 'vitest'
import { request } from './client'
import { clearIdentity, sessionState } from '../session/state'
import { endedNotice } from '../session/endedNotice'

const replace = vi.hoisted(() => vi.fn().mockResolvedValue(undefined))
vi.mock('../router', () => ({ router: { replace } }))
beforeEach(() => {
  clearIdentity()
  endedNotice.value = null
  replace.mockClear()
  vi.stubGlobal('location', { pathname: '/home', assign: vi.fn() })
  vi.stubGlobal('sessionStorage', {
    setItem: () => {
      throw new Error('blocked')
    },
    removeItem: vi.fn(),
  })
})
afterEach(() => vi.unstubAllGlobals())
const options = {
  path: '/auth/session',
  method: 'GET' as const,
  successStatus: 200 as const,
  decode: () => undefined,
}
function response(
  code = 'SESSION_REPLACED',
  status = 401,
  data: unknown = { reason: 'REPLACED', sourceIp: '127.0.0.1' },
) {
  return new Response(
    JSON.stringify({
      code,
      msg: '提示',
      data,
      traceId: 'a'.repeat(32),
      timestamp: '2026-09-26T12:00:00Z',
    }),
    { status, headers: { 'Content-Type': 'application/json', 'X-Trace-Id': 'a'.repeat(32) } },
  )
}
it('preserves the kick-out dialog with blocked storage and navigates without reloading', async () => {
  vi.stubGlobal('fetch', vi.fn().mockResolvedValue(response()))
  await expect(request(options)).rejects.toMatchObject({ code: 'SESSION_REPLACED' })
  await vi.waitFor(() => expect(replace).toHaveBeenCalledExactlyOnceWith('/login'))
  expect(endedNotice.value).toMatchObject({ reason: 'REPLACED', sourceIp: '127.0.0.1' })
  expect(location.assign).not.toHaveBeenCalled()
})
it('ignores a late old-session error after the identity epoch changes', async () => {
  let resolve!: (response: Response) => void
  vi.stubGlobal(
    'fetch',
    vi.fn().mockReturnValue(
      new Promise<Response>((done) => {
        resolve = done
      }),
    ),
  )
  const pending = request(options)
  sessionState.epoch++
  resolve(response())
  await expect(pending).rejects.toMatchObject({ code: 'REQUEST_CANCELLED' })
  expect(endedNotice.value).toBeNull()
  expect(replace).not.toHaveBeenCalled()
})

it('leaves captcha CSRF recovery to its caller without a background token request', async () => {
  sessionState.csrf = { headerName: 'X-CSRF-TOKEN', token: 'expired' }
  const epoch = sessionState.epoch
  const fetchMock = vi.fn().mockResolvedValue(response('CSRF_INVALID', 403, null))
  vi.stubGlobal('fetch', fetchMock)
  await expect(
    request({ ...options, path: '/auth/captcha', method: 'POST' }),
  ).rejects.toMatchObject({ code: 'CSRF_INVALID' })
  await vi.dynamicImportSettled()
  expect(sessionState.csrf).toBeNull()
  expect(sessionState.epoch).toBe(epoch)
  expect(fetchMock).toHaveBeenCalledTimes(1)
  expect(endedNotice.value).toBeNull()
  expect(location.assign).not.toHaveBeenCalled()
})

it('still refreshes CSRF after other rejected writes without repeating the failed action', async () => {
  sessionState.csrf = { headerName: 'X-CSRF-TOKEN', token: 'expired' }
  const token = { headerName: 'X-CSRF-TOKEN', token: 'fresh' }
  const fetchMock = vi
    .fn()
    .mockResolvedValueOnce(response('CSRF_INVALID', 403, null))
    .mockResolvedValueOnce(response('SUCCESS', 200, token))
  vi.stubGlobal('fetch', fetchMock)
  await expect(
    request({ ...options, path: '/auth/password', method: 'PUT' }),
  ).rejects.toMatchObject({ code: 'CSRF_INVALID' })
  await vi.waitFor(() => expect(sessionState.csrf).toEqual(token))
  expect(fetchMock).toHaveBeenCalledTimes(2)
  expect(fetchMock.mock.calls[1]?.[0]).toMatch(/\/auth\/csrf$/)
  expect(endedNotice.value).toBeNull()
  expect(location.assign).not.toHaveBeenCalled()
})

it.each(['UNAUTHORIZED', 'SESSION_REPLACED', 'SESSION_FORCED_LOGOUT'])(
  'preserves %s session handling for captcha requests',
  async (code) => {
    sessionState.csrf = { headerName: 'X-CSRF-TOKEN', token: 'expired' }
    const epoch = sessionState.epoch
    const fetchMock = vi.fn().mockResolvedValue(response(code))
    vi.stubGlobal('fetch', fetchMock)
    await expect(
      request({ ...options, path: '/auth/captcha', method: 'POST' }),
    ).rejects.toMatchObject({ code })
    await vi.dynamicImportSettled()
    expect(sessionState.csrf).toBeNull()
    expect(sessionState.epoch).toBe(epoch + 1)
    expect(fetchMock).toHaveBeenCalledTimes(1)
    if (code === 'UNAUTHORIZED') {
      expect(endedNotice.value).toBeNull()
      expect(location.assign).toHaveBeenCalledExactlyOnceWith('/login')
    } else {
      expect(endedNotice.value?.reason).toBe(
        code === 'SESSION_REPLACED' ? 'REPLACED' : 'FORCED_LOGOUT',
      )
      expect(replace).toHaveBeenCalledExactlyOnceWith('/login')
      expect(location.assign).not.toHaveBeenCalled()
    }
  },
)
