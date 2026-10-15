// @vitest-environment vue-renderer
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { nextTick, type App, type Component } from 'vue'
import { node, nodes, renderer, text, type Host } from '../support/renderer'
import RetentionDialog from '../../src/features/audit/RetentionDialog.vue'
import ExportDialog from '../../src/features/audit/ExportDialog.vue'

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
}))
vi.mock('../../src/api/audit/maintenance', () => api)
vi.mock('../../src/api/audit/api', () => ({ getAuditPage: api.getAuditPage }))
vi.mock('element-plus', async () => {
  const { defineComponent, h } = await import('vue')
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
})
afterEach(() => {
  app?.unmount()
  vi.useRealTimers()
})

describe('log maintenance dialogs', () => {
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
