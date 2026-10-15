import type { ExecutionState, ProfileStatus, StatusCheck } from '../../api/camera-status/types'

export const executionLabels: Record<ExecutionState, string> = {
  QUEUED: '等待执行',
  RUNNING: '正在检查',
  COMPLETED: '检查结束',
  SKIPPED: '未执行',
  CANCELLED: '已取消',
  INTERRUPTED: '执行中断',
}
export function isTerminal(check: StatusCheck): boolean {
  return check.executionState !== 'QUEUED' && check.executionState !== 'RUNNING'
}
const reasons: Record<string, string> = {
  RTSP_ACCESSIBLE: 'RTSP 描述可访问',
  AUTH_REJECTED: '认证未通过',
  AUTH_UNSUPPORTED: '认证方式不支持',
  RTSP_PATH_NOT_FOUND: 'RTSP 路径不存在',
  RTSP_TIMEOUT: 'RTSP 检查超时',
  RTSP_CONNECTION_FAILED: 'RTSP 连接失败',
  SDP_INVALID: '视频描述无效',
  RTSP_REDIRECT_REJECTED: '未跟随重定向',
  RTSP_REJECTED: 'RTSP 请求被拒绝',
  RTSP_UNSUPPORTED: 'RTSP 版本不支持',
  RTSP_INVALID_RESPONSE: 'RTSP 响应无效',
  STREAM_URI_TIMEOUT: '获取 RTSP 地址超时',
  STREAM_URI_AUTH_REJECTED: '获取 RTSP 地址时认证未通过',
  STREAM_URI_UNAVAILABLE: '暂时无法获取 RTSP 地址',
  STREAM_URI_INVALID: '设备返回的 RTSP 地址无效',
  NETWORK_BLOCKED: '网络策略未允许',
  INVALID_TARGET: '检查目标无效',
  PROBE_UNAVAILABLE: '检查服务暂不可用',
  DNS_UNAVAILABLE: '地址解析失败',
  WORKER_BUSY: '检查容量已满',
  PROBE_BUSY: '探针繁忙',
  SOURCE_BUSY: '接入来源正在检查',
  PROFILE_BUSY: '该码流正在检查',
  ENDPOINT_BUSY: '设备端点正在检查',
  DNS_BUSY: '地址解析繁忙',
  START_BUDGET_BUSY: '启动预算已满',
  QUEUE_DEADLINE_EXCEEDED: '等待执行超时',
  CANCELLED: '检查已取消',
  INTERRUPTED: '检查已中断',
  CONFIGURATION_CHANGED: '连接配置已变化',
  CAMERA_DISABLED: '通道未启用',
  PROFILE_DISABLED: '码流未启用',
  LOCATOR_NOT_CONFIGURED: '未配置 RTSP 地址',
  RTSP_ENDPOINT_NOT_CONFIGURED: '未配置 RTSP 主机或端口',
  ONVIF_MEDIA_NOT_CONFIGURED: '缺少 ONVIF 媒体信息，请重新发现设备',
  PROFILE_NOT_CONFIGURED: '没有已启用的码流配置',
  TARGET_UNAVAILABLE: '目标未启用、已移除或不在当前授权范围',
  UNSUPPORTED_LOCATOR: '此地址类型的连接检测尚未适配',
  FORBIDDEN: '没有手动检查权限',
  SOURCE_DISABLED: '接入来源未启用',
  TARGET_DELETED: '检查目标已删除',
  LOCATOR_INVALID: 'RTSP 地址配置无效',
  SOURCE_PROTOCOL_NOT_SUPPORTED: '此接入方式的连接检测尚未适配',
  CONFIGURATION_UNAVAILABLE: '连接配置暂不可用',
  CHANNEL_DISABLED: '通道未启用',
  ACTOR_REVOKED: '检查资格已失效',
  CHECK_NO_LONGER_ACTIVE: '检查已结束或中断',
  CHECK_EXPIRED: '检查期限已过',
  SESSION_STORE_UNAVAILABLE: '会话校验服务暂不可用',
  SERVICE_RESTARTED: '服务重启，检查已中断',
  PLAN_NO_LONGER_ACTIVE: '巡检计划已停止或到期',
  MISSED_SLOT: '错过检测时间，等待下一轮',
}
export function reasonLabel(reasonCode: string | null): string {
  return reasonCode ? (reasons[reasonCode] ?? reasonCode) : ''
}
export function factPresentation(profile: ProfileStatus | null) {
  if (!profile) return { label: '状态未读取', tone: 'info' as const, historical: false }
  if (!profile.observation) return { label: '尚未检查', tone: 'info' as const, historical: false }
  if (!profile.configurationCurrent)
    return { label: '配置已变化', tone: 'info' as const, historical: true }
  if (profile.freshness !== 'CURRENT')
    return { label: '结果已过期', tone: 'info' as const, historical: true }
  const values = {
    ACCESSIBLE: { label: '连接检查通过', tone: 'success' as const },
    FAILED: { label: '连接检查异常', tone: 'warning' as const },
    UNSUPPORTED: { label: '检测待适配', tone: 'info' as const },
  }
  return { ...values[profile.observation.value], historical: false }
}
