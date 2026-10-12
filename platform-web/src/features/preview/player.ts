import * as api from '../../api/preview/api'
import type { PreviewLease, PreviewStats } from '../../api/preview/types'
import type { StreamUsage } from './model'

export interface PreviewPlayer {
  connect(input: { cameraId: string; profileId: string; usage: StreamUsage }): Promise<PreviewLease>
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
  private closed = false
  private renewTimer: ReturnType<typeof setTimeout> | undefined
  private connectTimer: ReturnType<typeof setTimeout> | undefined
  constructor(private readonly events: PlayerEvents) {}
  async connect(input: {
    cameraId: string
    profileId: string
    usage: StreamUsage
  }): Promise<PreviewLease> {
    const codecs = supportedVideoCodecs()
    if (!codecs.length || typeof RTCPeerConnection === 'undefined')
      throw new Error('BROWSER_UNSUPPORTED')
    this.events.state('CONNECTING')
    const lease = await api.createPreviewLease({
      cameraId: input.cameraId,
      streamProfileId: input.profileId,
      usage: input.usage,
      videoCodecs: codecs,
    })
    if (lease.state !== 'READY' || !lease.leaseId)
      throw new Error(lease.reasonCode || 'MEDIA_NOT_READY')
    this.lease = lease
    if (this.closed) {
      await this.releaseLease()
      throw new Error('CANCELLED')
    }
    const peer = new RTCPeerConnection()
    this.peer = peer
    peer.addTransceiver('video', { direction: 'recvonly' })
    peer.addTransceiver('audio', { direction: 'recvonly' })
    const stream = new MediaStream()
    peer.ontrack = (event) => {
      if (this.closed) return
      if (!stream.getTracks().some((track) => track.id === event.track.id))
        stream.addTrack(event.track)
      this.events.stream(stream)
    }
    peer.onconnectionstatechange = () => {
      if (this.closed) return
      if (peer.connectionState === 'connected') clearTimeout(this.connectTimer)
      if (peer.connectionState === 'failed' || peer.connectionState === 'disconnected') {
        this.events.failed('CONNECTION_FAILED')
        void this.close()
      }
    }
    const offer = await peer.createOffer()
    await peer.setLocalDescription(offer)
    await gatherIce(peer)
    if (this.closed) throw new Error('CANCELLED')
    const answer = await api.exchangeWebRtc(
      lease.leaseId,
      peer.localDescription?.sdp || offer.sdp || '',
    )
    if (this.closed) throw new Error('CANCELLED')
    await peer.setRemoteDescription(answer)
    this.connectTimer = setTimeout(() => {
      if (!this.closed && peer.connectionState !== 'connected') {
        this.events.failed('CONNECT_TIMEOUT')
        void this.close()
      }
    }, 20000)
    this.scheduleRenew()
    return lease
  }
  private scheduleRenew(): void {
    const expires = Date.parse(this.lease?.expiresAt || '')
    const delay = Number.isFinite(expires)
      ? Math.max(1000, Math.min(30000, expires - Date.now() - 10000))
      : 30000
    this.renewTimer = setTimeout(async () => {
      if (this.closed || !this.lease?.leaseId) return
      try {
        const result = await api.renewPreviewLease(this.lease.leaseId)
        if (this.closed) return
        if (result.state !== 'READY') throw new Error(result.reasonCode || 'LEASE_EXPIRED')
        this.lease = result
        this.scheduleRenew()
      } catch {
        if (!this.closed) {
          this.events.failed('LEASE_EXPIRED')
          void this.close()
        }
      }
    }, delay)
  }
  async stats(): Promise<PreviewStats | null> {
    return this.closed || !this.lease?.leaseId ? null : api.getPreviewStats(this.lease.leaseId)
  }
  async close(): Promise<void> {
    this.closed = true
    clearTimeout(this.renewTimer)
    clearTimeout(this.connectTimer)
    if (this.peer) {
      this.peer.ontrack = null
      this.peer.onconnectionstatechange = null
      this.peer.close()
      this.peer = null
    }
    await this.releaseLease()
  }
  private async releaseLease(): Promise<void> {
    const id = this.lease?.leaseId
    this.lease = null
    if (id) await api.deletePreviewLease(id).catch(() => undefined)
  }
}
export function createPreviewPlayer(events: PlayerEvents): PreviewPlayer {
  return new WebRtcPreviewPlayer(events)
}
