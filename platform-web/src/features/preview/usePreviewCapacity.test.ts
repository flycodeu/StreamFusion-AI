import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { effectScope, ref } from 'vue'
import type { EffectScope } from 'vue'
import { usePreviewCapacity } from './usePreviewCapacity'
import { clearIdentity, sessionState } from '../../session/state'
import type { AuthUser } from '../../api/auth/types'
function signIn(id = '1') {
  sessionState.me = { user: { id }, modules: ['camera'] } as AuthUser
}
const api = vi.hoisted(() => ({ read: vi.fn() }))
vi.mock('../../api/preview/api', () => ({ getPreviewCapacity: api.read }))
const sample = {
  maxSessions: 36,
  occupiedSessions: 12,
  closingSessions: 2,
  maxTranscodes: 2,
  occupiedTranscodes: 1,
  transcodesAvailable: true,
  observedAt: '2026-10-16T01:00:00Z',
}
let scope: EffectScope
let doc: EventTarget & { hidden: boolean }
const settle = async () => {
  await Promise.resolve()
  await Promise.resolve()
}
function setup(active = ref(true)) {
  scope = effectScope()
  return scope.run(() => usePreviewCapacity(active))!
}
beforeEach(() => {
  vi.useFakeTimers()
  api.read.mockReset().mockResolvedValue(sample)
  signIn()
  doc = Object.assign(new EventTarget(), { hidden: false })
  vi.stubGlobal('document', doc)
})
afterEach(() => {
  scope?.stop()
  clearIdentity()
  vi.unstubAllGlobals()
  vi.useRealTimers()
})
describe('bounded preview capacity polling', () => {
  it('reads only when active and polls serially after completion', async () => {
    const active = ref(false),
      model = setup(active)
    expect(api.read).not.toHaveBeenCalled()
    active.value = true
    await settle()
    expect(model.capacity.value).toEqual(sample)
    await vi.advanceTimersByTimeAsync(10000)
    expect(api.read).toHaveBeenCalledTimes(2)
    active.value = false
    expect(model.capacity.value).toBeNull()
    await vi.advanceTimersByTimeAsync(30000)
    expect(api.read).toHaveBeenCalledTimes(2)
  })
  it('aborts hidden reads, ignores late receipts and resumes visibly', async () => {
    let deliver!: (value: typeof sample) => void
    api.read.mockImplementationOnce(
      () =>
        new Promise((resolve) => {
          deliver = resolve
        }),
    )
    const model = setup()
    const signal = api.read.mock.calls[0]![0] as AbortSignal
    doc.hidden = true
    doc.dispatchEvent(new Event('visibilitychange'))
    expect(signal.aborted).toBe(true)
    deliver(sample)
    await settle()
    expect(model.capacity.value).toBeNull()
    doc.hidden = false
    doc.dispatchEvent(new Event('visibilitychange'))
    await settle()
    expect(model.capacity.value).toEqual(sample)
  })
  it('never overlaps a slow read or repeats manual refresh while loading', async () => {
    api.read.mockReturnValue(new Promise(() => {}))
    const model = setup()
    await model.refresh()
    await vi.advanceTimersByTimeAsync(60000)
    expect(api.read).toHaveBeenCalledOnce()
  })
  it('clears stale usage on error and pauses after three failures until manual refresh', async () => {
    const model = setup()
    await settle()
    api.read.mockRejectedValue(new Error('offline'))
    await vi.advanceTimersByTimeAsync(30000)
    expect(model.capacity.value).toBeNull()
    expect(model.paused.value).toBe(true)
    expect(api.read).toHaveBeenCalledTimes(4)
    await vi.advanceTimersByTimeAsync(60000)
    expect(api.read).toHaveBeenCalledTimes(4)
    api.read.mockResolvedValue(sample)
    await model.refresh()
    expect(model.capacity.value).toEqual(sample)
    expect(model.paused.value).toBe(false)
  })
  it('invalidates a previous session receipt and stops after disposal', async () => {
    let deliver!: (value: typeof sample) => void
    api.read.mockImplementationOnce(
      () =>
        new Promise((resolve) => {
          deliver = resolve
        }),
    )
    const model = setup()
    const signal = api.read.mock.calls[0]![0] as AbortSignal
    clearIdentity()
    await settle()
    expect(signal.aborted).toBe(true)
    expect(api.read).toHaveBeenCalledOnce()
    deliver({ ...sample, occupiedSessions: 35 })
    await settle()
    expect(model.capacity.value).toBeNull()
    signIn('2')
    await settle()
    expect(model.capacity.value?.occupiedSessions).toBe(12)
    scope.stop()
    await vi.advanceTimersByTimeAsync(30000)
    expect(api.read).toHaveBeenCalledTimes(2)
    expect(model.capacity.value).toBeNull()
  })
  it('does not start an anonymous read when a 401 handler clears identity', async () => {
    api.read.mockImplementationOnce(async () => {
      clearIdentity()
      throw new Error('401')
    })
    const model = setup()
    await settle()
    await vi.advanceTimersByTimeAsync(60000)
    expect(api.read).toHaveBeenCalledOnce()
    expect(model.capacity.value).toBeNull()
    await model.refresh()
    expect(api.read).toHaveBeenCalledOnce()
  })
})
