import { request } from '../client'
import { id } from '../parse'
import { parseCameraStatus, parseStatusCheck, parseStatusPage } from './types'
import type { StatusCheckInput, StatusSearch } from './types'

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
      body: { streamProfileId: id(input.streamProfileId), clientRequestId: input.clientRequestId },
      signal,
      timeoutMs: 10000,
      decode: (value) => {
        const result = parseStatusCheck(value)
        if (result.cameraId !== cameraId || result.streamProfileId !== input.streamProfileId)
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
