import { invalidResponse } from '../../lib/http/error'
import { parseRawJson, responseContext } from '../../lib/http/response'
import { send } from '../../lib/http/transport'

export async function getHealth(signal?: AbortSignal): Promise<{ status: 'UP' }> {
  const raw = await send({ path: '/actuator/health', method: 'GET', signal })
  const value = parseRawJson(raw)
  if (
    typeof value !== 'object' ||
    value === null ||
    !('status' in value) ||
    value.status !== 'UP'
  ) {
    throw invalidResponse(responseContext(raw))
  }
  return { status: 'UP' }
}
