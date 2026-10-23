// @vitest-environment vue-renderer
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { nextTick, reactive, type App } from 'vue'
import { node, nodes, renderer, text } from '../support/renderer'
import Diagnostics from '../../src/views/monitor/Diagnostics.vue'
import DiagnosticLink from '../../src/features/diagnostics/DiagnosticLink.vue'
import { sessionState, clearIdentity } from '../../src/session/state'

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
    'ElDialog',
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
async function mount(props: InstanceType<typeof Diagnostics>['$props'] = {}) {
  const root = node('root')
  app = renderer.createApp(Diagnostics, { ...props })
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
    nextCursor: null,
    cursorExpired: false,
    droppedEvents: 0,
  })
})
afterEach(() => {
  app?.unmount()
  clearIdentity()
})

describe('diagnostic query page', () => {
  it('shows the query total rather than the current page size', async () => {
    mock.getDiagnostics.mockResolvedValue({ items: [], total: 321, totalExact: true })
    const root = await mount()
    expect(text(root)).toContain('共 321 条')
    expect(text(root)).toContain('本页 0 条')
  })
  it('labels a bounded count as a lower bound and preserves unknown totals', async () => {
    mock.getDiagnostics.mockResolvedValue({ items: [], total: 17, totalExact: false })
    const root = await mount()
    expect(text(root)).toContain('已匹配至少 17 条')
    expect(text(root)).not.toContain('共 17 条')
    mock.getDiagnostics.mockResolvedValue({ items: [] })
    button(root, '重置').props.onClick!()
    await flush()
    expect(text(root)).toContain('总数量暂不可用')
  })
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
    expect(text(root)).toContain('已达到扫描预算')
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
    expect(text(root)).not.toContain('已达到扫描预算')
    expect(nodes(root).some((item) => item.type === 'ElButton' && text(item) === '查询日志')).toBe(
      true,
    )
  })

  it('uses backend cursors, preserves query bounds and returns to the previous page', async () => {
    mock.getDiagnostics.mockResolvedValueOnce({
      items: [],
      nextCursor: 'page-two',
      truncated: false,
      unavailable: false,
    })
    const root = await mount()
    const first = mock.getDiagnostics.mock.calls[0]![0]
    mock.getDiagnostics.mockResolvedValueOnce({
      items: [],
      nextCursor: null,
      truncated: false,
      unavailable: false,
    })
    button(root, '下一页').props.onClick!()
    await flush()
    expect(mock.getDiagnostics).toHaveBeenLastCalledWith({ ...first, cursor: 'page-two' })
    expect(text(root)).toContain('第 2 页')
    expect(button(root, '下一页').props.disabled).toBe(true)
    button(root, '上一页').props.onClick!()
    await flush()
    expect(mock.getDiagnostics).toHaveBeenLastCalledWith({ ...first, cursor: '' })
    expect(text(root)).toContain('第 1 页')
  })

  it('does not apply an in-flight response after a new invalid query', async () => {
    let resolve!: (value: unknown) => void
    mock.getDiagnostics.mockImplementationOnce(
      () =>
        new Promise((done) => {
          resolve = done
        }),
    )
    const root = await mount()
    mock.route.query = { traceId: 'invalid' }
    await flush()
    resolve({ items: [], truncated: true, unavailable: true })
    await flush()
    expect(text(root)).toContain('请输入完整的32位请求标识')
    expect(text(root)).not.toContain('已达到扫描预算')
    expect(mock.getDiagnostics).toHaveBeenCalledTimes(1)
  })

  it('resets cursor after page size changes and exposes cursor expiry and dropped logs', async () => {
    mock.getDiagnostics.mockResolvedValueOnce({
      items: [],
      nextCursor: null,
      cursorExpired: true,
      droppedEvents: 12,
    })
    const root = await mount()
    expect(text(root)).toContain('请重新查询')
    expect(text(root)).toContain('丢弃 12 条')
    expect(button(root, '下一页').props.disabled).toBe(true)
    const size = nodes(root).find(
      (n) => n.type === 'ElSelect' && n.props['aria-label'] === '每页条数',
    )!
    ;(size.props['onUpdate:modelValue'] as (v: number) => void)(20)
    ;(size.props.onChange as () => void)()
    await flush()
    expect(mock.getDiagnostics).toHaveBeenLastCalledWith(
      expect.objectContaining({ size: 20, cursor: '' }),
    )
  })

  it('loads context in place without reading or changing the parent route', async () => {
    mock.route.query = { traceId: 'b'.repeat(32) }
    await mount({ context: { traceId: 'a'.repeat(32), time: '2026-10-14T12:00:00Z' } })
    expect(mock.getDiagnostics).toHaveBeenLastCalledWith(
      expect.objectContaining({ traceId: 'a'.repeat(32) }),
    )
    expect(mock.route.query.traceId).toBe('b'.repeat(32))
  })

  it('renders complete diagnostic JSON even when request bodies were not captured', async () => {
    const event = {
      occurredAt: '2026-10-17T00:00:00Z',
      kind: 'REQUEST',
      level: 'INFO',
      traceId: 'a'.repeat(32),
      route: '/camera/devices',
      method: 'POST',
      status: 200,
      durationMs: 12,
      request: null,
      response: null,
      stack: [],
    }
    mock.getDiagnostics.mockResolvedValue({ items: [event] })
    const root = await mount()
    const table = nodes(root).find((n) => n.type === 'ElTable')!
    ;(table.props.onRowDblclick as (row: unknown) => void)(event)
    await flush()
    const json = nodes(root).find((n) => n.props['aria-label'] === '完整诊断 JSON')!
    expect(JSON.parse(text(json))).toEqual(event)
    expect(text(root)).toContain('旧记录不会补录')
    expect(mock.getDiagnostics).toHaveBeenCalledTimes(1)
  })

  it('offers existing capture controls inside related diagnostics only to super administrators', async () => {
    sessionState.me = {
      user: { id: '1001' },
      modules: ['diagnostics'],
      isSuperAdmin: true,
    } as NonNullable<typeof sessionState.me>
    const root = await mount({ context: { traceId: 'a'.repeat(32) } })
    expect(button(root, '临时详细记录')).toBeDefined()
    sessionState.me.isSuperAdmin = false
    await flush()
    expect(button(root, '临时详细记录')).toBeUndefined()
  })

  it('opens the diagnostic link in a dialog and requires diagnostic module access', async () => {
    const root = node('root')
    app = renderer.createApp(DiagnosticLink, { traceId: 'a'.repeat(32) })
    app.mount(root)
    await flush()
    expect(text(root)).not.toContain('查看诊断日志')
    sessionState.me = { modules: ['diagnostics'] } as NonNullable<typeof sessionState.me>
    await flush()
    button(root, '查看诊断日志').props.onClick!()
    await flush()
    const dialog = nodes(root).find((n) => n.type === 'ElDialog')!
    expect(dialog.props.title).toBe('关联诊断日志')
    expect(dialog.props).toHaveProperty('append-to-body')
    expect(mock.route.query).toEqual({})
    expect(nodes(root).some((n) => n.type === 'RouterLink')).toBe(false)
  })
})

function button(root: ReturnType<typeof node>, label: string) {
  return nodes(root).find((n) => n.type === 'ElButton' && text(n) === label)! as ReturnType<
    typeof node
  > & { props: { onClick?: () => void; disabled?: boolean } }
}
