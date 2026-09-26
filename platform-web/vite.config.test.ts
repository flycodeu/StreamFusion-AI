import { describe, expect, it } from 'vitest'
import { EventEmitter } from 'node:events'
import type { ProxyOptions } from 'vite'
import config from './vite.config'

const proxy = config({ command: 'serve', mode: 'test' }).server?.proxy ?? {}
const businessProxyRules = Object.keys(proxy).filter((key) => key.startsWith('^'))
const isProxied = (path: string) => businessProxyRules.some((rule) => new RegExp(rule).test(path))

describe('development business API proxy', () => {
  it.each([
    ['198.51.100.42', '198.51.100.42'],
    ['2001:db8::42', '2001:db8::42'],
    ['::ffff:192.0.2.42', '::ffff:192.0.2.42'],
    [undefined, '127.0.0.1'],
  ])(
    'sanitizes forged forwarding headers using the socket address %s',
    (remoteAddress, expected) => {
      const rule = proxy['^/api(?:/|$)']
      if (!rule || typeof rule === 'string' || !rule.configure) {
        throw new Error('The business API proxy must register its edge header sanitizer')
      }
      const events = new EventEmitter()
      rule.configure(
        events as unknown as Parameters<NonNullable<ProxyOptions['configure']>>[0],
        rule,
      )
      expect(events.listenerCount('proxyReq')).toBe(1)

      const headers = new Map([
        ['x-forwarded-for', '203.0.113.10, 203.0.113.11'],
        ['forwarded', 'for=203.0.113.12;proto=https'],
        ['x-real-ip', '203.0.113.13'],
        ['x-request-id', 'request-42'],
      ])
      events.emit(
        'proxyReq',
        {
          setHeader: (name: string, value: string) => headers.set(name.toLowerCase(), value),
          removeHeader: (name: string) => headers.delete(name.toLowerCase()),
        },
        { socket: { remoteAddress } },
      )

      expect(headers.get('x-forwarded-for')).toBe(expected)
      expect(headers.has('forwarded')).toBe(false)
      expect(headers.has('x-real-ip')).toBe(false)
      expect(headers.get('x-request-id')).toBe('request-42')
    },
  )

  it.each(['user', 'auth', 'roles', 'menus', 'departments', 'camera'])(
    'forwards the %s module root, query and child paths',
    (module) => {
      expect(isProxied(`/api/${module}`)).toBe(true)
      expect(isProxied(`/api/${module}?page=1`)).toBe(true)
      expect(isProxied(`/api/${module}/9007199254740993`)).toBe(true)
      const rule = proxy['^/api(?:/|$)']
      if (typeof rule === 'object') {
        expect(rule.rewrite?.(`/api/${module}?page=1`)).toBe(`/${module}?page=1`)
      }
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
    '/camera/manage',
    '/system/Department',
    '/api-docs',
    '/src/views/camera/manage.vue',
  ])('does not send a page or asset path to the backend: %s', (path) => {
    expect(isProxied(path)).toBe(false)
  })
})
