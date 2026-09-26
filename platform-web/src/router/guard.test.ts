import { beforeEach, describe, expect, it, vi } from 'vitest'
import type { AuthUser, MenuRoute } from '../api/auth/types'

const session = vi.hoisted(() => ({
  state: { ready: true, me: null as AuthUser | null },
  restore: vi.fn<() => Promise<void>>(),
  refresh: vi.fn<() => Promise<void>>(),
}))

vi.mock('vue-router', async (importOriginal) => {
  const original = await importOriginal<typeof import('vue-router')>()
  return { ...original, createWebHistory: original.createMemoryHistory }
})
vi.mock('../session/state', () => ({ sessionState: session.state }))
vi.mock('../session/session', () => ({
  restoreSession: session.restore,
  refreshIdentity: session.refresh,
}))
vi.mock('../pages/auth/LoginView.vue', () => ({ default: { render: () => null } }))
vi.mock('../pages/home/HomeView.vue', () => ({ default: { render: () => null } }))
vi.mock('../pages/account/ProfileView.vue', () => ({ default: { render: () => null } }))
vi.mock('../pages/auth/ChangePasswordView.vue', () => ({ default: { render: () => null } }))
vi.mock('../pages/error/ForbiddenView.vue', () => ({ default: { render: () => null } }))
vi.mock('../pages/error/UnavailableView.vue', () => ({ default: { render: () => null } }))
vi.mock('../pages/error/RouteUnavailableView.vue', () => ({ default: { render: () => null } }))

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
    modules: [],
    roles: [],
    routes: [],
    isSuperAdmin: false,
  }
}

const camera: MenuRoute = {
  id: '10',
  name: '相机管理',
  type: 'PAGE',
  icon: null,
  visible: true,
  routeName: 'camera',
  path: '/camera/manage',
  componentKey: null,
  moduleKey: 'camera',
  children: [],
}

beforeEach(() => {
  vi.resetModules()
  session.state.ready = true
  session.state.me = identity()
  session.refresh.mockReset().mockResolvedValue()
  session.restore.mockReset().mockImplementation(async () => {
    session.state.ready = true
    session.state.me = identity()
  })
})

describe('session-aware route guard', () => {
  it('uses the initial restore result without fetching the identity twice', async () => {
    session.state.ready = false
    session.state.me = null
    const { router } = await import('./index')
    await router.push('/home')
    expect(router.currentRoute.value.path).toBe('/home')
    expect(session.restore).toHaveBeenCalledTimes(1)
    expect(session.refresh).not.toHaveBeenCalled()
  })

  it.each(['/home', '/profile', '/change-password', '/forbidden'])(
    'validates the session on fixed page %s without requiring a PAGE grant',
    async (path) => {
      const { router } = await import('./index')
      await router.push(path)
      expect(router.currentRoute.value.path).toBe(path)
      expect(session.refresh).toHaveBeenCalledTimes(1)
    },
  )

  it.each(['/home', '/profile', '/change-password', '/forbidden'])(
    'redirects an expired session from %s to login without a refresh loop',
    async (path) => {
      session.refresh.mockImplementation(async () => {
        session.state.me = null
        throw new Error('session expired')
      })
      const { router } = await import('./index')
      await router.push(path)
      expect(router.currentRoute.value.path).toBe('/login')
      expect(router.currentRoute.value.query.next).toBe(path)
      expect(session.refresh).toHaveBeenCalledTimes(1)
    },
  )

  it('checks a newly required password change after refreshing the identity', async () => {
    session.refresh.mockImplementation(async () => {
      session.state.me = identity()
      session.state.me.user.mustChangePassword = true
    })
    const { router } = await import('./index')
    await router.push('/profile')
    expect(router.currentRoute.value.path).toBe('/change-password')
    expect(session.refresh).toHaveBeenCalledTimes(2)
  })

  it('redirects to login when another request clears identity during its refresh', async () => {
    session.refresh.mockImplementation(async () => {
      session.state.me = null
    })
    const { router } = await import('./index')
    await router.push('/profile')
    expect(router.currentRoute.value.path).toBe('/login')
    expect(router.currentRoute.value.query.next).toBe('/profile')
    expect(session.refresh).toHaveBeenCalledTimes(1)
  })

  it('shows the unavailable page after an identity dependency failure without repeating it', async () => {
    session.refresh.mockRejectedValue(new Error('backend unavailable'))
    const { router } = await import('./index')
    await router.push('/profile')
    expect(router.currentRoute.value.path).toBe('/unavailable')
    expect(session.refresh).toHaveBeenCalledTimes(1)
  })

  it.each(['/login', '/unavailable'])(
    'does not request the identity on public or recovery route %s',
    async (path) => {
      session.state.me = null
      const { router } = await import('./index')
      await router.push(path)
      expect(router.currentRoute.value.path).toBe(path)
      expect(session.refresh).not.toHaveBeenCalled()
    },
  )

  it.each(['/login', '/unavailable'])(
    'allows %s to render when initial restoration fails',
    async (path) => {
      session.state.ready = false
      session.state.me = null
      session.restore.mockImplementation(async () => {
        session.state.ready = true
        throw new Error('backend unavailable')
      })
      const { router } = await import('./index')
      await router.push(path)
      expect(router.currentRoute.value.path).toBe(path)
      expect(session.restore).toHaveBeenCalledTimes(1)
      expect(session.refresh).not.toHaveBeenCalled()
    },
  )

  it('rematches a newly registered dynamic route once and removes it after revocation', async () => {
    session.state.me = { ...identity(), modules: ['camera'], routes: [camera] }
    const { router } = await import('./index')
    await router.push('/camera/manage')
    expect(router.currentRoute.value.path).toBe('/camera/manage')
    expect(session.refresh).toHaveBeenCalledTimes(2)
    await router.push('/home')
    session.refresh.mockClear().mockImplementation(async () => {
      session.state.me = identity()
    })
    await router.push('/camera/manage')
    expect(router.currentRoute.value.path).toBe('/forbidden')
    expect(router.hasRoute('page:camera')).toBe(false)
    expect(session.refresh).toHaveBeenCalledTimes(2)
  })
})
