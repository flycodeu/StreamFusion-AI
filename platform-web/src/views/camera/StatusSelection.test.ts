// @vitest-environment vue-renderer
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { nextTick } from 'vue'
import type { App } from 'vue'
import type { Camera } from '../../api/camera/types'
import type { StatusChannel } from '../../api/camera-status/types'
import { node, nodes, renderer, text } from '../../../tests/support/renderer'
import type { Host } from '../../../tests/support/renderer'
import Status from './Status.vue'
import { clearIdentity } from '../../session/state'

const api = vi.hoisted(() => ({
  getCamera: vi.fn(),
  getCameraStatus: vi.fn(),
  getDeviceChannels: vi.fn(),
  rows: [] as StatusChannel[],
}))
vi.mock('vue-router', () => ({ useRoute: () => ({ query: {} }) }))
vi.mock('../../api/camera/api', () => api)
vi.mock('../../api/camera-status/api', () => api)
vi.mock('../../features/camera-status/PlanDialog.vue', () => ({ default: { render: () => null } }))
vi.mock('../../features/camera-status/useStatusPage', async () => {
  const { ref } = await import('vue')
  return {
    useStatusPage: () => ({
      rows: ref(api.rows),
      result: ref({ total: 1 }),
      summary: ref({
        total: 1,
        unknown: 1,
        accessible: 0,
        failed: 0,
        checking: 0,
        stale: 0,
        disabled: 0,
      }),
      total: ref(1),
      loading: ref(false),
      error: ref(null),
      paused: ref(false),
      refresh: vi.fn(),
    }),
  }
})
vi.mock('../../features/camera-status/StatusPanel.vue', async () => {
  const { defineComponent, h } = await import('vue')
  return {
    default: defineComponent({
      props: ['camera', 'initialProfileId', 'active'],
      setup(props, { slots }) {
        return () =>
          h(
            'StatusPanel',
            { camera: props.camera, initialProfileId: props.initialProfileId },
            props.camera
              ? slots.summary?.({
                  connection: { state: 'unknown', label: '未确认', title: '最近一次检查' },
                })
              : [],
          )
      },
    }),
  }
})
vi.mock('element-plus', async () => {
  const { defineComponent, h } = await import('vue')
  const names = [
    'ElButton',
    'ElForm',
    'ElFormItem',
    'ElInput',
    'ElIcon',
    'ElOption',
    'ElPagination',
    'ElSelect',
    'ElRadioGroup',
    'ElRadioButton',
  ]
  return {
    ...Object.fromEntries(
      names.map((name) => [
        name,
        defineComponent({
          inheritAttrs: false,
          setup(_, { attrs, slots }) {
            return () => h(name, attrs, slots.default?.())
          },
        }),
      ]),
    ),
    ElDrawer: defineComponent({
      props: ['modelValue'],
      inheritAttrs: false,
      setup(props, { attrs, slots }) {
        return () =>
          h(
            'ElDrawer',
            { ...attrs, modelValue: props.modelValue },
            props.modelValue ? slots.default?.() : [],
          )
      },
    }),
    vLoading: {},
  }
})

let app: App
let root: Host
async function settle() {
  for (let index = 0; index < 8; index++) {
    await Promise.resolve()
    await nextTick()
  }
}
function camera(id: string): Camera {
  return {
    cameraId: id,
    name: `内部通道${id}`,
    remark: null,
    sourceId: '7',
    sourceName: null,
    sourceDisplayName: '测试来源',
    sourceVersion: '0',
    sourceType: 'RTSP',
    connectionCategory: 'RTSP',
    vendorHint: null,
    groupId: '8',
    groupPath: '测试分组',
    lifecycle: 'ENABLED',
    defaultPreviewProfileId: `${id}0`,
    version: '0',
    createdAt: '2026-10-24T00:00:00Z',
    updatedAt: '2026-10-24T00:00:00Z',
    deviceId: '7',
    deviceSummary: { model: `型号${id}` },
    profiles: ['MAIN', 'SUB'].map((usageHint, index) => ({
      streamProfileId: `${id}${index}`,
      label: usageHint === 'MAIN' ? '主码流' : '辅码流',
      usageHint,
      usageOrigin: 'MANUAL',
      enabled: true,
      version: '0',
      videoCodec: null,
      width: null,
      height: null,
      frameRate: null,
      bitrateKbps: null,
      audioCodec: null,
      hasAudio: null,
      parametersObservedAt: null,
      parametersOrigin: null,
      locatorSummary: {
        editable: false,
        locatorKind: 'RTSP',
        configured: true,
        hostMode: 'SOURCE',
        host: null,
        port: null,
        transport: 'TCP',
      },
    })),
  }
}
function mount() {
  root = node('root')
  app = renderer.createApp(Status)
  app.mount(root)
}
async function open() {
  const button = nodes(root).find((item) => item.type === 'ElButton' && text(item) === '详情')!
  ;(button.props.onClick as () => void)()
  await settle()
}
function select(label: string) {
  return nodes(root).find((item) => item.type === 'ElSelect' && item.props['aria-label'] === label)!
}
function panel() {
  return nodes(root).find((item) => item.type === 'StatusPanel')!
}
beforeEach(() => {
  vi.clearAllMocks()
  api.rows = [
    {
      cameraId: '1',
      groupKey: 'd7',
      deviceName: '十六路录像机',
      name: '内部通道1',
      lifecycle: 'ENABLED',
      profiles: [
        {
          streamProfileId: '11',
          enabled: true,
          control: { freshness: 'NEVER', observation: null },
          media: { freshness: 'NEVER', observation: null },
        },
      ],
    } as StatusChannel,
  ]
  api.getCamera.mockImplementation(async (id: string) => camera(id))
  api.getCameraStatus.mockImplementation(async (id: string) => ({
    profiles: [{ streamProfileId: `${id}1` }, { streamProfileId: `${id}0` }],
  }))
  api.getDeviceChannels.mockResolvedValue({
    items: [camera('1'), camera('2')],
    total: 2,
    page: 1,
    size: 50,
  })
})
afterEach(() => {
  app?.unmount()
  vi.restoreAllMocks()
})

describe('device status manual target selection', () => {
  it('shows one device and lets advanced inspection select a channel and stream without changing preview settings', async () => {
    mount()
    expect(text(root)).toContain('十六路录像机')
    expect(text(root)).toContain('当前页 1 台相机')
    await open()
    expect(api.getDeviceChannels).toHaveBeenCalledWith('d7', { page: 1, size: 50 })
    expect(panel().props.initialProfileId).toBe('11')
    expect((panel().props.camera as Camera).cameraId).toBe('1')
    ;(select('检测通道').props['onUpdate:modelValue'] as (value: string) => void)('2')
    await settle()
    expect(api.getCamera).toHaveBeenLastCalledWith('2', expect.any(AbortSignal))
    expect((panel().props.camera as Camera).cameraId).toBe('2')
    expect(panel().props.initialProfileId).toBe('21')
    ;(select('检测码流').props['onUpdate:modelValue'] as (value: string) => void)('20')
    await settle()
    expect(panel().props.initialProfileId).toBe('20')
  })

  it('discards delayed channel detail when the drawer closes and reopens on the representative', async () => {
    let resolveLater!: (value: Camera) => void
    api.getCamera.mockImplementation((id: string) =>
      id === '2'
        ? new Promise<Camera>((resolve) => {
            resolveLater = resolve
          })
        : Promise.resolve(camera(id)),
    )
    mount()
    await open()
    ;(select('检测通道').props['onUpdate:modelValue'] as (value: string) => void)('2')
    await settle()
    const drawer = nodes(root).find((item) => item.type === 'ElDrawer')!
    ;(drawer.props['onUpdate:modelValue'] as (value: boolean) => void)(false)
    await settle()
    await open()
    expect((panel().props.camera as Camera).cameraId).toBe('1')
    resolveLater(camera('2'))
    await settle()
    expect((panel().props.camera as Camera).cameraId).toBe('1')
    expect(panel().props.initialProfileId).toBe('11')
    expect(text(root)).not.toContain('型号2')
  })

  it('does not add channel or profile selectors for a single usable stream', async () => {
    api.getDeviceChannels.mockResolvedValue({ items: [camera('1')], total: 1, page: 1, size: 50 })
    api.getCamera.mockResolvedValue({ ...camera('1'), profiles: [camera('1').profiles[1]!] })
    mount()
    await open()
    expect(select('检测通道')).toBeUndefined()
    expect(select('检测码流')).toBeUndefined()
    expect(panel().props.initialProfileId).toBe('11')
  })

  it('closes the drawer on identity loss without starting more reads or applying delayed results', async () => {
    let resolveLater!: (value: Camera) => void
    api.getCamera.mockImplementation((id: string) =>
      id === '2'
        ? new Promise<Camera>((resolve) => {
            resolveLater = resolve
          })
        : Promise.resolve(camera(id)),
    )
    mount()
    await open()
    ;(select('检测通道').props['onUpdate:modelValue'] as (value: string) => void)('2')
    await settle()
    const counts = [
      api.getCamera.mock.calls.length,
      api.getCameraStatus.mock.calls.length,
      api.getDeviceChannels.mock.calls.length,
    ]
    clearIdentity()
    await settle()
    expect([
      api.getCamera.mock.calls.length,
      api.getCameraStatus.mock.calls.length,
      api.getDeviceChannels.mock.calls.length,
    ]).toEqual(counts)
    expect(nodes(root).find((item) => item.type === 'ElDrawer')!.props.modelValue).toBe(false)
    resolveLater(camera('2'))
    await settle()
    expect(panel()).toBeUndefined()
    expect(text(root)).not.toContain('型号2')
  })
})
