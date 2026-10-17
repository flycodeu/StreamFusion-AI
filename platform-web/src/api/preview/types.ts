import { boolean, integer, list, object, optionalString, string } from '../parse'
import type { Page } from '../parse'

export type PreviewUsage = 'MAIN' | 'SUB' | 'THIRD' | 'CUSTOM' | 'UNKNOWN'
export interface PreviewProfile {
  streamProfileId: string
  label: string
  usage: PreviewUsage
  enabled: boolean
  configured: boolean
  videoCodec: string | null
  width: number | null
  height: number | null
  frameRate: number | null
  bitrateKbps: number | null
}
export interface PreviewChannel {
  cameraId: string
  name: string
  deviceGroupKey: string
  deviceName: string | null
  groupId: string | null
  groupPath: string | null
  lifecycle: string
  profiles: PreviewProfile[]
}
export interface PreviewLease {
  /** Frontend receipt context, copied from the existing response envelope. */
  receipt?: { status: number; traceId: string; timestamp: string }
  leaseId: string | null
  cameraId: string
  streamProfileId: string | null
  state: 'READY' | 'UNAVAILABLE'
  reasonCode: string | null
  expiresAt: string | null
  videoCodec: string | null
  transcoded: boolean
}
export interface PreviewStats {
  state: string
  videoCodec: string | null
  width: number | null
  height: number | null
  frameRate: number | null
  mediaKbps: number | null
  readers: number | null
  observedAt: string | null
}
export interface PreviewCapacity {
  maxSessions: number
  occupiedSessions: number
  closingSessions: number
  maxTranscodes: number
  occupiedTranscodes: number | null
  transcodesAvailable: boolean
  observedAt: string
}
export function parsePreviewCapacity(value: unknown): PreviewCapacity {
  const row = object(value)
  const result: PreviewCapacity = {
    maxSessions: integer(row.maxSessions),
    occupiedSessions: integer(row.occupiedSessions),
    closingSessions: integer(row.closingSessions),
    maxTranscodes: integer(row.maxTranscodes),
    occupiedTranscodes: row.occupiedTranscodes === null ? null : integer(row.occupiedTranscodes),
    transcodesAvailable: boolean(row.transcodesAvailable),
    observedAt: string(row.observedAt),
  }
  if (
    result.maxSessions < 0 ||
    result.maxSessions > 1088 ||
    result.occupiedSessions < 0 ||
    result.closingSessions < 0 ||
    result.closingSessions > result.occupiedSessions ||
    result.maxTranscodes < 0 ||
    result.maxTranscodes > 1088 ||
    (result.occupiedTranscodes !== null && result.occupiedTranscodes < 0) ||
    result.transcodesAvailable !== (result.occupiedTranscodes !== null) ||
    !Number.isFinite(Date.parse(result.observedAt))
  )
    throw new Error('preview capacity')
  return result
}
function nullableNumber(value: unknown): number | null {
  if (value == null) return null
  return typeof value === 'number' && Number.isFinite(value) ? value : null
}
function parseProfile(value: unknown): PreviewProfile {
  const row = object(value),
    usage = string(row.usage)
  const valid: PreviewUsage[] = ['MAIN', 'SUB', 'THIRD', 'CUSTOM', 'UNKNOWN']
  return {
    streamProfileId: string(row.streamProfileId),
    label: string(row.label),
    usage: valid.includes(usage as PreviewUsage) ? (usage as PreviewUsage) : 'UNKNOWN',
    enabled: boolean(row.enabled),
    configured: boolean(row.configured),
    videoCodec: optionalString(row.videoCodec),
    width: nullableNumber(row.width),
    height: nullableNumber(row.height),
    frameRate: nullableNumber(row.frameRate),
    bitrateKbps: nullableNumber(row.bitrateKbps),
  }
}
export function parsePreviewCatalog(value: unknown): Page<PreviewChannel> {
  const row = object(value)
  const items = list(row.items, (value) => {
    const item = object(value),
      lifecycle = string(item.lifecycle)
    return {
      cameraId: string(item.cameraId),
      name: string(item.name),
      deviceGroupKey: string(item.deviceGroupKey),
      deviceName: optionalString(item.deviceName),
      groupId: item.groupId == null ? null : string(item.groupId),
      groupPath: optionalString(item.groupPath),
      lifecycle,
      profiles: list(item.profiles, parseProfile),
    }
  })
  const page = integer(row.page),
    size = integer(row.size),
    total = integer(row.total)
  if (page < 1 || size < 1 || total < 0) throw new Error('page')
  return { items, page, size, total }
}
export function parseLease(value: unknown): PreviewLease {
  const row = object(value),
    state = string(row.state)
  if (state !== 'READY' && state !== 'UNAVAILABLE') throw new Error('lease state')
  return {
    leaseId: optionalString(row.leaseId),
    cameraId: string(row.cameraId),
    streamProfileId: optionalString(row.streamProfileId),
    state,
    reasonCode: optionalString(row.reasonCode),
    expiresAt: optionalString(row.expiresAt),
    videoCodec: optionalString(row.videoCodec),
    transcoded: boolean(row.transcoded),
  }
}
export function parseAnswer(value: unknown): { sdp: string; type: 'answer' } {
  const row = object(value),
    type = string(row.type)
  if (type !== 'answer') throw new Error('sdp type')
  return { sdp: string(row.sdp), type }
}
export function parseStats(value: unknown): PreviewStats {
  const row = object(value)
  return {
    state: string(row.state),
    videoCodec: optionalString(row.videoCodec),
    width: nullableNumber(row.width),
    height: nullableNumber(row.height),
    frameRate: nullableNumber(row.frameRate),
    mediaKbps: nullableNumber(row.mediaKbps),
    readers: row.readers == null ? null : integer(row.readers),
    observedAt: optionalString(row.observedAt),
  }
}
