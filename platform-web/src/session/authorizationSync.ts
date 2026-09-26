import type { AuthUser } from '../api/auth/types'
import { buildMenuRoutes, fixedPaths } from '../router/dynamic/routes'
import { refreshIdentity } from './session'
import { sessionState } from './state'

/** A forbidden object action can retain its PAGE grant; only fresh server identity decides. */
export async function synchronizeAuthorization(epoch: number): Promise<void> {
  if (!sessionState.me || sessionState.epoch !== epoch) return
  let synchronizedIdentity: AuthUser | undefined
  try {
    synchronizedIdentity = await refreshIdentity()
  } catch (error) {
    if (sessionState.epoch !== epoch) return
    throw error
  }
  if (!synchronizedIdentity) return

  const { router } = await import('../router')
  const identity = sessionState.me
  if (!identity || sessionState.epoch !== epoch || identity !== synchronizedIdentity) return
  const route = router.currentRoute.value
  if (route.path === '/login' || route.path === '/unavailable') return
  const mustChangePassword = identity.user.mustChangePassword && route.path !== '/change-password'
  const allowed =
    fixedPaths.has(route.path) ||
    buildMenuRoutes(identity.routes, identity.modules).records.some(
      (record) => record.path === route.path,
    )

  // Keeping an authorized view mounted preserves its original error and never retries its GETs.
  if (mustChangePassword || !allowed) {
    await router.replace({ path: route.path, query: route.query, hash: route.hash, force: true })
  }
}
