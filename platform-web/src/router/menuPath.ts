import type { MenuRoute } from '../api/auth/types'

export function findMenuPagePath(
  routes: readonly MenuRoute[],
  componentKey: string,
  moduleKey?: string,
): string | null {
  for (const route of routes) {
    if (
      route.type === 'PAGE' &&
      route.componentKey === componentKey &&
      (moduleKey === undefined || route.moduleKey === moduleKey)
    )
      return route.path
    const child = findMenuPagePath(route.children, componentKey, moduleKey)
    if (child) return child
  }
  return null
}
