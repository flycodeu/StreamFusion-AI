import { reactive } from 'vue'
import type { CsrfToken } from '../lib/http/types'
import type { AuthUser } from '../api/auth/types'

export const sessionState = reactive({
  csrf: null as CsrfToken | null,
  me: null as AuthUser | null,
  epoch: 0,
  ready: false,
})

export function clearIdentity(): void {
  sessionState.epoch++
  sessionState.csrf = null
  sessionState.me = null
  try {
    sessionStorage.removeItem('sf-auth-routes-v1')
  } catch {
    /* Storage is optional. */
  }
}

export function setIdentity(me: AuthUser): void {
  sessionState.me = me
  try {
    sessionStorage.setItem(
      'sf-auth-routes-v1',
      JSON.stringify({
        userId: me.user.id,
        routes: me.routes,
        modules: me.modules,
        at: Date.now(),
      }),
    )
  } catch {
    /* Server-validated in-memory identity remains usable without storage. */
  }
}
