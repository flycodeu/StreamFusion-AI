import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createStatusCheck, getCameraStatus, getStatusCheck, getStatusHistory } from './api'
import { clearIdentity, sessionState } from '../../session/state'

const instant = '2026-10-13T01:00:00Z'
const check = {
  checkId: '100',
  cameraId: '9007199254740993',
  streamProfileId: '2',
  layer: 'CONTROL',
  probeKind: 'RTSP_DESCRIBE',
  executionState: 'QUEUED',
  requestedAt: instant,
  startedAt: null,
  finishedAt: null,
  deadlineAt: instant,
  observation: null,
  reasonCode: null,
  durationMs: null,
  traceId: 'a'.repeat(32),
  snapshotApplied: false,
}
function response(data: unknown, status = 200, code = 'SUCCESS') {
  return new Response(
    JSON.stringify({ code, msg: '请求结果', data, traceId: 'a'.repeat(32), timestamp: instant }),
    {
      status,
      headers: { 'Content-Type': 'application/json', 'X-Trace-Id': 'a'.repeat(32) },
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
describe('camera status existing HTTP contract', () => {
  it('reads bounded per-profile history with explicit layer and no probe POST', async () => {
    const history = {
      items: [check],
      total: 1,
      page: 1,
      size: 20,
      accessible: 0,
      failed: 0,
      unknown: 1,
      truncated: false,
    }
    const fetchMock = vi.fn().mockResolvedValue(response(history))
    vi.stubGlobal('fetch', fetchMock)
    expect(
      await getStatusHistory(check.cameraId, {
        streamProfileId: '2',
        layer: 'CONTROL',
        days: 7,
        page: 1,
        size: 20,
      }),
    ).toEqual(expect.objectContaining(history))
    expect(fetchMock).toHaveBeenCalledOnce()
    expect(fetchMock.mock.calls[0]![0]).toContain(`/api/cameras/${check.cameraId}/status-checks?`)
    expect(fetchMock.mock.calls[0]![0]).toContain('layer=CONTROL')
    expect(fetchMock.mock.calls[0]![1].method).toBe('GET')
  })
  it('rejects cross-profile history and inconsistent aggregate totals', async () => {
    const query = { streamProfileId: '2', layer: 'CONTROL' as const, days: 7, page: 1, size: 20 }
    const history = {
      items: [{ ...check, streamProfileId: '3' }],
      total: 1,
      page: 1,
      size: 20,
      accessible: 0,
      failed: 0,
      unknown: 1,
      truncated: false,
    }
    const fetchMock = vi.fn().mockResolvedValue(response(history))
    vi.stubGlobal('fetch', fetchMock)
    await expect(getStatusHistory(check.cameraId, query)).rejects.toMatchObject({
      code: 'INVALID_RESPONSE',
    })
    fetchMock.mockResolvedValue(response({ ...history, items: [check], total: 1001 }))
    await expect(getStatusHistory(check.cameraId, query)).rejects.toMatchObject({
      code: 'INVALID_RESPONSE',
    })
  })
  it('reads Snapshot with GET/abort and no body or implicit POST', async () => {
    const fetchMock = vi
      .fn()
      .mockResolvedValue(response({ cameraId: check.cameraId, serverNow: instant, profiles: [] }))
    vi.stubGlobal('fetch', fetchMock)
    const controller = new AbortController()
    await getCameraStatus(check.cameraId, controller.signal)
    expect(fetchMock).toHaveBeenCalledOnce()
    const [path, init] = fetchMock.mock.calls[0]!
    expect(path).toBe(`/api/cameras/${check.cameraId}/status`)
    expect(init.method).toBe('GET')
    expect(init.body).toBeUndefined()
    expect(init.signal).toBeInstanceOf(AbortSignal)
  })
  it('posts exactly one explicit Profile/key through existing CSRF and expects 202', async () => {
    const fetchMock = vi.fn().mockResolvedValue(response(check, 202))
    vi.stubGlobal('fetch', fetchMock)
    const input = {
      layer: 'CONTROL' as const,
      streamProfileId: '2',
      clientRequestId: '1791853200000-key',
    }
    expect((await createStatusCheck(check.cameraId, input)).checkId).toBe('100')
    const [path, init] = fetchMock.mock.calls[0]!
    expect(path).toBe(`/api/cameras/${check.cameraId}/status-checks`)
    expect(init.method).toBe('POST')
    expect(JSON.parse(init.body)).toEqual(input)
    expect(init.headers.get('X-CSRF-TOKEN')).toBe('test-csrf')
    expect(init.headers.get('Idempotency-Key')).toBeNull()
    expect(fetchMock).toHaveBeenCalledOnce()
  })
  it('reads the exact Check path with string identifiers', async () => {
    const fetchMock = vi.fn().mockResolvedValue(response(check))
    vi.stubGlobal('fetch', fetchMock)
    await getStatusCheck(check.cameraId, check.checkId)
    expect(fetchMock.mock.calls[0]![0]).toBe(`/api/cameras/${check.cameraId}/status-checks/100`)
    expect(fetchMock.mock.calls[0]![1].method).toBe('GET')
  })
  it('does not auto-replay lost POST responses or change the explicit request key', async () => {
    const fetchMock = vi.fn().mockRejectedValue(new TypeError('network'))
    vi.stubGlobal('fetch', fetchMock)
    const input = {
      layer: 'CONTROL' as const,
      streamProfileId: '2',
      clientRequestId: '1791853200000-original',
    }
    await expect(createStatusCheck(check.cameraId, input)).rejects.toMatchObject({
      code: 'NETWORK_ERROR',
    })
    expect(fetchMock).toHaveBeenCalledOnce()
    expect(JSON.parse(fetchMock.mock.calls[0]![1].body)).toEqual(input)
  })
  it('rejects a success envelope for a different Profile or Check before applying it', async () => {
    const fetchMock = vi.fn().mockResolvedValue(response({ ...check, streamProfileId: '3' }, 202))
    vi.stubGlobal('fetch', fetchMock)
    await expect(
      createStatusCheck(check.cameraId, {
        layer: 'CONTROL',
        streamProfileId: '2',
        clientRequestId: '1791853200000-key',
      }),
    ).rejects.toMatchObject({ code: 'INVALID_RESPONSE' })
    fetchMock.mockResolvedValue(response({ ...check, checkId: '200' }))
    await expect(getStatusCheck(check.cameraId, '100')).rejects.toMatchObject({
      code: 'INVALID_RESPONSE',
    })
  })
  it('keeps 409 errors in the shared diagnostics and does not retry', async () => {
    const fetchMock = vi.fn().mockResolvedValue(response(null, 409, 'REQUEST_CONFLICT'))
    vi.stubGlobal('fetch', fetchMock)
    await expect(
      createStatusCheck(check.cameraId, {
        layer: 'CONTROL',
        streamProfileId: '2',
        clientRequestId: '1791853200000-key',
      }),
    ).rejects.toMatchObject({ code: 'REQUEST_CONFLICT', status: 409, traceId: 'a'.repeat(32) })
    expect(fetchMock).toHaveBeenCalledOnce()
  })
  it('sends MEDIA explicitly and rejects a CONTROL receipt for the same profile', async () => {
    const fetchMock = vi
      .fn()
      .mockResolvedValue(response({ ...check, layer: 'MEDIA', probeKind: 'ZLM_RTP_FRAMES' }, 202))
    vi.stubGlobal('fetch', fetchMock)
    const input = { layer: 'MEDIA' as const, streamProfileId: '2', clientRequestId: 'media-key' }
    expect((await createStatusCheck(check.cameraId, input)).layer).toBe('MEDIA')
    expect(JSON.parse(fetchMock.mock.calls[0]![1].body)).toEqual(input)
    fetchMock.mockResolvedValue(response(check, 202))
    await expect(createStatusCheck(check.cameraId, input)).rejects.toMatchObject({
      code: 'INVALID_RESPONSE',
    })
  })
})
