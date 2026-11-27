// @vitest-environment vue-renderer
import { afterEach, beforeEach, expect, it, vi } from 'vitest'
import { nextTick } from 'vue'
import type { App } from 'vue'
import { node, nodes, renderer, text } from '../support/renderer'
import SecurityAlertsPage from '../../src/views/monitor/SecurityAlerts.vue'
import { securityAdmin, securityAlert, securityFeed } from '../support/securityAlerts'
import { sessionState } from '../../src/session/state'

const getSecurityAlerts = vi.hoisted(() => vi.fn())
vi.mock('../../src/api/security/alerts', async (original) => ({
  ...(await original<object>()),
  getSecurityAlerts,
}))
vi.mock('../../src/features/diagnostics/DiagnosticLink.vue', async () => {
  const { defineComponent, h } = await import('vue')
  return {
    default: defineComponent({
      inheritAttrs: false,
      setup:
        (_, { attrs }) =>
        () =>
          h('DiagnosticLink', attrs),
    }),
  }
})
vi.mock('element-plus', async () => {
  const { defineComponent, h } = await import('vue')
  return {
    vLoading: {},
    ...Object.fromEntries(
      [
        'ElAlert',
        'ElButton',
        'ElDescriptions',
        'ElDescriptionsItem',
        'ElDrawer',
        'ElEmpty',
        'ElIcon',
        'ElPagination',
        'ElTable',
        'ElTableColumn',
        'ElTag',
        'ElTooltip',
        'ElDialog',
      ].map((name) => [
        name,
        defineComponent({
          inheritAttrs: false,
          setup:
            (_, { attrs, slots }) =>
            () =>
              h(
                name,
                attrs,
                name === 'ElTableColumn' || (name === 'ElDrawer' && !attrs.modelValue)
                  ? []
                  : slots.default?.(),
              ),
        }),
      ]),
    ),
  }
})
let app: App
const result = () => ({
  items: [securityAlert()],
  page: 1,
  size: 20,
  total: 1,
  monitor: securityFeed(1),
})
const settle = async () => {
  for (let i = 0; i < 8; i++) await nextTick()
}
const mount = async () => {
  const root = node('root')
  app = renderer.createApp(SecurityAlertsPage)
  app.mount(root)
  await settle()
  return root
}
beforeEach(() => {
  getSecurityAlerts.mockReset().mockResolvedValue(result())
  sessionState.me = securityAdmin()
})
afterEach(() => {
  app?.unmount()
  sessionState.me = null
  sessionState.epoch++
})

it('keeps alert evidence and JSON without explanatory page notes', async () => {
  const root = await mount()
  expect(text(root)).not.toContain('按来源 IP 关联异常行为')
  expect(text(root)).not.toContain('当前实例')
  const table = nodes(root).find((n) => n.type === 'ElTable')!
  ;(table.props['onRowDblclick'] as (value: unknown) => void)(securityAlert())
  await settle()
  expect(text(root)).toContain('192.0.2.1')
  expect(text(root)).toContain('查看告警 JSON')
  expect(text(root)).toContain('待核查')
  expect(text(root)).not.toContain('先核对是否为同一出口')
  expect(nodes(root).find((n) => n.type === 'DiagnosticLink')?.props).toEqual({
    'trace-id': securityAlert().traceIds[0],
    time: securityAlert().lastSeen,
  })
})
it('shows the healthy empty state after a successful read', async () => {
  getSecurityAlerts.mockResolvedValue({
    ...result(),
    items: [],
    total: 0,
    monitor: securityFeed(),
  })
  const root = await mount()
  expect(text(root)).toContain('监测正常')
  expect(nodes(root).find((n) => n.type === 'ElTable')?.props['empty-text']).toBe(
    '最近 7 天暂无安全告警',
  )
})
it('shows disabled/degraded monitoring instead of implying full coverage', async () => {
  getSecurityAlerts.mockResolvedValue({
    ...result(),
    items: [],
    total: 0,
    monitor: { ...securityFeed(), enabled: false, persistenceHealthy: false, droppedEvents: 9 },
  })
  const root = await mount()
  const alerts = nodes(root).filter((n) => n.type === 'ElAlert')
  expect(alerts.map((n) => n.props.title)).toContain('安全监测已关闭')
  expect(alerts.map((n) => n.props.title)).toContain('安全监测异常')
  expect(text(root)).toContain('告警保存失败')
  expect(text(root)).not.toContain('监测正常')
  expect(nodes(root).find((n) => n.type === 'ElTable')?.props['empty-text']).toBe('暂无留存告警')
})
it('shows a failed refresh without presenting old data or a healthy status', async () => {
  const root = await mount()
  getSecurityAlerts.mockRejectedValueOnce(new Error('offline'))
  const refresh = nodes(root).find((n) => n.type === 'ElButton' && text(n) === '刷新')!
  ;(refresh.props.onClick as () => void)()
  await settle()
  expect(text(root)).toContain('安全告警读取失败')
  expect(text(root)).not.toContain('监测正常')
  expect(nodes(root).some((n) => n.type === 'ElTable')).toBe(false)

  ;(refresh.props.onClick as () => void)()
  await settle()
  expect(text(root)).toContain('监测正常')
  expect(text(root)).not.toContain('安全告警读取失败')
})
it('returns to an available page when alerts expire during pagination', async () => {
  getSecurityAlerts.mockResolvedValueOnce({ ...result(), total: 21 })
  const root = await mount()
  getSecurityAlerts.mockResolvedValueOnce({ ...result(), items: [], page: 2 })
  const pagination = nodes(root).find((n) => n.type === 'ElPagination')!
  ;(pagination.props['onUpdate:currentPage'] as (page: number) => void)(2)
  await settle()
  expect(getSecurityAlerts.mock.calls.map(([query]) => query.page)).toEqual([1, 2, 1])
  expect(nodes(root).find((n) => n.type === 'ElPagination')?.props['current-page']).toBe(1)
})
it.each([
  { ...securityAdmin(), isSuperAdmin: false },
  { ...securityAdmin(), modules: [] },
])('requires both super-admin and audit access before reading alerts', async (account) => {
  sessionState.me = account
  const root = await mount()
  expect(getSecurityAlerts).not.toHaveBeenCalled()
  expect(nodes(root).find((n) => n.type === 'ElEmpty')?.props.description).toContain('无权')
})
it('clears sensitive rows and drawer immediately when permission is revoked', async () => {
  const root = await mount()
  sessionState.me!.isSuperAdmin = false
  await settle()
  expect(nodes(root).filter((n) => n.type === 'ElTable')).toHaveLength(0)
  expect(text(root)).not.toContain('192.0.2.1')
})
it('aborts outstanding reads when the component is unmounted', async () => {
  getSecurityAlerts.mockReturnValue(new Promise(() => {}))
  await mount()
  const signal = getSecurityAlerts.mock.calls[0]![1] as AbortSignal
  app.unmount()
  expect(signal.aborted).toBe(true)
})

it('clears the previous account evidence even if an identity refresh does not change the epoch', async () => {
  const root = await mount()
  const table = nodes(root).find((n) => n.type === 'ElTable')!
  ;(table.props['onRowDblclick'] as (value: unknown) => void)(securityAlert())
  await settle()
  getSecurityAlerts.mockReturnValueOnce(new Promise(() => {}))
  sessionState.me = { ...securityAdmin(), user: { ...securityAdmin().user, id: '2' } }
  await settle()
  expect(text(root)).not.toContain('192.0.2.1')
  expect(nodes(root).some((n) => n.type === 'ElTable')).toBe(false)
  expect(getSecurityAlerts).toHaveBeenCalledTimes(2)
})
