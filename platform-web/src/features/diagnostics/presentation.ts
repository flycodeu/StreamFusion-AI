import type { DiagnosticEvent } from '../../api/diagnostics/api'
import { reasonLabel, executionLabels } from '../camera-status/model'
import { errorCodeLabels } from '../../lib/http/presentation'
import { labelOf } from '../../utils/display'

export function eventReason(event: DiagnosticEvent): string {
  if (event.reasonCode)
    return Object.hasOwn(errorCodeLabels, event.reasonCode)
      ? labelOf(errorCodeLabels, event.reasonCode)
      : reasonLabel(event.reasonCode)
  return event.errorCode ? labelOf(errorCodeLabels, event.errorCode) : '—'
}
export function eventResult(event: DiagnosticEvent): string {
  if (event.kind === 'REQUEST')
    return event.status != null && event.status < 400 ? '请求成功' : '请求失败'
  if (event.state === 'QUEUED') return executionLabels.QUEUED
  if (event.state === 'RUNNING') return executionLabels.RUNNING
  if (event.result === 'ACCESSIBLE') return '检查通过'
  if (event.result === 'FAILED') return '检查异常'
  return labelOf(executionLabels, event.state, '状态未确认')
}
