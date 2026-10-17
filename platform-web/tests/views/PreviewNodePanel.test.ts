// @vitest-environment vue-renderer
import { afterEach, beforeEach, expect, it, vi } from 'vitest'
import { nextTick } from 'vue'
import type { App } from 'vue'
import { node, nodes, renderer, text } from '../support/renderer'
import type { Host } from '../support/renderer'
import Panel from '../../src/features/preview/PreviewNodePanel.vue'
import { sessionState } from '../../src/session/state'
import type { PreviewCluster } from '../../src/api/preview-node/api'
const api = vi.hoisted(() => ({
  getPreviewNodes: vi.fn(),
  previewNodeAction: vi.fn(),
  savePreviewNode: vi.fn(),
  registerPreviewNode: vi.fn(),
  previewMaintenance: vi.fn(),
}))
vi.mock('../../src/api/preview-node/api', () => api)
vi.mock('vue-router', async () => {
  const { defineComponent, h } = await import('vue')
  return {
    RouterLink: defineComponent({
      setup:
        (_, { slots }) =>
        () =>
          h('a', slots.default?.()),
    }),
  }
})
vi.mock('element-plus', async () => {
  const { defineComponent, h } = await import('vue')
  return Object.fromEntries(
    ['ElButton', 'ElDialog', 'ElTag'].map((name) => [
      name,
      defineComponent({
        inheritAttrs: false,
        setup:
          (_, { attrs, slots }) =>
          () =>
            h(name, attrs, slots.default?.()),
      }),
    ]),
  )
})
let app: App | null = null
const value = (): PreviewCluster => ({
  nodes: [
    {
      nodeId: 'a',
      name: '测试节点',
      mode: 'LOCAL',
      version: 2,
      priority: 100,
      budget: {
        maxSources: 64,
        maxViewers: 64,
        maxTranscodes: 64,
        maxStarting: 4,
        ingressKbps: 655360,
        egressKbps: 655360,
        sourceUpperKbps: 8192,
        outputUpperKbps: 8192,
      },
      usage: {
        sources: 1,
        viewers: 2,
        transcodes: 1,
        starting: 0,
        ingressKbps: 10240,
        egressKbps: 20480,
      },
      state: 'READY',
      reasonCode: null,
      observedAt: '2026-10-17T05:00:00Z',
      controlUri: null,
      mediaAddress: null,
      desiredConfigVersion: 'c',
      observation: null,
      protection: null,
    },
  ],
  registrationOptions: [],
})
function administrator(): NonNullable<typeof sessionState.me> {
  return {
    isSuperAdmin: true,
    modules: ['server'],
    roles: [],
    routes: [],
    user: {
      id: '1',
      username: 'admin',
      nickname: null,
      avatarKey: null,
      phone: null,
      email: null,
      gender: 0,
      status: 1,
      mustChangePassword: false,
      version: '1',
    },
  }
}
const settle = async () => {
  for (let i = 0; i < 8; i++) await nextTick()
}
async function mount() {
  const root = node('root')
  app = renderer.createApp(Panel)
  app.mount(root)
  await settle()
  return root
}
async function expand(root: Host) {
  const panel = nodes(root).find((n) => n.type === 'details')!
  ;(panel.props.onToggle as (event: unknown) => void)({ target: { open: true } })
  await settle()
}
function buttons(root: Host) {
  return nodes(root).filter((n) => n.type === 'ElButton')
}
beforeEach(() => {
  vi.clearAllMocks()
  sessionState.me = null
  api.getPreviewNodes.mockResolvedValue(value())
})
afterEach(() => {
  app?.unmount()
  sessionState.me = null
  sessionState.epoch++
})
it('loads only on demand and keeps write controls hidden for a reader', async () => {
  const root = await mount()
  expect(api.getPreviewNodes).not.toHaveBeenCalled()
  await expand(root)
  expect(text(root)).toContain('测试节点')
  expect(buttons(root).map(text)).toContain('诊断 JSON')
  expect(buttons(root).map(text)).not.toContain('参与调度')
  expect(api.savePreviewNode).not.toHaveBeenCalled()
})
it('sends the observed version on explicit mode selection', async () => {
  sessionState.me = administrator()
  const root = await mount()
  await expand(root)
  api.savePreviewNode.mockResolvedValue(undefined)
  const updated = value()
  updated.nodes[0]!.mode = 'REMOTE'
  updated.nodes[0]!.version = 3
  api.getPreviewNodes.mockResolvedValue(updated)
  await (buttons(root).find((n) => text(n) === '参与调度')!.props.onClick as () => Promise<void>)()
  await settle()
  expect(api.savePreviewNode).toHaveBeenCalledWith(
    expect.objectContaining({ nodeId: 'a', mode: 'REMOTE', version: 2 }),
  )
  expect(buttons(root).map(text)).toContain('停止接新预览')
})
it('does not apply a response belonging to an earlier signed-in account', async () => {
  let finish!: (v: PreviewCluster) => void
  api.getPreviewNodes.mockReturnValue(
    new Promise((resolve) => {
      finish = resolve
    }),
  )
  const root = await mount()
  await expand(root)
  sessionState.epoch++
  finish(value())
  await settle()
  expect(text(root)).not.toContain('测试节点')
})
it('shows retained budgets while draining without offering migration', async () => {
  sessionState.me = administrator()
  const draining = value()
  draining.nodes[0]!.mode = 'DRAINING'
  api.getPreviewNodes.mockResolvedValue(draining)
  const root = await mount()
  await expand(root)
  expect(text(root)).toContain('排空中')
  expect(text(root)).toContain('2 / 64')
  expect(buttons(root).map(text)).not.toContain('切回本机')
})

it('requires draining for maintenance and prevents releasing a claimed Ops job', async () => {
  sessionState.me = administrator()
  const draining = value()
  draining.nodes[0]!.mode = 'DRAINING'
  api.getPreviewNodes.mockResolvedValue(draining)
  api.previewMaintenance.mockResolvedValue({
    state: 'CLAIMED',
    nodeId: 'a',
    permitId: 'test-permit',
    jobId: 'ops-job',
    expiresAt: Date.now() + 60000,
  })
  const root = await mount()
  await expand(root)
  ;(buttons(root).find((n) => text(n) === '维护许可')!.props.onClick as () => void)()
  await settle()
  expect(api.previewMaintenance).toHaveBeenCalledWith(
    expect.objectContaining({ nodeId: 'a', version: 2 }),
    undefined,
    undefined,
  )
  expect(text(root)).toContain('Ops 执行中')
  expect(buttons(root).find((n) => text(n) === '解除维护')!.props.disabled).toBe(true)
  expect(api.savePreviewNode).not.toHaveBeenCalled()
})
