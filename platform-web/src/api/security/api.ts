import { request } from '../client'
import { id, page } from '../parse'
import type { Page } from '../parse'
import { parseIpBlock } from './types'
import type { IpBlock, IpBlockStatus } from './types'

export interface IpBlockQuery {
  page: number
  size: number
  sourceIp?: string
  status?: IpBlockStatus
}

export async function getIpBlocks(
  query: IpBlockQuery,
  signal?: AbortSignal,
): Promise<Page<IpBlock>> {
  return (
    await request({
      path: '/audit/ip-blocks/page',
      method: 'GET',
      params: { ...query },
      signal,
      successStatus: 200,
      decode: (value) => page(value, parseIpBlock),
    })
  ).data
}

export async function unblockIp(input: Pick<IpBlock, 'id' | 'version'>): Promise<IpBlock> {
  return (
    await request({
      path: `/audit/ip-blocks/${id(input.id)}/unblock`,
      method: 'PUT',
      ifMatch: `"${id(input.version)}"`,
      successStatus: 200,
      decode: parseIpBlock,
    })
  ).data
}
