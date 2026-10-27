// @vitest-environment vue-renderer
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { nextTick } from 'vue'
import type { App } from 'vue'
import type { PreviewChannel } from '../../src/api/preview/types'
import { ApiRequestError } from '../../src/lib/http/error'
import { node, nodes, renderer, text } from '../support/renderer'
import type { Host } from '../support/renderer'
import Center from '../../src/views/preview/Center.vue'
import { sessionState } from '../../src/session/state'
import type { AuthUser } from '../../src/api/auth/types'
import type { SavedView } from '../../src/features/preview/saved-views'

const api = vi.hoisted(() => ({
  catalog: vi.fn(),
  groups: vi.fn(),
  camera: vi.fn(),
  connect: vi.fn(),
  close: vi.fn(),
  query: {} as Record<string, string>,
}))
vi.mock('vue-router', () => ({
  useRoute: () => ({ query: api.query }),
  useRouter: () => ({ push: vi.fn(), back: vi.fn() }),
}))
vi.mock('../../src/api/preview/api', () => ({ getPreviewCatalog: api.catalog }))
vi.mock('../../src/api/camera-groups/api', () => ({ getGroupTree: api.groups }))
vi.mock('../../src/api/camera/api', () => ({ getCamera: api.camera }))
vi.mock('../../src/features/preview/player', () => ({
  createPreviewPlayer: (events: { lease: (lease: unknown) => void }) => ({
    connect: async (input: unknown) => {
      api.connect(input)
      events.lease({ leaseId: 'lease-1', cameraId: '1', streamProfileId: '11', state: 'READY' })
      throw new ApiRequestError('SIGNAL_REJECTED', '媒体连接被拒绝', {
        traceId: 'abcdef0123456789abcdef0123456789',
        timestamp: '2026-10-17T02:00:00Z',
      })
    },
    close: api.close,
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
const savedView: SavedView = {
  id: 'local-1',
  name: '测试收藏',
  layout: '36',
  updatedAt: '2026-10-18T00:00:00Z',
  slots: [
    { index: 0, cameraId: '1', profileId: '11' },
    { index: 35, cameraId: '2', profileId: '21' },
  ],
}
vi.mock('../../src/features/preview/PreviewSavedViews.vue', async () => {
  const { defineComponent, h } = await import('vue')
  return {
    default: defineComponent({
      props: ['busy', 'current'],
      emits: ['open'],
      setup(props, { emit }) {
        return () =>
          h('saved-views', { busy: props.busy, current: props.current }, [
            h('button', { onClick: () => emit('open', savedView) }, '打开收藏测试'),
          ])
      },
    }),
  }
})
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
  api.query = {}
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
  api.close.mockResolvedValue(undefined)
  api.camera.mockImplementation(async (id: string) => ({
    ...channel(id),
    deviceId: '1',
    profiles: channel(id).profiles.map((profile) => ({
      ...profile,
      usageHint: profile.usage,
      locatorSummary: { configured: true },
    })),
  }))
  sessionState.me = { user: { id: '1' } } as AuthUser
})
afterEach(() => {
  app?.unmount()
  app = null
  vi.unstubAllGlobals()
  sessionState.me = null
})

describe('preview actions and diagnostics', () => {
  it('preserves the catalog and preview when the same account identity is refreshed', async () => {
    const root = await mount()
    await add(root)
    const closeCount = api.close.mock.calls.length

    sessionState.me = {
      ...sessionState.me!,
      user: { ...sessionState.me!.user, nickname: '新昵称' },
    }
    await nextTick()
    await nextTick()

    expect(text(root)).toContain('添加通道1')
    expect(text(root)).toContain('媒体连接被拒绝')
    expect(api.close).toHaveBeenCalledTimes(closeCount)
  })
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
  it('does not auto-restore and applies explicit saved positions through a single connection per profile', async () => {
    const root = await mount()
    expect(api.camera).not.toHaveBeenCalled()
    expect(api.connect).not.toHaveBeenCalled()
    await (button(root, '打开收藏测试').props.onClick as () => void)()
    await vi.waitFor(() => expect(api.connect).toHaveBeenCalledTimes(2))
    expect(api.camera).toHaveBeenCalledTimes(2)
    expect(api.connect).toHaveBeenNthCalledWith(1, {
      cameraId: '1',
      profileId: '11',
      usage: 'MAIN',
    })
    expect(api.connect).toHaveBeenNthCalledWith(2, {
      cameraId: '2',
      profileId: '21',
      usage: 'MAIN',
    })
    const tiles = nodes(root).filter((item) => item.type === 'article')
    expect(tiles).toHaveLength(36)
    expect(text(tiles[0]!)).toContain('通道1')
    expect(text(tiles[35]!)).toContain('通道2')
    expect(text(tiles[1]!)).not.toContain('通道')
  })
  it('stops before lookup when old resources cannot be released and permits retry', async () => {
    const root = await mount()
    await add(root)
    api.close.mockRejectedValueOnce(new Error('unconfirmed'))
    await (button(root, '打开收藏测试').props.onClick as () => void)()
    await vi.waitFor(() =>
      expect(nodes(root).find((item) => item.type === 'saved-views')?.props.busy).toBe(false),
    )
    expect(api.camera).not.toHaveBeenCalled()
    expect(api.connect).toHaveBeenCalledOnce()
    expect(text(root)).toContain('释放未确认')
    await (button(root, '打开收藏测试').props.onClick as () => void)()
    await vi.waitFor(() => expect(api.connect).toHaveBeenCalledTimes(3))
  })
  it('keeps unavailable profiles and unauthorized cameras in place without silently switching streams', async () => {
    api.camera.mockImplementation(async (id: string) => {
      if (id === '2') throw new ApiRequestError('FORBIDDEN', '权限不足')
      return { ...channel(id), deviceId: '1', profiles: [] }
    })
    const root = await mount()
    await (button(root, '打开收藏测试').props.onClick as () => void)()
    await vi.waitFor(() =>
      expect(nodes(root).filter((item) => item.type === 'article')).toHaveLength(36),
    )
    expect(api.connect).not.toHaveBeenCalled()
    const tiles = nodes(root).filter((item) => item.type === 'article')
    expect(text(tiles[0]!)).toContain('重新检查')
    expect(text(tiles[35]!)).toContain('收藏相机暂不可用')
    expect(nodes(tiles[0]!).some((item) => item.type === 'button' && text(item) === '重连')).toBe(
      false,
    )
  })
  it('rejects duplicate opens and discards late saved-camera results on identity change', async () => {
    const pending: (() => void)[] = []
    api.camera.mockImplementation(
      (id: string) =>
        new Promise((resolve) =>
          pending.push(() => resolve({ ...channel(id), deviceId: '1', profiles: [] })),
        ),
    )
    const root = await mount()
    const open = button(root, '打开收藏测试').props.onClick as () => void
    open()
    open()
    await vi.waitFor(() => expect(api.camera).toHaveBeenCalledTimes(2))
    sessionState.epoch++
    pending.forEach((finish) => finish())
    await nextTick()
    await nextTick()
    expect(api.connect).not.toHaveBeenCalled()
    expect(text(root)).not.toContain('收藏相机暂不可用')
    expect(nodes(root).filter((item) => item.type === 'article')).toHaveLength(4)
  })
  it('rechecks only the failed saved window and retains its exact profile', async () => {
    api.camera.mockRejectedValue(new ApiRequestError('REQUEST_TIMEOUT', '查询超时'))
    const root = await mount()
    await (button(root, '打开收藏测试').props.onClick as () => void)()
    await vi.waitFor(() =>
      expect(nodes(root).filter((item) => item.type === 'article')).toHaveLength(36),
    )
    expect(api.connect).not.toHaveBeenCalled()
    api.camera.mockImplementation(async (id: string) => ({
      ...channel(id),
      deviceId: '1',
      profiles: channel(id).profiles.map((profile) => ({
        ...profile,
        usageHint: profile.usage,
        locatorSummary: { configured: true },
      })),
    }))
    const tile = nodes(root).filter((item) => item.type === 'article')[35]!
    await (button(tile, '重新检查').props.onClick as (event: unknown) => void)({
      stopPropagation: vi.fn(),
    })
    await vi.waitFor(() => expect(api.connect).toHaveBeenCalledOnce())
    expect(api.camera).toHaveBeenCalledTimes(3)
    expect(api.connect).toHaveBeenCalledWith({ cameraId: '2', profileId: '21', usage: 'MAIN' })
  })
  it('does not let a late device-entry catalog replace an explicitly opened saved workspace', async () => {
    api.query = { mode: 'device', device: 'd1' }
    let deliver!: (result: unknown) => void
    api.catalog.mockReturnValueOnce(
      new Promise((resolve) => {
        deliver = resolve
      }),
    )
    const root = node('root')
    app = renderer.createApp(Center)
    app.mount(root)
    await nextTick()
    expect(api.connect).not.toHaveBeenCalled()
    await (button(root, '打开收藏测试').props.onClick as () => void)()
    await vi.waitFor(() => expect(api.connect).toHaveBeenCalledTimes(2))
    deliver({ items: [channel('3')], page: 1, size: 100, total: 1 })
    await nextTick()
    await nextTick()
    const tiles = nodes(root).filter((item) => item.type === 'article')
    expect(tiles).toHaveLength(36)
    expect(text(tiles[35]!)).toContain('通道2')
    expect(api.connect).toHaveBeenCalledTimes(2)
  })
  it('shows unavailable saved windows even when a device-entry catalog has no visible channels', async () => {
    api.query = { mode: 'device', device: 'd1' }
    api.catalog.mockResolvedValue({ items: [], page: 1, size: 100, total: 0 })
    api.camera.mockRejectedValue(new ApiRequestError('FORBIDDEN', '权限不足'))
    const root = node('root')
    app = renderer.createApp(Center)
    app.mount(root)
    await nextTick()
    await nextTick()
    await (button(root, '打开收藏测试').props.onClick as () => void)()
    await vi.waitFor(() =>
      expect(nodes(root).filter((item) => item.type === 'article')).toHaveLength(36),
    )
    const tiles = nodes(root).filter((item) => item.type === 'article')
    expect(text(tiles[0]!)).toContain('收藏相机暂不可用')
    expect(text(tiles[35]!)).toContain('重新检查')
    expect(api.connect).not.toHaveBeenCalled()
  })
})
