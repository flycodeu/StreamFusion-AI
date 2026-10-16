import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { effectScope, ref } from 'vue'
import type { EffectScope, Ref } from 'vue'
import type { CameraStatus, StatusCheck, StatusLayer } from '../../api/camera-status/types'
import { ApiRequestError } from '../../lib/http/error'
import { useStatusChecks } from './useStatusChecks'

const api = vi.hoisted(() => ({ status: vi.fn(), create: vi.fn(), check: vi.fn() }))
const hooks = vi.hoisted(() => ({
  activated: [] as (() => void)[],
  deactivated: [] as (() => void)[],
}))
vi.mock('../../api/camera-status/api', () => ({
  getCameraStatus: api.status,
  createStatusCheck: api.create,
  getStatusCheck: api.check,
}))
vi.mock('vue', async (original) => ({
  ...(await original<typeof import('vue')>()),
  getCurrentInstance: () => ({}),
  onActivated: (callback: () => void) => hooks.activated.push(callback),
  onDeactivated: (callback: () => void) => hooks.deactivated.push(callback),
}))
const now = '2026-10-13T01:00:00Z'
function check(
  state: StatusCheck['executionState'] = 'QUEUED',
  cameraId = '1',
  streamProfileId = '10',
): StatusCheck {
  return {
    checkId: '100',
    cameraId,
    streamProfileId,
    layer: 'CONTROL',
    probeKind: 'RTSP_DESCRIBE',
    executionState: state,
    requestedAt: now,
    startedAt: null,
    finishedAt: null,
    deadlineAt: '2026-10-13T01:01:00Z',
    observation: null,
    reasonCode: null,
    durationMs: null,
    traceId: 'a'.repeat(32),
    snapshotApplied: false,
  }
}
function status(cameraId = '1', latestCheck: StatusCheck | null = null): CameraStatus {
  return {
    cameraId,
    serverNow: now,
    profiles: ['10', '11'].map((streamProfileId) => ({
      streamProfileId,
      control: {
        layer: 'CONTROL',
        probeKind: 'RTSP_DESCRIBE',
        observation: null,
        freshness: 'NEVER',
        configurationCurrent: true,
        latestCheck: latestCheck?.streamProfileId === streamProfileId ? latestCheck : null,
        canCheck: true,
        blockingReasonCode: null,
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
    })),
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
let scope: EffectScope, controls: ReturnType<typeof useStatusChecks>
let cameraId: Ref<string | null>, profileId: Ref<string | null>
let active: Ref<boolean>, layer: Ref<StatusLayer>
let page: EventTarget & { hidden: boolean }
beforeEach(async () => {
  vi.useFakeTimers({ toFake: ['setTimeout', 'clearTimeout', 'Date', 'performance'] })
  vi.setSystemTime(new Date(now))
  page = Object.assign(new EventTarget(), { hidden: false })
  vi.stubGlobal('document', page)
  api.status.mockReset().mockImplementation(async (id: string) => status(id))
  api.create.mockReset().mockResolvedValue(check())
  api.check.mockReset().mockResolvedValue(check('RUNNING'))
  cameraId = ref('1')
  profileId = ref('10')
  active = ref(true)
  layer = ref('CONTROL')
  scope = effectScope()
  controls = scope.run(() => useStatusChecks(cameraId, profileId, active, layer))!
  await flush()
})
afterEach(() => {
  scope.stop()
  hooks.activated.splice(0)
  hooks.deactivated.splice(0)
  vi.unstubAllGlobals()
  vi.useRealTimers()
})

describe('manual CONTROL check read owner', () => {
  it('sends the selected layer and isolates unknown commands from the other layer', async () => {
    api.create.mockRejectedValueOnce(new ApiRequestError('REQUEST_TIMEOUT', '请求超时'))
    layer.value = 'MEDIA'
    await flush()
    await controls.submit()
    const original = api.create.mock.calls[0]![1]
    expect(original.layer).toBe('MEDIA')
    layer.value = 'CONTROL'
    await flush()
    expect(controls.pendingHere.value).toBe(false)
    await controls.submit()
    expect(api.create).toHaveBeenCalledOnce()
    layer.value = 'MEDIA'
    await flush()
    api.create.mockResolvedValue({ ...check(), layer: 'MEDIA', probeKind: 'ZLM_RTP_FRAMES' })
    await controls.submit()
    expect(api.create.mock.calls[1]![1]).toEqual(original)
    expect(controls.check.value?.layer).toBe('MEDIA')
  })
  it('ignores late CONTROL progress when the view switches to MEDIA', async () => {
    await controls.submit()
    const late = deferred<StatusCheck>()
    api.check.mockReturnValueOnce(late.promise)
    await vi.advanceTimersByTimeAsync(2000)
    const signal = api.check.mock.calls[0]![2] as AbortSignal
    layer.value = 'MEDIA'
    await flush()
    expect(signal.aborted).toBe(true)
    late.resolve(check('COMPLETED'))
    await flush()
    expect(controls.selected.value?.layer).toBe('MEDIA')
    expect(controls.check.value).toBeNull()
    expect(api.create).toHaveBeenCalledOnce()
  })
  it('keeps an unexecuted MEDIA receipt separate from a failed observation', async () => {
    const receipt: StatusCheck = {
      ...check('SKIPPED'),
      layer: 'MEDIA',
      probeKind: 'ZLM_RTP_FRAMES',
      reasonCode: 'MEDIA_BUSY',
    }
    const result = status()
    result.profiles[0]!.media.latestCheck = receipt
    api.status.mockResolvedValue(result)
    api.create.mockResolvedValue(receipt)
    layer.value = 'MEDIA'
    await flush()
    await controls.submit()
    expect(controls.check.value?.executionState).toBe('SKIPPED')
    expect(controls.selected.value?.observation).toBeNull()
    expect(controls.selected.value?.freshness).toBe('NEVER')
  })
  it('opens and selects a Profile with GET only, without assuming the default Profile', async () => {
    expect(api.status).toHaveBeenCalledOnce()
    profileId.value = '11'
    await flush()
    expect(api.create).not.toHaveBeenCalled()
    expect(controls.selectedProfile.value?.streamProfileId).toBe('11')
    profileId.value = null
    await flush()
    expect(controls.canSubmit.value).toBe(false)
  })
  it('obeys backend eligibility instead of inferring it from local asset fields', async () => {
    const blocked = status()
    blocked.profiles[0]!.control.canCheck = false
    blocked.profiles[0]!.control.blockingReasonCode = 'FORBIDDEN'
    api.status.mockResolvedValue(blocked)
    await controls.refresh()
    await controls.submit()
    expect(api.create).not.toHaveBeenCalled()
  })
  it('posts one frozen target, then serially reads progress without overlapping GETs', async () => {
    await controls.submit()
    const response = deferred<StatusCheck>()
    api.check.mockReturnValue(response.promise)
    await vi.advanceTimersByTimeAsync(2000)
    expect(api.check).toHaveBeenCalledOnce()
    await vi.advanceTimersByTimeAsync(8000)
    expect(api.check).toHaveBeenCalledOnce()
    response.resolve(check('RUNNING'))
    await flush()
    await vi.advanceTimersByTimeAsync(1999)
    expect(api.check).toHaveBeenCalledOnce()
    await vi.advanceTimersByTimeAsync(1)
    expect(api.check).toHaveBeenCalledTimes(2)
    expect(api.create).toHaveBeenCalledOnce()
    expect(api.create.mock.calls[0]![1]).toMatchObject({ streamProfileId: '10' })
  })
  it('stops at 30 progress reads, leaving manual GET refresh but no automatic new POST', async () => {
    await controls.submit()
    await vi.advanceTimersByTimeAsync(200000)
    expect(api.check).toHaveBeenCalledTimes(30)
    expect(controls.readPaused.value).toBe(true)
    expect(api.create).toHaveBeenCalledOnce()
    expect(vi.getTimerCount()).toBe(0)
  })
  it('reads Snapshot once after a terminal receipt and does not continue polling', async () => {
    api.check.mockResolvedValue(check('SKIPPED'))
    api.status.mockResolvedValue(status('1', check('SKIPPED')))
    await controls.submit()
    await vi.advanceTimersByTimeAsync(2000)
    expect(controls.check.value?.executionState).toBe('SKIPPED')
    expect(api.status).toHaveBeenCalledTimes(2)
    await vi.advanceTimersByTimeAsync(20000)
    expect(api.check).toHaveBeenCalledOnce()
  })
  it('pauses after three GET failures without overwriting the existing fact', async () => {
    api.check.mockRejectedValue(new ApiRequestError('NETWORK_ERROR', '网络请求失败'))
    await controls.submit()
    await vi.advanceTimersByTimeAsync(20000)
    expect(api.check).toHaveBeenCalledTimes(3)
    expect(controls.readPaused.value).toBe(true)
    expect(controls.snapshot.value?.cameraId).toBe('1')
  })
  it('keeps the original key after an unknown POST and retries only by explicit action', async () => {
    api.create.mockRejectedValueOnce(new ApiRequestError('REQUEST_TIMEOUT', '请求超时'))
    await controls.submit()
    const original = api.create.mock.calls[0]![1]
    expect(controls.pending.value?.uncertain).toBe(true)
    active.value = false
    active.value = true
    await flush()
    await vi.advanceTimersByTimeAsync(20000)
    expect(api.create).toHaveBeenCalledOnce()
    await controls.submit()
    expect(api.create.mock.calls[1]![1]).toEqual(original)
    expect(original.clientRequestId).toMatch(/^\d+-[a-f\d-]{36}$/)
    expect(controls.pending.value).toBeNull()
  })
  it('retains an uncertain command across channels and blocks new commands on another target', async () => {
    api.create.mockRejectedValueOnce(new ApiRequestError('NETWORK_ERROR', '网络请求失败'))
    await controls.submit()
    const original = api.create.mock.calls[0]![1]
    cameraId.value = '2'
    profileId.value = '11'
    await flush()
    await controls.submit()
    expect(api.create).toHaveBeenCalledOnce()
    expect(controls.canSubmit.value).toBe(false)
    expect(controls.pending.value?.cameraId).toBe('1')
    cameraId.value = '1'
    profileId.value = '10'
    await flush()
    await controls.submit()
    expect(api.create.mock.calls[1]![1]).toEqual(original)
  })
  it('keeps an unknown POST received while hidden and never replays it on restoration', async () => {
    let reject!: (cause: unknown) => void
    api.create.mockReturnValueOnce(
      new Promise<StatusCheck>((_resolve, fail) => {
        reject = fail
      }),
    )
    const posting = controls.submit()
    active.value = false
    reject(new ApiRequestError('REQUEST_TIMEOUT', '请求超时'))
    await posting
    expect(controls.pending.value?.uncertain).toBe(true)
    active.value = true
    await flush()
    expect(api.create).toHaveBeenCalledOnce()
    expect(controls.pendingHere.value).toBe(true)
  })
  it('reads a check acknowledged while inactive when restored, without another POST', async () => {
    const late = deferred<StatusCheck>()
    api.create.mockReturnValueOnce(late.promise)
    const posting = controls.submit()
    active.value = false
    late.resolve(check('RUNNING'))
    await posting
    expect(controls.pending.value).toBeNull()
    api.status.mockResolvedValue(status('1', check('RUNNING')))
    active.value = true
    await flush()
    expect(controls.check.value?.executionState).toBe('RUNNING')
    expect(api.create).toHaveBeenCalledOnce()
    await vi.advanceTimersByTimeAsync(2000)
    expect(api.check).toHaveBeenCalledOnce()
  })
  it('clears a definitive rejected POST without treating it as a camera fact', async () => {
    api.create.mockRejectedValue(new ApiRequestError('FORBIDDEN', '没有权限', { status: 403 }))
    await controls.submit()
    expect(controls.pending.value).toBeNull()
    expect(controls.error.value).toMatchObject({ code: 'FORBIDDEN' })
    expect(controls.selected.value?.observation).toBeNull()
    expect(api.check).not.toHaveBeenCalled()
  })
  it('aborts hidden-page GETs, ignores late responses, and restores with GET only', async () => {
    const late = deferred<CameraStatus>()
    api.status.mockReturnValueOnce(late.promise)
    const loading = controls.refresh()
    const signal = api.status.mock.calls.at(-1)![1] as AbortSignal
    page.hidden = true
    page.dispatchEvent(new Event('visibilitychange'))
    expect(signal.aborted).toBe(true)
    late.resolve(status('1', check('COMPLETED')))
    await loading
    expect(controls.check.value).toBeNull()
    page.hidden = false
    page.dispatchEvent(new Event('visibilitychange'))
    await flush()
    expect(api.status).toHaveBeenCalledTimes(3)
    expect(api.create).not.toHaveBeenCalled()
  })
  it.each(['inactive', 'deactivated'] as const)(
    'stops reads when %s and resumes only GET',
    async (kind) => {
      await controls.submit()
      if (kind === 'inactive') active.value = false
      else hooks.deactivated.forEach((callback) => callback())
      await vi.advanceTimersByTimeAsync(20000)
      expect(api.check).not.toHaveBeenCalled()
      if (kind === 'inactive') active.value = true
      else hooks.activated.forEach((callback) => callback())
      await flush()
      expect(api.status).toHaveBeenCalledTimes(2)
      expect(api.create).toHaveBeenCalledOnce()
    },
  )
  it('does not apply an older channel GET or POST to the new target', async () => {
    const lateGet = deferred<CameraStatus>(),
      latePost = deferred<StatusCheck>()
    api.create.mockReturnValueOnce(latePost.promise)
    const posting = controls.submit()
    api.status.mockReturnValueOnce(lateGet.promise)
    const reading = controls.refresh()
    cameraId.value = '2'
    profileId.value = '11'
    await flush()
    lateGet.resolve(status('1', check('COMPLETED')))
    latePost.resolve(check('RUNNING'))
    await Promise.all([posting, reading])
    expect(controls.snapshot.value?.cameraId).toBe('2')
    expect(controls.check.value).toBeNull()
    expect(controls.selectedProfile.value?.streamProfileId).toBe('11')
  })
  it('keeps the last result for an hour without probing or extending observation times', async () => {
    const result = status()
    result.profiles[0]!.control.freshness = 'CURRENT'
    result.profiles[0]!.control.observation = {
      checkId: '100',
      mediaEvidence: null,
      value: 'ACCESSIBLE',
      reasonCode: 'RTSP_ACCESSIBLE',
      observedAt: now,
      readAt: now,
      validUntil: '2026-10-13T01:00:05Z',
    }
    api.status.mockResolvedValue(result)
    await controls.refresh()
    expect(controls.selected.value?.freshness).toBe('CURRENT')
    await vi.advanceTimersByTimeAsync(3_600_001)
    expect(controls.selected.value?.freshness).toBe('CURRENT')
    expect(controls.selected.value?.observation?.observedAt).toBe(now)
    expect(api.status).toHaveBeenCalledTimes(2)
    expect(api.create).not.toHaveBeenCalled()
  })
  it('disposes timers and GET transport, ignoring late results without cancelling the server', async () => {
    await controls.submit()
    const late = deferred<StatusCheck>()
    api.check.mockReturnValueOnce(late.promise)
    await vi.advanceTimersByTimeAsync(2000)
    const signal = api.check.mock.calls[0]![2] as AbortSignal
    scope.stop()
    expect(signal.aborted).toBe(true)
    late.resolve(check('COMPLETED'))
    await flush()
    await controls.refresh()
    await controls.submit()
    expect(api.create).toHaveBeenCalledOnce()
    expect(vi.getTimerCount()).toBe(0)
  })
})
