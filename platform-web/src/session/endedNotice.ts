import { shallowRef } from 'vue'
import { isUtcTimestamp } from '../lib/http/error'

const storageKey = 'sf-session-ended-v1'
const maxAge = 5 * 60_000

export interface SessionEndedNotice {
  reason: 'REPLACED' | 'FORCED_LOGOUT'
  occurredAt: string
  loginAt?: string
  sourceIp?: string
  region?: string
  browser?: string
  os?: string
}

export const endedNotice = shallowRef<SessionEndedNotice | null>(null)

function safeText(value: unknown): string | undefined {
  return typeof value === 'string'
    ? Array.from(value)
        .filter((char) => char.charCodeAt(0) >= 32 && char.charCodeAt(0) !== 127)
        .join('')
        .trim()
        .slice(0, 120) || undefined
    : undefined
}

/** Only the display fields are retained; never persist the full error or request. */
export function parseEndedNotice(code: string, value: unknown): SessionEndedNotice | null {
  const reason =
    code === 'SESSION_REPLACED'
      ? 'REPLACED'
      : code === 'SESSION_FORCED_LOGOUT'
        ? 'FORCED_LOGOUT'
        : null
  if (!reason) return null
  const row = value && typeof value === 'object' ? (value as Record<string, unknown>) : {}
  return {
    reason,
    occurredAt: isUtcTimestamp(row.occurredAt) ? row.occurredAt : new Date().toISOString(),
    ...(reason === 'REPLACED'
      ? {
          loginAt: isUtcTimestamp(row.loginAt) ? row.loginAt : undefined,
          sourceIp: safeText(row.sourceIp),
          region: safeText(row.region),
          browser: safeText(row.browser),
          os: safeText(row.os),
        }
      : {}),
  }
}

export function rememberSessionEnded(code: string, data: unknown): void {
  const notice = parseEndedNotice(code, data)
  if (!notice) return
  endedNotice.value = notice
  try {
    sessionStorage.setItem(storageKey, JSON.stringify({ at: Date.now(), notice }))
  } catch {
    /* The in-memory dialog still works when storage is unavailable. */
  }
}

export function restoreSessionEnded(): void {
  try {
    const saved = sessionStorage.getItem(storageKey)
    sessionStorage.removeItem(storageKey)
    if (!saved || saved.length > 3000) return
    const entry = JSON.parse(saved)
    if (typeof entry.at !== 'number' || entry.at > Date.now() || Date.now() - entry.at > maxAge)
      return
    const code =
      entry.notice?.reason === 'REPLACED'
        ? 'SESSION_REPLACED'
        : entry.notice?.reason === 'FORCED_LOGOUT'
          ? 'SESSION_FORCED_LOGOUT'
          : ''
    endedNotice.value = parseEndedNotice(code, entry.notice)
  } catch {
    /* Invalid or disabled storage must not block login. */
  }
}

export function dismissSessionEnded(): void {
  endedNotice.value = null
  try {
    sessionStorage.removeItem(storageKey)
  } catch {
    /* Optional storage. */
  }
}
