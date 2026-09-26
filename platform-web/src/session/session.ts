import { ApiRequestError } from '../lib/http/error'
import * as auth from '../api/auth/api'
import { clearIdentity, sessionState, setIdentity } from './state'
import { forgetRememberedLogin } from './rememberedLogin'

let restoring: Promise<void> | null = null

export async function refreshCsrf(): Promise<void> {
  sessionState.csrf = await auth.getCsrf()
}

export async function refreshIdentity(): Promise<void> {
  const me = await auth.getMe()
  setIdentity(me)
}

export async function restoreSession(): Promise<void> {
  if (restoring) return restoring
  restoring = (async () => {
    try {
      await refreshCsrf()
      await refreshIdentity()
    } catch (error) {
      clearIdentity()
      if (!(error instanceof ApiRequestError && error.status === 401)) throw error
    } finally {
      sessionState.ready = true
    }
  })()
  try {
    await restoring
  } finally {
    restoring = null
  }
}

export async function signIn(username: string, password: string): Promise<void> {
  if (!sessionState.csrf) await refreshCsrf()
  await auth.login(username, password)
  clearIdentity()
  await refreshCsrf()
  await refreshIdentity()
  sessionState.ready = true
}

export async function signOut(): Promise<void> {
  await auth.logout()
  clearIdentity()
}

export async function updatePassword(currentPassword: string, newPassword: string): Promise<void> {
  await auth.changePassword(currentPassword, newPassword)
  clearIdentity()
  await forgetRememberedLogin().catch(() => undefined)
}
