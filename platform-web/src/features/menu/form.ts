import type { MenuWrite } from '../../api/menus/api'
import type { MenuNode } from '../../api/menus/types'
import { treeIds } from '../../utils/tree'

export type MenuForm = Omit<MenuWrite, 'componentKey' | 'moduleKey'>

export function newMenuForm(parentId: string | null = null): MenuForm {
  return {
    parentId,
    name: '',
    type: 'DIRECTORY',
    icon: null,
    sortOrder: 0,
    visible: true,
    enabled: true,
    routeName: null,
    path: null,
  }
}

export function editMenuForm(node: MenuNode): MenuForm {
  return {
    parentId: node.parentId,
    name: node.name,
    type: node.type,
    icon: node.icon,
    sortOrder: node.sortOrder,
    visible: node.visible,
    enabled: node.enabled,
    routeName: node.page?.routeName ?? null,
    path: node.page?.path ?? null,
  }
}

export function menuParentOptions(nodes: MenuNode[], current: MenuNode | null): MenuNode[] {
  const excluded = current ? treeIds(current) : new Set<string>()
  const directories = (items: MenuNode[]): MenuNode[] =>
    items
      .filter((node) => node.type === 'DIRECTORY' && !excluded.has(node.id))
      .map((node) => ({ ...node, children: directories(node.children) }))
  return directories(nodes)
}

export function menuWrite(form: MenuForm, original: MenuNode | null = null): MenuWrite {
  const page = form.type === 'PAGE'
  const path = page ? form.path?.trim() || null : null
  const previous = original?.page
  const componentKey =
    !page || !previous ? null : path === previous.path ? previous.componentKey : path
  return {
    parentId: form.parentId || null,
    name: form.name.trim(),
    type: form.type,
    icon: form.icon?.trim() || null,
    sortOrder: Number(form.sortOrder),
    visible: form.visible,
    enabled: form.enabled,
    routeName: page ? form.routeName?.trim() || null : null,
    path,
    componentKey,
    // Existing API module ownership is immutable, even if the route key is renamed.
    moduleKey: page ? (previous?.moduleKey ?? null) : null,
  }
}
