import type { StatusChannel, StatusSummary } from '../../api/camera-status/types'
import type { MenuRoute } from '../../api/auth/types'

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

export function representativeConnection(channel: StatusChannel): ConnectionState {
  if (channel.lifecycle !== 'ENABLED') return 'disabled'
  const profile = representativeProfile(channel)
  if (!profile) return 'unknown'
  if (['QUEUED', 'RUNNING'].includes(profile.latestCheck?.executionState ?? '')) return 'checking'
  if (!profile.observation) return 'unknown'
  if (!profile.configurationCurrent || profile.freshness !== 'CURRENT') return 'stale'
  if (profile.observation.value === 'ACCESSIBLE') return 'accessible'
  if (profile.observation.value === 'FAILED') return 'failed'
  return 'unknown'
}

export const connectionLabels: Record<ConnectionState, string> = {
  accessible: '代表流可达',
  failed: '连接异常',
  checking: '检测中',
  stale: '结果已过期',
  unknown: '未确认',
  disabled: '未启用',
}

export function summarizeChannels(channels: StatusChannel[]): StatusSummary {
  const summary: StatusSummary = {
    total: channels.length,
    accessible: 0,
    failed: 0,
    checking: 0,
    stale: 0,
    unknown: 0,
    disabled: 0,
  }
  for (const channel of channels) ++summary[representativeConnection(channel)]
  return summary
}

export function findCameraStatusPath(routes: MenuRoute[]): string | null {
  for (const route of routes) {
    if (
      route.type === 'PAGE' &&
      route.moduleKey === 'camera' &&
      route.componentKey === '/camera/Status'
    )
      return route.path
    const found = findCameraStatusPath(route.children)
    if (found) return found
  }
  return null
}

export function channelConnection(channel: StatusChannel) {
  if (channel.lifecycle !== 'ENABLED')
    return { label: channel.lifecycle === 'DISABLED' ? '已停用' : '待归档', tone: 'info' as const }
  const enabled = channel.profiles.filter((profile) => profile.enabled)
  if (
    enabled.some((profile) =>
      ['QUEUED', 'RUNNING'].includes(profile.latestCheck?.executionState ?? ''),
    )
  )
    return { label: '检查中', tone: 'info' as const }
  const current = enabled.filter(
    (profile) => profile.configurationCurrent && profile.freshness === 'CURRENT',
  )
  const accessible = current.some((profile) => profile.observation?.value === 'ACCESSIBLE')
  const failed = current.some((profile) => profile.observation?.value === 'FAILED')
  if (accessible && failed) return { label: '部分连接异常', tone: 'warning' as const }
  if (failed) return { label: '连接异常', tone: 'warning' as const }
  if (accessible) return { label: '控制连接可达', tone: 'success' as const }
  return { label: '尚无有效结果', tone: 'info' as const }
}

export function checkAvailability(reason: string | null): string {
  return reason === 'SOURCE_PROTOCOL_NOT_SUPPORTED' || reason === 'UNSUPPORTED_LOCATOR'
    ? '检测待适配'
    : ''
}
