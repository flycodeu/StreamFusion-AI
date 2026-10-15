// @vitest-environment vue-renderer
import { afterEach, describe, expect, it, vi } from 'vitest'
import type { App } from 'vue'
import { renderer, node } from '../support/renderer'
import { useDeviceRefresh } from '../../src/features/camera-status/useDeviceRefresh'

let app: App | undefined
afterEach(() => {
  app?.unmount()
  app = undefined
  vi.useRealTimers()
  vi.unstubAllGlobals()
})
function mount(read: () => Promise<void>) {
  vi.useFakeTimers()
  const document = Object.assign(new EventTarget(), { hidden: false })
  vi.stubGlobal('document', document)
  let reset = () => {}
  app = renderer.createApp({
    setup() {
      reset = useDeviceRefresh(read)
      return () => null
    },
  })
  app.mount(node('root'))
  return { document, reset: () => reset() }
}
describe('bounded management summary refresh', () => {
  it('stops after three failures and explicit refresh resets the budget', async () => {
    const read = vi.fn().mockRejectedValue(new Error('unavailable'))
    const view = mount(read)
    await vi.advanceTimersByTimeAsync(180000)
    expect(read).toHaveBeenCalledTimes(3)
    view.reset()
    await vi.advanceTimersByTimeAsync(30000)
    expect(read).toHaveBeenCalledTimes(4)
  })
  it('pauses when hidden and clears timers on disposal', async () => {
    const read = vi.fn().mockResolvedValue(undefined)
    const view = mount(read)
    view.document.hidden = true
    view.document.dispatchEvent(new Event('visibilitychange'))
    await vi.advanceTimersByTimeAsync(90000)
    expect(read).not.toHaveBeenCalled()
    view.document.hidden = false
    view.document.dispatchEvent(new Event('visibilitychange'))
    await vi.advanceTimersByTimeAsync(30000)
    expect(read).toHaveBeenCalledTimes(1)
    app?.unmount()
    app = undefined
    await vi.advanceTimersByTimeAsync(90000)
    expect(read).toHaveBeenCalledTimes(1)
  })
  it('does not overlap a slow read', async () => {
    let complete!: () => void
    const read = vi.fn(
      () =>
        new Promise<void>((resolve) => {
          complete = resolve
        }),
    )
    mount(read)
    await vi.advanceTimersByTimeAsync(90000)
    expect(read).toHaveBeenCalledTimes(1)
    complete()
    await vi.advanceTimersByTimeAsync(30000)
    expect(read).toHaveBeenCalledTimes(2)
  })
})
