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
import type { StatusPage, StatusSearch } from '../../api/camera-status/types'
import { isTerminal } from './model'

export function useStatusPage(query: Ref<StatusSearch>, active: Ref<boolean>) {
  const result = shallowRef<StatusPage | null>(null)
  const total = ref(0)
  const loading = ref(false),
    paused = ref(false),
    error = shallowRef<unknown>(null)
  const visible = ref(typeof document === 'undefined' || !document.hidden),
    activated = ref(true),
    elapsed = ref(0)
  const enabled = computed(() => active.value && visible.value && activated.value)
  const rows = computed(() => {
    if (!result.value) return []
    const now = Date.parse(result.value.serverNow) + elapsed.value
    return result.value.items.map((channel) => ({
      ...channel,
      profiles: channel.profiles.map((profile) =>
        profile.freshness === 'CURRENT' &&
        profile.observation &&
        Date.parse(profile.observation.validUntil) <= now
          ? { ...profile, freshness: 'STALE' as const }
          : profile,
      ),
    }))
  })
  let disposed = false,
    epoch = 0,
    reads = 0,
    failures = 0,
    receivedAt = 0
  let controller: AbortController | undefined
  let pollTimer: ReturnType<typeof setTimeout> | undefined
  let expiryTimer: ReturnType<typeof setTimeout> | undefined

  function stop() {
    ++epoch
    clearTimeout(pollTimer)
    clearTimeout(expiryTimer)
    controller?.abort()
    controller = undefined
    loading.value = false
  }
  function current(generation: number) {
    return !disposed && enabled.value && generation === epoch
  }
  function updateExpiry() {
    clearTimeout(expiryTimer)
    elapsed.value = Math.max(0, performance.now() - receivedAt)
    if (!enabled.value || !result.value) return
    const now = Date.parse(result.value.serverNow) + elapsed.value
    const deadlines = result.value.items.flatMap((channel) =>
      channel.profiles.flatMap((profile) =>
        profile.freshness === 'CURRENT' && profile.observation
          ? [Date.parse(profile.observation.validUntil)]
          : [],
      ),
    )
    const next = Math.min(...deadlines.filter((deadline) => deadline > now))
    if (Number.isFinite(next))
      expiryTimer = setTimeout(
        updateExpiry,
        Math.min(Math.max(1, Math.ceil(next - now)), 2_147_483_647),
      )
  }
  function schedule(generation: number) {
    clearTimeout(pollTimer)
    if (!current(generation)) return
    const progressing =
      result.value?.items.some((channel) =>
        channel.profiles.some((profile) => profile.latestCheck && !isTerminal(profile.latestCheck)),
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
      receivedAt = performance.now()
      elapsed.value = 0
      failures = 0
      error.value = null
      updateExpiry()
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
  watch(
    [query, enabled],
    ([nextQuery], [previousQuery]) => {
      stop()
      if (nextQuery !== previousQuery) result.value = null
      error.value = null
      reads = 0
      failures = 0
      paused.value = false
      updateExpiry()
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
  return { rows, result, total, loading, error, paused, refresh }
}
