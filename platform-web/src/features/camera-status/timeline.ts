import type { LayerStatus } from '../../api/camera-status/types'

export function observationSpan(profile: LayerStatus | null): string | null {
  if (
    !profile?.configurationCurrent ||
    profile.observation?.value !== 'FAILED' ||
    !profile.timeline
  )
    return null
  const milliseconds =
    Date.parse(profile.observation.observedAt) - Date.parse(profile.timeline.stateSince)
  if (milliseconds <= 0) return '首次检出'
  const minutes = Math.floor(milliseconds / 60_000)
  if (minutes < 1) return '不足 1 分钟'
  if (minutes < 60) return `${minutes} 分钟`
  const hours = Math.floor(minutes / 60)
  if (hours < 24) return `${hours} 小时 ${minutes % 60} 分钟`
  return `${Math.floor(hours / 24)} 天 ${hours % 24} 小时`
}

export function stateSinceLabel(profile: LayerStatus): string {
  return profile.observation?.value === 'FAILED'
    ? '异常首次检出'
    : profile.observation?.value === 'ACCESSIBLE'
      ? profile.layer === 'MEDIA'
        ? '本轮收帧始于'
        : '本轮可达始于'
      : '本轮结果始于'
}
