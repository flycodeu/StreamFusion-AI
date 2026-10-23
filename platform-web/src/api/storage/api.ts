import { request } from '../client'
import { object, string } from '../parse'
import {
  parseInstance,
  parseObject,
  parsePage,
  parseTask,
  type StorageOptions,
  type InstanceInput,
  type RuleInput,
  type ObjectPage,
  type CleanupPlan,
  type CleanupRule,
} from './types'

const raw = (value: unknown) => object(value)
const get = async <T>(
  path: string,
  decode: (value: unknown) => T,
  params?: Record<string, string | number | boolean | undefined>,
) => (await request({ path, method: 'GET', successStatus: 200, decode, params })).data
const write = async <T>(
  path: string,
  method: 'POST' | 'PUT' | 'DELETE',
  body: unknown,
  decode: (value: unknown) => T,
  successStatus: 200 | 201 | 202 = 200,
) => (await request({ path, method, body, successStatus, decode })).data
export type HistoryPolicyInput = {
  version: string
  inheritGlobal: boolean
  enabled: boolean
  retentionDays: number | null
}
export type HistoryPolicy = HistoryPolicyInput & {
  effective: { enabled: boolean; retentionDays: number | null; source: 'GLOBAL' | 'INSTANCE' }
}
const parseHistoryPolicy = (value: unknown): HistoryPolicy => {
  const row = object(value),
    applied = object(row.effective)
  const days = (value: unknown) => {
    if (value === null) return null
    if (typeof value !== 'number' || !Number.isInteger(value) || value < 1 || value > 3650)
      throw new Error('记录清理策略格式错误')
    return value
  }
  if (
    typeof row.inheritGlobal !== 'boolean' ||
    typeof row.enabled !== 'boolean' ||
    typeof applied.enabled !== 'boolean' ||
    !['GLOBAL', 'INSTANCE'].includes(String(applied.source))
  )
    throw new Error('记录清理策略格式错误')
  return {
    version: string(row.version),
    inheritGlobal: row.inheritGlobal,
    enabled: row.enabled,
    retentionDays: days(row.retentionDays),
    effective: {
      enabled: applied.enabled,
      retentionDays: days(applied.retentionDays),
      source: applied.source as 'GLOBAL' | 'INSTANCE',
    },
  }
}
const policyPath = (id?: string) =>
  id ? `/storage/instances/${id}/history-policy` : '/storage/history-policy'
export const getHistoryPolicy = (id?: string) => get(policyPath(id), parseHistoryPolicy)
export const saveHistoryPolicy = (input: HistoryPolicyInput, id?: string) =>
  write(policyPath(id), 'PUT', input, parseHistoryPolicy)
export const getOptions = () =>
  get('/storage/options', (value): StorageOptions => {
    const row = object(value)
    if (!Array.isArray(row.networkPolicies)) throw new Error('网络策略响应格式错误')
    return {
      networkPolicies: row.networkPolicies.map((value) => {
        const p = object(value)
        return { key: string(p.key), name: string(p.name) }
      }),
      credentialProtectionReady: row.credentialProtectionReady === true,
      deleteReady: row.deleteReady === true,
      deleteReason: string(row.deleteReason),
    }
  })
export const listInstances = (page: number, keyword: string, size = 20) =>
  get('/storage/instances/page', (value) => parsePage(value, parseInstance), {
    page,
    size,
    keyword,
  })
export const getInstance = (id: string) => get(`/storage/instances/${id}`, parseInstance)
export const getTask = (id: string) => get(`/storage/tasks/${id}`, parseTask)
export const saveInstance = (input: InstanceInput, id?: string) =>
  write(
    id ? `/storage/instances/${id}` : '/storage/instances',
    id ? 'PUT' : 'POST',
    input,
    parseInstance,
    id ? 200 : 201,
  )
export const draftCheck = async (input: InstanceInput, id?: string) =>
  (
    await request({
      path: '/storage/connection-tests',
      method: 'POST',
      body: input,
      params: { instanceId: id },
      successStatus: 200,
      decode: raw,
    })
  ).data
export const checkInstance = (id: string, version: string, scopeId?: string) =>
  write(
    `/storage/instances/${id}/checks`,
    'POST',
    { version, scopeId, mode: 'READ_ONLY', idempotencyKey: crypto.randomUUID() },
    parseTask,
    202,
  )
export const scanUsage = (id: string, version: string) =>
  write(
    `/storage/instances/${id}/usage-scans`,
    'POST',
    { version, idempotencyKey: crypto.randomUUID() },
    parseTask,
    202,
  )
export const listTasks = (id: string, page = 1, size = 20) =>
  get(`/storage/instances/${id}/tasks/page`, (value) => parsePage(value, parseTask), { page, size })
export const cancelTask = (id: string, version: string) =>
  write(
    `/storage/tasks/${id}/cancel`,
    'POST',
    { version, idempotencyKey: crypto.randomUUID() },
    parseTask,
    202,
  )
export const resumeTask = (id: string, version: string) =>
  write(
    `/storage/tasks/${id}/resume`,
    'POST',
    { version, idempotencyKey: crypto.randomUUID() },
    parseTask,
    202,
  )
export const removeInstance = (id: string, version: string) =>
  request({
    path: `/storage/instances/${id}`,
    method: 'DELETE',
    ifMatch: version,
    successStatus: 200,
    decode: () => null,
  })
export const removeScope = (id: string, version: string) =>
  request({
    path: `/storage/scopes/${id}`,
    method: 'DELETE',
    ifMatch: version,
    successStatus: 200,
    decode: () => null,
  })
export const addScope = (id: string, body: unknown) =>
  write(`/storage/instances/${id}/scopes`, 'POST', body, raw, 201)
export const listObjects = (id: string, prefix: string, cursor?: string, limit = 50) =>
  get(
    `/storage/scopes/${id}/objects`,
    (value): ObjectPage => {
      const row = object(value)
      if (!Array.isArray(row.items) || !Array.isArray(row.commonPrefixes))
        throw new Error('文件列表响应格式错误')
      return {
        items: row.items.map(parseObject),
        commonPrefixes: row.commonPrefixes.map(string),
        nextCursor: row.nextCursor == null ? null : string(row.nextCursor),
        hasMore: row.hasMore === true,
      }
    },
    { prefix, cursor, limit },
  )
export const getObject = async (id: string, key: string, signal?: AbortSignal) =>
  (
    await request({
      path: `/storage/scopes/${id}/object`,
      method: 'GET',
      params: { key },
      signal,
      successStatus: 200,
      decode: parseObject,
    })
  ).data
export const uploadFile = (
  scopeId: string,
  file: File,
  relativePrefix: string,
  instanceVersion: string,
  scopeVersion: string,
  idempotencyKey: string,
) => {
  const body = new FormData()
  body.append('file', file)
  body.append('relativePrefix', relativePrefix)
  body.append('instanceVersion', instanceVersion)
  body.append('scopeVersion', scopeVersion)
  body.append('idempotencyKey', idempotencyKey)
  return request({
    path: `/storage/scopes/${scopeId}/objects/upload`,
    method: 'POST',
    body,
    timeoutMs: 120000,
    successStatus: 201,
    decode: parseTask,
  })
}
export const createFolder = (scopeId: string, body: unknown) =>
  write(`/storage/scopes/${scopeId}/objects/folder`, 'POST', body, parseTask, 201)
export const deleteFile = (scopeId: string, body: unknown) =>
  write(`/storage/scopes/${scopeId}/object`, 'DELETE', body, parseTask)
export const objectContentUrl = (
  scopeId: string,
  key: string,
  objectToken: string,
  disposition = 'inline',
) => {
  const params = new URLSearchParams({ key, objectToken, disposition })
  return `${import.meta.env.DEV ? '/api' : ''}/storage/scopes/${scopeId}/object-content?${params}`
}
export const previewCleanup = (scopeId: string, body: unknown) =>
  write(`/storage/scopes/${scopeId}/cleanup-plans`, 'POST', body, raw, 202)
export const getCleanupPlan = (id: string) =>
  get(`/storage/cleanup-plans/${id}`, (value): CleanupPlan => {
    const row = object(value)
    return {
      planId: string(row.planId),
      version: string(row.version),
      scopeId: string(row.scopeId),
      status: string(row.status),
      candidateCount: string(row.candidateCount),
      candidateBytes: string(row.candidateBytes),
      limitReached: row.limitReached === true,
      scanComplete: row.scanComplete === true,
      deleteReady: row.deleteReady === true,
      reasonCode: string(row.reasonCode),
      suggestion: string(row.suggestion),
      expiresAt: string(row.expiresAt),
    }
  })
export const listCleanupItems = (id: string, page = 1) =>
  get(`/storage/cleanup-plans/${id}/items`, (value) => parsePage(value, raw), { page, size: 20 })
const parseRule = (value: unknown): CleanupRule => {
  const row = object(value)
  return {
    id: string(row.id),
    scopeId: string(row.scopeId),
    name: string(row.name),
    relativePrefix: string(row.relativePrefix),
    retentionDays: Number(row.retentionDays),
    scheduleTime: string(row.scheduleTime),
    maxDeleteObjects: Number(row.maxDeleteObjects),
    maxDeleteBytes: string(row.maxDeleteBytes),
    enabled: row.enabled === true,
    version: string(row.version),
  }
}
export const listRules = (scopeId: string) =>
  get(`/storage/scopes/${scopeId}/cleanup-rules`, (value) => {
    if (!Array.isArray(value)) throw new Error('清理规则响应格式错误')
    return value.map(parseRule)
  })
export const saveRule = (scopeId: string, body: RuleInput, id?: string) =>
  write(
    id
      ? `/storage/scopes/${scopeId}/cleanup-rules/${id}`
      : `/storage/scopes/${scopeId}/cleanup-rules`,
    id ? 'PUT' : 'POST',
    body,
    parseRule,
    id ? 200 : 201,
  )
export const removeRule = (id: string, version: string) =>
  request({
    path: `/storage/cleanup-rules/${id}`,
    method: 'DELETE',
    ifMatch: version,
    successStatus: 200,
    decode: () => null,
  })

export const previewFolderDeletion = (id: string, body: unknown) =>
  write(`/storage/scopes/${id}/folder-deletions`, 'POST', body, raw, 202)
export const getFolderDeletion = (id: string) => get(`/storage/folder-deletions/${id}`, raw)
export const executeFolderDeletion = (id: string, body: unknown) =>
  write(`/storage/folder-deletions/${id}/execute`, 'POST', body, parseTask, 202)
