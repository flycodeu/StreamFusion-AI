import { describe, expect, it } from 'vitest'
import { ApiRequestError } from '../../lib/http/error'
import { previewFailure } from './failure'

const traceId = 'abcdef0123456789abcdef0123456789'
const timestamp = '2026-10-17T02:00:00Z'

describe('preview diagnostic boundary', () => {
  it('preserves request and resource identity without including response data', () => {
    const failure = previewFailure(
      new ApiRequestError('SOURCE_UNAVAILABLE', '上游暂不可用', {
        traceId,
        timestamp,
        data: { password: 'do-not-display', rtsp: 'rtsp://secret' },
      }),
      {
        cameraId: '10',
        cameraName: '门口',
        streamProfileId: '20',
        profileName: '主码流',
        leaseId: 'lease-1',
      },
    )
    expect(failure).toMatchObject({
      code: 'SOURCE_UNAVAILABLE',
      message: '上游暂不可用',
      traceId,
      timestamp,
      cameraId: '10',
      streamProfileId: '20',
      leaseId: 'lease-1',
      requestCode: null,
    })
    expect(JSON.stringify(failure)).not.toContain('do-not-display')
    expect(JSON.stringify(failure)).not.toContain('rtsp://')
  })

  it('keeps cleanup classification and its underlying request trace together', () => {
    const request = new ApiRequestError('REQUEST_TIMEOUT', '请求超时', { traceId, timestamp })
    const failure = previewFailure(new Error('LEASE_RELEASE_FAILED', { cause: request }), {
      leaseId: 'lease-1',
    })
    expect(failure).toMatchObject({
      code: 'LEASE_RELEASE_FAILED',
      requestCode: 'REQUEST_TIMEOUT',
      traceId,
      timestamp,
      leaseId: 'lease-1',
    })
    expect(failure.message).toContain('释放未确认')
  })

  it('does not display raw browser exception text or invalid diagnostic metadata', () => {
    const failure = previewFailure(new Error('rtsp://user:password@camera/private'))
    expect(failure.code).toBe('PREVIEW_FAILED')
    expect(failure.message).not.toContain('password')
    const invalid = previewFailure(
      new ApiRequestError('SOURCE_UNAVAILABLE', '不可用', {
        traceId: 'not-a-trace',
        timestamp: 'invalid',
        receivedAt: 'invalid',
      }),
    )
    expect(invalid.traceId).toBeNull()
    expect(Number.isFinite(Date.parse(invalid.timestamp))).toBe(true)
  })
})
