import { ApiRequestError } from '../lib/http/error'
import * as auth from '../api/auth/api'
import type { AuthUser } from '../api/auth/types'
import type { LoginCaptchaAnswer } from '../api/auth/captcha'
import { clearIdentity, sessionState, setIdentity } from './state'
import { forgetRememberedLogin } from './rememberedLogin'
import { notifyOtherTabs } from './crossTab'

let restoring: Promise<void> | null = null
let identitySequence = 0
let sharedSessionReloading = false

/** A new shared cookie needs the standard startup flow, including CSRF and route rebuilding. */
export function reloadSharedSession(): void {
  if (sharedSessionReloading) return
  sharedSessionReloading = true
  clearIdentity()
  if (globalThis.location) globalThis.location.replace('/login')
}

function requireCurrentEpoch(epoch: number): void {
  if (sessionState.epoch !== epoch) throw new ApiRequestError('REQUEST_CANCELLED', '请求已取消')
}

export async function refreshCsrf(signal?: AbortSignal): Promise<void> {
  const epoch = sessionState.epoch
  const token = await auth.getCsrf(signal)
  requireCurrentEpoch(epoch)
  sessionState.csrf = token
}

/** All identity reads share one ordering; return only the snapshot this request applied. */
export async function refreshIdentity(): Promise<AuthUser | undefined> {
  const sequence = ++identitySequence
  const epoch = sessionState.epoch
  const previous = sessionState.me
  const me = await auth.getMe()
  requireCurrentEpoch(epoch)
  if (sequence !== identitySequence || sessionState.me !== previous) return
  setIdentity(me)
  return sessionState.me ?? undefined
}

export async function restoreSession(): Promise<void> {
  if (restoring) return restoring
  const epoch = sessionState.epoch
  restoring = (async () => {
    try {
      await refreshCsrf()
      await refreshIdentity()
    } catch (error) {
      if (sessionState.epoch === epoch) clearIdentity()
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

export async function signIn(
  username: string,
  password: string,
  captcha: LoginCaptchaAnswer,
): Promise<void> {
  const epoch = sessionState.epoch
  if (!sessionState.csrf) await refreshCsrf()
  requireCurrentEpoch(epoch)
  await auth.login(username, password, captcha)
  requireCurrentEpoch(epoch)
  clearIdentity()
  notifyOtherTabs()
  try {
    await refreshCsrf()
    await refreshIdentity()
  } catch (cause) {
    // The cookie is already authenticated. A temporary read failure must not restart anonymous login.
    if (
      cause instanceof ApiRequestError &&
      (cause.code === 'REQUEST_CANCELLED' || cause.status === 401 || cause.code === 'IP_BLOCKED')
    )
      throw cause
    throw new ApiRequestError(
      'LOGIN_RESTORE_FAILED',
      '登录成功，但未能加载账户信息，请刷新页面继续',
      cause instanceof ApiRequestError ? cause : {},
    )
  }
  sessionState.ready = true
}

export async function signOut(): Promise<void> {
  const epoch = sessionState.epoch
  await auth.logout()
  requireCurrentEpoch(epoch)
  clearIdentity()
  notifyOtherTabs()
}

export async function updatePassword(currentPassword: string, newPassword: string): Promise<void> {
  const epoch = sessionState.epoch
  await auth.changePassword(currentPassword, newPassword)
  requireCurrentEpoch(epoch)
  clearIdentity()
  notifyOtherTabs()
  await forgetRememberedLogin().catch(() => undefined)
}
