import { describe, expect, it } from 'vitest'
import { parseCamera, parseProfile } from './types'
import { parseCameraScope } from '../camera-scopes/types'
import { parseImpact } from '../camera-groups/types'

const camera = {
  cameraId: '9007199254740993',
  name: '炉前',
  sourceDisplayName: 'NVR',
  sourceType: 'RTSP',
  lifecycle: 'PENDING_ASSIGNMENT',
  version: '9007199254740994',
  createdAt: '2026-10-06T00:00:00Z',
  updatedAt: '2026-10-06T00:00:00Z',
}
describe('camera API response boundaries', () => {
  it('keeps manual vendor hints separate from unobserved manufacturer and permits missing source type', () => {
    const result = parseCamera({
      ...camera,
      sourceType: undefined,
      connectionCategory: 'DEVICE',
      vendorHint: '手工备注',
      profiles: [],
    })
    expect(result.sourceType).toBeNull()
    expect(result.vendorHint).toBe('手工备注')
    expect(result.deviceSummary).toBeNull()
    expect(result.defaultPreviewProfileId).toBeNull()
  })
  it('accepts omitted private fields and unknown observations without fabricating values', () => {
    const result = parseCamera(camera)
    expect(result.cameraId).toBe('9007199254740993')
    expect(result.sourceId).toBeNull()
    expect(result.groupId).toBeNull()
    expect(result.deviceSummary).toBeNull()
    expect(result.profiles).toEqual([])
  })
  it.each([
    { ...camera, cameraId: Number('9007199254740993') },
    { ...camera, version: '-1' },
    { ...camera, lifecycle: 'ONLINE' },
  ])('rejects malformed identity or lifecycle %j', (value) => {
    expect(() => parseCamera(value)).toThrow()
  })
  it('keeps profile identity separate from camera and permits unknown measured parameters', () => {
    const result = parseProfile({
      streamProfileId: '200',
      label: '辅码流',
      usageHint: 'SUB',
      usageOrigin: 'MANUAL',
      enabled: true,
      version: '0',
      locatorSummary: {
        locatorKind: 'RTSP',
        configured: true,
        hostMode: 'SOURCE',
        transport: 'TCP',
      },
    })
    expect(result.streamProfileId).toBe('200')
    expect(result.frameRate).toBeNull()
    expect(result.hasAudio).toBeNull()
    expect(result.locatorSummary.host).toBeNull()
  })
  it('reads group move impacts with an empty target lifecycle', () => {
    expect(
      parseImpact({
        confirmation: 'signature',
        expiresAt: '2026-10-06T00:05:00Z',
        targetLifecycle: '',
        affectedCameraCount: 2,
        authorizationImpact: { gainedUserCount: 1, lostUserCount: 2 },
      }).targetLifecycle,
    ).toBeNull()
  })
  it('retains all 2000 direct grants, including unavailable options, and rejects a larger aggregate', () => {
    const ids = Array.from({ length: 2000 }, (_, index) => String(index + 1))
    const data = {
      userId: '1',
      version: '2',
      mode: 'CUSTOM',
      groupIds: [],
      cameraIds: ids,
      groupGrants: [],
      cameraGrants: ids.map((cameraId) => ({ cameraId, name: cameraId, lifecycle: 'DISABLED' })),
      effectiveSummary: {
        effectiveCameraCount: 2000,
        enabledCameraCount: 0,
        computedAt: '2026-10-06T00:00:00Z',
      },
    }
    expect(parseCameraScope(data).cameraIds).toHaveLength(2000)
    expect(() => parseCameraScope({ ...data, cameraIds: [...ids, '2001'] })).toThrow()
  })
})
