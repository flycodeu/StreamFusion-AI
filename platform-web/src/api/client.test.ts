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
function replacedResponse() {
  return new Response(
    JSON.stringify({
      code: 'SESSION_REPLACED',
      msg: '登录已替换',
      data: { reason: 'REPLACED', sourceIp: '127.0.0.1' },
      traceId: 'a'.repeat(32),
      timestamp: '2026-09-26T12:00:00Z',
    }),
    { status: 401, headers: { 'Content-Type': 'application/json', 'X-Trace-Id': 'a'.repeat(32) } },
  )
}
it('preserves the kick-out dialog with blocked storage and navigates without reloading', async () => {
  vi.stubGlobal('fetch', vi.fn().mockResolvedValue(replacedResponse()))
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
  resolve(replacedResponse())
  await expect(pending).rejects.toMatchObject({ code: 'REQUEST_CANCELLED' })
  expect(endedNotice.value).toBeNull()
  expect(replace).not.toHaveBeenCalled()
})
