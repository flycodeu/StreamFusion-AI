import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { defineComponent, h, nextTick, onBeforeMount } from 'vue'
import type { Ref } from 'vue'
import type { AccessAdapter, AccessOptions } from '../../api/camera-access/types'
import { clearIdentity } from '../../session/state'
import { node, renderer } from '../../../tests/support/renderer'
import { useAccessAdapters, useDeviceAdapters } from './useDeviceAdapters'

const api = vi.hoisted(() => ({ options: vi.fn() }))
vi.mock('../../api/camera-access/api', () => ({ getAccessOptions: api.options }))
const device: AccessAdapter = {
  type: 'NEW_DEVICE',
  label: '新设备',
  category: 'DEVICE',
  inputKind: 'DEVICE_LOGIN',
  autoDetect: true,
  paged: false,
}
const platform: AccessAdapter = {
  ...device,
  type: 'NEW_PLATFORM',
  category: 'PLATFORM',
  inputKind: 'PLATFORM_APPKEY',
}
const options = (adapters = [device, platform]): AccessOptions => ({
  ready: true,
  methods: [],
  adapters,
  diagnostics: [],
  networkPolicies: [],
})
function deferred() {
  let resolve!: (value: AccessOptions) => void
  const promise = new Promise<AccessOptions>((ready) => {
    resolve = ready
  })
  return { promise, resolve }
}
const dispose: (() => void)[] = []
function mount(onlyDevices = false, beforeMount?: () => void) {
  let adapters!: Ref<AccessAdapter[]>
  const app = renderer.createApp(
    defineComponent({
      setup() {
        adapters = onlyDevices ? useDeviceAdapters() : useAccessAdapters()
        if (beforeMount) onBeforeMount(beforeMount)
        return () => h('div')
      },
    }),
  )
  app.mount(node('root'))
  dispose.push(() => app.unmount())
  return adapters
}
async function flush() {
  for (let i = 0; i < 8; i++) await Promise.resolve()
  await nextTick()
}
beforeEach(() => {
  clearIdentity()
  vi.resetAllMocks()
  vi.useFakeTimers()
})
afterEach(() => {
  dispose.splice(0).forEach((unmount) => unmount())
  vi.useRealTimers()
})

describe('session-bounded adapter metadata', () => {
  it('shares concurrent loads while preserving device-only consumers', async () => {
    const request = deferred()
    api.options.mockReturnValue(request.promise)
    const all = mount(),
      devices = mount(true)
    expect(api.options).toHaveBeenCalledTimes(1)
    request.resolve(options())
    await flush()
    expect(all.value).toEqual([device, platform])
    expect(devices.value).toEqual([device])
  })
  it('refreshes after one minute instead of retaining outdated server labels', async () => {
    api.options.mockResolvedValue(options())
    mount()
    await flush()
    vi.advanceTimersByTime(59_999)
    expect(mount().value).toEqual([device, platform])
    await flush()
    expect(api.options).toHaveBeenCalledTimes(1)
    vi.advanceTimersByTime(1)
    const changed = { ...device, label: '更新设备名称' }
    api.options.mockResolvedValue(options([changed]))
    const refreshed = mount()
    await flush()
    expect(refreshed.value).toEqual([changed])
    expect(api.options).toHaveBeenCalledTimes(2)
  })
  it('ignores old-session responses without discarding the new pending request', async () => {
    const oldRequest = deferred(),
      newRequest = deferred()
    api.options.mockReturnValueOnce(oldRequest.promise).mockReturnValueOnce(newRequest.promise)
    const previous = mount()
    clearIdentity()
    const current = mount()
    oldRequest.resolve(options())
    await flush()
    const another = mount()
    expect(api.options).toHaveBeenCalledTimes(2)
    expect(previous.value).toEqual([])
    newRequest.resolve(options([device]))
    await flush()
    expect(current.value).toEqual([device])
    expect(another.value).toEqual([device])
  })
  it('uses the mounted session even when identity changes after setup', async () => {
    api.options.mockResolvedValue(options())
    const current = mount(false, clearIdentity)
    await flush()
    expect(current.value).toEqual([device, platform])
    mount()
    await flush()
    expect(api.options).toHaveBeenCalledTimes(1)
  })
  it('does not update an unmounted component', async () => {
    const request = deferred()
    api.options.mockReturnValue(request.promise)
    const closed = mount()
    dispose.pop()!()
    request.resolve(options())
    await flush()
    expect(closed.value).toEqual([])
  })
  it('allows retry after an optional metadata request fails', async () => {
    api.options.mockRejectedValueOnce(new Error('offline')).mockResolvedValueOnce(options())
    expect(mount().value).toEqual([])
    await flush()
    const retried = mount()
    await flush()
    expect(retried.value).toEqual([device, platform])
    expect(api.options).toHaveBeenCalledTimes(2)
  })
})
