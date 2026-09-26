import type { RouteRecordRaw } from 'vue-router'
import type { MenuRoute } from '../../api/auth/types'
import { normalizeViewPath, resolvePage } from './views'
import { fixedPaths } from '../fixed'

export { fixedPaths } from '../fixed'
const reservedRoots = new Set([
  ...fixedPaths,
  '/api',
  '/auth',
  '/actuator',
  '/assets',
  '/src',
  '/public',
  '/node_modules',
])

export function validMenuPath(path: string | null): path is string {
  if (!path || path.length > 200 || !/^\/[A-Za-z0-9_-]+(?:\/[A-Za-z0-9_-]+)*$/.test(path))
    return false
  const root = `/${path.split('/')[1]}`.toLowerCase()
  return !reservedRoots.has(root)
}

export interface MenuNavigation {
  id: string
  title: string
  icon: string | null
  path?: string
  children: MenuNavigation[]
}

export function buildMenuRoutes(
  menus: MenuRoute[],
  modules: string[],
  resolve = resolvePage,
): { records: RouteRecordRaw[]; navigation: MenuNavigation[] } {
  const records: RouteRecordRaw[] = []
  const paths = new Set<string>()
  const keys = new Set<string>()
  const visit = (nodes: MenuRoute[], ancestors: string[] = []): MenuNavigation[] =>
    nodes.flatMap((node) => {
      if (node.type === 'DIRECTORY') {
        const children = visit(node.children, [...ancestors, node.name])
        return node.visible && children.length
          ? [{ id: node.id, title: node.name, icon: node.icon, children }]
          : []
      }
      if (
        !validMenuPath(node.path) ||
        !node.routeName ||
        !/^[A-Za-z][A-Za-z0-9_:-]{0,63}$/.test(node.routeName) ||
        !node.moduleKey ||
        !modules.includes(node.moduleKey) ||
        paths.has(node.path.toLowerCase()) ||
        keys.has(node.routeName.toLowerCase())
      )
        return []
      paths.add(node.path.toLowerCase())
      keys.add(node.routeName.toLowerCase())
      const componentPath = normalizeViewPath(node.componentKey || node.path)
      const component = resolve(componentPath)
      records.push({
        path: node.path,
        name: `page:${node.routeName}`,
        sensitive: true,
        component: component ?? (() => import('../../pages/error/RouteUnavailableView.vue')),
        meta: {
          title: node.name,
          menuId: node.id,
          moduleKey: node.moduleKey,
          breadcrumbs: [...ancestors, node.name],
          componentPath,
          configurationError: !component,
        },
      })
      return node.visible
        ? [{ id: node.id, title: node.name, icon: node.icon, path: node.path, children: [] }]
        : []
    })
  return { records, navigation: visit(menus) }
}
