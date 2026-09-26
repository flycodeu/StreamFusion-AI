import { getMe } from '../api/auth/api'
import { buildMenuRoutes, fixedPaths } from '../router/dynamic/routes'
import { sessionState, setIdentity } from './state'

/** A forbidden object action can retain its PAGE grant; only fresh server identity decides. */
export async function synchronizeAuthorization(epoch: number): Promise<void> {
  const initialIdentity = sessionState.me
  const current = () => sessionState.epoch === epoch && sessionState.me === initialIdentity
  if (!initialIdentity || !current()) return

  const me = await getMe()
  if (!current()) return
  setIdentity(me)
  const synchronizedIdentity = sessionState.me

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
