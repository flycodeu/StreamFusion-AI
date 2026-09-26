import { request } from '../client'
import { id, page } from '../parse'
import { parseLoginRecord } from './types'

export async function getLoginRecords(
  userId: string | undefined,
  query: { page: number; size: number },
  signal?: AbortSignal,
) {
  return (
    await request({
      path: userId ? `/user/${id(userId)}/login-records/page` : '/auth/login-records/page',
      method: 'GET',
      params: { ...query },
      signal,
      successStatus: 200,
      decode: (value) => page(value, parseLoginRecord),
    })
  ).data
}
