import { describe, expect, it } from 'vitest'
import { observationSpan } from './timeline'
import type { LayerStatus } from '../../api/camera-status/types'

const failed: LayerStatus = {
  layer: 'CONTROL',
  probeKind: 'RTSP_DESCRIBE',
  freshness: 'CURRENT',
  configurationCurrent: true,
  latestCheck: null,
  canCheck: true,
  blockingReasonCode: null,
  observation: {
    value: 'FAILED',
    reasonCode: 'RTSP_TIMEOUT',
    observedAt: '2026-10-14T03:00:00Z',
    readAt: '2026-10-14T03:00:00Z',
    validUntil: '2026-10-14T03:05:00Z',
    checkId: '1',
    mediaEvidence: null,
  },
  timeline: {
    stateSince: '2026-10-14T01:00:00Z',
    lastAccessibleAt: null,
    lastFailedAt: '2026-10-14T03:00:00Z',
  },
}
describe('sampled connection timeline', () => {
  it('ends the span at the actual observation even after the result expires', () => {
    expect(observationSpan(failed)).toBe('2 小时 0 分钟')
    expect(observationSpan({ ...failed, freshness: 'STALE' })).toBe('2 小时 0 分钟')
  })
  it('does not invent a duration for missing or changed configuration data', () => {
    expect(observationSpan({ ...failed, timeline: null })).toBeNull()
    expect(observationSpan({ ...failed, configurationCurrent: false })).toBeNull()
    expect(observationSpan(null)).toBeNull()
  })
  it('labels the first observation without claiming a measured downtime', () => {
    expect(
      observationSpan({
        ...failed,
        timeline: { ...failed.timeline!, stateSince: failed.observation!.observedAt },
      }),
    ).toBe('首次检出')
  })
})
