import type { PreviewChannel, PreviewProfile } from '../../api/preview/types'
import type { MenuRoute } from '../../api/auth/types'
export { usageLabels } from '../camera/streamLabels'

export type StreamUsage = 'MAIN' | 'SUB'
export type PreviewMenuAction = 'snapshot' | 'maximize' | 'reconnect' | 'close' | 'diagnostics'
export interface PreviewDevice {
  key: string
  name: string
  channels: PreviewChannel[]
}
export interface PreviewGroup {
  path: string
  devices: PreviewDevice[]
}
export function selectProfile(channel: PreviewChannel, usage: StreamUsage): PreviewProfile | null {
  return (
    channel.profiles.find(
      (profile) => profile.enabled && profile.configured && profile.usage === usage,
    ) ??
    // A manually registered RTSP stream has no MAIN/SUB classification. It remains playable
    // from the default device view without silently substituting a known main for a missing sub.
    channel.profiles.find(
      (profile) => profile.enabled && profile.configured && profile.usage === 'UNKNOWN',
    ) ??
    null
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
    INGRESS_NOT_CONFIGURED: '主动接入尚未启用，请管理员配置本机 ZLM 接入与发布鉴权',
    INGRESS_ORIGIN_REJECTED: '接入地址与管理员配置的 ZLM 地址或 RTSP 端口不一致',
    INGRESS_REQUIRES_LOCAL_ZLM: '主动接入要求部署选定的本机 ZLM',
    INGRESS_ZLM_HOOK_NOT_CONFIGURED: 'ZLM 发布回调或 RTSP 端口配置不一致，请管理员核对',
    INGRESS_ZLM_UNAVAILABLE: '无法读取主动接入的 ZLM 状态，请检查日志与本机服务',
    INGRESS_ZLM_REJECTED: 'ZLM 拒绝了主动接入请求，请核对版本与配置',
    PUSH_STREAM_NOT_CONFIGURED: '此推流 ID 尚未加入管理员发布白名单',
    PUSH_STREAM_NOT_ONLINE: '相机尚未推流，请检查发布地址与推流令牌',
    GB_NOT_CONFIGURED: 'GB28181 信令服务尚未启用',
    GB_DEVICE_NOT_REGISTERED: '设备未注册或心跳已过期，请检查 SIP 地址、设备 ID、密码和网络',
    GB_CHANNEL_NOT_FOUND: '未收到此通道的目录信息，请核对设备目录与通道 ID',
    GB_INVITE_REJECTED: '设备拒绝实时点播，请检查通道及设备端日志',
    GB_INVITE_TIMEOUT: '设备点播响应超时，请检查 SIP 通信与防火墙',
    GB_SDP_UNSUPPORTED: '设备未协商 PS/90000 视频，当前不支持此媒体格式',
    GB_SIGNAL_UNAVAILABLE: 'GB28181 信令通信失败，请检查服务与设备网络',
    GB_SESSION_CAPACITY: 'GB28181 接收会话已满，请先关闭空闲预览',
    GB_RTP_OPEN_FAILED: 'ZLM RTP 接收端口创建失败，请检查端口占用和权限',
    GB_ZLM_VERSION_UNSUPPORTED: 'ZLM 不支持按应用隔离的 RTP 接收，请升级兼容版本',
    GB_RECOVERY_REQUIRED: 'RTP 接收资源清理尚未确认，请管理员核对接入日志与责任日志',
    HTTP_MEDIA_REQUIRES_LOCAL_NODE: 'HTTP 视频源需要托管预览节点的安全转发，不能使用直连 ZLM 节点',
    HTTP_MEDIA_REDIRECT_REJECTED: '视频地址返回重定向，请登记允许网络内的最终地址',
    HTTP_MEDIA_ORIGIN_REJECTED: 'HLS 引用了其他来源地址，当前仅允许同源播放列表和分片',
    HTTP_MEDIA_UPSTREAM_REJECTED: 'HTTP 视频服务器拒绝请求，请核对地址与凭据',
    HTTP_MEDIA_UPSTREAM_UNAVAILABLE: 'HTTP 视频读取中断或超时，请检查源服务',
    HTTP_MEDIA_CAPACITY: 'HTTP 视频接入容量已满，请稍后重试',
    HTTP_MEDIA_UNAVAILABLE: 'HTTP 视频转发不可用，请管理员检查节点日志',
    HTTP_MEDIA_RESOURCE_TOO_LARGE: 'HLS 分片超过接入上限，请检查分片大小',
    HLS_PLAYLIST_INVALID: 'HLS 播放列表格式或嵌套引用无效',
    HLS_ENCRYPTION_UNSUPPORTED: '当前不支持加密 HLS，请提供未加密 MPEG-TS HLS',
    HLS_FMP4_UNSUPPORTED: '当前不支持 fMP4 或低延迟 HLS，请提供 MPEG-TS HLS',
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
    NODE_CREDENTIAL_DIRECTORY_MISSING: '平台密钥存储不可用，请检查服务账号权限',
    NODE_REGISTRATION_INVALID: '预览节点登记信息无效，请核对地址、证书指纹与受保护凭据文件',
    OPS_UNAVAILABLE: '无法读取 Ops 实例目录，请检查 Ops 服务和平台连接配置',
    OPS_CONFIGURATION_INVALID: 'Ops 连接配置无效，请检查平台服务端设置',
    OPS_CREDENTIAL_INVALID: 'Ops 实例交接凭据无效，请检查实例部署和平台凭据',
    OPS_RELEASE_CHANGED: 'Ops 实例版本已变化，请刷新列表后重新引入',
    OPS_IDENTITY_REQUIRED: 'Ops 目录缺少稳定实例身份，请先升级 Ops 后重试',
    OPS_INSTANCE_MISMATCH: '所选实例与节点原 Ops 来源不一致，不允许改绑',
    OPS_INSTANCE_ALREADY_LINKED: '该 Ops 实例已关联其他节点，请使用原节点的同步入口',
    OPS_CONNECTION_CHANGED: '节点地址或机器身份不一致，请按独立迁移流程处理',
    OPS_SYNC_REQUIRED: '该节点需要从同步 Ops 配置入口维护，不能重复导入或手工覆盖',
    OPS_SYNC_REVIEW_REQUIRED: '请先完成同步预检，再应用配置',
    OPS_SYNC_REVIEW_CHANGED: '预检后配置已变化，请刷新并重新预检',
    OPS_JOB_PENDING: 'Ops 存在未决作业、目标锁或待确认 ACK，请先核对原作业',
    OPS_HANDOFF_UNAVAILABLE: 'Ops 无法安全交接，请核对目标 executor 版本、实例状态及原作业',
    NODE_RESOURCES_ACTIVE: '节点实际媒体资源未归零，请排空后再同步',
    OPS_RESPONSE_INVALID: 'Ops 实例目录响应无效，请检查服务版本和日志',
    OPS_NOT_CONFIGURED: 'Platform 尚未配置 Ops 实例目录连接',
    OPS_SELECTION_INVALID: '所选 Ops 实例无效，请刷新列表后重试',
    NODE_ALREADY_REGISTERED: '节点凭据已存在，请核对当前登记后再操作',
    NODE_CREDENTIAL_WRITE_FAILED: '节点凭据保存失败，请检查 Platform 受保护目录权限',
    SOURCE_GENERATION_PENDING: '该码流旧配置仍有未关闭的预览，请先退出旧观看',
    NO_PROFILE: '没有可用码流，请检查通道配置',
    PROFILE_UNAVAILABLE: '所选码流不可用',
    NO_MAIN_PROFILE: '没有可用主码流',
    NO_SUB_PROFILE: '没有可用子码流',
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
