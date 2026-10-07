import type {
  AccessConnection,
  AccessMethod,
  AccessCandidate,
  CandidateSelection,
  AccessAdapter,
} from '../../api/camera-access/types'

export const methodLabels: Record<AccessMethod, string> = {
  AUTO: '设备自动识别',
  ONVIF: 'ONVIF 设备',
  HIKVISION: '海康设备',
  DAHUA: '大华设备',
  HIK_PLATFORM: '海康平台',
  RTSP: '完整 RTSP 地址',
}
const warningLabels: Record<string, string> = {
  RTSP_CONFIGURATION_ONLY: '本次只整理视频地址，尚未连接相机验证；编码、分辨率和码率暂未获取。',
  DEVICE_IDENTITY_UNVERIFIED: '未确认设备唯一标识，请核对设备信息后再导入。',
  FIRMWARE_UNAVAILABLE: '未获取到固件版本，其他已读取的信息仍可查看。',
  PROFILE_ENABLE_UNKNOWN: '部分码流未返回启用状态，请在设备端核对。',
  PROFILE_CONFIGURATION_INCOMPLETE: '部分码流配置不完整，仅可导入已明确识别的码流。',
  PLATFORM_STREAM_CAPABILITY_UNVERIFIED: '已读取平台目录，视频地址与播放能力尚未验证。',
  DIRECTORY_CHANGED_DURING_READ: '读取过程中设备目录发生变化，请重新读取后核对通道。',
  DIRECTORY_LIMIT_REACHED: '目录已达到本次读取上限，当前仅显示已读取的通道。',
  DIRECTORY_INCOMPLETE: '目录未完整读取，请核对当前通道，必要时重新读取。',
  CHANNEL_MAPPING_REQUIRED: '部分码流无法明确归属到通道，已暂停这些通道的导入。',
  NON_VIDEO_PROFILE_SKIPPED: '已跳过不包含视频的配置。',
  PROFILE_URI_UNAVAILABLE: '部分码流未获取到视频地址，请核对设备权限与码流配置。',
  PLATFORM_DIRECTORY_ONLY: '已读取平台相机目录，未申请视频地址。可以直接保存相机档案。',
  SCAN_TIME_LIMIT_REACHED: '搜索已达到时间上限，当前显示已发现的端口候选。',
  NO_CHANNEL_IDENTITIES: '已读取设备信息，但没有获得可靠的通道标识。可以返回手动登记已知相机资料。',
}
export function accessWarningLabel(code: string): string {
  return warningLabels[code] ?? `设备返回接入提示：${code}`
}
export interface ConnectionDraft {
  method: AccessMethod
  name: string
  host: string
  port: number
  scheme: 'http' | 'https'
  username: string
  password: string
  rtspPort: number
  networkPolicyKey: string
  rtspUrls: string[]
  reuse: boolean
  sourceId: string | null
  sourceVersion: string | null
}
export function newConnection(): ConnectionDraft {
  return {
    method: 'AUTO',
    name: '',
    host: '',
    port: 80,
    scheme: 'http',
    username: '',
    password: '',
    rtspPort: 554,
    networkPolicyKey: '',
    rtspUrls: [''],
    reuse: false,
    sourceId: null,
    sourceVersion: null,
  }
}
export function connectionError(draft: ConnectionDraft, adapter?: AccessAdapter): string {
  if (draft.reuse && (!draft.sourceId || !draft.sourceVersion)) return '请选择已保存的连接。'
  if (adapter?.inputKind === 'RTSP_URL' || draft.method === 'RTSP') {
    if (!draft.name.trim()) return '请填写相机名称。'
    if (!draft.rtspUrls.length || draft.rtspUrls.length > 8) return '请提供 1～8 个 RTSP 地址。'
    for (const raw of draft.rtspUrls) {
      try {
        const url = new URL(raw.trim())
        if (url.protocol !== 'rtsp:' || !url.hostname || url.hash || /[\r\n\t]/.test(raw))
          return '请填写完整的 rtsp:// 地址，不含锚点或控制字符。'
      } catch {
        return '请填写完整的 rtsp:// 地址。'
      }
    }
  } else if (
    !draft.reuse &&
    (!draft.host.trim() || !Number.isInteger(draft.port) || draft.port < 1 || draft.port > 65535)
  )
    return '请填写设备地址和有效端口。'
  if (
    !draft.reuse &&
    (adapter?.inputKind === 'PLATFORM_APPKEY' || draft.method === 'HIK_PLATFORM') &&
    (!draft.username.trim() || !draft.password)
  )
    return '请填写平台提供的 AppKey 和 AppSecret。'
  if (!draft.reuse && ((draft.username && !draft.password) || (!draft.username && draft.password)))
    return '账号和密码请一起填写，或同时留空。'
  return ''
}
export function connectionInput(draft: ConnectionDraft, adapter?: AccessAdapter): AccessConnection {
  const rtsp = adapter?.inputKind === 'RTSP_URL' || draft.method === 'RTSP'
  if (draft.reuse)
    return {
      method: draft.method,
      sourceId: draft.sourceId!,
      sourceVersion: draft.sourceVersion!,
      ...(draft.networkPolicyKey ? { networkPolicyKey: draft.networkPolicyKey } : {}),
      ...(rtsp
        ? { name: draft.name.trim(), rtspUrls: draft.rtspUrls.map((url) => url.trim()) }
        : {}),
    }
  return {
    method: draft.method,
    name: draft.name.trim(),
    ...(rtsp
      ? { rtspUrls: draft.rtspUrls.map((value) => value.trim()) }
      : {
          host: draft.host.trim(),
          port: draft.port,
          scheme: draft.scheme,
          rtspPort: draft.rtspPort,
        }),
    ...(draft.username ? { username: draft.username, password: draft.password } : {}),
    ...(draft.networkPolicyKey ? { networkPolicyKey: draft.networkPolicyKey } : {}),
  }
}
export function initialSelections(candidates: AccessCandidate[]): CandidateSelection[] {
  return candidates
    .filter((candidate) => !candidate.mappingRequired)
    .map((candidate) => ({
      candidateId: candidate.candidateId,
      profileIds: candidate.profiles.map((profile) => profile.profileId),
      ...(candidate.profiles.length
        ? {
            defaultProfileId: (candidate.profiles.find((profile) => profile.usageHint === 'MAIN') ??
              candidate.profiles[0])!.profileId,
          }
        : {}),
    }))
}
