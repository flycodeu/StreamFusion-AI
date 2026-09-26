import { createRouter, createWebHistory } from 'vue-router'
import type { MenuRoute } from '../api/auth/types'
import { refreshIdentity, restoreSession } from '../session/session'
import { sessionState } from '../session/state'
import { buildMenuRoutes, fixedPaths } from './dynamic/routes'
import { fixedRoutes } from './fixed'

export const router = createRouter({
  history: createWebHistory(import.meta.env.BASE_URL),
  routes: fixedRoutes,
})

const dynamicNames = new Set<string>()
let routeSignature = ''

function syncRoutes(routes: MenuRoute[], modules: string[]): void {
  const signature = JSON.stringify({ routes, modules })
  if (signature === routeSignature) return
  for (const name of dynamicNames) router.removeRoute(name)
  dynamicNames.clear()
  for (const record of buildMenuRoutes(routes, modules).records) {
    router.addRoute(record)
    dynamicNames.add(String(record.name))
  }
  routeSignature = signature
}

router.beforeEach(async (to) => {
  let identityRefreshed = false
  if (!sessionState.ready) {
    try {
      await restoreSession()
      identityRefreshed = true
    } catch {
      if (to.path !== '/unavailable' && to.path !== '/login') return '/unavailable'
    }
  }
  if (to.path === '/login') {
    syncRoutes([], [])
    if (sessionState.me)
      return sessionState.me.user.mustChangePassword ? '/change-password' : '/home'
    return true
  }
  if (to.path === '/unavailable') return true
  if (!sessionState.me) {
    syncRoutes([], [])
    return { path: '/login', query: { next: to.fullPath } }
  }
  // Fixed pages skip PAGE authorization, but every protected navigation validates its session.
  if (!identityRefreshed) {
    try {
      await refreshIdentity()
    } catch {
      return sessionState.me ? '/unavailable' : { path: '/login', query: { next: to.fullPath } }
    }
  }
  const me = sessionState.me
  if (!me) {
    syncRoutes([], [])
    return { path: '/login', query: { next: to.fullPath } }
  }
  if (me.user.mustChangePassword && to.path !== '/change-password') return '/change-password'
  if (to.path === '/change-password') return true
  syncRoutes(me.routes, me.modules)
  const resolved = router.resolve(to.fullPath)
  if (!resolved.matched.length) return '/forbidden'
  if (to.matched.at(-1) !== resolved.matched.at(-1)) return to.fullPath
  if (!fixedPaths.has(to.path) && !dynamicNames.has(String(resolved.name))) return '/forbidden'
  return true
})
