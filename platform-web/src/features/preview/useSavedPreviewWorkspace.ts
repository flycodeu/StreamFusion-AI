import { onBeforeUnmount, ref, type Ref } from 'vue'
import { ElMessage } from 'element-plus'
import type { PreviewChannel } from '../../api/preview/types'
import { usePageScope } from '../../composables/usePageScope'
import { sessionState } from '../../session/state'
import type { PreviewLayout } from './layouts'
import { captureView, resolveSavedView, type SavedView } from './saved-views'
import type { usePreviewSlots } from './usePreviewSlots'

type Playback = Pick<
  ReturnType<typeof usePreviewSlots>,
  'slots' | 'stop' | 'play' | 'assign' | 'unavailable' | 'muteAll' | 'slotRevision'
>

interface WorkspaceActions {
  takeWorkspace: () => void
  chooseLayout: (layout: PreviewLayout, isCurrent: () => boolean) => Promise<boolean>
  showRestoredChannels: (channels: PreviewChannel[]) => void
  closeSlot: (index: number) => void
}

function missingChannel(cameraId: string): PreviewChannel {
  return {
    cameraId,
    name: '收藏相机暂不可用',
    deviceGroupKey: `c${cameraId}`,
    deviceName: null,
    groupId: null,
    groupPath: null,
    lifecycle: 'DISABLED',
    profiles: [],
  }
}

export function useSavedPreviewWorkspace(
  layout: Ref<PreviewLayout>,
  { slots, stop, play, assign, unavailable, muteAll, slotRevision }: Playback,
  { takeWorkspace, chooseLayout, showRestoredChannels, closeSlot }: WorkspaceActions,
) {
  const captureScope = usePageScope()
  const openingView = ref(false)
  const retryingSlots = ref(new Set<number>())
  const retryRequests = new Map<number, AbortController>()
  let viewSequence = 0
  let viewAbort: AbortController | undefined

  function cancelRetries() {
    for (const request of retryRequests.values()) request.abort()
    retryRequests.clear()
    retryingSlots.value.clear()
  }

  function cancel() {
    ++viewSequence
    viewAbort?.abort()
    viewAbort = undefined
    cancelRetries()
    openingView.value = false
  }

  async function retrySlot(index: number) {
    if (openingView.value || retryingSlots.value.has(index) || retryingSlots.value.size >= 4) return
    const slot = slots[index]!
    if (slot.error?.code === 'LEASE_RELEASE_FAILED') {
      closeSlot(index)
      return
    }
    if (!slot.channel || !slot.savedProfileId) {
      await play(index)
      return
    }
    const cameraId = slot.channel.cameraId
    const profileId = slot.savedProfileId
    const active = captureScope()
    const revision = viewSequence
    const controller = new AbortController()
    const isCurrent = (generation: number) =>
      generation >= 0 &&
      active() &&
      !controller.signal.aborted &&
      revision === viewSequence &&
      slotRevision(index) === generation &&
      slot.channel?.cameraId === cameraId &&
      slot.savedProfileId === profileId
    retryRequests.set(index, controller)
    retryingSlots.value.add(index)
    try {
      const generation = await stop(index, false)
      if (!isCurrent(generation)) return
      slot.state = 'CONNECTING'
      slot.error = null
      const [target] = await resolveSavedView(
        {
          id: 'retry',
          name: 'retry',
          layout: layout.value,
          updatedAt: new Date().toISOString(),
          slots: [{ index, cameraId, profileId }],
        },
        controller.signal,
      )
      if (!target || !isCurrent(generation)) return
      if (target.channel) await assign(index, target.channel, slot.usage, profileId)
      else unavailable(index, missingChannel(cameraId), profileId, target.failure)
    } finally {
      if (retryRequests.get(index) === controller) {
        retryRequests.delete(index)
        retryingSlots.value.delete(index)
      }
    }
  }

  function currentView() {
    return captureView(
      layout.value,
      slots.map((slot) => ({
        channel: slot.channel,
        profile:
          slot.profile || (slot.savedProfileId ? { streamProfileId: slot.savedProfileId } : null),
      })),
    )
  }

  async function openView(view: SavedView) {
    if (openingView.value || !sessionState.me) return
    cancel()
    const revision = viewSequence
    const active = captureScope()
    const controller = new AbortController()
    const isCurrent = () => active() && revision === viewSequence && !controller.signal.aborted
    viewAbort = controller
    openingView.value = true
    takeWorkspace()
    muteAll()
    try {
      const closed = await Promise.all(slots.map((_slot, index) => stop(index)))
      if (!isCurrent()) return
      if (closed.some((value) => value < 0)) {
        ElMessage.info('原画面资源释放尚未确认，请重试关闭后再打开收藏')
        return
      }
      const targets = await resolveSavedView(view, controller.signal)
      if (!isCurrent() || !(await chooseLayout(view.layout, isCurrent)) || !isCurrent()) return
      const restored: PreviewChannel[] = []
      const starts: Promise<void>[] = []
      for (const target of targets) {
        if (!isCurrent()) return
        if (target.channel) {
          restored.push(target.channel)
          starts.push(assign(target.index, target.channel, 'MAIN', target.profileId))
        } else {
          unavailable(
            target.index,
            missingChannel(target.cameraId),
            target.profileId,
            target.failure,
          )
        }
      }
      showRestoredChannels(restored)
      await Promise.all(starts)
      if (isCurrent()) {
        const failed = targets.filter((target) => slots[target.index]?.state === 'ERROR').length
        if (failed) ElMessage.info(`已应用收藏，${failed} 个窗口需重新选择或重试；其余按队列连接`)
      }
    } catch {
      if (isCurrent()) ElMessage.info('收藏未能完整打开，请保留当前画面并重试')
    } finally {
      if (viewAbort === controller) viewAbort = undefined
      if (revision === viewSequence) openingView.value = false
    }
  }

  onBeforeUnmount(cancel)
  return { openingView, retryingSlots, currentView, openView, retrySlot, cancel }
}
