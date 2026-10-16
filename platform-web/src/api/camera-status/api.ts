import { request } from '../client'
import { id, object, list, integer, boolean } from '../parse'
import { parseCameraStatus, parseStatusCheck, parseStatusPage } from './types'
import type { StatusCheckInput, StatusSearch, StatusLayer } from './types'

export async function getStatusHistory(
  cameraId: string,
  query: {
    streamProfileId: string
    layer: StatusLayer
    days: number
    page: number
    size: number
  },
  signal?: AbortSignal,
) {
  return (
    await request({
      path: `/cameras/${id(cameraId)}/status-checks`,
      method: 'GET',
      params: query,
      signal,
      successStatus: 200,
      decode: (value) => {
        const row = object(value)
        const result = {
          items: list(row.items, parseStatusCheck),
          total: integer(row.total),
          page: integer(row.page),
          size: integer(row.size),
          accessible: integer(row.accessible),
          failed: integer(row.failed),
          unknown: integer(row.unknown),
          truncated: boolean(row.truncated),
        }
        if (
          result.total < 0 ||
          result.total > 1000 ||
          result.items.length > query.size ||
          result.page !== query.page ||
          result.size !== query.size ||
          result.accessible < 0 ||
          result.failed < 0 ||
          result.unknown < 0 ||
          result.accessible + result.failed + result.unknown !== result.total ||
          result.items.some(
            (item) =>
              item.cameraId !== cameraId ||
              item.streamProfileId !== query.streamProfileId ||
              item.layer !== query.layer,
          )
        )
          throw new Error('status history bounds')
        return result
      },
    })
  ).data
}

export async function getStatusPage(query: StatusSearch, signal?: AbortSignal) {
  return (
    await request({
      path: '/camera-status',
      method: 'GET',
      params: { ...query },
      successStatus: 200,
      signal,
      timeoutMs: 10000,
      decode: parseStatusPage,
    })
  ).data
}

export async function getCameraStatus(cameraId: string, signal?: AbortSignal) {
  return (
    await request({
      path: `/cameras/${id(cameraId)}/status`,
      method: 'GET',
      successStatus: 200,
      signal,
      timeoutMs: 10000,
      decode: (value) => {
        const result = parseCameraStatus(value)
        if (result.cameraId !== cameraId) throw new Error('camera identity')
        return result
      },
    })
  ).data
}
export async function createStatusCheck(
  cameraId: string,
  input: StatusCheckInput,
  signal?: AbortSignal,
) {
  return (
    await request({
      path: `/cameras/${id(cameraId)}/status-checks`,
      method: 'POST',
      successStatus: 202,
      body: {
        layer: input.layer,
        streamProfileId: id(input.streamProfileId),
        clientRequestId: input.clientRequestId,
      },
      signal,
      timeoutMs: 10000,
      decode: (value) => {
        const result = parseStatusCheck(value)
        if (
          result.cameraId !== cameraId ||
          result.streamProfileId !== input.streamProfileId ||
          result.layer !== input.layer
        )
          throw new Error('check target identity')
        return result
      },
    })
  ).data
}
export async function getStatusCheck(cameraId: string, checkId: string, signal?: AbortSignal) {
  return (
    await request({
      path: `/cameras/${id(cameraId)}/status-checks/${id(checkId)}`,
      method: 'GET',
      successStatus: 200,
      signal,
      timeoutMs: 10000,
      decode: (value) => {
        const result = parseStatusCheck(value)
        if (result.cameraId !== cameraId || result.checkId !== checkId)
          throw new Error('check identity')
        return result
      },
    })
  ).data
}
