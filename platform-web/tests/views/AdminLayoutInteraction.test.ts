// @vitest-environment vue-renderer
import { afterEach, describe, expect, it, vi } from 'vitest'
import { nextTick, reactive, type App } from 'vue'
import { routeLocationKey } from 'vue-router'
import type { RouteLocationNormalizedLoaded } from 'vue-router'
import { node, nodes, renderer } from '../support/renderer'
import AdminLayout from '../../src/layouts/AdminLayout.vue'

vi.mock('../../src/layouts/components/AdminHeader.vue', async () => {
  const { defineComponent, h } = await import('vue')
  return {
    default: defineComponent({
      setup(_, { attrs }) {
        return () => h('AdminHeader', attrs)
      },
    }),
  }
})
vi.mock('../../src/layouts/components/AdminSidebar.vue', async () => {
  const { defineComponent, h } = await import('vue')
  return {
    default: defineComponent({
      setup(_, { attrs }) {
        return () => h('AdminSidebar', attrs)
      },
    }),
  }
})
vi.mock('../../src/layouts/components/AdminContent.vue', async () => {
  const { defineComponent, h } = await import('vue')
  return {
    default: defineComponent({
      setup(_, { slots }) {
        return () => h('main', {}, slots.default?.())
      },
    }),
  }
})
let app: App | undefined
afterEach(() => {
  app?.unmount()
  vi.unstubAllGlobals()
})

describe('admin navigation overlay', () => {
  it('closes an opened mobile sidebar after navigation completes', async () => {
    vi.stubGlobal('innerWidth', 390)
    const route = reactive({ fullPath: '/home' }) as RouteLocationNormalizedLoaded
    const root = node('root')
    app = renderer.createApp(AdminLayout)
    app.provide(routeLocationKey, route)
    app.mount(root)
    const header = () => nodes(root).find((n) => n.type === 'AdminHeader')!
    ;(header().props.onToggleSidebar as () => void)()
    await nextTick()
    expect(header().props.collapsed).toBe(false)
    route.fullPath = '/system/role'
    await nextTick()
    expect(header().props.collapsed).toBe(true)
    expect(nodes(root).some((n) => n.props.class === 'sidebar-backdrop')).toBe(false)
  })
  it('preserves the desktop sidebar preference on navigation', async () => {
    vi.stubGlobal('innerWidth', 1440)
    const route = reactive({ fullPath: '/home' }) as RouteLocationNormalizedLoaded
    const root = node('root')
    app = renderer.createApp(AdminLayout)
    app.provide(routeLocationKey, route)
    app.mount(root)
    route.fullPath = '/system/role'
    await nextTick()
    expect(nodes(root).find((n) => n.type === 'AdminHeader')?.props.collapsed).toBe(false)
  })
})
