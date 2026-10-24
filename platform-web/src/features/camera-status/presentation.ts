import type {
  ExecutionState,
  ObservationFilter,
  ObservationValue,
  StatusLayer,
  StatusSummary,
} from '../../api/camera-status/types'
import type { PlanProgress, StatusPlan } from '../../api/camera-status/plans'
import { labelOf } from '../../utils/display'

export type ConnectionState = Exclude<keyof StatusSummary, 'total'>
export const executionLabels = {
  QUEUED: '等待执行',
  RUNNING: '正在检查',
  COMPLETED: '检查结束',
  SKIPPED: '未执行',
  CANCELLED: '已取消',
  INTERRUPTED: '执行中断',
} as const satisfies Record<ExecutionState, string>
export const summaryLabels = {
  accessible: '检查通过',
  failed: '检查异常',
  checking: '检测中',
  stale: '配置已变更',
  unknown: '尚未检查',
  disabled: '未启用',
} as const satisfies Record<ConnectionState, string>
export const connectionLabels = {
  ...summaryLabels,
  accessible: '在线',
  failed: '连接异常',
  unknown: '未确认',
}
export const mediaLabels = { ...summaryLabels, accessible: '采样收帧正常', failed: '采样收帧异常' }
export const upstreamLabels = {
  ...summaryLabels,
  accessible: '平台在线',
  failed: '平台离线',
  checking: '查询中',
  unknown: '未获取',
}
export const observationLabels = {
  NEVER: '尚未检查',
  ACCESSIBLE: '最近检查通过',
  FAILED: '最近检查异常',
  UNSUPPORTED: '检测待适配',
} as const satisfies Record<ObservationFilter, string>
export const factLabels = {
  CONTROL: { ACCESSIBLE: '连接检查通过', FAILED: '连接检查异常', UNSUPPORTED: '检测待适配' },
  MEDIA: { ACCESSIBLE: '采样收帧正常', FAILED: '采样收帧异常', UNSUPPORTED: '检测待适配' },
  UPSTREAM: { ACCESSIBLE: '平台在线', FAILED: '平台离线', UNSUPPORTED: '检测待适配' },
} as const satisfies Record<StatusLayer, Record<ObservationValue, string>>
export const planStateLabels = {
  ACTIVE: '运行中',
  STOPPED: '已停止',
  EXPIRED: '已到期',
} as const satisfies Record<StatusPlan['state'], string>
export const planStateLabel = (value: string) => labelOf(planStateLabels, value)
export const progressItems = [
  { key: 'success', label: '成功' },
  { key: 'failed', label: '异常' },
  { key: 'pending', label: '进行中' },
  { key: 'unknown', label: '未确认' },
  { key: 'skipped', label: '未执行' },
] as const satisfies readonly { key: keyof PlanProgress; label: string }[]
