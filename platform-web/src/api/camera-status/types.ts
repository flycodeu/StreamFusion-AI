import { boolean, id, integer, list, object, optionalString, page, string } from '../parse'
import type { Page } from '../parse'
import { parseCameraAddress } from '../camera/types'
import type { CameraAddress, Lifecycle } from '../camera/types'
import { isTraceId, isUtcTimestamp } from '../../lib/http/error'

export type ObservationValue = 'ACCESSIBLE' | 'FAILED' | 'UNSUPPORTED'
export type Freshness = 'NEVER' | 'CURRENT' | 'STALE'
export type ExecutionState =
  'QUEUED' | 'RUNNING' | 'COMPLETED' | 'SKIPPED' | 'CANCELLED' | 'INTERRUPTED'

export interface StatusObservation {
  value: ObservationValue
  reasonCode: string
  observedAt: string
  readAt: string
  validUntil: string
  checkId: string
}
export interface StatusCheck {
  checkId: string
  cameraId: string
  streamProfileId: string
  probeKind: 'RTSP_DESCRIBE'
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
  layer: 'CONTROL'
  probeKind: 'RTSP_DESCRIBE'
  observation: StatusObservation | null
  freshness: Freshness
  configurationCurrent: boolean
  latestCheck: StatusCheck | null
  canCheck: boolean
  blockingReasonCode: string | null
  timeline?: StatusTimeline | null
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
  }
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
    probeKind: choice(row.probeKind, ['RTSP_DESCRIBE']),
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
  return result
}
export function parseCameraStatus(value: unknown): CameraStatus {
  const row = object(value)
  const cameraId = entityId(row.cameraId)
  const profiles = list(row.profiles, (entry): ProfileStatus => {
    const profile = object(entry)
    const fact = observation(profile.observation)
    const result: ProfileStatus = {
      streamProfileId: entityId(profile.streamProfileId),
      layer: choice(profile.layer, ['CONTROL']),
      probeKind: choice(profile.probeKind, ['RTSP_DESCRIBE']),
      observation: fact,
      freshness: choice(profile.freshness, ['NEVER', 'CURRENT', 'STALE']),
      configurationCurrent: boolean(profile.configurationCurrent),
      latestCheck: profile.latestCheck === null ? null : parseStatusCheck(profile.latestCheck),
      canCheck: boolean(profile.canCheck),
      blockingReasonCode: nullableReason(profile.blockingReasonCode),
      ...(profile.timeline === undefined ? {} : { timeline: timeline(profile.timeline, fact) }),
    }
    if (
      result.latestCheck &&
      (result.latestCheck.cameraId !== cameraId ||
        result.latestCheck.streamProfileId !== result.streamProfileId)
    )
      throw new Error('status profile identity')
    if ((result.freshness === 'NEVER') !== (result.observation === null))
      throw new Error('status freshness')
    return result
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
