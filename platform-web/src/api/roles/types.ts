import { boolean, id, list, object, optionalString, string } from '../parse'

export interface Role {
  id: string
  code: string
  name: string
  description: string | null
  status: 'ENABLED' | 'DISABLED'
  version: string
}

export interface RoleOption {
  id: string
  code: string
  name: string
}

export interface RoleMenuNode {
  id: string
  name: string
  type: 'PAGE' | 'DIRECTORY'
  enabled: boolean
  children: RoleMenuNode[]
}

export interface RoleMenus {
  roleId: string
  version: string
  selectedPageIds: string[]
  tree: RoleMenuNode[]
}

export function parseRoleOption(value: unknown): RoleOption {
  const row = object(value)
  return { id: id(row.id), code: string(row.code), name: string(row.name) }
}

export function parseRole(value: unknown): Role {
  const row = object(value)
  const status = string(row.status)
  if (status !== 'ENABLED' && status !== 'DISABLED') throw new Error('status')
  return {
    ...parseRoleOption(row),
    description: optionalString(row.description),
    status,
    version: id(row.version),
  }
}

function parseNode(value: unknown, depth = 0): RoleMenuNode {
  if (depth > 5) throw new Error('depth')
  const row = object(value)
  const type = string(row.type)
  if (type !== 'PAGE' && type !== 'DIRECTORY') throw new Error('type')
  return {
    id: id(row.id),
    name: string(row.name),
    type,
    enabled: boolean(row.enabled),
    children:
      row.children == null ? [] : list(row.children, (child) => parseNode(child, depth + 1)),
  }
}

export function parseRoleMenus(value: unknown): RoleMenus {
  const row = object(value)
  return {
    roleId: id(row.roleId),
    version: id(row.version),
    selectedPageIds: list(row.selectedPageIds, id),
    tree: list(row.tree, parseNode),
  }
}
