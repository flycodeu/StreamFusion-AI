import { describe, expect, it } from 'vitest'
import { parseCameraStatus, parseStatusCheck } from './types'

const instant = '2026-10-13T01:00:00Z'
const check = {
  checkId: '9007199254740993',
  cameraId: '1',
  streamProfileId: '2',
  probeKind: 'RTSP_DESCRIBE',
  executionState: 'SKIPPED',
  requestedAt: instant,
  startedAt: null,
  finishedAt: instant,
  deadlineAt: instant,
  observation: null,
  reasonCode: 'WORKER_BUSY',
  durationMs: 0,
  traceId: 'a'.repeat(32),
  snapshotApplied: false,
}
const profile = {
  streamProfileId: '2',
  layer: 'CONTROL',
  probeKind: 'RTSP_DESCRIBE',
  observation: null,
  freshness: 'NEVER',
  configurationCurrent: true,
  latestCheck: check,
  canCheck: false,
  blockingReasonCode: 'FORBIDDEN',
}
const snapshot = { cameraId: '1', serverNow: instant, profiles: [profile] }
describe('camera status response contracts', () => {
  it('keeps sampled timeline separate and rejects future timestamps or timelines without facts', () => {
    const fact = {
      value: 'FAILED',
      reasonCode: 'RTSP_TIMEOUT',
      observedAt: instant,
      readAt: instant,
      validUntil: instant,
      checkId: '5',
    }
    const times = {
      stateSince: '2026-10-12T01:00:00Z',
      lastAccessibleAt: null,
      lastFailedAt: instant,
    }
    const entry = { ...profile, freshness: 'CURRENT', observation: fact, timeline: times }
    expect(parseCameraStatus({ ...snapshot, profiles: [entry] }).profiles[0]?.timeline).toEqual(
      times,
    )
    expect(() =>
      parseCameraStatus({ ...snapshot, profiles: [{ ...profile, timeline: times }] }),
    ).toThrow()
    expect(() =>
      parseCameraStatus({
        ...snapshot,
        profiles: [{ ...entry, timeline: { ...times, stateSince: '2026-10-15T01:00:00Z' } }],
      }),
    ).toThrow()
  })
  it('preserves decimal string IDs and separates skipped execution from a missing fact', () => {
    expect(parseStatusCheck(check).checkId).toBe('9007199254740993')
    expect(parseCameraStatus(snapshot).profiles[0]).toMatchObject({
      observation: null,
      freshness: 'NEVER',
      latestCheck: { executionState: 'SKIPPED' },
    })
  })
  it.each([
    ['executionState', 'FAILED'],
    ['probeKind', 'MEDIA'],
    ['traceId', 'invalid'],
    ['requestedAt', '2026-10-13 09:00:00'],
    ['durationMs', -1],
    ['checkId', 100],
  ])('rejects invalid check %s', (field, value) => {
    expect(() => parseStatusCheck({ ...check, [field]: value })).toThrow()
  })
  it.each([
    ['canCheck', undefined],
    ['configurationCurrent', undefined],
    ['layer', 'MEDIA'],
    ['freshness', 'ONLINE'],
    ['reasonCode', 'rtsp://secret'],
  ])('rejects missing or invalid eligibility/layer field %s', (field, value) => {
    const entry =
      field === 'reasonCode'
        ? { ...profile, blockingReasonCode: value }
        : { ...profile, [field]: value }
    expect(() => parseCameraStatus({ ...snapshot, profiles: [entry] })).toThrow()
  })
  it('rejects duplicate Profiles and a receipt from another camera/Profile', () => {
    expect(() => parseCameraStatus({ ...snapshot, profiles: [profile, profile] })).toThrow()
    expect(() =>
      parseCameraStatus({
        ...snapshot,
        profiles: [{ ...profile, latestCheck: { ...check, cameraId: '3' } }],
      }),
    ).toThrow()
  })
  it('does not accept CURRENT without a fact or associate a different observation check ID', () => {
    expect(() =>
      parseCameraStatus({ ...snapshot, profiles: [{ ...profile, freshness: 'CURRENT' }] }),
    ).toThrow()
    expect(() =>
      parseStatusCheck({
        ...check,
        observation: {
          value: 'ACCESSIBLE',
          reasonCode: 'RTSP_ACCESSIBLE',
          observedAt: instant,
          readAt: instant,
          validUntil: instant,
          checkId: '4',
        },
      }),
    ).toThrow()
  })
})
