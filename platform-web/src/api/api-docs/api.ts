import { request } from '../client'
import { boolean, object } from '../parse'

export async function getApiDocsStatus(): Promise<{ enabled: boolean }> {
  return (
    await request({
      path: '/api-docs/status',
      method: 'GET',
      successStatus: 200,
      decode: (value) => ({ enabled: boolean(object(value).enabled) }),
    })
  ).data
}
