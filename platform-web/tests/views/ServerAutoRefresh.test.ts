// @vitest-environment vue-renderer
import { afterEach, describe, expect, it, vi } from 'vitest'
import { computed, ref, type App } from 'vue'
import { node, renderer } from '../support/renderer'
import { useServerAutoRefresh } from '../../src/features/server/useServerAutoRefresh'
import { usePageScope } from '../../src/composables/usePageScope'
import { clearIdentity } from '../../src/session/state'

let app: App | undefined
afterEach(() => {
  app?.unmount()
  app = undefined
  vi.useRealTimers()
  vi.unstubAllGlobals()
})
function mount(read = vi.fn().mockResolvedValue(true)) {
  vi.useFakeTimers()
  const document = Object.assign(new EventTarget(), { hidden: false })
  vi.stubGlobal('document', document)
  const host = ref(true),
    busy = ref(false)
  let refresh!: ReturnType<typeof useServerAutoRefresh>
  app = renderer.createApp({
    setup() {
      const inScope = usePageScope()()
      refresh = useServerAutoRefresh(
        read,
        computed(() => host.value && inScope()),
        busy,
      )
      return () => null
    },
  })
  app.mount(node('root'))
  return { read, document, host, busy, ...refresh }
}
describe('opt-in server refresh', () => {
  it('stays off until selected and uses the chosen interval', async () => {
    const view = mount()
    await vi.advanceTimersByTimeAsync(120000)
    expect(view.read).not.toHaveBeenCalled()
    view.interval.value = 60
    view.enabled.value = true
    await vi.advanceTimersByTimeAsync(59000)
    expect(view.remaining.value).toBe(1)
    expect(view.read).not.toHaveBeenCalled()
    await vi.advanceTimersByTimeAsync(1000)
    expect(view.read).toHaveBeenCalledTimes(1)
    view.enabled.value = false
    await vi.advanceTimersByTimeAsync(120000)
    expect(view.read).toHaveBeenCalledTimes(1)
  })
  it('pauses for hidden pages and other tabs, then disposes its timer', async () => {
    const view = mount()
    view.enabled.value = true
    view.document.hidden = true
    view.document.dispatchEvent(new Event('visibilitychange'))
    await vi.advanceTimersByTimeAsync(60000)
    expect(view.read).not.toHaveBeenCalled()
    view.document.hidden = false
    view.document.dispatchEvent(new Event('visibilitychange'))
    view.host.value = false
    await vi.advanceTimersByTimeAsync(60000)
    expect(view.read).not.toHaveBeenCalled()
    view.host.value = true
    await vi.advanceTimersByTimeAsync(30000)
    expect(view.read).toHaveBeenCalledTimes(1)
    app?.unmount()
    app = undefined
    expect(vi.getTimerCount()).toBe(0)
  })
  it('does not overlap manual or pending automatic reads', async () => {
    let complete!: (result: boolean) => void
    const read = vi.fn(
      () =>
        new Promise<boolean>((resolve) => {
          complete = resolve
        }),
    )
    const view = mount(read)
    view.enabled.value = true
    view.busy.value = true
    await vi.advanceTimersByTimeAsync(90000)
    expect(read).not.toHaveBeenCalled()
    view.busy.value = false
    await vi.advanceTimersByTimeAsync(90000)
    expect(read).toHaveBeenCalledTimes(1)
    // Toggling while a request is pending must not start a second request.
    view.enabled.value = false
    view.enabled.value = true
    await vi.advanceTimersByTimeAsync(90000)
    expect(read).toHaveBeenCalledTimes(1)
    complete(true)
    await vi.advanceTimersByTimeAsync(30000)
    expect(read).toHaveBeenCalledTimes(2)
  })
  it.each([false, new Error('unavailable')])(
    'stops on a failed read without retry loops: %s',
    async (result) => {
      const read =
        result instanceof Error
          ? vi.fn().mockRejectedValue(result)
          : vi.fn().mockResolvedValue(result)
      const view = mount(read)
      view.enabled.value = true
      await vi.advanceTimersByTimeAsync(180000)
      expect(read).toHaveBeenCalledTimes(1)
      expect(view.enabled.value).toBe(false)
      expect(view.paused.value).toBe(true)
      view.enabled.value = true
      expect(view.paused.value).toBe(false)
    },
  )
  it('stops reads after the signed-in account changes', async () => {
    const view = mount()
    view.enabled.value = true
    clearIdentity()
    await vi.advanceTimersByTimeAsync(60000)
    expect(view.read).not.toHaveBeenCalled()
  })
  it('does not restart after disposal during a pending request', async () => {
    let complete!: (result: boolean) => void
    const view = mount(
      vi.fn(
        () =>
          new Promise<boolean>((resolve) => {
            complete = resolve
          }),
      ),
    )
    view.enabled.value = true
    await vi.advanceTimersByTimeAsync(30000)
    app?.unmount()
    app = undefined
    complete(true)
    await vi.advanceTimersByTimeAsync(60000)
    expect(view.read).toHaveBeenCalledTimes(1)
    expect(vi.getTimerCount()).toBe(0)
  })
})
