import { describe, expect, it } from 'vitest'
import { parseUserSummary } from './types'

const user = {
  id: '9007199254740993',
  username: 'operator',
  nickname: null,
  avatarKey: null,
  status: 1,
  lockedUntil: null,
  loginRestricted: false,
  departments: [],
  roles: [],
  version: '9007199254740994',
}

describe('user summary response contract', () => {
  it.each([false, true])('preserves login restriction %s and string IDs', (loginRestricted) => {
    const response = { ...user, loginRestricted }
    expect(parseUserSummary(response)).toEqual(response)
  })

  it.each([undefined, null, 'false'])(
    'does not treat a missing or invalid login restriction as unlocked: %s',
    (loginRestricted) => {
      expect(() => parseUserSummary({ ...user, loginRestricted })).toThrow('boolean')
    },
  )
})
