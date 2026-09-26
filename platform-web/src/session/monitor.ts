import { onScopeDispose, watch } from 'vue'
import { request } from '../api/client'
import { empty } from '../api/parse'
import { sessionState } from './state'

/** The server's read-only endpoint does not renew the session's idle timeout. */
export function useSessionMonitor(): void {
  const stop = watch(
    () => [sessionState.me?.user.id, sessionState.epoch],
    ([userId], _, cleanup) => {
      if (!userId) return
      let active = true
      let timer: ReturnType<typeof setTimeout> | undefined
      let pending: AbortController | null = null
      const check = async () => {
        if (!active || pending || document.visibilityState === 'hidden') return
        clearTimeout(timer)
        pending = new AbortController()
        try {
          await request({
            path: '/auth/session',
            method: 'GET',
            successStatus: 200,
            decode: empty,
            timeoutMs: 8000,
            signal: pending.signal,
          })
        } catch {
          /* Auth failures use the shared redirect; connectivity failures retry quietly. */
        } finally {
          pending = null
          if (active) timer = setTimeout(() => void check(), 15_000)
        }
      }
      const resume = () => void check()
      document.addEventListener('visibilitychange', resume)
      window.addEventListener('focus', resume)
      timer = setTimeout(resume, 15_000)
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
}
