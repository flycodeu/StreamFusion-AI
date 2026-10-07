import { onMounted, ref } from 'vue'
import { getAccessOptions } from '../../api/camera-access/api'
import type { AccessAdapter } from '../../api/camera-access/types'
import { usePageScope } from '../../composables/usePageScope'

/** Optional hints never make local asset registration depend on network readiness. */
export function useDeviceAdapters() {
  const adapters = ref<AccessAdapter[]>([])
  const captureScope = usePageScope()
  onMounted(async () => {
    const active = captureScope()
    try {
      const options = await getAccessOptions()
      if (active())
        adapters.value = (options.adapters ?? []).filter((item) => item.category === 'DEVICE')
    } catch {
      // The user can leave the adapter unspecified and maintain the connection later.
    }
  })
  return adapters
}
