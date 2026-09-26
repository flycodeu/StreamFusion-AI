import { request } from '../client'
import { boolean, object } from '../parse'

export function apiDocsFrameUrl(): string {
  const prefix = import.meta.env.DEV ? '/api' : ''
  return `${import.meta.env.BASE_URL}api-docs.html?apiPrefix=${encodeURIComponent(prefix)}`
}

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
