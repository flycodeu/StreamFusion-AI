// @vitest-environment vue-renderer
import { afterEach, beforeEach, expect, it, vi } from 'vitest'
import { nextTick } from 'vue'
import type { App } from 'vue'
import { node, nodes, renderer, text } from '../support/renderer'
import type { Host } from '../support/renderer'
import Panel from '../../src/features/preview/PreviewNodePanel.vue'
import { sessionState } from '../../src/session/state'
import type { DirectZlmInstances, PreviewCluster } from '../../src/api/preview-node/api'
import { ApiRequestError } from '../../src/lib/http/error'
const api = vi.hoisted(() => ({
  getPreviewNodes: vi.fn(),
  getDirectZlmInstances: vi.fn(),
  createDirectZlmInstance: vi.fn(),
  saveDirectZlmInstance: vi.fn(),
  rotateDirectZlmSecret: vi.fn(),
  deleteDirectZlmInstance: vi.fn(),
  getOpsPreviewInstances: vi.fn(),
  importOpsPreviewInstance: vi.fn(),
  getOpsZlmInstances: vi.fn().mockResolvedValue({ available: false, reasonCode: null, items: [] }),
  importOpsZlmInstance: vi.fn(),
  synchronizeOpsNode: vi.fn(),
  previewNodeAction: vi.fn(),
  savePreviewNode: vi.fn(),
  registerPreviewNode: vi.fn(),
  configurePreviewNode: vi.fn(),
  previewMaintenance: vi.fn(),
  getMediaRuntime: vi.fn(),
}))
vi.mock('../../src/api/preview-node/api', () => api)
vi.mock('vue', async () => {
  const actual = await vi.importActual<typeof import('vue')>('vue')
  return { ...actual, vModelText: {} }
})
vi.mock('../../src/api/media-runtime/api', () => ({
  getMediaRuntime: api.getMediaRuntime,
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
      opsSource: null,
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
function directListing(): DirectZlmInstances {
  return {
    registrationAvailable: true,
    items: [
      {
        ...value().nodes[0]!,
        nodeId: 'direct-a',
        name: '直连实例',
        apiHost: '10.0.0.1',
        apiPort: 8080,
        rtcPort: null,
        rtspPort: null,
        portsObservedAt: null,
        zlmVersion: null,
        mediaAddress: '10.0.0.1',
        installPath: '/srv/zlm',
        budget: { ...value().nodes[0]!.budget, maxTranscodes: 0 },
        usage: {
          sources: 0,
          viewers: 0,
          transcodes: 0,
          starting: 0,
          ingressKbps: 0,
          egressKbps: 0,
        },
      },
    ],
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
function dialog(root: Host, title: string) {
  return nodes(root).find((item) => item.type === 'ElDialog' && item.props.title === title)!
}
function click(root: Host, label: string) {
  return (buttons(root).find((item) => text(item) === label)!.props.onClick as () => void)()
}
function submit(subject: Host) {
  const form = nodes(subject).find((item) => item.type === 'form')!
  ;(form.props.onSubmit as (event: { preventDefault(): void }) => void)({ preventDefault() {} })
}
beforeEach(() => {
  vi.clearAllMocks()
  sessionState.me = null
  api.getPreviewNodes.mockResolvedValue(value())
  api.getDirectZlmInstances.mockResolvedValue({ registrationAvailable: true, items: [] })
  api.getOpsPreviewInstances.mockResolvedValue({ available: false, reasonCode: null, items: [] })
  api.getOpsZlmInstances.mockResolvedValue({ available: false, reasonCode: null, items: [] })
  api.getMediaRuntime.mockResolvedValue({
    selected: { zlmId: null, ffmpegId: null },
    candidates: [],
  })
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
  expect(
    nodes(root)
      .filter((item) => item.type === 'progress')
      .slice(0, 4)
      .map((item) => [item.props['aria-label'], item.props.value, item.props.max]),
  ).toEqual([
    ['源流预算使用率', 1, 64],
    ['观看预算使用率', 2, 64],
    ['转码预算使用率', 1, 64],
    ['启动中预算使用率', 0, 4],
  ])
  expect(buttons(root).map(text)).toContain('诊断 JSON')
  expect(buttons(root).map(text)).not.toContain('参与调度')
  expect(api.savePreviewNode).not.toHaveBeenCalled()
  expect(api.getOpsPreviewInstances).not.toHaveBeenCalled()
})
it('offers a read-only retry after the initial node load fails', async () => {
  api.getPreviewNodes.mockRejectedValueOnce(new ApiRequestError('NETWORK_ERROR', '节点读取失败'))
  const root = await mount()
  expect(text(root)).toContain('节点读取失败')
  expect(text(root)).not.toContain('测试节点')
  click(root, '重新加载')
  await settle()
  expect(api.getPreviewNodes).toHaveBeenCalledTimes(2)
  expect(api.previewNodeAction).not.toHaveBeenCalled()
  expect(text(root)).toContain('测试节点')
  expect(text(root)).not.toContain('节点读取失败')
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
        rtcPort: 8000,
        rtspPort: 8554,
        portsObservedAt: '2026-10-18T12:00:00Z',
        zlmVersion: 'v1',
        mediaAddress: '10.0.0.10',
        installPath: '/srv/zlm',
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
  expect(buttons(root).map(text)).toContain('+ 接入 ZLM')
  expect(text(root)).toContain('ZLM HTTP：10.0.0.10:8080')
  expect(text(root)).toContain('RTC 端口：8000')
  expect(text(root)).toContain('RTSP 端口：8554')
  expect(text(root)).toContain('端口最近核对：')
  expect(text(root)).toContain('安装位置：/srv/zlm')
  expect(text(root)).toContain('ZLM 版本：v1')
  expect(text(root)).toContain('ZLM A')
  expect(text(root)).not.toContain('ZLM secret')
  expect(api.getDirectZlmInstances).toHaveBeenCalledOnce()
})
it.each(['success', 'write-failure', 'readback-failure'] as const)(
  'refreshes the Ops catalog only after the import is applied (%s)',
  async (outcome) => {
    sessionState.me = administrator()
    api.getOpsZlmInstances.mockResolvedValue({
      available: true,
      reasonCode: null,
      items: [
        {
          instanceId: 'ops-zlm-a',
          controllerId: 'controller-a',
          targetId: 'target-a',
          name: 'Ops ZLM A',
          targetName: '服务器 A',
          targetAddress: '10.0.0.20',
          installRoot: '/srv/media',
          installPath: '/srv/media/instances/ops-zlm-a/releases/release-a',
          releaseId: 'release-a',
          apiUrl: 'http://10.0.0.20:8080',
          mediaAddress: '10.0.0.20',
          apiPort: 8080,
          rtcPort: 8000,
          maxSources: 64,
          maxViewers: 64,
          maxTranscodes: 0,
        },
      ],
    })
    if (outcome === 'write-failure')
      api.importOpsZlmInstance.mockRejectedValueOnce(
        new ApiRequestError('ZLM_UNAVAILABLE', '引入失败，请检查 ZLM'),
      )
    else api.importOpsZlmInstance.mockResolvedValueOnce(undefined)
    const root = await mount(true)
    expect(text(root)).toContain('未引入平台')
    expect(text(root)).toContain('服务器地址：10.0.0.20')
    expect(text(root)).toContain('HTTP 端口：8080')
    expect(text(root)).toContain('RTC 端口：8000')
    expect(text(root)).toContain('Ops 目标根目录：/srv/media')
    expect(text(root)).toContain('ZLM 发布目录：/srv/media/instances/ops-zlm-a/releases/release-a')
    expect(api.importOpsZlmInstance).not.toHaveBeenCalled()
    if (outcome === 'readback-failure')
      api.getPreviewNodes.mockRejectedValueOnce(
        new ApiRequestError('NETWORK_ERROR', '节点读回失败'),
      )
    await (
      buttons(root).find((button) => text(button) === '引入平台')!.props.onClick as () => void
    )()
    await settle()
    expect(api.importOpsZlmInstance).toHaveBeenCalledWith('ops-zlm-a', 'release-a')
    expect(api.importOpsZlmInstance).toHaveBeenCalledOnce()
    expect(api.getDirectZlmInstances).toHaveBeenCalledTimes(outcome === 'write-failure' ? 1 : 2)
    expect(api.getOpsZlmInstances).toHaveBeenCalledTimes(outcome === 'write-failure' ? 1 : 2)
    if (outcome === 'write-failure') expect(text(root)).toContain('引入失败，请检查 ZLM')
    if (outcome === 'readback-failure') expect(text(root)).toContain('节点读回失败')
  },
)
it('invalidates the old Ops ZLM catalog after a failed refresh and recovers on retry', async () => {
  sessionState.me = administrator()
  const catalog = {
    available: true,
    reasonCode: null,
    items: [
      {
        instanceId: 'ops-zlm-a',
        controllerId: 'controller-a',
        targetId: 'target-a',
        name: '旧 ZLM 条目',
        targetName: '服务器 A',
        targetAddress: '10.0.0.20',
        installRoot: '/srv/zlm',
        installPath: '/srv/zlm/release-a',
        releaseId: 'release-a',
        apiUrl: 'http://10.0.0.20:8080',
        mediaAddress: '10.0.0.20',
        apiPort: 8080,
        rtcPort: 8000,
        maxSources: 64,
        maxViewers: 64,
        maxTranscodes: 0,
      },
    ],
  }
  api.getOpsZlmInstances.mockResolvedValue(catalog)
  const root = await mount(true)
  const directory = nodes(root).find((item) => item.props.id === 'preview-ops-zlm-panel')!
  expect(buttons(directory).map(text)).toContain('引入平台')
  api.getOpsZlmInstances.mockRejectedValueOnce(new ApiRequestError('NETWORK_ERROR', '目录读取失败'))
  click(directory, '刷新')
  await settle()
  expect(text(root)).toContain('目录读取失败')
  expect(text(root)).not.toContain('旧 ZLM 条目')
  expect(buttons(directory).map(text)).not.toContain('引入平台')
  expect(api.importOpsZlmInstance).not.toHaveBeenCalled()
  click(directory, '刷新')
  await settle()
  expect(text(root)).not.toContain('目录读取失败')
  expect(buttons(directory).map(text)).toContain('引入平台')
})
it('does not mistake a manually registered API address for an imported Ops instance', async () => {
  sessionState.me = administrator()
  api.getOpsZlmInstances.mockResolvedValue({
    available: true,
    reasonCode: null,
    items: [
      {
        instanceId: 'ops-zlm-a',
        controllerId: 'controller-a',
        targetId: 'target-a',
        name: 'Ops ZLM A',
        targetName: '服务器 A',
        targetAddress: '10.0.0.20',
        installRoot: '/srv/media',
        installPath: '/srv/media/instances/ops-zlm-a/releases/release-a',
        releaseId: 'release-a',
        apiUrl: 'http://10.0.0.20:8080',
        mediaAddress: '10.0.0.20',
        apiPort: 8080,
        rtcPort: 8000,
        maxSources: 64,
        maxViewers: 64,
        maxTranscodes: 0,
      },
    ],
  })
  api.getDirectZlmInstances.mockResolvedValue({
    registrationAvailable: true,
    items: [
      {
        nodeId: 'zlm-manual',
        name: '手工节点',
        apiHost: '10.0.0.20',
        apiPort: 8080,
        rtcPort: null,
        rtspPort: null,
        portsObservedAt: null,
        zlmVersion: null,
        mediaAddress: '10.0.0.20',
        installPath: null,
        mode: 'LOCAL',
        version: 0,
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
  expect(text(root)).toContain('地址已登记')
  expect(text(root)).not.toContain('已引入平台')
  expect(buttons(root).find((button) => text(button) === '引入平台')!.props.disabled).toBe(true)
})
it('states that an absent Ops directory is optional for manual ZLM registration', async () => {
  sessionState.me = administrator()
  const root = await mount(true)
  expect(text(root)).toContain('Ops 目录未配置；可继续手工接入')
  expect(buttons(root).find((button) => text(button) === '+ 接入 ZLM')!.props.disabled).toBe(false)
  expect(api.importOpsZlmInstance).not.toHaveBeenCalled()
})
it('explains server-side credential storage failures without asking for manual setup', async () => {
  sessionState.me = administrator()
  api.getDirectZlmInstances.mockResolvedValue({ registrationAvailable: false, items: [] })
  const root = await mount(true)
  expect(buttons(root).find((button) => text(button) === '+ 接入 ZLM')!.props.disabled).toBe(true)
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
  expect(text(root)).toContain('Ops 目录未配置（可选）')
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
it('releases the local busy state when an in-flight operation loses account scope', async () => {
  sessionState.me = administrator()
  let finish!: () => void
  api.savePreviewNode.mockReturnValueOnce(
    new Promise<void>((resolve) => {
      finish = resolve
    }),
  )
  const root = await mount(true)
  click(root, '参与调度')
  await settle()
  expect(buttons(root).find((item) => text(item) === '参与调度')?.props.disabled).toBe(true)
  sessionState.epoch++
  finish()
  await settle()
  expect(text(root)).not.toContain('测试节点')
  expect(api.getPreviewNodes).toHaveBeenCalledOnce()
})
it('clears the node snapshot and management drafts when the account epoch changes', async () => {
  sessionState.me = administrator()
  const root = await mount(true)
  click(root, '预算与优先级')
  await settle()
  expect(text(root)).toContain('测试节点')
  sessionState.epoch++
  await settle()
  expect(text(root)).not.toContain('测试节点')
  expect(
    nodes(root).filter((item) => item.type === 'ElDialog' && item.props['model-value']),
  ).toHaveLength(0)
})
it('reloads the node snapshot after a new account signs in', async () => {
  sessionState.me = administrator()
  const root = await mount(true)
  expect(api.getPreviewNodes).toHaveBeenCalledOnce()
  sessionState.epoch++
  sessionState.me = null
  await settle()
  expect(text(root)).not.toContain('测试节点')
  sessionState.me = administrator()
  await settle()
  expect(text(root)).toContain('测试节点')
  expect(api.getPreviewNodes).toHaveBeenCalledTimes(2)
  expect(api.getMediaRuntime).toHaveBeenCalledTimes(2)
})
it('preserves the node snapshot and draft after a same-account identity refresh', async () => {
  sessionState.me = administrator()
  const root = await mount(true)
  click(root, '预算与优先级')
  await settle()
  sessionState.me = administrator()
  await settle()
  expect(text(root)).toContain('测试节点')
  expect(dialog(root, '节点预算与优先级').props['model-value']).toBe(true)
  expect(api.getPreviewNodes).toHaveBeenCalledOnce()
  expect(api.getDirectZlmInstances).toHaveBeenCalledOnce()
})
it('clears the diagnostic dialog when the account expires', async () => {
  sessionState.me = administrator()
  const root = await mount(true)
  click(root, '诊断 JSON')
  await settle()
  sessionState.epoch++
  sessionState.me = null
  await settle()
  expect(
    nodes(root).filter((item) => item.type === 'ElDialog' && item.props['model-value']),
  ).toHaveLength(0)
  expect(text(root)).not.toContain('测试节点')
})
it('restores management catalogs when accounts switch without an intermediate render', async () => {
  sessionState.me = administrator()
  const root = await mount(true)
  sessionState.epoch++
  sessionState.me = null
  const nextAccount = administrator()
  nextAccount.user.id = '2'
  sessionState.me = nextAccount
  await settle()
  expect(text(root)).toContain('测试节点')
  expect(api.getPreviewNodes).toHaveBeenCalledTimes(2)
  expect(api.getMediaRuntime).toHaveBeenCalledTimes(2)
  expect(api.getDirectZlmInstances).toHaveBeenCalledTimes(2)
  expect(api.getOpsPreviewInstances).toHaveBeenCalledTimes(2)
  expect(api.getOpsZlmInstances).toHaveBeenCalledTimes(2)
})
it('keeps a new account operation busy when the previous account request finishes', async () => {
  sessionState.me = administrator()
  const root = await mount(true)
  let finishPrevious!: () => void
  let finishCurrent!: () => void
  api.savePreviewNode
    .mockReturnValueOnce(
      new Promise<void>((resolve) => {
        finishPrevious = resolve
      }),
    )
    .mockReturnValueOnce(
      new Promise<void>((resolve) => {
        finishCurrent = resolve
      }),
    )
  click(root, '参与调度')
  sessionState.epoch++
  sessionState.me = null
  sessionState.me = administrator()
  await settle()
  click(root, '参与调度')
  await settle()
  finishPrevious()
  await settle()
  expect(buttons(root).find((item) => text(item) === '参与调度')?.props.disabled).toBe(true)
  click(root, '参与调度')
  expect(api.savePreviewNode).toHaveBeenCalledTimes(2)
  finishCurrent()
  await settle()
  expect(buttons(root).find((item) => text(item) === '参与调度')?.props.disabled).toBe(false)
})
it.each(['getDirectZlmInstances', 'getOpsPreviewInstances', 'getOpsZlmInstances'] as const)(
  'ignores cancelled %s errors after management is disabled and re-enabled',
  async (method) => {
    sessionState.me = administrator()
    let reject!: (cause: unknown) => void
    api[method].mockReturnValueOnce(
      new Promise((_, fail) => {
        reject = fail
      }),
    )
    const root = await mount(true)
    sessionState.me!.isSuperAdmin = false
    await settle()
    sessionState.me!.isSuperAdmin = true
    await settle()
    expect(api[method]).toHaveBeenCalledTimes(2)
    reject(new ApiRequestError('DEPENDENCY_UNAVAILABLE', '旧目录请求失败'))
    await settle()
    expect(text(root)).not.toContain('旧目录请求失败')
  },
)
it('does not let a cancelled direct directory replace the latest registration availability', async () => {
  sessionState.me = administrator()
  let resolve!: (value: unknown) => void
  api.getDirectZlmInstances.mockReturnValueOnce(
    new Promise((done) => {
      resolve = done
    }),
  )
  const root = await mount(true)
  sessionState.me!.isSuperAdmin = false
  await settle()
  sessionState.me!.isSuperAdmin = true
  await settle()
  resolve({ registrationAvailable: false, items: [] })
  await settle()
  expect(buttons(root).find((button) => text(button) === '+ 接入 ZLM')!.props.disabled).toBe(false)
})
it.each(['account', 'unmount'] as const)(
  'does not refresh after an in-flight mutation loses its %s scope',
  async (reason) => {
    sessionState.me = administrator()
    let resolve!: () => void
    api.savePreviewNode.mockReturnValueOnce(
      new Promise<void>((done) => {
        resolve = done
      }),
    )
    const root = await mount(true)
    ;(buttons(root).find((button) => text(button) === '参与调度')!.props.onClick as () => void)()
    await settle()
    if (reason === 'account') sessionState.epoch++
    else {
      app!.unmount()
      app = null
    }
    resolve()
    await settle()
    expect(api.getPreviewNodes).toHaveBeenCalledOnce()
  },
)
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

it.each([
  ['预算与优先级', '节点预算与优先级'],
  ['手工修改连接', '修改节点连接'],
  ['维护许可', '预览节点维护'],
])('closes the %s dialog when management permission is lost', async (entry, title) => {
  sessionState.me = administrator()
  api.previewMaintenance.mockResolvedValue({ state: 'IDLE', nodeId: 'a' })
  const root = await mount(true)
  click(root, entry)
  await settle()
  expect(dialog(root, title).props['model-value']).toBe(true)
  sessionState.me!.isSuperAdmin = false
  await settle()
  expect(
    nodes(root).filter((item) => item.type === 'ElDialog' && item.props['model-value']),
  ).toHaveLength(0)
  sessionState.me!.isSuperAdmin = true
  await settle()
  expect(
    nodes(root).filter((item) => item.type === 'ElDialog' && item.props['model-value']),
  ).toHaveLength(0)
})

it.each([
  ['+ 接入 ZLM', '接入 ZLM', 'createDirectZlmInstance'],
  ['手工修改连接', '修改节点连接', 'configurePreviewNode'],
  ['预算与优先级', '节点预算与优先级', 'savePreviewNode'],
] as const)(
  'keeps a reopened %s draft when the previous save finishes',
  async (entry, title, method) => {
    sessionState.me = administrator()
    let finish!: () => void
    api[method].mockReturnValueOnce(
      new Promise<void>((resolve) => {
        finish = resolve
      }),
    )
    const root = await mount(true)
    click(root, entry)
    await settle()
    submit(dialog(root, title))
    await settle()
    expect(api[method]).toHaveBeenCalledOnce()
    ;(dialog(root, title).props.onClose as () => void)()
    await settle()
    click(root, entry)
    await settle()
    finish()
    await settle()
    expect(dialog(root, title).props['model-value']).toBe(true)
  },
)

it('does not erase a new ZLM secret when another operation is still busy', async () => {
  sessionState.me = administrator()
  let fail!: (cause: Error) => void
  api.createDirectZlmInstance.mockReturnValueOnce(
    new Promise<void>((_, reject) => {
      fail = reject
    }),
  )
  const root = await mount(true)
  click(root, '+ 接入 ZLM')
  await settle()
  submit(dialog(root, '接入 ZLM'))
  await settle()
  const secret = nodes(dialog(root, '接入 ZLM')).find(
    (item) => item.type === 'input' && item.props.type === 'password',
  )!
  ;(secret.props['onUpdate:modelValue'] as (value: string) => void)('new-draft-secret')
  await settle()
  submit(dialog(root, '接入 ZLM'))
  await settle()
  expect(api.createDirectZlmInstance).toHaveBeenCalledOnce()
  fail(new Error('首次保存失败'))
  await settle()
  submit(dialog(root, '接入 ZLM'))
  await settle()
  expect(api.createDirectZlmInstance).toHaveBeenCalledTimes(2)
  expect(api.createDirectZlmInstance).toHaveBeenLastCalledWith(
    expect.objectContaining({ secret: 'new-draft-secret' }),
  )
  await settle()
})

it('does not close a budget draft after a read-only node check', async () => {
  sessionState.me = administrator()
  const root = await mount(true)
  click(root, '预算与优先级')
  await settle()
  click(root, '检查节点')
  await settle()
  expect(dialog(root, '节点预算与优先级').props['model-value']).toBe(true)
  expect(api.savePreviewNode).not.toHaveBeenCalled()
})

it.each(['success', 'failure', 'refresh-failure'] as const)(
  'closes only the applied budget draft (%s)',
  async (outcome) => {
    sessionState.me = administrator()
    const root = await mount(true)
    click(root, '预算与优先级')
    await settle()
    if (outcome === 'failure') api.savePreviewNode.mockRejectedValueOnce(new Error('保存失败'))
    else api.savePreviewNode.mockResolvedValueOnce(undefined)
    if (outcome === 'refresh-failure')
      api.getPreviewNodes.mockRejectedValueOnce(new Error('刷新失败'))
    submit(dialog(root, '节点预算与优先级'))
    await settle()
    expect(dialog(root, '节点预算与优先级').props['model-value']).toBe(outcome === 'failure')
    expect(api.savePreviewNode).toHaveBeenCalledWith(
      expect.objectContaining({ nodeId: 'a', version: 2 }),
    )
  },
)

it.each([
  ['+ 接入 ZLM', '接入 ZLM', 'createDirectZlmInstance'],
  ['预算', 'ZLM 配置与预算', 'saveDirectZlmInstance'],
  ['密钥', '更新 ZLM secret', 'rotateDirectZlmSecret'],
  ['移除', '移除 ZLM 实例', 'deleteDirectZlmInstance'],
] as const)(
  'refreshes direct instances after %s is applied even if node read-back fails',
  async (entry, title, method) => {
    sessionState.me = administrator()
    api.getDirectZlmInstances.mockResolvedValue(directListing())
    const root = await mount(true)
    click(root, entry)
    await settle()
    if (method === 'rotateDirectZlmSecret') {
      const secret = nodes(dialog(root, title)).find(
        (item) => item.type === 'input' && item.props.type === 'password',
      )!
      ;(secret.props['onUpdate:modelValue'] as (value: string) => void)('replacement-secret')
    }
    api[method].mockResolvedValueOnce(undefined)
    api.getPreviewNodes.mockRejectedValueOnce(new ApiRequestError('NETWORK_ERROR', '节点读回失败'))
    api.getDirectZlmInstances.mockResolvedValue({ registrationAvailable: true, items: [] })
    if (method === 'deleteDirectZlmInstance') click(dialog(root, title), '确认移除')
    else submit(dialog(root, title))
    await settle()
    expect(api[method]).toHaveBeenCalledOnce()
    expect(api.getDirectZlmInstances).toHaveBeenCalledTimes(2)
    expect(text(root)).not.toContain('直连实例')
    expect(text(root)).toContain('节点读回失败')
    expect(dialog(root, title).props['model-value']).toBe(false)
  },
)

it('supersedes a pre-write direct directory request after removal succeeds', async () => {
  sessionState.me = administrator()
  const listing = directListing()
  api.getDirectZlmInstances.mockResolvedValue(listing)
  const root = await mount(true)
  click(root, '移除')
  await settle()
  let finishRemoval!: () => void
  api.deleteDirectZlmInstance.mockReturnValueOnce(
    new Promise<void>((resolve) => {
      finishRemoval = resolve
    }),
  )
  click(dialog(root, '移除 ZLM 实例'), '确认移除')
  let finishDirectory!: (result: ReturnType<typeof directListing>) => void
  api.getDirectZlmInstances.mockReturnValueOnce(
    new Promise((resolve) => {
      finishDirectory = resolve
    }),
  )
  const directory = nodes(root).find(
    (item) => item.type === 'section' && item.props['aria-label'] === '直连 ZLM',
  )!
  click(directory, '刷新')
  await settle()
  api.getDirectZlmInstances.mockResolvedValue({ registrationAvailable: true, items: [] })
  finishRemoval()
  await settle()
  expect(api.getDirectZlmInstances).toHaveBeenCalledTimes(3)
  finishDirectory(listing)
  await settle()
  expect(text(root)).not.toContain('直连实例')
  expect(api.deleteDirectZlmInstance).toHaveBeenCalledOnce()
})

it.each([
  ['预算', 'ZLM 配置与预算'],
  ['移除', '移除 ZLM 实例'],
])('clears the direct ZLM %s draft after losing management permission', async (entry, title) => {
  sessionState.me = administrator()
  api.getDirectZlmInstances.mockResolvedValue(directListing())
  const root = await mount(true)
  click(root, entry)
  await settle()
  expect(dialog(root, title).props['model-value']).toBe(true)
  sessionState.me!.isSuperAdmin = false
  await settle()
  expect(dialog(root, title).props['model-value']).toBe(false)
})
