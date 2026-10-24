import { computed, onMounted, ref } from 'vue'
import { getAccessOptions } from '../../api/camera-access/api'
import type { AccessAdapter } from '../../api/camera-access/types'
import { usePageScope } from '../../composables/usePageScope'
import { sessionState } from '../../session/state'

let cached: { epoch: number; expiresAt: number; adapters: AccessAdapter[] } | undefined
let pending: { epoch: number; promise: Promise<AccessAdapter[]> } | undefined

function loadAdapters(epoch: number): Promise<AccessAdapter[]> {
  if (cached?.epoch === epoch && cached.expiresAt > Date.now())
    return Promise.resolve(cached.adapters)
  if (pending?.epoch === epoch) return pending.promise
  const promise = getAccessOptions()
    .then((options) => {
      const adapters = options.adapters ?? []
      if (sessionState.epoch === epoch) cached = { epoch, expiresAt: Date.now() + 60_000, adapters }
      return adapters
    })
    .finally(() => {
      if (pending?.promise === promise) pending = undefined
    })
  pending = { epoch, promise }
  return promise
}

/** Optional metadata is shared across drawers, bounded to the current session and one minute. */
export function useAccessAdapters() {
  const epoch = sessionState.epoch
  const adapters = ref<AccessAdapter[]>(
    cached?.epoch === epoch && cached.expiresAt > Date.now() ? cached.adapters : [],
  )
  const captureScope = usePageScope()
  onMounted(async () => {
    const epoch = sessionState.epoch
    const active = captureScope()
    try {
      const result = await loadAdapters(epoch)
      if (active()) adapters.value = result
    } catch {
      // The user can leave the adapter unspecified and maintain the connection later.
    }
  })
  return adapters
}

/** Local registration remains usable when optional capability metadata cannot be loaded. */
export function useDeviceAdapters() {
  const adapters = useAccessAdapters()
  return computed(() => adapters.value.filter((adapter) => adapter.category === 'DEVICE'))
}
