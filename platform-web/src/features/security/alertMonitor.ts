import { computed, onScopeDispose, ref, watch } from 'vue'
import { getSecurityFeed } from '../../api/security/alerts'
import type { SecurityFeed } from '../../api/security/alerts'
import { sessionState } from '../../session/state'

/** The feed is read before the servlet session wrapper, so this polling never renews idle time. */
export function useSecurityAlerts(notify: (count: number) => void) {
  const allowed = computed(
    () => !!sessionState.me?.isSuperAdmin && sessionState.me.modules?.includes('audit'),
  )
  const feed = ref<SecurityFeed | null>(null)
  const unavailable = ref(false)
  const stop = watch(
    () => [allowed.value, sessionState.me?.user.id, sessionState.epoch] as const,
    ([permitted], _, cleanup) => {
      feed.value = null
      unavailable.value = false
      if (!permitted || typeof document === 'undefined') return
      let active = true
      let timer: ReturnType<typeof setTimeout> | undefined
      let pending: AbortController | null = null
      let previous: SecurityFeed | null = null
      const poll = async () => {
        if (!active || pending || document.visibilityState === 'hidden') return
        clearTimeout(timer)
        pending = new AbortController()
        try {
          const result = await getSecurityFeed(pending.signal)
          if (!active) return
          feed.value = result
          unavailable.value = false
          if (previous?.instanceId === result.instanceId && result.revision > previous.revision)
            notify(result.revision - previous.revision)
          previous = result
        } catch {
          if (active) unavailable.value = true
        } finally {
          pending = null
          if (active) timer = setTimeout(() => void poll(), 20_000)
        }
      }
      const resume = () => void poll()
      document.addEventListener('visibilitychange', resume)
      window.addEventListener('focus', resume)
      void poll()
      cleanup(() => {
        active = false
        clearTimeout(timer)
        pending?.abort()
        document.removeEventListener('visibilitychange', resume)
        window.removeEventListener('focus', resume)
      })
    },
    { immediate: true },
  )
  onScopeDispose(stop)
  return { allowed, feed, unavailable }
}
