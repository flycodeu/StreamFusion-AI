import { shallowRef } from 'vue'
import { ApiRequestError } from '../../lib/http/error'
import type { SecretAction, SecretWrite } from '../../api/camera/types'
export type { LocatorInput, SecretAction, SecretWrite, UsageHint } from '../../api/camera/types'

export const formError = (message: string) => new ApiRequestError('VALIDATION_ERROR', message)
export const isRequestRejected = (cause: unknown) =>
  cause instanceof ApiRequestError &&
  cause.status != null &&
  cause.status >= 400 &&
  cause.status < 500

export const usageOptions = [
  { value: 'MAIN', label: '主码流' },
  { value: 'SUB', label: '辅码流' },
  { value: 'THIRD', label: '第三码流' },
  { value: 'CUSTOM', label: '自定义' },
  { value: 'UNKNOWN', label: '未指定' },
] as const
export function usageLabel(value: string): string {
  return usageOptions.find((item) => item.value === value)?.label ?? value
}
export const lifecycleLabels = {
  PENDING_ASSIGNMENT: '待归档',
  ENABLED: '已启用',
  DISABLED: '已停用',
} as const
export function connectionLabel(type: string | null | undefined, category?: string | null): string {
  const labels: Record<string, string> = {
    ONVIF: 'ONVIF 设备',
    HIKVISION: '海康设备',
    DAHUA: '大华设备',
    HIK_PLATFORM: '海康 ISC 平台',
    RTSP: 'RTSP 地址',
  }
  return type
    ? (labels[type] ?? type)
    : category === 'PLATFORM'
      ? '平台连接'
      : '设备连接（未指定方式）'
}
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
