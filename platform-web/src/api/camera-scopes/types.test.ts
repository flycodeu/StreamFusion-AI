import { describe, expect, it } from 'vitest'
import { parseScopeUser } from './types'

const user = {
  userId: '9007199254740993',
  username: 'viewer',
  nickname: null,
  enabled: true,
  isSuperAdmin: false,
}
describe('scope account departments', () => {
  it('retains several department identities and names', () => {
    const departments = [
      { id: '9007199254740995', name: '研发部门' },
      { id: '9007199254740997', name: '运维部门' },
    ]
    expect(parseScopeUser({ ...user, departments }).departments).toEqual(departments)
  })
  it('does not invent department memberships for empty or older responses', () => {
    expect(parseScopeUser({ ...user, departments: [] }).departments).toEqual([])
    expect(parseScopeUser(user).departments).toEqual([])
  })
  it('rejects malformed department identities', () => {
    expect(() =>
      parseScopeUser({ ...user, departments: [{ id: 2002, name: '研发部门' }] }),
    ).toThrow()
  })
})
