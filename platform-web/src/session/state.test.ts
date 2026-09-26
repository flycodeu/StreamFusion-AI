import { afterEach, expect, it, vi } from 'vitest'
import type { AuthUser } from '../api/auth/types'
import { clearIdentity, sessionState, setIdentity } from './state'

afterEach(() => {
  clearIdentity()
  vi.unstubAllGlobals()
})

it('keeps authentication and logout usable when browser storage is blocked', () => {
  vi.stubGlobal('sessionStorage', {
    setItem: () => {
      throw new Error('blocked')
    },
    removeItem: () => {
      throw new Error('blocked')
    },
  })
  const me: AuthUser = {
    user: {
      id: '1',
      username: 'tester',
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
  expect(() => setIdentity(me)).not.toThrow()
  expect(sessionState.me?.user.username).toBe('tester')
  const before = sessionState.epoch
  expect(() => clearIdentity()).not.toThrow()
  expect(sessionState.me).toBeNull()
  expect(sessionState.epoch).toBe(before + 1)
})
