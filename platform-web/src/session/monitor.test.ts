import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { effectScope, nextTick } from 'vue'
import type { EffectScope } from 'vue'
import { useSessionMonitor } from './monitor'
import { sessionState } from './state'
import type { AuthUser } from '../api/auth/types'

const request = vi.hoisted(() => vi.fn())
vi.mock('../api/client', () => ({ request }))
let scope: EffectScope
let page: EventTarget & { visibilityState: string }
let win: EventTarget
beforeEach(() => {
  vi.useFakeTimers()
  request.mockReset().mockResolvedValue(undefined)
  page = Object.assign(new EventTarget(), { visibilityState: 'visible' })
  win = new EventTarget()
  vi.stubGlobal('document', page)
  vi.stubGlobal('window', win)
  sessionState.me = { user: { id: '1' } } as AuthUser
  scope = effectScope()
  scope.run(useSessionMonitor)
})
afterEach(() => {
  scope.stop()
  sessionState.me = null
  vi.useRealTimers()
  vi.unstubAllGlobals()
})

describe('session monitor', () => {
  it('polls only while signed in and visible, then resumes on focus', async () => {
    await vi.advanceTimersByTimeAsync(15000)
    expect(request).toHaveBeenCalledTimes(1)
    page.visibilityState = 'hidden'
    await vi.advanceTimersByTimeAsync(30000)
    expect(request).toHaveBeenCalledTimes(1)
    page.visibilityState = 'visible'
    page.dispatchEvent(new Event('visibilitychange'))
    await nextTick()
    expect(request).toHaveBeenCalledTimes(2)
    sessionState.me = null
    await nextTick()
    win.dispatchEvent(new Event('focus'))
    await vi.advanceTimersByTimeAsync(30000)
    expect(request).toHaveBeenCalledTimes(2)
  })
  it('never overlaps requests and aborts the old identity request on cleanup', async () => {
    request.mockReturnValue(new Promise(() => {}))
    win.dispatchEvent(new Event('focus'))
    win.dispatchEvent(new Event('focus'))
    await vi.advanceTimersByTimeAsync(30000)
    expect(request).toHaveBeenCalledTimes(1)
    const signal = request.mock.calls[0]![0].signal as AbortSignal
    sessionState.epoch++
    await nextTick()
    expect(signal.aborted).toBe(true)
    scope.stop()
    win.dispatchEvent(new Event('focus'))
    await vi.advanceTimersByTimeAsync(30000)
    expect(request).toHaveBeenCalledTimes(1)
  })
})
