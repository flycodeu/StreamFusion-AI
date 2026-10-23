// @vitest-environment vue-renderer
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { nextTick } from 'vue'
import type { App } from 'vue'
import { node, nodes, renderer, text } from '../support/renderer'
import type { Host } from '../support/renderer'
import { clearIdentity, setIdentity } from '../../src/session/state'
import HomeView from '../../src/pages/home/HomeView.vue'
import { ApiRequestError } from '../../src/lib/http/error'
import type { AuthUser } from '../../src/api/auth/types'

const api = vi.hoisted(() => ({
  getHealth: vi.fn(),
  getPreviewCapacity: vi.fn(),
  getPreviewCatalog: vi.fn(),
  push: vi.fn(),
}))

vi.mock('vue-router', () => ({
  useRouter: () => ({
    push: api.push,
  }),
}))

vi.mock('../../src/api/health/api', () => ({
  getHealth: api.getHealth,
}))

vi.mock('../../src/api/preview/api', () => ({
  getPreviewCapacity: api.getPreviewCapacity,
  getPreviewCatalog: api.getPreviewCatalog,
}))

vi.mock('element-plus', async () => {
  const { defineComponent, h } = await import('vue')
  return {
    ElAlert: defineComponent({
      props: ['title', 'type'],
      setup(props, { slots }) {
        return () =>
          h('div', { class: `el-alert el-alert--${props.type}` }, [props.title, slots.default?.()])
      },
    }),
    ElButton: defineComponent({
      props: ['loading'],
      setup(props, { attrs, slots }) {
        return () => h('button', { ...attrs, 'data-loading': props.loading }, slots.default?.())
      },
    }),
    ElCard: defineComponent({
      setup(_, { slots }) {
        return () => h('div', { class: 'el-card' }, [slots.header?.(), slots.default?.()])
      },
    }),
    ElTag: defineComponent({
      props: ['type'],
      setup(props, { slots }) {
        return () => h('span', { class: `el-tag el-tag--${props.type}` }, slots.default?.())
      },
    }),
  }
})

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

const findButton = (root: Host, labelText: string) =>
  nodes(root).find((item) => item.type === 'button' && text(item).includes(labelText))

beforeEach(() => {
  vi.clearAllMocks()
  clearIdentity()
  api.getHealth.mockResolvedValue({ status: 'UP' })
  api.getPreviewCapacity.mockResolvedValue({
    maxSessions: 16,
    occupiedSessions: 3,
    closingSessions: 0,
    maxTranscodes: 4,
    occupiedTranscodes: 1,
    transcodesAvailable: true,
    observedAt: '2026-10-23T19:00:00Z',
  })
  api.getPreviewCatalog.mockResolvedValue({
    total: 12,
    page: 1,
    size: 24,
    items: [
      {
        cameraId: 'cam-01',
        name: '东门入口摄像机',
        deviceName: '门禁枪机-01',
        deviceGroupKey: 'd1',
        groupId: null,
        groupPath: null,
        lifecycle: 'ACTIVE',
        profiles: [],
      },
    ],
  })
})

afterEach(() => {
  app?.unmount()
  app = null
  vi.restoreAllMocks()
})

describe('HomeView Enterprise Dashboard', () => {
  it('renders 3-tier enterprise layout without redundant remarks', async () => {
    const root = await mount()
    const content = text(root)
    expect(content).toContain('接入视频源')
    expect(content).toContain('实时推理流')
    expect(content).toContain('今日推送统计')
    expect(content).toContain('服务运行状态')
    expect(content).toContain('核心在线')
    expect(content).toContain('推送统计趋势')
    expect(content).toContain('推理触发分类占比')
    expect(content).toContain('最新推理截图')
    // Ensure removed sections and redundant remarks do not exist
    expect(content).not.toContain('平台运行总览')
    expect(content).not.toContain('最近录像片段')
    expect(content).not.toContain('实时抓拍流监听就绪，下拉滚动加载更多截图记录')
    expect(content).not.toContain('实时分流与分析会话通道')
    expect(content).not.toContain('platform-api')
  })

  it('switches trend chart between 24h time slots and 7d real date+weekday labels', async () => {
    const root = await mount()
    expect(text(root)).toContain('00:00')
    expect(text(root)).toContain('22:00')

    const btn7d = findButton(root, '近7天')
    expect(btn7d).toBeDefined()
    ;(btn7d!.props.onClick as () => void)()
    await nextTick()

    const xLabels = nodes(root)
      .filter((n) => n.props?.class === 'x-label')
      .map((n) => text(n).trim())
    expect(xLabels).toHaveLength(7)
    for (const label of xLabels) {
      expect(label).toMatch(/^\d{2}\/\d{2} 周[日一二三四五六]$/)
    }
  })

  it('dynamically adapts snapshot count to screen resolution (more on large screen, fewer on small screen)', async () => {
    const prevWidth = globalThis.innerWidth
    const prevHeight = globalThis.innerHeight

    try {
      Object.defineProperty(globalThis, 'innerWidth', { value: 1280, configurable: true })
      Object.defineProperty(globalThis, 'innerHeight', { value: 768, configurable: true })
      const smallRoot = await mount()
      const smallCards = nodes(smallRoot).filter((n) => n.type === 'article')
      app?.unmount()
      app = null

      Object.defineProperty(globalThis, 'innerWidth', { value: 2560, configurable: true })
      Object.defineProperty(globalThis, 'innerHeight', { value: 1440, configurable: true })
      const largeRoot = await mount()
      const largeCards = nodes(largeRoot).filter((n) => n.type === 'article')

      expect(largeCards.length).toBeGreaterThan(smallCards.length)
      expect(largeCards.length).toBeGreaterThan(10)
    } finally {
      Object.defineProperty(globalThis, 'innerWidth', { value: prevWidth, configurable: true })
      Object.defineProperty(globalThis, 'innerHeight', { value: prevHeight, configurable: true })
    }
  })

  it('gracefully loads preview capacity and point names when user has camera module', async () => {
    setIdentity({
      user: {
        id: '1',
        username: 'admin',
        nickname: '系统管理员',
        version: '1',
        status: 1,
        gender: 1,
        mustChangePassword: false,
        avatarKey: null,
        phone: null,
        email: null,
      },
      modules: ['camera'],
      roles: [{ id: '1', code: 'ADMIN', name: '系统管理员' }],
      routes: [],
      isSuperAdmin: true,
    } as AuthUser)

    const root = await mount()
    await vi.waitFor(() => expect(api.getPreviewCapacity).toHaveBeenCalled())
    await vi.waitFor(() => expect(api.getPreviewCatalog).toHaveBeenCalled())
    const content = text(root)
    expect(content).toContain('12')
    expect(content).toContain('3')
    expect(content).toContain('/ 16 路')
    expect(content).toContain('东门入口摄像机')
  })

  it('does not call getPreviewCapacity when user lacks camera module', async () => {
    setIdentity({
      user: {
        id: '2',
        username: 'auditor',
        nickname: '审计员',
        version: '1',
        status: 1,
        gender: 1,
        mustChangePassword: false,
        avatarKey: null,
        phone: null,
        email: null,
      },
      modules: ['audit'],
      roles: [{ id: '2', code: 'AUDITOR', name: '安全审计员' }],
      routes: [],
      isSuperAdmin: false,
    } as AuthUser)

    const root = await mount()
    await nextTick()
    expect(api.getPreviewCapacity).not.toHaveBeenCalled()
    expect(api.getPreviewCatalog).not.toHaveBeenCalled()
    const content = text(root)
    expect(content).toContain('—')
  })

  it('triggers health refresh on clicking check button', async () => {
    const root = await mount()
    const checkBtn = findButton(root, '刷新')
    expect(checkBtn).toBeDefined()

    api.getHealth.mockClear()
    ;(checkBtn!.props.onClick as () => void)()
    await vi.waitFor(() => expect(api.getHealth).toHaveBeenCalledTimes(1))
  })

  it('handles platform health check failure and displays error information', async () => {
    api.getHealth.mockRejectedValue(
      new ApiRequestError('CONNECT_REFUSED', '平台后台未响应，无法连接服务', {
        status: 503,
        traceId: 'trace-err-999',
      }),
    )

    const root = await mount()
    await nextTick()
    const content = text(root)
    expect(content).toContain('连接异常')
    expect(content).toContain('错误码：CONNECT_REFUSED')
    expect(content).toContain('trace-err-999')
  })

  it('navigates when clicking quick action link', async () => {
    const root = await mount()
    const previewBtn = findButton(root, '实时预览')
    expect(previewBtn).toBeDefined()
    ;(previewBtn!.props.onClick as () => void)()
    expect(api.push).toHaveBeenCalledWith('/preview/center')
  })
})
