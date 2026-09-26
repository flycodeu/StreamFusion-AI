import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import type { AuthUser } from '../api/auth/types'

const auth = vi.hoisted(() => ({
  getCsrf: vi.fn(),
  getMe: vi.fn(),
  login: vi.fn(),
  logout: vi.fn(),
  changePassword: vi.fn(),
}))
const signal = vi.hoisted(() => ({ notify: vi.fn() }))
vi.mock('../api/auth/api', () => auth)
vi.mock('./crossTab', () => ({ notifyOtherTabs: signal.notify }))
vi.mock('./rememberedLogin', () => ({
  forgetRememberedLogin: vi.fn().mockResolvedValue(undefined),
}))

const identity: AuthUser = {
  user: {
    id: '1',
    username: 'operator',
    nickname: null,
    avatarKey: null,
    phone: null,
    email: null,
    gender: 0,
    status: 1,
    mustChangePassword: false,
    version: '0',
  },
  modules: [],
  roles: [],
  routes: [],
  isSuperAdmin: false,
}

function deferred<T>() {
  let resolve!: (value: T) => void
  const promise = new Promise<T>((yes) => {
    resolve = yes
  })
  return { promise, resolve }
}

beforeEach(() => {
  vi.resetModules()
  vi.clearAllMocks()
  for (const mock of Object.values(auth)) mock.mockReset()
  vi.stubGlobal('location', { replace: vi.fn() })
})
afterEach(() => vi.unstubAllGlobals())

describe('shared cookie identity rebuilding', () => {
  it('clears identity immediately, reloads once, and rejects a pending old restoration', async () => {
    const { sessionState, setIdentity } = await import('./state')
    const { reloadSharedSession, restoreSession } = await import('./session')
    setIdentity(identity)
    const oldEpoch = sessionState.epoch
    const pending = deferred<{ headerName: string; token: string }>()
    auth.getCsrf.mockReturnValue(pending.promise)
    const restoring = restoreSession()
    const rejected = expect(restoring).rejects.toMatchObject({ code: 'REQUEST_CANCELLED' })

    reloadSharedSession()
    reloadSharedSession()
    expect(sessionState.epoch).toBe(oldEpoch + 1)
    expect(sessionState.me).toBeNull()
    expect(globalThis.location.replace).toHaveBeenCalledExactlyOnceWith('/login')

    pending.resolve({ headerName: 'X-CSRF-TOKEN', token: 'old' })
    await rejected
    expect(auth.getMe).not.toHaveBeenCalled()
    expect(sessionState.me).toBeNull()
  })

  it('does not restore an in-flight local login after another tab requests rebuilding', async () => {
    const { sessionState } = await import('./state')
    const { reloadSharedSession, signIn } = await import('./session')
    sessionState.csrf = { headerName: 'X-CSRF-TOKEN', token: 'csrf' }
    const pending = deferred<void>()
    auth.login.mockReturnValue(pending.promise)
    const signingIn = signIn('operator', 'example')
    const rejected = expect(signingIn).rejects.toMatchObject({ code: 'REQUEST_CANCELLED' })
    reloadSharedSession()
    pending.resolve()
    await rejected
    expect(signal.notify).not.toHaveBeenCalled()
    expect(auth.getMe).not.toHaveBeenCalled()
  })

  it('notifies after successful server login even when the following identity refresh fails', async () => {
    const { sessionState } = await import('./state')
    const { signIn } = await import('./session')
    sessionState.csrf = { headerName: 'X-CSRF-TOKEN', token: 'csrf' }
    auth.login.mockResolvedValue(undefined)
    auth.getCsrf.mockRejectedValue(new Error('network unavailable'))
    await expect(signIn('operator', 'example')).rejects.toThrow('network unavailable')
    expect(signal.notify).toHaveBeenCalledOnce()
  })

  it.each(['logout', 'password'] as const)(
    'notifies after a successful %s change',
    async (action) => {
      const { signOut, updatePassword } = await import('./session')
      if (action === 'logout') await signOut()
      else await updatePassword('example-old', 'example-new')
      expect(signal.notify).toHaveBeenCalledOnce()
    },
  )

  it('does not broadcast a rejected login', async () => {
    const { sessionState } = await import('./state')
    const { signIn } = await import('./session')
    sessionState.csrf = { headerName: 'X-CSRF-TOKEN', token: 'csrf' }
    auth.login.mockRejectedValue(new Error('rejected'))
    await expect(signIn('operator', 'example')).rejects.toThrow('rejected')
    expect(signal.notify).not.toHaveBeenCalled()
  })
})
