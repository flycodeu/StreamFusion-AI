import { request } from '../client'
import { parseRuntimeSnapshot } from './types'
import type { RuntimeSelection } from './types'

export async function getMediaRuntime(signal?: AbortSignal) {
  return (
    await request({
      path: '/media-runtime',
      method: 'GET',
      successStatus: 200,
      timeoutMs: 20000,
      signal,
      decode: parseRuntimeSnapshot,
    })
  ).data
}
export async function detectMediaRuntime(signal?: AbortSignal) {
  return (
    await request({
      path: '/media-runtime/detect',
      method: 'POST',
      successStatus: 200,
      timeoutMs: 30000,
      signal,
      decode: parseRuntimeSnapshot,
    })
  ).data
}
export async function selectMediaRuntime(version: string, selection: RuntimeSelection) {
  return (
    await request({
      path: '/media-runtime/selection',
      method: 'PUT',
      body: selection,
      ifMatch: `"${version}"`,
      successStatus: 200,
      timeoutMs: 20000,
      decode: parseRuntimeSnapshot,
    })
  ).data
}
