import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import {
  createPreviewLease,
  deletePreviewLease,
  exchangeWebRtc,
  getPreviewCapacity,
  renewPreviewLease,
} from './api'
import { clearIdentity, sessionState } from '../../session/state'

const input = {
  clientRequestId: 'baeea598-5662-453c-9ccc-19aa8ad80baa',
  cameraId: '9007199254740993',
  streamProfileId: '2',
  usage: 'MAIN' as const,
  videoCodecs: ['H264'],
}
const lease = {
  leaseId: 'lease-1',
  cameraId: input.cameraId,
  streamProfileId: '2',
  state: 'READY',
  reasonCode: null,
  expiresAt: '2030-01-01T00:00:00Z',
  videoCodec: 'H264',
  transcoded: false,
}
function response(data: unknown, status = 201, code = 'SUCCESS') {
  return new Response(
    JSON.stringify({
      code,
      msg: '请求结果',
      data,
      traceId: 'a'.repeat(32),
      timestamp: '2026-10-15T01:00:00Z',
    }),
    { status, headers: { 'Content-Type': 'application/json' } },
  )
}
beforeEach(() => {
  vi.stubEnv('DEV', true)
  clearIdentity()
  sessionState.csrf = { headerName: 'X-CSRF-TOKEN', token: 'csrf' }
})
afterEach(() => {
  clearIdentity()
  vi.unstubAllEnvs()
  vi.unstubAllGlobals()
  vi.useRealTimers()
})
describe('preview creation receipt recovery', () => {
  it('retains envelope diagnostics for unavailable creation and renewal receipts without replaying them', async () => {
    const unavailable = {
      ...lease,
      leaseId: null,
      state: 'UNAVAILABLE',
      reasonCode: 'SOURCE_UNAVAILABLE',
    }
    const fetchMock = vi
      .fn()
      .mockResolvedValueOnce(response(unavailable, 201))
      .mockResolvedValueOnce(response(unavailable, 200))
    vi.stubGlobal('fetch', fetchMock)
    expect(await createPreviewLease(input)).toMatchObject({
      state: 'UNAVAILABLE',
      receipt: { status: 201, traceId: 'a'.repeat(32), timestamp: '2026-10-15T01:00:00Z' },
    })
    expect(await renewPreviewLease('lease-1')).toMatchObject({
      state: 'UNAVAILABLE',
      receipt: { status: 200, traceId: 'a'.repeat(32), timestamp: '2026-10-15T01:00:00Z' },
    })
    expect(fetchMock).toHaveBeenCalledTimes(2)
  })
  it('reads aggregate capacity with a cancellable GET and no retry on rejection', async () => {
    const controller = new AbortController()
    const fetchMock = vi.fn().mockResolvedValue(response(null, 403, 'ACCESS_DENIED'))
    vi.stubGlobal('fetch', fetchMock)
    await expect(getPreviewCapacity(controller.signal)).rejects.toMatchObject({
      code: 'ACCESS_DENIED',
    })
    expect(fetchMock).toHaveBeenCalledOnce()
    expect(fetchMock.mock.calls[0]![0]).toBe('/api/preview/capacity')
    expect(fetchMock.mock.calls[0]![1].method).toBe('GET')
  })
  it('preserves an explicit idempotency key across one lost receipt', async () => {
    const fetchMock = vi
      .fn()
      .mockRejectedValueOnce(new TypeError('network'))
      .mockResolvedValueOnce(response(lease))
    vi.stubGlobal('fetch', fetchMock)
    expect((await createPreviewLease(input)).leaseId).toBe('lease-1')
    expect(fetchMock).toHaveBeenCalledTimes(2)
    for (const [path, request] of fetchMock.mock.calls) {
      expect(path).toBe('/api/preview/leases')
      expect(JSON.parse(request.body)).toEqual(input)
      expect(request.headers.get('X-CSRF-TOKEN')).toBe('csrf')
    }
  })
  it('recovers a malformed success receipt once with the same payload', async () => {
    const fetchMock = vi
      .fn()
      .mockResolvedValueOnce(response({ ...lease, state: 'INVALID' }))
      .mockResolvedValueOnce(response(lease))
    vi.stubGlobal('fetch', fetchMock)
    await createPreviewLease(input)
    expect(fetchMock).toHaveBeenCalledTimes(2)
    expect(fetchMock.mock.calls[0]![1].body).toBe(fetchMock.mock.calls[1]![1].body)
  })
  it('does not keep retrying an unreachable endpoint', async () => {
    const fetchMock = vi.fn().mockRejectedValue(new TypeError('network'))
    vi.stubGlobal('fetch', fetchMock)
    await expect(createPreviewLease(input)).rejects.toMatchObject({ code: 'NETWORK_ERROR' })
    expect(fetchMock).toHaveBeenCalledTimes(2)
  })
  it.each([
    [401, 'LOGIN_FAILED'],
    [403, 'ACCESS_DENIED'],
    [400, 'VALIDATION_ERROR'],
    [409, 'REQUEST_CONFLICT'],
    [429, 'RATE_LIMITED'],
    [503, 'SERVICE_UNAVAILABLE'],
  ])('does not replay explicit rejection %s/%s', async (status, code) => {
    const fetchMock = vi.fn().mockResolvedValue(response(null, status, code))
    vi.stubGlobal('fetch', fetchMock)
    await expect(createPreviewLease(input)).rejects.toMatchObject({ code, status })
    expect(fetchMock).toHaveBeenCalledOnce()
  })
  it('waits beyond the backend deadline and recovers a timed-out receipt only once', async () => {
    vi.useFakeTimers()
    const fetchMock = vi
      .fn()
      .mockImplementationOnce(
        (_path, request) =>
          new Promise((_resolve, reject) => {
            request.signal.addEventListener('abort', () => reject(new Error('aborted')))
          }),
      )
      .mockResolvedValueOnce(response(lease))
    vi.stubGlobal('fetch', fetchMock)
    const pending = createPreviewLease(input)
    await vi.advanceTimersByTimeAsync(24999)
    expect(fetchMock).toHaveBeenCalledOnce()
    await vi.advanceTimersByTimeAsync(1)
    await pending
    expect(fetchMock).toHaveBeenCalledTimes(2)
    expect(fetchMock.mock.calls[0]![1].body).toBe(fetchMock.mock.calls[1]![1].body)
  })
  it('does not retry signaling, which is not an idempotent creation operation', async () => {
    const fetchMock = vi.fn().mockRejectedValue(new TypeError('network'))
    vi.stubGlobal('fetch', fetchMock)
    await expect(exchangeWebRtc('lease-1', 'offer')).rejects.toMatchObject({
      code: 'NETWORK_ERROR',
    })
    expect(fetchMock).toHaveBeenCalledOnce()
  })
})

describe('preview deletion receipt recovery', () => {
  it('confirms the same lease after a lost response and cleanup still pending', async () => {
    vi.useFakeTimers()
    const fetchMock = vi
      .fn()
      .mockRejectedValueOnce(new TypeError('network'))
      .mockResolvedValueOnce(response(null, 503, 'DEPENDENCY_UNAVAILABLE'))
      .mockResolvedValueOnce(response(null, 503, 'DEPENDENCY_UNAVAILABLE'))
      .mockResolvedValueOnce(response(null, 200))
    vi.stubGlobal('fetch', fetchMock)
    const closing = deletePreviewLease('lease-1')
    await vi.advanceTimersByTimeAsync(11000)
    await expect(closing).resolves.toBeUndefined()
    expect(fetchMock).toHaveBeenCalledTimes(4)
    for (const [path, request] of fetchMock.mock.calls) {
      expect(path).toBe('/api/preview/leases/lease-1')
      expect(request.method).toBe('DELETE')
    }
  })

  it('keeps cleanup unconfirmed after bounded retries and does not replay authorization failures', async () => {
    vi.useFakeTimers()
    const fetchMock = vi.fn().mockResolvedValue(response(null, 503, 'DEPENDENCY_UNAVAILABLE'))
    vi.stubGlobal('fetch', fetchMock)
    const closing = deletePreviewLease('lease-1')
    const rejected = expect(closing).rejects.toMatchObject({ status: 503 })
    await vi.advanceTimersByTimeAsync(11000)
    await rejected
    expect(fetchMock).toHaveBeenCalledTimes(4)

    fetchMock.mockClear().mockResolvedValue(response(null, 403, 'ACCESS_DENIED'))
    await expect(deletePreviewLease('lease-1')).rejects.toMatchObject({ status: 403 })
    expect(fetchMock).toHaveBeenCalledOnce()
  })
})
