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
  let active = true,
    statsBusy = false
  const current = (index: number, revision: number) => active && revisions.get(index) === revision
  function bindVideo(video: HTMLVideoElement, stream: MediaStream | null) {
    // Function refs run on each render; reassigning even the same stream resets playback.
    if (video.srcObject !== stream) video.srcObject = stream
  }
  function setVideoRef(index: number, value: unknown) {
    if (value instanceof HTMLVideoElement) {
      videoRefs.set(index, value)
      bindVideo(value, streams.get(index) || null)
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
    slot.player = null
    slot.lease = null
    slot.stats = null
    streams.delete(index)
    const video = videoRefs.get(index)
    if (video) video.srcObject = null
    if (clear) {
      slot.channel = null
      slot.profile = null
      slot.state = 'EMPTY'
      slot.error = null
    }
    await player?.close()
    return revision
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
          if (current(index, revision)) slot.state = state
        },
        failed(code) {
          if (current(index, revision)) {
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
      frameTimers.set(
        index,
        setTimeout(() => {
          if (!current(index, revision) || slot.state === 'PLAYING') return
          slot.state = 'ERROR'
          slot.error = '尚未收到视频画面，请重连或检查相机编码'
          void player.close()
        }, 20000),
      )
    } catch (cause) {
      await player.close()
      if (!current(index, revision)) return
      slot.state = 'ERROR'
      slot.error =
        cause instanceof ApiRequestError
          ? `${cause.message} (${cause.code})`
          : previewReason(cause instanceof Error ? cause.message : null)
    }
  }
  function firstFrame(index: number) {
    const slot = slots[index]
    if (slot.player && streams.has(index)) {
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
  async function trim(layout: PreviewLayout): Promise<void> {
    const closing: Promise<number>[] = []
    for (let index = layoutCount(layout); index < MAX_PREVIEW_SLOTS; index++)
      closing.push(stop(index))
    await Promise.all(closing)
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
