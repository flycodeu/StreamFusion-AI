// @vitest-environment vue-renderer
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { nextTick } from 'vue'
import type { App } from 'vue'
import type { PreviewChannel } from '../../src/api/preview/types'
import { ApiRequestError } from '../../src/lib/http/error'
import { node, nodes, renderer, text } from '../support/renderer'
import type { Host } from '../support/renderer'
import Center from '../../src/views/preview/Center.vue'

const api = vi.hoisted(() => ({ catalog: vi.fn(), groups: vi.fn() }))
vi.mock('vue-router', () => ({
  useRoute: () => ({ query: {} }),
  useRouter: () => ({ push: vi.fn(), back: vi.fn() }),
}))
vi.mock('../../src/api/preview/api', () => ({ getPreviewCatalog: api.catalog }))
vi.mock('../../src/api/camera-groups/api', () => ({ getGroupTree: api.groups }))
vi.mock('../../src/features/preview/player', () => ({
  createPreviewPlayer: (events: { lease: (lease: unknown) => void }) => ({
    connect: async () => {
      events.lease({ leaseId: 'lease-1', cameraId: '1', streamProfileId: '11', state: 'READY' })
      throw new ApiRequestError('SIGNAL_REJECTED', '媒体连接被拒绝', {
        traceId: 'abcdef0123456789abcdef0123456789',
        timestamp: '2026-10-17T02:00:00Z',
      })
    },
    close: async () => {},
    stats: async () => null,
    recoverVideoError: async () => null,
  }),
}))
vi.mock('element-plus', async () => {
  const { defineComponent, h } = await import('vue')
  const component = (name: string) =>
    defineComponent({
      setup(_, { attrs, slots }) {
        return () => h(name, attrs, slots.default?.())
      },
    })
  return {
    vLoading: {},
    ElButton: component('button'),
    ElEmpty: component('empty'),
    ElInput: component('input'),
    ElMessage: { info: vi.fn() },
  }
})
vi.mock('../../src/features/preview/PreviewCameraTree.vue', async () => {
  const { defineComponent, h } = await import('vue')
  return {
    default: defineComponent({
      props: ['channels'],
      emits: ['channel'],
      setup(props, { emit }) {
        return () =>
          h(
            'div',
            {},
            props.channels.map((channel: PreviewChannel) =>
              h('button', { onClick: () => emit('channel', channel) }, `添加${channel.name}`),
            ),
          )
      },
    }),
  }
})
vi.mock('../../src/features/preview/PreviewCapacity.vue', () => ({
  default: { render: () => null },
}))
vi.mock('../../src/features/preview/PreviewLayoutPicker.vue', () => ({
  default: { render: () => null },
}))
vi.mock('../../src/features/preview/PreviewSlotMenu.vue', async () => {
  const { defineComponent, h } = await import('vue')
  return {
    default: defineComponent({
      emits: ['close', 'action'],
      setup(_, { attrs, emit }) {
        return () =>
          h('slot-menu', attrs, [
            h('button', { onClick: () => emit('close') }, '关闭操作菜单'),
            h('button', { onClick: () => emit('action', 'diagnostics') }, '菜单排查信息'),
          ])
      },
    }),
  }
})
vi.mock('../../src/features/preview/PreviewDiagnosticsDialog.vue', async () => {
  const { defineComponent, h } = await import('vue')
  return {
    default: defineComponent({
      props: ['failure'],
      setup(props) {
        return () => h('preview-diagnostics', {}, JSON.stringify(props.failure))
      },
    }),
  }
})

class FocusTarget {
  isConnected = true
  focus = vi.fn()
  closest() {
    return this
  }
  getBoundingClientRect() {
    return { right: 300, bottom: 150 }
  }
}
const channel = (cameraId: string): PreviewChannel => ({
  cameraId,
  name: `通道${cameraId}`,
  deviceGroupKey: 'd1',
  deviceName: '相机1',
  groupId: null,
  groupPath: null,
  lifecycle: 'ENABLED',
  profiles: [
    {
      streamProfileId: `${cameraId}1`,
      label: 'Main',
      usage: 'MAIN',
      enabled: true,
      configured: true,
      videoCodec: 'H264',
      width: null,
      height: null,
      frameRate: null,
      bitrateKbps: null,
    },
  ],
})
let app: App | null = null
const button = (root: Host, label: string) =>
  nodes(root).find((item) => item.type === 'button' && text(item) === label)!
async function mount() {
  const root = node('root')
  app = renderer.createApp(Center)
  app.mount(root)
  await vi.waitFor(() => expect(text(root)).toContain('已加载 1 台'))
  return root
}
async function add(root: Host) {
  await (button(root, '添加通道1').props.onClick as () => void)()
  await vi.waitFor(() => expect(text(root)).toContain('媒体连接被拒绝'))
}
beforeEach(() => {
  vi.clearAllMocks()
  vi.stubGlobal('HTMLElement', FocusTarget)
  vi.stubGlobal('HTMLVideoElement', class {})
  vi.stubGlobal('innerWidth', 1440)
  vi.stubGlobal('innerHeight', 900)
  vi.stubGlobal('document', { addEventListener: vi.fn(), removeEventListener: vi.fn() })
  api.catalog.mockResolvedValue({
    items: [channel('1'), channel('2')],
    page: 1,
    size: 100,
    total: 500,
  })
  api.groups.mockResolvedValue([])
})
afterEach(() => {
  app?.unmount()
  app = null
  vi.unstubAllGlobals()
})

describe('preview actions and diagnostics', () => {
  it('labels loaded device count separately from total channel count', async () => {
    const root = await mount()
    expect(text(root)).toContain('已加载 1 台')
    expect(text(root)).toContain('共 500 个通道')
    expect(nodes(root).filter((item) => item.type === 'article')).toHaveLength(4)
  })
  it('maximizes an occupied tile on double click and restores it from the visible control', async () => {
    const root = await mount()
    await add(root)
    let tile = nodes(root).find((item) => item.type === 'article')!
    await (tile.props.onDblclick as () => void)()
    await nextTick()
    tile = nodes(root).find((item) => item.type === 'article')!
    expect(tile.props.class).toContain('maximized')
    const exit = nodes(root).find((item) => item.props['aria-label'] === '退出大屏')!
    await (exit.props.onClick as (event: unknown) => void)({ stopPropagation: vi.fn() })
    await nextTick()
    tile = nodes(root).find((item) => item.type === 'article')!
    expect(tile.props.class).not.toContain('maximized')
  })
  it('opens the same action menu through the explicit button and preserves diagnostics', async () => {
    const root = await mount()
    await add(root)
    const more = nodes(root).find((item) => item.props['aria-label'] === '通道1 更多操作')!
    expect(more.props['aria-haspopup']).toBe('dialog')
    const trigger = new FocusTarget()
    await (more.props.onClick as (event: unknown) => void)({
      currentTarget: trigger,
      stopPropagation: vi.fn(),
    })
    await nextTick()
    expect(nodes(root).find((item) => item.type === 'slot-menu')?.props['camera-id']).toBe('1')
    await (button(root, '菜单排查信息').props.onClick as () => void)()
    await nextTick()
    const diagnostics = nodes(root).find((item) => item.type === 'preview-diagnostics')!
    expect(text(diagnostics)).toContain('abcdef0123456789abcdef0123456789')
    expect(text(diagnostics)).toContain('lease-1')
    expect(nodes(root).some((item) => item.type === 'slot-menu')).toBe(false)
  })
  it('supports Shift+F10 on a tile and returns focus when the menu closes', async () => {
    const root = await mount()
    await add(root)
    const tile = nodes(root).find((item) => item.type === 'article')!
    const target = new FocusTarget(),
      preventDefault = vi.fn()
    await (tile.props.onKeydown as (event: unknown) => void)({
      key: 'F10',
      shiftKey: true,
      target,
      currentTarget: target,
      preventDefault,
    })
    await nextTick()
    expect(preventDefault).toHaveBeenCalledOnce()
    expect(nodes(root).some((item) => item.type === 'slot-menu')).toBe(true)
    await (button(root, '关闭操作菜单').props.onClick as () => void)()
    await nextTick()
    expect(target.focus).toHaveBeenCalledOnce()
  })
})
