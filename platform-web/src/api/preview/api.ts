import { request } from '../client'
import { empty } from '../parse'
import { parseAnswer, parseLease, parsePreviewCatalog, parseStats } from './types'
import type { PreviewUsage } from './types'

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
  cameraId: string
  streamProfileId?: string
  usage: PreviewUsage
  videoCodecs: string[]
}) {
  return (
    await request({
      path: '/preview/leases',
      method: 'POST',
      body: { clientRequestId: crypto.randomUUID(), ...input },
      successStatus: 201,
      timeoutMs: 20000,
      decode: parseLease,
    })
  ).data
}
export async function exchangeWebRtc(leaseId: string, sdp: string) {
  return (
    await request({
      path: `/preview/leases/${encodeURIComponent(leaseId)}/webrtc`,
      method: 'POST',
      body: { sdp },
      successStatus: 200,
      timeoutMs: 20000,
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
    decode: empty,
  })
}
