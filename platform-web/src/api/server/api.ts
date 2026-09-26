import { request } from '../client'
import { parseServerSnapshot } from './types'

export async function getServerStatus() {
  return (
    await request({
      path: '/server/status',
      method: 'GET',
      successStatus: 200,
      timeoutMs: 12000,
      decode: parseServerSnapshot,
    })
  ).data
}
