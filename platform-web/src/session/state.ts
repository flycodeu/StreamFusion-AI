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
}

export function setIdentity(me: AuthUser): void {
  sessionState.me = me
}
