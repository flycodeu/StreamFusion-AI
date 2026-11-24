// @vitest-environment vue-renderer
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { nextTick, type App } from 'vue'
import { node, nodes, renderer, text } from '../support/renderer'
import { clearIdentity, setIdentity } from '../../src/session/state'
import HomeView from '../../src/pages/home/HomeView.vue'
import { ApiRequestError } from '../../src/lib/http/error'
import type { MenuRoute } from '../../src/api/auth/types'

const api = vi.hoisted(() => ({
  getHealth: vi.fn(),
  getPreviewCapacity: vi.fn(),
  getPreviewCatalog: vi.fn(),
  getStatusPage: vi.fn(),
}))

vi.mock('vue-router', async () => {
  const { defineComponent, h } = await import('vue')
  return {
    RouterLink: defineComponent({
      props: ['to'],
      setup(props, { slots }) {
        return () => h('a', { targetRoute: props.to }, slots.default?.())
      },
    }),
  }
})
vi.mock('../../src/api/health/api', () => ({ getHealth: api.getHealth }))
vi.mock('../../src/api/preview/api', () => ({
  getPreviewCapacity: api.getPreviewCapacity,
  getPreviewCatalog: api.getPreviewCatalog,
}))
vi.mock('../../src/api/camera-status/api', () => ({ getStatusPage: api.getStatusPage }))
vi.mock('element-plus', async () => {
  const { defineComponent, h } = await import('vue')
  return {
    ElButton: defineComponent({
      setup(_, { slots }) {
        return () => h('button', slots.default?.())
      },
    }),
    ElCard: defineComponent({
      setup(_, { slots }) {
        return () => h('section', slots.default?.())
      },
    }),
  }
})

function route(id: string, path: string, componentKey: string): MenuRoute {
  return {
    id,
    name: id,
    type: 'PAGE',
    icon: null,
    visible: true,
    routeName: id,
    path,
    componentKey,
    moduleKey: 'camera',
    children: [],
  }
}

function identity(routes: MenuRoute[]) {
  setIdentity({
    user: {
      id: '1',
      username: 'admin',
      nickname: '运维员',
      version: '1',
      status: 1,
      gender: 1,
      mustChangePassword: false,
      avatarKey: null,
      phone: null,
      email: null,
    },
    modules: ['camera'],
    roles: [],
    routes,
    isSuperAdmin: false,
  })
}

let app: App | null = null
async function mount() {
  const root = node('root')
  app = renderer.createApp(HomeView)
  app.mount(root)
  await nextTick()
  await vi.waitFor(() => expect(api.getHealth).toHaveBeenCalled())
  await nextTick()
  return root
}

beforeEach(() => {
  vi.resetAllMocks()
  clearIdentity()
  api.getHealth.mockResolvedValue({ status: 'UP' })
  api.getPreviewCapacity.mockResolvedValue({ maxSessions: 16, occupiedSessions: 3 })
  api.getPreviewCatalog.mockResolvedValue({ total: 12, items: [] })
  api.getStatusPage.mockResolvedValue({ total: 2, items: [] })
})

afterEach(() => {
  app?.unmount()
  app = null
})

describe('视频工作台', () => {
  it('shows live figures and uses authorized menu paths, including the failed check filter', async () => {
    identity([
      {
        ...route('video', '/video', ''),
        type: 'DIRECTORY',
        children: [
          route('1', '/video/assets', '/camera/Manage'),
          route('2', '/video/live', '/preview/Center'),
          route('3', '/video/checks', '/camera/Status'),
        ],
      },
    ])
    const root = await mount()
    const content = text(root)
    expect(content).toContain('可见视频通道12')
    expect(content).toContain('预览会话占用3 / 16')
    expect(content).toContain('连接检查异常设备2')
    expect(content).not.toContain('实时推理流')
    expect(content).not.toContain('最新推理截图')
    expect(api.getStatusPage).toHaveBeenCalledWith({
      layer: 'CONTROL',
      observation: 'FAILED',
      page: 1,
      size: 1,
    })
    expect(
      nodes(root)
        .filter((item) => item.type === 'a')
        .map((item) => item.props.targetRoute),
    ).toEqual([
      '/video/assets',
      '/video/live',
      { path: '/video/checks', query: { observation: 'FAILED' } },
    ])
  })

  it('skips video requests and hides the links without authorized menu entries', async () => {
    identity([])
    const root = await mount()
    expect(api.getPreviewCatalog).not.toHaveBeenCalled()
    expect(api.getPreviewCapacity).not.toHaveBeenCalled()
    expect(api.getStatusPage).not.toHaveBeenCalled()
    expect(text(root)).toContain('平台服务连接正常')
    expect(nodes(root).filter((item) => item.type === 'a')).toHaveLength(0)
  })

  it('keeps the request identifier available when the health check fails', async () => {
    const traceId = 'a'.repeat(32)
    api.getHealth.mockRejectedValue(
      new ApiRequestError('CONNECT_REFUSED', '平台后台未响应，无法连接服务', {
        status: 503,
        traceId,
      }),
    )
    const root = await mount()
    expect(text(root)).toContain('平台服务连接异常')
    expect(text(root)).toContain('CONNECT_REFUSED')
    expect(text(root)).toContain(traceId)
    const refresh = nodes(root).find((item) => item.type === 'button' && text(item) === '刷新')!
    ;(refresh.props.onClick as () => void)()
    await vi.waitFor(() => expect(api.getHealth).toHaveBeenCalledTimes(2))
  })
})
