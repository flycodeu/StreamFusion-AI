import { onScopeDispose } from 'vue'

const channelName = 'sf-session-change-v1'
const storageKey = channelName
let senderId = ''

interface SessionChange {
  version: 1
  sender: string
  id: string
}

function ownId(): string {
  return (senderId ||= crypto.randomUUID())
}

function parseChange(value: unknown): SessionChange | null {
  if (!value || typeof value !== 'object') return null
  const row = value as Record<string, unknown>
  const validId = (id: unknown): id is string =>
    typeof id === 'string' && /^[a-f0-9-]{36}$/.test(id)
  if (row.version !== 1 || !validId(row.sender) || !validId(row.id)) return null
  return { version: 1, sender: row.sender, id: row.id }
}

/** Notify only that the shared cookie changed. No account, token or credential crosses tabs. */
export function notifyOtherTabs(): void {
  if (typeof window === 'undefined') return
  const message: SessionChange = { version: 1, sender: ownId(), id: crypto.randomUUID() }
  try {
    const channel = new BroadcastChannel(channelName)
    try {
      channel.postMessage(message)
    } finally {
      channel.close()
    }
  } catch {
    /* Storage events cover browsers where BroadcastChannel is unavailable. */
  }
  try {
    localStorage.setItem(storageKey, JSON.stringify(message))
    localStorage.removeItem(storageKey)
  } catch {
    /* BroadcastChannel still works when browser storage is disabled. */
  }
}

/** Other tabs share our cookie, so rebuild identity without inventing a forced-logout notice. */
export function useCrossTabSession(onChange: () => void): void {
  if (typeof window === 'undefined') return
  const seen = new Set<string>()
  const receive = (value: unknown) => {
    const message = parseChange(value)
    if (!message || message.sender === ownId() || seen.has(message.id)) return
    seen.add(message.id)
    // Both transports can deliver the same signal. Keep this cache bounded in long-lived tabs.
    if (seen.size > 32) seen.delete(seen.values().next().value!)
    onChange()
  }
  const onStorage = (event: StorageEvent) => {
    if (event.key !== storageKey || !event.newValue || event.newValue.length > 512) return
    try {
      receive(JSON.parse(event.newValue))
    } catch {
      /* Invalid data from an older tab must not affect the current identity. */
    }
  }
  let channel: BroadcastChannel | undefined
  try {
    channel = new BroadcastChannel(channelName)
    channel.onmessage = (event: MessageEvent<unknown>) => receive(event.data)
  } catch {
    /* The storage listener remains available. */
  }
  window.addEventListener('storage', onStorage)
  onScopeDispose(() => {
    window.removeEventListener('storage', onStorage)
    if (channel) {
      channel.onmessage = null
      channel.close()
    }
  })
}
