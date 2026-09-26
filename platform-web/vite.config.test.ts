import { describe, expect, it } from 'vitest'
import config from './vite.config'

const proxy = config({ command: 'serve', mode: 'test' }).server?.proxy ?? {}
const businessProxyRules = Object.keys(proxy).filter((key) => key.startsWith('^'))
const isProxied = (path: string) => businessProxyRules.some((rule) => new RegExp(rule).test(path))

describe('development business API proxy', () => {
  it.each(['user', 'auth', 'roles', 'menus', 'departments'])(
    'forwards the %s module root, query and child paths',
    (module) => {
      expect(isProxied(`/${module}`)).toBe(true)
      expect(isProxied(`/${module}?page=1`)).toBe(true)
      expect(isProxied(`/${module}/9007199254740993`)).toBe(true)
    },
  )

  it.each([
    '/users',
    '/authentication',
    '/role',
    '/roles-admin',
    '/menu',
    '/menus-extra',
    '/department',
    '/departments-private',
    '/private',
  ])('does not proxy a path outside the declared modules: %s', (path) => {
    expect(isProxied(path)).toBe(false)
  })
})
