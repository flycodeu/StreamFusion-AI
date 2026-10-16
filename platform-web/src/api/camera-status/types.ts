import { boolean, id, integer, list, object, optionalString, page, string } from '../parse'
import type { Page } from '../parse'
import { parseCameraAddress } from '../camera/types'
import type { CameraAddress, Lifecycle } from '../camera/types'
import { isTraceId, isUtcTimestamp } from '../../lib/http/error'

export type ObservationValue = 'ACCESSIBLE' | 'FAILED' | 'UNSUPPORTED'
export type Freshness = 'NEVER' | 'CURRENT' | 'STALE'
export type StatusLayer = 'CONTROL' | 'MEDIA' | 'UPSTREAM'
export type ProbeKind = 'RTSP_DESCRIBE' | 'ZLM_RTP_FRAMES' | 'HIK_PLATFORM_ONLINE'
export function layerKey(layer: StatusLayer) {
  return layer === 'UPSTREAM' ? 'upstream' : layer === 'MEDIA' ? 'media' : 'control'
}
export type ExecutionState =
  'QUEUED' | 'RUNNING' | 'COMPLETED' | 'SKIPPED' | 'CANCELLED' | 'INTERRUPTED'

export interface StatusObservation {
  value: ObservationValue
  reasonCode: string
  observedAt: string
  readAt: string
  validUntil: string
  checkId: string
  mediaEvidence: MediaEvidence | null
}
export interface MediaEvidence {
  windowStartedAt: string
  windowEndedAt: string
  firstFrames: number
  lastFrames: number
  sampleCount: number
  maxGapMs: number
}
export interface StatusCheck {
  checkId: string
  cameraId: string
  streamProfileId: string
  layer: StatusLayer
  probeKind: ProbeKind
  executionState: ExecutionState
  requestedAt: string
  startedAt: string | null
  finishedAt: string | null
  deadlineAt: string
  observation: StatusObservation | null
  reasonCode: string | null
  durationMs: number | null
  traceId: string
  snapshotApplied: boolean
}
export interface ProfileStatus {
  streamProfileId: string
  control: LayerStatus
  media: LayerStatus
  upstream?: LayerStatus
}
export interface LayerStatus {
  layer: StatusLayer
  probeKind: ProbeKind
  observation: StatusObservation | null
  freshness: Freshness
  configurationCurrent: boolean
  latestCheck: StatusCheck | null
  canCheck: boolean
  blockingReasonCode: string | null
  timeline: StatusTimeline | null
}
export interface StatusTimeline {
  stateSince: string
  lastAccessibleAt: string | null
  lastFailedAt: string | null
}
export interface CameraStatus {
  cameraId: string
  serverNow: string
  profiles: ProfileStatus[]
}
export interface StatusCheckInput {
  layer: StatusLayer
  streamProfileId: string
  clientRequestId: string
}
export type ObservationFilter = 'NEVER' | ObservationValue
export interface StatusProfile extends ProfileStatus {
  name: string | null
  streamKind: string | null
  enabled: boolean
}
export interface StatusChannel {
  cameraId: string
  groupKey?: string
  defaultPreviewProfileId?: string | null
  name: string
  deviceName: string | null
  accessAddress?: CameraAddress | null
  lifecycle: Lifecycle
  profiles: StatusProfile[]
}
export interface StatusPage extends Page<StatusChannel> {
  serverNow: string
  summary: StatusSummary
}
export interface StatusSummary {
  total: number
  accessible: number
  failed: number
  checking: number
  stale: number
  unknown: number
  disabled: number
}
export interface StatusSearch {
  layer: StatusLayer
  page: number
  size: number
  name?: string
  observation?: ObservationFilter
}

function choice<T extends string>(value: unknown, values: readonly T[]): T {
  const result = string(value)
  if (!values.includes(result as T)) throw new Error('status enum')
  return result as T
}
function entityId(value: unknown): string {
  const result = id(value)
  if (result === '0') throw new Error('status id')
  return result
}
function instant(value: unknown): string {
  if (!isUtcTimestamp(value)) throw new Error('status timestamp')
  return value
}
const nullableInstant = (value: unknown) => (value === null ? null : instant(value))
function reason(value: unknown): string {
  const result = string(value)
  if (!/^[A-Z][A-Z0-9_]{0,63}$/.test(result)) throw new Error('status reason')
  return result
}
const nullableReason = (value: unknown) => (value === null ? null : reason(value))
function observation(value: unknown): StatusObservation | null {
  if (value === null) return null
  const row = object(value)
  return {
    value: choice(row.value, ['ACCESSIBLE', 'FAILED', 'UNSUPPORTED']),
    reasonCode: reason(row.reasonCode),
    observedAt: instant(row.observedAt),
    readAt: instant(row.readAt),
    validUntil: instant(row.validUntil),
    checkId: entityId(row.checkId),
    mediaEvidence: mediaEvidence(row.mediaEvidence),
  }
}
function mediaEvidence(value: unknown): MediaEvidence | null {
  if (value === null) return null
  const row = object(value)
  const result = {
    windowStartedAt: instant(row.windowStartedAt),
    windowEndedAt: instant(row.windowEndedAt),
    firstFrames: integer(row.firstFrames),
    lastFrames: integer(row.lastFrames),
    sampleCount: integer(row.sampleCount),
    maxGapMs: integer(row.maxGapMs),
  }
  if (
    Date.parse(result.windowEndedAt) - Date.parse(result.windowStartedAt) < 5000 ||
    result.firstFrames < 0 ||
    result.lastFrames < result.firstFrames ||
    result.sampleCount < 3 ||
    result.maxGapMs < 0
  )
    throw new Error('media evidence bounds')
  return result
}
function timeline(value: unknown, fact: StatusObservation | null): StatusTimeline | null {
  if (value == null) return null
  const row = object(value)
  const result = {
    stateSince: instant(row.stateSince),
    lastAccessibleAt: nullableInstant(row.lastAccessibleAt),
    lastFailedAt: nullableInstant(row.lastFailedAt),
  }
  if (
    !fact ||
    Object.values(result).some((time) => time && Date.parse(time) > Date.parse(fact.observedAt))
  )
    throw new Error('status timeline bounds')
  return result
}
export function parseStatusCheck(value: unknown): StatusCheck {
  const row = object(value)
  const result: StatusCheck = {
    checkId: entityId(row.checkId),
    cameraId: entityId(row.cameraId),
    streamProfileId: entityId(row.streamProfileId),
    layer: choice(row.layer, ['CONTROL', 'MEDIA', 'UPSTREAM']),
    probeKind: choice(row.probeKind, ['RTSP_DESCRIBE', 'ZLM_RTP_FRAMES', 'HIK_PLATFORM_ONLINE']),
    executionState: choice(row.executionState, [
      'QUEUED',
      'RUNNING',
      'COMPLETED',
      'SKIPPED',
      'CANCELLED',
      'INTERRUPTED',
    ]),
    requestedAt: instant(row.requestedAt),
    startedAt: nullableInstant(row.startedAt),
    finishedAt: nullableInstant(row.finishedAt),
    deadlineAt: instant(row.deadlineAt),
    observation: observation(row.observation),
    reasonCode: nullableReason(row.reasonCode),
    durationMs: row.durationMs === null ? null : integer(row.durationMs),
    traceId: string(row.traceId),
    snapshotApplied: boolean(row.snapshotApplied),
  }
  if (!isTraceId(result.traceId) || (result.durationMs !== null && result.durationMs < 0))
    throw new Error('status check')
  if (result.observation && result.observation.checkId !== result.checkId)
    throw new Error('status observation identity')
  validateLayer(result.layer, result.probeKind, result.observation)
  return result
}
function validateLayer(layer: StatusLayer, probe: ProbeKind, fact: StatusObservation | null) {
  if (
    { CONTROL: 'RTSP_DESCRIBE', MEDIA: 'ZLM_RTP_FRAMES', UPSTREAM: 'HIK_PLATFORM_ONLINE' }[
      layer
    ] !== probe
  )
    throw new Error('status layer probe')
  if (layer !== 'MEDIA' && fact?.mediaEvidence) throw new Error('control media evidence')
  if (
    layer === 'MEDIA' &&
    fact &&
    (!fact.mediaEvidence ||
      fact.value === 'UNSUPPORTED' ||
      Date.parse(fact.mediaEvidence.windowEndedAt) > Date.parse(fact.observedAt))
  )
    throw new Error('incomplete media observation')
}
function parseLayer(
  value: unknown,
  layer: StatusLayer,
  cameraId: string,
  profileId: string,
): LayerStatus {
  const row = object(value)
  const fact = observation(row.observation)
  const result: LayerStatus = {
    layer: choice(row.layer, [layer]),
    probeKind: choice(row.probeKind, ['RTSP_DESCRIBE', 'ZLM_RTP_FRAMES', 'HIK_PLATFORM_ONLINE']),
    observation: fact,
    freshness: choice(row.freshness, ['NEVER', 'CURRENT', 'STALE']),
    configurationCurrent: boolean(row.configurationCurrent),
    latestCheck: row.latestCheck === null ? null : parseStatusCheck(row.latestCheck),
    canCheck: boolean(row.canCheck),
    blockingReasonCode: nullableReason(row.blockingReasonCode),
    timeline: timeline(row.timeline, fact),
  }
  validateLayer(result.layer, result.probeKind, fact)
  if (
    result.latestCheck &&
    (result.latestCheck.cameraId !== cameraId ||
      result.latestCheck.streamProfileId !== profileId ||
      result.latestCheck.layer !== layer)
  )
    throw new Error('status profile identity')
  if ((result.freshness === 'NEVER') !== (result.observation === null))
    throw new Error('status freshness')
  return result
}
export function parseCameraStatus(value: unknown): CameraStatus {
  const row = object(value)
  const cameraId = entityId(row.cameraId)
  const profiles = list(row.profiles, (entry): ProfileStatus => {
    const profile = object(entry)
    const streamProfileId = entityId(profile.streamProfileId)
    return {
      streamProfileId,
      control: parseLayer(profile.control, 'CONTROL', cameraId, streamProfileId),
      media: parseLayer(profile.media, 'MEDIA', cameraId, streamProfileId),
      ...(profile.upstream == null
        ? {}
        : { upstream: parseLayer(profile.upstream, 'UPSTREAM', cameraId, streamProfileId) }),
    }
  })
  if (new Set(profiles.map((profile) => profile.streamProfileId)).size !== profiles.length)
    throw new Error('duplicate status profile')
  return { cameraId, serverNow: instant(row.serverNow), profiles }
}

export function parseStatusPage(value: unknown): StatusPage {
  const row = object(value)
  const serverNow = instant(row.serverNow)
  const result = page(row, (entry): StatusChannel => {
    const channel = object(entry)
    const snapshot = parseCameraStatus({ ...channel, serverNow })
    return {
      cameraId: snapshot.cameraId,
      groupKey: parseGroupKey(channel.groupKey),
      defaultPreviewProfileId:
        channel.defaultPreviewProfileId == null ? null : entityId(channel.defaultPreviewProfileId),
      name: string(channel.name),
      deviceName: optionalString(channel.deviceName),
      ...(channel.accessAddress === undefined
        ? {}
        : { accessAddress: parseCameraAddress(channel.accessAddress) }),
      lifecycle: choice(channel.lifecycle, ['PENDING_ASSIGNMENT', 'ENABLED', 'DISABLED']),
      profiles: snapshot.profiles.map((profile, index) => {
        const metadata = object((channel.profiles as unknown[])[index])
        return {
          ...profile,
          name: optionalString(metadata.name),
          streamKind: optionalString(metadata.streamKind),
          enabled: boolean(metadata.enabled),
        }
      }),
    }
  })
  if (new Set(result.items.map((channel) => channel.cameraId)).size !== result.items.length)
    throw new Error('duplicate status channel')
  const summaryRow = object(row.summary)
  const summary: StatusSummary = {
    total: integer(summaryRow.total),
    accessible: integer(summaryRow.accessible),
    failed: integer(summaryRow.failed),
    checking: integer(summaryRow.checking),
    stale: integer(summaryRow.stale),
    unknown: integer(summaryRow.unknown),
    disabled: integer(summaryRow.disabled),
  }
  const { total, ...counts } = summary
  if (
    total !== result.items.length ||
    Object.values(counts).some((count) => count < 0) ||
    Object.values(counts).reduce((sum, count) => sum + count, 0) !== total
  )
    throw new Error('status summary bounds')
  return { ...result, serverNow, summary }
}

function parseGroupKey(value: unknown): string {
  const result = string(value)
  if (!/^[dc][1-9][0-9]{0,18}$/.test(result)) throw new Error('status group key')
  entityId(result.slice(1))
  return result
}
