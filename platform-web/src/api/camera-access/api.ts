import { request } from '../client'
import { id, object, page, list, string, integer, optionalString, boolean } from '../parse'
import {
  parseAccessJob,
  parseAccessOptions,
  parseImportImpact,
  parseImportResult,
  parseBulkImpact,
  parseImportItem,
} from './types'
import type { AccessConnection, ImportInput, ScanInput, BulkImportInput } from './types'

export interface PlatformRegion {
  indexCode: string
  parentIndexCode: string | null
  name: string
}
export async function getIngressStatus() {
  return (
    await request({
      path: '/camera-access/ingress',
      method: 'GET',
      successStatus: 200,
      decode: (value) => {
        const row = object(value)
        return {
          enabled: boolean(row.enabled),
          gbEnabled: boolean(row.gbEnabled),
          mediaHost: string(row.mediaHost),
          rtspPort: integer(row.rtspPort),
          sipPort: integer(row.sipPort),
          registeredDevices: integer(row.registeredDevices),
        }
      },
    })
  ).data
}
export async function getPlatformRegions(input: {
  connection: AccessConnection
  parentCode?: string
  page: number
  size: number
}) {
  return (
    await request({
      path: '/camera-access/platform-regions',
      method: 'POST',
      body: input,
      successStatus: 200,
      decode: (value) => {
        const row = object(value)
        const result = {
          items: list(row.items, (value): PlatformRegion => {
            const item = object(value)
            return {
              indexCode: string(item.indexCode),
              parentIndexCode: optionalString(item.parentIndexCode),
              name: string(item.name),
            }
          }),
          total: integer(row.total),
          page: integer(row.page),
          size: integer(row.size),
        }
        if (
          result.total < 0 ||
          result.items.length > input.size ||
          result.page !== input.page ||
          result.size !== input.size
        )
          throw new Error('platform regions')
        return result
      },
    })
  ).data
}

export async function getImportJobs(query: { page: number; size: number }) {
  return (
    await request({
      path: '/camera-access/jobs',
      method: 'GET',
      params: { ...query, kind: 'BULK_IMPORT' },
      successStatus: 200,
      decode: (value) => page(value, parseAccessJob),
    })
  ).data
}
export async function previewBulkImport(jobId: string, input: BulkImportInput) {
  return (
    await request({
      path: `/camera-access/jobs/${id(jobId)}/bulk-import-preview`,
      method: 'POST',
      body: input,
      successStatus: 200,
      decode: parseBulkImpact,
    })
  ).data
}
export async function startBulkImport(
  jobId: string,
  input: BulkImportInput & { confirmation: string; clientRequestId: string },
) {
  return (
    await request({
      path: `/camera-access/jobs/${id(jobId)}/bulk-import`,
      method: 'POST',
      body: input,
      successStatus: 202,
      decode: parseAccessJob,
    })
  ).data
}
export async function getImportItems(
  jobId: string,
  query: { page: number; size: number; status?: 'FAILED' },
) {
  return (
    await request({
      path: `/camera-access/jobs/${id(jobId)}/import-items`,
      method: 'GET',
      params: query,
      successStatus: 200,
      decode: (value) => page(value, parseImportItem),
    })
  ).data
}

export async function createScanJob(input: ScanInput) {
  return (
    await request({
      path: '/camera-access/scan-jobs',
      method: 'POST',
      body: input,
      successStatus: 202,
      decode: (value) => ({ jobId: id(object(value).jobId) }),
    })
  ).data
}

export async function getAccessOptions() {
  return (
    await request({
      path: '/camera-access/options',
      method: 'GET',
      successStatus: 200,
      decode: parseAccessOptions,
    })
  ).data
}
export async function createAccessJob(input: {
  clientRequestId: string
  connection: AccessConnection
}) {
  return (
    await request({
      path: '/camera-access/jobs',
      method: 'POST',
      body: input,
      successStatus: 202,
      decode: (value) => ({ jobId: id(object(value).jobId) }),
    })
  ).data
}
export async function getAccessJob(jobId: string) {
  return (
    await request({
      path: `/camera-access/jobs/${id(jobId)}`,
      method: 'GET',
      successStatus: 200,
      decode: parseAccessJob,
    })
  ).data
}
export async function cancelAccessJob(jobId: string) {
  return (
    await request({
      path: `/camera-access/jobs/${id(jobId)}/cancel`,
      method: 'POST',
      successStatus: 200,
      decode: parseAccessJob,
    })
  ).data
}
export async function previewImport(jobId: string, input: ImportInput) {
  return (
    await request({
      path: `/camera-access/jobs/${id(jobId)}/import-preview`,
      method: 'POST',
      body: input,
      successStatus: 200,
      decode: parseImportImpact,
    })
  ).data
}
export async function importCandidates(
  jobId: string,
  input: ImportInput & { confirmation: string; clientRequestId: string },
) {
  return (
    await request({
      path: `/camera-access/jobs/${id(jobId)}/import`,
      method: 'POST',
      body: input,
      successStatus: 200,
      decode: parseImportResult,
    })
  ).data
}
