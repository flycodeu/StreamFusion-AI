import { beforeEach, describe, expect, it, vi } from 'vitest'
import type { AuthUser, MenuRoute } from '../api/auth/types'
import { buildMenuRoutes } from '../router/dynamic/routes'
import { clearIdentity, sessionState, setIdentity } from './state'
import { synchronizeAuthorization } from './authorizationSync'

const auth = vi.hoisted(() => ({ getMe: vi.fn<() => Promise<AuthUser>>() }))
const routing = vi.hoisted(() => ({
  currentRoute: { value: { path: '/camera/manage', query: { page: '2' }, hash: '#details' } },
  replace: vi.fn().mockResolvedValue(undefined),
}))
vi.mock('../api/auth/api', () => auth)
vi.mock('../router', () => ({ router: routing }))

const camera: MenuRoute = {
  id: '10',
  name: '相机管理',
  type: 'PAGE',
  icon: null,
  visible: true,
  routeName: 'camera',
  path: '/camera/manage',
  componentKey: '/camera/manage',
  moduleKey: 'camera',
  children: [],
}
function identity(): AuthUser {
  return {
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
    modules: ['camera'],
    roles: [],
    routes: [camera],
    isSuperAdmin: false,
  }
}

beforeEach(() => {
  clearIdentity()
  setIdentity(identity())
  routing.currentRoute.value = { path: '/camera/manage', query: { page: '2' }, hash: '#details' }
  routing.replace.mockClear()
  auth.getMe.mockReset().mockResolvedValue(identity())
})

describe('authorization synchronization after a business refusal', () => {
  it('removes revoked navigation and reruns the current guard with its query and hash', async () => {
    auth.getMe.mockResolvedValue({ ...identity(), modules: [], routes: [] })
    await synchronizeAuthorization(sessionState.epoch)
    expect(sessionState.me?.modules).toEqual([])
    expect(buildMenuRoutes(sessionState.me!.routes, sessionState.me!.modules).navigation).toEqual(
      [],
    )
    expect(routing.replace).toHaveBeenCalledExactlyOnceWith({
      path: '/camera/manage',
      query: { page: '2' },
      hash: '#details',
      force: true,
    })
    expect(auth.getMe).toHaveBeenCalledTimes(1)
  })

  it('retains an authorized view and its error after an object-level refusal', async () => {
    const updated = identity()
    updated.user.nickname = '当前昵称'
    auth.getMe.mockResolvedValue(updated)
    await synchronizeAuthorization(sessionState.epoch)
    expect(sessionState.me?.user.nickname).toBe('当前昵称')
    expect(routing.replace).not.toHaveBeenCalled()
    expect(auth.getMe).toHaveBeenCalledTimes(1)
  })

  it('retains a hidden PAGE that still has its module grant', async () => {
    auth.getMe.mockResolvedValue({ ...identity(), routes: [{ ...camera, visible: false }] })
    await synchronizeAuthorization(sessionState.epoch)
    expect(routing.replace).not.toHaveBeenCalled()
    expect(buildMenuRoutes(sessionState.me!.routes, sessionState.me!.modules).navigation).toEqual(
      [],
    )
  })

  it('reruns the guard when the PAGE exists but its module grant was removed', async () => {
    auth.getMe.mockResolvedValue({ ...identity(), modules: [] })
    await synchronizeAuthorization(sessionState.epoch)
    expect(routing.replace).toHaveBeenCalledTimes(1)
  })

  it('refreshes fixed-page navigation without requiring PAGE grants', async () => {
    routing.currentRoute.value.path = '/profile'
    auth.getMe.mockResolvedValue({ ...identity(), modules: [], routes: [] })
    await synchronizeAuthorization(sessionState.epoch)
    expect(sessionState.me?.routes).toEqual([])
    expect(routing.replace).not.toHaveBeenCalled()
  })

  it('reruns the guard for a newly required password change', async () => {
    const updated = identity()
    updated.user.mustChangePassword = true
    auth.getMe.mockResolvedValue(updated)
    await synchronizeAuthorization(sessionState.epoch)
    expect(routing.replace).toHaveBeenCalledTimes(1)
  })

  it.each(['/login', '/unavailable', '/change-password'])(
    'does not start a redirect loop on %s',
    async (path) => {
      routing.currentRoute.value.path = path
      const updated = identity()
      updated.user.mustChangePassword = true
      auth.getMe.mockResolvedValue(updated)
      await synchronizeAuthorization(sessionState.epoch)
      expect(routing.replace).not.toHaveBeenCalled()
    },
  )

  it('leaves identity and navigation unchanged if the identity endpoint fails', async () => {
    auth.getMe.mockRejectedValue(new Error('unavailable'))
    await expect(synchronizeAuthorization(sessionState.epoch)).rejects.toThrow('unavailable')
    expect(sessionState.me?.modules).toEqual(['camera'])
    expect(routing.replace).not.toHaveBeenCalled()
  })

  it('ignores a previous identity both before and after the refresh', async () => {
    await synchronizeAuthorization(sessionState.epoch - 1)
    expect(auth.getMe).not.toHaveBeenCalled()
    let resolve!: (value: AuthUser) => void
    auth.getMe.mockImplementation(() => new Promise((done) => (resolve = done)))
    const pending = synchronizeAuthorization(sessionState.epoch)
    clearIdentity()
    const nextUser = identity()
    nextUser.user.id = '2'
    setIdentity(nextUser)
    resolve({ ...identity(), modules: [], routes: [] })
    await pending
    expect(sessionState.me?.user.id).toBe('2')
    expect(sessionState.me?.modules).toEqual(['camera'])
    expect(routing.replace).not.toHaveBeenCalled()
  })

  it('does not overwrite a newer snapshot of the same account while getMe is pending', async () => {
    let resolve!: (value: AuthUser) => void
    auth.getMe.mockImplementation(() => new Promise((done) => (resolve = done)))
    const epoch = sessionState.epoch
    const pending = synchronizeAuthorization(epoch)
    const newer = identity()
    newer.user.nickname = '已更新资料'
    setIdentity(newer)
    const latestIdentity = sessionState.me
    resolve({ ...identity(), modules: [], routes: [] })
    await pending
    expect(sessionState.epoch).toBe(epoch)
    expect(sessionState.me).toBe(latestIdentity)
    expect(sessionState.me?.user.nickname).toBe('已更新资料')
    expect(sessionState.me?.modules).toEqual(['camera'])
    expect(routing.replace).not.toHaveBeenCalled()
  })

  it('does not navigate if its own snapshot was replaced while the router import was pending', async () => {
    const state = await import('./state')
    const applyIdentity = state.setIdentity
    const newer = { ...identity(), modules: [], routes: [] }
    newer.user.nickname = '后续身份刷新'
    const write = vi.spyOn(state, 'setIdentity').mockImplementationOnce((me) => {
      applyIdentity(me)
      queueMicrotask(() => applyIdentity(newer))
    })
    const epoch = sessionState.epoch
    try {
      await synchronizeAuthorization(epoch)
      expect(write).toHaveBeenCalledTimes(1)
      expect(sessionState.epoch).toBe(epoch)
      expect(sessionState.me?.user.nickname).toBe('后续身份刷新')
      expect(sessionState.me?.modules).toEqual([])
      expect(routing.replace).not.toHaveBeenCalled()
    } finally {
      write.mockRestore()
    }
  })
})
