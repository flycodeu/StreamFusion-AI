import { describe, expect, it } from 'vitest'
import type { StatusChannel, StatusProfile, LayerStatus } from '../../api/camera-status/types'
import { representativeProfile, representativeConnection } from './health'

const profile: StatusProfile = {
  streamProfileId: '2',
  name: '主码流',
  streamKind: 'MAIN',
  enabled: true,
  control: {
    layer: 'CONTROL',
    probeKind: 'RTSP_DESCRIBE',
    freshness: 'CURRENT',
    configurationCurrent: true,
    canCheck: true,
    blockingReasonCode: null,
    latestCheck: null,
    observation: {
      value: 'ACCESSIBLE',
      reasonCode: 'RTSP_ACCESSIBLE',
      checkId: '3',
      mediaEvidence: null,
      observedAt: '2026-10-13T01:00:00Z',
      readAt: '2026-10-13T01:00:00Z',
      validUntil: '2026-10-13T01:05:00Z',
    },
    timeline: null,
  },
  media: {
    layer: 'MEDIA',
    probeKind: 'ZLM_RTP_FRAMES',
    observation: null,
    freshness: 'NEVER',
    configurationCurrent: true,
    latestCheck: null,
    canCheck: true,
    blockingReasonCode: null,
    timeline: null,
  },
}
const channel: StatusChannel = {
  cameraId: '1',
  name: '相机',
  deviceName: null,
  lifecycle: 'ENABLED',
  profiles: [profile],
}

describe('channel connection summary', () => {
  it('prefers the enabled default preview even when it is not MAIN', () => {
    const sub = { ...profile, streamProfileId: '5', streamKind: 'SUB' }
    expect(
      representativeProfile({ ...channel, defaultPreviewProfileId: '5', profiles: [profile, sub] }),
    ).toBe(sub)
    expect(
      representativeProfile({
        ...channel,
        defaultPreviewProfileId: '5',
        profiles: [profile, { ...sub, enabled: false }],
      }),
    ).toBe(profile)
  })
  it('uses stable numeric IDs for fallback without sorting the source array', () => {
    const larger = { ...profile, streamProfileId: '100', streamKind: 'UNKNOWN' }
    const smaller = { ...profile, streamProfileId: '9', streamKind: 'UNKNOWN' }
    const profiles = [larger, smaller]
    expect(representativeProfile({ ...channel, profiles })).toBe(smaller)
    expect(profiles).toEqual([larger, smaller])
  })
  it('summarizes the representative only and never mistakes another stream for the chosen stream', () => {
    const failed = {
      ...profile,
      streamProfileId: '4',
      streamKind: 'SUB',
      control: {
        ...profile.control,
        observation: { ...profile.control.observation!, value: 'FAILED' as const },
      },
    }
    expect(representativeConnection({ ...channel, profiles: [profile, failed] })).toBe('accessible')
    expect(
      representativeConnection({
        ...channel,
        defaultPreviewProfileId: '4',
        profiles: [profile, failed],
      }),
    ).toBe('failed')
  })

  it('keeps CONTROL success separate from missing, failed and skipped MEDIA observations', () => {
    expect(representativeConnection(channel)).toBe('accessible')
    expect(representativeConnection(channel, 'MEDIA')).toBe('unknown')
    const media = {
      ...profile.media,
      freshness: 'CURRENT' as const,
      observation: { ...profile.control.observation!, value: 'FAILED' as const },
    }
    expect(
      representativeConnection({ ...channel, profiles: [{ ...profile, media }] }, 'MEDIA'),
    ).toBe('failed')
    const skipped: LayerStatus = {
      ...profile.media,
      latestCheck: { executionState: 'SKIPPED' } as LayerStatus['latestCheck'],
    }
    expect(
      representativeConnection({ ...channel, profiles: [{ ...profile, media: skipped }] }, 'MEDIA'),
    ).toBe('unknown')
    expect(
      representativeConnection(
        { ...channel, profiles: [{ ...profile, media: { ...media, freshness: 'STALE' } }] },
        'MEDIA',
      ),
    ).toBe('stale')
  })
  it('selects one enabled MAIN profile ahead of other enabled profiles', () => {
    const sub = { ...profile, streamProfileId: '5', streamKind: 'SUB' }
    expect(representativeProfile({ ...channel, profiles: [sub, profile] })).toBe(profile)
  })
  it('falls back to the first enabled profile, never to a disabled MAIN', () => {
    const sub = { ...profile, streamProfileId: '5', streamKind: 'SUB' }
    expect(
      representativeProfile({ ...channel, profiles: [{ ...profile, enabled: false }, sub] }),
    ).toBe(sub)
    expect(
      representativeProfile({ ...channel, profiles: [{ ...profile, enabled: false }] }),
    ).toBeNull()
    expect(representativeProfile({ ...channel, profiles: [] })).toBeNull()
  })
})
