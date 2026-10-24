import { shallowRef } from 'vue'
import { ApiRequestError } from '../../lib/http/error'
import type { SecretAction, SecretWrite } from '../../api/camera/types'
export type { LocatorInput, SecretAction, SecretWrite, UsageHint } from '../../api/camera/types'
export { effectiveUsage, streamLabel, usageLabel, usageOptions } from './streamLabels'

export const formError = (message: string) => new ApiRequestError('VALIDATION_ERROR', message)
export const isRequestRejected = (cause: unknown) =>
  cause instanceof ApiRequestError &&
  cause.status != null &&
  cause.status >= 400 &&
  cause.status < 500

export { lifecycleLabels, lifecycleLabel, brandOptions, sourceBrandLabel } from './presentation'
export { connectionLabel } from '../camera-access/presentation'
export function secret(action: SecretAction, value: string): SecretWrite {
  return action === 'REPLACE' ? { action, value } : { action }
}
/** An uncertain response must retry the same command and key, never silently create a new asset. */
export function useCreateRequest<T extends object>() {
  const pending = shallowRef<(T & { clientRequestId: string }) | null>(null)
  return {
    pending,
    capture(input: T) {
      if (!pending.value)
        pending.value = {
          ...structuredClone(input),
          clientRequestId: `${Date.now()}-${crypto.randomUUID()}`,
        }
      return pending.value
    },
    reset() {
      pending.value = null
    },
  }
}
