import { onActivated, onDeactivated, onMounted, onScopeDispose } from 'vue'

/** Visible-page reads only. Patrol work has a separate backend lifetime. */
export function useDeviceRefresh(read: () => Promise<void>) {
  if (typeof document === 'undefined') return () => {}
  let timer: ReturnType<typeof setTimeout> | undefined
  let active = true,
    disposed = false,
    failures = 0
  function schedule() {
    clearTimeout(timer)
    if (!active || disposed || document.hidden || failures >= 3) return
    timer = setTimeout(async () => {
      try {
        await read()
        failures = 0
      } catch {
        failures++
      }
      schedule()
    }, 30000)
  }
  onMounted(schedule)
  onActivated(() => {
    active = true
    schedule()
  })
  onDeactivated(() => {
    active = false
    clearTimeout(timer)
  })
  document.addEventListener('visibilitychange', schedule)
  onScopeDispose(() => {
    disposed = true
    clearTimeout(timer)
    document.removeEventListener('visibilitychange', schedule)
  })
  return () => {
    failures = 0
    schedule()
  }
}
