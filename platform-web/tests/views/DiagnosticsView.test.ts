// @vitest-environment vue-renderer
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { nextTick, reactive, type App } from 'vue'
import { node, nodes, renderer, text } from '../support/renderer'
import Diagnostics from '../../src/views/monitor/Diagnostics.vue'

const mock = vi.hoisted(() => ({
  route: { query: {} as Record<string, string> },
  getDiagnostics: vi.fn(),
}))
vi.mock('../../src/api/diagnostics/api', () => ({ getDiagnostics: mock.getDiagnostics }))
vi.mock('vue-router', async (original) => ({
  ...(await original<typeof import('vue-router')>()),
  useRoute: () => mock.route,
}))
vi.mock('element-plus', async () => {
  const { defineComponent, h } = await import('vue')
  const controls = [
    'ElButton',
    'ElDatePicker',
    'ElDrawer',
    'ElEmpty',
    'ElForm',
    'ElFormItem',
    'ElInput',
    'ElOption',
    'ElSelect',
    'ElTable',
    'ElTableColumn',
    'ElTag',
  ]
  return {
    ...Object.fromEntries(
      controls.map((name) => [
        name,
        defineComponent({
          inheritAttrs: false,
          setup(_, { attrs, slots }) {
            return () => h(name, attrs, name === 'ElTableColumn' ? [] : slots.default?.())
          },
        }),
      ]),
    ),
    ElMessage: { success: vi.fn(), warning: vi.fn() },
    vLoading: {},
  }
})
let app: App | undefined
async function mount() {
  const root = node('root')
  app = renderer.createApp(Diagnostics)
  app.mount(root)
  await flush()
  return root
}
async function flush() {
  await Promise.resolve()
  await nextTick()
  await Promise.resolve()
  await nextTick()
}
beforeEach(() => {
  mock.route = reactive({ query: {} })
  mock.getDiagnostics.mockReset().mockResolvedValue({
    items: [],
    truncated: false,
    unavailable: false,
    retentionDays: 7,
    limit: 200,
  })
})
afterEach(() => app?.unmount())

describe('diagnostic query page', () => {
  it('loads trace and event time from a diagnostic link', async () => {
    Object.assign(mock.route.query, { traceId: 'a'.repeat(32), time: '2026-10-14T12:00:00Z' })
    await mount()
    expect(mock.getDiagnostics).toHaveBeenCalledWith(
      expect.objectContaining({
        traceId: 'a'.repeat(32),
        startTime: '2026-10-14T11:00:00.000Z',
        endTime: '2026-10-14T13:00:00.000Z',
      }),
    )
  })
  it('does not submit an invalid trace and makes the input error actionable', async () => {
    mock.route.query.traceId = 'invalid'
    const root = await mount()
    expect(mock.getDiagnostics).not.toHaveBeenCalled()
    expect(text(root)).toContain('请输入完整的32位请求标识')
  })
  it('shows bounded-query and unavailable notices instead of a false complete result', async () => {
    mock.getDiagnostics.mockResolvedValue({
      items: [],
      truncated: true,
      unavailable: true,
      retentionDays: 7,
      limit: 200,
    })
    const root = await mount()
    expect(text(root)).toContain('已达到本次查询上限')
    expect(text(root)).toContain('当前结果可能不完整')
  })
  it('ignores a late previous query response after a route filter change', async () => {
    let first!: (value: unknown) => void
    mock.getDiagnostics.mockImplementationOnce(
      () =>
        new Promise((resolve) => {
          first = resolve
        }),
    )
    const root = await mount()
    mock.route.query = { traceId: 'b'.repeat(32) }
    await flush()
    first({ items: [], truncated: true, unavailable: true, retentionDays: 7, limit: 200 })
    await flush()
    expect(mock.getDiagnostics).toHaveBeenCalledTimes(2)
    expect(text(root)).not.toContain('已达到本次查询上限')
    expect(nodes(root).some((item) => item.type === 'ElButton' && text(item) === '查询日志')).toBe(
      true,
    )
  })
})
