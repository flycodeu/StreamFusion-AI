import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import type { PreviewChannel, PreviewLease } from '../../api/preview/types'
import { ApiRequestError } from '../../lib/http/error'
import { createPreviewPlayer } from './player'
import type { PlayerEvents, PreviewPlayer } from './player'
import { usePreviewSlots } from './usePreviewSlots'

const hooks = vi.hoisted(() => ({ cleanup: [] as (() => void)[] }))
vi.mock('vue', async (original) => ({
  ...(await original<typeof import('vue')>()),
  onBeforeUnmount: (callback: () => void) => hooks.cleanup.push(callback),
}))
vi.mock('./player', () => ({ createPreviewPlayer: vi.fn() }))
const channel = (cameraId: string): PreviewChannel => ({
  cameraId,
  name: `通道${cameraId}`,
  deviceGroupKey: 'd1',
  deviceName: '相机',
  groupId: null,
  groupPath: null,
  lifecycle: 'ENABLED',
  profiles: [
    {
      streamProfileId: cameraId,
      label: 'Main',
      usage: 'MAIN',
      enabled: true,
      configured: true,
      videoCodec: 'H264',
      width: null,
      height: null,
      frameRate: null,
      bitrateKbps: null,
    },
  ],
})
const lease: PreviewLease = {
  cameraId: '1',
  streamProfileId: '1',
  leaseId: 'lease-1',
  state: 'READY',
  reasonCode: null,
  expiresAt: null,
  videoCodec: 'H264',
  transcoded: false,
}
let players: PreviewPlayer[], callbacks: PlayerEvents[]
beforeEach(() => {
  vi.useFakeTimers()
  players = []
  callbacks = []
  vi.stubGlobal('HTMLVideoElement', class extends EventTarget {})
  vi.stubGlobal('HTMLElement', class {})
  vi.mocked(createPreviewPlayer).mockImplementation((events) => {
    const player = {
      connect: vi.fn().mockResolvedValue(lease),
      recoverVideoError: vi.fn().mockResolvedValue(null),
      close: vi.fn().mockResolvedValue(undefined),
      stats: vi.fn().mockResolvedValue(null),
    }
    players.push(player)
    callbacks.push(events)
    return player
  })
})
afterEach(() => {
  for (const callback of hooks.cleanup.splice(0)) callback()
  vi.clearAllMocks()
  vi.unstubAllGlobals()
  vi.useRealTimers()
})
describe('preview slot lifetime and first frame', () => {
  it('keeps the playing stream attached across renders and repeated track events', async () => {
    const preview = usePreviewSlots(),
      video = new HTMLVideoElement(),
      stream = {} as MediaStream
    let attached: MediaStream | null = null
    const attach = vi.fn((value: MediaStream | null) => {
      attached = value
    })
    Object.defineProperty(video, 'srcObject', { get: () => attached, set: attach })
    preview.setVideoRef(0, video)
    await preview.assign(0, channel('1'), 'MAIN')
    callbacks[0]?.stream(stream)
    attach.mockClear()
    preview.setVideoRef(0, null)
    preview.setVideoRef(0, video)
    callbacks[0]?.stream(stream)
    await preview.sampleStats()
    preview.setVideoRef(0, video)
    expect(attach).not.toHaveBeenCalled()
    expect(video.srcObject).toBe(stream)
    await preview.stop(0)
    expect(video.srcObject).toBeNull()
  })
  it('releases hidden leases when shrinking eight by eight to four by four', async () => {
    const preview = usePreviewSlots()
    expect(preview.slots).toHaveLength(64)
    await preview.assign(0, channel('1'), 'MAIN')
    await preview.assign(63, channel('64'), 'MAIN')
    await preview.trim('16')
    expect(preview.slots[0]!.channel?.cameraId).toBe('1')
    expect(preview.slots[63]!.state).toBe('EMPTY')
    expect(preview.slots[63]!.lease).toBeNull()
    expect(players[1]?.close).toHaveBeenCalled()
  })
  it('does not consider a successful media lease to be the first video frame', async () => {
    const preview = usePreviewSlots()
    await preview.assign(0, channel('1'), 'MAIN')
    expect(preview.slots[0]!.state).toBe('CONNECTING')
    await vi.advanceTimersByTimeAsync(20000)
    expect(preview.slots[0]!.state).toBe('ERROR')
    expect(preview.slots[0]!.error?.message).toContain('视频画面')
    expect(preview.slots[0]!.error).toMatchObject({
      code: 'FIRST_FRAME_TIMEOUT',
      leaseId: 'lease-1',
      cameraId: '1',
    })
    expect(players[0]?.close).toHaveBeenCalled()
  })
  it('acknowledges a browser video frame and removes its frame timeout', async () => {
    const preview = usePreviewSlots()
    await preview.assign(0, channel('1'), 'MAIN')
    callbacks[0]?.stream({} as MediaStream)
    preview.firstFrame(0)
    expect(preview.slots[0]!.state).toBe('PLAYING')
    await vi.advanceTimersByTimeAsync(20000)
    expect(preview.slots[0]!.state).toBe('PLAYING')
  })
  it('ignores a stale assignment after an older player takes longer to close', async () => {
    const preview = usePreviewSlots()
    await preview.assign(0, channel('1'), 'MAIN')
    let release!: () => void
    vi.mocked(players[0]!.close).mockReturnValueOnce(
      new Promise<void>((resolve) => {
        release = resolve
      }),
    )
    const stale = preview.assign(0, channel('2'), 'MAIN')
    await preview.assign(0, channel('3'), 'MAIN')
    release()
    await stale
    expect(preview.slots[0]!.channel?.cameraId).toBe('3')
    expect(players).toHaveLength(2)
    callbacks[0]?.failed('CONNECTION_FAILED')
    expect(preview.slots[0]!.state).toBe('CONNECTING')
  })
  it('does not let a late connection response resurrect a cleared slot', async () => {
    let resolve!: (lease: PreviewLease) => void
    vi.mocked(createPreviewPlayer).mockImplementationOnce((events) => {
      const player = {
        connect: vi.fn().mockReturnValue(
          new Promise<PreviewLease>((ready) => {
            resolve = ready
          }),
        ),
        close: vi.fn().mockResolvedValue(undefined),
        recoverVideoError: vi.fn().mockResolvedValue(null),
        stats: vi.fn().mockResolvedValue(null),
      }
      players.push(player)
      callbacks.push(events)
      return player
    })
    const preview = usePreviewSlots(),
      pending = preview.assign(0, channel('1'), 'MAIN')
    await vi.waitUntil(() => players.length === 1)
    await preview.stop(0)
    resolve(lease)
    await pending
    expect(preview.slots[0]!.state).toBe('EMPTY')
    expect(preview.slots[0]!.lease).toBeNull()
    callbacks[0]?.stream({} as MediaStream)
    preview.firstFrame(0)
    expect(preview.slots[0]!.state).toBe('EMPTY')
  })
  it('keeps release failure visible and blocks switching until closing is confirmed', async () => {
    const preview = usePreviewSlots()
    await preview.assign(0, channel('1'), 'MAIN')
    vi.mocked(players[0]!.close).mockRejectedValueOnce(new Error('unconfirmed'))
    await preview.assign(0, channel('2'), 'MAIN')
    expect(preview.slots[0]!.state).toBe('ERROR')
    expect(preview.slots[0]!.error?.message).toContain('释放未确认')
    expect(preview.slots[0]!.channel?.cameraId).toBe('1')
    expect(players).toHaveLength(1)
    await preview.assign(0, channel('2'), 'MAIN')
    expect(preview.slots[0]!.channel?.cameraId).toBe('2')
    expect(players).toHaveLength(2)
  })
  it('does not let a queued playing event overwrite a failed connection', async () => {
    const preview = usePreviewSlots()
    await preview.assign(0, channel('1'), 'MAIN')
    callbacks[0]!.stream({} as MediaStream)
    callbacks[0]!.failed('LEASE_EXPIRED')
    preview.firstFrame(0)
    expect(preview.slots[0]!.state).toBe('ERROR')
  })
  it('routes a current decoder error through bounded recovery and replaces its lease', async () => {
    const preview = usePreviewSlots(),
      video = new HTMLVideoElement()
    Object.defineProperty(video, 'error', { value: { code: 3 } })
    preview.setVideoRef(0, video)
    await preview.assign(0, channel('1'), 'MAIN')
    callbacks[0]!.stream({} as MediaStream)
    preview.firstFrame(0)
    vi.mocked(players[0]!.recoverVideoError).mockImplementationOnce(async () => {
      callbacks[0]!.state('CONNECTING')
      callbacks[0]!.stream({} as MediaStream)
      return { ...lease, leaseId: 'lease-2', transcoded: true }
    })
    video.dispatchEvent(new Event('error'))
    await vi.waitUntil(() => preview.slots[0]!.lease?.leaseId === 'lease-2')
    expect(players[0]!.recoverVideoError).toHaveBeenCalledWith(3)
    expect(preview.slots[0]!.state).toBe('CONNECTING')
    preview.firstFrame(0)
    await vi.advanceTimersByTimeAsync(20000)
    expect(preview.slots[0]!.state).toBe('PLAYING')
  })
  it('detaches an old video error listener and does not resurrect cleared recovery', async () => {
    const preview = usePreviewSlots(),
      video = new HTMLVideoElement()
    Object.defineProperty(video, 'error', { value: { code: 3 } })
    preview.setVideoRef(0, video)
    await preview.assign(0, channel('1'), 'MAIN')
    callbacks[0]!.stream({} as MediaStream)
    preview.setVideoRef(0, null)
    video.dispatchEvent(new Event('error'))
    expect(players[0]!.recoverVideoError).not.toHaveBeenCalled()
    preview.setVideoRef(0, video)
    let resolve!: (lease: PreviewLease) => void
    vi.mocked(players[0]!.recoverVideoError).mockReturnValueOnce(
      new Promise((ready) => {
        resolve = ready
      }),
    )
    video.dispatchEvent(new Event('error'))
    await preview.stop(0)
    resolve({ ...lease, leaseId: 'lease-2' })
    await Promise.resolve()
    expect(preview.slots[0]!.state).toBe('EMPTY')
    expect(preview.slots[0]!.lease).toBeNull()
  })
  it('does not ask the player to transcode on a first frame timeout', async () => {
    const preview = usePreviewSlots()
    await preview.assign(0, channel('1'), 'MAIN')
    await vi.advanceTimersByTimeAsync(20000)
    expect(players[0]!.recoverVideoError).not.toHaveBeenCalled()
    expect(players[0]!.close).toHaveBeenCalledOnce()
  })
  it('keeps the old profile label when a requested profile switch cannot release it', async () => {
    const preview = usePreviewSlots(),
      camera = channel('1')
    camera.profiles.push({ ...camera.profiles[0]!, streamProfileId: '2', usage: 'SUB' })
    await preview.assign(0, camera, 'MAIN')
    vi.mocked(players[0]!.close).mockRejectedValueOnce(new Error('unconfirmed'))
    await preview.switchProfile(0, '2')
    expect(preview.slots[0]!.profile?.streamProfileId).toBe('1')
    expect(preview.slots[0]!.usage).toBe('MAIN')
    expect(preview.slots[0]!.state).toBe('ERROR')
    expect(players).toHaveLength(1)
  })
  it('refuses to hide a lease whose release was not confirmed', async () => {
    const preview = usePreviewSlots()
    await preview.assign(63, channel('64'), 'MAIN')
    vi.mocked(players[0]!.close).mockRejectedValueOnce(new Error('unconfirmed'))
    expect(await preview.trim('16')).toBe(false)
    expect(preview.slots[63]!.state).toBe('ERROR')
    expect(preview.slots[63]!.channel?.cameraId).toBe('64')
    expect(await preview.trim('16')).toBe(true)
    expect(preview.slots[63]!.state).toBe('EMPTY')
  })
  it('does not hide a newly assigned stream during an older layout cleanup', async () => {
    const preview = usePreviewSlots()
    await preview.assign(63, channel('64'), 'MAIN')
    let confirm!: () => void
    vi.mocked(players[0]!.close).mockReturnValueOnce(
      new Promise((resolve) => {
        confirm = resolve
      }),
    )
    const pending = preview.trim('16')
    await preview.assign(63, channel('65'), 'MAIN')
    confirm()
    expect(await pending).toBe(false)
    expect(preview.slots[63]!.channel?.cameraId).toBe('65')
  })

  it('keeps the allocated lease and signaling trace when connection fails', async () => {
    const request = new ApiRequestError('SIGNAL_REJECTED', '信令被拒绝', {
      traceId: 'abcdef0123456789abcdef0123456789',
      timestamp: '2026-10-17T02:00:00Z',
    })
    vi.mocked(createPreviewPlayer).mockImplementationOnce((events) => ({
      connect: vi.fn().mockImplementation(async () => {
        events.lease?.(lease)
        throw request
      }),
      close: vi.fn().mockResolvedValue(undefined),
      recoverVideoError: vi.fn().mockResolvedValue(null),
      stats: vi.fn().mockResolvedValue(null),
    }))
    const preview = usePreviewSlots()
    await preview.assign(0, channel('1'), 'MAIN')
    expect(preview.slots[0]!.error).toMatchObject({
      code: 'SIGNAL_REJECTED',
      leaseId: 'lease-1',
      cameraId: '1',
      streamProfileId: '1',
      traceId: request.traceId,
      timestamp: request.timestamp,
    })
  })

  it('retains renewal diagnostics but ignores lease and error callbacks from the previous assignment', async () => {
    const preview = usePreviewSlots()
    await preview.assign(0, channel('1'), 'MAIN')
    const oldCallbacks = callbacks[0]!
    callbacks[0]!.failed(
      'LEASE_EXPIRED',
      new ApiRequestError('FORBIDDEN', '权限已变化', {
        traceId: 'abcdef0123456789abcdef0123456789',
        timestamp: '2026-10-17T02:00:00Z',
      }),
    )
    expect(preview.slots[0]!.error).toMatchObject({
      code: 'LEASE_EXPIRED',
      requestCode: 'FORBIDDEN',
      leaseId: 'lease-1',
      traceId: 'abcdef0123456789abcdef0123456789',
    })
    await preview.assign(0, channel('2'), 'MAIN')
    oldCallbacks.lease?.({ ...lease, leaseId: 'stale-lease' })
    oldCallbacks.failed(
      'LEASE_RELEASE_FAILED',
      new ApiRequestError('REQUEST_TIMEOUT', '旧请求超时'),
    )
    expect(preview.slots[0]!.error).toBeNull()
    expect(preview.slots[0]!.lease?.leaseId).toBe('lease-1')
    expect(preview.slots[0]!.channel?.cameraId).toBe('2')
  })
})
