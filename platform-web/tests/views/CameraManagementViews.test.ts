// @vitest-environment vue-renderer
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { nextTick } from 'vue'
import type { App, Component } from 'vue'
import { node, nodes, renderer, text } from '../support/renderer'
import type { Host } from '../support/renderer'
import { clearIdentity } from '../../src/session/state'
import CameraManage from '../../src/views/camera/Manage.vue'
import ScopeManage from '../../src/views/camera-scope/Manage.vue'
import CreateDialog from '../../src/features/camera/CreateDialog.vue'
import DetailDialog from '../../src/features/camera/DetailDialog.vue'
import PlanDialog from '../../src/features/camera-status/PlanDialog.vue'
import PlanCameraPicker from '../../src/features/camera-status/PlanCameraPicker.vue'
import DeviceDetailPanel from '../../src/features/camera/DeviceDetailPanel.vue'
import DeviceMoveDialog from '../../src/features/camera/DeviceMoveDialog.vue'
import DeviceEditor from '../../src/features/camera/DeviceEditor.vue'
import ProfileEditor from '../../src/features/camera/ProfileEditor.vue'
import PlacementDialog from '../../src/features/camera/PlacementDialog.vue'
import ManualCreateDialog from '../../src/features/camera/ManualCreateDialog.vue'
import ScanDialog from '../../src/features/camera-access/ScanDialog.vue'
import SourceEditor from '../../src/features/camera-sources/SourceEditor.vue'
import BulkImportPanel from '../../src/features/camera-access/BulkImportPanel.vue'
import ImportTasksDialog from '../../src/features/camera-access/ImportTasksDialog.vue'
import PlatformDirectory from '../../src/features/camera-access/PlatformDirectory.vue'
import { ApiRequestError } from '../../src/lib/http/error'

const api = vi.hoisted(() => ({
  getCameraOptions: vi.fn(),
  getAccessOptions: vi.fn(),
  createAccessJob: vi.fn(),
  createScanJob: vi.fn(),
  getAccessJob: vi.fn(),
  cancelAccessJob: vi.fn(),
  previewImport: vi.fn(),
  importCandidates: vi.fn(),
  getImportJobs: vi.fn(),
  previewBulkImport: vi.fn(),
  startBulkImport: vi.fn(),
  getImportItems: vi.fn(),
  getCameras: vi.fn(),
  getCameraDevices: vi.fn(),
  getDeviceChannels: vi.fn(),
  getCameraDevice: vi.fn(),
  updateCameraDevice: vi.fn(),
  getCamera: vi.fn(),
  createCamera: vi.fn(),
  updateCamera: vi.fn(),
  deleteCamera: vi.fn(),
  createProfile: vi.fn(),
  updateProfile: vi.fn(),
  deleteProfile: vi.fn(),
  getSourceOptions: vi.fn(),
  getSources: vi.fn(),
  getSource: vi.fn(),
  updateSource: vi.fn(),
  createSource: vi.fn(),
  getPlatformRegions: vi.fn(),
  getScopeUsers: vi.fn(),
  getCameraScope: vi.fn(),
  updateCameraScope: vi.fn(),
  getScopeGroupTree: vi.fn(),
  getScopeCameras: vi.fn(),
  getGroupTree: vi.fn(),
  getDevicePlacements: vi.fn(),
  previewDeviceMove: vi.fn(),
  moveDevice: vi.fn(),
  createGroup: vi.fn(),
  updateGroup: vi.fn(),
  deleteGroup: vi.fn(),
  previewCameraMove: vi.fn(),
  previewCameraLifecycle: vi.fn(),
  confirm: vi.fn(),
  success: vi.fn(),
  getStatusPlans: vi.fn(),
  createStatusPlan: vi.fn(),
  stopStatusPlan: vi.fn(),
  resumeStatusPlan: vi.fn(),
  inspectPlanTargets: vi.fn(),
}))
vi.mock('../../src/api/camera/api', () => api)
vi.mock('../../src/api/camera-status/plans', () => api)
vi.mock('../../src/api/camera-status/planTargets', () => api)
vi.mock('../../src/api/camera-access/api', () => api)
vi.mock('../../src/api/camera-sources/api', () => api)
vi.mock('../../src/api/camera-scopes/api', () => api)
vi.mock('../../src/api/camera-groups/api', () => api)
vi.mock('../../src/components/table/ColumnPicker.vue', () => ({ default: { render: () => null } }))
vi.mock('element-plus', async () => {
  const { defineComponent, h, inject, provide } = await import('vue')
  const controls = [
    'ElButton',
    'ElForm',
    'ElFormItem',
    'ElInput',
    'ElInputNumber',
    'ElOption',
    'ElPagination',
    'ElSelect',
    'ElSwitch',
    'ElTag',
    'ElCheckbox',
    'ElDescriptions',
    'ElDescriptionsItem',
    'ElTabs',
    'ElTabPane',
    'ElSteps',
    'ElStep',
    'ElRadioGroup',
    'ElRadioButton',
    'ElDropdown',
    'ElDropdownMenu',
    'ElDropdownItem',
  ]
  const simple = Object.fromEntries(
    controls.map((name) => [
      name,
      defineComponent({
        inheritAttrs: false,
        setup(_, { attrs, slots }) {
          return () => h(name, attrs, slots.default?.())
        },
      }),
    ]),
  )
  return {
    ...simple,
    ElTreeSelect: defineComponent({
      inheritAttrs: false,
      setup(_, { attrs, slots }) {
        return () =>
          h(
            'ElTreeSelect',
            attrs,
            ((attrs.data as Record<string, unknown>[]) ?? []).map((data) =>
              slots.default?.({ data }),
            ),
          )
      },
    }),
    ElTree: defineComponent({
      inheritAttrs: false,
      setup(_, { attrs, expose }) {
        expose({ filter: () => {}, setCurrentKey: () => {}, setCheckedKeys: () => {} })
        return () => h('ElTree', attrs)
      },
    }),
    vLoading: {},
    ElMessage: { success: api.success },
    ElMessageBox: { confirm: api.confirm },
    ElDrawer: defineComponent({
      inheritAttrs: false,
      setup(_, { attrs, slots }) {
        return () =>
          (attrs.modelValue ?? attrs['model-value'])
            ? h('ElDrawer', attrs, [slots.default?.(), slots.footer?.()])
            : null
      },
    }),
    ElAlert: defineComponent({
      setup(_, { attrs, slots }) {
        return () => h('ElAlert', attrs, [String(attrs.title), slots.default?.()])
      },
    }),
    ElDialog: defineComponent({
      inheritAttrs: false,
      setup(_, { attrs, slots }) {
        return () =>
          (attrs.modelValue ?? attrs['model-value'])
            ? h('ElDialog', attrs, [slots.default?.(), slots.footer?.()])
            : null
      },
    }),
    ElTable: defineComponent({
      inheritAttrs: false,
      setup(_, { attrs, slots }) {
        provide('rows', () => attrs.data as Record<string, unknown>[])
        return () => h('ElTable', attrs, slots.default?.())
      },
    }),
    ElTableColumn: defineComponent({
      inheritAttrs: false,
      setup(_, { attrs, slots }) {
        const rows = inject<() => Record<string, unknown>[]>('rows', () => [])
        return () =>
          h(
            'ElTableColumn',
            attrs,
            rows().map((row) => slots.default?.({ row }) ?? String(row[String(attrs.prop)] ?? '')),
          )
      },
    }),
  }
})
const profile = {
  streamProfileId: '20',
  label: '辅码流',
  usageHint: 'SUB',
  usageOrigin: 'MANUAL',
  enabled: true,
  version: '2',
  locatorSummary: { locatorKind: 'RTSP', configured: true, hostMode: 'SOURCE', transport: 'TCP' },
  videoCodec: null,
  width: null,
  height: null,
  frameRate: null,
  bitrateKbps: null,
}
const camera = {
  cameraId: '10',
  name: '炉前',
  remark: null,
  sourceDisplayName: '来源',
  sourceType: 'RTSP',
  groupId: '30',
  groupPath: '厂区',
  lifecycle: 'ENABLED',
  version: '3',
  defaultPreviewProfileId: '20',
  createdAt: '2026-10-06T00:00:00Z',
  updatedAt: '2026-10-06T00:00:00Z',
  profiles: [profile],
}
const device = {
  groupKey: 'd100',
  name: '设备一',
  identified: true,
  manufacturer: 'Dahua',
  model: '双通道',
  sourceDisplayName: '来源',
  sourceType: 'ONVIF',
  connectionCategory: 'DEVICE',
  channelCount: 2,
  enabledCount: 2,
  disabledCount: 0,
  pendingCount: 0,
}
const source = {
  sourceId: '1',
  name: '来源',
  remark: null,
  version: '4',
  adapterType: 'RTSP',
  enabled: true,
  networkPolicyKey: 'approved',
  credentials: [],
  endpoints: [
    {
      purpose: 'RTSP',
      scheme: 'rtsp',
      host: '192.0.2.10',
      port: 554,
      authMode: 'NONE',
      credentialPurpose: null,
    },
  ],
}
const scope = {
  userId: '5',
  version: '6',
  mode: 'CUSTOM',
  groupIds: ['30', '31'],
  cameraIds: ['10', '11'],
  groupGrants: [
    { groupId: '30', name: '厂区', path: '厂区' },
    { groupId: '31', name: '深层', path: '厂区 / 深层' },
  ],
  cameraGrants: [
    { cameraId: '10', name: '炉前', lifecycle: 'ENABLED' },
    { cameraId: '11', name: '旧画面', lifecycle: 'DISABLED' },
  ],
  effectiveSummary: {
    effectiveCameraCount: 2,
    enabledCameraCount: 1,
    computedAt: '2026-10-06T00:00:00Z',
  },
}
const accessJob = {
  jobId: '900',
  version: '2',
  status: 'SUCCEEDED',
  method: 'RTSP',
  sourceId: null,
  expiresAt: '2099-10-06T00:15:00Z',
  complete: true,
  device: null,
  warnings: [],
  diagnostic: null,
  result: null,
  candidates: [
    {
      candidateId: 'channel-a',
      name: '炉前',
      mappingRequired: false,
      profiles: [
        {
          profileId: 'stream-a',
          name: '主码流',
          usageHint: 'MAIN',
          videoCodec: 'H264',
          width: 1920,
          height: 1080,
          frameRate: 25,
          bitrateKbps: 2048,
        },
        {
          profileId: 'stream-b',
          name: '辅码流',
          usageHint: 'SUB',
          videoCodec: null,
          width: null,
          height: null,
          frameRate: null,
          bitrateKbps: null,
        },
      ],
    },
  ],
}
const impact = {
  confirmation: 'proof',
  expiresAt: '2026-10-06T00:05:00Z',
  targetLifecycle: 'ENABLED',
  targetGroupId: '30',
  gainedUserCount: 2,
  lostUserCount: 0,
  affectedCameraCount: 1,
  fromPath: null,
  toPath: '厂区',
}
const bulkJob = {
  ...accessJob,
  kind: 'BULK_IMPORT',
  method: 'HIK_PLATFORM',
  sourceId: '70',
  sourceVersion: '9',
  status: 'RUNNING',
  candidates: [],
  bulk: {
    groupId: '30',
    pageNumber: 2,
    total: 1200,
    processedCount: 100,
    createdCount: 75,
    existingCount: 25,
    failedCount: 0,
    duplicateCount: 2,
    maxItems: 10000,
  },
}
const button = (root: Host, label: string) =>
  nodes(root).find((item) => item.type === 'ElButton' && text(item) === label)!
const click = async (root: Host, label: string) => {
  await (button(root, label).props.onClick as () => unknown)()
  await nextTick()
}
const field = (root: Host, label: string, kind = 'ElInput') =>
  nodes(nodes(root).find((item) => item.type === 'ElFormItem' && item.props.label === label)!).find(
    (item) => item.type === kind,
  )!
const setField = async (root: Host, label: string, value: unknown, kind = 'ElInput') => {
  ;(field(root, label, kind).props['onUpdate:modelValue'] as (value: unknown) => void)(value)
  await nextTick()
}
async function selectPlatformRegion(root: Host) {
  const tree = nodes(root).find((item) => item.type === 'ElTree')!
  await (tree.props.onNodeClick as (data: unknown, node: unknown) => Promise<void>)(
    { indexCode: 'region-A', parentIndexCode: null, name: '平台区域A' },
    {},
  )
  await settle()
}
async function openPlatformRegion(root: Host, reused = true) {
  await click(root, reused ? '打开平台目录' : '保存密钥并打开目录')
  expect(api.createAccessJob).not.toHaveBeenCalled()
  await selectPlatformRegion(root)
}
async function choosePatrolPage(root: Host) {
  await click(root, '下一步')
  if (nodes(root).some((item) => item.type === 'ElFormItem' && item.props.label === '调度方式')) {
    await click(root, '下一步')
    await click(root, '下一步')
  }
  await settle()
}
async function patrolTab(root: Host, tab: 'create' | 'tasks') {
  const tabs = nodes(root).find((item) => item.type === 'ElTabs')!
  ;(tabs.props['onUpdate:modelValue'] as (value: string) => void)(tab)
  await settle()
}
async function patrolScope(root: Host, scope: string) {
  await setField(root, '选择范围', scope, 'ElRadioGroup')
  ;(field(root, '选择范围', 'ElRadioGroup').props.onChange as () => void)()
  await settle()
}
let app: App | null = null
async function mount(component: Component, props = {}) {
  const root = node('root')
  app = renderer.createApp(component, props)
  app.mount(root)
  await settle()
  return root
}
async function settle() {
  for (let i = 0; i < 8; i++) await nextTick()
}
beforeEach(() => {
  vi.resetAllMocks()
  api.createSource.mockResolvedValue({
    ...source,
    sourceId: '70',
    version: '9',
    adapterType: 'HIK_PLATFORM',
  })
  api.getPlatformRegions.mockResolvedValue({
    items: [{ indexCode: 'region-A', parentIndexCode: null, name: '平台区域A' }],
    total: 1,
    page: 1,
    size: 100,
  })
  api.inspectPlanTargets.mockResolvedValue({ targets: [], limitExceeded: false })
  clearIdentity()
  api.getCameraOptions.mockResolvedValue({ canManageShared: true, manualStorageReady: true })
  api.getGroupTree.mockResolvedValue([])
  api.getDevicePlacements.mockResolvedValue([
    { groupId: null, groupPath: '待归档', channelCount: 2 },
  ])
  api.getCameraDevice.mockResolvedValue({
    deviceId: '100',
    name: '设备一',
    localName: null,
    remark: '北门',
    version: '4',
  })
  api.updateCameraDevice.mockResolvedValue({
    deviceId: '100',
    name: '入口相机',
    localName: '入口相机',
    remark: '北门',
    version: '5',
  })
  api.getAccessOptions.mockResolvedValue({
    ready: true,
    methods: ['RTSP'],
    adapters: [
      {
        type: 'RTSP',
        label: 'RTSP',
        category: 'RTSP',
        inputKind: 'RTSP_URL',
        autoDetect: false,
        paged: false,
      },
    ],
    networkPolicies: [{ key: 'approved', name: '批准网络' }],
    diagnostics: [],
  })
  api.createAccessJob.mockResolvedValue({ jobId: '900' })
  api.createScanJob.mockResolvedValue({ jobId: '901' })
  api.createCamera.mockResolvedValue({ cameraId: '10' })
  api.getAccessJob.mockResolvedValue(accessJob)
  api.getImportJobs.mockResolvedValue({ items: [bulkJob], page: 1, size: 20, total: 1 })
  api.previewBulkImport.mockResolvedValue({
    confirmation: 'bulk-proof',
    groupPath: '厂区',
    affectedUserCount: 2,
    declaredTotal: 1200,
    maxItems: 10000,
    pageSize: 100,
  })
  api.startBulkImport.mockResolvedValue(bulkJob)
  api.getImportItems.mockResolvedValue({ items: [], page: 1, size: 20, total: 0 })
  api.cancelAccessJob.mockResolvedValue({ ...accessJob, status: 'CANCELLED' })
  api.previewImport.mockResolvedValue({
    confirmation: 'import-proof',
    groupPath: '厂区',
    affectedUserCount: 2,
    cameraCount: 1,
  })
  api.importCandidates.mockResolvedValue({
    sourceId: '1',
    cameras: [{ candidateId: 'channel-a', cameraId: '10', version: '0', status: 'CREATED' }],
    createdCount: 1,
    existingCount: 0,
  })
  api.getCameras.mockResolvedValue({ items: [camera], total: 1, page: 1, size: 20 })
  api.getCameraDevices.mockResolvedValue({ items: [device], total: 1, page: 1, size: 20 })
  api.getDeviceChannels.mockResolvedValue({ items: [camera], total: 1, page: 1, size: 20 })
  api.getCamera.mockResolvedValue(camera)
  api.updateCamera.mockResolvedValue(camera)
  api.getSourceOptions.mockResolvedValue({
    ready: true,
    adapterTypes: ['RTSP'],
    credentialPurposes: ['RTSP'],
    networkPolicies: [{ key: 'approved', name: '批准网络' }],
  })
  api.getSources.mockResolvedValue({ items: [source], total: 1, page: 1, size: 20 })
  api.getSource.mockResolvedValue(source)
  api.getScopeUsers.mockResolvedValue({
    items: [
      {
        userId: '5',
        username: 'safety',
        nickname: '安全处',
        enabled: true,
        isSuperAdmin: false,
        departments: [
          { id: '2002', name: '研发部门' },
          { id: '2003', name: '运维部门' },
        ],
      },
    ],
    total: 1,
    page: 1,
    size: 20,
  })
  api.getCameraScope.mockResolvedValue(scope)
  api.updateCameraScope.mockResolvedValue(scope)
  api.getScopeGroupTree.mockResolvedValue([
    {
      groupId: '30',
      name: '厂区',
      parentId: null,
      sortOrder: 0,
      version: '0',
      visibleCameraCount: 2,
    },
    {
      groupId: '31',
      name: '深层',
      parentId: '30',
      sortOrder: 0,
      version: '0',
      visibleCameraCount: 1,
    },
  ])
  api.getScopeCameras.mockResolvedValue({ items: [], total: 0, page: 1, size: 20 })
  api.previewCameraMove.mockResolvedValue(impact)
  api.previewCameraLifecycle.mockResolvedValue(impact)
  api.confirm.mockResolvedValue('confirm')
  api.deleteCamera.mockResolvedValue(undefined)
  api.updateProfile.mockResolvedValue(profile)
})
afterEach(() => {
  app?.unmount()
  app = null
  vi.useRealTimers()
  vi.restoreAllMocks()
})

describe('camera management interaction boundaries', () => {
  it('loads only expanded platform region pages and keeps every level initially collapsed', async () => {
    const connection = { method: 'HIK_PLATFORM', sourceId: '70', sourceVersion: '9' }
    const root = await mount(PlatformDirectory, { connection, disabled: false })
    expect(api.getPlatformRegions).not.toHaveBeenCalled()
    const tree = nodes(root).find((item) => item.type === 'ElTree')!
    expect(tree.props['default-expand-all']).toBe(false)
    const resolve = vi.fn(),
      reject = vi.fn()
    const load = tree.props.load as (
      node: unknown,
      resolve: typeof vi.fn,
      reject: typeof vi.fn,
    ) => Promise<void>
    await load({ level: 0 }, resolve, reject)
    expect(api.getPlatformRegions).toHaveBeenCalledWith({
      connection,
      parentCode: undefined,
      page: 1,
      size: 100,
    })
    api.getPlatformRegions.mockResolvedValueOnce({
      items: [{ indexCode: 'child', name: '子区域' }],
      total: 201,
      size: 100,
    })
    await load({ level: 1, data: { indexCode: 'region-A' } }, resolve, reject)
    expect(api.getPlatformRegions).toHaveBeenLastCalledWith({
      connection,
      parentCode: 'region-A',
      page: 1,
      size: 100,
    })
    expect(resolve.mock.calls[1]![0]).toEqual([
      { indexCode: 'child', name: '子区域' },
      expect.objectContaining({ more: true, nextPage: 2, parentIndexCode: 'region-A', leaf: true }),
    ])
    expect(api.createAccessJob).not.toHaveBeenCalled()
    expect(reject).not.toHaveBeenCalled()
  })
  it('ignores a late platform tree response after refreshing the directory', async () => {
    let finish!: (value: unknown) => void
    api.getPlatformRegions.mockImplementationOnce(
      () =>
        new Promise((resolve) => {
          finish = resolve
        }),
    )
    const root = await mount(PlatformDirectory, {
      connection: { method: 'HIK_PLATFORM', sourceId: '70', sourceVersion: '9' },
      disabled: false,
    })
    const tree = nodes(root).find((item) => item.type === 'ElTree')!
    const resolve = vi.fn(),
      reject = vi.fn()
    const pending = (
      tree.props.load as (n: unknown, r: typeof vi.fn, e: typeof vi.fn) => Promise<void>
    )({ level: 0 }, resolve, reject)
    await click(root, '刷新目录')
    finish({ items: [{ indexCode: 'old', name: '旧区域' }], total: 1, size: 100 })
    await pending
    expect(resolve).not.toHaveBeenCalled()
    expect(reject).not.toHaveBeenCalled()
  })
  it('starts with a camera list and sends explicit group and parallel CONTROL settings through steps', async () => {
    api.getStatusPlans.mockResolvedValue([])
    api.inspectPlanTargets.mockResolvedValue({
      targets: [{ groupKey: 'c10', name: '组内相机', reasonCode: null }],
      limitExceeded: false,
    })
    api.createStatusPlan.mockResolvedValue({ planId: '1' })
    const root = await mount(PlanDialog, { groupKeys: ['c20'] })
    expect(field(root, '选择范围', 'ElRadioGroup').props.modelValue).toBe('PAGE')
    expect(api.inspectPlanTargets).not.toHaveBeenCalled()
    expect(text(root)).not.toContain('检测间隔')
    await patrolScope(root, 'GROUPS')
    ;(field(root, '视频分组', 'ElTree').props.onCheck as (...args: unknown[]) => void)(
      {},
      { checkedKeys: ['3'] },
    )
    await click(root, '下一步')
    await setField(root, '调度方式', 'PARALLEL', 'ElSelect')
    await setField(root, '并发请求数', 2, 'ElSelect')
    await click(root, '下一步')
    await click(root, '下一步')
    await click(root, '启动巡检')
    expect(api.inspectPlanTargets).toHaveBeenCalledWith({ scope: 'GROUPS', groupIds: ['3'] })
    expect(api.createStatusPlan).toHaveBeenCalledWith(
      expect.objectContaining({
        groupIds: ['3'],
        groupKeys: ['c10'],
        layer: 'CONTROL',
        dispatchMode: 'PARALLEL',
        parallelism: 2,
      }),
    )
  })
  it('resumes an unexpired stopped patrol without creating another plan', async () => {
    const plan = {
      planId: '1',
      state: 'STOPPED',
      targetCount: 1,
      intervalMinutes: 60,
      layer: 'MEDIA',
      dispatchMode: 'POLLING',
      parallelism: 1,
      version: '3',
      startAt: new Date().toISOString(),
      endAt: new Date(Date.now() + 3600000).toISOString(),
    }
    api.getStatusPlans.mockResolvedValue([plan])
    api.resumeStatusPlan.mockResolvedValue({ ...plan, state: 'ACTIVE' })
    const root = await mount(PlanDialog, { groupKeys: [] })
    await patrolTab(root, 'tasks')
    await click(root, '恢复执行')
    expect(api.resumeStatusPlan).toHaveBeenCalledWith(plan)
    expect(api.createStatusPlan).not.toHaveBeenCalled()
  })
  it('keeps the complete tree while selecting a group and can return from pending to all', async () => {
    const rootGroup = {
      groupId: '30',
      parentId: null,
      name: '厂区',
      sortOrder: 0,
      version: '0',
      visibleCameraCount: 2,
    }
    const child = { ...rootGroup, groupId: '31', parentId: '30', name: '车间' }
    api.getGroupTree.mockResolvedValue([rootGroup, child])
    const root = await mount(CameraManage)
    const tree = nodes(root).find((n) => n.type === 'ElTree')!
    expect(tree.props.data).toMatchObject([{ groupId: '30', children: [{ groupId: '31' }] }])
    ;(tree.props.onNodeClick as (v: unknown) => void)(child)
    await settle()
    expect(api.getCameraDevices).toHaveBeenLastCalledWith(
      expect.objectContaining({ groupId: '31', includeDescendants: true, page: 1 }),
    )
    expect(text(root)).toContain('厂区 / 车间')
    await click(root, '待归档')
    expect(api.getCameraDevices).toHaveBeenLastCalledWith(
      expect.objectContaining({ lifecycle: 'PENDING_ASSIGNMENT', groupId: undefined }),
    )
    await click(root, '全部相机')
    expect(api.getCameraDevices).toHaveBeenLastCalledWith(
      expect.objectContaining({ lifecycle: undefined, groupId: undefined }),
    )
    expect(nodes(root).some((n) => n.type === 'ElButton' && text(n) === '预览')).toBe(false)
  })

  it('moves a device once using the server total rather than a filtered channel count', async () => {
    api.getDevicePlacements.mockResolvedValue([
      { groupId: '31', groupPath: '原区域', channelCount: 25 },
    ])
    api.previewDeviceMove.mockResolvedValue({
      groupKey: 'd100',
      placements: [{ groupId: '31', groupPath: '原区域', channelCount: 25 }],
      impact: { ...impact, affectedCameraCount: 25 },
    })
    api.moveDevice.mockResolvedValue(undefined)
    const saved = vi.fn()
    const root = await mount(DeviceMoveDialog, {
      device: { ...device, channelCount: 1 },
      onSaved: saved,
    })
    const tree = nodes(root).find((n) => n.type === 'ElTree')!
    expect(api.getDevicePlacements).toHaveBeenCalledExactlyOnceWith('d100')
    expect(text(root)).toContain('原区域')
    expect(text(root)).toContain('25 个通道')
    expect(tree.props['current-node-key']).toBe('31')
    expect(button(root, '下一步：确认影响').props.disabled).toBe(true)
    ;(tree.props.onNodeClick as (v: unknown) => void)({ groupId: '30' })
    await settle()
    await click(root, '下一步：确认影响')
    expect(api.previewDeviceMove).toHaveBeenCalledWith('d100', '30')
    expect(text(root)).toContain('25 个通道')
    await click(root, '确认移动全部通道')
    expect(api.moveDevice).toHaveBeenCalledExactlyOnceWith('d100', '30', 'proof')
    expect(api.updateCamera).not.toHaveBeenCalled()
    expect(saved).toHaveBeenCalledOnce()
  })

  it.each([false, true])(
    'protects pending connection details from search or reuse exit: %s',
    async (exitReuse) => {
      api.getAccessOptions.mockResolvedValue({
        ready: true,
        methods: ['AUTO', 'DAHUA'],
        adapters: [
          {
            type: 'DAHUA',
            label: '大华设备',
            category: 'DEVICE',
            inputKind: 'DEVICE_LOGIN',
            autoDetect: true,
            paged: false,
          },
        ],
        networkPolicies: [{ key: 'approved', name: '网络' }],
        diagnostics: [],
      })
      let resolveDetail!: (value: unknown) => void
      api.getSources.mockResolvedValue({ items: [], total: 0 })
      api.getSource.mockImplementation(
        () =>
          new Promise((resolve) => {
            resolveDetail = resolve
          }),
      )
      const root = await mount(CreateDialog, {
        initialConnection: { sourceId: '1', sourceVersion: '4', method: 'AUTO' },
      })
      const method = () =>
        nodes(root).find((item) => item.type === 'button' && text(item) === '大华设备')!
      expect(method().props.disabled).toBe(true)
      const selector = field(root, '已保存连接', 'ElSelect')
      await (selector.props['remote-method'] as (name: string) => Promise<void>)(
        'not-in-first-page',
      )
      await settle()
      expect(method().props.disabled).toBe(true)
      const reuse = nodes(root).find(
        (item) => item.type === 'ElCheckbox' && text(item) === '使用已保存连接',
      )!
      if (exitReuse) {
        ;(reuse.props['onUpdate:modelValue'] as (value: boolean) => void)(false)
        await settle()
        expect(method().props.disabled).toBe(false)
      }
      resolveDetail({ ...source, connectionCategory: 'DEVICE', adapterType: null })
      await settle()
      expect(method().props.disabled).toBe(false)
      if (exitReuse) expect(field(root, '设备地址')).toBeDefined()
      else {
        expect(
          nodes(root).some(
            (item) =>
              item.type === 'ElOption' && item.props.value === '1' && item.props.label === '来源',
          ),
        ).toBe(true)
        await (method().props.onClick as () => void)()
        await settle()
        await (
          field(root, '已保存连接', 'ElSelect').props['remote-method'] as (
            name: string,
          ) => Promise<void>
        )('different-name')
        await settle()
        expect(
          nodes(root).some((item) => item.type === 'ElOption' && item.props.value === '1'),
        ).toBe(true)
        await click(root, '读取设备')
        expect(api.createAccessJob.mock.calls[0]![0].connection).toMatchObject({
          sourceId: '1',
          sourceVersion: '4',
          method: 'DAHUA',
        })
      }
    },
  )
  it.each([
    ['DEVICE', null, true],
    ['DEVICE', 'HIKVISION', false],
    ['PLATFORM', 'HIK_PLATFORM', false],
    ['RTSP', 'RTSP', false],
  ])(
    'preserves only an unidentified device when switching methods: %s/%s',
    async (category, adapterType, retained) => {
      api.getAccessOptions.mockResolvedValue({
        ready: true,
        methods: ['AUTO', 'DAHUA'],
        adapters: [
          {
            type: 'DAHUA',
            label: '大华设备',
            category: 'DEVICE',
            inputKind: 'DEVICE_LOGIN',
            autoDetect: true,
            paged: false,
          },
        ],
        networkPolicies: [{ key: 'approved', name: '网络' }],
        diagnostics: [],
      })
      api.getSources.mockResolvedValue({ items: [], total: 0 })
      api.getSource.mockResolvedValue({ ...source, connectionCategory: category, adapterType })
      const root = await mount(CreateDialog, {
        initialConnection: { sourceId: '1', sourceVersion: '4', method: 'AUTO' },
      })
      const method = nodes(root).find(
        (item) => item.type === 'button' && text(item) === '大华设备',
      )!
      await (method.props.onClick as () => void)()
      await settle()
      const searchCalls = api.getSources.mock.calls.length
      await (method.props.onClick as () => void)()
      await settle()
      expect(api.getSources).toHaveBeenCalledTimes(searchCalls)
      await click(root, '读取设备')
      if (retained) {
        expect(api.createAccessJob.mock.calls[0]![0].connection).toMatchObject({
          method: 'DAHUA',
          sourceId: '1',
          sourceVersion: '4',
        })
        expect(api.getSource).toHaveBeenCalledTimes(1)
      } else expect(api.createAccessJob).not.toHaveBeenCalled()
    },
  )
  it('confirms whole-directory scope once and retries an uncertain start with the same request', async () => {
    api.startBulkImport
      .mockRejectedValueOnce(new Error('lost start response'))
      .mockResolvedValueOnce(bulkJob)
    const root = await mount(BulkImportPanel, { initialJob: accessJob, groupId: '30' })
    expect(api.previewBulkImport).toHaveBeenCalledWith('900', { version: '2', groupId: '30' })
    expect(text(root)).toContain('1200')
    expect(api.startBulkImport).not.toHaveBeenCalled()
    await click(root, '确认整批导入')
    expect(button(root, '返回选择').props.disabled).toBe(true)
    await click(root, '重试原启动请求')
    expect(api.startBulkImport.mock.calls[1]).toEqual(api.startBulkImport.mock.calls[0])
    expect(api.startBulkImport.mock.calls[0]![1]).toMatchObject({
      version: '2',
      groupId: '30',
      confirmation: 'bulk-proof',
      clientRequestId: expect.any(String),
    })
    app?.unmount()
    app = null
    expect(api.cancelAccessJob).not.toHaveBeenCalled()
  })
  it('cancels a running bulk task explicitly and retains imported counts in the result', async () => {
    api.getAccessJob.mockResolvedValue(bulkJob)
    api.cancelAccessJob.mockResolvedValue({ ...bulkJob, status: 'CANCELLED' })
    const root = await mount(BulkImportPanel, { initialJob: bulkJob })
    await click(root, '取消导入')
    expect(api.cancelAccessJob).toHaveBeenCalledWith('900')
    expect(text(root)).toContain('已取消')
    expect(text(root)).toContain('75')
    expect(text(root)).toContain('海康平台')
    expect(text(root)).not.toContain('HIK_PLATFORM')
    expect(button(root, '重新读取并重试')).toBeDefined()
  })
  it('does not offer a misleading resume after the whole-directory import limit', async () => {
    const limited = {
      ...bulkJob,
      status: 'PARTIAL',
      diagnostic: { reasonCode: 'IMPORT_LIMIT_REACHED', actionHint: '已达到整批导入上限' },
    }
    api.getAccessJob.mockResolvedValue(limited)
    const root = await mount(BulkImportPanel, { initialJob: limited })
    expect(button(root, '重新读取并重试')).toBeUndefined()
    expect(text(root)).toContain('不会续传剩余目录')
    expect(text(root)).toContain('执行期限')
    expect(text(root)).toContain('75')
  })
  it.each([
    ['CANCELLED_BY_USER', false],
    ['ACTOR_REVOKED', true],
  ])('keeps the correct cancellation guidance for %s', async (reasonCode, visible) => {
    const cancelled = {
      ...bulkJob,
      status: 'CANCELLED',
      diagnostic: { reasonCode, actionHint: 'server diagnostic guidance' },
    }
    api.getAccessJob.mockResolvedValue(cancelled)
    const root = await mount(BulkImportPanel, { initialJob: cancelled })
    expect(text(root).includes('server diagnostic guidance')).toBe(visible)
    expect(text(root)).toContain(reasonCode)
  })
  it('loads saved import tasks and pages failed items without rendering a second dialog', async () => {
    const failed = { ...bulkJob, status: 'PARTIAL', bulk: { ...bulkJob.bulk, failedCount: 2 } }
    api.getImportJobs.mockResolvedValue({ items: [failed], page: 1, size: 20, total: 1 })
    api.getAccessJob.mockResolvedValue(failed)
    api.getImportItems.mockResolvedValue({
      items: [
        {
          id: '1',
          externalKey: 'platform-key',
          name: '失败相机',
          reasonCode: 'CONFLICT',
          pageNumber: 2,
        },
      ],
      page: 1,
      size: 20,
      total: 21,
    })
    const root = await mount(ImportTasksDialog)
    expect(api.getImportJobs).toHaveBeenCalledWith({ page: 1, size: 20 })
    await click(root, '查看')
    await settle()
    await click(root, '失败明细（2）')
    expect(api.getImportItems).toHaveBeenCalledWith('900', { page: 1, size: 20, status: 'FAILED' })
    expect(text(root)).toContain('失败相机')
    expect(nodes(root).filter((item) => item.type === 'ElDialog')).toHaveLength(1)
    const pager = nodes(root).find((item) => item.type === 'ElPagination')!
    ;(pager.props['onUpdate:currentPage'] as (value: number) => void)(2)
    await (pager.props.onCurrentChange as () => Promise<void>)()
    expect(api.getImportItems).toHaveBeenLastCalledWith('900', {
      page: 2,
      size: 20,
      status: 'FAILED',
    })
    await click(root, '返回任务列表')
    await settle()
    expect(api.getImportJobs).toHaveBeenCalledTimes(2)
    expect(api.cancelAccessJob).not.toHaveBeenCalled()
  })
  it.each([false, true])(
    'keeps background import running when the wizard unmounts, unknown response: %s',
    async (responseLost) => {
      if (responseLost) api.startBulkImport.mockRejectedValueOnce(new Error('lost response'))
      api.getAccessOptions.mockResolvedValue({
        ready: true,
        methods: ['HIK_PLATFORM'],
        adapters: [
          {
            type: 'HIK_PLATFORM',
            label: '海康平台',
            category: 'PLATFORM',
            inputKind: 'PLATFORM_APPKEY',
            autoDetect: false,
            paged: true,
          },
        ],
        networkPolicies: [{ key: 'approved', name: '网络' }],
        diagnostics: [],
      })
      api.getAccessJob.mockResolvedValue({
        ...accessJob,
        method: 'HIK_PLATFORM',
        page: { pageNumber: 1, pageSize: 100, total: 1200, hasMore: true },
      })
      const saved = vi.fn(),
        root = await mount(CreateDialog, {
          mode: 'platform',
          initialConnection: { sourceId: '70', sourceVersion: '9', method: 'HIK_PLATFORM' },
          onSaved: saved,
        })
      await openPlatformRegion(root)
      await click(root, '导入当前区域全部相机')
      await settle()
      expect(nodes(root).filter((item) => item.type === 'ElDialog')).toHaveLength(1)
      await click(root, '确认整批导入')
      if (responseLost) expect(button(root, '重试原启动请求')).toBeDefined()
      else {
        await click(root, '完成')
        expect(saved).toHaveBeenCalledWith([])
      }
      app?.unmount()
      app = null
      expect(api.cancelAccessJob).not.toHaveBeenCalled()
    },
  )
  it('restarts a page-level failed bulk import with the same saved platform and destination', async () => {
    api.getAccessOptions.mockResolvedValue({
      ready: true,
      methods: ['HIK_PLATFORM'],
      adapters: [
        {
          type: 'HIK_PLATFORM',
          label: '海康平台',
          category: 'PLATFORM',
          inputKind: 'PLATFORM_APPKEY',
          autoDetect: false,
          paged: true,
        },
      ],
      networkPolicies: [{ key: 'approved', name: '网络' }],
      diagnostics: [],
    })
    api.createAccessJob
      .mockResolvedValueOnce({ jobId: '900' })
      .mockResolvedValueOnce({ jobId: '902' })
    api.getAccessJob.mockImplementation(async (jobId: string) => ({
      ...accessJob,
      jobId,
      method: 'HIK_PLATFORM',
      page: { pageNumber: 1, pageSize: 100, total: 1200, hasMore: true },
    }))
    api.startBulkImport.mockResolvedValue({
      ...bulkJob,
      status: 'FAILED',
      diagnostic: { reasonCode: 'DIRECTORY_INCOMPLETE', actionHint: '请重试目录读取' },
    })
    const root = await mount(CreateDialog, {
      mode: 'platform',
      initialConnection: { sourceId: '70', sourceVersion: '9', method: 'HIK_PLATFORM' },
      initialGroupId: '30',
    })
    await openPlatformRegion(root)
    await click(root, '导入当前区域全部相机')
    await settle()
    await click(root, '确认整批导入')
    expect(text(root)).toContain('导入中断')
    await click(root, '重新读取并重试')
    await settle()
    await selectPlatformRegion(root)
    expect(api.createAccessJob.mock.calls[1]![0].connection).toEqual({
      method: 'HIK_PLATFORM',
      sourceId: '70',
      sourceVersion: '9',
      pageNumber: 1,
      pageSize: 100,
      regionIndexCode: 'region-A',
      includeSubRegions: false,
    })
    await click(root, '导入当前区域全部相机')
    await settle()
    expect(api.previewBulkImport).toHaveBeenLastCalledWith('902', { version: '2', groupId: '30' })
  })
  it('offers three management operations and saves an unknown device without access jobs or profiles', async () => {
    api.getAccessOptions.mockResolvedValue({
      ready: false,
      methods: [],
      adapters: [
        {
          type: 'EXTENSION_CAMERA',
          label: '已安装设备驱动',
          category: 'DEVICE',
          inputKind: 'DEVICE_LOGIN',
          autoDetect: false,
          paged: false,
        },
      ],
      networkPolicies: [],
      diagnostics: ['未配置联网策略'],
    })
    const root = await mount(CameraManage)
    expect(button(root, '手动添加')).toBeDefined()
    expect(button(root, '网段搜索')).toBeDefined()
    expect(button(root, '平台导入')).toBeDefined()
    await click(root, '手动添加')
    await setField(
      nodes(root).find((item) => item.type === 'ElDialog')!,
      '相机名称',
      '设备档案',
    )
    await setField(root, '设备地址', '192.0.2.20')
    await click(root, '保存相机档案')
    expect(api.createCamera.mock.calls[0]![0]).toEqual({
      clientRequestId: expect.any(String),
      name: '设备档案',
      remark: null,
      connection: { host: '192.0.2.20', port: 80, scheme: 'http' },
    })
    expect(api.getAccessOptions).toHaveBeenCalledOnce()
    expect(api.createAccessJob).not.toHaveBeenCalled()
    expect(text(root)).toContain('已保存为待归档')
    await click(root, '完成')
    await settle()
    expect(nodes(root).filter((item) => item.type === 'ElDialog')).toHaveLength(0)
    expect(api.getCamera).not.toHaveBeenCalled()
  })
  it('reports missing storage keys separately from network-policy readiness even without credentials', async () => {
    const root = await mount(ManualCreateDialog, { storageReady: false })
    await setField(root, '相机名称', '手工登记')
    await setField(root, '设备地址', '192.0.2.20')
    await setField(root, '设备账号', 'operator')
    await setField(root, '设备密码', 'example-only')
    await click(root, '保存相机档案')
    expect(text(root)).toContain('部署密钥尚未配置')
    expect(api.createCamera).not.toHaveBeenCalled()
    await setField(root, '设备账号', '')
    await setField(root, '设备密码', '')
    await click(root, '保存相机档案')
    expect(api.createCamera).not.toHaveBeenCalled()
    expect(api.createAccessJob).not.toHaveBeenCalled()
  })
  it('keeps a created manual camera when group placement is cancelled and never resubmits creation', async () => {
    const saved = vi.fn(),
      root = await mount(ManualCreateDialog, { onSaved: saved })
    await setField(root, '相机名称', '待归档档案')
    await setField(root, '设备地址', '192.0.2.20')
    await setField(root, '视频分组', '30', 'ElTreeSelect')
    await click(root, '保存相机档案')
    await settle()
    expect(nodes(root).filter((item) => item.type === 'ElDialog')).toHaveLength(1)
    expect(button(root, '保存相机档案')).toBeUndefined()
    expect(api.createCamera).toHaveBeenCalledOnce()
    await click(root, '取消')
    await click(root, '完成')
    expect(saved).toHaveBeenCalledWith(['10'])
    expect(api.updateCamera).not.toHaveBeenCalled()
    expect(api.createCamera).toHaveBeenCalledOnce()
  })
  it('switches camera connection and profile editing inside the detail container', async () => {
    api.getCamera.mockResolvedValue({ ...camera, sourceId: '1', sourceVersion: '4' })
    const root = await mount(DetailDialog, { cameraId: '10', canManage: true })
    await click(root, '连接设置')
    await settle()
    expect(nodes(root).filter((item) => item.type === 'ElDialog')).toHaveLength(1)
    expect(field(root, '连接名称')).toBeDefined()
    expect(button(root, '保存本地资料')).toBeUndefined()
    await click(root, '关闭')
    await click(root, '编辑')
    await settle()
    expect(nodes(root).filter((item) => item.type === 'ElDialog')).toHaveLength(1)
    expect(field(root, '码流标签')).toBeDefined()
    expect(button(root, '保存本地资料')).toBeUndefined()
  })
  it('protects an unsaved camera draft from reload and shared settings until saved or reverted', async () => {
    api.getCamera.mockResolvedValue({ ...camera, sourceId: '1', sourceVersion: '4' })
    const root = await mount(DetailDialog, { cameraId: '10', canManage: true })
    await setField(root, '通道名称', '未保存的名称')
    expect(button(root, '刷新资料').props.disabled).toBe(true)
    expect(button(root, '连接设置').props.disabled).toBe(true)
    expect(button(root, '编辑').props.disabled).toBe(true)
    expect(button(root, '删除').props.disabled).toBe(true)
    await click(root, '刷新资料')
    expect(api.getCamera).toHaveBeenCalledOnce()
    expect(field(root, '通道名称').props.modelValue).toBe('未保存的名称')
    await click(root, '撤销修改')
    expect(field(root, '通道名称').props.modelValue).toBe(camera.name)
    expect(button(root, '连接设置').props.disabled).toBe(false)
    await click(root, '编辑')
    await settle()
    expect(field(root, '码流标签')).toBeDefined()
  })
  it('keeps local changes after a rejected save and resets the draft only after a successful save', async () => {
    const root = await mount(DetailDialog, { cameraId: '10', canManage: true })
    await setField(root, '通道名称', ' 新名称 ')
    api.updateCamera.mockRejectedValueOnce(new ApiRequestError('VERSION_CONFLICT', '资料已更新'))
    await click(root, '保存本地资料')
    expect(field(root, '通道名称').props.modelValue).toBe(' 新名称 ')
    expect(button(root, '编辑').props.disabled).toBe(true)
    api.updateCamera.mockResolvedValueOnce({ ...camera, name: '新名称', version: '4' })
    await click(root, '保存本地资料')
    expect(field(root, '通道名称').props.modelValue).toBe('新名称')
    expect(button(root, '撤销修改')).toBeUndefined()
    expect(button(root, '编辑').props.disabled).toBe(false)
  })
  it('requires explicit discard to close a dirty camera detail from either close entry', async () => {
    const closed = vi.fn()
    const root = await mount(DetailDialog, { cameraId: '10', canManage: true, onClose: closed })
    await setField(root, '备注', '保留这段草稿')
    api.confirm.mockRejectedValueOnce('cancel')
    await click(root, '关闭')
    expect(closed).not.toHaveBeenCalled()
    expect(field(root, '备注').props.modelValue).toBe('保留这段草稿')
    const dialog = nodes(root).find((item) => item.type === 'ElDialog')!
    await (dialog.props['before-close'] as () => Promise<void>)()
    expect(api.confirm).toHaveBeenCalledTimes(2)
    expect(closed).toHaveBeenCalledOnce()
    expect(api.updateCamera).not.toHaveBeenCalled()
  })
  it('edits a saved connection in the access container and returns without opening another dialog', async () => {
    const root = await mount(CreateDialog, {
      mode: 'rtsp',
      initialConnection: { sourceId: '1', sourceVersion: '4', method: 'RTSP' },
    })
    await click(root, '连接设置')
    await settle()
    expect(nodes(root).filter((item) => item.type === 'ElDialog')).toHaveLength(1)
    expect(button(root, '解析视频地址')).toBeUndefined()
    await click(root, '关闭')
    await settle()
    expect(button(root, '解析视频地址')).toBeDefined()
    expect(nodes(root).filter((item) => item.type === 'ElDialog')).toHaveLength(1)
  })
  it('retries unknown manual-save results with the original payload and releases an explicit rejection', async () => {
    api.createCamera
      .mockRejectedValueOnce(new Error('network'))
      .mockRejectedValueOnce(
        new ApiRequestError('VALIDATION_ERROR', 'invalid host', { status: 400 }),
      )
      .mockResolvedValueOnce({ cameraId: '10' })
    const root = await mount(ManualCreateDialog)
    await setField(root, '相机名称', '档案')
    await setField(root, '设备地址', 'bad-host')
    await click(root, '保存相机档案')
    await click(root, '重试原保存')
    expect(api.createCamera.mock.calls[1]).toEqual(api.createCamera.mock.calls[0])
    await setField(root, '设备地址', '192.0.2.20')
    await click(root, '保存相机档案')
    expect(api.createCamera.mock.calls[2]![0].clientRequestId).not.toBe(
      api.createCamera.mock.calls[0]![0].clientRequestId,
    )
    expect(api.createCamera.mock.calls[2]![0].connection.host).toBe('192.0.2.20')
  })
  it('edits an unbound manual connection with no network policy and preserves credential KEEP semantics', async () => {
    api.getSourceOptions.mockResolvedValue({
      ready: false,
      adapterTypes: [],
      credentialPurposes: ['DEVICE_HTTP'],
      networkPolicies: [],
    })
    api.getSource.mockResolvedValue({
      ...source,
      adapterType: null,
      connectionCategory: 'DEVICE',
      networkPolicyKey: null,
      rtspPort: null,
      endpointEditable: true,
      credentials: [{ purpose: 'DEVICE_HTTP', configured: true }],
      endpoints: [
        {
          ...source.endpoints[0],
          purpose: 'DEVICE_HTTP',
          scheme: 'http',
          port: 80,
          authMode: 'DRIVER_NEGOTIATED',
          credentialPurpose: 'DEVICE_HTTP',
        },
      ],
    })
    const root = await mount(SourceEditor, { sourceId: '1' })
    await setField(root, '连接名称', '手工连接新名称')
    await click(root, '保存配置')
    expect(api.updateSource).toHaveBeenCalledWith('1', { version: '4', name: '手工连接新名称' })
  })
  it('imports a catalog channel without profiles and binds it explicitly to a saved manual camera', async () => {
    api.getAccessOptions.mockResolvedValue({
      ready: true,
      methods: ['AUTO', 'ONVIF'],
      adapters: [
        {
          type: 'ONVIF',
          label: 'ONVIF设备',
          category: 'DEVICE',
          inputKind: 'DEVICE_LOGIN',
          autoDetect: true,
          paged: false,
        },
      ],
      networkPolicies: [{ key: 'approved', name: '批准网络' }],
      diagnostics: [],
    })
    api.getAccessJob.mockResolvedValue({
      ...accessJob,
      method: 'ONVIF',
      candidates: [
        { candidateId: 'channel-a', name: '设备通道', mappingRequired: false, profiles: [] },
      ],
    })
    const root = await mount(CreateDialog, {
      initialConnection: { sourceId: '1', sourceVersion: '4', method: 'AUTO' },
      targetCamera: { cameraId: '10', version: '3', name: '既有档案' },
    })
    await click(root, '读取设备')
    expect(text(root)).toContain('暂无码流资料')
    expect(text(root)).toContain('已选 0 个')
    expect(button(root, '选择可导入通道')).toBeUndefined()
    const select = nodes(root).find(
      (item) => item.type === 'ElCheckbox' && text(item) === '设备通道',
    )!
    ;(select.props['onUpdate:modelValue'] as (value: boolean) => void)(true)
    await nextTick()
    expect(
      nodes(root).filter((item) => item.type === 'ElFormItem' && item.props.label === '默认码流'),
    ).toHaveLength(0)
    await click(root, '查看导入影响')
    expect(api.previewImport).toHaveBeenCalledWith('900', {
      version: '2',
      targetCameraId: '10',
      targetCameraVersion: '3',
      selections: [{ candidateId: 'channel-a', profileIds: [] }],
    })
  })
  it('preserves successful scan registrations and retries only an uncertain row with its original request', async () => {
    api.getAccessJob.mockResolvedValue({
      ...accessJob,
      kind: 'SCAN',
      candidates: [],
      hosts: [
        {
          candidateId: 'h1',
          host: '192.0.2.1',
          openPorts: [80],
          identityConfidence: 'PORT_OPEN_ONLY',
        },
        {
          candidateId: 'h2',
          host: '192.0.2.2',
          openPorts: [80],
          identityConfidence: 'PORT_OPEN_ONLY',
        },
      ],
      scannedTargets: 2,
      totalTargets: 2,
    })
    api.createCamera
      .mockResolvedValueOnce({ cameraId: '11' })
      .mockRejectedValueOnce(new Error('lost'))
      .mockResolvedValueOnce({ cameraId: '12' })
    const root = await mount(ScanDialog)
    await setField(root, 'IPv4 网段', '192.0.2.0/31')
    await click(root, '开始搜索')
    expect(api.createCamera).not.toHaveBeenCalled()
    expect(text(root)).toContain('端口可达，未识别设备')
    const choices = nodes(root).filter((item) => item.type === 'ElCheckbox')
    choices.forEach((item) => (item.props['onUpdate:modelValue'] as (v: boolean) => void)(true))
    const names = nodes(root).filter(
      (item) => item.type === 'ElInput' && item.props.placeholder === '确认是相机后填写名称',
    )
    names.forEach((item, index) =>
      (item.props['onUpdate:modelValue'] as (v: string) => void)(`已确认相机${index + 1}`),
    )
    await nextTick()
    await click(root, '保存勾选的相机')
    expect(api.createCamera).toHaveBeenCalledTimes(2)
    expect(text(root)).toContain('已保存，待归档')
    await click(root, '保存勾选的相机')
    expect(api.createCamera).toHaveBeenCalledTimes(3)
    expect(api.createCamera.mock.calls[2]).toEqual(api.createCamera.mock.calls[1])
    expect(api.createCamera.mock.calls[0]![0]).not.toHaveProperty('profiles')
    expect(api.createAccessJob).not.toHaveBeenCalled()
  })
  it('uses stable saved platform identity after import and discards previous-page selections', async () => {
    api.createAccessJob
      .mockResolvedValueOnce({ jobId: '900' })
      .mockRejectedValueOnce(new Error('page response lost'))
      .mockResolvedValueOnce({ jobId: '902' })
    api.getAccessOptions.mockResolvedValue({
      ready: true,
      methods: ['HIK_PLATFORM'],
      adapters: [
        {
          type: 'HIK_PLATFORM',
          label: '已注册平台',
          category: 'PLATFORM',
          inputKind: 'PLATFORM_APPKEY',
          autoDetect: false,
          paged: true,
        },
      ],
      networkPolicies: [{ key: 'approved', name: '批准网络' }],
      diagnostics: [],
    })
    api.getAccessJob
      .mockResolvedValueOnce({
        ...accessJob,
        method: 'HIK_PLATFORM',
        candidates: [{ candidateId: 'p1', name: '平台相机', mappingRequired: false, profiles: [] }],
        page: { pageNumber: 1, pageSize: 100, total: 101, hasMore: true },
      })
      .mockResolvedValueOnce({
        ...accessJob,
        jobId: '902',
        method: 'HIK_PLATFORM',
        candidates: [],
        page: { pageNumber: 2, pageSize: 100, total: 101, hasMore: false },
      })
    api.importCandidates.mockResolvedValueOnce({
      sourceId: '70',
      sourceVersion: '9',
      createdCount: 1,
      existingCount: 0,
      cameras: [{ candidateId: 'p1', cameraId: '71', version: '0', status: 'CREATED' }],
    })
    const root = await mount(CreateDialog, { mode: 'platform' })
    await setField(root, '平台地址', 'platform.test')
    await setField(root, 'AppKey', 'test-key')
    await setField(root, 'AppSecret', 'test-secret')
    await openPlatformRegion(root, false)
    await click(root, '选择可导入通道')
    await click(root, '查看导入影响')
    await click(root, '确认导入')
    await click(root, '下一页')
    expect(button(root, '重试本页读取请求')).toBeDefined()
    await click(root, '重试本页读取请求')
    expect(api.createAccessJob.mock.calls[2]).toEqual(api.createAccessJob.mock.calls[1])
    expect(api.createAccessJob.mock.calls[1]![0].connection).toEqual({
      method: 'HIK_PLATFORM',
      sourceId: '70',
      sourceVersion: '9',
      pageNumber: 2,
      pageSize: 100,
      regionIndexCode: 'region-A',
      includeSubRegions: false,
    })
    expect(text(root)).toContain('已选 0 个')
    expect(api.createAccessJob.mock.calls[1]![0]).not.toHaveProperty('password')
  })
  it('loads device details only from the row action and switches channels without nesting tables', async () => {
    api.getDeviceChannels.mockResolvedValue({
      items: [camera, { ...camera, cameraId: '11', name: '通道二' }],
      total: 2,
      page: 1,
      size: 20,
    })
    const root = await mount(CameraManage)
    expect(api.getCameraDevices).toHaveBeenCalledOnce()
    expect(api.getCameras).not.toHaveBeenCalled()
    expect(api.getDeviceChannels).not.toHaveBeenCalled()
    expect(nodes(root).filter((item) => item.type === 'ElTable')).toHaveLength(1)
    await click(root, '详情')
    await settle()
    expect(api.getDeviceChannels).toHaveBeenCalledWith(
      'd100',
      expect.objectContaining({ page: 1, size: 20 }),
    )
    expect(
      nodes(root).filter(
        (item) => item.type === 'ElTable' && item.props['row-key'] === 'groupKey',
      )[0]?.props.data,
    ).toHaveLength(1)
    expect(text(root)).toContain('通道二')
    expect(nodes(root).filter((item) => item.type === 'ElTable')).toHaveLength(1)
    api.getCamera.mockResolvedValueOnce({
      ...camera,
      cameraId: '11',
      profiles: [{ ...profile, label: '通道二码流' }],
    })
    await click(root, '通道二')
    await settle()
    expect(api.getCamera).toHaveBeenLastCalledWith('11')
    expect(text(root)).toContain('通道二码流')
    await click(root, '编辑通道')
    await settle()
    expect(nodes(root).filter((item) => item.type === 'ElDrawer')).toHaveLength(0)
    expect(nodes(root).filter((item) => item.type === 'ElDialog')).toHaveLength(1)
    await click(root, '关闭')
    await settle()
    expect(nodes(root).filter((item) => item.type === 'ElDrawer')).toHaveLength(1)
  })
  it('keeps the latest channel selection when an earlier detail response arrives late', async () => {
    api.getDeviceChannels.mockResolvedValue({
      items: [camera, { ...camera, cameraId: '11', name: '通道二' }],
      total: 2,
      page: 1,
      size: 20,
    })
    const root = await mount(DeviceDetailPanel, { device, canManage: true })
    let finish!: (value: unknown) => void
    api.getCamera.mockImplementationOnce(
      () =>
        new Promise((resolve) => {
          finish = resolve
        }),
    )
    const selecting = (button(root, '通道二').props.onClick as () => Promise<void>)()
    await nextTick()
    api.getCamera.mockResolvedValueOnce({
      ...camera,
      profiles: [{ ...profile, label: '当前通道码流' }],
    })
    await click(root, camera.name)
    finish({ ...camera, cameraId: '11', profiles: [{ ...profile, label: '迟到的另一个通道' }] })
    await selecting
    await settle()
    expect(text(root)).toContain('当前通道码流')
    expect(text(root)).not.toContain('迟到的另一个通道')
  })
  it('returns to the remaining channel page after deleting the last item on page two', async () => {
    api.getDeviceChannels.mockResolvedValue({ items: [camera], total: 21, page: 1, size: 20 })
    const root = await mount(DeviceDetailPanel, { device, canManage: true })
    const pager = nodes(root).find((item) => item.type === 'ElPagination')!
    ;(pager.props['onUpdate:currentPage'] as (value: number) => void)(2)
    await (pager.props.onCurrentChange as () => Promise<void>)()
    api.getDeviceChannels
      .mockResolvedValueOnce({ items: [], total: 20, page: 2, size: 20 })
      .mockResolvedValueOnce({
        items: [{ ...camera, name: '剩余通道' }],
        total: 20,
        page: 1,
        size: 20,
      })
    await click(root, '删除通道')
    expect(api.getDeviceChannels).toHaveBeenLastCalledWith(
      'd100',
      expect.objectContaining({ page: 1 }),
    )
    expect(text(root)).toContain('剩余通道')
  })
  it('uses one platform selector and blocks unimplemented DSS and ICC presets', async () => {
    api.getAccessOptions.mockResolvedValue({
      ready: true,
      methods: ['HIK_PLATFORM'],
      adapters: [
        {
          type: 'HIK_PLATFORM',
          label: '海康 ISC / Artemis',
          category: 'PLATFORM',
          inputKind: 'PLATFORM_APPKEY',
          autoDetect: false,
          paged: true,
        },
      ],
      platformPresets: [
        {
          type: 'HIK_PLATFORM',
          label: '海康 ISC / Artemis',
          apiFamily: '资源 API v2',
          available: true,
          status: '可读取目录',
        },
        {
          type: 'DAHUA_DSS',
          label: '大华 DSS',
          apiFamily: '版本待确定',
          available: false,
          status: '预设，尚未适配',
        },
        {
          type: 'DAHUA_ICC',
          label: '大华 ICC',
          apiFamily: '版本待确定',
          available: false,
          status: '预设，尚未适配',
        },
      ],
      networkPolicies: [],
      diagnostics: [],
    })
    const root = await mount(CreateDialog, { mode: 'platform' })
    const selector = nodes(root).find(
      (item) => item.type === 'ElSelect' && item.props.id === 'camera-platform-product',
    )!
    await (selector.props.onChange as (value: string) => void)('DAHUA_ICC')
    await settle()
    expect(text(root)).toContain('尚未适配')
    expect(button(root, '保存密钥并打开目录').props.disabled).toBe(true)
    expect(
      nodes(root).some(
        (item) => item.type === 'ElInput' && item.props.autocomplete === 'new-password',
      ),
    ).toBe(false)
    await click(root, '保存密钥并打开目录')
    expect(api.createAccessJob).not.toHaveBeenCalled()
  })
  it('shows inferred roles without turning a label edit into a manual role override', async () => {
    api.getCamera.mockResolvedValue({
      ...camera,
      profiles: [
        {
          ...profile,
          usageHint: 'UNKNOWN',
          classification: { usageHint: 'MAIN', origin: 'NAME_RULE', rule: 'ONVIF_STREAM_NAME_V1' },
        },
      ],
    })
    const root = await mount(ProfileEditor, { cameraId: '10', profileId: '20' })
    expect(text(root)).toContain('自动识别为主码流')
    expect(field(root, '用途', 'ElSelect').props.modelValue).toBe('MAIN')
    await setField(root, '码流标签', '业务名称')
    await click(root, '保存')
    expect(api.updateProfile.mock.calls[0]![2]).not.toHaveProperty('usageHint')
  })
  it('saves an explicit usage correction after prefilling an inferred role', async () => {
    api.getCamera.mockResolvedValue({
      ...camera,
      profiles: [
        {
          ...profile,
          usageHint: 'UNKNOWN',
          classification: { usageHint: 'MAIN', origin: 'NAME_RULE' },
        },
      ],
    })
    const root = await mount(ProfileEditor, { cameraId: '10', profileId: '20' })
    await setField(root, '用途', 'SUB', 'ElSelect')
    await click(root, '保存')
    expect(api.updateProfile).toHaveBeenCalledWith('10', '20', { version: '2', usageHint: 'SUB' })
  })
  it('edits the device name with its own version while preserving channels and the shared source', async () => {
    const saved = vi.fn()
    const root = await mount(DeviceEditor, { device, onSaved: saved })
    expect(field(root, '相机名称').props.modelValue).toBe('设备一')
    expect(field(root, '备注').props.modelValue).toBe('北门')
    await setField(root, '相机名称', '入口相机')
    await click(root, '保存')
    expect(api.updateCameraDevice).toHaveBeenCalledExactlyOnceWith('100', {
      version: '4',
      localName: '入口相机',
    })
    expect(api.updateCamera).not.toHaveBeenCalled()
    expect(api.updateSource).not.toHaveBeenCalled()
    expect(saved).toHaveBeenCalledWith('入口相机')
  })
  it('keeps unidentified camera edits on their single record and protects unsaved changes', async () => {
    const closed = vi.fn()
    const root = await mount(DeviceEditor, {
      device: { ...device, groupKey: 'c10', identified: false },
      onClose: closed,
    })
    await setField(root, '相机名称', '独立相机')
    api.confirm.mockRejectedValueOnce('cancel')
    await click(root, '取消')
    expect(closed).not.toHaveBeenCalled()
    await click(root, '保存')
    expect(api.updateCamera).toHaveBeenCalledWith('10', { version: '3', name: '独立相机' })
    expect(api.updateCameraDevice).not.toHaveBeenCalled()
  })
  it('blocks moving when current device placements cannot be read', async () => {
    api.getDevicePlacements.mockRejectedValue(new ApiRequestError('NETWORK_ERROR', '读取归属失败'))
    const root = await mount(DeviceMoveDialog, { device })
    expect(button(root, '下一步：确认影响').props.disabled).toBe(true)
    expect(nodes(root).some((n) => n.type === 'ElTree')).toBe(false)
    expect(api.previewDeviceMove).not.toHaveBeenCalled()
  })
  it('hides channel counts by default and shows only the source brand', async () => {
    api.getCameraDevices.mockResolvedValue({
      items: [{ ...device, sourceType: 'HIKVISION', sourceDisplayName: '不应展示的来源名称' }],
      total: 1,
    })
    const root = await mount(CameraManage)
    const labels = nodes(root)
      .filter((node) => node.type === 'ElTableColumn')
      .map((node) => node.props.label)
    expect(labels).toContain('品牌')
    expect(labels).not.toContain('可见通道')
    expect(labels).toContain('连接状态')
    expect(labels).toContain('IP / 主机')
    expect(labels).toContain('端口')
    expect(labels).not.toContain('配置状态')
    expect(labels).not.toContain('通道状态')
    expect(text(root)).toContain('未检测')
    expect(text(root)).not.toContain('2 启用')
    expect(text(root)).toContain('海康')
    expect(text(root)).not.toContain('不应展示的来源名称')
    expect(text(root)).not.toContain('海康设备')
  })
  it('shows addresses on management cards and detail without reading each row separately', async () => {
    const accessAddress = { kind: 'DEVICE', host: '10.0.2.8', port: 80 }
    api.getCameraDevices.mockResolvedValue({ items: [{ ...device, accessAddress }], total: 1 })
    api.getCamera.mockResolvedValue({ ...camera, accessAddress })
    const root = await mount(CameraManage)
    expect(text(root)).toContain('10.0.2.8:80')
    expect(api.getCamera).not.toHaveBeenCalled()
    await click(root, '详情')
    await settle()
    expect(text(root)).toContain('IP / 主机')
    expect(text(root)).toContain('10.0.2.8:80')
  })
  it('identifies a platform address instead of claiming a physical camera IP', async () => {
    api.getCameraDevices.mockResolvedValue({
      items: [{ ...device, accessAddress: { kind: 'PLATFORM', host: 'isc.example', port: 443 } }],
      total: 1,
    })
    const root = await mount(CameraManage)
    expect(text(root)).toContain('平台地址')
    expect(text(root)).toContain('isc.example:443')
  })
  it('opens state in the same device drawer rather than navigating away', async () => {
    api.getCameraDevices.mockResolvedValue({
      items: [
        {
          ...device,
          statusSummary: {
            state: 'FAILED',
            checkedAt: new Date().toISOString(),
            validUntil: new Date(Date.now() + 60000).toISOString(),
            reachableCount: 0,
            failedCount: 1,
            unknownCount: 0,
          },
        },
      ],
      total: 1,
    })
    const root = await mount(CameraManage)
    await click(root, '连接异常')
    await settle()
    expect(text(root)).toContain('IP / 主机')
    expect(api.getDeviceChannels).toHaveBeenCalledWith(device.groupKey, { page: 1, size: 20 })
    expect(text(root)).not.toContain('查看状态')
    expect(api.createStatusPlan).not.toHaveBeenCalled()
  })
  it('creates patrol only on explicit action and retries an uncertain request with the same key', async () => {
    api.inspectPlanTargets.mockResolvedValue({
      targets: [{ groupKey: 'd100', name: '入口', reasonCode: null }],
      limitExceeded: false,
    })
    api.getStatusPlans.mockResolvedValue([])
    api.createStatusPlan.mockRejectedValueOnce(new Error('lost')).mockResolvedValue({ planId: '1' })
    const root = await mount(PlanDialog, { groupKeys: ['d100'] })
    await choosePatrolPage(root)
    expect(api.createStatusPlan).not.toHaveBeenCalled()
    await click(root, '启动巡检')
    expect(text(root)).toContain('响应未确认')
    expect(button(root, '上一步').props.disabled).toBe(true)
    await click(root, '重试原计划')
    expect(api.createStatusPlan.mock.calls[1]).toEqual(api.createStatusPlan.mock.calls[0])
    expect(api.createStatusPlan.mock.calls[0]![0]).toMatchObject({
      groupKeys: ['d100'],
      intervalMinutes: 60,
      durationHours: 24,
    })
  })
  it('creates a sequential video plan only after completing the dialog steps', async () => {
    api.getStatusPlans.mockResolvedValue([])
    api.inspectPlanTargets.mockResolvedValue({
      targets: [{ groupKey: 'd100', name: '入口', reasonCode: null }],
      limitExceeded: false,
    })
    api.createStatusPlan.mockResolvedValue({ planId: '1' })
    const root = await mount(PlanDialog, { groupKeys: ['d100'] })
    await click(root, '下一步')
    expect(field(root, '调度方式', 'ElSelect')).toBeDefined()
    expect(text(root)).not.toContain('检测间隔')
    await setField(root, '调度方式', 'PARALLEL', 'ElSelect')
    await setField(root, '并发请求数', 2, 'ElSelect')
    await click(root, '下一步')
    await setField(root, '连接通过后检查视频', true, 'ElSwitch')
    expect(text(root)).toContain('连接异常则跳过')
    expect(api.createStatusPlan).not.toHaveBeenCalled()
    await click(root, '上一步')
    expect(field(root, '并发请求数', 'ElSelect').props.disabled).toBe(true)
    await click(root, '下一步')
    await click(root, '下一步')
    expect(field(root, '检测间隔', 'ElSelect')).toBeDefined()
    await click(root, '启动巡检')
    expect(api.createStatusPlan).toHaveBeenCalledWith(
      expect.objectContaining({
        layer: 'CONTROL',
        checkVideo: true,
        parallelism: 1,
        intervalMinutes: 60,
      }),
    )
  })
  it('keeps camera selections across list pages and back navigation without creating a task', async () => {
    api.getStatusPlans.mockResolvedValue([])
    api.getCameraDevices
      .mockResolvedValueOnce({ items: [device], total: 11 })
      .mockResolvedValue({ items: [{ ...device, groupKey: 'd200', name: '设备二' }], total: 11 })
    api.inspectPlanTargets.mockResolvedValue({
      targets: [
        { groupKey: 'd100', name: '设备一', reasonCode: null },
        { groupKey: 'd200', name: '设备二', reasonCode: null },
      ],
      limitExceeded: false,
    })
    const root = await mount(PlanDialog, { groupKeys: [] })
    const checkbox = () => nodes(root).find((item) => item.type === 'ElCheckbox')!
    ;(checkbox().props['onUpdate:modelValue'] as (value: boolean) => void)(true)
    await settle()
    const pager = nodes(root).find((item) => item.type === 'ElPagination')!
    ;(pager.props['onUpdate:currentPage'] as (value: number) => void)(2)
    await (pager.props.onCurrentChange as () => Promise<void>)()
    await settle()
    expect(api.getCameraDevices).toHaveBeenLastCalledWith(
      expect.objectContaining({ page: 2, size: 10, lifecycle: 'ENABLED' }),
    )
    ;(checkbox().props['onUpdate:modelValue'] as (value: boolean) => void)(true)
    await settle()
    expect(text(root)).toContain('已选 2 台')
    await click(root, '下一步')
    expect(api.inspectPlanTargets).toHaveBeenCalledWith({
      scope: 'PAGE',
      groupKeys: ['d100', 'd200'],
    })
    expect(field(root, '调度方式', 'ElSelect')).toBeDefined()
    expect(text(root)).not.toContain('检测间隔')
    await click(root, '上一步')
    expect(text(root)).toContain('已选 2 台')
    expect(api.createStatusPlan).not.toHaveBeenCalled()
  })
  it('ignores a late camera search response and prevents changes when the picker is locked', async () => {
    let finish!: (value: unknown) => void
    api.getCameraDevices
      .mockReturnValueOnce(
        new Promise((resolve) => {
          finish = resolve
        }),
      )
      .mockResolvedValue({ items: [{ ...device, name: '新查询' }], total: 1 })
    const update = vi.fn()
    const root = await mount(PlanCameraPicker, {
      modelValue: [],
      disabled: true,
      'onUpdate:modelValue': update,
    })
    await click(root, '查询')
    finish({ items: [{ ...device, name: '旧查询' }], total: 1 })
    await settle()
    expect(text(root)).toContain('新查询')
    expect(text(root)).not.toContain('旧查询')
    const checkbox = nodes(root).find((item) => item.type === 'ElCheckbox')!
    ;(checkbox.props['onUpdate:modelValue'] as (value: boolean) => void)(true)
    expect(update).not.toHaveBeenCalled()
  })
  it('blocks a patrol period shorter than its interval after step navigation', async () => {
    api.getStatusPlans.mockResolvedValue([])
    api.inspectPlanTargets.mockResolvedValue({
      targets: [{ groupKey: 'd100', name: '设备', reasonCode: null }],
      limitExceeded: false,
    })
    const root = await mount(PlanDialog, { groupKeys: ['d100'] })
    await choosePatrolPage(root)
    await setField(root, '检测间隔', 1440, 'ElSelect')
    await setField(root, '持续时间', 1, 'ElSelect')
    expect(button(root, '启动巡检').props.disabled).toBe(true)
    await click(root, '启动巡检')
    expect(api.createStatusPlan).not.toHaveBeenCalled()
    expect(text(root)).toContain('持续时间不能小于检测间隔')
  })
  it('keeps the wider patrol dialog free of redundant notes without enabling an empty task', async () => {
    api.getStatusPlans.mockResolvedValue([])
    const root = await mount(PlanDialog, { groupKeys: [] })
    expect(nodes(root).find((item) => item.type === 'ElDialog')?.props.width).toBe('1080px')
    expect(text(root)).not.toContain('每台检查一路代表通道')
    expect(text(root)).not.toContain('当前范围没有可检测目标')
    expect(text(root)).not.toContain('当前检查 RTSP 连接')
    expect(button(root, '下一步').props.disabled).toBe(true)
    expect(button(root, '关闭')).toBeDefined()
  })
  it('offers the existing five and ten minute patrol intervals without extending fact freshness', async () => {
    api.getStatusPlans.mockResolvedValue([])
    api.createStatusPlan.mockResolvedValue({ planId: '1' })
    api.inspectPlanTargets.mockResolvedValue({
      targets: [{ groupKey: 'd100', name: '入口', reasonCode: null }],
      limitExceeded: false,
    })
    const root = await mount(PlanDialog, { groupKeys: ['d100'] })
    await choosePatrolPage(root)
    const options = nodes(root).filter((item) => item.type === 'ElOption')
    expect(options.some((item) => item.props.value === 5 && item.props.label === '每 5 分钟')).toBe(
      true,
    )
    expect(
      options.some((item) => item.props.value === 10 && item.props.label === '每 10 分钟'),
    ).toBe(true)
    const field = nodes(root).find(
      (item) => item.type === 'ElSelect' && item.props['aria-label'] === '检测间隔',
    )!
    ;(field.props['onUpdate:modelValue'] as (value: number) => void)(5)
    await click(root, '启动巡检')
    expect(api.createStatusPlan.mock.calls[0]![0]).toMatchObject({ intervalMinutes: 5 })
    expect(text(root)).not.toContain('检测间隔不延长结果有效期')
  })
  it('passes the plan version when stopping and permits correcting a rejected new plan', async () => {
    api.inspectPlanTargets.mockResolvedValue({
      targets: [{ groupKey: 'd100', name: '入口', reasonCode: null }],
      limitExceeded: false,
    })
    const plan = {
      planId: '1',
      state: 'ACTIVE',
      targetCount: 2,
      intervalMinutes: 60,
      startAt: new Date().toISOString(),
      endAt: new Date(Date.now() + 3600000).toISOString(),
      version: '3',
    }
    api.getStatusPlans.mockResolvedValue([plan])
    api.stopStatusPlan.mockResolvedValue({ ...plan, state: 'STOPPED' })
    api.createStatusPlan.mockRejectedValueOnce(
      new ApiRequestError('RATE_LIMITED', '繁忙', { status: 429 }),
    )
    const root = await mount(PlanDialog, { groupKeys: ['d100'] })
    await choosePatrolPage(root)
    await patrolTab(root, 'tasks')
    await click(root, '停止')
    expect(api.stopStatusPlan).toHaveBeenCalledWith(plan)
    await patrolTab(root, 'create')
    await click(root, '启动巡检')
    expect(text(root)).not.toContain('响应未确认')
    expect(button(root, '启动巡检')).toBeDefined()
    expect(text(root)).toContain('增大检测间隔')
  })
  it('expires patrol controls using server time and releases the refresh timer on close', async () => {
    vi.useFakeTimers()
    vi.setSystemTime(new Date('2026-10-14T00:00:00Z'))
    const plan = {
      planId: '1',
      state: 'ACTIVE',
      targetCount: 1,
      intervalMinutes: 60,
      startAt: '2026-10-14T08:00:00Z',
      endAt: '2026-10-14T08:00:05Z',
      serverNow: '2026-10-14T08:00:00Z',
      version: '1',
      progress: { success: 0, failed: 0, pending: 0, skipped: 1, unknown: 0 },
    }
    api.getStatusPlans.mockResolvedValue([plan])
    const root = await mount(PlanDialog, { groupKeys: [] })
    await patrolTab(root, 'tasks')
    expect(button(root, '停止')).toBeDefined()
    expect(text(root)).toContain('未执行 1')
    await vi.advanceTimersByTimeAsync(6000)
    await settle()
    expect(text(root)).toContain('已到期')
    expect(button(root, '停止')).toBeUndefined()
    app?.unmount()
    app = null
    expect(vi.getTimerCount()).toBe(0)
  })
  it('reloads a stop conflict and does not mislabel it as an unsupported RTSP target', async () => {
    const plan = {
      planId: '1',
      state: 'ACTIVE',
      targetCount: 1,
      intervalMinutes: 60,
      startAt: new Date().toISOString(),
      endAt: new Date(Date.now() + 3600000).toISOString(),
      version: '1',
    }
    api.getStatusPlans
      .mockResolvedValueOnce([plan])
      .mockResolvedValue([{ ...plan, state: 'EXPIRED', version: '2' }])
    api.stopStatusPlan.mockRejectedValue(new ApiRequestError('CONFLICT', '冲突', { status: 409 }))
    const root = await mount(PlanDialog, { groupKeys: [] })
    await patrolTab(root, 'tasks')
    await click(root, '停止')
    expect(api.getStatusPlans).toHaveBeenCalledTimes(2)
    expect(text(root)).toContain('任务状态已变化')
    expect(text(root)).not.toContain('RTSP 地址')
    expect(button(root, '停止')).toBeUndefined()
  })
  it('pauses patrol reads after three failures without submitting checks', async () => {
    vi.useFakeTimers()
    const plan = {
      planId: '1',
      state: 'ACTIVE',
      targetCount: 1,
      intervalMinutes: 60,
      startAt: new Date().toISOString(),
      endAt: new Date(Date.now() + 3600000).toISOString(),
      version: '1',
    }
    api.getStatusPlans.mockResolvedValueOnce([plan]).mockRejectedValue(new Error('unavailable'))
    const root = await mount(PlanDialog, { groupKeys: [] })
    await patrolTab(root, 'tasks')
    await vi.advanceTimersByTimeAsync(120000)
    await settle()
    expect(api.getStatusPlans).toHaveBeenCalledTimes(4)
    expect(text(root)).toContain('已暂停自动刷新')
    expect(api.createStatusPlan).not.toHaveBeenCalled()
  })
  it('excludes unsupported representatives and prevents an empty patrol request', async () => {
    api.getStatusPlans.mockResolvedValue([])
    api.inspectPlanTargets.mockResolvedValue({
      targets: [
        {
          groupKey: 'd100',
          name: 'ONVIF相机',
          reasonCode: 'SOURCE_PROTOCOL_NOT_SUPPORTED',
        },
      ],
      limitExceeded: false,
    })
    const root = await mount(PlanDialog, { groupKeys: ['d100', 'd100'] })
    await choosePatrolPage(root)
    expect(api.inspectPlanTargets).toHaveBeenCalledTimes(1)
    expect(text(root).replace(/\s+/g, ' ')).toContain('0 台可检测 / 1 台 · 1 台跳过')
    expect(text(root)).toContain('此接入方式的连接检测尚未适配')
    expect(button(root, '启动巡检')).toBeUndefined()
    await click(root, '下一步')
    expect(api.createStatusPlan).not.toHaveBeenCalled()
  })
  it('shows excluded targets before creating a supported subset', async () => {
    api.getStatusPlans.mockResolvedValue([])
    api.inspectPlanTargets.mockResolvedValue({
      targets: ['d100', 'd200'].map((groupKey) => ({
        groupKey,
        name: groupKey,
        reasonCode: groupKey === 'd200' ? 'LOCATOR_NOT_CONFIGURED' : null,
      })),
      limitExceeded: false,
    })
    api.createStatusPlan.mockResolvedValue({ planId: '1' })
    const root = await mount(PlanDialog, { groupKeys: ['d100', 'd200'] })
    await choosePatrolPage(root)
    expect(text(root).replace(/\s+/g, ' ')).toContain('1 台可检测 / 2 台 · 1 台跳过')
    expect(text(root)).toContain('d200：未配置 RTSP 地址')
    await click(root, '启动巡检')
    expect(api.createStatusPlan.mock.calls[0]![0].groupKeys).toEqual(['d100'])
  })
  it('blocks submission when target inspection fails without claiming offline', async () => {
    api.getStatusPlans.mockResolvedValue([])
    api.inspectPlanTargets.mockRejectedValue(new ApiRequestError('REQUEST_TIMEOUT', '核对超时'))
    const root = await mount(PlanDialog, { groupKeys: ['d100'] })
    await choosePatrolPage(root)
    expect(button(root, '启动巡检')).toBeUndefined()
    expect(text(root)).not.toContain('离线')
    expect(button(root, '重新核对目标')).toBeDefined()
  })
  it('previews the full scope without submitting and refuses an oversized scope', async () => {
    api.getStatusPlans.mockResolvedValue([])
    const root = await mount(PlanDialog, { groupKeys: ['d100'] })
    api.inspectPlanTargets.mockResolvedValue({ targets: [], limitExceeded: true })
    await patrolScope(root, 'ALL')
    await click(root, '下一步')
    await settle()
    expect(api.inspectPlanTargets).toHaveBeenLastCalledWith({ scope: 'ALL' })
    expect(text(root)).toContain('超过 2000 台')
    expect(button(root, '启动巡检')).toBeUndefined()
    expect(api.createStatusPlan).not.toHaveBeenCalled()
  })
  it('rechecks a stopped task range and requires confirmation to start a fresh finite task', async () => {
    const plan = {
      planId: '9',
      state: 'STOPPED',
      targetCount: 1,
      intervalMinutes: 30,
      startAt: '2026-10-14T08:00:00Z',
      endAt: '2026-10-14T14:00:00Z',
      version: '1',
      layer: 'MEDIA',
      dispatchMode: 'POLLING',
      parallelism: 1,
    }
    api.getStatusPlans.mockResolvedValue([plan])
    api.createStatusPlan.mockResolvedValue({ planId: '10' })
    const root = await mount(PlanDialog, { groupKeys: [] })
    api.inspectPlanTargets.mockResolvedValue({
      targets: [{ groupKey: 'd900', name: '模拟设备', reasonCode: null }],
      limitExceeded: false,
    })
    await patrolTab(root, 'tasks')
    await click(root, '复用范围')
    await settle()
    expect(api.inspectPlanTargets).toHaveBeenLastCalledWith({ scope: 'PLAN', planId: '9' })
    expect(text(root)).toContain('确认后启动新任务')
    expect(api.createStatusPlan).not.toHaveBeenCalled()
    await choosePatrolPage(root)
    await click(root, '启动巡检')
    expect(api.createStatusPlan).toHaveBeenCalledWith(
      expect.objectContaining({ groupKeys: ['d900'], intervalMinutes: 30, durationHours: 6 }),
    )
  })
  it('ignores an older scope preview that arrives after a newer selection', async () => {
    let resolve: (value: unknown) => void = () => {}
    api.inspectPlanTargets.mockReturnValueOnce(
      new Promise((done) => {
        resolve = done
      }),
    )
    api.getStatusPlans.mockResolvedValue([])
    const root = await mount(PlanDialog, { groupKeys: ['d100'] })
    const old = (button(root, '下一步').props.onClick as () => Promise<void>)()
    api.inspectPlanTargets.mockResolvedValue({
      targets: [{ groupKey: 'd200', name: '全量目标', reasonCode: null }],
      limitExceeded: false,
    })
    await patrolScope(root, 'ALL')
    await choosePatrolPage(root)
    resolve({
      targets: [{ groupKey: 'd100', name: '旧目标', reasonCode: null }],
      limitExceeded: false,
    })
    await old
    await settle()
    await click(root, '启动巡检')
    expect(api.createStatusPlan).toHaveBeenCalledWith(
      expect.objectContaining({ groupKeys: ['d200'] }),
    )
  })
  it('shows only local edits to ordinary camera managers and no unavailable operations', async () => {
    api.getCameraOptions.mockResolvedValue({ canManageShared: false })
    const root = await mount(CameraManage)
    expect(button(root, '详情')).toBeDefined()
    await click(root, '详情')
    await settle()
    expect(button(root, '编辑通道')).toBeDefined()
    expect(button(root, '新增相机')).toBeUndefined()
    expect(button(root, '删除通道')).toBeUndefined()
    expect(text(root)).not.toMatch(/播放|>在线<|网段搜索|海康接入/)
  })
  it('discards a stale list response when a newer filter completes first', async () => {
    let finish!: (value: unknown) => void
    api.getCameraDevices.mockReturnValueOnce(
      new Promise((resolve) => {
        finish = resolve
      }),
    )
    const root = await mount(CameraManage)
    api.getCameraDevices.mockResolvedValue({ items: [{ ...device, name: '新筛选' }], total: 1 })
    await click(root, '刷新')
    finish({ items: [{ ...device, name: '迟到旧筛选' }], total: 1 })
    await settle()
    expect(text(root)).toContain('新筛选')
    expect(text(root)).not.toContain('迟到旧筛选')
  })
  it('does not delete after the confirmation was cancelled or the identity changed', async () => {
    const root = await mount(CameraManage)
    await click(root, '详情')
    await settle()
    api.confirm.mockRejectedValueOnce('cancel')
    await click(root, '删除通道')
    expect(api.deleteCamera).not.toHaveBeenCalled()
    api.confirm.mockImplementationOnce(async () => {
      clearIdentity()
      return 'confirm'
    })
    await click(root, '删除通道')
    expect(api.deleteCamera).not.toHaveBeenCalled()
  })
  it('submits only name, remark and default Profile for ordinary local editing', async () => {
    const root = await mount(DetailDialog, { cameraId: '10', canManage: false })
    await setField(root, '通道名称', '新名称')
    await click(root, '保存本地资料')
    expect(api.updateCamera).toHaveBeenCalledWith('10', {
      version: '3',
      name: '新名称',
      remark: null,
    })
    expect(button(root, '增加码流')).toBeUndefined()
    expect(api.getSources).not.toHaveBeenCalled()
  })
  it('retains full grant selections when the options only show one page', async () => {
    api.getScopeCameras.mockResolvedValue({
      items: [{ cameraId: '12', name: '新通道', path: '厂区 / 深层', lifecycle: 'ENABLED' }],
      total: 100,
      page: 1,
      size: 20,
    })
    const root = await mount(ScopeManage)
    await click(root, '配置范围')
    await settle()
    const tabs = nodes(root).find((item) => item.type === 'ElTabs')!
    ;(tabs.props['onUpdate:modelValue'] as (value: string) => void)('camera')
    await settle()
    const checkbox = nodes(root).find((item) => item.type === 'ElCheckbox')!
    ;(checkbox.props['onUpdate:modelValue'] as (value: boolean) => void)(true)
    await nextTick()
    await click(root, '保存相机范围')
    expect(api.updateCameraScope).toHaveBeenCalledWith('5', {
      version: '6',
      groupIds: ['30', '31'],
      cameraIds: ['10', '11', '12'],
    })
    expect(text(root)).toContain('已停用，保留授权')
  })
  it('shows departments and checks a parent without adding child group grants', async () => {
    api.getCameraScope.mockResolvedValue({ ...scope, groupIds: [], groupGrants: [] })
    const root = await mount(ScopeManage)
    expect(text(root)).toContain('研发部门')
    expect(text(root)).toContain('运维部门')
    expect(text(root)).not.toContain('直接授权与分组授权取并集')
    await click(root, '配置范围')
    await settle()
    const tree = nodes(root).find((item) => item.type === 'ElTree')!
    expect(tree.props['check-strictly']).toBe('')
    const groups = tree.props.data as { groupId: string; children: { groupId: string }[] }[]
    expect(groups[0]!.children[0]!.groupId).toBe('31')
    ;(tree.props.onCheck as (data: unknown, state: unknown) => void)(groups[0], {
      checkedKeys: ['30'],
    })
    await nextTick()
    await click(root, '保存相机范围')
    expect(api.updateCameraScope).toHaveBeenCalledWith('5', {
      version: '6',
      groupIds: ['30'],
      cameraIds: ['10', '11'],
    })
  })
  it('keeps changes when closing is cancelled and discards only after confirmation', async () => {
    const root = await mount(ScopeManage)
    await click(root, '配置范围')
    await settle()
    const tag = nodes(root).find((item) => item.type === 'ElTag' && text(item) === '厂区 / 深层')!
    ;(tag.props.onClose as () => void)()
    await nextTick()
    api.confirm.mockRejectedValueOnce('cancel')
    await click(root, '关闭')
    expect(nodes(root).some((item) => item.type === 'ElDialog')).toBe(true)
    expect(text(root)).not.toContain('厂区 / 深层')
    await click(root, '关闭')
    expect(nodes(root).some((item) => item.type === 'ElDialog')).toBe(false)
    expect(api.updateCameraScope).not.toHaveBeenCalled()
  })
  it('ignores a scope response that arrives after its dialog is closed', async () => {
    let resolve!: (value: typeof scope) => void
    api.getCameraScope.mockReturnValueOnce(
      new Promise((done) => {
        resolve = done
      }),
    )
    const root = await mount(ScopeManage)
    const opening = (button(root, '配置范围').props.onClick as () => Promise<void>)()
    await settle()
    await click(root, '关闭')
    resolve(scope)
    await opening
    await settle()
    expect(nodes(root).some((item) => item.type === 'ElDialog')).toBe(false)
    expect(api.updateCameraScope).not.toHaveBeenCalled()
  })
  it('requires impact preview and explicit confirmation before assigning a camera', async () => {
    api.getCamera.mockResolvedValue({
      ...camera,
      groupId: null,
      groupPath: null,
      lifecycle: 'PENDING_ASSIGNMENT',
    })
    const root = await mount(PlacementDialog, { cameraId: '10', action: 'move' })
    const select = nodes(root).find((item) => item.type === 'ElTreeSelect')!
    ;(select.props['onUpdate:modelValue'] as (value: string) => void)('30')
    await nextTick()
    await click(root, '查看影响')
    expect(api.updateCamera).not.toHaveBeenCalled()
    expect(api.previewCameraMove).toHaveBeenCalledWith('10', '3', '30')
    await click(root, '确认并应用')
    expect(api.updateCamera).toHaveBeenCalledWith('10', {
      version: '3',
      groupId: '30',
      lifecycle: 'ENABLED',
      confirmation: 'proof',
    })
  })
  it('retries an uncertain access request with the same key and full URL without creating a source first', async () => {
    api.createAccessJob
      .mockRejectedValueOnce(new Error('response lost'))
      .mockResolvedValueOnce({ jobId: '900' })
    const root = await mount(CreateDialog, { mode: 'rtsp' })
    await setField(root, '相机名称', '炉前')
    await setField(root, 'RTSP 地址', 'rtsp://camera.test/channel%2F2?channel=1')
    await click(root, '解析视频地址')
    await click(root, '重试原读取请求')
    expect(api.createAccessJob).toHaveBeenCalledTimes(2)
    expect(api.createAccessJob.mock.calls[1]![0]).toEqual(api.createAccessJob.mock.calls[0]![0])
    expect(api.createAccessJob.mock.calls[0]![0].connection.rtspUrls).toEqual([
      'rtsp://camera.test/channel%2F2?channel=1',
    ])
    expect(api.createCamera).not.toHaveBeenCalled()
    expect(api.getSources).not.toHaveBeenCalled()
  })
  it('selects fetched profiles and default, previews group impact, then retries the original import', async () => {
    api.importCandidates.mockRejectedValueOnce(new Error('response lost')).mockResolvedValueOnce({
      sourceId: '1',
      cameras: [{ candidateId: 'channel-a', cameraId: '10', version: '0', status: 'CREATED' }],
      createdCount: 1,
      existingCount: 0,
    })
    const saved = vi.fn(),
      root = await mount(CreateDialog, { mode: 'rtsp', onSaved: saved })
    await setField(root, '相机名称', '炉前')
    await setField(root, 'RTSP 地址', 'rtsp://camera.test/main')
    await click(root, '解析视频地址')
    await settle()
    expect(text(root)).toContain('2048 kbps')
    expect(text(root)).toContain('码率未获取')
    await setField(root, '默认码流', 'stream-b', 'ElSelect')
    const select = nodes(root).find((item) => item.type === 'ElTreeSelect')!
    ;(select.props['onUpdate:modelValue'] as (value: string) => void)('30')
    await click(root, '查看导入影响')
    expect(api.importCandidates).not.toHaveBeenCalled()
    expect(api.previewImport).toHaveBeenCalledWith('900', {
      version: '2',
      groupId: '30',
      selections: [
        {
          candidateId: 'channel-a',
          profileIds: ['stream-a', 'stream-b'],
          defaultProfileId: 'stream-b',
        },
      ],
    })
    await click(root, '确认导入')
    expect(button(root, '返回选择').props.disabled).toBe(true)
    await click(root, '重试原导入')
    expect(api.importCandidates.mock.calls[1]).toEqual(api.importCandidates.mock.calls[0])
    expect(api.importCandidates.mock.calls[0]![1]).not.toHaveProperty('rtspUrls')
    await click(root, '完成')
    expect(saved).toHaveBeenCalledWith(['10'])
  })
  it('imports selected profiles without a default while rejecting a default outside the selection', async () => {
    const root = await mount(CreateDialog, { mode: 'rtsp' })
    await setField(root, '相机名称', '仅登记码流')
    await setField(root, 'RTSP 地址', 'rtsp://camera.test/main')
    await click(root, '解析视频地址')
    expect(field(root, '默认码流', 'ElSelect').props).toHaveProperty('clearable')
    await setField(root, '默认码流', 'not-selected', 'ElSelect')
    await click(root, '查看导入影响')
    expect(api.previewImport).not.toHaveBeenCalled()
    expect(text(root)).toContain('必须选择已勾选的码流')
    await setField(root, '默认码流', '', 'ElSelect')
    await click(root, '查看导入影响')
    const selections = [{ candidateId: 'channel-a', profileIds: ['stream-a', 'stream-b'] }]
    expect(api.previewImport).toHaveBeenCalledWith('900', { version: '2', selections })
    await click(root, '确认导入')
    expect(api.importCandidates.mock.calls[0]![1].selections).toEqual(selections)
  })
  it('allows editing after explicit access validation rejection and does not offer missing adapters', async () => {
    api.createAccessJob.mockRejectedValueOnce(
      new ApiRequestError('VALIDATION_ERROR', '地址错误', { status: 400 }),
    )
    const root = await mount(CreateDialog, { mode: 'rtsp' })
    expect(text(root)).not.toContain('海康设备')
    await setField(root, '相机名称', '炉前')
    await setField(root, 'RTSP 地址', 'rtsp://camera.test/main')
    await click(root, '解析视频地址')
    expect(button(root, '重试原读取请求')).toBeUndefined()
    expect(button(root, '解析视频地址')).toBeDefined()
  })
  it('imports exactly the selection that received the signed impact even if a late form event arrives', async () => {
    let complete!: (value: unknown) => void
    api.previewImport.mockReturnValue(
      new Promise((resolve) => {
        complete = resolve
      }),
    )
    const root = await mount(CreateDialog, { mode: 'rtsp' })
    await setField(root, '相机名称', '炉前')
    await setField(root, 'RTSP 地址', 'rtsp://camera.test/main')
    await click(root, '解析视频地址')
    const group = nodes(root).find((item) => item.type === 'ElTreeSelect')!
    const setGroup = group.props['onUpdate:modelValue'] as (value: string) => void
    setGroup('30')
    const pending = (button(root, '查看导入影响').props.onClick as () => Promise<void>)()
    await nextTick()
    expect(group.props.disabled).toBe(true)
    setGroup('31')
    complete({
      confirmation: 'signed-original-group',
      groupPath: '原目标',
      affectedUserCount: 1,
      cameraCount: 1,
    })
    await pending
    await nextTick()
    await click(root, '确认导入')
    expect(api.importCandidates.mock.calls[0]![1].groupId).toBe('30')
  })
  it('displays a deployment blocker outside the connection form', async () => {
    api.getAccessOptions.mockResolvedValue({
      ready: false,
      methods: ['RTSP'],
      adapters: [
        {
          type: 'RTSP',
          label: 'RTSP',
          category: 'RTSP',
          inputKind: 'RTSP_URL',
          autoDetect: false,
          paged: false,
        },
      ],
      networkPolicies: [],
      diagnostics: ['请配置接入密钥'],
    })
    const root = await mount(CreateDialog, { mode: 'rtsp' })
    expect(text(root)).toContain('请联系系统管理员')
    expect(nodes(root).filter((item) => item.type === 'ElInput')).toHaveLength(0)
    expect(button(root, '解析视频地址')).toBeUndefined()
  })
  it('cancels a running job on explicit close and stops further reads', async () => {
    api.getAccessJob.mockResolvedValue({ ...accessJob, status: 'RUNNING', candidates: [] })
    const close = vi.fn(),
      root = await mount(CreateDialog, { mode: 'rtsp', onClose: close })
    await setField(root, '相机名称', '炉前')
    await setField(root, 'RTSP 地址', 'rtsp://camera.test/main')
    await click(root, '解析视频地址')
    await click(root, '取消读取并关闭')
    expect(api.cancelAccessJob).toHaveBeenCalledWith('900')
    expect(close).toHaveBeenCalledOnce()
    expect(api.getAccessJob).toHaveBeenCalledTimes(1)
  })
  it('does not offer locator changes for discovered profiles even when their locator is RTSP', async () => {
    api.getCamera.mockResolvedValue({
      ...camera,
      profiles: [{ ...profile, locatorSummary: { ...profile.locatorSummary, editable: false } }],
    })
    const root = await mount(ProfileEditor, { cameraId: '10', profileId: '20' })
    expect(text(root)).not.toContain('RTSP 路径')
    await setField(root, '码流标签', '现场主画面')
    await click(root, '保存')
    expect(api.updateProfile).toHaveBeenCalledWith('10', '20', {
      version: '2',
      label: '现场主画面',
    })
  })
  it('explicitly clears the default when disabling its Profile', async () => {
    const root = await mount(ProfileEditor, { cameraId: '10', profileId: '20' })
    await setField(root, '允许新使用', false, 'ElSwitch')
    await click(root, '保存')
    expect(api.updateProfile).toHaveBeenCalledWith(
      '10',
      '20',
      expect.objectContaining({
        version: '2',
        cameraVersion: '3',
        enabled: false,
        replacementDefaultProfileId: null,
      }),
    )
    expect(api.updateProfile.mock.calls[0]![2]).not.toHaveProperty('locator')
  })
  it('adds a full RTSP URL to the existing camera and supplies an automatic local label', async () => {
    api.createProfile.mockResolvedValue({ streamProfileId: '21' })
    const root = await mount(ProfileEditor, { cameraId: '10', profileId: null })
    await setField(root, 'RTSP 地址', 'rtsp://camera.test/channel%2F2?sub=1')
    await click(root, '保存')
    expect(api.createProfile).toHaveBeenCalledWith(
      '10',
      expect.objectContaining({
        cameraVersion: '3',
        label: '码流 2',
        locatorKind: 'RTSP',
        locator: { fullUrl: 'rtsp://camera.test/channel%2F2?sub=1', transport: 'TCP' },
      }),
    )
    expect(api.createCamera).not.toHaveBeenCalled()
    expect(api.createAccessJob).not.toHaveBeenCalled()
    expect(text(root)).not.toContain('RTSP 路径')
  })
  it('replaces an existing manual profile address only after explicit replacement selection', async () => {
    const root = await mount(ProfileEditor, { cameraId: '10', profileId: '20' })
    expect(
      nodes(root).some((item) => item.type === 'ElFormItem' && item.props.label === 'RTSP 地址'),
    ).toBe(false)
    await setField(root, '视频地址', true, 'ElSelect')
    await setField(root, 'RTSP 地址', 'rtsp://different.test:8554/sub?track=2')
    await click(root, '保存')
    expect(api.updateProfile).toHaveBeenCalledWith('10', '20', {
      version: '2',
      locator: { fullUrl: 'rtsp://different.test:8554/sub?track=2', transport: 'TCP' },
    })
  })
  it('retries adding a Profile with the same full URL and idempotency key', async () => {
    api.createProfile
      .mockRejectedValueOnce(new Error('response lost'))
      .mockResolvedValueOnce({ streamProfileId: '21' })
    const root = await mount(ProfileEditor, { cameraId: '10', profileId: null })
    await setField(root, 'RTSP 地址', 'rtsp://camera.test/third')
    await click(root, '保存')
    await click(root, '重试原请求')
    expect(api.createProfile.mock.calls[1]).toEqual(api.createProfile.mock.calls[0])
  })
  it('allows correcting a rejected Profile URL without reopening the editor', async () => {
    api.createProfile
      .mockRejectedValueOnce(
        new ApiRequestError('VALIDATION_ERROR', '地址未获批准', { status: 400 }),
      )
      .mockResolvedValueOnce({ streamProfileId: '21' })
    const root = await mount(ProfileEditor, { cameraId: '10', profileId: null })
    await setField(root, 'RTSP 地址', 'rtsp://127.0.0.1/third')
    await click(root, '保存')
    expect(button(root, '重试原请求')).toBeUndefined()
    expect(nodes(root).find((item) => item.type === 'ElForm')!.props.disabled).toBe(false)
    await setField(root, 'RTSP 地址', 'rtsp://camera.test/third')
    await click(root, '保存')
    const first = api.createProfile.mock.calls[0]![1],
      corrected = api.createProfile.mock.calls[1]![1]
    expect(corrected.locator.fullUrl).toBe('rtsp://camera.test/third')
    expect(corrected.clientRequestId).not.toBe(first.clientRequestId)
  })
  it('does not resend a locator or observed usage when only a Profile label changes', async () => {
    const root = await mount(ProfileEditor, { cameraId: '10', profileId: '20' })
    await setField(root, '码流标签', '标签已改')
    await click(root, '保存')
    expect(api.updateProfile).toHaveBeenCalledWith('10', '20', { version: '2', label: '标签已改' })
  })
  it('keeps credentials and network settings untouched when only a source name changes', async () => {
    const root = await mount(SourceEditor, { sourceId: '1' })
    await settle()
    await setField(
      nodes(root).find((item) => item.type === 'ElDialog')!,
      '连接名称',
      '来源改名',
    )
    await click(root, '保存配置')
    expect(api.updateSource).toHaveBeenCalledWith('1', { version: '4', name: '来源改名' })
  })
  it('shows partial catalog warnings but excludes ambiguous channels from import', async () => {
    api.getAccessJob.mockResolvedValue({
      ...accessJob,
      status: 'PARTIAL',
      complete: false,
      candidates: [
        ...accessJob.candidates,
        {
          candidateId: 'ambiguous',
          name: '需人工映射的通道',
          mappingRequired: true,
          profiles: accessJob.candidates[0]!.profiles,
        },
      ],
    })
    const root = await mount(CreateDialog, { mode: 'rtsp' })
    await setField(root, '相机名称', '炉前')
    await setField(root, 'RTSP 地址', 'rtsp://camera.test/main')
    await click(root, '解析视频地址')
    expect(text(root)).toContain('目录不完整')
    expect(text(root)).toContain('通道映射待确认')
    await click(root, '查看导入影响')
    expect(api.previewImport.mock.calls[0]![1].selections).toHaveLength(1)
  })
  it('presents a device diagnostic without pretending an HTTP-successful job discovered channels', async () => {
    api.getAccessJob.mockResolvedValue({
      ...accessJob,
      status: 'FAILED',
      candidates: [],
      diagnostic: {
        reasonCode: 'AUTHENTICATION_FAILED',
        actionHint: '请核对设备账户',
        originTraceId: 'a'.repeat(32),
        occurredAt: '2026-10-06T00:00:00Z',
      },
    })
    const root = await mount(CreateDialog, { mode: 'rtsp' })
    await setField(root, '相机名称', '炉前')
    await setField(root, 'RTSP 地址', 'rtsp://camera.test/main')
    await click(root, '解析视频地址')
    expect(text(root)).toContain('请核对设备账户')
    expect(text(root)).toContain('a'.repeat(32))
    expect(button(root, '查看导入影响')).toBeUndefined()
  })
  it('updates a platform endpoint using its original purpose and base path', async () => {
    const platformSource = {
      ...source,
      adapterType: 'HIK_PLATFORM',
      credentials: [{ purpose: 'PLATFORM_HTTP', configured: true, usernameMasked: 'k***' }],
      endpoints: [
        {
          ...source.endpoints[0],
          purpose: 'PLATFORM_HTTP',
          scheme: 'https',
          port: 443,
          basePath: '/artemis',
          authMode: 'DRIVER_NEGOTIATED',
          credentialPurpose: 'PLATFORM_HTTP',
        },
      ],
    }
    api.getSources.mockResolvedValue({ items: [platformSource], total: 1 })
    api.getSource.mockResolvedValue(platformSource)
    const root = await mount(SourceEditor, { sourceId: '1' })
    await settle()
    await setField(root, '连接主机', 'platform-new.test')
    await click(root, '保存配置')
    expect(api.updateSource).toHaveBeenCalledWith('1', {
      version: '4',
      endpointsUpsert: [
        {
          purpose: 'PLATFORM_HTTP',
          scheme: 'https',
          host: 'platform-new.test',
          port: 443,
          basePath: '/artemis',
          authMode: 'DRIVER_NEGOTIATED',
          credentialPurpose: 'PLATFORM_HTTP',
          tlsPolicy: 'SYSTEM_CA',
        },
      ],
    })
  })
  it('locks an imported native device origin while retaining local metadata editing', async () => {
    api.getSource.mockResolvedValue({
      ...source,
      adapterType: 'ONVIF',
      endpointEditable: false,
      endpoints: [
        {
          ...source.endpoints[0],
          purpose: 'ONVIF',
          scheme: 'http',
          port: 80,
          basePath: '/onvif/device_service',
        },
      ],
    })
    const root = await mount(SourceEditor, { sourceId: '1' })
    await settle()
    expect(field(root, '连接主机').props.disabled).toBe(true)
    expect(field(root, '服务端口', 'ElInputNumber').props.disabled).toBe(true)
    expect(field(root, '连接协议', 'ElSelect').props.disabled).toBe(true)
    expect(text(root)).toContain('设备地址不可修改')
    await setField(
      nodes(root).find((item) => item.type === 'ElDialog')!,
      '连接名称',
      '设备新标签',
    )
    await click(root, '保存配置')
    expect(api.updateSource).toHaveBeenCalledWith('1', { version: '4', name: '设备新标签' })
  })
})
