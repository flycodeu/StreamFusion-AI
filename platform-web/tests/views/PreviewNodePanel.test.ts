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
  getDirectZlmInstances: vi.fn(),
  createDirectZlmInstance: vi.fn(),
  saveDirectZlmInstance: vi.fn(),
  rotateDirectZlmSecret: vi.fn(),
  deleteDirectZlmInstance: vi.fn(),
  getOpsPreviewInstances: vi.fn(),
  importOpsPreviewInstance: vi.fn(),
  previewNodeAction: vi.fn(),
  savePreviewNode: vi.fn(),
  registerPreviewNode: vi.fn(),
  configurePreviewNode: vi.fn(),
  previewMaintenance: vi.fn(),
}))
vi.mock('../../src/api/preview-node/api', () => api)
vi.mock('../../src/api/media-runtime/api', () => ({
  getMediaRuntime: vi.fn().mockResolvedValue({
    selected: { zlmId: null, ffmpegId: null },
    candidates: [],
  }),
}))
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
      kind: 'MANAGED',
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
      zlmApiAddress: null,
      certificateSha256: null,
      mediaAddress: null,
      desiredConfigVersion: 'c',
      observation: null,
      protection: null,
      zlmLoad: null,
    },
  ],
  registrationOptions: [],
  directRegistrationAvailable: false,
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
async function mount(management = false) {
  const root = node('root')
  app = renderer.createApp(Panel, { management })
  app.mount(root)
  await settle()
  return root
}
function buttons(root: Host) {
  return nodes(root).filter((n) => n.type === 'ElButton')
}
beforeEach(() => {
  vi.clearAllMocks()
  sessionState.me = null
  api.getPreviewNodes.mockResolvedValue(value())
  api.getDirectZlmInstances.mockResolvedValue({ registrationAvailable: true, items: [] })
  api.getOpsPreviewInstances.mockResolvedValue({ available: false, reasonCode: null, items: [] })
})
afterEach(() => {
  app?.unmount()
  sessionState.me = null
  sessionState.epoch++
  vi.useRealTimers()
})
it('refreshes ZLM observations only while a direct instance is registered', async () => {
  vi.useFakeTimers()
  const root = await mount()
  expect(api.getPreviewNodes).toHaveBeenCalledOnce()
  await vi.advanceTimersByTimeAsync(45_000)
  expect(api.getPreviewNodes).toHaveBeenCalledOnce()
  expect(text(root)).not.toContain('网络线程负载')

  const configured = value()
  configured.nodes[0] = { ...configured.nodes[0]!, kind: 'ZLM_DIRECT' }
  api.getPreviewNodes.mockResolvedValue(configured)
  await (buttons(root).find((button) => text(button) === '检查节点')!.props.onClick as () => void)()
  await settle()
  const reads = api.getPreviewNodes.mock.calls.length
  await vi.advanceTimersByTimeAsync(15_000)
  expect(api.getPreviewNodes).toHaveBeenCalledTimes(reads + 1)
})
it('shows registered nodes immediately and keeps write controls hidden for a reader', async () => {
  const root = await mount()
  expect(api.getPreviewNodes).toHaveBeenCalledOnce()
  expect(text(root)).toContain('测试节点')
  expect(text(root)).toContain('1 台已登记')
  expect(buttons(root).map(text)).toContain('诊断 JSON')
  expect(buttons(root).map(text)).not.toContain('参与调度')
  expect(api.savePreviewNode).not.toHaveBeenCalled()
  expect(api.getOpsPreviewInstances).not.toHaveBeenCalled()
})
it('keeps the overview read-only for a super administrator', async () => {
  sessionState.me = administrator()
  const root = await mount()
  expect(buttons(root).map(text)).not.toContain('预算与优先级')
  expect(buttons(root).map(text)).not.toContain('参与调度')
  expect(text(root)).not.toContain('添加远程节点')
})
it('shows configured nodes without the setup checklist and refreshes after import', async () => {
  sessionState.me = administrator()
  const configured = value()
  configured.registrationOptions = [{ nodeId: 'b', name: '预览节点 B' }]
  api.getPreviewNodes.mockResolvedValue(configured)
  api.registerPreviewNode.mockResolvedValue(undefined)
  const root = await mount(true)
  expect(text(root)).toContain('预览节点')
  expect(text(root)).toContain('从 Ops 引入')
  expect(text(root)).toContain('预览节点 B')
  expect(text(root)).toContain('待导入')
  await (buttons(root).find((button) => text(button) === '导入节点')!.props.onClick as () => void)()
  await settle()
  expect(api.registerPreviewNode).toHaveBeenCalledWith('b')
  expect(api.getPreviewNodes).toHaveBeenCalledTimes(2)
})
it('shows a simple ZLM entry and existing direct instances without revealing credentials', async () => {
  sessionState.me = administrator()
  api.getDirectZlmInstances.mockResolvedValue({
    registrationAvailable: true,
    items: [
      {
        nodeId: 'zlm-a',
        name: 'ZLM A',
        apiHost: '10.0.0.10',
        apiPort: 8080,
        zlmVersion: 'v1',
        mediaAddress: '10.0.0.10',
        mode: 'LOCAL',
        version: 2,
        priority: 100,
        budget: { ...value().nodes[0]!.budget, maxTranscodes: 0 },
        usage: { ...value().nodes[0]!.usage, sources: 0, viewers: 0, transcodes: 0 },
        state: 'READY',
        reasonCode: null,
        observedAt: null,
      },
    ],
  })
  const root = await mount(true)
  expect(text(root)).toContain('直连 ZLM')
  expect(buttons(root).map(text)).toContain('接入 ZLM')
  expect(text(root)).toContain('ZLM HTTP：10.0.0.10:8080')
  expect(text(root)).toContain('ZLM 版本：v1')
  expect(text(root)).toContain('ZLM A')
  expect(text(root)).not.toContain('ZLM secret')
  expect(api.getDirectZlmInstances).toHaveBeenCalledOnce()
})
it('explains server-side credential storage failures without asking for manual setup', async () => {
  sessionState.me = administrator()
  api.getDirectZlmInstances.mockResolvedValue({ registrationAvailable: false, items: [] })
  const root = await mount(true)
  expect(buttons(root).find((button) => text(button) === '接入 ZLM')!.props.disabled).toBe(true)
  expect(text(root)).toContain('平台密钥存储不可用，请检查服务账号权限')
})
it('shows the direct ZLM API address in read-only node status without agent-only controls', async () => {
  const configured = value()
  configured.nodes[0] = {
    ...configured.nodes[0]!,
    nodeId: 'zlm-a',
    kind: 'ZLM_DIRECT',
    zlmApiAddress: 'http://10.0.0.10:8080',
    controlUri: null,
  }
  api.getPreviewNodes.mockResolvedValue(configured)
  const root = await mount()
  expect(text(root)).toContain('ZLM API 地址')
  expect(text(root)).toContain('http://10.0.0.10:8080')
  expect(text(root)).not.toContain('节点控制地址未登记')
  expect(buttons(root).map(text)).not.toContain('维护许可')
})
it('shows fresh ZLM network-thread load as an observation and unknown when absent', async () => {
  const configured = value()
  configured.nodes[0] = {
    ...configured.nodes[0]!,
    nodeId: 'zlm-a',
    kind: 'ZLM_DIRECT',
    zlmApiAddress: 'http://10.0.0.10:8080',
    zlmLoad: {
      networkThreadCount: 4,
      averageNetworkThreadLoadPercent: 23.5,
      peakNetworkThreadLoadPercent: 41.2,
      observedAt: new Date().toISOString(),
    },
  }
  api.getPreviewNodes.mockResolvedValue(configured)
  const root = await mount()
  expect(text(root)).toContain('网络线程负载')
  expect(text(root)).toContain('平均 23.5%')
  expect(text(root)).toContain('峰值 41.2%')
  expect(text(root)).toContain('4 个线程')
  expect(text(root)).toContain('不代表可接纳预览数')
  configured.nodes[0]!.zlmLoad = null
  await (buttons(root).find((button) => text(button) === '检查节点')!.props.onClick as () => void)()
  await settle()
  expect(text(root)).toContain('暂无数据')
})
it('offers direct instance configuration only when protected credentials are available', async () => {
  sessionState.me = administrator()
  const configured = value()
  api.getPreviewNodes.mockResolvedValue(configured)
  const root = await mount(true)
  const create = buttons(root).find((button) => text(button) === '手工登记')!
  expect(create.props.disabled).toBe(true)
  expect(text(root)).toContain('节点包需要预置机器令牌')
  configured.directRegistrationAvailable = true
  await (buttons(root).find((button) => text(button) === '检查节点')!.props.onClick as () => void)()
  await settle()
  expect(buttons(root).find((button) => text(button) === '手工登记')!.props.disabled).toBe(false)
  expect(text(root)).toContain('尚未连接 Ops 实例目录')
  expect(text(root)).toContain('接入节点包')
  expect(text(root)).toContain('手工登记节点包')
})
it('imports a deployed Ops instance by identity and release without asking for connection secrets', async () => {
  sessionState.me = administrator()
  api.getOpsPreviewInstances.mockResolvedValue({
    available: true,
    reasonCode: null,
    items: [
      {
        instanceId: 'ops-instance-a',
        name: '预览服务 A',
        targetName: '服务器 A',
        targetAddress: '10.0.0.10',
        installRoot: '/srv/preview-a',
        releaseId: 'release-2',
        nodeId: 'preview-a',
        controlUri: 'https://10.0.0.10:9443',
        mediaAddress: '10.0.0.10',
        maxSources: 64,
        maxViewers: 64,
        maxTranscodes: 64,
      },
    ],
  })
  api.importOpsPreviewInstance.mockResolvedValue(undefined)
  const root = await mount(true)
  expect(text(root)).toContain('服务器：服务器 A')
  expect(text(root)).toContain('地址：10.0.0.10')
  expect(text(root)).toContain('安装位置：/srv/preview-a')
  expect(text(root)).toContain('发布版本：release-2')
  await (buttons(root).find((button) => text(button) === '引入节点')!.props.onClick as () => void)()
  await settle()
  expect(api.importOpsPreviewInstance).toHaveBeenCalledWith('ops-instance-a', 'release-2')
  expect(api.getPreviewNodes).toHaveBeenCalledTimes(2)
})
it('omits an empty local Ops address instead of leaving a separator', async () => {
  sessionState.me = administrator()
  api.getOpsPreviewInstances.mockResolvedValue({
    available: true,
    reasonCode: null,
    items: [
      {
        instanceId: 'local-ops',
        name: '本机实例',
        targetName: '本机',
        targetAddress: '',
        installRoot: 'D:/preview',
        releaseId: 'release-local',
        nodeId: 'local-ops',
        controlUri: 'https://127.0.0.1:9443',
        mediaAddress: '127.0.0.1',
        maxSources: 64,
        maxViewers: 64,
        maxTranscodes: 64,
      },
    ],
  })
  const root = await mount(true)
  expect(text(root)).toContain('服务器：本机')
  expect(text(root)).toContain('安装位置：D:/preview')
  expect(text(root)).not.toContain('地址：')
})
it('shows an actionable Ops directory error without mislabeling it as an empty catalog', async () => {
  sessionState.me = administrator()
  api.getOpsPreviewInstances.mockResolvedValue({
    available: true,
    reasonCode: 'OPS_UNAVAILABLE',
    items: [],
  })
  const root = await mount(true)
  expect(text(root)).toContain('无法读取 Ops 实例目录')
  expect(text(root)).not.toContain('Ops 暂无可引入的预览实例')
})
it('does not leave stale Ops instances selectable after a failed refresh', async () => {
  sessionState.me = administrator()
  api.getOpsPreviewInstances.mockResolvedValueOnce({
    available: true,
    reasonCode: null,
    items: [
      {
        instanceId: 'old-instance',
        name: '旧实例',
        targetName: '旧服务器',
        targetAddress: '',
        installRoot: '/srv/old',
        releaseId: 'old-release',
        nodeId: 'old-node',
        controlUri: 'https://10.0.0.1:9443',
        mediaAddress: '10.0.0.1',
        maxSources: 64,
        maxViewers: 64,
        maxTranscodes: 64,
      },
    ],
  })
  const root = await mount(true)
  expect(buttons(root).map(text)).toContain('引入节点')
  api.getOpsPreviewInstances.mockRejectedValueOnce(new Error('Ops unavailable'))
  await (buttons(root).find((button) => text(button) === '刷新实例')!.props.onClick as () => void)()
  await settle()
  expect(buttons(root).map(text)).not.toContain('引入节点')
  expect(api.importOpsPreviewInstance).not.toHaveBeenCalled()
})
it('labels an existing connection as registered and blocks re-import while serving', async () => {
  sessionState.me = administrator()
  const configured = value()
  configured.nodes[0]!.mode = 'REMOTE'
  configured.registrationOptions = [{ nodeId: 'a', name: '测试节点' }]
  api.getPreviewNodes.mockResolvedValue(configured)
  const root = await mount(true)
  expect(text(root)).toContain('已登记')
  expect(buttons(root).find((button) => text(button) === '重新导入')!.props.disabled).toBe(true)
})
it('sends the observed version on explicit mode selection', async () => {
  sessionState.me = administrator()
  const root = await mount(true)
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
  const root = await mount(true)
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
  const root = await mount(true)
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
