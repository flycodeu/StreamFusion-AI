import { request } from '../client'
import { boolean, integer, list, object, optionalString, string } from '../parse'
import type { AuditQuery } from './api'

export interface RetentionPolicy {
  enabled: boolean
  mode: string
  keepMonths: number
  maxRecords: number
  version: string
  nextAt: string | null
  lastStartedAt: string | null
  lastFinishedAt: string | null
  lastDeleted: number
  lastResult: string | null
}
export interface RetentionCommand {
  enabled: boolean
  mode: string
  keepMonths: number
  maxRecords: number
  version: string
}
export interface RetentionPreview {
  total: number
  protectedCount: number
  candidates: number
  limited: boolean
  cutoff: string | null
  exportInProgress: boolean
}
export interface ExportJob {
  id: string
  actorId: string
  state: string
  format: string
  rowCount: number
  fileBytes: number
  reasonCode: string | null
  createdAt: string
  finishedAt: string | null
  expiresAt: string
  downloadable: boolean
  filter: Record<string, unknown>
}
function parsePolicy(value: unknown): RetentionPolicy {
  const r = object(value)
  return {
    enabled: boolean(r.enabled),
    mode: string(r.mode),
    keepMonths: integer(r.keepMonths),
    maxRecords: integer(r.maxRecords),
    version: string(r.version),
    nextAt: optionalString(r.nextAt),
    lastStartedAt: optionalString(r.lastStartedAt),
    lastFinishedAt: optionalString(r.lastFinishedAt),
    lastDeleted: integer(r.lastDeleted),
    lastResult: optionalString(r.lastResult),
  }
}
function parsePreview(value: unknown): RetentionPreview {
  const r = object(value)
  return {
    total: integer(r.total),
    protectedCount: integer(r.protectedCount),
    candidates: integer(r.candidates),
    limited: boolean(r.limited),
    cutoff: optionalString(r.cutoff),
    exportInProgress: boolean(r.exportInProgress),
  }
}
export function parseExport(value: unknown): ExportJob {
  const r = object(value)
  return {
    id: string(r.id),
    actorId: string(r.actorId),
    state: string(r.state),
    format: string(r.format),
    rowCount: integer(r.rowCount),
    fileBytes: integer(r.fileBytes),
    reasonCode: optionalString(r.reasonCode),
    createdAt: string(r.createdAt),
    finishedAt: optionalString(r.finishedAt),
    expiresAt: string(r.expiresAt),
    downloadable: boolean(r.downloadable),
    filter: object(r.filter),
  }
}
export const getRetention = async () =>
  (
    await request({
      path: '/audit/retention',
      method: 'GET',
      successStatus: 200,
      decode: parsePolicy,
    })
  ).data
export const saveRetention = async (body: RetentionCommand) =>
  (
    await request({
      path: '/audit/retention',
      method: 'PUT',
      body,
      successStatus: 200,
      decode: parsePolicy,
    })
  ).data
export const previewRetention = async (body: RetentionCommand) =>
  (
    await request({
      path: '/audit/retention/preview',
      method: 'POST',
      body,
      successStatus: 200,
      decode: parsePreview,
    })
  ).data
export const runRetention = async (version: string) =>
  (
    await request({
      path: '/audit/retention/run',
      method: 'POST',
      body: { version },
      successStatus: 200,
      decode: parsePolicy,
    })
  ).data
export const getExports = async () =>
  (
    await request({
      path: '/audit/exports',
      method: 'GET',
      successStatus: 200,
      decode: (v) => list(v, parseExport),
    })
  ).data
export const createExport = async (format: string, filter: AuditQuery, clientRequestId: string) =>
  (
    await request({
      path: '/audit/exports',
      method: 'POST',
      body: { format, filter, clientRequestId },
      successStatus: 201,
      decode: parseExport,
    })
  ).data
export const cancelExport = async (id: string) =>
  (
    await request({
      path: `/audit/exports/${id}/cancel`,
      method: 'POST',
      successStatus: 200,
      decode: parseExport,
    })
  ).data
export const removeExport = async (id: string) =>
  (
    await request({
      path: `/audit/exports/${id}/file`,
      method: 'DELETE',
      successStatus: 200,
      decode: parseExport,
    })
  ).data
export async function downloadExport(id: string) {
  const result = await request({
    path: `/audit/exports/${id}/download`,
    method: 'GET',
    responseType: 'file',
    timeoutMs: 120000,
    successStatus: 200,
    decode: (value) => {
      const r = object(value)
      if (!(r.blob instanceof Blob)) throw new Error('file')
      return { blob: r.blob, name: string(r.name) }
    },
  })
  const url = URL.createObjectURL(result.data.blob)
  const anchor = document.createElement('a')
  anchor.href = url
  anchor.download = result.data.name
  anchor.click()
  globalThis.setTimeout(() => URL.revokeObjectURL(url), 10000)
}
