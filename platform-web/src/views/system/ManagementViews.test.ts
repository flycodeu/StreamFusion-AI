// @vitest-environment vue-renderer
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createRenderer, nextTick } from 'vue'
import type { App, Component } from 'vue'
import type { Department } from '../../api/departments/types'
import type { MenuNode } from '../../api/menus/types'
import type { Role, RoleMenus } from '../../api/roles/types'
import DepartmentView from './Department.vue'
import MenuView from './Menu.vue'
import RoleView from './Role.vue'

const api = vi.hoisted(() => ({
  getDepartments: vi.fn(),
  createDepartment: vi.fn(),
  updateDepartment: vi.fn(),
  deleteDepartment: vi.fn(),
  getMenus: vi.fn(),
  getRoles: vi.fn(),
  createRole: vi.fn(),
  deleteRole: vi.fn(),
  getRoleMenus: vi.fn(),
  setRoleMenus: vi.fn(),
}))
vi.mock('../../api/departments/api', () => api)
vi.mock('../../api/menus/api', () => api)
vi.mock('../../api/roles/api', () => api)
vi.mock('../../session/session', () => ({ refreshIdentity: vi.fn() }))
vi.mock('vue-router', () => ({
  useRoute: () => ({ path: '/system/Menu', query: {}, hash: '' }),
  useRouter: () => ({ replace: vi.fn() }),
}))
vi.mock('../../components/table/ColumnPicker.vue', () => ({ default: { render: () => null } }))
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
    ElMessageBox: { confirm: vi.fn().mockResolvedValue(undefined) },
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
async function input(target: HostNode, value: string) {
  ;(target.props['onUpdate:modelValue'] as (value: string) => void)(value)
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
function deferred<T>() {
  let resolve!: (value: T) => void
  const promise = new Promise<T>((done) => {
    resolve = done
  })
  return { promise, resolve }
}
beforeEach(() => {
  vi.resetAllMocks()
  api.getDepartments.mockResolvedValue([])
  api.getMenus.mockResolvedValue([])
  api.getRoles.mockResolvedValue({ items: [], total: 0, page: 1, size: 20 })
  api.setRoleMenus.mockResolvedValue(undefined)
})
afterEach(() => {
  apps.splice(0).forEach((app) => app.unmount())
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
})
