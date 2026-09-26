import { request } from '../client'
import { boolean, object } from '../parse'
import { ApiRequestError, invalidResponse } from '../../lib/http/error'
import { parseRawJson, responseContext } from '../../lib/http/response'
import { send } from '../../lib/http/transport'

export function apiDocsFrameUrl(): string {
  const prefix = import.meta.env.DEV ? '/api' : ''
  return `/api-docs.html?apiPrefix=${encodeURIComponent(prefix)}`
}

export async function getApiDocsStatus(signal?: AbortSignal): Promise<{ enabled: boolean }> {
  return (
    await request({
      path: '/api-docs/status',
      method: 'GET',
      successStatus: 200,
      signal,
      decode: (value) => ({ enabled: boolean(object(value).enabled) }),
    })
  ).data
}

export async function getOpenApiDocument(signal?: AbortSignal): Promise<string> {
  // Recheck the PAGE grant at export time; the raw Springdoc resource keeps its existing policy.
  if (!(await getApiDocsStatus(signal)).enabled) {
    throw new ApiRequestError('API_DOCS_DISABLED', '当前环境未启用接口文档')
  }
  const raw = await send({ path: '/v3/api-docs', method: 'GET', signal, timeoutMs: 15000 })
  const value = parseRawJson(raw)
  if (
    raw.headers.contentType?.split(';')[0]?.trim().toLowerCase() !== 'application/json' ||
    typeof value !== 'object' ||
    value === null ||
    !('openapi' in value) ||
    typeof value.openapi !== 'string' ||
    !/^3\.\d+\.\d+(?:[-+].*)?$/.test(value.openapi) ||
    !('paths' in value) ||
    typeof value.paths !== 'object' ||
    value.paths === null ||
    Array.isArray(value.paths)
  ) {
    throw invalidResponse(responseContext(raw))
  }
  return JSON.stringify(value, null, 2)
}
