// @vitest-environment vue-renderer
import { afterEach, beforeEach, expect, it, vi } from 'vitest'
import { nextTick } from 'vue'
import type { App } from 'vue'
import { node, nodes, renderer, text } from '../support/renderer'
import Panel from '../../src/features/security/SecurityAlertPanel.vue'
import { securityAdmin, securityAlert, securityFeed } from '../support/securityAlerts'
import { sessionState } from '../../src/session/state'

const getSecurityAlerts = vi.hoisted(() => vi.fn())
vi.mock('../../src/api/security/alerts', async (original) => ({
  ...(await original<object>()),
  getSecurityAlerts,
}))
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
        'ElPagination',
        'ElTable',
        'ElTableColumn',
        'ElTag',
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
  app = renderer.createApp(Panel)
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
})
it('shows disabled/degraded monitoring instead of implying full coverage', async () => {
  getSecurityAlerts.mockResolvedValue({
    ...result(),
    monitor: { ...securityFeed(), enabled: false, persistenceHealthy: false, droppedEvents: 9 },
  })
  const root = await mount()
  const alerts = nodes(root).filter((n) => n.type === 'ElAlert')
  expect(alerts.map((n) => n.props.title)).toContain('安全监测已关闭')
  expect(alerts.map((n) => n.props.title)).toContain('安全监测异常')
  expect(text(root)).toContain('告警保存失败')
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
  sessionState.me = { ...securityAdmin(), user: { ...securityAdmin().user, id: '2' } }
  await settle()
  expect(text(root)).not.toContain('192.0.2.1')
  expect(nodes(root).find((n) => n.type === 'ElTable')!.props.data).toEqual([])
})
