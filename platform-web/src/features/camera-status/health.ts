import type { StatusChannel, StatusLayer } from '../../api/camera-status/types'
import { layerKey } from '../../api/camera-status/types'
import type { ConnectionState } from './presentation'
export type { ConnectionState } from './presentation'
export { mediaLabels, connectionLabels, upstreamLabels } from './presentation'

export function representativeProfile(channel: StatusChannel) {
  // Server order follows detection cost; preview defaults serve a separate purpose.
  return channel.profiles.find((profile) => profile.enabled) ?? null
}

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
