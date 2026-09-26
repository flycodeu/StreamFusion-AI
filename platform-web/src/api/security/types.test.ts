import { describe, expect, it } from 'vitest'
import { parseIpBlock } from './types'

const blocked = {
  id: '9007199254740993',
  sourceIp: '2001:db8::2',
  status: 'BLOCKED',
  reasonCode: 'LOGIN_FAILURE_THRESHOLD',
  failedAttempts: 20,
  windowSeconds: 600,
  blockedAt: '2026-09-26T00:00:00Z',
  unblockedAt: null,
  unblockedBy: null,
  unblockedByReference: null,
  version: '9007199254740994',
}

describe('IP block response contract', () => {
  it('preserves IPv6, large string IDs and versions, and nullable release fields', () => {
    expect(parseIpBlock(blocked)).toEqual(blocked)
  })

  it('accepts released records and preserves future reason codes', () => {
    const released = {
      ...blocked,
      status: 'RELEASED',
      reasonCode: 'FUTURE_REASON',
      unblockedAt: '2026-09-26T00:15:00.123456Z',
      unblockedBy: '9007199254740995',
    }
    expect(parseIpBlock(released)).toEqual(released)
  })

  it('accepts older responses without the optional user summary', () => {
    const legacy = { ...blocked, unblockedBy: '123', unblockedByReference: undefined }
    expect(parseIpBlock(legacy)).toMatchObject({
      unblockedBy: '123',
      unblockedByReference: null,
    })
  })

  it.each(['SNAPSHOT', 'CURRENT', 'MISSING'])(
    'preserves a readable release user with source %s',
    (source) => {
      const reference = { id: '123', type: 'USER', name: '运维', code: 'operator', source }
      expect(
        parseIpBlock({ ...blocked, unblockedBy: '123', unblockedByReference: reference }),
      ).toMatchObject({ unblockedBy: '123', unblockedByReference: reference })
    },
  )

  it.each([
    { id: '124', type: 'USER' },
    { id: '123', type: 'ROLE' },
  ])('rejects a mismatched release user summary: %j', (invalid) => {
    expect(() =>
      parseIpBlock({
        ...blocked,
        unblockedBy: '123',
        unblockedByReference: { ...invalid, name: '运维', code: null, source: 'CURRENT' },
      }),
    ).toThrow()
  })

  it.each([
    { id: 9007199254740992 },
    { version: 1 },
    { unblockedBy: 12 },
    { status: 'DISABLED' },
    { failedAttempts: -1 },
    { failedAttempts: 1.5 },
    { windowSeconds: 0 },
    { blockedAt: '2026-09-26 08:00:00' },
    { unblockedAt: '2026-02-30T00:00:00Z' },
    { sourceIp: '' },
  ])('rejects malformed security data: %j', (invalid) => {
    expect(() => parseIpBlock({ ...blocked, ...invalid })).toThrow()
  })
})
