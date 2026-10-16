import type { StatusChannel, StatusLayer, StatusSummary } from '../../api/camera-status/types'
import { layerKey } from '../../api/camera-status/types'

export function representativeProfile(channel: StatusChannel) {
  const enabled = channel.profiles.filter((profile) => profile.enabled)
  enabled.sort(
    (a, b) =>
      a.streamProfileId.length - b.streamProfileId.length ||
      a.streamProfileId.localeCompare(b.streamProfileId),
  )
  return (
    enabled.find((profile) => profile.streamProfileId === channel.defaultPreviewProfileId) ??
    enabled.find((profile) => profile.streamKind === 'MAIN') ??
    enabled[0] ??
    null
  )
}

export type ConnectionState = Exclude<keyof StatusSummary, 'total'>

function representativeLayer(channel: StatusChannel, layer: StatusLayer) {
  const profile = representativeProfile(channel)
  return profile?.upstream && profile.upstream.blockingReasonCode !== 'STATUS_LAYER_NOT_APPLICABLE'
    ? profile.upstream
    : profile?.[layerKey(layer)]
}

/** Current-page representative facts only, never a whole-catalog online rate. */
export function pageCoverage(channels: StatusChannel[], layer: StatusLayer) {
  let enabled = 0,
    current = 0
  for (const channel of channels) {
    if (channel.lifecycle !== 'ENABLED') continue
    ++enabled
    const fact = representativeLayer(channel, layer)
    if (
      fact?.configurationCurrent &&
      fact.freshness === 'CURRENT' &&
      (fact.observation?.value === 'ACCESSIBLE' || fact.observation?.value === 'FAILED')
    )
      ++current
  }
  return { enabled, current, percent: enabled ? Math.round((current * 100) / enabled) : null }
}

export function representativeConnection(
  channel: StatusChannel,
  layer: StatusLayer = 'CONTROL',
): ConnectionState {
  if (channel.lifecycle !== 'ENABLED') return 'disabled'
  const profile = representativeLayer(channel, layer)
  if (!profile) return 'unknown'
  if (['QUEUED', 'RUNNING'].includes(profile.latestCheck?.executionState ?? '')) return 'checking'
  if (!profile.observation) return 'unknown'
  if (!profile.configurationCurrent || profile.freshness !== 'CURRENT') return 'stale'
  if (profile.observation.value === 'ACCESSIBLE') return 'accessible'
  if (profile.observation.value === 'FAILED') return 'failed'
  return 'unknown'
}
export const mediaLabels: Record<ConnectionState, string> = {
  accessible: '采样收帧正常',
  failed: '采样收帧异常',
  checking: '检测中',
  stale: '配置已变更',
  unknown: '尚未检查',
  disabled: '未启用',
}

export const connectionLabels: Record<ConnectionState, string> = {
  accessible: '代表流可达',
  failed: '连接异常',
  checking: '检测中',
  stale: '配置已变更',
  unknown: '未确认',
  disabled: '未启用',
}
export const upstreamLabels: Record<ConnectionState, string> = {
  accessible: '平台在线',
  failed: '平台离线',
  checking: '查询中',
  stale: '配置已变更',
  unknown: '未获取',
  disabled: '未启用',
}
