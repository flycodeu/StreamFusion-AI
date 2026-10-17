// @vitest-environment vue-renderer
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { nextTick, type App, type Component } from 'vue'
import { node, nodes, renderer, text, type Host } from '../support/renderer'
import RetentionDialog from '../../src/features/audit/RetentionDialog.vue'
import ExportDialog from '../../src/features/audit/ExportDialog.vue'
import CaptureDialog from '../../src/features/diagnostics/CaptureDialog.vue'

const api = vi.hoisted(() => ({
  getRetention: vi.fn(),
  saveRetention: vi.fn(),
  previewRetention: vi.fn(),
  runRetention: vi.fn(),
  getExports: vi.fn(),
  createExport: vi.fn(),
  cancelExport: vi.fn(),
  removeExport: vi.fn(),
  downloadExport: vi.fn(),
  getAuditPage: vi.fn(),
  getCapture: vi.fn(),
  captureRoutes: vi.fn(),
  startCapture: vi.fn(),
  stopCapture: vi.fn(),
}))
vi.mock('../../src/api/audit/maintenance', () => api)
vi.mock('../../src/api/audit/api', () => ({ getAuditPage: api.getAuditPage }))
vi.mock('../../src/api/diagnostics/api', () => api)
vi.mock('element-plus', async () => {
  const { computed, defineComponent, h, inject, provide } = await import('vue')
  return {
    ...Object.fromEntries(
      [
        'ElButton',
        'ElDialog',
        'ElForm',
        'ElFormItem',
        'ElInputNumber',
        'ElRadio',
        'ElRadioGroup',
        'ElSwitch',
        'ElDescriptions',
        'ElDescriptionsItem',
        'ElTag',
        'ElSelect',
        'ElOption',
        'ElTable',
        'ElTableColumn',
        'ElInput',
      ].map((name) => [
        name,
        defineComponent({
          inheritAttrs: false,
          setup(_, { attrs, slots }) {
            return () =>
              h(name, attrs, name === 'ElTableColumn' ? [] : [slots.default?.(), slots.footer?.()])
          },
        }),
      ]),
    ),
    ElTable: defineComponent({
      inheritAttrs: false,
      setup(_, { attrs, slots }) {
        provide(
          'maintenanceRows',
          computed(() => attrs.data as Record<string, unknown>[]),
        )
        return () => h('ElTable', attrs, slots.default?.())
      },
    }),
    ElTableColumn: defineComponent({
      inheritAttrs: false,
      setup(_, { attrs, slots }) {
        const rows = inject<{ value: Record<string, unknown>[] }>('maintenanceRows')!
        return () =>
          h(
            'ElTableColumn',
            attrs,
            rows.value.map((row) => slots.default?.({ row })),
          )
      },
    }),
    ElMessage: { success: vi.fn() },
    ElMessageBox: { confirm: vi.fn() },
  }
})
let app: App | undefined
const policy = {
  enabled: false,
  mode: 'AGE',
  keepMonths: 6,
  maxRecords: 50000,
  version: '0',
  nextAt: null,
  lastStartedAt: null,
  lastFinishedAt: null,
  lastDeleted: 0,
  lastResult: null,
}
const preview = {
  total: 10,
  protectedCount: 1,
  candidates: 3,
  limited: false,
  cutoff: null,
  exportInProgress: false,
}
const capture = {
  enabled: true,
  ownerId: '1',
  expiresAt: '2026-10-17T04:00:00Z',
  matched: 10,
  recorded: 10,
  limit: 1000,
  routes: ['camera-status'],
  actorId: null,
  errorsOnly: true,
}
function deferred<T>() {
  let resolve!: (value: T) => void
  const promise = new Promise<T>((ready) => {
    resolve = ready
  })
  return { promise, resolve }
}
async function flush() {
  for (let i = 0; i < 4; i++) {
    await Promise.resolve()
    await nextTick()
  }
}
async function mount(component: Component, props = {}) {
  const root = node('root')
  app = renderer.createApp(component, props)
  app.mount(root)
  await flush()
  return root
}
const control = (root: Host, type: string) => nodes(root).find((n) => n.type === type)!
const button = (root: Host, label: string) =>
  nodes(root).find((n) => n.type === 'ElButton' && text(n) === label)!
async function click(root: Host, label: string) {
  const b = button(root, label)
  if (!b.props.disabled) await (b.props.onClick as () => unknown)()
  await flush()
}
async function change(root: Host, type: string, value: unknown) {
  ;(control(root, type).props['onUpdate:modelValue'] as (v: unknown) => void)(value)
  await flush()
}
beforeEach(() => {
  vi.useFakeTimers()
  vi.resetAllMocks()
  api.getRetention.mockResolvedValue({ ...policy })
  api.previewRetention.mockResolvedValue(preview)
  api.saveRetention.mockImplementation(async (input) => ({ ...policy, ...input, version: '1' }))
  api.getExports.mockResolvedValue([])
  api.getAuditPage.mockResolvedValue({ items: [], total: 5, page: 1, size: 1 })
  api.getCapture.mockResolvedValue({ ...capture })
  api.captureRoutes.mockResolvedValue([{ key: 'camera-status', label: '状态检查' }])
  api.stopCapture.mockResolvedValue({ ...capture, enabled: false })
})
afterEach(() => {
  app?.unmount()
  vi.useRealTimers()
})

describe('log maintenance dialogs', () => {
  it('does not restore an enabled capture from a poll that finishes after stopping', async () => {
    const late = deferred<typeof capture>()
    const root = await mount(CaptureDialog)
    api.getCapture.mockReturnValueOnce(late.promise)
    await vi.advanceTimersByTimeAsync(5000)
    await click(root, '停止记录')
    expect(text(root)).toContain('未开启')
    late.resolve({ ...capture, recorded: 9 })
    await flush()
    expect(text(root)).toContain('未开启')
    expect(button(root, '开始记录').props.disabled).toBe(false)
    expect(nodes(root).some((n) => n.type === 'ElButton' && text(n) === '停止记录')).toBe(false)
  })
  it('keeps the current retention execution state when an older poll arrives afterwards', async () => {
    api.getRetention.mockResolvedValue({ ...policy, enabled: true })
    api.runRetention.mockResolvedValue({
      ...policy,
      enabled: true,
      version: '1',
      lastResult: 'RUNNING',
    })
    const root = await mount(RetentionDialog)
    const late = deferred<typeof policy>()
    api.getRetention.mockReturnValueOnce(late.promise)
    await vi.advanceTimersByTimeAsync(5000)
    await click(root, '立即执行')
    expect(text(root)).toContain('正在清理')
    late.resolve({ ...policy, enabled: true })
    await flush()
    expect(text(root)).toContain('正在清理')
    expect(api.runRetention).toHaveBeenCalledExactlyOnceWith('0')
  })
  it('does not restore a queued export after its cancellation receipt was refreshed', async () => {
    const job = {
      id: '1',
      actorId: '1',
      state: 'QUEUED',
      format: 'CSV',
      rowCount: 0,
      fileBytes: 0,
      reasonCode: null,
      createdAt: '2026-10-17T02:00:00Z',
      finishedAt: null,
      expiresAt: '2026-10-18T02:00:00Z',
      downloadable: false,
      filter: {},
    }
    api.getExports.mockResolvedValue([job])
    const root = await mount(ExportDialog, { filter: { page: 1, size: 20 } })
    const late = deferred<(typeof job)[]>()
    api.getExports.mockReturnValueOnce(late.promise)
    await vi.advanceTimersByTimeAsync(3000)
    api.getExports.mockResolvedValueOnce([{ ...job, state: 'CANCELLED' }])
    await click(root, '取消')
    expect(text(root)).toContain('已取消')
    late.resolve([job])
    await flush()
    expect(text(root)).toContain('已取消')
    expect(text(root)).not.toContain('等待导出')
    expect(api.cancelExport).toHaveBeenCalledExactlyOnceWith('1')
  })
  it('requires preview and only sends command fields from the policy response', async () => {
    const root = await mount(RetentionDialog)
    await change(root, 'ElSwitch', true)
    expect(button(root, '保存并启用').props.disabled).toBe(true)
    await click(root, '预览范围')
    expect(text(root)).toContain('初始化记录')
    await click(root, '保存并启用')
    expect(api.saveRetention).toHaveBeenCalledExactlyOnceWith({
      enabled: true,
      mode: 'AGE',
      keepMonths: 6,
      maxRecords: 50000,
      version: '0',
    })
  })
  it('does not accept a preview response after the form has changed', async () => {
    let complete!: (value: unknown) => void
    api.previewRetention.mockImplementation(
      () =>
        new Promise((resolve) => {
          complete = resolve
        }),
    )
    const root = await mount(RetentionDialog)
    await change(root, 'ElSwitch', true)
    const pending = click(root, '预览范围')
    await flush()
    expect(control(root, 'ElForm').props.disabled).toBe(true)
    await change(root, 'ElInputNumber', 12)
    complete(preview)
    await pending
    expect(button(root, '保存并启用').props.disabled).toBe(true)
    expect(api.saveRetention).not.toHaveBeenCalled()
  })
  it('does not immediately run an unsaved strategy', async () => {
    api.getRetention.mockResolvedValue({ ...policy, enabled: true })
    const root = await mount(RetentionDialog)
    expect(button(root, '立即执行').props.disabled).toBe(false)
    await change(root, 'ElInputNumber', 12)
    expect(button(root, '立即执行').props.disabled).toBe(true)
    await click(root, '立即执行')
    expect(api.runRetention).not.toHaveBeenCalled()
  })
  it('counts the whole selected range and blocks an oversized export', async () => {
    api.getAuditPage.mockResolvedValue({ items: [], total: 100001, page: 1, size: 1 })
    const root = await mount(ExportDialog, { filter: { page: 3, size: 20, module: 'CAMERA' } })
    expect(api.getAuditPage).toHaveBeenCalledWith({ page: 1, size: 1, module: 'CAMERA' })
    expect(text(root)).toContain('相机通道')
    expect(text(root)).toContain('超过10万条')
    expect(button(root, '导出当前筛选').props.disabled).toBe(true)
    await click(root, '导出当前筛选')
    expect(api.createExport).not.toHaveBeenCalled()
  })
  it('does not resume polling after a pending dialog load is unmounted', async () => {
    let complete!: (value: unknown) => void
    api.getExports.mockImplementation(
      () =>
        new Promise((resolve) => {
          complete = resolve
        }),
    )
    await mount(ExportDialog, { filter: { page: 1, size: 20 } })
    app?.unmount()
    app = undefined
    complete([{ state: 'RUNNING' }])
    await flush()
    await vi.advanceTimersByTimeAsync(10000)
    expect(api.getExports).toHaveBeenCalledTimes(1)
  })
})
