import { onActivated, onDeactivated, onScopeDispose, ref, watch, type Ref } from 'vue'

/** Opt-in reads for the visible host tab. A failed read requires the user to resume. */
export function useServerAutoRefresh(
  read: () => Promise<boolean>,
  visible: Ref<boolean>,
  busy: Ref<boolean>,
) {
  const enabled = ref(false)
  const interval = ref(30)
  const remaining = ref(30)
  const paused = ref(false)
  let timer: ReturnType<typeof setTimeout> | undefined
  let active = true
  let disposed = false
  let generation = 0
  let reading = false
  function clear() {
    clearTimeout(timer)
    generation++
    remaining.value = interval.value
  }
  function eligible() {
    return (
      !disposed &&
      active &&
      enabled.value &&
      visible.value &&
      typeof document !== 'undefined' &&
      !document.hidden
    )
  }
  function schedule() {
    if (!eligible()) return
    const current = generation
    timer = setTimeout(async () => {
      if (current !== generation || !eligible()) return
      if (busy.value || reading) remaining.value = interval.value
      else if (--remaining.value <= 0) {
        reading = true
        let succeeded = false
        try {
          succeeded = await read()
        } catch {
          /* The caller renders the request error. */
        } finally {
          reading = false
        }
        if (current !== generation || !eligible()) return
        if (!succeeded) {
          enabled.value = false
          paused.value = true
          return
        }
        remaining.value = interval.value
      }
      schedule()
    }, 1000)
  }
  function reset() {
    clear()
    schedule()
  }
  watch(
    [enabled, interval, visible],
    () => {
      if (enabled.value) paused.value = false
      reset()
    },
    { flush: 'sync' },
  )
  onActivated(() => {
    active = true
    reset()
  })
  onDeactivated(() => {
    active = false
    clear()
  })
  if (typeof document !== 'undefined') document.addEventListener('visibilitychange', reset)
  onScopeDispose(() => {
    disposed = true
    clear()
    if (typeof document !== 'undefined') document.removeEventListener('visibilitychange', reset)
  })
  return { enabled, interval, remaining, paused }
}
