import { createApiClient } from '../lib/http/client'
import { clearIdentity, sessionState } from '../session/state'

export const request = createApiClient({
  getCsrf: () => sessionState.csrf,
  getIdentityEpoch: () => sessionState.epoch,
  onForbidden: async (_error, epoch) => {
    const { synchronizeAuthorization } = await import('../session/authorizationSync')
    await synchronizeAuthorization(epoch)
  },
  onAuthFailure: (error, epoch) => {
    if (error.code === 'IP_BLOCKED') {
      clearIdentity()
      if (globalThis.location && globalThis.location.pathname !== '/login')
        globalThis.location.assign('/login?reason=ip-blocked')
    } else if (error.status === 401 && error.code !== 'LOGIN_FAILED') {
      clearIdentity()
      if (globalThis.location && globalThis.location.pathname !== '/login')
        globalThis.location.assign('/login')
    } else if (error.code === 'PASSWORD_CHANGE_REQUIRED') {
      if (globalThis.location) globalThis.location.assign('/change-password')
    } else if (error.code === 'CSRF_INVALID') {
      sessionState.csrf = null
      void import('./auth/api')
        .then(async ({ getCsrf }) => {
          const token = await getCsrf()
          if (sessionState.epoch === epoch) sessionState.csrf = token
        })
        .catch(() => {
          /* Current action remains failed; user may refresh. */
        })
    }
  },
})
