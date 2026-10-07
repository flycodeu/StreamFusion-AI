import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import {
  createCamera,
  deleteCamera,
  getCameraOptions,
  getCameras,
  updateCamera,
  updateProfile,
} from './api'
import { parseSource } from '../camera-sources/types'
import { updateSource } from '../camera-sources/api'
import { updateCameraScope } from '../camera-scopes/api'
import { clearIdentity, sessionState } from '../../session/state'

const camera = {
  cameraId: '9007199254740993',
  name: '炉前',
  sourceDisplayName: 'NVR',
  sourceType: 'RTSP',
  lifecycle: 'ENABLED',
  version: '9',
  createdAt: '2026-10-06T00:00:00Z',
  updatedAt: '2026-10-06T00:00:00Z',
}
function response(data: unknown, status = 200, code = 'SUCCESS') {
  return new Response(
    JSON.stringify({
      code,
      msg: '请求结果',
      data,
      traceId: 'a'.repeat(32),
      timestamp: '2026-10-06T00:00:00Z',
    }),
    {
      status,
      headers: { 'Content-Type': 'application/json', 'X-Trace-Id': 'a'.repeat(32), ETag: '"9"' },
    },
  )
}
beforeEach(() => {
  vi.stubEnv('DEV', true)
  clearIdentity()
  sessionState.csrf = { headerName: 'X-CSRF-TOKEN', token: 'test-csrf' }
})
afterEach(() => {
  clearIdentity()
  vi.unstubAllEnvs()
  vi.unstubAllGlobals()
})
describe('camera requests through the existing HTTP client', () => {
  it('rejects missing availability rather than enabling manual storage by default', async () => {
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(response({ canManageShared: true })))
    await expect(getCameraOptions()).rejects.toMatchObject({ code: 'INVALID_RESPONSE' })
  })
  it('rejects missing endpoint mutability rather than enabling connection edits', () => {
    expect(() =>
      parseSource({
        sourceId: '1',
        name: '设备',
        enabled: true,
        version: '0',
        createdAt: '2026-10-07T00:00:00Z',
        updatedAt: '2026-10-07T00:00:00Z',
        endpoints: [],
        credentials: [],
      }),
    ).toThrow()
  })
  it('passes encoded filters and parses server pagination', async () => {
    const fetchMock = vi
      .fn()
      .mockResolvedValue(response({ items: [camera], page: 2, size: 20, total: 25 }))
    vi.stubGlobal('fetch', fetchMock)
    expect(
      (await getCameras({ page: 2, size: 20, name: '炉 前', lifecycle: 'ENABLED' })).total,
    ).toBe(25)
    const url = new URL(fetchMock.mock.calls[0]![0], 'http://localhost')
    expect(url.pathname).toBe('/api/cameras/page')
    expect(url.searchParams.get('name')).toBe('炉 前')
  })
  it('sends only the explicit local edit with string version and CSRF', async () => {
    const fetchMock = vi.fn().mockResolvedValue(response(camera))
    vi.stubGlobal('fetch', fetchMock)
    const command = { version: '8', name: '炉前', remark: null, defaultPreviewProfileId: null }
    await updateCamera(camera.cameraId, command)
    const [path, init] = fetchMock.mock.calls[0]!
    expect(path).toBe('/api/cameras/9007199254740993')
    expect(JSON.parse(init.body)).toEqual(command)
    expect(init.headers.get('X-CSRF-TOKEN')).toBe('test-csrf')
  })
  it('uses quoted If-Match without DELETE body and preserves version conflicts', async () => {
    const fetchMock = vi.fn().mockResolvedValue(response(null, 412, 'VERSION_CONFLICT'))
    vi.stubGlobal('fetch', fetchMock)
    await expect(deleteCamera(camera)).rejects.toMatchObject({ code: 'VERSION_CONFLICT' })
    expect(fetchMock).toHaveBeenCalledTimes(1)
    expect(fetchMock.mock.calls[0]![1].headers.get('If-Match')).toBe('"9"')
    expect(fetchMock.mock.calls[0]![1].body).toBeUndefined()
  })
  it('does not replay a failed create or discard its explicit request key', async () => {
    const fetchMock = vi.fn().mockRejectedValue(new TypeError('network'))
    vi.stubGlobal('fetch', fetchMock)
    const command = {
      clientRequestId: '1791244800000-key',
      name: '炉前',
      remark: null,
      connection: { host: 'camera.example' },
    }
    await expect(createCamera(command)).rejects.toMatchObject({ code: 'NETWORK_ERROR' })
    expect(fetchMock).toHaveBeenCalledTimes(1)
    expect(JSON.parse(fetchMock.mock.calls[0]![1].body).clientRequestId).toBe(
      command.clientRequestId,
    )
  })
  it('sends intentional default replacement with Profile disable', async () => {
    const fetchMock = vi.fn().mockResolvedValue(
      response({
        streamProfileId: '2',
        label: '主',
        usageHint: 'MAIN',
        usageOrigin: 'MANUAL',
        enabled: false,
        version: '2',
        locatorSummary: { locatorKind: 'RTSP', configured: true },
      }),
    )
    vi.stubGlobal('fetch', fetchMock)
    const command = {
      version: '1',
      enabled: false,
      cameraVersion: '5',
      replacementDefaultProfileId: null,
    }
    await updateProfile('1', '2', command)
    expect(JSON.parse(fetchMock.mock.calls[0]![1].body)).toEqual(command)
  })
  it('keeps explicit empty grant arrays and does not fetch unrelated user endpoints', async () => {
    const fetchMock = vi.fn().mockResolvedValue(
      response({
        userId: '1',
        version: '2',
        mode: 'CUSTOM',
        groupIds: [],
        cameraIds: [],
        groupGrants: [],
        cameraGrants: [],
        effectiveSummary: {
          effectiveCameraCount: 0,
          enabledCameraCount: 0,
          computedAt: '2026-10-06T00:00:00Z',
        },
      }),
    )
    vi.stubGlobal('fetch', fetchMock)
    await updateCameraScope('1', { version: '1', groupIds: [], cameraIds: [] })
    expect(fetchMock.mock.calls[0]![0]).toBe('/api/camera-scopes/users/1')
    expect(JSON.parse(fetchMock.mock.calls[0]![1].body)).toEqual({
      version: '1',
      groupIds: [],
      cameraIds: [],
    })
  })
  it('does not send an old plaintext secret with KEEP', async () => {
    const fetchMock = vi.fn().mockResolvedValue(response(null, 409, 'VERSION_CONFLICT'))
    vi.stubGlobal('fetch', fetchMock)
    const input = {
      version: '0',
      name: '来源',
      remark: null,
      networkPolicyKey: 'approved',
      enabled: true,
      endpointsUpsert: [],
      credentialsUpsert: [
        {
          purpose: 'RTSP' as const,
          username: { action: 'KEEP' as const },
          password: { action: 'KEEP' as const },
        },
      ],
    }
    await expect(updateSource('1', input)).rejects.toMatchObject({ code: 'VERSION_CONFLICT' })
    expect(JSON.parse(fetchMock.mock.calls[0]![1].body)).toEqual(input)
  })
  it.each([
    { cameraId: '../sources', version: '0' },
    { cameraId: '1', version: '0"\r\nBad: true' },
  ])('rejects invalid identity before sending %j', async (input) => {
    const fetchMock = vi.fn()
    vi.stubGlobal('fetch', fetchMock)
    await expect(deleteCamera(input)).rejects.toThrow()
    expect(fetchMock).not.toHaveBeenCalled()
  })
})
