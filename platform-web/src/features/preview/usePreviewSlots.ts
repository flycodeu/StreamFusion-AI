import { markRaw, onBeforeUnmount, reactive } from 'vue'
import type {
  PreviewChannel,
  PreviewLease,
  PreviewProfile,
  PreviewStats,
} from '../../api/preview/types'
import { ApiRequestError } from '../../lib/http/error'
import { createPreviewPlayer } from './player'
import type { PreviewPlayer } from './player'
import { previewReason, selectProfile } from './model'
import type { StreamUsage } from './model'
import { layoutCount, MAX_PREVIEW_SLOTS, type PreviewLayout } from './layouts'

export interface PreviewSlot {
  channel: PreviewChannel | null
  profile: PreviewProfile | null
  usage: StreamUsage
  lease: PreviewLease | null
  player: PreviewPlayer | null
  stats: PreviewStats | null
  state: 'EMPTY' | 'CONNECTING' | 'PLAYING' | 'ERROR'
  error: string | null
}
export function usePreviewSlots() {
  const slots = reactive<PreviewSlot[]>(
    Array.from({ length: MAX_PREVIEW_SLOTS }, () => ({
      channel: null,
      profile: null,
      usage: 'MAIN',
      lease: null,
      player: null,
      stats: null,
      state: 'EMPTY',
      error: null,
    })),
  )
  const videoRefs = new Map<number, HTMLVideoElement>()
  const streams = new Map<number, MediaStream>()
  const tileRefs = new Map<number, HTMLElement>()
  const revisions = new Map<number, number>()
  const frameTimers = new Map<number, ReturnType<typeof setTimeout>>()
  const videoErrors = new Map<number, { video: HTMLVideoElement; listener: () => void }>()
  const recovering = new Map<number, number>()
  let active = true,
    statsBusy = false
  const current = (index: number, revision: number) => active && revisions.get(index) === revision
  function bindVideo(video: HTMLVideoElement, stream: MediaStream | null) {
    // Function refs run on each render; reassigning even the same stream resets playback.
    if (video.srcObject !== stream) video.srcObject = stream
  }
  function setVideoRef(index: number, value: unknown) {
    const previous = videoErrors.get(index)
    if (previous?.video === value) return
    if (previous) previous.video.removeEventListener('error', previous.listener)
    videoErrors.delete(index)
    if (value instanceof HTMLVideoElement) {
      videoRefs.set(index, value)
      bindVideo(value, streams.get(index) || null)
      const listener = () => void videoError(index, value)
      value.addEventListener('error', listener)
      videoErrors.set(index, { video: value, listener })
    } else videoRefs.delete(index)
  }
  function setTileRef(index: number, value: unknown) {
    if (value instanceof HTMLElement) tileRefs.set(index, value)
    else tileRefs.delete(index)
  }
  async function stop(index: number, clear = true): Promise<number> {
    const slot = slots[index]
    if (!slot) return -1
    const revision = (revisions.get(index) || 0) + 1
    revisions.set(index, revision)
    clearTimeout(frameTimers.get(index))
    frameTimers.delete(index)
    const player = slot.player
    slot.stats = null
    streams.delete(index)
    const video = videoRefs.get(index)
    if (video) video.srcObject = null
    try {
      await player?.close()
    } catch {
      if (current(index, revision)) {
        slot.state = 'ERROR'
        slot.error = previewReason('LEASE_RELEASE_FAILED')
      }
      return -1
    }
    if (!current(index, revision)) return revision
    slot.player = null
    slot.lease = null
    if (clear) {
      slot.channel = null
      slot.profile = null
      slot.state = 'EMPTY'
      slot.error = null
    }
    return revision
  }
  function armFrameTimeout(index: number, revision: number, player: PreviewPlayer) {
    clearTimeout(frameTimers.get(index))
    if (slots[index]!.state === 'PLAYING') return
    frameTimers.set(
      index,
      setTimeout(() => {
        if (!current(index, revision) || slots[index]!.state === 'PLAYING') return
        slots[index]!.state = 'ERROR'
        slots[index]!.error = '尚未收到视频画面，请重连或检查相机编码'
        void player.close().catch(() => {
          if (current(index, revision)) slots[index]!.error = previewReason('LEASE_RELEASE_FAILED')
        })
      }, 20000),
    )
  }
  async function videoError(index: number, video: HTMLVideoElement): Promise<void> {
    const slot = slots[index]!,
      player = slot.player,
      revision = revisions.get(index) || 0,
      stream = streams.get(index)
    if (
      !current(index, revision) ||
      !player ||
      !stream ||
      videoRefs.get(index) !== video ||
      video.srcObject !== stream ||
      !video.error ||
      recovering.get(index) === revision
    )
      return
    const errorCode = video.error.code
    recovering.set(index, revision)
    clearTimeout(frameTimers.get(index))
    streams.delete(index)
    bindVideo(video, null)
    slot.stats = null
    slot.state = 'CONNECTING'
    slot.error = null
    try {
      const lease = await player.recoverVideoError(errorCode)
      if (!current(index, revision)) return
      if (!lease) throw new Error(errorCode === 3 ? 'VIDEO_DECODE_FAILED' : 'VIDEO_PLAYBACK_FAILED')
      slot.lease = lease
      armFrameTimeout(index, revision, player)
    } catch (cause) {
      let failure = cause
      try {
        await player.close()
      } catch {
        failure = new Error('LEASE_RELEASE_FAILED')
      }
      if (current(index, revision)) {
        slot.state = 'ERROR'
        slot.error =
          failure instanceof ApiRequestError
            ? `${failure.code === 'RATE_LIMITED' ? previewReason(failure.code) : failure.message} (${failure.code})`
            : previewReason(failure instanceof Error ? failure.message : null)
      }
    } finally {
      if (recovering.get(index) === revision) recovering.delete(index)
    }
  }
  async function play(index: number): Promise<void> {
    const slot = slots[index]
    if (!slot.channel || !slot.profile) return
    const revision = await stop(index, false)
    if (!current(index, revision) || !slot.channel || !slot.profile) return
    slot.state = 'CONNECTING'
    slot.error = null
    const player = markRaw(
      createPreviewPlayer({
        stream(stream) {
          if (current(index, revision)) {
            streams.set(index, stream)
            const video = videoRefs.get(index)
            if (video) bindVideo(video, stream)
          }
        },
        state(state) {
          if (current(index, revision)) {
            slot.state = state
            slot.error = null
          }
        },
        failed(code) {
          if (current(index, revision)) {
            clearTimeout(frameTimers.get(index))
            streams.delete(index)
            const video = videoRefs.get(index)
            if (video) bindVideo(video, null)
            slot.state = 'ERROR'
            slot.error = previewReason(code)
          }
        },
      }),
    )
    slot.player = player
    try {
      const lease = await player.connect({
        cameraId: slot.channel.cameraId,
        profileId: slot.profile.streamProfileId,
        usage: slot.usage,
      })
      if (!current(index, revision)) return
      slot.lease = lease
      armFrameTimeout(index, revision, player)
    } catch (cause) {
      let failure = cause
      try {
        await player.close()
      } catch {
        failure = new Error('LEASE_RELEASE_FAILED')
      }
      if (!current(index, revision)) return
      slot.state = 'ERROR'
      slot.error =
        failure instanceof ApiRequestError
          ? `${failure.code === 'RATE_LIMITED' ? previewReason(failure.code) : failure.message} (${failure.code})`
          : previewReason(failure instanceof Error ? failure.message : null)
    }
  }
  function firstFrame(index: number) {
    const slot = slots[index]
    if (slot.player && streams.has(index) && slot.state === 'CONNECTING') {
      clearTimeout(frameTimers.get(index))
      frameTimers.delete(index)
      slot.state = 'PLAYING'
    }
  }
  async function assign(index: number, channel: PreviewChannel, usage: StreamUsage): Promise<void> {
    const slot = slots[index],
      revision = await stop(index)
    if (!current(index, revision)) return
    slot.channel = channel
    slot.usage = usage
    slot.profile = selectProfile(channel, usage)
    if (!slot.profile) {
      slot.state = 'ERROR'
      slot.error = usage === 'MAIN' ? '没有可用主码流' : '没有可用辅码流'
      return
    }
    await play(index)
  }
  async function switchProfile(index: number, profileId: string): Promise<void> {
    const slot = slots[index],
      profile = slot.channel?.profiles.find(
        (profile) => profile.streamProfileId === profileId && profile.enabled && profile.configured,
      )
    if (!profile) return
    const revision = await stop(index, false)
    if (!current(index, revision)) return
    slot.profile = profile
    if (profile.usage === 'MAIN' || profile.usage === 'SUB') slot.usage = profile.usage
    await play(index)
  }
  async function sampleStats(): Promise<void> {
    if (!active || statsBusy) return
    statsBusy = true
    try {
      await Promise.allSettled(
        slots.map(async (slot, index) => {
          const player = slot.player,
            revision = revisions.get(index) || 0
          if (!player) return
          const stats = await player.stats()
          if (current(index, revision)) slot.stats = stats
        }),
      )
    } finally {
      statsBusy = false
    }
  }
  async function trim(layout: PreviewLayout): Promise<boolean> {
    const closing: Promise<number>[] = []
    const count = layoutCount(layout)
    for (let index = count; index < MAX_PREVIEW_SLOTS; index++) closing.push(stop(index))
    const results = await Promise.all(closing)
    return (
      results.every((revision) => revision >= 0) &&
      slots.slice(count).every((slot) => !slot.channel && !slot.player)
    )
  }
  async function fullscreen(index: number): Promise<void> {
    await tileRefs.get(index)?.requestFullscreen()
  }
  async function snapshot(index: number): Promise<boolean> {
    const video = videoRefs.get(index)
    if (!video || video.readyState < 2 || !video.videoWidth) return false
    const canvas = document.createElement('canvas')
    canvas.width = video.videoWidth
    canvas.height = video.videoHeight
    canvas.getContext('2d')?.drawImage(video, 0, 0)
    const blob = await new Promise<Blob | null>((resolve) => canvas.toBlob(resolve, 'image/png'))
    if (!blob) return false
    const url = URL.createObjectURL(blob),
      link = document.createElement('a')
    link.download = `${slots[index].channel?.name || 'camera'}-${Date.now()}.png`
    link.href = url
    link.click()
    setTimeout(() => URL.revokeObjectURL(url), 1000)
    return true
  }
  onBeforeUnmount(() => {
    active = false
    for (const { video, listener } of videoErrors.values())
      video.removeEventListener('error', listener)
    videoErrors.clear()
    for (let index = 0; index < slots.length; index++) void stop(index)
  })
  return {
    slots,
    setVideoRef,
    setTileRef,
    assign,
    play,
    stop,
    switchProfile,
    sampleStats,
    fullscreen,
    snapshot,
    firstFrame,
    trim,
  }
}
