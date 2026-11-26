import { computed, onBeforeUnmount, ref, watch } from 'vue'
import { getGroupTree } from '../../api/camera-groups/api'
import type { CameraGroup } from '../../api/camera-groups/types'
import { getPreviewCatalog } from '../../api/preview/api'
import type { PreviewChannel } from '../../api/preview/types'
import { usePageScope } from '../../composables/usePageScope'

interface CatalogQuery {
  deviceMode: boolean
  device?: string
  trialCamera?: string
}

interface DeviceEntry {
  channels: PreviewChannel[]
  total: number
  trialCamera?: string
  isCurrent: () => boolean
}

export function usePreviewCatalog(
  query: () => CatalogQuery,
  openDevice: (entry: DeviceEntry) => Promise<void>,
) {
  const captureScope = usePageScope()
  const channels = ref<PreviewChannel[]>([])
  const groups = ref<CameraGroup[]>([])
  const loading = ref(false)
  const error = ref<unknown>(null)
  const missingTrialChannel = ref(false)
  const search = ref('')
  const total = ref(0)
  const loadedDeviceCount = computed(
    () => new Set(channels.value.map((channel) => channel.deviceGroupKey)).size,
  )
  const deviceTitle = computed(
    () => channels.value[0]?.deviceName || channels.value[0]?.name || '相机预览',
  )
  const lastPage = ref<{ name: string; device?: string; page: number; loaded: number }>()
  const searching = computed(() => Boolean(lastPage.value?.name))
  const hasMore = computed(() => {
    const result = lastPage.value
    return (
      result !== undefined &&
      result.name === search.value.trim() &&
      result.device === query().device &&
      result.loaded < total.value
    )
  })
  let groupsLoaded = false
  let pending: AbortController | undefined
  let searchTimer: ReturnType<typeof globalThis.setTimeout> | undefined

  function invalidate() {
    globalThis.clearTimeout(searchTimer)
    pending?.abort()
    pending = undefined
    loading.value = false
  }

  function reset() {
    invalidate()
    channels.value = []
    groups.value = []
    total.value = 0
    lastPage.value = undefined
    groupsLoaded = false
    missingTrialChannel.value = false
    error.value = null
  }

  async function load(append = false) {
    if (append && (!hasMore.value || loading.value)) return
    invalidate()
    const { deviceMode, device, trialCamera } = query()
    const name = search.value.trim()
    const previous = append ? lastPage.value : undefined
    error.value = null
    missingTrialChannel.value = false
    if (deviceMode && !device) {
      channels.value = []
      total.value = 0
      lastPage.value = undefined
      return
    }
    const active = captureScope()
    const controller = new AbortController()
    pending = controller
    const isCurrent = () => active() && pending === controller && !controller.signal.aborted
    loading.value = true
    try {
      const refreshGroups = !deviceMode && !append && (!groupsLoaded || !name)
      const groupResult = refreshGroups
        ? getGroupTree(controller.signal).catch(() => null)
        : Promise.resolve(null)
      const [result, groupRows] = await Promise.all([
        getPreviewCatalog(
          { page: (previous?.page ?? 0) + 1, size: 100, name: name || undefined, device },
          controller.signal,
        ),
        groupResult,
      ])
      if (!isCurrent()) return
      if (groupRows) {
        groups.value = groupRows
        groupsLoaded = true
      }
      if (previous) include(result.items)
      else channels.value = result.items
      lastPage.value = {
        name,
        device,
        page: result.page,
        loaded: (previous?.loaded ?? 0) + result.items.length,
      }
      total.value = result.total
      if (deviceMode && !append)
        await openDevice({ channels: result.items, total: result.total, trialCamera, isCurrent })
    } catch (cause) {
      if (isCurrent()) error.value = cause
    } finally {
      if (isCurrent()) {
        pending = undefined
        loading.value = false
      }
    }
  }

  function include(restored: PreviewChannel[]) {
    const byCamera = new Map(channels.value.map((channel) => [channel.cameraId, channel]))
    for (const channel of restored) byCamera.set(channel.cameraId, channel)
    channels.value = [...byCamera.values()]
  }

  watch(search, () => {
    globalThis.clearTimeout(searchTimer)
    searchTimer = globalThis.setTimeout(() => void load(), 300)
  })
  onBeforeUnmount(invalidate)

  return {
    channels,
    groups,
    loading,
    error,
    missingTrialChannel,
    search,
    searching,
    total,
    hasMore,
    loadedDeviceCount,
    deviceTitle,
    load,
    invalidate,
    reset,
    include,
  }
}
