import type { PreviewChannel, PreviewProfile } from '../../api/preview/types'
import type { MenuRoute } from '../../api/auth/types'

export type StreamUsage = 'MAIN' | 'SUB'
export type PreviewMenuAction = 'snapshot' | 'fullscreen' | 'reconnect' | 'close' | 'diagnostics'
export interface PreviewDevice {
  key: string
  name: string
  channels: PreviewChannel[]
}
export interface PreviewGroup {
  path: string
  devices: PreviewDevice[]
}
export const usageLabels: Record<string, string> = {
  MAIN: '主码流',
  SUB: '辅码流',
  THIRD: '第三码流',
  CUSTOM: '自定义',
  UNKNOWN: '未指定',
}
export function selectProfile(channel: PreviewChannel, usage: StreamUsage): PreviewProfile | null {
  return (
    channel.profiles.find(
      (profile) => profile.enabled && profile.configured && profile.usage === usage,
    ) ?? null
  )
}
export function groupCatalog(channels: PreviewChannel[]): PreviewGroup[] {
  const devices = new Map<string, PreviewDevice>()
  for (const channel of channels) {
    let device = devices.get(channel.deviceGroupKey)
    if (!device) {
      device = {
        key: channel.deviceGroupKey,
        name: channel.deviceName || channel.name,
        channels: [],
      }
      devices.set(channel.deviceGroupKey, device)
    }
    device.channels.push(channel)
  }
  const groups = new Map<string, PreviewGroup>()
  for (const device of devices.values()) {
    const paths = new Set(device.channels.map((channel) => channel.groupPath || '未分组'))
    const path = paths.size === 1 ? [...paths][0]! : '多个分组'
    let group = groups.get(path)
    if (!group) {
      group = { path, devices: [] }
      groups.set(path, group)
    }
    group.devices.push(device)
  }
  return [...groups.values()]
}
export function findPreviewPath(routes: MenuRoute[]): string | null {
  for (const route of routes) {
    if (
      route.type === 'PAGE' &&
      route.moduleKey === 'camera' &&
      route.componentKey === '/preview/Center'
    )
      return route.path
    const found = findPreviewPath(route.children)
    if (found) return found
  }
  return null
}
export function previewReason(code: string | null): string {
  const reasons: Record<string, string> = {
    NODE_SOURCE_CAPACITY: '该节点源流预算已满，新源可分配到其他节点',
    NODE_START_CAPACITY: '该节点启动并发已满，请稍后重试',
    NODE_INGRESS_CAPACITY: '该节点入站带宽预算不足',
    NODE_EGRESS_CAPACITY: '该节点出口带宽预算不足，同源观看不能转投其他节点',
    NODE_NO_CAPACITY: '当前没有可接收新预览的节点',
    NODE_UNOWNED_RESOURCES: '节点存在未登记资源，请先核对资源责任',
    NODE_BUDGET_MISMATCH: '预算超出节点已应用配置，请核对节点配置',
    NODE_REGISTRY_FULL: '预览节点登记数量已达上限',
    SOURCE_ASSIGNMENT_CONFLICT: '源流已有其他节点归属，请重试原请求',
    NODE_UNREACHABLE: '无法连接预览节点，请管理员检查网络、证书和节点服务',
    NODE_UNAUTHORIZED: '预览节点机器身份已失效，请管理员核对凭据',
    NODE_RECONCILING: '预览节点正在核对旧资源，请稍后重连',
    NODE_DRAINING: '预览节点已停止接收新观看，现有观看可继续',
    NODE_DRAIN_REQUIRED: '请先关闭当前预览并确认节点资源归零',
    NODE_MAINTENANCE: '节点正在维护，请等待维护完成并明确启用',
    MAINTENANCE_IN_PROGRESS: 'Ops 正在维护该节点，请先在 Ops 核对作业结果',
    MAINTENANCE_PERMIT_STALE: '维护许可已过期或节点已重启，请重新获取许可',
    MAINTENANCE_PERMIT_MISMATCH: '维护许可与节点不一致，请刷新维护状态',
    MAINTENANCE_JOB_MISMATCH: '维护作业编号不一致，请核对原 Ops 作业',
    NODE_MAINTENANCE_DAMAGED: '节点维护记录异常，请核对节点状态目录和日志',
    NODE_CONFIGURATION_CHANGED: '预览节点配置或预算已变化，请排空后重新登记',
    NODE_MEDIA_UNAVAILABLE: '节点媒体环境尚未就绪，请检查 ZLM 与 FFmpeg 配置',
    NODE_IDENTITY_CHANGED: '预览节点已重启或身份变化，请重连并核对节点',
    NODE_OPERATION_PENDING: '节点操作结果待核对，请稍后重试关闭',
    NODE_REGISTRATION_MISSING: '请先配置平台的预览节点登记文件',
    NODE_CREDENTIAL_DIRECTORY_MISSING: '请先配置 Platform 的受保护机器凭据目录',
    NODE_REGISTRATION_INVALID: '预览节点登记信息无效，请核对地址、证书指纹与受保护凭据文件',
    OPS_UNAVAILABLE: '无法读取 Ops 实例目录，请检查 Ops 服务和平台连接配置',
    OPS_CONFIGURATION_INVALID: 'Ops 连接配置无效，请检查平台服务端设置',
    OPS_CREDENTIAL_INVALID: 'Ops 实例交接凭据无效，请检查实例部署和平台凭据',
    OPS_RELEASE_CHANGED: 'Ops 实例版本已变化，请刷新列表后重新引入',
    OPS_RESPONSE_INVALID: 'Ops 实例目录响应无效，请检查服务版本和日志',
    OPS_NOT_CONFIGURED: 'Platform 尚未配置 Ops 实例目录连接',
    OPS_SELECTION_INVALID: '所选 Ops 实例无效，请刷新列表后重试',
    NODE_ALREADY_REGISTERED: '节点凭据已存在，请核对当前登记后再操作',
    NODE_CREDENTIAL_WRITE_FAILED: '节点凭据保存失败，请检查 Platform 受保护目录权限',
    SOURCE_GENERATION_PENDING: '该码流旧配置仍有未关闭的预览，请先退出旧观看',
    NO_PROFILE: '没有可用码流，请检查通道配置',
    PROFILE_UNAVAILABLE: '所选码流不可用',
    NO_MAIN_PROFILE: '没有可用主码流',
    NO_SUB_PROFILE: '没有可用辅码流',
    CAMERA_DISABLED: '通道已停用',
    ZLM_UNAVAILABLE: '媒体服务不可用',
    ZLM_NOT_READY: '媒体服务尚未就绪，请检测运行环境',
    RUNTIME_UNAVAILABLE: '媒体环境不可用',
    CODEC_UNSUPPORTED: '浏览器不支持此视频编码',
    UNSUPPORTED_CODEC: '浏览器不支持此视频编码',
    FFMPEG_UNAVAILABLE: '转码环境不可用',
    SOURCE_UNAVAILABLE: '暂时无法获取相机视频',
    MEDIA_NOT_READY: '媒体暂未就绪',
    LOCATOR_NOT_CONFIGURED: '视频地址未配置，请检查通道连接',
    LOCATOR_INVALID: '视频地址无效，请检查通道连接',
    SOURCE_PROTOCOL_NOT_SUPPORTED: '该接入方式暂不支持预览',
    TRANSCODE_NOT_CONFIGURED: '浏览器需要转码，请检查媒体转码配置',
    TRANSCODE_VERSION_MISMATCH: '转码版本与选择的运行环境不一致',
    SIGNAL_REJECTED: '媒体连接被拒绝，请检查媒体服务',
    PROFILE_NOT_FOUND: '码流已不存在，请刷新相机列表',
    PROFILE_DISABLED: '码流已停用，请切换其他码流',
    DESTINATION_REJECTED: '视频地址不在允许访问的范围',
    DNS_UNAVAILABLE: '无法解析相机地址，请检查网络',
    UPSTREAM_REJECTED: '相机拒绝视频请求，请检查连接资料',
    BROWSER_UNSUPPORTED: '当前浏览器不支持视频预览',
    CONNECT_TIMEOUT: '连接超时，请重连',
    FIRST_FRAME_TIMEOUT: '尚未收到视频画面，请重连或检查相机编码',
    LEASE_EXPIRED: '预览已过期，请重连',
    CONNECTION_FAILED: '视频连接中断，请重连',
    VIDEO_CODEC_NEGOTIATION_FAILED: '视频编码协商失败，请切换码流或检查转码配置',
    VIDEO_DECODE_FAILED: '视频解码失败，请切换码流或检查转码配置',
    VIDEO_PLAYBACK_FAILED: '视频播放中断，请重连或检查网络',
    LEASE_RELEASE_FAILED: '预览资源释放未确认，请重试关闭后再打开',
    PREVIEW_TIMEOUT: '媒体准备超时，请重连或检查媒体服务',
    PREVIEW_BUSY: '预览资源繁忙，请稍后重试',
    PREVIEW_CAPACITY: '该节点观看预算已满，同源观看须等待原节点释放额度',
    TRANSCODE_CAPACITY: '该节点共享转码预算不足',
    SOURCE_REUSE_UNAVAILABLE: '原节点共享源不可复用，请关闭旧观看后重试',
    NODE_HOST_OVERLOADED: '节点 CPU 或内存持续高负载，已暂停新准入，已有观看保留',
    NODE_BITRATE_EXCEEDED: '实际码率持续超过配置上界，已暂停新准入，请核对预算',
    PREVIEW_CAPACITY_EXCEEDED: '全站预览容量已满，请关闭其他画面后重试',
    RATE_LIMITED: '预览配额或请求上限已达，请关闭其他画面或稍后重试',
    TRANSCODE_CAPACITY_EXCEEDED: '转码容量已满，请选择浏览器支持的码流或关闭其他转码画面',
    TRANSCODE_DISABLED: '服务器未开放软件转码，请选择浏览器支持的码流',
    TRANSCODE_RECOVERY_REQUIRED: '转码登记待核对，请管理员检查工作目录和媒体进程',
    PREVIEW_RECOVERY_REQUIRED:
      '重启后的预览资源正在核对，请稍后重连；持续失败请管理员检查媒体服务和资源日志',
    CLEANUP_PENDING: '预览资源释放未确认，请稍后重试关闭',
    CANCELLED: '预览已取消',
  }
  return reasons[code || ''] || '暂时无法预览，请检查相机和媒体服务'
}
