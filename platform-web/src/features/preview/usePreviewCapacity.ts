import {
  computed,
  getCurrentInstance,
  onActivated,
  onDeactivated,
  onScopeDispose,
  ref,
  shallowRef,
  watch,
} from 'vue'
import type { Ref } from 'vue'
import { getPreviewCapacity } from '../../api/preview/api'
import type { PreviewCapacity } from '../../api/preview/types'
import { sessionState } from '../../session/state'

/** Advisory single-instance snapshot; admission always belongs to the backend. */
export function usePreviewCapacity(active: Ref<boolean>) {
  const capacity = shallowRef<PreviewCapacity | null>(null)
  const error = shallowRef<unknown>(null)
  const loading = ref(false),
    paused = ref(false),
    activated = ref(true)
  const visible = ref(typeof document === 'undefined' || !document.hidden)
  const enabled = computed(
    () =>
      active.value &&
      activated.value &&
      visible.value &&
      !!sessionState.me?.modules.includes('camera'),
  )
  let disposed = false,
    generation = 0,
    failures = 0
  let controller: AbortController | undefined
  let timer: ReturnType<typeof setTimeout> | undefined
  function stop() {
    ++generation
    clearTimeout(timer)
    controller?.abort()
    controller = undefined
    loading.value = false
    capacity.value = null
  }
  function current(epoch: number) {
    return !disposed && enabled.value && epoch === generation
  }
  async function read(epoch: number) {
    if (!current(epoch) || loading.value) return
    controller = new AbortController()
    loading.value = true
    try {
      const result = await getPreviewCapacity(controller.signal)
      if (!current(epoch)) return
      capacity.value = result
      error.value = null
      failures = 0
    } catch (cause) {
      if (!current(epoch)) return
      capacity.value = null
      error.value = cause
      ++failures
    } finally {
      if (current(epoch)) {
        loading.value = false
        controller = undefined
        paused.value = failures >= 3
        if (!paused.value) timer = setTimeout(() => void read(epoch), 10000)
      }
    }
  }
  async function refresh() {
    if (disposed || !enabled.value || loading.value) return
    stop()
    failures = 0
    paused.value = false
    await read(generation)
  }
  watch(
    () => sessionState.epoch,
    () => {
      stop()
      error.value = null
      failures = 0
      paused.value = false
    },
    { flush: 'sync' },
  )
  watch(
    [enabled, () => sessionState.me?.user.id],
    () => {
      stop()
      error.value = null
      failures = 0
      paused.value = false
      if (enabled.value) void read(generation)
    },
    { immediate: true, flush: 'sync' },
  )
  const visibility = () => {
    visible.value = !document.hidden
  }
  if (typeof document !== 'undefined') document.addEventListener('visibilitychange', visibility)
  if (getCurrentInstance()) {
    onActivated(() => {
      activated.value = true
    })
    onDeactivated(() => {
      activated.value = false
    })
  }
  onScopeDispose(() => {
    disposed = true
    stop()
    if (typeof document !== 'undefined')
      document.removeEventListener('visibilitychange', visibility)
  })
  return { capacity, error, loading, paused, refresh }
}
