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
import { getStatusPage } from '../../api/camera-status/api'
import type { StatusPage, StatusProfile, StatusSearch } from '../../api/camera-status/types'
import { isTerminal } from './model'
import { pageCoverage } from './health'
import { sessionState } from '../../session/state'

export function useStatusPage(query: Ref<StatusSearch>, active: Ref<boolean>) {
  const result = shallowRef<StatusPage | null>(null)
  const total = ref(0)
  const loading = ref(false),
    paused = ref(false),
    error = shallowRef<unknown>(null)
  const visible = ref(typeof document === 'undefined' || !document.hidden),
    activated = ref(true)
  const authorized = computed(() => !!sessionState.me?.modules.includes('camera'))
  const enabled = computed(
    () => active.value && visible.value && activated.value && authorized.value,
  )
  const rows = computed(() => result.value?.items ?? [])
  const summary = computed(() => result.value?.summary ?? null)
  const coverage = computed(() =>
    result.value ? pageCoverage(rows.value, query.value.layer) : null,
  )
  function layers(profile: StatusProfile) {
    return [profile.control, profile.media, ...(profile.upstream ? [profile.upstream] : [])]
  }
  let disposed = false,
    epoch = 0,
    reads = 0,
    failures = 0
  let controller: AbortController | undefined
  let pollTimer: ReturnType<typeof setTimeout> | undefined

  function stop() {
    ++epoch
    clearTimeout(pollTimer)
    controller?.abort()
    controller = undefined
    loading.value = false
  }
  function current(generation: number) {
    return !disposed && enabled.value && generation === epoch
  }
  function schedule(generation: number) {
    clearTimeout(pollTimer)
    if (!current(generation)) return
    const progressing =
      result.value?.items.some((channel) =>
        channel.profiles.some((profile) =>
          layers(profile).some((layer) => layer.latestCheck && !isTerminal(layer.latestCheck)),
        ),
      ) ?? false
    if ((progressing && reads >= 30) || failures >= 3) {
      paused.value = true
      return
    }
    if (!progressing) reads = 0
    pollTimer = setTimeout(() => void read(generation, progressing), progressing ? 2000 : 30000)
  }
  async function read(generation: number, progress: boolean): Promise<void> {
    if (!current(generation) || loading.value) return
    if (progress) ++reads
    controller = new AbortController()
    loading.value = true
    try {
      const response = await getStatusPage({ ...query.value }, controller.signal)
      if (!current(generation)) return
      result.value = response
      total.value = response.total
      failures = 0
      error.value = null
    } catch (cause) {
      if (current(generation)) {
        error.value = cause
        ++failures
      }
    } finally {
      if (current(generation)) {
        loading.value = false
        controller = undefined
        schedule(generation)
      }
    }
  }
  async function refresh() {
    if (disposed || !enabled.value) return
    stop()
    reads = 0
    failures = 0
    paused.value = false
    await read(epoch, false)
  }
  // Identity cleanup increments epoch before clearing me. Invalidate synchronously, never read
  // from this callback with the still-present old identity (401 must not trigger another GET).
  watch(
    () => sessionState.epoch,
    () => {
      stop()
      result.value = null
      total.value = 0
      error.value = null
      reads = 0
      failures = 0
      paused.value = false
    },
    { flush: 'sync' },
  )
  watch(
    [query, enabled, () => sessionState.me?.user.id, authorized],
    ([nextQuery, , nextUser], [previousQuery, , previousUser]) => {
      stop()
      if (nextQuery !== previousQuery || nextUser !== previousUser || !authorized.value)
        result.value = null
      if (nextUser !== previousUser) total.value = 0
      error.value = null
      reads = 0
      failures = 0
      paused.value = false
      if (enabled.value) void read(epoch, false)
    },
    { immediate: true, flush: 'sync' },
  )
  function visibilityChanged() {
    visible.value = !document.hidden
  }
  if (typeof document !== 'undefined')
    document.addEventListener('visibilitychange', visibilityChanged)
  if (getCurrentInstance()) {
    onDeactivated(() => {
      activated.value = false
    })
    onActivated(() => {
      activated.value = true
    })
  }
  onScopeDispose(() => {
    disposed = true
    stop()
    if (typeof document !== 'undefined')
      document.removeEventListener('visibilitychange', visibilityChanged)
  })
  const displayedResult = computed(() =>
    result.value ? { ...result.value, summary: summary.value! } : null,
  )
  return {
    rows,
    result: displayedResult,
    summary,
    coverage,
    total,
    loading,
    error,
    paused,
    refresh,
  }
}
