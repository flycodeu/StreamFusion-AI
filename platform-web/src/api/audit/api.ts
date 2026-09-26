import { request } from '../client'
import { page } from '../parse'
import { parseAuditDetail, parseAuditEntry } from './types'

export interface AuditQuery {
  page: number
  size: number
  user?: string
  action?: string
  module?: string
  result?: string
  traceId?: string
  startTime?: string
  endTime?: string
}

export async function getAuditPage(query: AuditQuery) {
  return (
    await request({
      path: '/audit/page',
      method: 'GET',
      params: { ...query },
      successStatus: 200,
      decode: (value) => page(value, parseAuditEntry),
    })
  ).data
}

export async function getAuditDetail(id: string) {
  return (
    await request({
      path: `/audit/${id}`,
      method: 'GET',
      successStatus: 200,
      decode: parseAuditDetail,
    })
  ).data
}
