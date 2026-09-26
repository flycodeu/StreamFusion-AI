import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import {
  dismissSessionEnded,
  endedNotice,
  parseEndedNotice,
  rememberSessionEnded,
  restoreSessionEnded,
} from './endedNotice'

let stored: Map<string, string>
beforeEach(() => {
  stored = new Map()
  vi.stubGlobal('sessionStorage', {
    getItem: (key: string) => stored.get(key) ?? null,
    setItem: (key: string, value: string) => stored.set(key, value),
    removeItem: (key: string) => stored.delete(key),
  })
  endedNotice.value = null
})
afterEach(() => {
  vi.unstubAllGlobals()
  vi.useRealTimers()
})

describe('session end notices', () => {
  it('retains only safe fields and tolerates malformed details', () => {
    const notice = parseEndedNotice('SESSION_REPLACED', {
      sourceIp: '127.0.0.1\n',
      browser: 'x'.repeat(500),
      token: 'secret',
      loginAt: 'invalid',
    })
    expect(notice?.sourceIp).toBe('127.0.0.1')
    expect(notice?.browser).toHaveLength(120)
    expect(notice?.loginAt).toBeUndefined()
    expect(notice).not.toHaveProperty('token')
    expect(parseEndedNotice('UNAUTHORIZED', null)).toBeNull()
    expect(parseEndedNotice('SESSION_FORCED_LOGOUT', { sourceIp: 'hidden' })).not.toHaveProperty(
      'sourceIp',
    )
    expect(parseEndedNotice('SESSION_REPLACED', null)?.reason).toBe('REPLACED')
  })
  it('survives the login redirect once and expires after five minutes', () => {
    vi.useFakeTimers()
    rememberSessionEnded('SESSION_REPLACED', { region: '本机' })
    endedNotice.value = null
    restoreSessionEnded()
    expect(endedNotice.value).toMatchObject({ region: '本机' })
    expect(stored.size).toBe(0)
    dismissSessionEnded()
    restoreSessionEnded()
    expect(endedNotice.value).toBeNull()
    rememberSessionEnded('SESSION_FORCED_LOGOUT', null)
    endedNotice.value = null
    vi.advanceTimersByTime(300001)
    restoreSessionEnded()
    expect(endedNotice.value).toBeNull()
  })
  it('keeps login usable when storage is blocked', () => {
    vi.stubGlobal('sessionStorage', {
      setItem: () => {
        throw new Error('blocked')
      },
      getItem: () => {
        throw new Error('blocked')
      },
      removeItem: () => {
        throw new Error('blocked')
      },
    })
    expect(() => rememberSessionEnded('SESSION_REPLACED', null)).not.toThrow()
    expect(endedNotice.value?.reason).toBe('REPLACED')
    expect(restoreSessionEnded).not.toThrow()
    expect(dismissSessionEnded).not.toThrow()
  })
})
