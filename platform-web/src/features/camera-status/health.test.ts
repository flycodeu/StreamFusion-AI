import { describe, expect, it } from 'vitest'
import type { StatusChannel, StatusProfile } from '../../api/camera-status/types'
import {
  channelConnection,
  checkAvailability,
  representativeProfile,
  findCameraStatusPath,
  representativeConnection,
  summarizeChannels,
} from './health'
import type { MenuRoute } from '../../api/auth/types'

const profile: StatusProfile = {
  streamProfileId: '2',
  name: '主码流',
  streamKind: 'MAIN',
  enabled: true,
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
    observedAt: '2026-10-13T01:00:00Z',
    readAt: '2026-10-13T01:00:00Z',
    validUntil: '2026-10-13T01:05:00Z',
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
      observation: { ...profile.observation!, value: 'FAILED' as const },
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
  it('counts exclusive categories for only the loaded page and downgrades expired facts', () => {
    const rows: StatusChannel[] = [
      channel,
      {
        ...channel,
        profiles: [{ ...profile, observation: { ...profile.observation!, value: 'FAILED' } }],
      },
      {
        ...channel,
        profiles: [
          {
            ...profile,
            latestCheck: { executionState: 'RUNNING' } as StatusProfile['latestCheck'],
          },
        ],
      },
      { ...channel, profiles: [{ ...profile, freshness: 'STALE' }] },
      { ...channel, profiles: [{ ...profile, configurationCurrent: false }] },
      { ...channel, profiles: [] },
      { ...channel, lifecycle: 'DISABLED' },
      { ...channel, lifecycle: 'PENDING_ASSIGNMENT' },
    ]
    expect(summarizeChannels(rows)).toEqual({
      total: 8,
      accessible: 1,
      failed: 1,
      checking: 1,
      stale: 2,
      unknown: 1,
      disabled: 2,
    })
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
  it('finds the authorized status route without assuming a fixed menu path', () => {
    const route: MenuRoute = {
      id: '1',
      name: '状态',
      type: 'PAGE',
      icon: null,
      visible: true,
      routeName: 'status',
      path: '/custom-status',
      componentKey: '/camera/Status',
      moduleKey: 'camera',
      children: [],
    }
    expect(
      findCameraStatusPath([
        { ...route, type: 'DIRECTORY', componentKey: null, children: [route] },
      ]),
    ).toBe('/custom-status')
    expect(findCameraStatusPath([{ ...route, moduleKey: 'other' }])).toBeNull()
    expect(findCameraStatusPath([])).toBeNull()
  })
  it('summarizes control accessibility without claiming camera online or media health', () => {
    expect(channelConnection(channel)).toEqual({ label: '控制连接可达', tone: 'success' })
  })
  it.each([
    { ...profile, freshness: 'STALE' as const },
    { ...profile, configurationCurrent: false },
    { ...profile, enabled: false },
  ])('does not count expired, mismatched or disabled streams as current accessibility', (fact) => {
    expect(channelConnection({ ...channel, profiles: [fact] })).toEqual({
      label: '尚无有效结果',
      tone: 'info',
    })
  })
  it('keeps partial stream failure visible', () => {
    const failed = {
      ...profile,
      streamProfileId: '4',
      observation: { ...profile.observation!, value: 'FAILED' as const },
    }
    expect(channelConnection({ ...channel, profiles: [profile, failed] }).label).toBe(
      '部分连接异常',
    )
  })
  it('does not infer platform origin from unsupported protocol', () => {
    expect(checkAvailability('SOURCE_PROTOCOL_NOT_SUPPORTED')).toBe('检测待适配')
    expect(checkAvailability('PROFILE_DISABLED')).toBe('')
  })
})
