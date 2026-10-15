import type { DiagnosticEvent } from '../../api/diagnostics/api'
import { reasonLabel } from '../camera-status/model'

const reasons: Record<string, string> = {
  CONFLICT: '操作与当前状态冲突',
  VALIDATION_ERROR: '请求参数不符合要求',
  UNAUTHORIZED: '会话未登录或已失效',
  FORBIDDEN: '没有操作权限',
  NOT_FOUND: '目标不存在或不可访问',
  INTERNAL_ERROR: '服务内部异常',
  DEPENDENCY_UNAVAILABLE: '依赖服务暂不可用',
  RATE_LIMITED: '请求频率超过限制',
  PLAN_REQUEST_REUSED: '同一请求编号提交了不同的巡检配置',
  PLAN_ACTIVE_LIMIT: '执行中的巡检任务已达到上限',
  PLAN_TARGET_RANGE_INVALID: '巡检范围为空或超过设备数量上限',
  PLAN_TARGET_UNAVAILABLE: '巡检目标已移除或不在授权范围',
  PLAN_PROFILE_MISSING: '部分设备缺少代表码流',
  PLAN_TARGET_NOT_CHECKABLE: '部分设备的连接配置不满足检查条件',
  PLAN_CAPACITY_EXCEEDED: '巡检频率超过当前检查容量',
  PLAN_VERSION_CHANGED: '任务已变化，请刷新后重试',
}
export function eventReason(event: DiagnosticEvent): string {
  if (event.reasonCode) return reasons[event.reasonCode] ?? reasonLabel(event.reasonCode)
  return event.errorCode ? (reasons[event.errorCode] ?? '其他错误（见诊断代码）') : '—'
}
export function eventResult(event: DiagnosticEvent): string {
  if (event.kind === 'REQUEST')
    return event.status != null && event.status < 400 ? '请求成功' : '请求失败'
  if (event.state === 'QUEUED') return '等待检查'
  if (event.state === 'RUNNING') return '正在检查'
  if (event.result === 'ACCESSIBLE') return '连接通过'
  if (event.result === 'FAILED') return '连接异常'
  if (event.state === 'CANCELLED') return '已取消'
  if (event.state === 'INTERRUPTED') return '已中断'
  if (event.state === 'SKIPPED') return '未执行'
  return '检查结束'
}
