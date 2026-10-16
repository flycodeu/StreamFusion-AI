import { request } from '../client'
import { empty } from '../parse'
import { parseAnswer, parseLease, parsePreviewCatalog, parseStats } from './types'
import type { PreviewUsage } from './types'
import { ApiRequestError } from '../../lib/http/error'

export function isUnknownPreviewReceipt(cause: unknown): boolean {
  return (
    cause instanceof ApiRequestError &&
    (cause.status === undefined || (cause.status >= 200 && cause.status < 300)) &&
    (cause.code === 'NETWORK_ERROR' ||
      cause.code === 'REQUEST_TIMEOUT' ||
      (cause.code === 'INVALID_RESPONSE' && cause.status === 201))
  )
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
    return (await create()).data
  } catch (cause) {
    if (!isUnknownPreviewReceipt(cause)) throw cause
    return (await create()).data
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
  return (
    await request({
      path: `/preview/leases/${encodeURIComponent(leaseId)}/renew`,
      method: 'POST',
      successStatus: 200,
      timeoutMs: 10000,
      decode: parseLease,
    })
  ).data
}
export async function deletePreviewLease(leaseId: string) {
  await request({
    path: `/preview/leases/${encodeURIComponent(leaseId)}`,
    method: 'DELETE',
    successStatus: 200,
    timeoutMs: 15000,
    decode: empty,
  })
}
