import { request } from '../client'
import { empty } from '../parse'
import {
  parseAnswer,
  parseLease,
  parsePreviewCatalog,
  parseStats,
  parsePreviewCapacity,
} from './types'
import type { PreviewUsage } from './types'
import type { PreviewLease } from './types'
import type { ApiResult } from '../../lib/http/types'
import { ApiRequestError } from '../../lib/http/error'

export async function getPreviewCapacity(signal?: AbortSignal) {
  return (
    await request({
      path: '/preview/capacity',
      method: 'GET',
      successStatus: 200,
      timeoutMs: 10000,
      signal,
      decode: parsePreviewCapacity,
    })
  ).data
}

export function isUnknownPreviewReceipt(cause: unknown): boolean {
  return (
    cause instanceof ApiRequestError &&
    (cause.status === undefined || (cause.status >= 200 && cause.status < 300)) &&
    (cause.code === 'NETWORK_ERROR' ||
      cause.code === 'REQUEST_TIMEOUT' ||
      (cause.code === 'INVALID_RESPONSE' && cause.status === 201))
  )
}

function withLeaseReceipt(result: ApiResult<PreviewLease>): PreviewLease {
  const { status, traceId, timestamp } = result.meta
  return { ...result.data, receipt: { status, traceId, timestamp } }
}

export async function getPreviewCatalog(query: {
  page: number
  size: number
  name?: string
  device?: string
}) {
  return (
    await request({
      path: '/preview/catalog',
      method: 'GET',
      params: query,
      successStatus: 200,
      timeoutMs: 15000,
      decode: parsePreviewCatalog,
    })
  ).data
}
export async function createPreviewLease(input: {
  clientRequestId: string
  cameraId: string
  streamProfileId?: string
  usage: PreviewUsage
  videoCodecs: string[]
}) {
  const body = { ...input, videoCodecs: [...input.videoCodecs] }
  const create = () =>
    request({
      path: '/preview/leases',
      method: 'POST',
      body,
      successStatus: 201,
      timeoutMs: 25000,
      decode: parseLease,
    })
  try {
    return withLeaseReceipt(await create())
  } catch (cause) {
    if (!isUnknownPreviewReceipt(cause)) throw cause
    return withLeaseReceipt(await create())
  }
}
export async function exchangeWebRtc(leaseId: string, sdp: string) {
  return (
    await request({
      path: `/preview/leases/${encodeURIComponent(leaseId)}/webrtc`,
      method: 'POST',
      body: { sdp },
      successStatus: 200,
      timeoutMs: 15000,
      decode: parseAnswer,
    })
  ).data
}
export async function getPreviewStats(leaseId: string) {
  return (
    await request({
      path: `/preview/leases/${encodeURIComponent(leaseId)}/stats`,
      method: 'GET',
      successStatus: 200,
      timeoutMs: 10000,
      decode: parseStats,
    })
  ).data
}
export async function renewPreviewLease(leaseId: string) {
  return withLeaseReceipt(
    await request({
      path: `/preview/leases/${encodeURIComponent(leaseId)}/renew`,
      method: 'POST',
      successStatus: 200,
      timeoutMs: 10000,
      decode: parseLease,
    }),
  )
}
export async function deletePreviewLease(leaseId: string) {
  // The server treats DELETE by lease ID as idempotent. A lost receipt or a
  // still-running cleanup must be confirmed before this slot can be reused.
  const retryDelays = [500, 1500, 9000]
  for (let attempt = 0; ; attempt++) {
    try {
      await request({
        path: `/preview/leases/${encodeURIComponent(leaseId)}`,
        method: 'DELETE',
        successStatus: 200,
        timeoutMs: 15000,
        decode: empty,
      })
      return
    } catch (cause) {
      if (
        attempt >= retryDelays.length ||
        !(cause instanceof ApiRequestError) ||
        !(
          cause.code === 'NETWORK_ERROR' ||
          cause.code === 'REQUEST_TIMEOUT' ||
          (cause.code === 'INVALID_RESPONSE' && cause.status === 200) ||
          cause.status === 429 ||
          (cause.status !== undefined && cause.status >= 500)
        )
      )
        throw cause
      await new Promise((resolve) => globalThis.setTimeout(resolve, retryDelays[attempt]))
    }
  }
}
