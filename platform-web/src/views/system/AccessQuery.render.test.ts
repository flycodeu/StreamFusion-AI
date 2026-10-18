// @vitest-environment vue-renderer
import { afterEach, beforeEach, expect, it, vi } from 'vitest'
import { defineComponent, h, nextTick, ssrContextKey, type App } from 'vue'
import { node, nodes, renderer, text, type Host } from '../../../tests/support/renderer'
import AccessQuery from './AccessQuery.vue'
import type { PageOverview, RoleOverview } from '../../api/access-query/types'

const api = vi.hoisted(() => ({
  getRoleOverview: vi.fn(),
  getPageOverview: vi.fn(),
  getRoleUsers: vi.fn(),
  getRolePages: vi.fn(),
  getPageUsers: vi.fn(),
  getPageRoles: vi.fn(),
  getPageExcel: vi.fn(),
}))
vi.mock('../../api/access-query/api', () => api)
vi.mock('../../components/feedback/RequestError.vue', () => ({ default: { render: () => null } }))
vi.mock('../../components/table/TablePanel.vue', () => ({
  default: defineComponent({
    setup:
      (_, { slots }) =>
      () =>
        h('table-panel', [slots.default?.(), slots.actions?.(), slots.footer?.()]),
  }),
}))
vi.mock('element-plus', () => {
  const wrapper = defineComponent({
    inheritAttrs: false,
    setup:
      (_, { attrs, slots }) =>
      () =>
        h('stub', attrs, slots.default?.()),
  })
  const column = defineComponent({
    inheritAttrs: false,
    setup:
      (_, { attrs }) =>
      () =>
        h('column', attrs, String(attrs.label ?? '')),
  })
  return {
    ...Object.fromEntries(
      [
        'ElButton',
        'ElDrawer',
        'ElInput',
        'ElPagination',
        'ElRadioButton',
        'ElRadioGroup',
        'ElTable',
        'ElTag',
      ].map((name) => [name, wrapper]),
    ),
    ElTableColumn: column,
    vLoading: {},
  }
})

const result = {
  items: [],
  total: 0,
  page: 1,
  size: 20,
  checkedAt: '2026-10-18T08:00:00Z',
  subjectUser: null,
  subjectPage: null,
  subjectAccess: null,
}
const role: RoleOverview = {
  role: { id: '8', code: 'READER', name: '观察员', status: 'ENABLED' },
  assignedUserCount: 1,
  usableUserCount: 1,
  assignedPageCount: 1,
  effectivePageCount: 1,
}
const page: PageOverview = {
  menu: {
    id: '1101',
    name: '相机管理',
    path: '/camera/manage',
    menuPath: '视频 / 相机管理',
    moduleKey: 'camera',
    enabled: true,
    chainEnabled: true,
    navigationVisible: true,
    moduleRegistered: true,
    blockers: [],
  },
  assignedRoleCount: 1,
  activeRoleCount: 1,
  assignedUserCount: 1,
  eligibleUserCount: 1,
}
let app: App | null = null
beforeEach(() => {
  vi.clearAllMocks()
  for (const call of Object.values(api)) call.mockResolvedValue(result)
})
afterEach(() => app?.unmount())

it('shows department columns in both account details and Excel only in page view', async () => {
  const root = node('root')
  app = renderer.createApp(AccessQuery)
  app.provide(ssrContextKey, { modules: new Set<string>() })
  app.mount(root)
  await nextTick()
  const state = (
    app._instance as unknown as {
      setupState: {
        mode: 'role' | 'page'
        openRole: (role: RoleOverview, tab: 'users') => void
        openPage: (page: PageOverview, tab: 'users') => void
      }
    }
  ).setupState
  expect(text(root)).not.toContain('导出当前结果（Excel）')
  state.openRole(role, 'users')
  await nextTick()
  expect(
    nodes(root)
      .filter((item: Host) => item.type === 'column')
      .map((item) => item.props.label),
  ).toContain('部门')
  state.mode = 'page'
  await nextTick()
  expect(text(root)).toContain('导出当前结果（Excel）')
  state.openPage(page, 'users')
  await nextTick()
  expect(
    nodes(root)
      .filter((item: Host) => item.type === 'column')
      .map((item) => item.props.label),
  ).toContain('部门')
})
