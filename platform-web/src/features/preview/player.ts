import * as api from '../../api/preview/api'
import type { PreviewLease, PreviewStats } from '../../api/preview/types'
import type { StreamUsage } from './model'

type PreviewInput = { cameraId: string; profileId: string; usage: StreamUsage }
export interface PreviewPlayer {
  connect(input: PreviewInput): Promise<PreviewLease>
  recoverVideoError(code: number): Promise<PreviewLease | null>
  close(): Promise<void>
  stats(): Promise<PreviewStats | null>
}
export interface PlayerEvents {
  stream(stream: MediaStream): void
  state(state: 'CONNECTING'): void
  failed(code: string): void
}
export function supportedVideoCodecs(): string[] {
  if (typeof RTCRtpReceiver === 'undefined') return []
  const codecs = RTCRtpReceiver.getCapabilities?.('video')?.codecs || []
  const mimeTypes = new Set(codecs.map((codec) => codec.mimeType.toUpperCase()))
  return [
    mimeTypes.has('VIDEO/H264') ? 'H264' : null,
    mimeTypes.has('VIDEO/H265') || mimeTypes.has('VIDEO/HEVC') ? 'H265' : null,
  ].filter((name): name is string => name !== null)
}

// Unknown or multi-video SDP stays on the normal error path.
export function offerExcludesH265(sdp: string): boolean {
  const sections = sdp.split(/\r?\nm=/).map((section, index) => (index ? `m=${section}` : section))
  const videos = sections.filter((section) => section.startsWith('m=video '))
  if (videos.length !== 1) return false
  const sessionLines = sections[0]?.split(/\r?\n/) || []
  const sessionCannotReceive = sessionLines.some((line) =>
    ['a=inactive', 'a=sendonly'].includes(line),
  )
  const lines = videos[0]!.split(/\r?\n/),
    media = lines[0]!.trim().split(/\s+/)
  if (
    media[1] === '0' ||
    !/^\d+$/.test(media[1] || '') ||
    !/RTP\/SAVPF$/.test(media[2] || '') ||
    lines.includes('a=inactive') ||
    lines.includes('a=sendonly') ||
    (sessionCannotReceive && !lines.some((line) => ['a=recvonly', 'a=sendrecv'].includes(line)))
  )
    return false
  const payloads = media.slice(3)
  if (!payloads.length || payloads.some((payload) => !/^\d+$/.test(payload))) return false
  const codecs = new Map<string, string>()
  for (const line of lines) {
    const mapping = /^a=rtpmap:(\d+) ([A-Za-z0-9-]+)\/\d+(?:\/\d+)?$/.exec(line)
    if (mapping) codecs.set(mapping[1]!, mapping[2]!.toUpperCase())
  }
  if (payloads.some((payload) => !codecs.has(payload))) return false
  const names = payloads.map((payload) => codecs.get(payload))
  return names.includes('H264') && !names.includes('H265') && !names.includes('HEVC')
}

function gatherIce(peer: RTCPeerConnection): Promise<void> {
  if (peer.iceGatheringState === 'complete') return Promise.resolve()
  return new Promise((resolve) => {
    const finish = () => {
      clearTimeout(timeout)
      peer.removeEventListener('icegatheringstatechange', check)
      resolve()
    }
    const check = () => {
      if (peer.iceGatheringState === 'complete') finish()
    }
    const timeout = setTimeout(finish, 3000)
    peer.addEventListener('icegatheringstatechange', check)
  })
}
class WebRtcPreviewPlayer implements PreviewPlayer {
  private peer: RTCPeerConnection | null = null
  private lease: PreviewLease | null = null
  private input: PreviewInput | null = null
  private codecs: string[] = []
  private closed = false
  private fallbackUsed = false
  private creationPending: Promise<PreviewLease> | null = null
  private unknownCreation = false
  private releasePending: Promise<void> | null = null
  private recoveryPending: Promise<PreviewLease> | null = null
  private renewTimer: ReturnType<typeof setTimeout> | undefined
  private connectTimer: ReturnType<typeof setTimeout> | undefined
  constructor(private readonly events: PlayerEvents) {}

  async connect(input: PreviewInput): Promise<PreviewLease> {
    this.codecs = supportedVideoCodecs()
    if (!this.codecs.length || typeof RTCPeerConnection === 'undefined')
      throw new Error('BROWSER_UNSUPPORTED')
    this.input = input
    try {
      return await this.open(this.codecs)
    } catch (cause) {
      if (cause instanceof Error && cause.message === 'VIDEO_CODEC_NEGOTIATION_FAILED') {
        try {
          if (this.canFallback()) return await this.recoverH264()
        } catch (fallbackFailure) {
          await this.close()
          throw fallbackFailure
        }
      }
      await this.close()
      throw cause
    }
  }
  private async open(videoCodecs: string[]): Promise<PreviewLease> {
    if (this.closed || !this.input) throw new Error('CANCELLED')
    this.events.state('CONNECTING')
    const creation = api.createPreviewLease({
      clientRequestId: crypto.randomUUID(),
      cameraId: this.input.cameraId,
      streamProfileId: this.input.profileId,
      usage: this.input.usage,
      videoCodecs,
    })
    this.creationPending = creation
    let lease: PreviewLease
    try {
      lease = await creation
    } catch (cause) {
      this.unknownCreation = api.isUnknownPreviewReceipt(cause)
      throw cause
    } finally {
      if (this.creationPending === creation) this.creationPending = null
    }
    this.lease = lease
    if (this.closed) {
      await this.releaseLease()
      throw new Error('CANCELLED')
    }
    if (lease.state !== 'READY' || !lease.leaseId)
      throw new Error(lease.reasonCode || 'MEDIA_NOT_READY')
    const peer = new RTCPeerConnection()
    this.peer = peer
    peer.addTransceiver('video', { direction: 'recvonly' })
    peer.addTransceiver('audio', { direction: 'recvonly' })
    const stream = new MediaStream()
    peer.ontrack = (event) => {
      if (this.closed || this.peer !== peer) return
      if (!stream.getTracks().some((track) => track.id === event.track.id))
        stream.addTrack(event.track)
      this.events.stream(stream)
    }
    peer.onconnectionstatechange = () => {
      if (this.closed || this.peer !== peer) return
      if (peer.connectionState === 'connected') clearTimeout(this.connectTimer)
      if (peer.connectionState === 'failed' || peer.connectionState === 'disconnected')
        this.fail('CONNECTION_FAILED')
    }
    const offer = await peer.createOffer()
    if (this.closed) throw new Error('CANCELLED')
    if (this.canFallback() && offerExcludesH265(offer.sdp || ''))
      throw new Error('VIDEO_CODEC_NEGOTIATION_FAILED')
    await peer.setLocalDescription(offer)
    await gatherIce(peer)
    if (this.closed) throw new Error('CANCELLED')
    const answer = await api.exchangeWebRtc(
      lease.leaseId,
      peer.localDescription?.sdp || offer.sdp || '',
    )
    if (this.closed) throw new Error('CANCELLED')
    await peer.setRemoteDescription(answer)
    if (this.closed) throw new Error('CANCELLED')
    this.connectTimer = setTimeout(() => {
      if (!this.closed && this.peer === peer && peer.connectionState !== 'connected')
        this.fail('CONNECT_TIMEOUT')
    }, 20000)
    this.scheduleRenew()
    return lease
  }
  private canFallback(): boolean {
    return (
      !this.closed &&
      !this.fallbackUsed &&
      this.codecs.includes('H264') &&
      !this.lease?.transcoded &&
      ['H265', 'HEVC'].includes(this.lease?.videoCodec?.toUpperCase() || '')
    )
  }
  async recoverVideoError(code: number): Promise<PreviewLease | null> {
    if (code !== 3) return null
    if (this.recoveryPending) return this.recoveryPending
    if (!this.canFallback()) return null
    this.recoveryPending = this.recoverH264()
    try {
      return await this.recoveryPending
    } catch (cause) {
      await this.close()
      throw cause
    } finally {
      this.recoveryPending = null
    }
  }
  private async recoverH264(): Promise<PreviewLease> {
    this.fallbackUsed = true
    this.disconnect()
    await this.releaseLease()
    return this.open(['H264'])
  }
  private scheduleRenew(): void {
    const expires = Date.parse(this.lease?.expiresAt || '')
    const delay = Number.isFinite(expires)
      ? Math.max(1000, Math.min(30000, expires - Date.now() - 10000))
      : 30000
    this.renewTimer = setTimeout(async () => {
      if (this.closed || !this.lease?.leaseId) return
      const lease = this.lease
      try {
        const result = await api.renewPreviewLease(lease.leaseId!)
        if (this.closed || this.lease !== lease) return
        if (result.state !== 'READY') throw new Error(result.reasonCode || 'LEASE_EXPIRED')
        this.lease = result
        this.scheduleRenew()
      } catch {
        if (!this.closed && this.lease === lease) this.fail('LEASE_EXPIRED')
      }
    }, delay)
  }
  private fail(code: string): void {
    this.events.failed(code)
    void this.close().catch(() => this.events.failed('LEASE_RELEASE_FAILED'))
  }
  async stats(): Promise<PreviewStats | null> {
    const lease = this.lease
    if (this.closed || !lease?.leaseId) return null
    const stats = await api.getPreviewStats(lease.leaseId)
    return this.closed || this.lease !== lease ? null : stats
  }
  private disconnect(): void {
    clearTimeout(this.renewTimer)
    clearTimeout(this.connectTimer)
    if (this.peer) {
      this.peer.ontrack = null
      this.peer.onconnectionstatechange = null
      this.peer.close()
      this.peer = null
    }
  }
  async close(): Promise<void> {
    this.closed = true
    this.disconnect()
    const creation = this.creationPending
    if (creation) {
      try {
        await creation
      } catch (cause) {
        this.unknownCreation = api.isUnknownPreviewReceipt(cause)
      }
    }
    if (this.unknownCreation) throw new Error('LEASE_RELEASE_FAILED')
    await this.releaseLease()
  }
  private async releaseLease(): Promise<void> {
    if (this.releasePending) return this.releasePending
    const lease = this.lease
    if (!lease?.leaseId) return
    this.releasePending = api.deletePreviewLease(lease.leaseId).catch(() => {
      throw new Error('LEASE_RELEASE_FAILED')
    })
    try {
      await this.releasePending
      if (this.lease === lease) this.lease = null
    } finally {
      this.releasePending = null
    }
  }
}
export function createPreviewPlayer(events: PlayerEvents): PreviewPlayer {
  return new WebRtcPreviewPlayer(events)
}
