import { onBeforeUnmount, onMounted, ref } from 'vue'
import {
  getPreviewNodes,
  type PreviewCluster,
  type PreviewNodeStatus,
} from '../../api/preview-node/api'
import { usePageScope } from '../../composables/usePageScope'

export function usePreviewNodeObservation(onForegroundRead: (snapshot: PreviewCluster) => void) {
  const captureScope = usePageScope()
  const snapshot = ref<PreviewCluster | null>(null)
  const busy = ref(false)
  const error = ref<unknown>(null)
  const displayClock = ref(Date.now())
  const observedAt = ref(0)
  let lastAttemptAt = Date.now()
  let foreground: AbortController | undefined
  let background: AbortController | undefined
  let timer: ReturnType<typeof globalThis.setInterval> | undefined

  function publish(value: PreviewCluster) {
    snapshot.value = value
    observedAt.value = Date.now()
    lastAttemptAt = observedAt.value
  }

  function cancelReads() {
    foreground?.abort()
    background?.abort()
    foreground = undefined
    background = undefined
  }

  function reset() {
    cancelReads()
    snapshot.value = null
    observedAt.value = 0
    busy.value = false
    error.value = null
  }

  async function run(operation?: () => Promise<unknown>, afterApplied?: () => void) {
    if (busy.value) return
    background?.abort()
    background = undefined
    const active = captureScope()
    const controller = new AbortController()
    foreground = controller
    const isCurrent = () => active() && foreground === controller && !controller.signal.aborted
    busy.value = true
    error.value = null
    try {
      if (operation) await operation()
      if (!isCurrent()) return
      afterApplied?.()
      const value = await getPreviewNodes(controller.signal)
      if (!isCurrent()) return
      publish(value)
      onForegroundRead(value)
    } catch (cause) {
      if (isCurrent()) error.value = cause
    } finally {
      if (foreground === controller) {
        foreground = undefined
        busy.value = false
      }
    }
  }

  async function refreshObservation() {
    if (background) return
    const active = captureScope()
    const controller = new AbortController()
    background = controller
    try {
      const value = await getPreviewNodes(controller.signal)
      if (active() && !busy.value && background === controller) publish(value)
    } catch {
      // The last reading expires in the UI if a background read fails.
    } finally {
      if (background === controller) background = undefined
    }
  }

  function liveZlmLoad(node: PreviewNodeStatus | undefined) {
    return displayClock.value - observedAt.value > 20_000 ? null : (node?.zlmLoad ?? null)
  }

  onMounted(() => {
    void run()
    timer = globalThis.setInterval(() => {
      displayClock.value = Date.now()
      if (
        !busy.value &&
        snapshot.value?.nodes.some((node) => node.kind === 'ZLM_DIRECT') &&
        displayClock.value - lastAttemptAt >= 15_000
      ) {
        lastAttemptAt = displayClock.value
        void refreshObservation()
      }
    }, 5000)
  })
  onBeforeUnmount(() => {
    cancelReads()
    globalThis.clearInterval(timer)
  })

  return { snapshot, busy, error, run, reset, liveZlmLoad }
}
