import { describe, expect, it } from 'vitest'
import { parseCameraStatus, parseStatusCheck } from './types'

const instant = '2026-10-13T01:00:00Z'
const check = {
  checkId: '9007199254740993',
  cameraId: '1',
  streamProfileId: '2',
  layer: 'CONTROL',
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
const control = {
  layer: 'CONTROL',
  probeKind: 'RTSP_DESCRIBE',
  observation: null,
  freshness: 'NEVER',
  configurationCurrent: true,
  latestCheck: check,
  canCheck: false,
  blockingReasonCode: 'FORBIDDEN',
  timeline: null,
}
const profile = {
  streamProfileId: '2',
  control,
  media: { ...control, layer: 'MEDIA', probeKind: 'ZLM_RTP_FRAMES', latestCheck: null },
}
const snapshot = { cameraId: '1', serverNow: instant, profiles: [profile] }
describe('camera status response contracts', () => {
  it('requires both layers and does not accept the previous flat CONTROL response', () => {
    expect(() =>
      parseCameraStatus({ ...snapshot, profiles: [{ streamProfileId: '2', ...control }] }),
    ).toThrow()
    expect(() =>
      parseCameraStatus({ ...snapshot, profiles: [{ ...profile, media: undefined }] }),
    ).toThrow()
  })
  it('retains complete MEDIA evidence and rejects missing, short, reset or cross-layer evidence', () => {
    const evidence = {
      windowStartedAt: '2026-10-13T00:59:55Z',
      windowEndedAt: instant,
      firstFrames: 10,
      lastFrames: 30,
      sampleCount: 3,
      maxGapMs: 2500,
    }
    const fact = {
      value: 'ACCESSIBLE',
      reasonCode: 'MEDIA_RECEIVING',
      observedAt: instant,
      readAt: instant,
      validUntil: instant,
      checkId: '5',
      mediaEvidence: evidence,
    }
    const media = { ...profile.media, observation: fact, freshness: 'CURRENT' }
    const parse = (observation: unknown) =>
      parseCameraStatus({
        ...snapshot,
        profiles: [{ ...profile, media: { ...media, observation } }],
      })
    expect(parse(fact).profiles[0]?.media.observation?.mediaEvidence).toEqual(evidence)
    for (const invalid of [
      null,
      { ...evidence, sampleCount: 2 },
      { ...evidence, windowStartedAt: '2026-10-13T00:59:56Z' },
      { ...evidence, lastFrames: 9 },
      { ...evidence, maxGapMs: -1 },
    ])
      expect(() => parse({ ...fact, mediaEvidence: invalid })).toThrow()
    expect(() => parse({ ...fact, value: 'UNSUPPORTED' })).toThrow()
    expect(() =>
      parseCameraStatus({
        ...snapshot,
        profiles: [
          { ...profile, control: { ...control, observation: fact, freshness: 'CURRENT' } },
        ],
      }),
    ).toThrow()
  })
  it('keeps sampled timeline separate and rejects future timestamps or timelines without facts', () => {
    const fact = {
      value: 'FAILED',
      reasonCode: 'RTSP_TIMEOUT',
      observedAt: instant,
      readAt: instant,
      validUntil: instant,
      checkId: '5',
      mediaEvidence: null,
    }
    const times = {
      stateSince: '2026-10-12T01:00:00Z',
      lastAccessibleAt: null,
      lastFailedAt: instant,
    }
    const entry = { ...control, freshness: 'CURRENT', observation: fact, timeline: times }
    expect(
      parseCameraStatus({ ...snapshot, profiles: [{ ...profile, control: entry }] }).profiles[0]
        ?.control.timeline,
    ).toEqual(times)
    expect(() =>
      parseCameraStatus({
        ...snapshot,
        profiles: [{ ...profile, control: { ...control, timeline: times } }],
      }),
    ).toThrow()
    expect(() =>
      parseCameraStatus({
        ...snapshot,
        profiles: [
          {
            ...profile,
            control: { ...entry, timeline: { ...times, stateSince: '2026-10-15T01:00:00Z' } },
          },
        ],
      }),
    ).toThrow()
  })
  it('preserves decimal string IDs and separates skipped execution from a missing fact', () => {
    expect(parseStatusCheck(check).checkId).toBe('9007199254740993')
    expect(parseCameraStatus(snapshot).profiles[0]?.control).toMatchObject({
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
        ? { ...control, blockingReasonCode: value }
        : { ...control, [field]: value }
    expect(() =>
      parseCameraStatus({ ...snapshot, profiles: [{ ...profile, control: entry }] }),
    ).toThrow()
  })
  it('rejects duplicate Profiles and a receipt from another camera/Profile', () => {
    expect(() => parseCameraStatus({ ...snapshot, profiles: [profile, profile] })).toThrow()
    expect(() =>
      parseCameraStatus({
        ...snapshot,
        profiles: [
          { ...profile, control: { ...control, latestCheck: { ...check, cameraId: '3' } } },
        ],
      }),
    ).toThrow()
  })
  it('does not accept CURRENT without a fact or associate a different observation check ID', () => {
    expect(() =>
      parseCameraStatus({
        ...snapshot,
        profiles: [{ ...profile, control: { ...control, freshness: 'CURRENT' } }],
      }),
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
          mediaEvidence: null,
        },
      }),
    ).toThrow()
  })
})
