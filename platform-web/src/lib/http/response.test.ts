import { describe, expect, it } from 'vitest'
import { parseEnvelope } from './response'
import type { RawHttpResponse } from './types'

const trace = 'a'.repeat(32)
function raw(
  body: unknown,
  status = 200,
  overrides: Partial<RawHttpResponse['headers']> = {},
): RawHttpResponse {
  return {
    status,
    bodyText: JSON.stringify(body),
    requestTraceId: trace,
    receivedAt: '2026-09-16T07:00:00Z',
    headers: {
      contentType: 'application/json',
      traceId: trace,
      etag: '"7"',
      location: '/user/1',
      retryAfter: null,
      ...overrides,
    },
  }
}
const envelope = (data: unknown = null) => ({
  code: 'SUCCESS',
  msg: '操作成功',
  data,
  traceId: trace,
  timestamp: '2026-09-16T07:00:00.123456Z',
})

describe('R response parser', () => {
  it.each([null, [], { items: [], total: 0 }, 'string', { id: '9007199254740993' }])(
    'unwraps once and retains metadata: %s',
    (data) => {
      expect(parseEnvelope(raw(envelope(data)))).toEqual({
        data,
        meta: {
          status: 200,
          traceId: trace,
          timestamp: '2026-09-16T07:00:00.123456Z',
          etag: '"7"',
          location: '/user/1',
        },
      })
    },
  )
  it.each([
    {},
    { ...envelope(), code: 200 },
    { ...envelope(), data: undefined },
    { ...envelope(), timestamp: '2026-02-30T00:00:00Z' },
    { ...envelope(), timestamp: 'yesterday' },
    { ...envelope(), traceId: 'bad' },
    { ...envelope(), code: 'FORBIDDEN' },
  ])('rejects malformed or contradictory success: %s', (body) => {
    expect(() => parseEnvelope(raw(body))).toThrow(
      expect.objectContaining({ code: 'INVALID_RESPONSE' }),
    )
  })
  it('rejects error HTTP with success code, empty success, HTML and trace mismatch', () => {
    for (const response of [
      raw(envelope(), 409),
      { ...raw(envelope()), status: 204, bodyText: '' },
      { ...raw(envelope()), bodyText: '<html>secret</html>' },
      raw(envelope(), 200, { traceId: 'b'.repeat(32) }),
    ]) {
      expect(() => parseEnvelope(response)).toThrow(
        expect.objectContaining({ code: 'INVALID_RESPONSE' }),
      )
    }
  })
  it('retains unknown server errors and safe details without changing HTTP status', () => {
    expect(() =>
      parseEnvelope(
        raw({ ...envelope(), code: 'NEW_CONFLICT', msg: '发生冲突', data: { userCount: 3 } }, 409),
      ),
    ).toThrow(
      expect.objectContaining({
        code: 'NEW_CONFLICT',
        status: 409,
        data: { userCount: 3 },
        traceId: trace,
      }),
    )
  })
  it('handles non-JSON proxy errors and malformed error envelopes safely', () => {
    for (const bodyText of ['Bad gateway password=secret', '{', '']) {
      expect(() => parseEnvelope({ ...raw(null, 502), bodyText })).toThrow(
        expect.objectContaining({ code: 'HTTP_502', status: 502, message: '请求失败（HTTP 502）' }),
      )
    }
  })
  it.each([
    ['15', 15],
    ['Wed, 16 Sep 2026 07:00:20 GMT', 20],
    ['nonsense', undefined],
    ['9999999999999999999999', undefined],
  ])('parses Retry-After %s', (retryAfter, expected) => {
    expect(() =>
      parseEnvelope(
        raw({ ...envelope(), code: 'RATE_LIMITED' }, 429, { retryAfter: String(retryAfter) }),
      ),
    ).toThrow(expect.objectContaining({ retryAfter: expected }))
  })
})
