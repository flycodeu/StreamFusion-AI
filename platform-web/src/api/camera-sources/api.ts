import { request } from '../client'
import { id, page } from '../parse'
import { parseSource, parseSourceOptions } from './types'
import type { SourceUpdate } from './types'
export async function getSourceOptions() {
  return (
    await request({
      path: '/camera-sources/options',
      method: 'GET',
      successStatus: 200,
      decode: parseSourceOptions,
    })
  ).data
}
export async function getSources(query: {
  page: number
  size: number
  name?: string
  enabled?: boolean
  adapterType?: string
}) {
  return (
    await request({
      path: '/camera-sources',
      method: 'GET',
      params: query,
      successStatus: 200,
      decode: (v) => page(v, parseSource),
    })
  ).data
}
export async function getSource(sourceId: string) {
  return (
    await request({
      path: `/camera-sources/${id(sourceId)}`,
      method: 'GET',
      successStatus: 200,
      decode: parseSource,
    })
  ).data
}
export async function updateSource(sourceId: string, input: SourceUpdate) {
  return (
    await request({
      path: `/camera-sources/${id(sourceId)}`,
      method: 'PUT',
      body: input,
      successStatus: 200,
      decode: parseSource,
    })
  ).data
}
