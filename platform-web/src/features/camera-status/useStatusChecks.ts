import {
  computed,
  getCurrentInstance,
  onActivated,
  onDeactivated,
  onScopeDispose,
  ref,
  shallowRef,
  watch,
} from 'vue'
import type { Ref } from 'vue'
import * as api from '../../api/camera-status/api'
import { layerKey } from '../../api/camera-status/types'
import type {
  CameraStatus,
  LayerStatus,
  StatusLayer,
  StatusCheck,
  StatusCheckInput,
} from '../../api/camera-status/types'
import { isRequestRejected } from '../camera/form'
import { invalidResponse } from '../../lib/http/error'
import { isTerminal } from './model'

interface PendingCommand {
  cameraId: string
  input: StatusCheckInput
  uncertain: boolean
  error: unknown
}

/** A single read owner; stopping browser reads never cancels a server-side check. */
export function useStatusChecks(
  cameraId: Ref<string | null>,
  profileId: Ref<string | null>,
  active: Ref<boolean>,
  layer: Ref<StatusLayer>,
) {
  const snapshot = shallowRef<CameraStatus | null>(null)
  const localCheck = shallowRef<StatusCheck | null>(null)
  const pending = shallowRef<PendingCommand | null>(null)
  const reading = ref(false),
    posting = ref(false),
    readPaused = ref(false)
  const error = shallowRef<unknown>(null)
  const visible = ref(typeof document === 'undefined' || !document.hidden)
  const activated = ref(true)
  const enabled = computed(
    () => active.value && visible.value && activated.value && !!cameraId.value,
  )
  const selectedProfile = computed(
    () => snapshot.value?.profiles.find((item) => item.streamProfileId === profileId.value) ?? null,
  )
  const selected = computed<LayerStatus | null>(() => {
    const profile = selectedProfile.value?.[layerKey(layer.value)]
    return profile ?? null
  })
  const check = computed(() => localCheck.value ?? selected.value?.latestCheck ?? null)
  const pendingHere = computed(
    () =>
      !!pending.value &&
      pending.value.cameraId === cameraId.value &&
      pending.value.input.streamProfileId === profileId.value &&
      pending.value.input.layer === layer.value,
  )
  const canSubmit = computed(
    () =>
      enabled.value &&
      !posting.value &&
      !pending.value &&
      selected.value?.canCheck === true &&
      (!check.value || isTerminal(check.value)),
  )
  let disposed = false,
    generation = 0,
    pollCount = 0,
    failures = 0
  let readController: AbortController | undefined
  let postController: AbortController | undefined
  let pollTimer: ReturnType<typeof setTimeout> | undefined

  function viewing(targetCamera: string, targetProfile: string | null, current: number) {
    return (
      !disposed &&
      enabled.value &&
      generation === current &&
      cameraId.value === targetCamera &&
      profileId.value === targetProfile
    )
  }
  function stopReads() {
    ++generation
    clearTimeout(pollTimer)
    readController?.abort()
    readController = undefined
    reading.value = false
  }
  function schedule(current: number) {
    clearTimeout(pollTimer)
    if (
      disposed ||
      !enabled.value ||
      current !== generation ||
      !check.value ||
      isTerminal(check.value)
    )
      return
    if (pollCount >= 30 || failures >= 3) {
      readPaused.value = true
      return
    }
    pollTimer = setTimeout(() => void readCheck(current), 2000)
  }
  function applyCheck(result: StatusCheck) {
    localCheck.value = result
    if (snapshot.value?.cameraId !== result.cameraId) return
    const key = layerKey(result.layer)
    snapshot.value = {
      ...snapshot.value,
      profiles: snapshot.value.profiles.map((profile) =>
        profile.streamProfileId === result.streamProfileId
          ? { ...profile, [key]: { ...profile[key], latestCheck: result } }
          : profile,
      ),
    }
  }
  async function readSnapshot(current = generation): Promise<void> {
    const targetCamera = cameraId.value,
      targetProfile = profileId.value
    if (!targetCamera || !viewing(targetCamera, targetProfile, current) || reading.value) return
    const controller = new AbortController()
    readController = controller
    reading.value = true
    try {
      const result = await api.getCameraStatus(targetCamera, controller.signal)
      if (!viewing(targetCamera, targetProfile, current)) return
      snapshot.value = result
      localCheck.value =
        result.profiles.find((item) => item.streamProfileId === targetProfile)?.[
          layerKey(layer.value)
        ]?.latestCheck ?? null
      failures = 0
      error.value = null
    } catch (cause) {
      if (viewing(targetCamera, targetProfile, current)) {
        error.value = cause
        ++failures
      }
    } finally {
      if (viewing(targetCamera, targetProfile, current)) {
        reading.value = false
        readController = undefined
        schedule(current)
      }
    }
  }
  async function readCheck(current: number): Promise<void> {
    const targetCamera = cameraId.value,
      targetProfile = profileId.value,
      targetCheck = check.value
    if (
      !targetCamera ||
      !targetCheck ||
      isTerminal(targetCheck) ||
      !viewing(targetCamera, targetProfile, current) ||
      reading.value
    )
      return
    if (pollCount >= 30) {
      readPaused.value = true
      return
    }
    ++pollCount
    const controller = new AbortController()
    readController = controller
    reading.value = true
    let completed = false
    try {
      const result = await api.getStatusCheck(targetCamera, targetCheck.checkId, controller.signal)
      if (!viewing(targetCamera, targetProfile, current)) return
      if (result.streamProfileId !== targetProfile || result.layer !== layer.value)
        throw invalidResponse({ traceId: result.traceId })
      applyCheck(result)
      failures = 0
      error.value = null
      completed = isTerminal(result)
    } catch (cause) {
      if (viewing(targetCamera, targetProfile, current)) {
        error.value = cause
        ++failures
      }
    } finally {
      if (viewing(targetCamera, targetProfile, current)) {
        reading.value = false
        readController = undefined
        if (completed) await readSnapshot(current)
        else schedule(current)
      }
    }
  }
  async function refresh() {
    if (disposed || !enabled.value || reading.value) return
    stopReads()
    pollCount = 0
    failures = 0
    readPaused.value = false
    await readSnapshot()
  }
  async function submit() {
    if (disposed || !enabled.value || posting.value || !cameraId.value || !profileId.value) return
    if (pending.value && !pendingHere.value) return
    if (!pending.value) {
      if (!canSubmit.value) return
      pending.value = {
        cameraId: cameraId.value,
        input: {
          layer: layer.value,
          streamProfileId: profileId.value,
          clientRequestId: `${Date.now()}-${crypto.randomUUID()}`,
        },
        uncertain: false,
        error: null,
      }
    }
    const command = pending.value,
      current = generation
    posting.value = true
    postController = new AbortController()
    try {
      const result = await api.createStatusCheck(
        command.cameraId,
        command.input,
        postController.signal,
      )
      if (disposed) return
      if (pending.value === command) pending.value = null
      if (
        viewing(command.cameraId, command.input.streamProfileId, current) &&
        command.input.layer === layer.value
      ) {
        stopReads()
        applyCheck(result)
        error.value = null
        pollCount = 0
        failures = 0
        readPaused.value = false
        if (isTerminal(result)) await readSnapshot()
        else schedule(generation)
      } else if (
        enabled.value &&
        cameraId.value === command.cameraId &&
        profileId.value === command.input.streamProfileId &&
        layer.value === command.input.layer
      ) {
        // Reactivation reads current server state; it never replays the POST.
        await refresh()
      }
    } catch (cause) {
      if (disposed) return
      if (isRequestRejected(cause)) {
        if (pending.value === command) pending.value = null
        if (viewing(command.cameraId, command.input.streamProfileId, current)) error.value = cause
      } else if (pending.value === command) {
        pending.value = { ...command, uncertain: true, error: cause }
      }
    } finally {
      if (!disposed) posting.value = false
      postController = undefined
    }
  }

  const unwatch = watch(
    [cameraId, profileId, enabled, layer],
    ([nextCamera], [previousCamera]) => {
      stopReads()
      localCheck.value = null
      if (nextCamera !== previousCamera) snapshot.value = null
      error.value = null
      pollCount = 0
      failures = 0
      readPaused.value = false
      if (enabled.value) void readSnapshot()
    },
    { immediate: true, flush: 'sync' },
  )
  function visibilityChanged() {
    visible.value = !document.hidden
  }
  if (typeof document !== 'undefined')
    document.addEventListener('visibilitychange', visibilityChanged)
  if (getCurrentInstance()) {
    onDeactivated(() => {
      activated.value = false
    })
    onActivated(() => {
      activated.value = true
    })
  }
  onScopeDispose(() => {
    disposed = true
    stopReads()
    postController?.abort()
    unwatch()
    if (typeof document !== 'undefined')
      document.removeEventListener('visibilitychange', visibilityChanged)
  })
  return {
    snapshot,
    selectedProfile,
    selected,
    check,
    pending,
    pendingHere,
    canSubmit,
    reading,
    posting,
    readPaused,
    error,
    refresh,
    submit,
  }
}
