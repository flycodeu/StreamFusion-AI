import { boolean, id, integer, list, object, optionalString, string } from '../parse'

export type SecretAction = 'KEEP' | 'REPLACE' | 'CLEAR'
export type SecretWrite = { action: SecretAction; value?: string }
export type UsageHint = 'MAIN' | 'SUB' | 'THIRD' | 'CUSTOM' | 'UNKNOWN'
export interface LocatorInput {
  hostMode: 'SOURCE' | 'EXPLICIT'
  host?: string
  port?: number
  pathSecret: SecretWrite
  querySecret: SecretWrite
  transport: 'TCP'
}
export interface FullRtspLocatorInput {
  fullUrl: string
  transport: 'TCP'
}

export type Lifecycle = 'PENDING_ASSIGNMENT' | 'ENABLED' | 'DISABLED'
export interface CameraAddress {
  kind: 'DEVICE' | 'PLATFORM' | 'RTSP'
  host: string
  port: number | null
}
export function parseCameraAddress(value: unknown): CameraAddress | null {
  if (value == null) return null
  const row = object(value)
  const kind = string(row.kind)
  if (!['DEVICE', 'PLATFORM', 'RTSP'].includes(kind)) throw new Error('address kind')
  const host = string(row.host)
  const port = row.port == null ? null : integer(row.port)
  if (!host.trim() || /[\s/@?#]/.test(host) || (port !== null && (port < 1 || port > 65535)))
    throw new Error('camera address')
  return { kind: kind as CameraAddress['kind'], host, port }
}
export interface LocatorSummary {
  editable: boolean
  locatorKind: string | null
  configured: boolean
  hostMode: 'SOURCE' | 'EXPLICIT' | null
  host: string | null
  port: number | null
  transport: string | null
}
export interface StreamClassification {
  usageHint: string
  origin: string
  rule: string | null
}
export function parseClassification(value: unknown): StreamClassification | undefined {
  if (value == null) return undefined
  const row = object(value)
  return {
    usageHint: string(row.usageHint),
    origin: string(row.origin),
    rule: optionalString(row.rule),
  }
}
export interface CameraDeviceGroup {
  statusSummary?: DeviceStatusSummary | null
  groupKey: string
  name: string
  identified: boolean
  manufacturer: string | null
  model: string | null
  sourceDisplayName: string
  sourceType: string | null
  connectionCategory: string
  accessAddress?: CameraAddress | null
  channelCount: number
  enabledCount: number
  disabledCount: number
  pendingCount: number
  groupCount?: number | null
  groupPath?: string | null
}
export interface DeviceStatusSummary {
  state: 'ACCESSIBLE' | 'FAILED' | 'PARTIAL' | 'CHECKING' | 'STALE' | 'NEVER'
  checkedAt: string | null
  validUntil: string | null
  reachableCount: number
  failedCount: number
  unknownCount: number
}
export function parseDeviceStatus(value: unknown): DeviceStatusSummary | null {
  if (value == null) return null
  const row = object(value),
    state = string(row.state)
  if (!['ACCESSIBLE', 'FAILED', 'PARTIAL', 'CHECKING', 'STALE', 'NEVER'].includes(state))
    throw new Error('device status')
  const checkedAt = optionalString(row.checkedAt),
    validUntil = optionalString(row.validUntil)
  if (
    (checkedAt !== null && !Number.isFinite(Date.parse(checkedAt))) ||
    (validUntil !== null && !Number.isFinite(Date.parse(validUntil)))
  )
    throw new Error('status time')
  const counts = [row.reachableCount, row.failedCount, row.unknownCount].map(integer)
  if (counts.some((count) => count < 0)) throw new Error('status counts')
  return {
    state: state as DeviceStatusSummary['state'],
    checkedAt,
    validUntil,
    reachableCount: counts[0]!,
    failedCount: counts[1]!,
    unknownCount: counts[2]!,
  }
}
export interface CameraDeviceInfo {
  deviceId: string
  sourceId: string
  name: string
  localName: string | null
  sourceName: string | null
  remark: string | null
  version: string
}
export function parseDeviceInfo(value: unknown): CameraDeviceInfo {
  const row = object(value)
  return {
    deviceId: id(row.deviceId),
    sourceId: id(row.sourceId),
    name: string(row.name),
    localName: optionalString(row.localName),
    sourceName: optionalString(row.sourceName),
    remark: optionalString(row.remark),
    version: id(row.version),
  }
}
export function parseDeviceGroup(value: unknown): CameraDeviceGroup {
  const row = object(value),
    groupKey = string(row.groupKey)
  if (!/^[dc][1-9][0-9]{0,18}$/.test(groupKey)) throw new Error('groupKey')
  return {
    groupKey,
    name: string(row.name),
    identified: boolean(row.identified),
    manufacturer: optionalString(row.manufacturer),
    model: optionalString(row.model),
    sourceDisplayName: string(row.sourceDisplayName),
    sourceType: optionalString(row.sourceType),
    connectionCategory: string(row.connectionCategory),
    accessAddress: parseCameraAddress(row.accessAddress),
    statusSummary: parseDeviceStatus(row.statusSummary),
    channelCount: integer(row.channelCount),
    enabledCount: integer(row.enabledCount),
    disabledCount: integer(row.disabledCount),
    pendingCount: integer(row.pendingCount),
    groupCount: row.groupCount == null ? null : integer(row.groupCount),
    groupPath: optionalString(row.groupPath),
  }
}
export interface CameraProfile {
  classification?: StreamClassification
  streamProfileId: string
  label: string
  usageHint: string
  usageOrigin: string
  enabled: boolean
  version: string
  videoCodec: string | null
  width: number | null
  height: number | null
  frameRate: number | null
  bitrateKbps: number | null
  audioCodec: string | null
  hasAudio: boolean | null
  parametersObservedAt: string | null
  parametersOrigin: string | null
  locatorSummary: LocatorSummary
}
export interface Camera {
  cameraId: string
  name: string
  remark: string | null
  sourceName: string | null
  sourceDisplayName: string
  sourceType: string | null
  connectionCategory: string | null
  accessAddress?: CameraAddress | null
  vendorHint: string | null
  groupId: string | null
  groupPath: string | null
  lifecycle: Lifecycle
  defaultPreviewProfileId: string | null
  version: string
  createdAt: string
  updatedAt: string
  profiles: CameraProfile[]
  sourceId: string | null
  sourceVersion: string | null
  deviceId: string | null
  deviceSummary: Record<string, unknown> | null
}
export interface InitialProfile {
  clientKey: string
  label: string
  usageHint: UsageHint
  enabled: boolean
  locatorKind: 'RTSP'
  locator: LocatorInput | FullRtspLocatorInput
}
export interface ManualCameraConnection {
  host: string
  port?: number
  scheme?: 'http' | 'https'
  adapterType?: string
  vendorHint?: string
  username?: string
  password?: string
}
export interface ManualCameraCreate {
  clientRequestId: string
  name: string
  remark?: string | null
  connection: ManualCameraConnection
  profiles?: []
}
export interface CameraUpdate {
  version: string
  name?: string
  remark?: string | null
  defaultPreviewProfileId?: string | null
  groupId?: string
  lifecycle?: Lifecycle
  confirmation?: string
}
export interface ProfileUpdate {
  version: string
  label?: string
  usageHint?: UsageHint
  enabled?: boolean
  locator?: LocatorInput | FullRtspLocatorInput
  cameraVersion?: string
  replacementDefaultProfileId?: string | null
}
const nullableId = (v: unknown) => (v == null ? null : id(v))
const numeric = (v: unknown) => {
  if (v == null) return null
  if (typeof v !== 'number' || !Number.isFinite(v)) throw new Error('number')
  return v
}
export function parseProfile(value: unknown): CameraProfile {
  const row = object(value),
    locator = object(row.locatorSummary)
  const mode = optionalString(locator.hostMode)
  if (mode !== null && mode !== 'SOURCE' && mode !== 'EXPLICIT') throw new Error('hostMode')
  return {
    streamProfileId: id(row.streamProfileId),
    label: string(row.label),
    usageHint: string(row.usageHint),
    usageOrigin: string(row.usageOrigin),
    ...(row.classification == null
      ? {}
      : { classification: parseClassification(row.classification) }),
    enabled: boolean(row.enabled),
    version: id(row.version),
    videoCodec: optionalString(row.videoCodec),
    width: numeric(row.width),
    height: numeric(row.height),
    frameRate: numeric(row.frameRate),
    bitrateKbps: numeric(row.bitrateKbps),
    audioCodec: optionalString(row.audioCodec),
    hasAudio: row.hasAudio == null ? null : boolean(row.hasAudio),
    parametersObservedAt: optionalString(row.parametersObservedAt),
    parametersOrigin: optionalString(row.parametersOrigin),
    locatorSummary: {
      editable:
        locator.editable == null ? locator.locatorKind === 'RTSP' : boolean(locator.editable),
      locatorKind: optionalString(locator.locatorKind),
      configured: boolean(locator.configured),
      hostMode: mode,
      host: optionalString(locator.host),
      port: locator.port == null ? null : integer(locator.port),
      transport: optionalString(locator.transport),
    },
  }
}
export function parseCamera(value: unknown): Camera {
  const row = object(value),
    lifecycle = string(row.lifecycle)
  if (!['PENDING_ASSIGNMENT', 'ENABLED', 'DISABLED'].includes(lifecycle))
    throw new Error('lifecycle')
  return {
    cameraId: id(row.cameraId),
    name: string(row.name),
    remark: optionalString(row.remark),
    sourceName: optionalString(row.sourceName),
    sourceDisplayName: string(row.sourceDisplayName),
    sourceType: optionalString(row.sourceType),
    connectionCategory: optionalString(row.connectionCategory),
    accessAddress: parseCameraAddress(row.accessAddress),
    vendorHint: optionalString(row.vendorHint),
    groupId: nullableId(row.groupId),
    groupPath: optionalString(row.groupPath),
    lifecycle: lifecycle as Lifecycle,
    defaultPreviewProfileId: nullableId(row.defaultPreviewProfileId),
    version: id(row.version),
    createdAt: string(row.createdAt),
    updatedAt: string(row.updatedAt),
    profiles: row.profiles == null ? [] : list(row.profiles, parseProfile),
    sourceId: nullableId(row.sourceId),
    sourceVersion: nullableId(row.sourceVersion),
    deviceId: nullableId(row.deviceId),
    deviceSummary: row.deviceSummary == null ? null : object(row.deviceSummary),
  }
}
