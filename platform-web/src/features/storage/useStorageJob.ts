import { onScopeDispose, ref, watch } from 'vue'
import * as api from '../../api/storage/api'
import type { StorageTask } from '../../api/storage/types'

const live = new Set(['QUEUED', 'RUNNING', 'RECONCILING', 'IN_PROGRESS'])
export function useStorageJob(instanceId: () => string, updated: () => void) {
  const task = ref<StorageTask | null>(null),
    error = ref<unknown>(null),
    busy = ref(false)
  let generation = 0,
    timer: ReturnType<typeof globalThis.setTimeout> | undefined
  function stop() {
    generation++
    globalThis.clearTimeout(timer)
    busy.value = false
  }
  watch(instanceId, () => {
    stop()
    task.value = null
    error.value = null
  })
  onScopeDispose(stop)
  async function poll(id: string, current: number, remaining: number) {
    try {
      const value = await api.getTask(id)
      if (current !== generation) return
      task.value = value
      if (live.has(value.status) && remaining > 0)
        timer = globalThis.setTimeout(() => void poll(id, current, remaining - 1), 2000)
      else {
        busy.value = false
        updated()
      }
    } catch (e) {
      if (current === generation) {
        busy.value = false
        error.value = e
      }
    }
  }
  async function run(create: () => Promise<StorageTask>) {
    if (busy.value) return
    stop()
    const current = generation
    busy.value = true
    error.value = null
    try {
      const receipt = await create()
      if (current !== generation) return
      task.value = receipt
      await poll(receipt.taskId, current, 59)
    } catch (e) {
      if (current === generation) {
        error.value = e
        busy.value = false
      }
    }
  }
  return {
    task,
    error,
    busy,
    run,
  }
}
