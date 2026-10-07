import { boolean, id, integer, list, object, optionalString, string } from '../parse'

export type AccessMethod = string
export interface AccessAdapter {
  type: string
  label: string
  category: 'DEVICE' | 'PLATFORM' | 'RTSP'
  inputKind: 'DEVICE_LOGIN' | 'PLATFORM_APPKEY' | 'RTSP_URL'
  autoDetect: boolean
  paged: boolean
}
export type JobStatus =
  'QUEUED' | 'RUNNING' | 'SUCCEEDED' | 'PARTIAL' | 'FAILED' | 'CANCELLED' | 'EXPIRED'
export interface AccessOptions {
  ready: boolean
  methods: AccessMethod[]
  adapters: AccessAdapter[]
  networkPolicies: { key: string; name: string }[]
  diagnostics: string[]
}
export interface AccessConnection {
  method: AccessMethod
  name?: string
  host?: string
  port?: number
  scheme?: 'http' | 'https'
  username?: string
  password?: string
  rtspPort?: number
  networkPolicyKey?: string
  sourceId?: string
  sourceVersion?: string
  rtspUrls?: string[]
  pageNumber?: number
  pageSize?: number
}
export interface CandidateProfile {
  profileId: string
  name: string
  usageHint: string
  videoCodec: string | null
  width: number | null
  height: number | null
  frameRate: number | null
  bitrateKbps: number | null
}
export interface AccessCandidate {
  candidateId: string
  name: string
  mappingRequired: boolean
  profiles: CandidateProfile[]
}
export interface ImportResult {
  sourceId: string | null
  sourceVersion: string | null
  cameras: {
    candidateId: string
    cameraId: string
    version: string
    status: 'CREATED' | 'EXISTING'
  }[]
  createdCount: number
  existingCount: number
}
export interface AccessJob {
  kind: 'SCAN' | 'CATALOG' | 'BULK_IMPORT'
  jobId: string
  version: string
  status: JobStatus
  method: AccessMethod
  sourceId: string | null
  sourceVersion: string | null
  expiresAt: string
  complete: boolean
  device: null | {
    name: string | null
    manufacturer: string | null
    model: string | null
    firmware: string | null
    serialNumber: string | null
  }
  candidates: AccessCandidate[]
  warnings: string[]
  diagnostic: null | {
    reasonCode: string
    actionHint: string
    originTraceId: string | null
    occurredAt: string | null
  }
  result: ImportResult | null
  hosts: ScanHost[]
  scannedTargets: number
  totalTargets: number
  page: null | { pageNumber: number; pageSize: number; total: number | null; hasMore: boolean }
  bulk: null | BulkProgress
}
export interface BulkProgress {
  groupId: string | null
  pageNumber: number
  total: number | null
  processedCount: number
  createdCount: number
  existingCount: number
  failedCount: number
  duplicateCount: number
  maxItems: number
}
export interface BulkImportInput {
  version: string
  groupId?: string | null
}
export interface BulkImportImpact {
  confirmation: string
  groupPath: string | null
  affectedUserCount: number
  declaredTotal: number | null
  maxItems: number
  pageSize: number
}
export interface ImportItem {
  id: string
  externalKey: string
  pageNumber: number
  itemIndex: number
  status: 'CREATED' | 'EXISTING' | 'FAILED'
  cameraId: string | null
  name: string | null
  reasonCode: string | null
  createdAt: string
}
export interface ScanHost {
  candidateId: string
  host: string
  openPorts: number[]
  identityConfidence: string
}
export interface ScanInput {
  clientRequestId: string
  networkPolicyKey: string
  cidr?: string
  startAddress?: string
  endAddress?: string
  ports: number[]
}
export interface CandidateSelection {
  candidateId: string
  profileIds: string[]
  defaultProfileId?: string
}
export interface ImportInput {
  version: string
  selections: CandidateSelection[]
  groupId?: string
  targetCameraId?: string
  targetCameraVersion?: string
}
export interface ImportImpact {
  confirmation: string
  groupPath: string | null
  affectedUserCount: number
  cameraCount: number
}
function method(value: unknown): AccessMethod {
  return string(value)
}
function numberOrNull(value: unknown) {
  if (value == null) return null
  if (typeof value !== 'number' || !Number.isFinite(value)) throw new Error('number')
  return value
}
export function parseAccessOptions(value: unknown): AccessOptions {
  const row = object(value)
  return {
    ready: boolean(row.ready),
    methods: list(row.methods, method),
    adapters: list(row.adapters, (value) => {
      const adapter = object(value)
      if (
        !['DEVICE', 'PLATFORM', 'RTSP'].includes(string(adapter.category)) ||
        !['DEVICE_LOGIN', 'PLATFORM_APPKEY', 'RTSP_URL'].includes(string(adapter.inputKind))
      )
        throw new Error('adapter descriptor')
      return {
        type: string(adapter.type),
        label: string(adapter.label),
        category: adapter.category as AccessAdapter['category'],
        inputKind: adapter.inputKind as AccessAdapter['inputKind'],
        autoDetect: boolean(adapter.autoDetect),
        paged: boolean(adapter.paged),
      }
    }),
    diagnostics: list(row.diagnostics, string),
    networkPolicies: list(row.networkPolicies, (v) => {
      const p = object(v)
      return { key: string(p.key), name: string(p.name) }
    }),
  }
}
export function parseImportResult(value: unknown): ImportResult {
  const row = object(value)
  return {
    sourceId: row.sourceId == null ? null : id(row.sourceId),
    sourceVersion: row.sourceVersion == null ? null : id(row.sourceVersion),
    createdCount: integer(row.createdCount),
    existingCount: integer(row.existingCount),
    cameras: list(row.cameras, (v) => {
      const camera = object(v)
      if (camera.status !== 'CREATED' && camera.status !== 'EXISTING')
        throw new Error('import status')
      return {
        candidateId: string(camera.candidateId),
        cameraId: id(camera.cameraId),
        version: id(camera.version),
        status: camera.status,
      }
    }),
  }
}
export function parseAccessJob(value: unknown): AccessJob {
  const row = object(value)
  const statuses: JobStatus[] = [
    'QUEUED',
    'RUNNING',
    'SUCCEEDED',
    'PARTIAL',
    'FAILED',
    'CANCELLED',
    'EXPIRED',
  ]
  if (!statuses.includes(row.status as JobStatus)) throw new Error('job status')
  if (row.kind != null && !['SCAN', 'CATALOG', 'BULK_IMPORT'].includes(string(row.kind)))
    throw new Error('job kind')
  const device = row.device == null ? null : object(row.device)
  const diagnostic = row.diagnostic == null ? null : object(row.diagnostic)
  const candidates = list(row.candidates ?? [], (v): AccessCandidate => {
    const candidate = object(v)
    const profiles = list(candidate.profiles, (v): CandidateProfile => {
      const profile = object(v)
      return {
        profileId: string(profile.profileId),
        name: string(profile.name),
        usageHint: string(profile.usageHint),
        videoCodec: optionalString(profile.videoCodec),
        width: numberOrNull(profile.width),
        height: numberOrNull(profile.height),
        frameRate: numberOrNull(profile.frameRate),
        bitrateKbps: numberOrNull(profile.bitrateKbps),
      }
    })
    if (profiles.length > 8) throw new Error('profile limit')
    return {
      candidateId: string(candidate.candidateId),
      name: string(candidate.name),
      mappingRequired: boolean(candidate.mappingRequired),
      profiles,
    }
  })
  if (candidates.length > 256) throw new Error('candidate limit')
  return {
    kind: row.kind == null ? 'CATALOG' : (row.kind as AccessJob['kind']),
    jobId: id(row.jobId),
    version: id(row.version),
    status: row.status as JobStatus,
    method: method(row.method),
    sourceId: row.sourceId == null ? null : id(row.sourceId),
    sourceVersion: row.sourceVersion == null ? null : id(row.sourceVersion),
    expiresAt: string(row.expiresAt),
    complete: boolean(row.complete),
    candidates,
    warnings: list(row.warnings ?? [], string),
    device: device && {
      name: optionalString(device.name),
      manufacturer: optionalString(device.manufacturer),
      model: optionalString(device.model),
      firmware: optionalString(device.firmware),
      serialNumber: optionalString(device.serialNumber),
    },
    diagnostic: diagnostic && {
      reasonCode: string(diagnostic.reasonCode),
      actionHint: string(diagnostic.actionHint),
      originTraceId: optionalString(diagnostic.originTraceId),
      occurredAt: optionalString(diagnostic.occurredAt),
    },
    result: row.result == null ? null : parseImportResult(row.result),
    bulk: row.bulk == null ? null : parseBulkProgress(row.bulk),
    hosts:
      row.hosts == null
        ? []
        : list(row.hosts, (value) => {
            const host = object(value)
            return {
              candidateId: string(host.candidateId),
              host: string(host.host),
              openPorts: list(host.openPorts, integer),
              identityConfidence: string(host.identityConfidence),
            }
          }),
    scannedTargets: row.scannedTargets == null ? 0 : integer(row.scannedTargets),
    totalTargets: row.totalTargets == null ? 0 : integer(row.totalTargets),
    page:
      row.page == null
        ? null
        : (() => {
            const page = object(row.page)
            return {
              pageNumber: integer(page.pageNumber),
              pageSize: integer(page.pageSize),
              total: page.total == null ? null : integer(page.total),
              hasMore: boolean(page.hasMore),
            }
          })(),
  }
}
function parseBulkProgress(value: unknown): BulkProgress {
  const row = object(value)
  return {
    groupId: row.groupId == null ? null : id(row.groupId),
    pageNumber: integer(row.pageNumber),
    total: row.total == null ? null : integer(row.total),
    processedCount: integer(row.processedCount),
    createdCount: integer(row.createdCount),
    existingCount: integer(row.existingCount),
    failedCount: integer(row.failedCount),
    duplicateCount: integer(row.duplicateCount),
    maxItems: integer(row.maxItems),
  }
}
export function parseBulkImpact(value: unknown): BulkImportImpact {
  const row = object(value)
  return {
    confirmation: string(row.confirmation),
    groupPath: optionalString(row.groupPath),
    affectedUserCount: integer(row.affectedUserCount),
    declaredTotal: row.declaredTotal == null ? null : integer(row.declaredTotal),
    maxItems: integer(row.maxItems),
    pageSize: integer(row.pageSize),
  }
}
export function parseImportItem(value: unknown): ImportItem {
  const row = object(value)
  if (!['CREATED', 'EXISTING', 'FAILED'].includes(string(row.status)))
    throw new Error('import item status')
  return {
    id: id(row.id),
    externalKey: string(row.externalKey),
    pageNumber: integer(row.pageNumber),
    itemIndex: integer(row.itemIndex),
    status: row.status as ImportItem['status'],
    cameraId: row.cameraId == null ? null : id(row.cameraId),
    name: optionalString(row.name),
    reasonCode: optionalString(row.reasonCode),
    createdAt: string(row.createdAt),
  }
}
export function parseImportImpact(value: unknown): ImportImpact {
  const row = object(value)
  return {
    confirmation: string(row.confirmation),
    groupPath: optionalString(row.groupPath),
    affectedUserCount: integer(row.affectedUserCount),
    cameraCount: integer(row.cameraCount),
  }
}
