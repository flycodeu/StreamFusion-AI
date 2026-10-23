import { object, string, id } from '../parse'

export interface StorageScope {
  scopeId: string
  instanceId: string
  bucket: string
  prefix: string
  displayName: string | null
  probePrefix: string
  managementMode: string
  version: string
  observation: Record<string, unknown>
}
export interface StorageInstance {
  instanceId: string
  name: string
  endpoint: string
  region: string
  networkPolicyKey: string
  metricsEndpoint: string | null
  managementUrl: string | null
  remark: string | null
  writeEnabled: boolean
  credentialConfigured: boolean
  version: string
  freshness: string
  accessStatus: string
  serviceState: string
  checkedAt: string | null
  observation: Record<string, unknown>
  usage: Record<string, unknown>
  metrics: Record<string, unknown>
  scopes: StorageScope[]
}
export interface Page<T> {
  items: T[]
  page: number
  size: number
  total: number
}
export interface StorageOptions {
  networkPolicies: { key: string; name: string }[]
  credentialProtectionReady: boolean
  deleteReady: boolean
  deleteReason: string
}
export interface SecretWrite {
  action: 'KEEP' | 'REPLACE'
  value?: string
}
export interface ScopeInput {
  bucket: string
  prefix: string
  displayName: string
  probePrefix: string
}
export interface InstanceInput {
  name: string
  endpoint: string
  region: string
  networkPolicyKey?: string
  metricsEndpoint: string
  managementUrl: string
  remark: string
  accessKey: SecretWrite
  secretKey: SecretWrite
  scopes?: ScopeInput[]
  version?: string
  idempotencyKey: string
}
export interface StorageObject {
  objectKey: string
  name: string
  sizeBytes: string
  etag: string
  lastModified: string
  contentType: string | null
  objectVersionId: string | null
  objectToken?: string
}
export interface ObjectPage {
  items: StorageObject[]
  commonPrefixes: string[]
  nextCursor: string | null
  hasMore: boolean
}
export interface StorageTask {
  taskId: string
  runId: string
  instanceId: string
  type: string
  status: string
  phase: string
  version: string
  cancelRequested: boolean
  traceId: string | null
  acceptedAt: string
  startedAt: string | null
  finishedAt: string | null
  result: Record<string, unknown>
}
export interface CleanupPlan {
  planId: string
  version: string
  scopeId: string
  status: string
  candidateCount: string
  candidateBytes: string
  limitReached: boolean
  scanComplete: boolean
  deleteReady: boolean
  reasonCode: string
  suggestion: string
  expiresAt: string
}
export interface CleanupRule {
  id: string
  scopeId: string
  name: string
  relativePrefix: string
  retentionDays: number
  scheduleTime: string
  maxDeleteObjects: number
  maxDeleteBytes: string
  enabled: boolean
  version: string
}
export interface RuleInput {
  name: string
  relativePrefix: string
  retentionDays: number
  scheduleTime: string
  maxDeleteObjects: number
  maxDeleteBytes: string
  version?: string
  idempotencyKey: string
}
export function parseScope(value: unknown): StorageScope {
  const row = object(value)
  return {
    scopeId: id(row.scopeId),
    instanceId: id(row.instanceId),
    bucket: string(row.bucket),
    prefix: string(row.prefix),
    displayName: nullableString(row.displayName),
    probePrefix: string(row.probePrefix),
    managementMode: string(row.managementMode),
    version: id(row.version),
    observation: object(row.observation),
  }
}
function nullableString(value: unknown) {
  return value == null ? null : string(value)
}
export function parseInstance(value: unknown): StorageInstance {
  const row = object(value)
  if (!Array.isArray(row.scopes) || typeof row.writeEnabled !== 'boolean')
    throw new Error('存储实例响应格式错误')
  return {
    instanceId: id(row.instanceId),
    name: string(row.name),
    endpoint: string(row.endpoint),
    region: string(row.region),
    networkPolicyKey: string(row.networkPolicyKey),
    metricsEndpoint: nullableString(row.metricsEndpoint),
    managementUrl: nullableString(row.managementUrl),
    remark: nullableString(row.remark),
    writeEnabled: row.writeEnabled,
    credentialConfigured: row.credentialConfigured === true,
    version: id(row.version),
    freshness: string(row.freshness),
    accessStatus: string(row.accessStatus),
    serviceState: string(row.serviceState),
    checkedAt: nullableString(row.checkedAt),
    observation: object(row.observation),
    usage: object(row.usage),
    metrics: object(row.metrics),
    scopes: row.scopes.map(parseScope),
  }
}
export function parseTask(value: unknown): StorageTask {
  const row = object(value)
  return {
    taskId: id(row.taskId),
    runId: id(row.runId),
    instanceId: id(row.instanceId),
    type: string(row.type),
    status: string(row.status),
    phase: string(row.phase),
    version: id(row.version),
    cancelRequested: row.cancelRequested === true,
    traceId: nullableString(row.traceId),
    acceptedAt: string(row.acceptedAt),
    startedAt: nullableString(row.startedAt),
    finishedAt: nullableString(row.finishedAt),
    result: object(row.result),
  }
}
export function parsePage<T>(value: unknown, parse: (value: unknown) => T): Page<T> {
  const row = object(value)
  if (!Array.isArray(row.items) || !Number.isSafeInteger(Number(row.total)))
    throw new Error('分页响应格式错误')
  return {
    items: row.items.map(parse),
    page: Number(row.page),
    size: Number(row.size),
    total: Number(row.total),
  }
}
export function parseObject(value: unknown): StorageObject {
  const row = object(value)
  return {
    objectKey: string(row.objectKey),
    name: string(row.name),
    sizeBytes: id(row.sizeBytes),
    etag: string(row.etag),
    lastModified: string(row.lastModified),
    contentType: nullableString(row.contentType),
    objectVersionId: nullableString(row.objectVersionId),
    ...(typeof row.objectToken === 'string' ? { objectToken: row.objectToken } : {}),
  }
}
