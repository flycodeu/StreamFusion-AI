// @vitest-environment vue-renderer
import { afterEach, beforeEach, expect, it, vi } from 'vitest'
import { nextTick, type App } from 'vue'
import { node, nodes, renderer, text, type Host } from '../support/renderer'
import Sync from '../../src/features/preview/PreviewOpsSync.vue'
import type { PreviewNodeStatus, OpsSyncReview } from '../../src/api/preview-node/api'
import { sessionState } from '../../src/session/state'

const api = vi.hoisted(() => ({
  getOpsPreviewInstances: vi.fn(),
  getOpsZlmInstances: vi.fn(),
  synchronizeOpsNode: vi.fn(),
}))
vi.mock('../../src/api/preview-node/api', () => api)
vi.mock('vue', async () => {
  const actual = await vi.importActual<typeof import('vue')>('vue')
  return { ...actual, vModelSelect: {} }
})
vi.mock('element-plus', async () => {
  const { defineComponent, h } = await import('vue')
  return Object.fromEntries(
    ['ElButton', 'ElDialog'].map((name) => [
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
const budget = {
  maxSources: 8,
  maxViewers: 8,
  maxTranscodes: 1,
  maxStarting: 4,
  ingressKbps: 81920,
  egressKbps: 81920,
  sourceUpperKbps: 8192,
  outputUpperKbps: 8192,
}
const source = {
  controllerId: 'controller-a',
  targetId: 'target-a',
  instanceId: 'instance-a',
  releaseId: 'release-a',
  syncedAt: '2026-10-18T01:00:00Z',
}
const candidate = { ...source, releaseId: 'release-b', syncedAt: null }
const value = (): PreviewNodeStatus => ({
  nodeId: 'node-a',
  kind: 'MANAGED',
  name: '预览节点 A',
  mode: 'DRAINING',
  version: 7,
  priority: 27,
  budget,
  usage: { sources: 0, viewers: 0, transcodes: 0, starting: 0, ingressKbps: 0, egressKbps: 0 },
  state: 'READY',
  reasonCode: null,
  observedAt: null,
  controlUri: 'https://10.0.0.1:8443',
  zlmApiAddress: null,
  certificateSha256: null,
  mediaAddress: '10.0.0.1',
  desiredConfigVersion: 'config-a',
  observation: null,
  protection: null,
  zlmLoad: null,
  opsSource: source,
})
const review = (): OpsSyncReview => ({
  nodeId: 'node-a',
  version: 7,
  previousSource: source,
  candidateSource: candidate,
  previousConfigVersion: 'config-a',
  candidateConfigVersion: 'config-b',
  previousName: '预览节点 A',
  candidateName: '预览节点 A',
  previousInstallPath: null,
  candidateInstallPath: null,
  retainedBudget: budget,
  candidateLimit: budget,
  credentialChanged: false,
  digest: 'd'.repeat(64),
  checkedAt: '2026-10-18T02:00:00Z',
})
let app: App | null = null
let applied: ReturnType<typeof vi.fn>
const settle = async () => {
  for (let i = 0; i < 10; i++) await nextTick()
}
const buttons = (root: Host) => nodes(root).filter((n) => n.type === 'ElButton')
const button = (root: Host, label: string) => buttons(root).find((n) => text(n) === label)!
async function mount(subject = value()) {
  const root = node('root')
  app = renderer.createApp(Sync, { node: subject, onApplied: applied })
  app.mount(root)
  await settle()
  return root
}
beforeEach(() => {
  vi.clearAllMocks()
  applied = vi.fn()
  api.getOpsPreviewInstances.mockResolvedValue({
    available: true,
    reasonCode: null,
    items: [
      {
        ...candidate,
        name: '预览节点 A',
        targetName: '目标 A',
      },
    ],
  })
  api.getOpsZlmInstances.mockResolvedValue({ available: true, reasonCode: null, items: [] })
  api.synchronizeOpsNode.mockResolvedValue(review())
})
afterEach(() => {
  app?.unmount()
  app = null
  sessionState.epoch++
})

it('checks the original identity before applying a non-sensitive reviewed candidate', async () => {
  const root = await mount()
  expect(button(root, '应用同步').props.disabled).toBe(true)
  expect(text(root)).toContain('release-a')
  expect(text(root)).toContain('release-b')
  expect(nodes(root).find((n) => n.type === 'select')!.props.disabled).toBe(true)
  await (button(root, '预检').props.onClick as () => Promise<void>)()
  await settle()
  expect(api.synchronizeOpsNode).toHaveBeenCalledWith(
    'node-a',
    {
      controllerId: 'controller-a',
      targetId: 'target-a',
      instanceId: 'instance-a',
      releaseId: 'release-b',
      version: 7,
      digest: undefined,
    },
    false,
  )
  expect(text(root)).toContain('预检通过')
  expect(text(root)).toContain('保持：源 8')
  await (button(root, '应用同步').props.onClick as () => Promise<void>)()
  await settle()
  expect(api.synchronizeOpsNode).toHaveBeenLastCalledWith(
    'node-a',
    expect.objectContaining({ digest: 'd'.repeat(64) }),
    true,
  )
  expect(applied).toHaveBeenCalledOnce()
})

it('never rebinds an existing source to a same-named unrelated instance', async () => {
  api.getOpsPreviewInstances.mockResolvedValue({
    available: true,
    reasonCode: null,
    items: [
      {
        ...candidate,
        targetId: 'other-target',
        name: '预览节点 A',
        targetName: '目标 A',
      },
    ],
  })
  const root = await mount()
  expect(button(root, '预检').props.disabled).toBe(true)
  expect(api.synchronizeOpsNode).not.toHaveBeenCalled()
  expect(text(root)).toContain('没有可选的已发布实例')
})

it('requires draining and clears the old review after failed apply', async () => {
  const root = await mount()
  await (button(root, '预检').props.onClick as () => Promise<void>)()
  api.synchronizeOpsNode.mockRejectedValueOnce(new Error('版本已变化'))
  await (button(root, '应用同步').props.onClick as () => Promise<void>)()
  await settle()
  expect(button(root, '应用同步').props.disabled).toBe(true)
  expect(applied).not.toHaveBeenCalled()
  app?.unmount()
  const serving = await mount({ ...value(), mode: 'REMOTE' })
  expect(button(serving, '预检').props.disabled).toBe(true)
  expect(text(serving)).toContain('排空资源')
})

it('keeps legacy linking explicit and tells direct ZLM users the maintenance limitation', async () => {
  const root = await mount({ ...value(), kind: 'ZLM_DIRECT', opsSource: null })
  expect(api.getOpsZlmInstances).toHaveBeenCalledOnce()
  expect(button(root, '预检').props.disabled).toBe(true)
  expect(text(root)).toContain('没有持久维护许可')
})

it('shows the reviewed ZLM installation path transition when Ops supplies it', async () => {
  api.getOpsZlmInstances.mockResolvedValue({
    available: true,
    reasonCode: null,
    items: [{ ...candidate, name: 'ZLM A', targetName: '目标 A' }],
  })
  api.synchronizeOpsNode.mockResolvedValue({
    ...review(),
    previousInstallPath: '/srv/zlm/old',
    candidateInstallPath: '/srv/zlm/new',
  })
  const root = await mount({ ...value(), kind: 'ZLM_DIRECT' })
  await (button(root, '预检').props.onClick as () => Promise<void>)()
  await settle()
  expect(text(root)).toContain('ZLM 安装位置')
  expect(text(root)).toContain('/srv/zlm/old → /srv/zlm/new')
})

it('does not emit success into another signed-in page scope', async () => {
  let resolve: (value: OpsSyncReview) => void = () => {}
  const root = await mount()
  await (button(root, '预检').props.onClick as () => Promise<void>)()
  api.synchronizeOpsNode.mockImplementationOnce(
    () =>
      new Promise<OpsSyncReview>((done) => {
        resolve = done
      }),
  )
  const pending = (button(root, '应用同步').props.onClick as () => Promise<void>)()
  sessionState.epoch++
  resolve(review())
  await pending
  await settle()
  expect(applied).not.toHaveBeenCalled()
})
