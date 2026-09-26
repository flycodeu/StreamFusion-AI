import { ApiRequestError, invalidResponse } from './error'
import { isBusinessApiPath } from './path'
import { parseEnvelope, responseContext } from './response'
import { send } from './transport'
import type { ApiResult, CsrfToken, RequestOptions } from './types'

export interface ClientHooks {
  getCsrf: () => CsrfToken | null
  getIdentityEpoch: () => number
  onAuthFailure: (error: ApiRequestError, epoch: number) => void
  onForbidden: (error: ApiRequestError, epoch: number) => Promise<void>
}

/** Explicit assembly avoids importing session/router into the HTTP layer or silently disabling it. */
export function createApiClient(hooks: ClientHooks) {
  if (
    !hooks ||
    typeof hooks.getCsrf !== 'function' ||
    typeof hooks.getIdentityEpoch !== 'function' ||
    typeof hooks.onAuthFailure !== 'function' ||
    typeof hooks.onForbidden !== 'function'
  ) {
    throw new ApiRequestError('INVALID_REQUEST', '请求客户端未正确初始化')
  }
  let notifiedEpoch: number | undefined
  const notified = new Set<string>()
  let authorizationEpoch: number | undefined
  let authorizationRevision = 0
  let synchronizing: { epoch: number; promise: Promise<void> } | undefined

  return async function request<T>(options: RequestOptions<T>): Promise<ApiResult<T>> {
    if (
      !isBusinessApiPath(options.path) ||
      ![200, 201].includes(options.successStatus) ||
      typeof options.decode !== 'function'
    ) {
      throw new ApiRequestError('INVALID_REQUEST', '请求参数不合法')
    }
    const epoch = hooks.getIdentityEpoch()
    if (!Number.isSafeInteger(epoch) || epoch < 0)
      throw new ApiRequestError('INVALID_REQUEST', '身份状态未初始化')
    if (authorizationEpoch !== epoch) {
      authorizationEpoch = epoch
      authorizationRevision = 0
      synchronizing = undefined
    }
    const authorizationAtStart = authorizationRevision
    const current = () => hooks.getIdentityEpoch() === epoch && !options.signal?.aborted
    const cancelled = () => new ApiRequestError('REQUEST_CANCELLED', '请求已取消')
    try {
      if (!current()) throw cancelled()
      const csrf = options.method === 'GET' ? undefined : hooks.getCsrf()
      if (options.method !== 'GET' && !csrf)
        throw new ApiRequestError('CSRF_UNAVAILABLE', '请求凭证未准备好，请刷新后重试')
      const raw = await send({ ...options, csrf: csrf ?? undefined })
      if (!current()) throw cancelled()
      const result = parseEnvelope(raw)
      if (result.meta.status !== options.successStatus) throw invalidResponse(responseContext(raw))
      let data: T
      try {
        data = options.decode(result.data)
      } catch {
        throw invalidResponse(result.meta)
      }
      if (!current()) throw cancelled()
      return { data, meta: result.meta }
    } catch (error: unknown) {
      const transportCancelled =
        error instanceof ApiRequestError &&
        (error.code === 'REQUEST_TIMEOUT' || error.code === 'REQUEST_CANCELLED')
      if (hooks.getIdentityEpoch() !== epoch || (options.signal?.aborted && !transportCancelled))
        throw cancelled()
      if (
        error instanceof ApiRequestError &&
        error.status === 403 &&
        error.code === 'FORBIDDEN' &&
        options.path !== '/auth' &&
        !options.path.startsWith('/auth/')
      ) {
        // Share one refresh across both concurrent refusals and late responses from that batch.
        if (!synchronizing || synchronizing.epoch !== epoch) {
          if (authorizationAtStart === authorizationRevision) {
            authorizationRevision++
            const task = { epoch, promise: Promise.resolve() }
            synchronizing = task
            task.promise = Promise.resolve()
              .then(() => {
                if (hooks.getIdentityEpoch() === epoch) return hooks.onForbidden(error, epoch)
              })
              .catch(() => {
                // Authorization synchronization must not replace the original business error.
              })
              .finally(() => {
                if (synchronizing === task) {
                  authorizationRevision++
                  synchronizing = undefined
                }
              })
          }
        }
        if (synchronizing?.epoch === epoch) await synchronizing.promise
        if (!current()) throw cancelled()
      }
      if (
        error instanceof ApiRequestError &&
        (error.status === 401 ||
          error.code === 'IP_BLOCKED' ||
          error.code === 'PASSWORD_CHANGE_REQUIRED' ||
          error.code === 'CSRF_INVALID')
      ) {
        if (error.code !== 'LOGIN_FAILED') {
          if (notifiedEpoch !== epoch) {
            notifiedEpoch = epoch
            notified.clear()
          }
          const category = error.status === 401 ? 'UNAUTHORIZED' : error.code
          if (category === 'CSRF_INVALID' || !notified.has(category)) {
            if (category !== 'CSRF_INVALID') notified.add(category)
            try {
              hooks.onAuthFailure(error, epoch)
            } catch {
              // No exception values: callbacks can capture sensitive session state.
              console.error('Authentication failure callback failed')
            }
          }
        }
      }
      throw error
    }
  }
}
