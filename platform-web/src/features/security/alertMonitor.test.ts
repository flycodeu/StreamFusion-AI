import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { effectScope, nextTick } from 'vue'
import type { EffectScope } from 'vue'
import type { SecurityFeed } from '../../api/security/alerts'
import { sessionState } from '../../session/state'
import { useSecurityAlerts } from './alertMonitor'
import { securityAdmin, securityFeed } from '../../../tests/support/securityAlerts'

const getSecurityFeed = vi.hoisted(() => vi.fn())
vi.mock('../../api/security/alerts', () => ({ getSecurityFeed }))
let scope: EffectScope
let page: EventTarget & { visibilityState: string }
let win: EventTarget
let monitor: ReturnType<typeof useSecurityAlerts>
const notify = vi.fn()
beforeEach(() => {
  vi.useFakeTimers()
  getSecurityFeed.mockReset().mockResolvedValue(securityFeed(2))
  notify.mockReset()
  page = Object.assign(new EventTarget(), { visibilityState: 'visible' })
  win = new EventTarget()
  vi.stubGlobal('document', page)
  vi.stubGlobal('window', win)
  sessionState.me = securityAdmin()
  scope = effectScope()
})
afterEach(() => {
  scope.stop()
  sessionState.me = null
  sessionState.epoch++
  vi.useRealTimers()
  vi.unstubAllGlobals()
})
async function start() {
  scope.run(() => {
    monitor = useSecurityAlerts(notify)
  })
  await nextTick()
}

describe('security alert polling', () => {
  it('establishes a baseline, notifies only new alerts and coalesces a batch', async () => {
    await start()
    expect(notify).not.toHaveBeenCalled()
    getSecurityFeed.mockResolvedValue(securityFeed(5))
    await vi.advanceTimersByTimeAsync(20_000)
    expect(notify).toHaveBeenCalledExactlyOnceWith(3)
    await vi.advanceTimersByTimeAsync(40_000)
    expect(notify).toHaveBeenCalledTimes(1)
  })
  it('does not replay alerts after the runtime instance restarts', async () => {
    await start()
    getSecurityFeed.mockResolvedValue({
      ...securityFeed(8),
      instanceId: '33333333-3333-4333-a333-333333333333',
    })
    await vi.advanceTimersByTimeAsync(20_000)
    expect(notify).not.toHaveBeenCalled()
  })
  it('requires both super identity and audit module', async () => {
    sessionState.me!.modules = []
    await start()
    expect(getSecurityFeed).not.toHaveBeenCalled()
    sessionState.me = { ...securityAdmin(), isSuperAdmin: false }
    await nextTick()
    expect(getSecurityFeed).not.toHaveBeenCalled()
  })
  it('ignores late responses after logout or role revocation and aborts the request', async () => {
    let resolve!: (value: SecurityFeed) => void
    getSecurityFeed.mockReturnValue(
      new Promise<SecurityFeed>((done) => {
        resolve = done
      }),
    )
    await start()
    const signal = getSecurityFeed.mock.calls[0]![0] as AbortSignal
    sessionState.me!.isSuperAdmin = false
    await nextTick()
    expect(signal.aborted).toBe(true)
    resolve(securityFeed(100))
    await nextTick()
    expect(monitor.feed.value).toBeNull()
    expect(notify).not.toHaveBeenCalled()
  })
  it('stops while hidden, resumes on focus and never overlaps requests', async () => {
    await start()
    page.visibilityState = 'hidden'
    await vi.advanceTimersByTimeAsync(60_000)
    expect(getSecurityFeed).toHaveBeenCalledTimes(1)
    page.visibilityState = 'visible'
    getSecurityFeed.mockReturnValue(new Promise(() => {}))
    win.dispatchEvent(new Event('focus'))
    win.dispatchEvent(new Event('focus'))
    await vi.advanceTimersByTimeAsync(60_000)
    expect(getSecurityFeed).toHaveBeenCalledTimes(2)
  })
  it('clears timers/listeners on disposal and resets history on account switch', async () => {
    await start()
    sessionState.epoch++
    await nextTick()
    expect(getSecurityFeed).toHaveBeenCalledTimes(2)
    scope.stop()
    win.dispatchEvent(new Event('focus'))
    await vi.advanceTimersByTimeAsync(60_000)
    expect(getSecurityFeed).toHaveBeenCalledTimes(2)
  })
  it('exposes connection failure then recovers without notification spam', async () => {
    getSecurityFeed.mockRejectedValue(new Error('offline'))
    await start()
    expect(monitor.unavailable.value).toBe(true)
    getSecurityFeed.mockResolvedValue(securityFeed(4))
    await vi.advanceTimersByTimeAsync(20_000)
    expect(monitor.unavailable.value).toBe(false)
    expect(notify).not.toHaveBeenCalled()
  })
})
