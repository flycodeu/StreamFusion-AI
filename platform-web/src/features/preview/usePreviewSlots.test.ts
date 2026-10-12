import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import type { PreviewChannel, PreviewLease } from '../../api/preview/types'
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
  vi.stubGlobal('HTMLVideoElement', class {})
  vi.stubGlobal('HTMLElement', class {})
  vi.mocked(createPreviewPlayer).mockImplementation((events) => {
    const player = {
      connect: vi.fn().mockResolvedValue(lease),
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
    expect(preview.slots[0]!.error).toContain('视频画面')
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
})
