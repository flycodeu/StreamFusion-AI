import { describe, expect, it } from 'vitest'
import type { MenuNode } from '../../api/menus/types'
import { editMenuForm, menuParentOptions, menuWrite, newMenuForm } from './form'

function page(overrides: Partial<NonNullable<MenuNode['page']>> = {}): MenuNode {
  return {
    id: '11',
    parentId: '1',
    name: '用户',
    type: 'PAGE',
    icon: 'user',
    sortOrder: 1,
    visible: true,
    enabled: true,
    version: '2',
    page: {
      routeName: 'userPage',
      path: '/system/User',
      componentKey: '/system/User',
      moduleKey: 'user',
      ...overrides,
    },
    children: [],
  }
}

function directory(id: string, children: MenuNode[] = []): MenuNode {
  return { ...page(), id, name: `目录${id}`, type: 'DIRECTORY', page: null, children }
}

describe('simplified menu form compatibility', () => {
  it('creates a page using server defaults for the selected file path and unique key', () => {
    const form = {
      ...newMenuForm('1'),
      type: 'PAGE' as const,
      name: ' 相机管理 ',
      path: ' /camera/manage ',
      routeName: ' camera ',
    }
    expect(menuWrite(form)).toMatchObject({
      parentId: '1',
      name: '相机管理',
      path: '/camera/manage',
      routeName: 'camera',
      componentKey: null,
      moduleKey: null,
    })
  })

  it('preserves a distinct component and immutable API module when editing display fields', () => {
    const original = page({ path: '/accounts/manage', componentKey: 'SYSTEM_USERS' })
    const form = { ...editMenuForm(original), name: '人员', icon: 'avatar' }
    expect(menuWrite(form, original)).toMatchObject({
      name: '人员',
      icon: 'avatar',
      path: '/accounts/manage',
      componentKey: 'SYSTEM_USERS',
      moduleKey: 'user',
    })
  })

  it('keeps API module ownership when the route key changes', () => {
    const original = page()
    const form = { ...editMenuForm(original), routeName: 'renamedUserRoute' }
    expect(menuWrite(form, original)).toMatchObject({
      routeName: 'renamedUserRoute',
      moduleKey: 'user',
      componentKey: '/system/User',
    })
  })

  it.each(['/system/User', 'SYSTEM_USERS', '/another/User'])(
    'updates the component to a deliberately changed path even for old mapping %s',
    (componentKey) => {
      const original = page({ path: '/accounts/manage', componentKey })
      const form = { ...editMenuForm(original), path: '/camera/manage' }
      expect(menuWrite(form, original)).toMatchObject({
        path: '/camera/manage',
        componentKey: '/camera/manage',
        moduleKey: 'user',
      })
    },
  )

  it('does not rewrite old component aliases when the path is unchanged', () => {
    const original = page({ componentKey: 'SYSTEM_USERS' })
    expect(menuWrite(editMenuForm(original), original).componentKey).toBe('SYSTEM_USERS')
  })

  it('removes page fields for directories', () => {
    const form = { ...editMenuForm(page()), type: 'DIRECTORY' as const }
    expect(menuWrite(form)).toMatchObject({
      routeName: null,
      path: null,
      componentKey: null,
      moduleKey: null,
    })
  })

  it('keeps nested directory choices, excludes pages, and blocks a subtree from parenting itself', () => {
    const nested = directory('2', [directory('3'), page()])
    const root = directory('1', [nested, directory('4')])
    const available = menuParentOptions([root], null)
    expect(available[0].children[0].children.map((node) => node.id)).toEqual(['3'])
    expect(menuParentOptions([root], nested)[0].children.map((node) => node.id)).toEqual(['4'])
    expect(root.children[0].children).toHaveLength(2)
  })
})
