import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import type { PreviewLease } from '../../api/preview/types'
import * as api from '../../api/preview/api'
import { createPreviewPlayer, supportedVideoCodecs } from './player'

vi.mock('../../api/preview/api', () => ({
  createPreviewLease: vi.fn(),
  exchangeWebRtc: vi.fn(),
  renewPreviewLease: vi.fn(),
  deletePreviewLease: vi.fn(),
  getPreviewStats: vi.fn(),
}))
const lease: PreviewLease = {
  leaseId: 'lease-1',
  cameraId: '1',
  streamProfileId: '2',
  state: 'READY',
  reasonCode: null,
  expiresAt: '2030-01-01T00:00:00Z',
  videoCodec: 'H264',
  transcoded: false,
}
class MockPeer {
  connectionState = 'new'
  iceGatheringState = 'complete'
  localDescription: { sdp: string } | null = null
  ontrack: unknown
  onconnectionstatechange: unknown
  addTransceiver = vi.fn()
  createOffer = vi.fn().mockResolvedValue({ type: 'offer', sdp: 'offer' })
  setLocalDescription = vi.fn().mockImplementation((offer) => {
    this.localDescription = offer
  })
  setRemoteDescription = vi.fn().mockResolvedValue(undefined)
  close = vi.fn()
}
const events = () => ({ stream: vi.fn(), state: vi.fn(), failed: vi.fn() })
beforeEach(() => {
  vi.useFakeTimers()
  vi.mocked(api.createPreviewLease).mockResolvedValue(lease)
  vi.mocked(api.exchangeWebRtc).mockResolvedValue({ type: 'answer', sdp: 'answer' })
  vi.mocked(api.renewPreviewLease).mockResolvedValue(lease)
  vi.mocked(api.deletePreviewLease).mockResolvedValue(undefined)
  vi.stubGlobal('RTCPeerConnection', MockPeer)
  vi.stubGlobal('RTCRtpReceiver', {
    getCapabilities: () => ({ codecs: [{ mimeType: 'video/H264' }] }),
  })
  vi.stubGlobal(
    'MediaStream',
    class {
      getTracks() {
        return []
      }
      addTrack() {}
    },
  )
})
afterEach(() => {
  vi.clearAllMocks()
  vi.unstubAllGlobals()
  vi.useRealTimers()
})
describe('native WebRTC preview adapter', () => {
  it('advertises only codecs actually available in the browser', async () => {
    expect(supportedVideoCodecs()).toEqual(['H264'])
    const player = createPreviewPlayer(events())
    await player.connect({ cameraId: '1', profileId: '2', usage: 'SUB' })
    expect(api.createPreviewLease).toHaveBeenCalledWith({
      cameraId: '1',
      streamProfileId: '2',
      usage: 'SUB',
      videoCodecs: ['H264'],
    })
    await player.close()
  })
  it('releases a lease which arrives after the user closes its slot', async () => {
    let resolve!: (lease: PreviewLease) => void
    vi.mocked(api.createPreviewLease).mockReturnValue(
      new Promise((ready) => {
        resolve = ready
      }),
    )
    const player = createPreviewPlayer(events())
    const pending = player.connect({ cameraId: '1', profileId: '2', usage: 'MAIN' })
    const rejected = expect(pending).rejects.toThrow('CANCELLED')
    await player.close()
    resolve(lease)
    await rejected
    expect(api.deletePreviewLease).toHaveBeenCalledWith('lease-1')
    expect(api.exchangeWebRtc).not.toHaveBeenCalled()
  })
  it('renews independent of bitrate display and releases on renewal failure', async () => {
    const callbacks = events(),
      player = createPreviewPlayer(callbacks)
    await player.connect({ cameraId: '1', profileId: '2', usage: 'MAIN' })
    // A connected peer avoids the separate connection timeout.
    const peer = (player as unknown as { peer: MockPeer }).peer
    peer.connectionState = 'connected'
    ;(peer.onconnectionstatechange as () => void)()
    vi.mocked(api.renewPreviewLease).mockRejectedValue(new Error('unavailable'))
    await vi.advanceTimersByTimeAsync(30000)
    expect(api.renewPreviewLease).toHaveBeenCalledWith('lease-1')
    expect(callbacks.failed).toHaveBeenCalledWith('LEASE_EXPIRED')
    expect(api.deletePreviewLease).toHaveBeenCalledWith('lease-1')
  })
  it('does not register a pull when the browser has no supported codec', async () => {
    vi.stubGlobal('RTCRtpReceiver', {
      getCapabilities: () => ({ codecs: [{ mimeType: 'video/VP8' }] }),
    })
    await expect(
      createPreviewPlayer(events()).connect({ cameraId: '1', profileId: '2', usage: 'MAIN' }),
    ).rejects.toThrow('BROWSER_UNSUPPORTED')
    expect(api.createPreviewLease).not.toHaveBeenCalled()
  })
})
