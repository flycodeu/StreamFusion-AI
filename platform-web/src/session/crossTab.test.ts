import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { effectScope } from 'vue'
import type { EffectScope } from 'vue'
import { notifyOtherTabs, useCrossTabSession } from './crossTab'

const channelName = 'sf-session-change-v1'
const change = {
  version: 1,
  sender: '11111111-1111-4111-8111-111111111111',
  id: '22222222-2222-4222-8222-222222222222',
}

class TestChannel {
  static instances: TestChannel[] = []
  onmessage: ((event: MessageEvent<unknown>) => void) | null = null
  constructor(readonly name: string) {
    TestChannel.instances.push(this)
  }
  postMessage = vi.fn((message: unknown) => {
    for (const target of TestChannel.instances) {
      if (target !== this) target.onmessage?.({ data: message } as MessageEvent<unknown>)
    }
  })
  close = vi.fn()
}

let scope: EffectScope
const storage = { setItem: vi.fn(), removeItem: vi.fn() }

function storageEvent(value: unknown): void {
  window.dispatchEvent(
    Object.assign(new Event('storage'), { key: channelName, newValue: JSON.stringify(value) }),
  )
}

beforeEach(() => {
  TestChannel.instances = []
  storage.setItem.mockReset()
  storage.removeItem.mockReset()
  vi.stubGlobal('window', new EventTarget())
  vi.stubGlobal('localStorage', storage)
  vi.stubGlobal('BroadcastChannel', TestChannel)
  scope = effectScope()
})
afterEach(() => {
  scope.stop()
  vi.unstubAllGlobals()
})

describe('shared-cookie session changes', () => {
  it('deduplicates channel and storage signals and ignores its own sign-in signal', () => {
    const refresh = vi.fn()
    scope.run(() => useCrossTabSession(refresh))
    TestChannel.instances[0]!.onmessage?.({ data: change } as MessageEvent<unknown>)
    storageEvent(change)
    expect(refresh).toHaveBeenCalledTimes(1)

    notifyOtherTabs()
    storageEvent(JSON.parse(storage.setItem.mock.calls[0]![1] as string))
    expect(refresh).toHaveBeenCalledTimes(1)
  })

  it('uses storage events when BroadcastChannel is unavailable', () => {
    vi.stubGlobal('BroadcastChannel', undefined)
    const refresh = vi.fn()
    scope.run(() => useCrossTabSession(refresh))
    storageEvent(change)
    expect(refresh).toHaveBeenCalledOnce()

    notifyOtherTabs()
    const message = JSON.parse(storage.setItem.mock.calls[0]![1] as string)
    expect(Object.keys(message).sort()).toEqual(['id', 'sender', 'version'])
    expect(storage.removeItem).toHaveBeenCalledWith(channelName)
  })

  it('publishes without browser storage and closes its temporary sender channel', () => {
    storage.setItem.mockImplementation(() => {
      throw new Error('storage disabled')
    })
    expect(() => notifyOtherTabs()).not.toThrow()
    expect(TestChannel.instances[0]!.postMessage).toHaveBeenCalledOnce()
    expect(TestChannel.instances[0]!.close).toHaveBeenCalledOnce()
  })

  it('ignores malformed and oversized data and disposes both listeners', () => {
    const refresh = vi.fn()
    scope.run(() => useCrossTabSession(refresh))
    const channel = TestChannel.instances[0]!
    for (const value of [null, {}, { ...change, version: 2 }, { ...change, id: 'bad' }]) {
      channel.onmessage?.({ data: value } as MessageEvent<unknown>)
    }
    storageEvent({ ...change, unwanted: 'x'.repeat(600) })
    expect(refresh).not.toHaveBeenCalled()

    scope.stop()
    expect(channel.close).toHaveBeenCalledOnce()
    expect(channel.onmessage).toBeNull()
    storageEvent(change)
    expect(refresh).not.toHaveBeenCalled()
  })
})
