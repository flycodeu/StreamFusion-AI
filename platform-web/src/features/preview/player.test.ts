import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import type { PreviewLease } from '../../api/preview/types'
import * as api from '../../api/preview/api'
import { ApiRequestError } from '../../lib/http/error'
import { createPreviewPlayer, offerExcludesH265, supportedVideoCodecs } from './player'

vi.mock('../../api/preview/api', async (original) => ({
  ...(await original<typeof api>()),
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
  static offerSdp = 'offer'
  static instances: MockPeer[] = []
  constructor() {
    MockPeer.instances.push(this)
  }
  connectionState = 'new'
  iceGatheringState = 'complete'
  localDescription: { sdp: string } | null = null
  ontrack: unknown
  onconnectionstatechange: unknown
  addTransceiver = vi.fn()
  createOffer = vi.fn().mockImplementation(async () => ({ type: 'offer', sdp: MockPeer.offerSdp }))
  setLocalDescription = vi.fn().mockImplementation((offer) => {
    this.localDescription = offer
  })
  setRemoteDescription = vi.fn().mockResolvedValue(undefined)
  close = vi.fn()
}
const events = () => ({ stream: vi.fn(), lease: vi.fn(), state: vi.fn(), failed: vi.fn() })
beforeEach(() => {
  vi.useFakeTimers()
  MockPeer.offerSdp = 'offer'
  MockPeer.instances = []
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
  it('preserves diagnostics from HTTP success with an unavailable lease without starting WebRTC', async () => {
    vi.mocked(api.createPreviewLease).mockResolvedValueOnce({
      ...lease,
      leaseId: null,
      state: 'UNAVAILABLE',
      reasonCode: 'SOURCE_UNAVAILABLE',
      receipt: { status: 201, traceId: 'a'.repeat(32), timestamp: '2026-10-17T02:00:00Z' },
    })
    await expect(
      createPreviewPlayer(events()).connect({ cameraId: '1', profileId: '2', usage: 'MAIN' }),
    ).rejects.toMatchObject({
      code: 'SOURCE_UNAVAILABLE',
      traceId: 'a'.repeat(32),
      timestamp: '2026-10-17T02:00:00Z',
      status: 201,
    })
    expect(MockPeer.instances).toHaveLength(0)
    expect(api.createPreviewLease).toHaveBeenCalledOnce()
    expect(api.deletePreviewLease).not.toHaveBeenCalled()
  })
  it('keeps unavailable renewal receipt diagnostics and closes the original lease', async () => {
    const callbacks = events(),
      player = createPreviewPlayer(callbacks)
    await player.connect({ cameraId: '1', profileId: '2', usage: 'MAIN' })
    const peer = MockPeer.instances[0]!
    peer.connectionState = 'connected'
    ;(peer.onconnectionstatechange as () => void)()
    vi.mocked(api.renewPreviewLease).mockResolvedValueOnce({
      ...lease,
      state: 'UNAVAILABLE',
      reasonCode: 'SOURCE_UNAVAILABLE',
      receipt: { status: 200, traceId: 'b'.repeat(32), timestamp: '2026-10-17T02:01:00Z' },
    })
    await vi.advanceTimersByTimeAsync(30000)
    expect(callbacks.failed).toHaveBeenCalledWith(
      'LEASE_EXPIRED',
      expect.objectContaining({
        code: 'SOURCE_UNAVAILABLE',
        traceId: 'b'.repeat(32),
        timestamp: '2026-10-17T02:01:00Z',
      }),
    )
    expect(api.deletePreviewLease).toHaveBeenCalledWith('lease-1')
  })
  it('advertises only codecs actually available in the browser', async () => {
    expect(supportedVideoCodecs()).toEqual(['H264'])
    const player = createPreviewPlayer(events())
    await player.connect({ cameraId: '1', profileId: '2', usage: 'SUB' })
    expect(api.createPreviewLease).toHaveBeenCalledWith({
      clientRequestId: expect.any(String),
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
    const closing = player.close()
    resolve(lease)
    await closing
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
    const failure = new ApiRequestError('REQUEST_TIMEOUT', '续租超时', {
      traceId: 'abcdef0123456789abcdef0123456789',
    })
    vi.mocked(api.renewPreviewLease).mockRejectedValue(failure)
    await vi.advanceTimersByTimeAsync(30000)
    expect(api.renewPreviewLease).toHaveBeenCalledWith('lease-1')
    expect(callbacks.failed).toHaveBeenCalledWith('LEASE_EXPIRED', failure)
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
  it('closes a failed signaling lease without guessing that the codec is incompatible', async () => {
    vi.mocked(api.createPreviewLease).mockResolvedValue({ ...lease, videoCodec: 'H265' })
    vi.mocked(api.exchangeWebRtc).mockRejectedValueOnce(new Error('SIGNAL_REJECTED'))
    await expect(
      createPreviewPlayer(events()).connect({ cameraId: '1', profileId: '2', usage: 'MAIN' }),
    ).rejects.toThrow('SIGNAL_REJECTED')
    expect(api.deletePreviewLease).toHaveBeenCalledWith('lease-1')
    expect(api.createPreviewLease).toHaveBeenCalledOnce()
    expect(MockPeer.instances[0]!.close).toHaveBeenCalledOnce()
  })
  it('publishes the allocated lease before signaling so failed setup can be diagnosed', async () => {
    const callbacks = events()
    vi.mocked(api.exchangeWebRtc).mockImplementationOnce(async () => {
      expect(callbacks.lease).toHaveBeenCalledWith(lease)
      throw new ApiRequestError('SIGNAL_REJECTED', '信令失败')
    })
    await expect(
      createPreviewPlayer(callbacks).connect({ cameraId: '1', profileId: '2', usage: 'MAIN' }),
    ).rejects.toThrow('信令失败')
    expect(api.deletePreviewLease).toHaveBeenCalledWith('lease-1')
  })
  it('releases H265 direct output before one H264 retry when the actual offer excludes H265', async () => {
    MockPeer.offerSdp = videoOffer()
    vi.mocked(api.createPreviewLease)
      .mockResolvedValueOnce({ ...lease, videoCodec: 'H265' })
      .mockResolvedValueOnce({ ...lease, leaseId: 'lease-2', transcoded: true })
    const player = createPreviewPlayer(events())
    expect((await player.connect({ cameraId: '1', profileId: '2', usage: 'MAIN' })).leaseId).toBe(
      'lease-2',
    )
    expect(api.createPreviewLease).toHaveBeenNthCalledWith(
      2,
      expect.objectContaining({ videoCodecs: ['H264'] }),
    )
    const firstKey = vi.mocked(api.createPreviewLease).mock.calls[0]![0].clientRequestId
    expect(vi.mocked(api.createPreviewLease).mock.calls[1]![0].clientRequestId).not.toBe(firstKey)
    expect(api.deletePreviewLease).toHaveBeenCalledWith('lease-1')
    expect(vi.mocked(api.deletePreviewLease).mock.invocationCallOrder[0]).toBeLessThan(
      vi.mocked(api.createPreviewLease).mock.invocationCallOrder[1]!,
    )
    expect(api.exchangeWebRtc).toHaveBeenCalledOnce()
    await player.close()
  })
  it('allows one H264 rebuild on an explicit H265 media decoder error, not missing frames', async () => {
    vi.mocked(api.createPreviewLease)
      .mockResolvedValueOnce({ ...lease, videoCodec: 'H265' })
      .mockResolvedValueOnce({ ...lease, leaseId: 'lease-2', transcoded: true })
    const player = createPreviewPlayer(events())
    await player.connect({ cameraId: '1', profileId: '2', usage: 'MAIN' })
    expect(await player.recoverVideoError(2)).toBeNull()
    expect(api.createPreviewLease).toHaveBeenCalledOnce()
    expect((await player.recoverVideoError(3))?.leaseId).toBe('lease-2')
    expect(await player.recoverVideoError(3)).toBeNull()
    expect(api.createPreviewLease).toHaveBeenCalledTimes(2)
    await player.close()
  })
  it.each([
    { ...lease, videoCodec: 'H264' },
    { ...lease, videoCodec: 'H265', transcoded: true },
  ])(
    'does not apply H265 recovery to an ineligible lease $videoCodec/$transcoded',
    async (input) => {
      vi.mocked(api.createPreviewLease).mockResolvedValue(input)
      const player = createPreviewPlayer(events())
      await player.connect({ cameraId: '1', profileId: '2', usage: 'MAIN' })
      expect(await player.recoverVideoError(3)).toBeNull()
      expect(api.createPreviewLease).toHaveBeenCalledOnce()
      await player.close()
    },
  )
  it('does not rebuild after ICE failure or its connection timeout', async () => {
    vi.mocked(api.createPreviewLease).mockResolvedValue({ ...lease, videoCodec: 'H265' })
    const callbacks = events(),
      player = createPreviewPlayer(callbacks)
    await player.connect({ cameraId: '1', profileId: '2', usage: 'MAIN' })
    await vi.advanceTimersByTimeAsync(20000)
    expect(callbacks.failed).toHaveBeenCalledWith('CONNECT_TIMEOUT')
    expect(api.createPreviewLease).toHaveBeenCalledOnce()
    expect(api.deletePreviewLease).toHaveBeenCalledWith('lease-1')
  })
  it('retains a failed release for retry and never opens a second pull without confirmation', async () => {
    vi.mocked(api.createPreviewLease).mockResolvedValue({ ...lease, videoCodec: 'H265' })
    const player = createPreviewPlayer(events())
    await player.connect({ cameraId: '1', profileId: '2', usage: 'MAIN' })
    vi.mocked(api.deletePreviewLease).mockRejectedValue(new Error('network'))
    await expect(player.recoverVideoError(3)).rejects.toThrow('LEASE_RELEASE_FAILED')
    expect(api.createPreviewLease).toHaveBeenCalledOnce()
    vi.mocked(api.deletePreviewLease).mockResolvedValue(undefined)
    await player.close()
    expect(api.deletePreviewLease).toHaveBeenLastCalledWith('lease-1')
  })
  it('releases a late H264 lease when the slot closes during recovery', async () => {
    vi.mocked(api.createPreviewLease).mockResolvedValueOnce({ ...lease, videoCodec: 'H265' })
    const player = createPreviewPlayer(events())
    await player.connect({ cameraId: '1', profileId: '2', usage: 'MAIN' })
    let resolve!: (lease: PreviewLease) => void
    vi.mocked(api.createPreviewLease).mockReturnValueOnce(
      new Promise((ready) => {
        resolve = ready
      }),
    )
    const pending = player.recoverVideoError(3)
    const rejected = expect(pending).rejects.toThrow('CANCELLED')
    await vi.waitUntil(() => vi.mocked(api.createPreviewLease).mock.calls.length === 2)
    const closing = player.close()
    resolve({ ...lease, leaseId: 'lease-2', transcoded: true })
    await closing
    await rejected
    expect(api.deletePreviewLease).toHaveBeenCalledWith('lease-2')
    expect(api.exchangeWebRtc).toHaveBeenCalledOnce()
  })
  it('deduplicates decoder recovery and waits for confirmed deletion before allocating', async () => {
    vi.mocked(api.createPreviewLease)
      .mockResolvedValueOnce({ ...lease, videoCodec: 'H265' })
      .mockResolvedValueOnce({ ...lease, leaseId: 'lease-2', transcoded: true })
    const player = createPreviewPlayer(events())
    await player.connect({ cameraId: '1', profileId: '2', usage: 'MAIN' })
    let confirm!: () => void
    vi.mocked(api.deletePreviewLease).mockReturnValueOnce(
      new Promise((resolve) => {
        confirm = resolve
      }),
    )
    const first = player.recoverVideoError(3),
      second = player.recoverVideoError(3)
    await Promise.resolve()
    expect(api.deletePreviewLease).toHaveBeenCalledOnce()
    expect(api.createPreviewLease).toHaveBeenCalledOnce()
    confirm()
    expect((await first)?.leaseId).toBe('lease-2')
    expect((await second)?.leaseId).toBe('lease-2')
    expect(api.createPreviewLease).toHaveBeenCalledTimes(2)
    await player.close()
  })
  it('does not infer incompatibility from a generic remote description failure', async () => {
    vi.mocked(api.createPreviewLease).mockResolvedValue({ ...lease, videoCodec: 'H265' })
    class RejectingPeer extends MockPeer {
      override setRemoteDescription = vi
        .fn()
        .mockRejectedValue(new DOMException('SDP failed', 'OperationError'))
    }
    vi.stubGlobal('RTCPeerConnection', RejectingPeer)
    await expect(
      createPreviewPlayer(events()).connect({ cameraId: '1', profileId: '2', usage: 'MAIN' }),
    ).rejects.toThrow('SDP failed')
    expect(api.createPreviewLease).toHaveBeenCalledOnce()
    expect(api.deletePreviewLease).toHaveBeenCalledWith('lease-1')
  })
  it('does not let an in-flight old renewal overwrite the replacement lease', async () => {
    vi.mocked(api.createPreviewLease)
      .mockResolvedValueOnce({ ...lease, videoCodec: 'H265' })
      .mockResolvedValueOnce({ ...lease, leaseId: 'lease-2', transcoded: true })
    const player = createPreviewPlayer(events())
    await player.connect({ cameraId: '1', profileId: '2', usage: 'MAIN' })
    const peer = MockPeer.instances[0]!
    peer.connectionState = 'connected'
    ;(peer.onconnectionstatechange as () => void)()
    let resolve!: (lease: PreviewLease) => void
    vi.mocked(api.renewPreviewLease).mockReturnValueOnce(
      new Promise((ready) => {
        resolve = ready
      }),
    )
    await vi.advanceTimersByTimeAsync(30000)
    await player.recoverVideoError(3)
    resolve({ ...lease, videoCodec: 'H265' })
    await Promise.resolve()
    await player.stats()
    expect(api.getPreviewStats).toHaveBeenCalledWith('lease-2')
    await player.close()
  })
  it('does not confirm closing until a pending creation and its late deletion complete', async () => {
    let ready!: (lease: PreviewLease) => void, deleted!: () => void
    vi.mocked(api.createPreviewLease).mockReturnValueOnce(
      new Promise((resolve) => {
        ready = resolve
      }),
    )
    vi.mocked(api.deletePreviewLease).mockReturnValueOnce(
      new Promise((resolve) => {
        deleted = resolve
      }),
    )
    const player = createPreviewPlayer(events())
    const connecting = player.connect({ cameraId: '1', profileId: '2', usage: 'MAIN' })
    const rejected = expect(connecting).rejects.toThrow('CANCELLED')
    let confirmed = false
    const closing = player.close().then(() => {
      confirmed = true
    })
    await Promise.resolve()
    expect(confirmed).toBe(false)
    ready(lease)
    await vi.waitUntil(() => vi.mocked(api.deletePreviewLease).mock.calls.length === 1)
    expect(confirmed).toBe(false)
    deleted()
    await closing
    await rejected
    expect(confirmed).toBe(true)
    expect(api.deletePreviewLease).toHaveBeenCalledOnce()
  })
  it('rejects closing if a lease arrives late and cannot be deleted', async () => {
    let ready!: (lease: PreviewLease) => void
    vi.mocked(api.createPreviewLease).mockReturnValueOnce(
      new Promise((resolve) => {
        ready = resolve
      }),
    )
    vi.mocked(api.deletePreviewLease).mockRejectedValue(new Error('network'))
    const player = createPreviewPlayer(events())
    const connecting = player.connect({ cameraId: '1', profileId: '2', usage: 'MAIN' })
    const rejected = expect(connecting).rejects.toThrow('LEASE_RELEASE_FAILED')
    const closing = player.close()
    const closeRejected = expect(closing).rejects.toThrow('LEASE_RELEASE_FAILED')
    ready(lease)
    await closeRejected
    await rejected
    expect(api.createPreviewLease).toHaveBeenCalledOnce()
    vi.mocked(api.deletePreviewLease).mockResolvedValue(undefined)
    await player.close()
    expect(api.deletePreviewLease).toHaveBeenLastCalledWith('lease-1')
  })
  it.each([
    new ApiRequestError('NETWORK_ERROR', 'lost'),
    new ApiRequestError('REQUEST_TIMEOUT', 'lost'),
    new ApiRequestError('INVALID_RESPONSE', 'lost', { status: 201 }),
  ])('blocks replacement after both creation receipts remain unknown: $code', async (cause) => {
    vi.mocked(api.createPreviewLease).mockRejectedValueOnce(cause)
    const player = createPreviewPlayer(events())
    await expect(player.connect({ cameraId: '1', profileId: '2', usage: 'MAIN' })).rejects.toThrow(
      'LEASE_RELEASE_FAILED',
    )
    await expect(player.close()).rejects.toThrow('LEASE_RELEASE_FAILED')
    expect(api.deletePreviewLease).not.toHaveBeenCalled()
    expect(api.createPreviewLease).toHaveBeenCalledOnce()
  })
  it.each([
    new ApiRequestError('FORBIDDEN', 'rejected', { status: 403 }),
    new ApiRequestError('RATE_LIMITED', 'rejected', { status: 429 }),
    new ApiRequestError('DEPENDENCY_UNAVAILABLE', 'rejected', { status: 503 }),
    new ApiRequestError('NETWORK_ERROR', 'rejected', { status: 401 }),
  ])('does not treat an explicit creation rejection as unknown resources: $code', async (cause) => {
    vi.mocked(api.createPreviewLease).mockRejectedValueOnce(cause)
    const player = createPreviewPlayer(events())
    await expect(player.connect({ cameraId: '1', profileId: '2', usage: 'MAIN' })).rejects.toBe(
      cause,
    )
    await expect(player.close()).resolves.toBeUndefined()
    expect(api.deletePreviewLease).not.toHaveBeenCalled()
  })
})

function videoOffer(extra = '') {
  return `v=0\r\nm=audio 9 UDP/TLS/RTP/SAVPF 111\r\na=rtpmap:111 opus/48000/2\r\nm=video 9 UDP/TLS/RTP/SAVPF 96 97\r\na=recvonly\r\na=rtpmap:96 H264/90000\r\na=rtpmap:97 rtx/90000\r\n${extra}`
}
describe('conservative video negotiation evidence', () => {
  it('identifies only active video H264 offers, ignoring audio codecs and repair payloads', () => {
    expect(offerExcludesH265(videoOffer())).toBe(true)
    expect(offerExcludesH265(videoOffer().replace('opus/48000/2', 'H265/90000'))).toBe(true)
  })
  it.each([
    'offer',
    videoOffer().replace('video 9', 'video 0'),
    videoOffer().replace('a=recvonly', 'a=inactive'),
    videoOffer().replace('a=recvonly', 'a=sendonly'),
    videoOffer().replace('UDP/TLS/RTP/SAVPF 96', 'UDP/DTLS/SCTP 96'),
    videoOffer().replace('a=rtpmap:97 rtx/90000', ''),
    videoOffer().replace('a=rtpmap:97 rtx/90000', 'a=rtpmap:97 H265/90000'),
    videoOffer().replace('a=rtpmap:97 rtx/90000', 'a=rtpmap:97 HEVC/90000'),
    videoOffer('m=video 9 UDP/TLS/RTP/SAVPF 98\r\na=rtpmap:98 H265/90000\r\n'),
    videoOffer().replace('v=0', 'v=0\r\na=inactive').replace('a=recvonly\r\n', ''),
    videoOffer().replace('v=0', 'v=0\r\na=sendonly').replace('a=recvonly\r\n', ''),
  ])(
    'does not infer a codec failure from rejected, inactive, incomplete or ambiguous SDP',
    (offer) => {
      expect(offerExcludesH265(offer)).toBe(false)
    },
  )
})
