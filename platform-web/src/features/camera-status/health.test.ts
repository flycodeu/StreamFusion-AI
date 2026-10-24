import { describe, expect, it } from 'vitest'
import type { StatusChannel, StatusProfile, LayerStatus } from '../../api/camera-status/types'
import { representativeProfile, representativeConnection, pageCoverage } from './health'

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
  it('counts current successful and failed facts, not an online rate or profile count', () => {
    const failed = {
      ...channel,
      cameraId: '2',
      profiles: [
        {
          ...profile,
          control: {
            ...profile.control,
            observation: { ...profile.control.observation!, value: 'FAILED' as const },
          },
        },
      ],
    }
    const result = pageCoverage(
      [channel, failed, { ...channel, cameraId: '3', lifecycle: 'DISABLED' }],
      'CONTROL',
    )
    expect(result).toEqual({ enabled: 2, current: 2, percent: 100 })
    expect(representativeConnection(failed, 'CONTROL')).toBe('failed')
  })
  it('excludes stale, changed, missing, unsupported and disabled evidence from coverage', () => {
    const variants: StatusChannel[] = [
      { ...channel, profiles: [] },
      ...[
        { ...profile.control, freshness: 'STALE' as const },
        { ...profile.control, configurationCurrent: false },
        {
          ...profile.control,
          observation: { ...profile.control.observation!, value: 'UNSUPPORTED' as const },
        },
      ].map((control) => ({ ...channel, profiles: [{ ...profile, control }] })),
    ]
    expect(pageCoverage(variants, 'CONTROL')).toEqual({ enabled: 4, current: 0, percent: 0 })
    expect(pageCoverage([], 'MEDIA')).toEqual({ enabled: 0, current: 0, percent: null })
    expect(pageCoverage([{ ...channel, lifecycle: 'DISABLED' }], 'CONTROL').percent).toBeNull()
  })
  it('prefers platform evidence and preserves a valid prior fact during a new check', () => {
    const upstream: LayerStatus = {
      ...profile.control,
      layer: 'UPSTREAM',
      probeKind: 'HIK_PLATFORM_ONLINE',
      latestCheck: { executionState: 'RUNNING' } as LayerStatus['latestCheck'],
    }
    const platform = { ...channel, profiles: [{ ...profile, upstream }] }
    expect(pageCoverage([platform], 'MEDIA').current).toBe(1)
    expect(representativeConnection(platform, 'MEDIA')).toBe('checking')
    expect(
      pageCoverage(
        [{ ...channel, profiles: [{ ...profile, upstream: { ...upstream, freshness: 'STALE' } }] }],
        'CONTROL',
      ).current,
    ).toBe(0)
  })
  it('uses server detection priority independently of the preview default', () => {
    const sub = { ...profile, streamProfileId: '5', streamKind: 'SUB' }
    expect(
      representativeProfile({ ...channel, defaultPreviewProfileId: '2', profiles: [sub, profile] }),
    ).toBe(sub)
    expect(
      representativeProfile({
        ...channel,
        defaultPreviewProfileId: '5',
        profiles: [profile, { ...sub, enabled: false }],
      }),
    ).toBe(profile)
  })
  it('preserves server cost ordering without sorting or mutating the source array', () => {
    const larger = { ...profile, streamProfileId: '100', streamKind: 'UNKNOWN' }
    const smaller = { ...profile, streamProfileId: '9', streamKind: 'UNKNOWN' }
    const profiles = [larger, smaller]
    expect(representativeProfile({ ...channel, profiles })).toBe(larger)
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
        defaultPreviewProfileId: '2',
        profiles: [failed, profile],
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
  it('does not reselect MAIN ahead of the server-selected lower-cost stream', () => {
    const sub = { ...profile, streamProfileId: '5', streamKind: 'SUB' }
    expect(representativeProfile({ ...channel, profiles: [sub, profile] })).toBe(sub)
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
