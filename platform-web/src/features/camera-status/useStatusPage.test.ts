import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { effectScope, ref, shallowRef } from 'vue'
import type { EffectScope, Ref } from 'vue'
import type { ExecutionState, StatusPage, StatusSearch } from '../../api/camera-status/types'
import { useStatusPage } from './useStatusPage'

const api = vi.hoisted(() => ({ page: vi.fn() }))
const hooks = vi.hoisted(() => ({
  activated: [] as (() => void)[],
  deactivated: [] as (() => void)[],
}))
vi.mock('../../api/camera-status/api', () => ({ getStatusPage: api.page }))
vi.mock('vue', async (original) => ({
  ...(await original<typeof import('vue')>()),
  getCurrentInstance: () => ({}),
  onActivated: (callback: () => void) => hooks.activated.push(callback),
  onDeactivated: (callback: () => void) => hooks.deactivated.push(callback),
}))
const now = '2026-10-13T01:00:00Z'
function page(state: ExecutionState | null = null, cameraId = '1'): StatusPage {
  return {
    total: 1,
    page: 1,
    size: 20,
    serverNow: now,
    summary: {
      total: 1,
      accessible: 0,
      failed: 0,
      checking: state === 'RUNNING' || state === 'QUEUED' ? 1 : 0,
      stale: 0,
      unknown: state === 'RUNNING' || state === 'QUEUED' ? 0 : 1,
      disabled: 0,
    },
    items: [
      {
        cameraId,
        name: '通道一',
        deviceName: null,
        lifecycle: 'ENABLED',
        profiles: [
          {
            streamProfileId: '10',
            name: '主码流',
            streamKind: 'MAIN',
            enabled: true,
            control: {
              layer: 'CONTROL',
              probeKind: 'RTSP_DESCRIBE',
              observation: null,
              freshness: 'NEVER',
              configurationCurrent: true,
              canCheck: true,
              blockingReasonCode: null,
              latestCheck: state
                ? {
                    checkId: '100',
                    cameraId,
                    streamProfileId: '10',
                    layer: 'CONTROL',
                    probeKind: 'RTSP_DESCRIBE',
                    executionState: state,
                    requestedAt: now,
                    startedAt: null,
                    finishedAt: null,
                    deadlineAt: now,
                    observation: null,
                    reasonCode: null,
                    durationMs: null,
                    traceId: 'a'.repeat(32),
                    snapshotApplied: false,
                  }
                : null,
              timeline: null,
            },
            media: {
              layer: 'MEDIA',
              probeKind: 'ZLM_RTP_FRAMES',
              observation: null,
              freshness: 'NEVER',
              configurationCurrent: true,
              latestCheck: null,
              canCheck: true,
              blockingReasonCode: null,
              timeline: null,
            },
          },
        ],
      },
    ],
  }
}
function deferred<T>() {
  let resolve!: (value: T) => void
  const promise = new Promise<T>((ready) => {
    resolve = ready
  })
  return { promise, resolve }
}
async function flush() {
  for (let i = 0; i < 8; i++) await Promise.resolve()
}
let scope: EffectScope, controls: ReturnType<typeof useStatusPage>
let query: Ref<StatusSearch>, active: Ref<boolean>, documentState: EventTarget & { hidden: boolean }
beforeEach(async () => {
  vi.useFakeTimers({ toFake: ['setTimeout', 'clearTimeout', 'Date', 'performance'] })
  documentState = Object.assign(new EventTarget(), { hidden: false })
  vi.stubGlobal('document', documentState)
  api.page.mockReset().mockResolvedValue(page())
  query = shallowRef({ page: 1, size: 20, layer: 'CONTROL' })
  active = ref(true)
  scope = effectScope()
  controls = scope.run(() => useStatusPage(query, active))!
  await flush()
})
afterEach(() => {
  scope.stop()
  hooks.activated.splice(0)
  hooks.deactivated.splice(0)
  vi.unstubAllGlobals()
  vi.useRealTimers()
})
describe('aggregate camera status page read owner', () => {
  it('expires MEDIA independently without changing CONTROL or starting a probe', async () => {
    const response = page()
    const profile = response.items[0]!.profiles[0]!
    const fact = {
      value: 'ACCESSIBLE' as const,
      reasonCode: 'RTSP_ACCESSIBLE',
      checkId: '100',
      observedAt: now,
      readAt: now,
      validUntil: '2026-10-13T01:00:20Z',
      mediaEvidence: null,
    }
    profile.control = { ...profile.control, freshness: 'CURRENT', observation: fact }
    profile.media = {
      ...profile.media,
      freshness: 'CURRENT',
      observation: {
        ...fact,
        reasonCode: 'MEDIA_RECEIVING',
        validUntil: '2026-10-13T01:00:05Z',
        mediaEvidence: {
          windowStartedAt: '2026-10-13T00:59:55Z',
          windowEndedAt: now,
          firstFrames: 10,
          lastFrames: 30,
          sampleCount: 3,
          maxGapMs: 2500,
        },
      },
    }
    api.page.mockResolvedValue(response)
    await controls.refresh()
    await vi.advanceTimersByTimeAsync(5001)
    expect(controls.rows.value[0]?.profiles[0]?.media.freshness).toBe('STALE')
    expect(controls.rows.value[0]?.profiles[0]?.control.freshness).toBe('CURRENT')
    expect(api.page).toHaveBeenCalledTimes(2)
  })
  it('opens and refreshes with aggregate GET only, with no per-channel request', async () => {
    expect(api.page).toHaveBeenCalledOnce()
    expect(api.page.mock.calls[0]![0]).toEqual({ page: 1, size: 20, layer: 'CONTROL' })
    await vi.advanceTimersByTimeAsync(20000)
    expect(api.page).toHaveBeenCalledOnce()
    await controls.refresh()
    expect(api.page).toHaveBeenCalledTimes(2)
  })
  it('serializes progress reads only when the current page contains an active check', async () => {
    api.page.mockResolvedValue(page('RUNNING'))
    await controls.refresh()
    const pending = deferred<StatusPage>()
    api.page.mockReturnValue(pending.promise)
    await vi.advanceTimersByTimeAsync(2000)
    expect(api.page).toHaveBeenCalledTimes(3)
    await vi.advanceTimersByTimeAsync(8000)
    expect(api.page).toHaveBeenCalledTimes(3)
    pending.resolve(page('COMPLETED'))
    await flush()
    await vi.advanceTimersByTimeAsync(20000)
    expect(api.page).toHaveBeenCalledTimes(3)
  })
  it('bounds progress to thirty GETs and pauses without discarding observations', async () => {
    api.page.mockResolvedValue(page('RUNNING'))
    await controls.refresh()
    await vi.advanceTimersByTimeAsync(200000)
    expect(api.page).toHaveBeenCalledTimes(32)
    expect(controls.paused.value).toBe(true)
    expect(controls.rows.value).toHaveLength(1)
    expect(vi.getTimerCount()).toBe(0)
  })
  it('pauses after three consecutive read errors and preserves the last result', async () => {
    api.page.mockResolvedValue(page('RUNNING'))
    await controls.refresh()
    api.page.mockRejectedValue(new Error('read unavailable'))
    await vi.advanceTimersByTimeAsync(20000)
    expect(api.page).toHaveBeenCalledTimes(5)
    expect(controls.paused.value).toBe(true)
    expect(controls.rows.value[0]?.cameraId).toBe('1')
    expect(controls.error.value).toBeInstanceOf(Error)
  })
  it('isolates query epochs, aborts old GETs and retains applied filters for paging', async () => {
    const old = deferred<StatusPage>()
    api.page.mockReturnValueOnce(old.promise)
    const reading = controls.refresh()
    const signal = api.page.mock.calls.at(-1)![1] as AbortSignal
    api.page.mockResolvedValue(page(null, '2'))
    query.value = { page: 2, size: 20, layer: 'CONTROL', name: '车间', observation: 'FAILED' }
    await flush()
    expect(signal.aborted).toBe(true)
    expect(api.page.mock.calls.at(-1)![0]).toEqual(query.value)
    old.resolve(page('RUNNING'))
    await reading
    expect(controls.rows.value[0]?.cameraId).toBe('2')
    expect(vi.getTimerCount()).toBe(1)
  })
  it('keeps the last known pagination extent during an in-flight query change', async () => {
    const response = page()
    response.total = 21
    api.page.mockResolvedValueOnce(response)
    await controls.refresh()
    const pending = deferred<StatusPage>()
    api.page.mockReturnValueOnce(pending.promise)
    query.value = { page: 2, size: 20, layer: 'CONTROL' }
    expect(controls.result.value).toBeNull()
    expect(controls.total.value).toBe(21)
    expect(controls.loading.value).toBe(true)
    pending.resolve({ ...page(null, '2'), page: 2, total: 21 })
    await flush()
    expect(controls.result.value?.page).toBe(2)
    expect(controls.total.value).toBe(21)
  })
  it.each(['inactive', 'deactivated', 'hidden'] as const)(
    'stops reads when %s and resumes with one GET',
    async (kind) => {
      api.page.mockResolvedValue(page('RUNNING'))
      await controls.refresh()
      const before = api.page.mock.calls.length
      if (kind === 'inactive') active.value = false
      else if (kind === 'deactivated') hooks.deactivated.forEach((callback) => callback())
      else {
        documentState.hidden = true
        documentState.dispatchEvent(new Event('visibilitychange'))
      }
      await vi.advanceTimersByTimeAsync(20000)
      expect(api.page).toHaveBeenCalledTimes(before)
      if (kind === 'inactive') active.value = true
      else if (kind === 'deactivated') hooks.activated.forEach((callback) => callback())
      else {
        documentState.hidden = false
        documentState.dispatchEvent(new Event('visibilitychange'))
      }
      await flush()
      expect(api.page).toHaveBeenCalledTimes(before + 1)
    },
  )
  it('expires multiple facts with one local timer and no network or timestamp refresh', async () => {
    const response = page()
    const profile = response.items[0]!.profiles[0]!
    profile.control.freshness = 'CURRENT'
    profile.control.observation = {
      mediaEvidence: null,
      value: 'ACCESSIBLE',
      reasonCode: 'RTSP_ACCESSIBLE',
      checkId: '100',
      observedAt: now,
      readAt: now,
      validUntil: '2026-10-13T01:00:05Z',
    }
    response.items[0]!.profiles.push({
      ...profile,
      streamProfileId: '11',
      control: {
        ...profile.control,
        observation: { ...profile.control.observation!, validUntil: '2026-10-13T01:00:08Z' },
      },
    })
    api.page.mockResolvedValue(response)
    await controls.refresh()
    expect(vi.getTimerCount()).toBe(2)
    await vi.advanceTimersByTimeAsync(5001)
    expect(controls.rows.value[0]?.profiles.map((entry) => entry.control.freshness)).toEqual([
      'STALE',
      'CURRENT',
    ])
    expect(vi.getTimerCount()).toBe(2)
    await vi.advanceTimersByTimeAsync(3000)
    expect(controls.rows.value[0]?.profiles.map((entry) => entry.control.freshness)).toEqual([
      'STALE',
      'STALE',
    ])
    expect(controls.rows.value[0]?.profiles[0]?.control.observation?.observedAt).toBe(now)
    expect(api.page).toHaveBeenCalledTimes(2)
    expect(vi.getTimerCount()).toBe(1)
  })
  it('reads scheduled results every thirty seconds without launching or overlapping camera checks', async () => {
    await vi.advanceTimersByTimeAsync(30000)
    expect(api.page).toHaveBeenCalledTimes(2)
    const pending = deferred<StatusPage>()
    api.page.mockReturnValueOnce(pending.promise)
    await vi.advanceTimersByTimeAsync(30000)
    expect(api.page).toHaveBeenCalledTimes(3)
    await vi.advanceTimersByTimeAsync(90000)
    expect(api.page).toHaveBeenCalledTimes(3)
    pending.resolve(page())
    await flush()
    await vi.advanceTimersByTimeAsync(30000)
    expect(api.page).toHaveBeenCalledTimes(4)
  })
  it('bounds failed idle reads and allows explicit recovery', async () => {
    api.page.mockRejectedValue(new Error('offline'))
    await vi.advanceTimersByTimeAsync(90000)
    expect(api.page).toHaveBeenCalledTimes(4)
    expect(controls.paused.value).toBe(true)
    expect(vi.getTimerCount()).toBe(0)
    api.page.mockResolvedValue(page())
    await controls.refresh()
    expect(controls.paused.value).toBe(false)
    expect(vi.getTimerCount()).toBe(1)
  })
  it('disposes in-flight aggregate reads and ignores late completion', async () => {
    const pending = deferred<StatusPage>()
    api.page.mockReturnValueOnce(pending.promise)
    const reading = controls.refresh()
    const signal = api.page.mock.calls.at(-1)![1] as AbortSignal
    scope.stop()
    expect(signal.aborted).toBe(true)
    pending.resolve(page('RUNNING', '2'))
    await reading
    expect(controls.rows.value[0]?.cameraId).toBe('1')
    await controls.refresh()
    expect(api.page).toHaveBeenCalledTimes(2)
    expect(vi.getTimerCount()).toBe(0)
  })
})
