import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createRenderer, nextTick, ssrContextKey } from 'vue'
import type { PageOverview, QueryResult, RoleOverview } from '../../api/access-query/types'
import { ApiRequestError } from '../../lib/http/error'
import { clearIdentity } from '../../session/state'
import AccessQuery from './AccessQuery.vue'

const api = vi.hoisted(() => ({
  getPageExcel: vi.fn(),
  getRoleOverview: vi.fn(),
  getPageOverview: vi.fn(),
  getRoleUsers: vi.fn(),
  getRolePages: vi.fn(),
  getPageUsers: vi.fn(),
  getPageRoles: vi.fn(),
}))
vi.mock('../../api/access-query/api', () => api)
vi.mock('../../components/feedback/RequestError.vue', () => ({ default: { render: () => null } }))
vi.mock('../../components/table/TablePanel.vue', () => ({ default: { render: () => null } }))
vi.mock('element-plus', async () => {
  const { defineComponent } = await import('vue')
  const stub = defineComponent({ render: () => null })
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
        'ElTableColumn',
        'ElTag',
      ].map((name) => [name, stub]),
    ),
    vLoading: {},
  }
})
const role: RoleOverview = {
  role: { id: '1', code: 'READER', name: '观察员', status: 'ENABLED' },
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
    menuPath: '视频管理 / 相机管理',
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
function result<T>(items: T[] = []): QueryResult<T> {
  return {
    items,
    page: 1,
    size: 20,
    total: items.length,
    checkedAt: '2026-10-18T08:00:00Z',
    subjectUser: null,
    subjectPage: null,
    subjectAccess: null,
  }
}
function deferred<T>() {
  let resolve!: (value: T) => void
  const promise = new Promise<T>((done) => {
    resolve = done
  })
  return { promise, resolve }
}
interface State {
  mode: 'role' | 'page'
  keyword: string
  appliedKeyword: string
  pageReady: boolean
  exportAdvice: string | undefined
  exportError: unknown
  current: number
  roles: RoleOverview[]
  pages: PageOverview[]
  drawer: boolean
  detailRole: RoleOverview | null
  detailPage: PageOverview | null
  total: number
  load: () => Promise<void>
  search: () => void
  exportPages: () => Promise<void>
  openRole: (role: RoleOverview, tab: 'users' | 'pages') => void
}
const renderer = createRenderer<Record<string, unknown>, Record<string, unknown>>({
  createElement: () => ({}),
  createText: () => ({}),
  createComment: () => ({}),
  setText() {},
  setElementText() {},
  patchProp() {},
  insert() {},
  remove() {},
  parentNode: () => null,
  nextSibling: () => null,
})
let dispose: () => void = () => {}
async function mount() {
  const app = renderer.createApp({ ...AccessQuery, render: () => null })
  app.provide(ssrContextKey, { modules: new Set<string>() })
  app.mount({})
  dispose = () => app.unmount()
  await Promise.resolve()
  await nextTick()
  return (app._instance as unknown as { setupState: State }).setupState
}
beforeEach(() => {
  vi.resetAllMocks()
  clearIdentity()
  for (const call of Object.values(api)) call.mockResolvedValue(result())
})
afterEach(() => {
  dispose()
  clearIdentity()
})

describe('permission-query overview lifecycle', () => {
  it('loads role overview immediately without a selected user or page', async () => {
    api.getRoleOverview.mockResolvedValueOnce(result([role]))
    const state = await mount()
    expect(api.getRoleOverview).toHaveBeenCalled()
    expect(state.roles).toEqual([role])
  })
  it('switches to page overview and discards an old role response', async () => {
    const state = await mount()
    const old = deferred<QueryResult<RoleOverview>>()
    api.getRoleOverview.mockReturnValueOnce(old.promise)
    const pending = state.load()
    state.mode = 'page'
    await nextTick()
    api.getPageOverview.mockResolvedValueOnce(result([page]))
    await state.load()
    old.resolve(result([role]))
    await pending
    expect(state.roles).toEqual([])
    expect(state.pages).toEqual([page])
  })
  it('clears displayed people and detail after identity changes', async () => {
    const state = await mount()
    api.getRoleOverview.mockResolvedValueOnce(result([role]))
    await state.load()
    state.openRole(role, 'users')
    expect(state.drawer).toBe(true)
    clearIdentity()
    expect(state.roles).toEqual([])
    expect(state.detailRole).toBeNull()
    expect(state.drawer).toBe(false)
    expect(state.total).toBe(0)
  })
  it('exports only the last successfully queried page filter, never unsent input', async () => {
    const state = await mount()
    state.mode = 'page'
    await nextTick()
    state.keyword = '相机'
    api.getPageOverview.mockResolvedValueOnce(result([page]))
    state.search()
    await Promise.resolve()
    await nextTick()
    expect(state.appliedKeyword).toBe('相机')
    expect(state.pageReady).toBe(true)
    state.keyword = '未查询的词'
    state.current = 2
    await state.load()
    expect(api.getPageOverview).toHaveBeenLastCalledWith(
      expect.objectContaining({ page: 2, keyword: '相机' }),
      expect.any(AbortSignal),
    )
    api.getPageExcel.mockRejectedValueOnce(new Error('download unavailable'))
    await state.exportPages()
    expect(api.getPageExcel).toHaveBeenCalledWith('相机', expect.any(AbortSignal))
  })
  it('turns a bounded Excel export rejection into a specific next step', async () => {
    const state = await mount()
    state.mode = 'page'
    await nextTick()
    await state.load()
    api.getPageExcel.mockRejectedValueOnce(
      new ApiRequestError('PAYLOAD_TOO_LARGE', '导出范围过大', { status: 413 }),
    )
    await state.exportPages()
    expect(state.exportError).toBeInstanceOf(ApiRequestError)
    expect(state.exportAdvice).toContain('缩小页面搜索条件')
    clearIdentity()
    expect(state.exportError).toBeNull()
    expect(state.pageReady).toBe(false)
  })
})
