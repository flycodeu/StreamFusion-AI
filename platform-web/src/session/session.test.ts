import { beforeEach, describe, expect, it, vi } from 'vitest'
import type { AuthUser } from '../api/auth/types'
import { ApiRequestError } from '../lib/http/error'
import { clearIdentity, sessionState, setIdentity } from './state'
import { refreshCsrf, refreshIdentity, restoreSession, signOut } from './session'

const auth = vi.hoisted(() => ({ getCsrf: vi.fn(), getMe: vi.fn(), logout: vi.fn() }))
vi.mock('../api/auth/api', () => auth)
vi.mock('./rememberedLogin', () => ({ forgetRememberedLogin: vi.fn() }))

function identity(version = '0'): AuthUser {
  return {
    user: {
      id: '1',
      username: 'operator',
      nickname: `姓名${version}`,
      avatarKey: null,
      phone: null,
      email: null,
      gender: 0,
      status: 1,
      mustChangePassword: false,
      version,
    },
    modules: [],
    roles: [],
    routes: [],
    isSuperAdmin: false,
  }
}
function deferred<T>() {
  let resolve!: (value: T) => void
  let reject!: (reason: unknown) => void
  const promise = new Promise<T>((yes, no) => {
    resolve = yes
    reject = no
  })
  return { promise, resolve, reject }
}
beforeEach(() => {
  vi.resetAllMocks()
  clearIdentity()
  sessionState.ready = false
})

describe('session coordination', () => {
  it.each(['older-first', 'newer-first'])(
    'retains the newest requested identity when concurrent refreshes finish %s',
    async (order) => {
      setIdentity(identity())
      const older = deferred<AuthUser>()
      const newer = deferred<AuthUser>()
      auth.getMe.mockReturnValueOnce(older.promise).mockReturnValueOnce(newer.promise)
      const oldRefresh = refreshIdentity()
      const newRefresh = refreshIdentity()
      const latest = { ...identity('2'), modules: ['user', 'audit'] }
      if (order === 'older-first') {
        older.resolve(identity('1'))
        await oldRefresh
        expect(sessionState.me?.user.version).toBe('0')
        newer.resolve(latest)
        await newRefresh
      } else {
        newer.resolve(latest)
        await newRefresh
        older.resolve(identity('1'))
        await oldRefresh
      }
      expect(sessionState.me?.user.version).toBe('2')
      expect(sessionState.me?.modules).toEqual(['user', 'audit'])
    },
  )

  it('does not overwrite a saved profile with an older pending identity response', async () => {
    setIdentity(identity())
    const pending = deferred<AuthUser>()
    auth.getMe.mockReturnValue(pending.promise)
    const refresh = refreshIdentity()
    setIdentity(identity('1'))
    pending.resolve(identity())
    await refresh
    expect(sessionState.me?.user.version).toBe('1')
    expect(sessionState.me?.user.nickname).toBe('姓名1')
  })

  it('does not restore a CSRF token from a previous session', async () => {
    const pending = deferred<{ headerName: string; token: string }>()
    auth.getCsrf.mockReturnValue(pending.promise)
    const refresh = refreshCsrf()
    clearIdentity()
    sessionState.csrf = { headerName: 'X-CSRF-TOKEN', token: 'new-session' }
    pending.resolve({ headerName: 'X-CSRF-TOKEN', token: 'old-session' })
    await expect(refresh).rejects.toMatchObject({ code: 'REQUEST_CANCELLED' })
    expect(sessionState.csrf.token).toBe('new-session')
  })

  it('does not clear a newer login when an initial restoration finishes with cancellation', async () => {
    const pending = deferred<{ headerName: string; token: string }>()
    auth.getCsrf.mockReturnValue(pending.promise)
    const restoring = restoreSession()
    clearIdentity()
    setIdentity(identity('2'))
    sessionState.csrf = { headerName: 'X-CSRF-TOKEN', token: 'new-session' }
    pending.reject(new ApiRequestError('REQUEST_CANCELLED', '请求已取消'))
    await expect(restoring).rejects.toMatchObject({ code: 'REQUEST_CANCELLED' })
    expect(sessionState.me?.user.version).toBe('2')
    expect(sessionState.csrf?.token).toBe('new-session')
  })

  it('does not clear a newer identity when a previous logout completes late', async () => {
    setIdentity(identity())
    const pending = deferred<void>()
    auth.logout.mockReturnValue(pending.promise)
    const logout = signOut()
    clearIdentity()
    setIdentity(identity('3'))
    pending.resolve()
    await expect(logout).rejects.toMatchObject({ code: 'REQUEST_CANCELLED' })
    expect(sessionState.me?.user.version).toBe('3')
  })
})
