// @vitest-environment vue-renderer
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createRenderer, nextTick } from 'vue'
import type { App, Component } from 'vue'
import type { Department } from '../../api/departments/types'
import type { MenuNode } from '../../api/menus/types'
import type { Role, RoleMenus } from '../../api/roles/types'
import type { UserCredentialResult, UserDetail } from '../../api/user/types'
import type { AuditDetail, AuditEntry } from '../../api/audit/types'
import type { IpBlock } from '../../api/security/types'
import { clearIdentity, setIdentity } from '../../session/state'
import DepartmentView from './Department.vue'
import MenuView from './Menu.vue'
import RoleView from './Role.vue'
import UserView from './User.vue'
import AuditView from './Audit.vue'
import LoginRecordTable from '../../features/login-records/LoginRecordTable.vue'
import IpBlockPanel from '../../features/security/IpBlockPanel.vue'

const api = vi.hoisted(() => ({
  getDepartments: vi.fn(),
  createDepartment: vi.fn(),
  updateDepartment: vi.fn(),
  deleteDepartment: vi.fn(),
  getMenus: vi.fn(),
  deleteMenu: vi.fn(),
  getRoles: vi.fn(),
  createRole: vi.fn(),
  changeRoleStatus: vi.fn(),
  deleteRole: vi.fn(),
  getRoleMenus: vi.fn(),
  setRoleMenus: vi.fn(),
  getUsers: vi.fn(),
  getUser: vi.fn(),
  changeUserStatus: vi.fn(),
  getDepartmentOptions: vi.fn(),
  deleteUser: vi.fn(),
  resetUserPassword: vi.fn(),
  getAuditPage: vi.fn(),
  getAuditDetail: vi.fn(),
  getLoginRecords: vi.fn(),
  getIpBlocks: vi.fn(),
  unblockIp: vi.fn(),
  success: vi.fn(),
  confirm: vi.fn(),
}))
vi.mock('../../api/departments/api', () => api)
vi.mock('../../api/menus/api', () => api)
vi.mock('../../api/roles/api', () => api)
vi.mock('../../api/user/api', () => api)
vi.mock('../../api/audit/api', () => api)
vi.mock('../../api/login-records/api', () => api)
vi.mock('../../api/security/api', () => api)
vi.mock('../../session/session', () => ({ refreshIdentity: vi.fn() }))
vi.mock('vue-router', () => ({
  useRoute: () => ({ path: '/system/Menu', query: {}, hash: '' }),
  useRouter: () => ({ replace: vi.fn() }),
}))
vi.mock('../../components/table/ColumnPicker.vue', async () => {
  const { defineComponent, h } = await import('vue')
  return {
    default: defineComponent({
      inheritAttrs: false,
      setup(_, { attrs }) {
        return () => h('ColumnPicker', attrs)
      },
    }),
  }
})
vi.mock('../../components/icons/IconPicker.vue', () => ({ default: { render: () => null } }))
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
    'ElDatePicker',
    'ElDescriptions',
    'ElDescriptionsItem',
    'ElEmpty',
    'ElTabPane',
    'ElTabs',
  ]
  const stubs = Object.fromEntries(
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
    ...stubs,
    vLoading: {},
    ElMessageBox: { confirm: api.confirm },
    ElMessage: { success: api.success, warning: vi.fn() },
    ElDrawer: defineComponent({
      inheritAttrs: false,
      setup(_, { attrs, slots }) {
        return () => (attrs.modelValue ? h('ElDrawer', attrs, slots.default?.()) : null)
      },
    }),
    ElDialog: defineComponent({
      inheritAttrs: false,
      setup(_, { attrs, slots }) {
        return () =>
          attrs.modelValue ? h('ElDialog', attrs, [slots.default?.(), slots.footer?.()]) : null
      },
    }),
    ElTable: defineComponent({
      inheritAttrs: false,
      setup(_, { attrs, slots }) {
        provide('tableRows', () => attrs.data as unknown[])
        return () => h('ElTable', attrs, slots.default?.())
      },
    }),
    ElTableColumn: defineComponent({
      inheritAttrs: false,
      setup(_, { attrs, slots }) {
        const rows = inject<() => unknown[]>('tableRows', () => [])
        return () =>
          h(
            'ElTableColumn',
            attrs,
            rows().map((row) => slots.default?.({ row })),
          )
      },
    }),
    ElTree: defineComponent({
      inheritAttrs: false,
      setup(_, { attrs, expose }) {
        let selected: string[] = []
        expose({
          setCheckedKeys: (ids: string[]) => {
            selected = ids
          },
          getCheckedKeys: () => selected,
        })
        return () => h('ElTree', attrs)
      },
    }),
  }
})

interface HostNode {
  type: string
  text: string
  props: Record<string, unknown>
  children: HostNode[]
  parent: HostNode | null
}
function node(type: string, text = ''): HostNode {
  return { type, text, props: {}, children: [], parent: null }
}
const renderer = createRenderer<HostNode, HostNode>({
  createElement: (type) => node(type),
  createText: (text) => node('#text', text),
  createComment: (text) => node('#comment', text),
  setText: (target, text) => {
    target.text = text
  },
  setElementText: (target, text) => {
    target.text = text
    target.children = []
  },
  patchProp: (target, key, _, value) => {
    target.props[key] = value
  },
  insert(target, parent, anchor) {
    if (target.parent) target.parent.children.splice(target.parent.children.indexOf(target), 1)
    const index = anchor ? parent.children.indexOf(anchor) : -1
    parent.children.splice(index < 0 ? parent.children.length : index, 0, target)
    target.parent = parent
  },
  remove(target) {
    if (target.parent) target.parent.children.splice(target.parent.children.indexOf(target), 1)
    target.parent = null
  },
  parentNode: (target) => target.parent,
  nextSibling: (target) => {
    const siblings = target.parent?.children || []
    return siblings[siblings.indexOf(target) + 1] || null
  },
})
const apps: App[] = []
async function flush() {
  for (let i = 0; i < 5; i++) {
    await Promise.resolve()
    await nextTick()
  }
}
async function mount(component: Component) {
  const root = node('root')
  const app = renderer.createApp(component)
  apps.push(app)
  app.mount(root)
  await flush()
  return root
}
function findAll(root: HostNode, type: string): HostNode[] {
  return [root, ...root.children.flatMap((child) => findAll(child, type))].filter(
    (item) => item.type === type,
  )
}
function content(root: HostNode): string {
  return root.text + root.children.map(content).join('')
}
function button(root: HostNode, label: string, index = 0): HostNode {
  const found = findAll(root, 'ElButton').filter((item) => content(item) === label)[index]
  expect(found, label).toBeDefined()
  return found!
}
async function click(target: HostNode) {
  expect(target.props.disabled).not.toBe(true)
  await (target.props.onClick as () => unknown)()
  await flush()
}
async function input(target: HostNode, value: string | number) {
  ;(target.props['onUpdate:modelValue'] as (value: string | number) => void)(value)
  await flush()
}
function field(root: HostNode, label: string): HostNode {
  return findAll(root, 'ElFormItem')
    .filter((item) => item.props.label === label)
    .at(-1)!
}
function department(id: string, name: string, children: Department[] = []): Department {
  return { id, name, parentId: null, sortOrder: 0, memberCount: 0, version: '0', children }
}
function role(id: string): Role {
  return {
    id,
    name: `角色${id}`,
    code: `ROLE_${id}`,
    description: null,
    status: 'ENABLED',
    version: '0',
  }
}
function user(id: string): UserDetail {
  return {
    id,
    username: `user${id}`,
    nickname: null,
    avatarKey: null,
    status: 1,
    lockedUntil: null,
    loginRestricted: false,
    departments: [],
    roles: [],
    version: '0',
    phone: null,
    email: null,
    gender: 0,
    mustChangePassword: false,
  }
}
function menu(id: string): MenuNode {
  return {
    id,
    parentId: null,
    name: `菜单${id}`,
    type: 'DIRECTORY',
    icon: null,
    sortOrder: 0,
    visible: true,
    enabled: true,
    version: '0',
    page: null,
    children: [],
  }
}
function audit(id = '1'): AuditEntry {
  return {
    id,
    actorId: '1001',
    username: 'operator',
    nickname: '运维',
    module: 'USER',
    targetId: '2',
    action: 'LOGIN',
    result: 'DENIED',
    reasonCode: 'ACCOUNT_COOLING_DOWN',
    traceId: 'a'.repeat(32),
    createdAt: '2026-09-26T00:00:00Z',
    actor: null,
    target: null,
  }
}
function auditDetail(id = '1'): AuditDetail {
  return { record: audit(id), sourceIp: '127.0.0.1', changes: {}, relations: {} }
}
function ipBlock(id = '1'): IpBlock {
  return {
    id,
    sourceIp: `192.0.2.${id}`,
    status: 'BLOCKED',
    reasonCode: 'LOGIN_FAILURE_THRESHOLD',
    failedAttempts: 20,
    windowSeconds: 600,
    blockedAt: '2026-09-26T00:00:00Z',
    unblockedAt: null,
    unblockedBy: null,
    unblockedByReference: null,
    version: '0',
  }
}
function signInAsSuperAdmin(id = '1001') {
  setIdentity({ user: user(id), modules: ['audit'], roles: [], routes: [], isSuperAdmin: true })
}
function deferred<T>() {
  let resolve!: (value: T) => void
  const promise = new Promise<T>((done) => {
    resolve = done
  })
  return { promise, resolve }
}
beforeEach(() => {
  vi.resetAllMocks()
  clearIdentity()
  const stored = new Map<string, string>()
  vi.stubGlobal('localStorage', {
    getItem: (key: string) => stored.get(key) ?? null,
    setItem: (key: string, value: string) => stored.set(key, value),
  })
  api.confirm.mockResolvedValue(undefined)
  api.getDepartments.mockResolvedValue([])
  api.getMenus.mockResolvedValue([])
  api.getRoles.mockResolvedValue({ items: [], total: 0, page: 1, size: 20 })
  api.setRoleMenus.mockResolvedValue(undefined)
  api.getUsers.mockResolvedValue({ items: [], total: 0, page: 1, size: 20 })
  api.getDepartmentOptions.mockResolvedValue([])
  api.getAuditPage.mockResolvedValue({ items: [], total: 0, page: 1, size: 20 })
  api.getLoginRecords.mockResolvedValue({ items: [], total: 0, page: 1, size: 20 })
  api.getIpBlocks.mockResolvedValue({ items: [], total: 0, page: 1, size: 20 })
})
afterEach(() => {
  apps.splice(0).forEach((app) => app.unmount())
  vi.unstubAllGlobals()
})

describe('management page interactions', () => {
  it('shows required role fields and rejects invalid codes before sending a create request', async () => {
    const root = await mount(RoleView)
    await click(button(root, '新建角色'))
    await click(button(root, '保存'))
    expect(field(root, '角色名称').props.error).toBe('请输入角色名称')
    expect(field(root, '角色编码').props.error).toBe('请输入角色编码')
    await input(findAll(field(root, '角色名称'), 'ElInput')[0]!, '运营')
    await input(findAll(field(root, '角色编码'), 'ElInput')[0]!, 'invalid-code')
    await click(button(root, '保存'))
    expect(field(root, '角色编码').props.error).toContain('大写字母')
    expect(api.createRole).not.toHaveBeenCalled()
    await input(findAll(field(root, '角色编码'), 'ElInput')[0]!, 'OPERATIONS')
    await click(button(root, '保存'))
    expect(api.createRole).toHaveBeenCalledExactlyOnceWith({
      code: 'OPERATIONS',
      name: '运营',
      description: null,
    })
  })

  it('shows an empty department name instead of silently ignoring save', async () => {
    const root = await mount(DepartmentView)
    await click(button(root, '新建部门'))
    await click(button(root, '保存'))
    expect(field(root, '部门名称').props.error).toBe('请输入部门名称')
    expect(api.createDepartment).not.toHaveBeenCalled()
  })

  it('locks a pending save dialog against dismissal or further edits', async () => {
    const pending = deferred<Role>()
    api.createRole.mockReturnValue(pending.promise)
    const root = await mount(RoleView)
    await click(button(root, '新建角色'))
    await input(findAll(field(root, '角色名称'), 'ElInput')[0]!, '运营')
    await input(findAll(field(root, '角色编码'), 'ElInput')[0]!, 'OPERATIONS')
    const saving = (button(root, '保存').props.onClick as () => Promise<void>)()
    await flush()
    const dialog = findAll(root, 'ElDialog')[0]!
    expect(dialog.props['close-on-click-modal']).toBe(false)
    expect(dialog.props['close-on-press-escape']).toBe(false)
    expect(dialog.props['show-close']).toBe(false)
    expect(findAll(dialog, 'ElForm')[0]!.props.disabled).toBe(true)
    expect(button(dialog, '取消').props.disabled).toBe(true)
    pending.resolve(role('1'))
    await saving
    await flush()
    expect(findAll(root, 'ElDialog')).toHaveLength(0)
  })

  it.each([1, 2])(
    'refreshes a filtered user list after disabling its last row on page %s',
    async (initialPage) => {
      const target = user('21')
      api.getUsers.mockResolvedValue({
        items: [target],
        total: 21,
        page: initialPage,
        size: 20,
      })
      api.changeUserStatus.mockResolvedValue({ ...target, status: 2, version: '1' })
      const root = await mount(UserView)
      await input(findAll(field(root, '状态'), 'ElSelect')[0]!, 1)
      const pagination = findAll(root, 'ElPagination')[0]!
      ;(pagination.props['onUpdate:currentPage'] as (value: number) => void)(initialPage)
      api.getUsers.mockClear().mockResolvedValue({ items: [], total: 0, page: 1, size: 20 })
      await (findAll(root, 'ElSwitch')[0]!.props.onChange as (value: boolean) => Promise<void>)(
        false,
      )
      await flush()
      expect(api.changeUserStatus).toHaveBeenCalledExactlyOnceWith(target, false)
      expect(api.getUsers).toHaveBeenCalledExactlyOnceWith({
        page: 1,
        size: 20,
        keyword: undefined,
        status: 1,
      })
      expect(findAll(root, 'ElTable')[0]!.props.data).toEqual([])
      expect(findAll(root, 'ElPagination')[0]!.props.total).toBe(0)
    },
  )

  it('submits a role status change once and returns from an emptied filtered last page', async () => {
    const target = role('21')
    api.getRoles.mockResolvedValue({ items: [target], total: 21, page: 2, size: 20 })
    const pending = deferred<Role>()
    api.changeRoleStatus.mockReturnValue(pending.promise)
    const root = await mount(RoleView)
    await input(findAll(field(root, '状态'), 'ElSelect')[0]!, 'ENABLED')
    ;(findAll(root, 'ElPagination')[0]!.props['onUpdate:currentPage'] as (value: number) => void)(2)
    api.getRoles.mockClear().mockResolvedValue({ items: [], total: 0, page: 1, size: 20 })
    const handler = button(root, '停用').props.onClick as () => Promise<void>
    const changing = handler()
    await handler()
    await flush()
    expect(button(root, '停用').props.loading).toBe(true)
    expect(api.changeRoleStatus).toHaveBeenCalledExactlyOnceWith(target, false)
    pending.resolve({ ...target, status: 'DISABLED', version: '1' })
    await changing
    expect(api.getRoles).toHaveBeenCalledExactlyOnceWith({
      page: 1,
      size: 20,
      keyword: undefined,
      status: 'ENABLED',
    })
  })

  it('does not load another user list after leaving during a status change', async () => {
    const target = user('2')
    api.getUsers.mockResolvedValue({ items: [target], total: 1, page: 1, size: 20 })
    const pending = deferred<UserDetail>()
    api.changeUserStatus.mockReturnValue(pending.promise)
    const root = await mount(UserView)
    api.getUsers.mockClear()
    const changing = (
      findAll(root, 'ElSwitch')[0]!.props.onChange as (value: boolean) => Promise<void>
    )(false)
    apps.pop()!.unmount()
    pending.resolve({ ...target, status: 2 })
    await changing
    expect(api.getUsers).not.toHaveBeenCalled()
  })

  it('returns to the previous page after deleting its last role', async () => {
    api.getRoles.mockResolvedValue({ items: [role('21')], total: 21, page: 2, size: 20 })
    const root = await mount(RoleView)
    const pagination = findAll(root, 'ElPagination')[0]!
    ;(pagination.props['onUpdate:currentPage'] as (value: number) => void)(2)
    await flush()
    api.getRoles.mockClear()
    await click(button(root, '删除'))
    expect(api.deleteRole).toHaveBeenCalledExactlyOnceWith(role('21'))
    expect(api.getRoles).toHaveBeenCalledExactlyOnceWith({
      page: 1,
      size: 20,
      keyword: undefined,
      status: undefined,
    })
  })

  it('uses the full department tree for delete protection and parent choices after searching', async () => {
    const child = department('2', '研发')
    const complete = department('1', '公司', [child])
    const filtered = { ...complete, children: [] }
    api.getDepartments.mockImplementation((keyword?: string) =>
      Promise.resolve(keyword ? [filtered] : [complete]),
    )
    const root = await mount(DepartmentView)
    await input(findAll(root, 'ElInput')[0]!, '公司')
    await (findAll(root, 'ElForm')[0]!.props.onSubmit as (event: unknown) => Promise<void>)({
      preventDefault() {},
    })
    await flush()
    expect(button(root, '删除').props.disabled).toBe(true)
    await click(button(root, '编辑', 1))
    const parent = field(root, '上级部门')
    expect(findAll(parent, 'ElOption').map((item) => item.props.value)).toEqual([])
  })

  it('queries menus once on form submission and retains hidden-child delete protection', async () => {
    const child: MenuNode = {
      id: '2',
      parentId: '1',
      name: '下级',
      type: 'DIRECTORY',
      icon: null,
      sortOrder: 0,
      visible: true,
      enabled: true,
      version: '0',
      page: null,
      children: [],
    }
    const complete: MenuNode = {
      ...child,
      id: '1',
      parentId: null,
      name: '监控面板',
      children: [child],
    }
    api.getMenus.mockImplementation((query: { name?: string }) =>
      Promise.resolve(query.name ? [{ ...complete, children: [] }] : [complete]),
    )
    const root = await mount(MenuView)
    await input(findAll(root, 'ElInput')[0]!, '监控')
    expect(findAll(root, 'ElInput')[0]!.props.onKeyup).toBeUndefined()
    api.getMenus.mockClear()
    await (findAll(root, 'ElForm')[0]!.props.onSubmit as (event: unknown) => Promise<void>)({
      preventDefault() {},
    })
    await flush()
    expect(api.getMenus).toHaveBeenCalledTimes(2)
    expect(api.getMenus.mock.calls.filter(([query]) => query.name === '监控')).toHaveLength(1)
    expect(button(root, '删除').props.disabled).toBe(true)
  })

  it('keeps the latest chosen role and its menus together when responses arrive out of order', async () => {
    const first = deferred<RoleMenus>()
    const second = deferred<RoleMenus>()
    api.getRoles.mockResolvedValue({ items: [role('1'), role('2')], total: 2, page: 1, size: 20 })
    api.getRoleMenus.mockImplementation((id: string) =>
      id === '1' ? first.promise : second.promise,
    )
    const root = await mount(RoleView)
    const a = (button(root, '配置页面', 0).props.onClick as () => Promise<void>)()
    const b = (button(root, '配置页面', 1).props.onClick as () => Promise<void>)()
    second.resolve({
      roleId: '2',
      version: '22',
      selectedPageIds: ['202'],
      tree: [{ id: '202', name: '页面2', type: 'PAGE', enabled: true, children: [] }],
    })
    await b
    first.resolve({
      roleId: '1',
      version: '11',
      selectedPageIds: ['101'],
      tree: [{ id: '101', name: '页面1', type: 'PAGE', enabled: true, children: [] }],
    })
    await a
    await flush()
    expect(findAll(root, 'ElDialog')[0]!.props.title).toBe('配置页面 · 角色2')
    await click(button(root, '保存'))
    expect(api.setRoleMenus).toHaveBeenCalledExactlyOnceWith('2', '22', ['202'])
  })

  const deleteCases = [
    {
      name: 'role',
      component: RoleView,
      seed: () =>
        api.getRoles.mockResolvedValue({ items: [role('1')], total: 1, page: 1, size: 20 }),
      remove: api.deleteRole,
    },
    {
      name: 'department',
      component: DepartmentView,
      seed: () => api.getDepartments.mockResolvedValue([department('1', '测试部门')]),
      remove: api.deleteDepartment,
    },
    {
      name: 'menu',
      component: MenuView,
      seed: () => api.getMenus.mockResolvedValue([menu('1')]),
      remove: api.deleteMenu,
    },
    {
      name: 'user',
      component: UserView,
      seed: () =>
        api.getUsers.mockResolvedValue({ items: [user('1')], total: 1, page: 1, size: 20 }),
      remove: api.deleteUser,
    },
  ]
  for (const reason of ['unmounted', 'identity-changed']) {
    it.each(deleteCases)(
      `does not delete $name after the page is ${reason} during confirmation`,
      async ({ component, seed, remove }) => {
        seed()
        const confirmation = deferred<unknown>()
        api.confirm.mockReturnValue(confirmation.promise)
        const root = await mount(component)
        const removing = (button(root, '删除').props.onClick as () => Promise<void>)()
        if (reason === 'unmounted') apps.pop()!.unmount()
        else clearIdentity()
        confirmation.resolve(undefined)
        await removing
        expect(remove).not.toHaveBeenCalled()
      },
    )
  }

  it('does not open a second delete confirmation while one is pending', async () => {
    api.getRoles.mockResolvedValue({ items: [role('1')], total: 1, page: 1, size: 20 })
    const confirmation = deferred<unknown>()
    api.confirm.mockReturnValue(confirmation.promise)
    const root = await mount(RoleView)
    const handler = button(root, '删除').props.onClick as () => Promise<void>
    const first = handler()
    await handler()
    expect(api.confirm).toHaveBeenCalledTimes(1)
    confirmation.resolve(undefined)
    await first
    expect(api.deleteRole).toHaveBeenCalledTimes(1)
  })

  it('does not reset passwords after the account changes during confirmation', async () => {
    const target = user('2')
    api.getUsers.mockResolvedValue({ items: [target], total: 1, page: 1, size: 20 })
    const confirmation = deferred<unknown>()
    api.confirm.mockReturnValue(confirmation.promise)
    const root = await mount(UserView)
    ;(findAll(root, 'ElTable')[0]!.props.onSelectionChange as (rows: UserDetail[]) => void)([
      target,
    ])
    await flush()
    const resetting = (button(root, '批量重置密码').props.onClick as () => Promise<void>)()
    clearIdentity()
    confirmation.resolve(undefined)
    await resetting
    expect(api.resetUserPassword).not.toHaveBeenCalled()
  })

  it('stops a batch reset before the next account after leaving the page', async () => {
    const targets = [user('2'), user('3')]
    api.getUsers.mockResolvedValue({ items: targets, total: 2, page: 1, size: 20 })
    const first = deferred<UserCredentialResult>()
    api.resetUserPassword.mockReturnValue(first.promise)
    const root = await mount(UserView)
    ;(findAll(root, 'ElTable')[0]!.props.onSelectionChange as (rows: UserDetail[]) => void)(targets)
    await flush()
    const resetting = (button(root, '批量重置密码').props.onClick as () => Promise<void>)()
    await flush()
    expect(api.resetUserPassword).toHaveBeenCalledTimes(1)
    apps.pop()!.unmount()
    first.resolve({ ...targets[0]!, temporaryPassword: 'TestCredential123!' })
    await resetting
    expect(api.resetUserPassword).toHaveBeenCalledTimes(1)
  })

  it('keeps the latest requested user form when detail requests finish out of order', async () => {
    api.getUsers.mockResolvedValue({ items: [user('1'), user('2')], total: 2, page: 1, size: 20 })
    const first = deferred<UserDetail>()
    const second = deferred<UserDetail>()
    api.getUser.mockImplementation((id: string) => (id === '1' ? first.promise : second.promise))
    const root = await mount(UserView)
    const a = (button(root, '编辑', 1).props.onClick as () => Promise<void>)()
    const b = (button(root, '编辑', 2).props.onClick as () => Promise<void>)()
    second.resolve(user('2'))
    await b
    first.resolve(user('1'))
    await a
    await flush()
    expect(findAll(field(root, '账号'), 'ElInput')[0]!.props.modelValue).toBe('user2')
  })

  it.each([
    { name: 'audit trace ID', component: AuditView, key: 'trace', label: '请求标识' },
    { name: 'login end time', component: LoginRecordTable, key: 'endedAt', label: '结束时间' },
  ])(
    'persists the optional $name column after reopening the page',
    async ({ component, key, label }) => {
      const root = await mount(component)
      const picker = findAll(root, 'ColumnPicker')[0]!
      ;(picker.props['onUpdate:modelValue'] as (columns: string[]) => void)([
        ...(picker.props.modelValue as string[]),
        key,
      ])
      await flush()
      apps.pop()!.unmount()
      const reopened = await mount(component)
      expect(findAll(reopened, 'ColumnPicker')[0]!.props.modelValue).toContain(key)
      expect(
        findAll(reopened, 'ElTableColumn').some((column) => column.props.label === label),
      ).toBe(true)
    },
  )
})

describe('audit and IP block recovery', () => {
  it('retries failed audit details in the same drawer and shows readable reasons', async () => {
    api.getAuditPage.mockResolvedValue({ items: [audit()], total: 1, page: 1, size: 20 })
    api.getAuditDetail
      .mockRejectedValueOnce(new Error('temporarily unavailable'))
      .mockResolvedValueOnce(auditDetail())
    const root = await mount(AuditView)
    expect(findAll(root, 'ElTableColumn').some((column) => column.props.type === 'selection')).toBe(
      false,
    )
    await click(button(root, '详情'))
    expect(findAll(root, 'ElDrawer')).toHaveLength(1)
    await click(button(root, '重新加载详情'))
    expect(api.getAuditDetail.mock.calls).toEqual([['1'], ['1']])
    expect(api.getAuditPage).toHaveBeenCalledTimes(1)
    expect(content(findAll(root, 'ElDrawer')[0]!)).toContain('连续登录失败，账号暂时锁定')
    expect(findAll(root, 'ElButton').some((item) => content(item) === '重新加载详情')).toBe(false)
  })

  it.each([
    { name: 'audit', component: AuditView, read: api.getAuditPage, item: () => audit() },
    { name: 'IP block', component: IpBlockPanel, read: api.getIpBlocks, item: () => ipBlock() },
  ])(
    'ignores a pending $name list after the account changes',
    async ({ component, read, item }) => {
      signInAsSuperAdmin()
      const pending = deferred<unknown>()
      read.mockReturnValue(pending.promise)
      const root = await mount(component)
      clearIdentity()
      signInAsSuperAdmin('1002')
      pending.resolve({ items: [item()], total: 1, page: 1, size: 20 })
      await flush()
      expect(findAll(root, 'ElTable')[0]!.props.data).toEqual([])
    },
  )

  it('ignores pending audit detail after the account changes', async () => {
    signInAsSuperAdmin()
    api.getAuditPage.mockResolvedValue({ items: [audit()], total: 1, page: 1, size: 20 })
    const pending = deferred<AuditDetail>()
    api.getAuditDetail.mockReturnValue(pending.promise)
    const root = await mount(AuditView)
    const opening = (button(root, '详情').props.onClick as () => Promise<void>)()
    clearIdentity()
    signInAsSuperAdmin('1002')
    pending.resolve(auditDetail())
    await opening
    await flush()
    expect(findAll(root, 'ElDescriptions')).toHaveLength(0)
  })

  it('shows release user names with snapshot sources and explicit legacy fallbacks', async () => {
    signInAsSuperAdmin()
    const released = { ...ipBlock(), status: 'RELEASED' as const, unblockedBy: '1001' }
    const snapshot = {
      id: '1001',
      type: 'USER',
      name: '原运维',
      code: 'operator',
      source: 'SNAPSHOT' as const,
    }
    api.getIpBlocks.mockResolvedValue({
      items: [
        { ...released, unblockedByReference: snapshot },
        {
          ...released,
          id: '2',
          unblockedByReference: { ...snapshot, name: '现运维', source: 'CURRENT' },
        },
        {
          ...released,
          id: '3',
          unblockedByReference: { ...snapshot, name: null, code: null, source: 'MISSING' },
        },
        { ...released, id: '4', unblockedByReference: null },
      ],
      total: 4,
      page: 1,
      size: 20,
    })
    const root = await mount(IpBlockPanel)
    const column = findAll(root, 'ElTableColumn').find((item) => item.props.label === '解封人')!
    expect(content(column)).toContain('原运维（operator）')
    expect(content(column)).toContain('操作时名称')
    expect(content(column)).toContain('现运维（operator）')
    expect(content(column)).toContain('当前名称')
    expect(content(column)).toContain('已删除或未知对象（ID 1001）')
    expect(content(column)).toContain('账号 ID 1001（无名称记录）')
  })

  it.each(['unmounted', 'identity-changed'])(
    'does not unblock after the page is %s during confirmation',
    async (reason) => {
      signInAsSuperAdmin()
      api.getIpBlocks.mockResolvedValue({ items: [ipBlock()], total: 1, page: 1, size: 20 })
      const confirmation = deferred<unknown>()
      api.confirm.mockReturnValue(confirmation.promise)
      const root = await mount(IpBlockPanel)
      const releasing = (button(root, '解除封禁').props.onClick as () => Promise<void>)()
      if (reason === 'unmounted') apps.pop()!.unmount()
      else {
        clearIdentity()
        signInAsSuperAdmin('1002')
      }
      confirmation.resolve(undefined)
      await releasing
      expect(api.unblockIp).not.toHaveBeenCalled()
    },
  )

  it('ignores an unblock completion after the account changes', async () => {
    signInAsSuperAdmin()
    api.getIpBlocks.mockResolvedValue({ items: [ipBlock()], total: 1, page: 1, size: 20 })
    const pending = deferred<IpBlock>()
    api.unblockIp.mockReturnValue(pending.promise)
    const root = await mount(IpBlockPanel)
    api.getIpBlocks.mockClear()
    const releasing = (button(root, '解除封禁').props.onClick as () => Promise<void>)()
    await flush()
    expect(api.unblockIp).toHaveBeenCalledTimes(1)
    clearIdentity()
    signInAsSuperAdmin('1002')
    pending.resolve({ ...ipBlock(), status: 'RELEASED' })
    await releasing
    expect(api.success).not.toHaveBeenCalled()
    expect(api.getIpBlocks).not.toHaveBeenCalled()
  })

  it.each(['unchanged', 'page', 'filter'])(
    'only returns from an emptied blocked page if the displayed list is unchanged (%s)',
    async (navigation) => {
      signInAsSuperAdmin()
      api.getIpBlocks.mockResolvedValue({ items: [ipBlock()], total: 21, page: 2, size: 20 })
      const pending = deferred<IpBlock>()
      api.unblockIp.mockReturnValue(pending.promise)
      const root = await mount(IpBlockPanel)
      const pagination = findAll(root, 'ElPagination')[0]!
      ;(pagination.props['onUpdate:currentPage'] as (page: number) => void)(2)
      const releasing = (button(root, '解除封禁').props.onClick as () => Promise<void>)()
      await flush()
      const expectedPage = navigation === 'unchanged' ? 1 : navigation === 'page' ? 3 : 2
      const sourceIp = navigation === 'filter' ? '192.0.2.2' : undefined
      if (navigation !== 'unchanged') {
        if (sourceIp) await input(findAll(field(root, '来源IP'), 'ElInput')[0]!, sourceIp)
        api.getIpBlocks.mockResolvedValue({
          items: [ipBlock('2')],
          total: 41,
          page: expectedPage,
          size: 20,
        })
        ;(pagination.props['onUpdate:currentPage'] as (page: number) => void)(expectedPage)
        await (pagination.props.onChange as () => Promise<void>)()
      }
      api.getIpBlocks
        .mockClear()
        .mockResolvedValue({ items: [], total: 0, page: expectedPage, size: 20 })
      pending.resolve({ ...ipBlock(), status: 'RELEASED' })
      await releasing
      expect(api.getIpBlocks).toHaveBeenCalledExactlyOnceWith(
        { page: expectedPage, size: 20, sourceIp, status: 'BLOCKED' },
        expect.any(AbortSignal),
      )
    },
  )
})
