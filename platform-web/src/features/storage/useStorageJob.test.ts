import { effectScope, nextTick, ref } from 'vue'
import { afterEach as cleanup, expect, it, vi } from 'vitest'
import * as api from '../../api/storage/api'
import type { StorageTask } from '../../api/storage/types'
import { useStorageJob } from './useStorageJob'

vi.mock('../../api/storage/api', () => ({ getTask: vi.fn() }))
const receipt = { taskId: '1', status: 'QUEUED' } as StorageTask
cleanup(() => {
  vi.restoreAllMocks()
  vi.useRealTimers()
})
it('polls the accepted task and refreshes only after its terminal result', async () => {
  vi.useFakeTimers()
  vi.mocked(api.getTask)
    .mockResolvedValueOnce({ ...receipt, status: 'RUNNING' })
    .mockResolvedValueOnce({ ...receipt, status: 'SUCCEEDED' })
  const scope = effectScope(),
    updated = vi.fn()
  const job = scope.run(() => useStorageJob(() => '10', updated))!
  await job.run(async () => receipt)
  expect(job.busy.value).toBe(true)
  expect(updated).not.toHaveBeenCalled()
  await vi.advanceTimersByTimeAsync(2000)
  expect(job.task.value?.status).toBe('SUCCEEDED')
  expect(updated).toHaveBeenCalledOnce()
  scope.stop()
})
it('discards an old instance response and stops polling after leaving', async () => {
  vi.useFakeTimers()
  let complete!: (value: StorageTask) => void
  vi.mocked(api.getTask).mockImplementation(
    () =>
      new Promise((resolve) => {
        complete = resolve
      }),
  )
  const scope = effectScope(),
    id = ref('10'),
    updated = vi.fn()
  const job = scope.run(() => useStorageJob(() => id.value, updated))!
  const running = job.run(async () => receipt)
  await Promise.resolve()
  id.value = '11'
  await nextTick()
  complete({ ...receipt, status: 'SUCCEEDED' })
  await running
  expect(job.task.value).toBeNull()
  expect(updated).not.toHaveBeenCalled()
  scope.stop()
  await vi.advanceTimersByTimeAsync(10000)
  expect(api.getTask).toHaveBeenCalledOnce()
})
