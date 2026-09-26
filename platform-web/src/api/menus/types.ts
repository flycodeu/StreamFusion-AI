import { boolean, id, integer, list, object, optionalString, string } from '../parse'

export interface MenuNode {
  id: string
  parentId: string | null
  name: string
  type: 'DIRECTORY' | 'PAGE'
  icon: string | null
  sortOrder: number
  visible: boolean
  enabled: boolean
  version: string
  page: { routeName: string; path: string; componentKey: string; moduleKey: string } | null
  children: MenuNode[]
}

export function parseMenuNode(value: unknown, depth = 0): MenuNode {
  if (depth > 5) throw new Error('depth')
  const row = object(value)
  const type = string(row.type)
  if (type !== 'DIRECTORY' && type !== 'PAGE') throw new Error('type')
  const page = row.page == null ? null : object(row.page)
  return {
    id: id(row.id),
    parentId: row.parentId == null ? null : id(row.parentId),
    name: string(row.name),
    type,
    icon: optionalString(row.icon),
    sortOrder: integer(row.sortOrder),
    visible: boolean(row.visible),
    enabled: boolean(row.enabled),
    version: id(row.version),
    page:
      page == null
        ? null
        : {
            routeName: string(page.routeName),
            path: string(page.path),
            componentKey: string(page.componentKey),
            moduleKey: string(page.moduleKey),
          },
    children:
      row.children == null ? [] : list(row.children, (child) => parseMenuNode(child, depth + 1)),
  }
}
