import { computed, onBeforeUnmount, onMounted, ref, shallowRef } from 'vue'
import * as api from '../../api/camera-access/api'
import type {
  AccessConnection,
  AccessJob,
  AccessOptions,
  CandidateSelection,
  ImportImpact,
  ImportInput,
  ImportResult,
} from '../../api/camera-access/types'
import { usePageScope } from '../../composables/usePageScope'
import { sessionState } from '../../session/state'
import { formError, isRequestRejected as rejected, useCreateRequest } from '../camera/form'
import { connectionError, connectionInput, initialSelections, newConnection } from './form'

export type AccessMode = 'device' | 'platform' | 'rtsp'
export interface ExistingCameraTarget {
  cameraId: string
  version: string
  name: string
}
export function useAccessWizard(
  close: () => void,
  mode: AccessMode = 'device',
  initial?: { sourceId: string; sourceVersion: string; method?: string },
  target?: ExistingCameraTarget,
) {
  const captureScope = usePageScope()
  const options = ref<AccessOptions | null>(null),
    connection = ref(newConnection()),
    job = ref<AccessJob | null>(null)
  const jobId = ref<string | null>(null),
    selections = ref<CandidateSelection[]>([]),
    groupId = ref<string | null>(null)
  const impact = ref<ImportImpact | null>(null),
    result = ref<ImportResult | null>(null),
    error = ref<unknown>(null)
  const confirmedInput = shallowRef<ImportInput | null>(null)
  const loading = ref(true),
    busy = ref(false),
    refreshing = ref(false),
    paused = ref(false)
  const creation = useCreateRequest<{ connection: AccessConnection }>()
  const importing = useCreateRequest<ImportInput & { confirmation: string }>()
  const createFrozen = computed(() => !!creation.pending.value)
  const importFrozen = computed(() => !!importing.pending.value)
  const running = computed(
    () =>
      !!jobId.value &&
      (!job.value || job.value.status === 'QUEUED' || job.value.status === 'RUNNING'),
  )
  const selectable = computed(
    () =>
      !!job.value &&
      job.value.kind !== 'BULK_IMPORT' &&
      ['SUCCEEDED', 'PARTIAL'].includes(job.value.status) &&
      !result.value,
  )
  const step = computed(() => (result.value ? 3 : impact.value ? 2 : jobId.value ? 1 : 0))
  let timer: ReturnType<typeof setTimeout> | undefined,
    reads = 0,
    sequence = 0
  const pageNumber = ref(1),
    accumulatedIds = ref<string[]>([])
  let savedConnection: AccessConnection | null = null
  let keepJobRunning = false
  let ownerEpoch = sessionState.epoch
  const stopPolling = () => {
    if (timer) clearTimeout(timer)
    timer = undefined
  }
  function acceptImport(value: ImportResult) {
    result.value = value
    impact.value = null
    confirmedInput.value = null
    accumulatedIds.value = [
      ...new Set([...accumulatedIds.value, ...value.cameras.map((camera) => camera.cameraId)]),
    ]
    if (value.sourceId && value.sourceVersion)
      savedConnection = {
        method: job.value!.method,
        sourceId: value.sourceId,
        sourceVersion: value.sourceVersion,
      }
    importing.reset()
  }
  async function loadOptions() {
    const active = captureScope()
    loading.value = true
    error.value = null
    try {
      const value = await api.getAccessOptions()
      if (!active()) return
      const category = mode === 'platform' ? 'PLATFORM' : mode === 'rtsp' ? 'RTSP' : 'DEVICE'
      const allowed = value.adapters
        .filter((adapter) => adapter.category === category)
        .map((adapter) => adapter.type)
      if (mode === 'device' && value.methods.includes('AUTO')) allowed.unshift('AUTO')
      options.value = { ...value, methods: [...new Set(allowed)] }
      connection.value.method = options.value.methods.includes('AUTO')
        ? 'AUTO'
        : (options.value.methods[0] ?? '')
      connection.value.networkPolicyKey = value.networkPolicies[0]?.key ?? ''
      if (initial)
        Object.assign(connection.value, {
          reuse: true,
          sourceId: initial.sourceId,
          sourceVersion: initial.sourceVersion,
          method: initial.method ?? connection.value.method,
        })
    } catch (cause) {
      if (active()) error.value = cause
    } finally {
      if (active()) loading.value = false
    }
  }
  async function refreshJob() {
    if (!jobId.value || refreshing.value) return
    stopPolling()
    const active = captureScope(),
      current = sequence
    refreshing.value = true
    paused.value = false
    error.value = null
    try {
      const value = await api.getAccessJob(jobId.value)
      if (!active() || current !== sequence) return
      job.value = value
      if (value.result) acceptImport(value.result)
      if (value.status === 'SUCCEEDED' || value.status === 'PARTIAL')
        selections.value = target ? [] : initialSelections(value.candidates)
      if (value.status === 'QUEUED' || value.status === 'RUNNING') {
        if (++reads < 180)
          timer = setTimeout(() => {
            if (active() && current === sequence) void refreshJob()
          }, 2000)
        else paused.value = true
      }
    } catch (cause) {
      if (active() && current === sequence) {
        error.value = cause
        paused.value = true
      }
    } finally {
      if (active() && current === sequence) refreshing.value = false
    }
  }
  async function start(page = 1) {
    if (busy.value || !options.value?.ready) return
    error.value = null
    const adapter = options.value.adapters?.find((item) => item.type === connection.value.method)
    const message =
      createFrozen.value || savedConnection ? '' : connectionError(connection.value, adapter)
    if (message) {
      error.value = formError(message)
      return
    }
    const active = captureScope()
    busy.value = true
    try {
      const base = savedConnection ?? connectionInput(connection.value, adapter)
      const command = {
        ...base,
        ...(mode === 'platform' ? { pageNumber: page, pageSize: 100 } : {}),
      }
      const captured = creation.capture({ connection: command })
      const created = await api.createAccessJob(captured)
      if (!active()) return
      savedConnection = base
      pageNumber.value = captured.connection.pageNumber ?? 1
      ownerEpoch = sessionState.epoch
      jobId.value = created.jobId
      keepJobRunning = false
      job.value = null
      selections.value = []
      result.value = null
      impact.value = null
      confirmedInput.value = null
      creation.reset()
      connection.value.password = ''
      connection.value.username = ''
      connection.value.rtspUrls = ['']
      reads = 0
      await refreshJob()
    } catch (cause) {
      if (active()) {
        error.value = cause
        if (rejected(cause)) creation.reset()
      }
    } finally {
      if (active()) busy.value = false
    }
  }
  async function cancelAndReset(shouldClose = false) {
    if (busy.value || importFrozen.value) return
    const active = captureScope()
    busy.value = true
    stopPolling()
    try {
      if (jobId.value && !result.value) await api.cancelAccessJob(jobId.value)
      if (!active()) return
      sequence++
      jobId.value = null
      job.value = null
      selections.value = []
      impact.value = null
      confirmedInput.value = null
      result.value = null
      creation.reset()
      savedConnection = null
      pageNumber.value = 1
      error.value = null
      paused.value = false
      refreshing.value = false
      if (shouldClose) close()
    } catch (cause) {
      if (active()) {
        error.value = cause
        if (shouldClose) close()
      }
    } finally {
      if (active()) busy.value = false
    }
  }
  function input(): ImportInput {
    return {
      version: job.value!.version,
      selections: structuredClone(
        selections.value.map(({ defaultProfileId, ...selection }) => ({
          ...selection,
          profileIds: [...selection.profileIds],
          ...(defaultProfileId ? { defaultProfileId } : {}),
        })),
      ),
      ...(!target && groupId.value ? { groupId: groupId.value } : {}),
      ...(target ? { targetCameraId: target.cameraId, targetCameraVersion: target.version } : {}),
    }
  }
  async function preview() {
    if (busy.value || !selectable.value || !jobId.value) return
    error.value = null
    if (target && selections.value.length !== 1) {
      error.value = formError(
        '请选择一个与当前档案对应的通道。多通道设备请分别核对，不能全部绑定到同一相机。',
      )
      return
    }
    if (
      !selections.value.length ||
      selections.value.some(
        (selection) =>
          !!selection.defaultProfileId &&
          !selection.profileIds.includes(selection.defaultProfileId),
      )
    ) {
      error.value = formError('请选择需要登记的通道；如指定默认码流，必须选择已勾选的码流。')
      return
    }
    const active = captureScope()
    busy.value = true
    try {
      const command = input()
      const value = await api.previewImport(jobId.value, command)
      if (active()) {
        impact.value = value
        confirmedInput.value = command
      }
    } catch (cause) {
      if (active()) error.value = cause
    } finally {
      if (active()) busy.value = false
    }
  }
  async function confirmImport() {
    if (busy.value || !impact.value || !confirmedInput.value || !jobId.value) return
    const active = captureScope()
    busy.value = true
    error.value = null
    try {
      const value = await api.importCandidates(
        jobId.value,
        importing.capture({ ...confirmedInput.value, confirmation: impact.value.confirmation }),
      )
      if (!active()) return
      acceptImport(value)
    } catch (cause) {
      if (active()) {
        error.value = cause
        if (rejected(cause)) {
          importing.reset()
          impact.value = null
          confirmedInput.value = null
        }
      }
    } finally {
      if (active()) busy.value = false
    }
  }
  async function changePage(next: number) {
    if (busy.value || running.value || createFrozen.value || importFrozen.value || next < 1) return
    const active = captureScope()
    stopPolling()
    busy.value = true
    if (jobId.value && !result.value) {
      try {
        const cancelled = await api.cancelAccessJob(jobId.value)
        if (active()) {
          job.value = cancelled
          selections.value = []
        }
      } catch (cause) {
        if (active()) {
          error.value = cause
          busy.value = false
        }
        return
      }
    }
    if (!active()) return
    busy.value = false
    sequence++
    await start(next)
  }
  function reviseSelection() {
    if (importFrozen.value || busy.value) return
    impact.value = null
    confirmedInput.value = null
    error.value = null
  }
  function detachJob() {
    keepJobRunning = true
  }
  async function restartFromSource(value: AccessJob) {
    if (busy.value || !value.sourceId || !value.sourceVersion) return
    sequence++
    stopPolling()
    jobId.value = null
    job.value = null
    result.value = null
    impact.value = null
    confirmedInput.value = null
    selections.value = []
    creation.reset()
    importing.reset()
    savedConnection = null
    groupId.value = value.bulk?.groupId ?? null
    Object.assign(connection.value, {
      method: value.method,
      reuse: true,
      sourceId: value.sourceId,
      sourceVersion: value.sourceVersion,
      networkPolicyKey: '',
    })
    await start(1)
  }
  onMounted(loadOptions)
  onBeforeUnmount(() => {
    sequence++
    stopPolling()
    connection.value.password = ''
    connection.value.username = ''
    connection.value.rtspUrls = ['']
    savedConnection = null
    creation.reset()
    importing.reset()
    if (
      jobId.value &&
      !keepJobRunning &&
      job.value?.kind !== 'BULK_IMPORT' &&
      !result.value &&
      sessionState.epoch === ownerEpoch
    )
      void api.cancelAccessJob(jobId.value).catch(() => {
        /* The bounded server job also expires. */
      })
  })
  return {
    pageNumber,
    accumulatedIds,
    changePage,
    options,
    connection,
    job,
    jobId,
    selections,
    groupId,
    impact,
    result,
    error,
    loading,
    busy,
    refreshing,
    paused,
    createFrozen,
    importFrozen,
    running,
    selectable,
    step,
    loadOptions,
    refreshJob,
    start,
    cancelAndReset,
    preview,
    confirmImport,
    reviseSelection,
    restartFromSource,
    detachJob,
  }
}
