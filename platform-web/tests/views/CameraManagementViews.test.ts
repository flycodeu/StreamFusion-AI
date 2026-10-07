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
import ProfileEditor from '../../src/features/camera/ProfileEditor.vue'
import PlacementDialog from '../../src/features/camera/PlacementDialog.vue'
import ManualCreateDialog from '../../src/features/camera/ManualCreateDialog.vue'
import ScanDialog from '../../src/features/camera-access/ScanDialog.vue'
import SourceEditor from '../../src/features/camera-sources/SourceEditor.vue'
import BulkImportPanel from '../../src/features/camera-access/BulkImportPanel.vue'
import ImportTasksDialog from '../../src/features/camera-access/ImportTasksDialog.vue'
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
  getScopeUsers: vi.fn(),
  getCameraScope: vi.fn(),
  updateCameraScope: vi.fn(),
  getScopeGroups: vi.fn(),
  getScopeCameras: vi.fn(),
  getGroups: vi.fn(),
  previewCameraMove: vi.fn(),
  previewCameraLifecycle: vi.fn(),
  confirm: vi.fn(),
  success: vi.fn(),
}))
vi.mock('../../src/api/camera/api', () => api)
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
    'ElTreeSelect',
    'ElCheckbox',
    'ElDescriptions',
    'ElDescriptionsItem',
    'ElTabs',
    'ElTabPane',
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
    vLoading: {},
    ElMessage: { success: api.success },
    ElMessageBox: { confirm: api.confirm },
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
  vi.clearAllMocks()
  clearIdentity()
  api.getCameraOptions.mockResolvedValue({ canManageShared: true, manualStorageReady: true })
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
      { userId: '5', username: 'safety', nickname: '安全处', enabled: true, isSuperAdmin: false },
    ],
    total: 1,
    page: 1,
    size: 20,
  })
  api.getCameraScope.mockResolvedValue(scope)
  api.updateCameraScope.mockResolvedValue(scope)
  api.getScopeGroups.mockResolvedValue({
    items: [{ groupId: '30', name: '厂区', parentId: null, hasChildren: false }],
    total: 1,
    page: 1,
    size: 20,
  })
  api.getScopeCameras.mockResolvedValue({ items: [], total: 0, page: 1, size: 20 })
  api.getGroups.mockResolvedValue({ items: [], total: 0, page: 1, size: 100 })
  api.previewCameraMove.mockResolvedValue(impact)
  api.previewCameraLifecycle.mockResolvedValue(impact)
  api.confirm.mockResolvedValue('confirm')
  api.deleteCamera.mockResolvedValue(undefined)
  api.updateProfile.mockResolvedValue(profile)
})
afterEach(() => {
  app?.unmount()
  app = null
  vi.restoreAllMocks()
})

describe('camera management interaction boundaries', () => {
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
      await click(root, '查询平台目录')
      await click(root, '整批导入')
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
    await click(root, '查询平台目录')
    await click(root, '整批导入')
    await settle()
    await click(root, '确认整批导入')
    expect(text(root)).toContain('导入中断')
    await click(root, '重新读取并重试')
    await settle()
    expect(api.createAccessJob.mock.calls[1]![0].connection).toEqual({
      method: 'HIK_PLATFORM',
      sourceId: '70',
      sourceVersion: '9',
      pageNumber: 1,
      pageSize: 100,
    })
    await click(root, '整批导入')
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
      methods: ['CUSTOM_PLATFORM'],
      adapters: [
        {
          type: 'CUSTOM_PLATFORM',
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
        method: 'CUSTOM_PLATFORM',
        candidates: [{ candidateId: 'p1', name: '平台相机', mappingRequired: false, profiles: [] }],
        page: { pageNumber: 1, pageSize: 100, total: 101, hasMore: true },
      })
      .mockResolvedValueOnce({
        ...accessJob,
        jobId: '902',
        method: 'CUSTOM_PLATFORM',
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
    await click(root, '查询平台目录')
    await click(root, '查看导入影响')
    await click(root, '确认导入')
    await click(root, '下一页')
    expect(button(root, '重试本页读取请求')).toBeDefined()
    await click(root, '重试本页读取请求')
    expect(api.createAccessJob.mock.calls[2]).toEqual(api.createAccessJob.mock.calls[1])
    expect(api.createAccessJob.mock.calls[1]![0].connection).toEqual({
      method: 'CUSTOM_PLATFORM',
      sourceId: '70',
      sourceVersion: '9',
      pageNumber: 2,
      pageSize: 100,
    })
    expect(text(root)).toContain('已选 0 个')
    expect(api.createAccessJob.mock.calls[1]![0]).not.toHaveProperty('password')
  })
  it('shows only local edits to ordinary camera managers and no unavailable operations', async () => {
    api.getCameraOptions.mockResolvedValue({ canManageShared: false })
    const root = await mount(CameraManage)
    expect(button(root, '详情 / 编辑')).toBeDefined()
    expect(button(root, '新增相机')).toBeUndefined()
    expect(button(root, '删除')).toBeUndefined()
    expect(text(root)).not.toMatch(/播放|在线|网段搜索|海康接入/)
  })
  it('discards a stale list response when a newer filter completes first', async () => {
    let finish!: (value: unknown) => void
    api.getCameras.mockReturnValueOnce(
      new Promise((resolve) => {
        finish = resolve
      }),
    )
    const root = await mount(CameraManage)
    api.getCameras.mockResolvedValue({ items: [{ ...camera, name: '新筛选' }], total: 1 })
    await click(root, '刷新')
    finish({ items: [{ ...camera, name: '迟到旧筛选' }], total: 1 })
    await settle()
    expect(text(root)).toContain('新筛选')
    expect(text(root)).not.toContain('迟到旧筛选')
  })
  it('does not delete after the confirmation was cancelled or the identity changed', async () => {
    const root = await mount(CameraManage)
    api.confirm.mockRejectedValueOnce('cancel')
    await click(root, '删除')
    expect(api.deleteCamera).not.toHaveBeenCalled()
    api.confirm.mockImplementationOnce(async () => {
      clearIdentity()
      return 'confirm'
    })
    await click(root, '删除')
    expect(api.deleteCamera).not.toHaveBeenCalled()
  })
  it('submits only name, remark and default Profile for ordinary local editing', async () => {
    const root = await mount(DetailDialog, { cameraId: '10', canManage: false })
    await setField(root, '相机名称', '新名称')
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
    const root = await mount(ScopeManage)
    await click(root, '配置范围')
    await settle()
    await click(root, '保存相机范围')
    expect(api.updateCameraScope).toHaveBeenCalledWith('5', {
      version: '6',
      groupIds: ['30', '31'],
      cameraIds: ['10', '11'],
    })
    expect(text(root)).toContain('已停用，保留授权')
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
