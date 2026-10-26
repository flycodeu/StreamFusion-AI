import { labelOf } from '../../utils/display'
import { previewReason } from '../preview/model'
import type { LayerStatus, StatusCheck } from '../../api/camera-status/types'

import { factLabels } from './presentation'
export { executionLabels } from './presentation'
export function isTerminal(check: StatusCheck): boolean {
  return check.executionState !== 'QUEUED' && check.executionState !== 'RUNNING'
}
const reasons: Record<string, string> = {
  PLAN_REQUEST_REUSED: '同一请求编号提交了不同的巡检配置',
  PLAN_ACTIVE_LIMIT: '执行中的巡检任务已达到上限',
  PLAN_TARGET_RANGE_INVALID: '巡检范围为空或超过设备数量上限',
  PLAN_TARGET_UNAVAILABLE: '巡检目标已移除或不在授权范围',
  PLAN_PROFILE_MISSING: '部分设备缺少代表码流',
  PLAN_VERSION_CHANGED: '任务已变化，请刷新后重试',

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
  STATUS_LAYER_NOT_APPLICABLE: '此来源使用对应平台的状态',
  PLATFORM_ONLINE: '平台报告在线',
  PLATFORM_OFFLINE: '平台报告离线',
  PLATFORM_STATUS_UNKNOWN: '平台返回未知状态',
  PLATFORM_STATUS_MISSING: '平台未返回此相机的状态',
  PLATFORM_STATUS_TIMEOUT: '平台状态查询超时',
  PLATFORM_STATUS_UNAVAILABLE: '平台状态暂不可获取',
  PLATFORM_API_REJECTED: '平台拒绝请求，请核对密钥和接口权限',
  PLAN_CAPACITY_EXCEEDED: '巡检容量不足，请增加检查间隔或减少目标',
  MEDIA_PLAN_CAPACITY_EXCEEDED: '视频采样容量不足，请增加检查间隔或减少目标',
  PLAN_TARGET_NOT_CHECKABLE: '部分巡检目标不可检测，请核对设备、码流配置和授权范围',
  PLAN_EXPIRED: '任务已经到期',
  CONFIGURATION_UNAVAILABLE: '连接配置暂不可用',
  CHANNEL_DISABLED: '通道未启用',
  ACTOR_REVOKED: '检查资格已失效',
  CHECK_NO_LONGER_ACTIVE: '检查已结束或中断',
  CHECK_EXPIRED: '检查期限已过',
  SESSION_STORE_UNAVAILABLE: '会话校验服务暂不可用',
  SERVICE_RESTARTED: '服务重启，检查已中断',
  PLAN_NO_LONGER_ACTIVE: '巡检计划已停止或到期',
  MISSED_SLOT: '错过检测时间，等待下一轮',
  MEDIA_RECEIVING: '采样窗口内收到视频帧',
  MEDIA_NO_FRAMES: '采样窗口内未收到视频帧',
  MEDIA_EVIDENCE_INSUFFICIENT: '视频收帧证据不足',
  MEDIA_INSTANCE_CHANGED: '媒体实例已变化，请重新检查',
  MEDIA_COUNTER_RESET: '接收帧计数已重置，请重新检查',
  MEDIA_BUSY: '视频检查繁忙',
  MEDIA_RECOVERY_REQUIRED: '上次视频检查资源尚未确认回收',
  MEDIA_TIMEOUT: '视频检查超时，未取得有效结果',
  MEDIA_RUNTIME_UNAVAILABLE: '媒体服务暂不可用',
}
export function reasonLabel(reasonCode: string | null): string {
  if (reasonCode && /^(GB_|INGRESS_|PUSH_STREAM_|HTTP_MEDIA_|HLS_)/.test(reasonCode))
    return previewReason(reasonCode)
  return labelOf(reasons, reasonCode, '')
}
export function factPresentation(profile: LayerStatus | null) {
  if (!profile) return { label: '状态未读取', tone: 'info' as const, historical: false }
  if (!profile.observation) return { label: '尚未检查', tone: 'info' as const, historical: false }
  if (!profile.configurationCurrent)
    return { label: '待刷新', tone: 'info' as const, historical: true }
  if (profile.freshness !== 'CURRENT')
    return { label: '待刷新', tone: 'info' as const, historical: true }
  return {
    label: factLabels[profile.layer][profile.observation.value],
    tone:
      profile.observation.value === 'ACCESSIBLE'
        ? ('success' as const)
        : profile.observation.value === 'FAILED'
          ? ('warning' as const)
          : ('info' as const),
    historical: false,
  }
}
