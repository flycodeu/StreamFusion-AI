import { markRaw, onBeforeUnmount, reactive } from 'vue'
import type {
  PreviewChannel,
  PreviewLease,
  PreviewProfile,
  PreviewStats,
} from '../../api/preview/types'
import { createPreviewPlayer } from './player'
import type { PreviewPlayer } from './player'
import { selectProfile } from './model'
import type { StreamUsage } from './model'
import { previewFailure } from './failure'
import type { PreviewFailure } from './failure'
import { layoutCount, MAX_PREVIEW_SLOTS, type PreviewLayout } from './layouts'

export interface PreviewSlot {
  channel: PreviewChannel | null
  profile: PreviewProfile | null
  usage: StreamUsage
  lease: PreviewLease | null
  player: PreviewPlayer | null
  stats: PreviewStats | null
  state: 'EMPTY' | 'CONNECTING' | 'PLAYING' | 'CLOSING' | 'ERROR'
  error: PreviewFailure | null
  muted: boolean
  audio: 'UNKNOWN' | 'AVAILABLE' | 'BLOCKED'
  savedProfileId?: string
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
      muted: true,
      audio: 'UNKNOWN',
    })),
  )
  const videoRefs = new Map<number, HTMLVideoElement>()
  const streams = new Map<number, MediaStream>()
  const revisions = new Map<number, number>()
  const frameTimers = new Map<number, ReturnType<typeof setTimeout>>()
  const videoErrors = new Map<number, { video: HTMLVideoElement; listener: () => void }>()
  const recovering = new Map<number, number>()
  const videoFrames = new Map<number, { video: HTMLVideoElement; handle: number }>()
  let soundSequence = 0
  const waitingStarts = new Map<
    number,
    { revision: number; resolve: (release: (() => void) | null) => void }
  >()
  let starting = 0
  let active = true,
    statsBusy = false
  const current = (index: number, revision: number) => active && revisions.get(index) === revision
  function drainStarts() {
    while (starting < 4 && waitingStarts.size) {
      const [index, pending] = waitingStarts.entries().next().value!
      waitingStarts.delete(index)
      if (!current(index, pending.revision)) {
        pending.resolve(null)
        continue
      }
      starting++
      let released = false
      pending.resolve(() => {
        if (released) return
        released = true
        starting--
        drainStarts()
      })
    }
  }
  function startWhenReady(index: number, revision: number) {
    return new Promise<(() => void) | null>((resolve) => {
      waitingStarts.get(index)?.resolve(null)
      waitingStarts.set(index, { revision, resolve })
      drainStarts()
    })
  }
  function fail(index: number, revision: number, cause: unknown, code?: string) {
    if (!current(index, revision)) return
    const slot = slots[index]!
    slot.state = 'ERROR'
    slot.error = previewFailure(cause, {
      code,
      cameraId: slot.channel?.cameraId,
      cameraName: slot.channel?.name,
      streamProfileId: slot.profile?.streamProfileId ?? slot.savedProfileId,
      profileName: slot.profile?.label,
      leaseId: slot.lease?.leaseId,
    })
  }
  function bindVideo(video: HTMLVideoElement, stream: MediaStream | null) {
    // Function refs run on each render; reassigning even the same stream resets playback.
    if (video.srcObject !== stream) video.srcObject = stream
  }
  function cancelVideoFrame(index: number) {
    const pending = videoFrames.get(index)
    pending?.video.cancelVideoFrameCallback?.(pending.handle)
    videoFrames.delete(index)
  }
  function observeVideoFrame(index: number) {
    const video = videoRefs.get(index),
      stream = streams.get(index),
      revision = revisions.get(index) || 0
    if (!video || !stream || !video.requestVideoFrameCallback || videoFrames.has(index)) return
    const handle = video.requestVideoFrameCallback(() => {
      videoFrames.delete(index)
      if (
        current(index, revision) &&
        videoRefs.get(index) === video &&
        video.srcObject === stream
      ) {
        firstFrame(index, true)
        if (slots[index]!.state === 'CONNECTING') observeVideoFrame(index)
      }
    })
    videoFrames.set(index, { video, handle })
  }
  function muteAll() {
    soundSequence++
    for (let index = 0; index < slots.length; index++) {
      slots[index]!.muted = true
      const video = videoRefs.get(index)
      if (video) video.muted = true
    }
  }
  async function toggleAudio(index: number): Promise<void> {
    const slot = slots[index],
      video = videoRefs.get(index)
    if (!slot || !video || slot.state !== 'PLAYING') return
    if (!slot.muted) {
      muteAll()
      return
    }
    if (
      !streams
        .get(index)
        ?.getAudioTracks?.()
        .some((track) => track.readyState !== 'ended')
    )
      return
    muteAll()
    const sequence = soundSequence,
      revision = revisions.get(index) || 0
    slot.muted = false
    video.muted = false
    try {
      await video.play()
      if (sequence === soundSequence && current(index, revision)) slot.audio = 'AVAILABLE'
    } catch {
      if (!current(index, revision)) return
      if (sequence === soundSequence) {
        slot.muted = true
        video.muted = true
        slot.audio = 'BLOCKED'
      }
      // Some browsers pause a playing element when unmuting is denied. Resume only
      // this still-current, now-muted video; an audio policy error must not kill it.
      if (slot.muted && slot.state === 'PLAYING' && videoRefs.get(index) === video)
        void video.play().catch(() => {})
    }
  }
  function setVideoRef(index: number, value: unknown) {
    const previous = videoErrors.get(index)
    if (previous?.video === value) return
    if (previous) previous.video.removeEventListener('error', previous.listener)
    videoErrors.delete(index)
    cancelVideoFrame(index)
    if (value instanceof HTMLVideoElement) {
      videoRefs.set(index, value)
      bindVideo(value, streams.get(index) || null)
      value.muted = slots[index]!.muted
      observeVideoFrame(index)
      const listener = () => void videoError(index, value)
      value.addEventListener('error', listener)
      videoErrors.set(index, { video: value, listener })
    } else videoRefs.delete(index)
  }
  async function stop(index: number, clear = true): Promise<number> {
    const slot = slots[index]
    if (!slot) return -1
    const revision = (revisions.get(index) || 0) + 1
    cancelVideoFrame(index)
    slot.muted = true
    slot.audio = 'UNKNOWN'
    revisions.set(index, revision)
    waitingStarts.get(index)?.resolve(null)
    waitingStarts.delete(index)
    clearTimeout(frameTimers.get(index))
    frameTimers.delete(index)
    const player = slot.player
    slot.stats = null
    streams.delete(index)
    const video = videoRefs.get(index)
    if (video) {
      video.muted = true
      video.srcObject = null
    }
    if (player) {
      slot.state = 'CLOSING'
      slot.error = null
    }
    try {
      await player?.close()
    } catch (cause) {
      fail(index, revision, cause, 'LEASE_RELEASE_FAILED')
      return -1
    }
    if (!current(index, revision)) return revision
    slot.player = null
    slot.lease = null
    if (clear) {
      slot.channel = null
      slot.profile = null
      slot.savedProfileId = undefined
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
        fail(index, revision, 'FIRST_FRAME_TIMEOUT')
        void player.close().catch((cause) => fail(index, revision, cause, 'LEASE_RELEASE_FAILED'))
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
    cancelVideoFrame(index)
    muteAll()
    slot.audio = 'UNKNOWN'
    clearTimeout(frameTimers.get(index))
    streams.delete(index)
    bindVideo(video, null)
    slot.stats = null
    slot.state = 'CONNECTING'
    slot.error = null
    let releaseStart: (() => void) | null = null
    try {
      releaseStart = await startWhenReady(index, revision)
      if (!releaseStart || !current(index, revision)) return
      const lease = await player.recoverVideoError(errorCode)
      if (!current(index, revision)) return
      if (!lease) throw new Error(errorCode === 3 ? 'VIDEO_DECODE_FAILED' : 'VIDEO_PLAYBACK_FAILED')
      slot.lease = lease
      armFrameTimeout(index, revision, player)
    } catch (cause) {
      let failure = cause
      try {
        await player.close()
      } catch (cause) {
        failure = new Error('LEASE_RELEASE_FAILED', { cause })
      }
      fail(index, revision, failure)
    } finally {
      releaseStart?.()
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
            if (stream.getAudioTracks?.().some((track) => track.readyState !== 'ended')) {
              if (slot.audio !== 'BLOCKED') slot.audio = 'AVAILABLE'
            } else {
              slot.audio = 'UNKNOWN'
              slot.muted = true
              if (video) video.muted = true
            }
            observeVideoFrame(index)
          }
        },
        lease(lease) {
          if (current(index, revision)) slot.lease = lease
        },
        state(state) {
          if (current(index, revision)) {
            slot.state = state
            slot.error = null
          }
        },
        failed(code, cause) {
          if (current(index, revision)) {
            clearTimeout(frameTimers.get(index))
            streams.delete(index)
            cancelVideoFrame(index)
            slot.muted = true
            slot.audio = 'UNKNOWN'
            const video = videoRefs.get(index)
            if (video) {
              video.muted = true
              bindVideo(video, null)
            }
            fail(index, revision, cause ?? code, code)
          }
        },
      }),
    )
    slot.player = player
    let releaseStart: (() => void) | null = null
    try {
      releaseStart = await startWhenReady(index, revision)
      if (!releaseStart || !current(index, revision)) return
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
      } catch (cause) {
        failure = new Error('LEASE_RELEASE_FAILED', { cause })
      }
      if (!current(index, revision)) return
      fail(index, revision, failure)
    } finally {
      releaseStart?.()
    }
  }
  function firstFrame(index: number, rendered = false) {
    const slot = slots[index],
      video = videoRefs.get(index),
      stream = streams.get(index)
    if (
      slot.player &&
      video &&
      stream &&
      video.srcObject === stream &&
      video.readyState >= 2 &&
      video.videoWidth > 0 &&
      video.videoHeight > 0 &&
      stream.getVideoTracks?.().some((track) => track.readyState !== 'ended') &&
      (rendered || !video.requestVideoFrameCallback) &&
      slot.state === 'CONNECTING'
    ) {
      clearTimeout(frameTimers.get(index))
      frameTimers.delete(index)
      slot.state = 'PLAYING'
    }
  }
  async function assign(
    index: number,
    channel: PreviewChannel,
    usage: StreamUsage,
    profileId?: string,
  ): Promise<void> {
    const slot = slots[index],
      revision = await stop(index)
    if (!current(index, revision)) return
    slot.channel = channel
    slot.usage = usage
    slot.savedProfileId = profileId
    slot.profile = profileId
      ? channel.profiles.find(
          (profile) =>
            profile.streamProfileId === profileId && profile.enabled && profile.configured,
        ) || null
      : selectProfile(channel, usage)
    if (channel.lifecycle !== 'ENABLED') {
      fail(index, revision, 'CAMERA_DISABLED')
      return
    }
    if (!slot.profile) {
      fail(
        index,
        revision,
        profileId ? 'PROFILE_UNAVAILABLE' : usage === 'MAIN' ? 'NO_MAIN_PROFILE' : 'NO_SUB_PROFILE',
      )
      return
    }
    if (profileId && (slot.profile.usage === 'MAIN' || slot.profile.usage === 'SUB'))
      slot.usage = slot.profile.usage
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
    slot.savedProfileId = profileId
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
  async function snapshot(index: number): Promise<boolean> {
    const video = videoRefs.get(index),
      stream = streams.get(index),
      revision = revisions.get(index) || 0
    if (!video || !stream || video.readyState < 2 || !video.videoWidth || !current(index, revision))
      return false
    const canvas = document.createElement('canvas')
    canvas.width = video.videoWidth
    canvas.height = video.videoHeight
    canvas.getContext('2d')?.drawImage(video, 0, 0)
    const blob = await new Promise<Blob | null>((resolve) => canvas.toBlob(resolve, 'image/png'))
    if (
      !blob ||
      !current(index, revision) ||
      videoRefs.get(index) !== video ||
      streams.get(index) !== stream
    )
      return false
    const url = URL.createObjectURL(blob),
      link = document.createElement('a')
    link.download = `${slots[index].channel?.name || 'camera'}-${Date.now()}.png`
    link.href = url
    link.click()
    setTimeout(() => URL.revokeObjectURL(url), 1000)
    return true
  }
  async function clearIdentity(): Promise<void> {
    muteAll()
    await Promise.all(
      slots.map((slot, index) => {
        // Hide the previous account's display data immediately, but retain the player until
        // release is confirmed. An unconfirmed old responsibility still blocks replacement.
        slot.channel = null
        slot.profile = null
        slot.savedProfileId = undefined
        slot.lease = null
        slot.error = null
        return stop(index)
      }),
    )
  }
  onBeforeUnmount(() => {
    active = false
    for (const { video, listener } of videoErrors.values())
      video.removeEventListener('error', listener)
    videoErrors.clear()
    muteAll()
    for (let index = 0; index < slots.length; index++) void stop(index)
  })
  return {
    slots,
    setVideoRef,
    assign,
    play,
    stop,
    switchProfile,
    sampleStats,
    snapshot,
    firstFrame,
    trim,
    muteAll,
    toggleAudio,
    clearIdentity,
    slotRevision(index: number) {
      return revisions.get(index) || 0
    },
    unavailable(index: number, channel: PreviewChannel, profileId: string, cause: unknown) {
      const slot = slots[index]!
      if (slot.player) return
      slot.channel = channel
      slot.profile = null
      slot.savedProfileId = profileId
      fail(index, revisions.get(index) || 0, cause)
    },
  }
}
