import { describe, expect, it } from 'vitest'
import {
  eligibilityText,
  parsePage,
  parsePageRole,
  parsePageOverview,
  parsePageUser,
  parseResult,
  parseRoleOverview,
  parseRolePage,
  parseUser,
  parseUserPage,
  reasonText,
} from './types'

const user = {
  id: '9007199254740993',
  username: 'Reader01',
  nickname: null,
  departments: [{ id: '2002', name: '研发部门' }],
  status: 1,
  mustChangePassword: false,
  loginRestricted: false,
  accountUsable: true,
}
const role = { id: '8', code: 'READER', name: '观察员', status: 'ENABLED' }
const menu = {
  id: '1101',
  name: '相机管理',
  path: '/camera/manage',
  menuPath: '视频管理 / 相机管理',
  moduleKey: 'camera',
  enabled: true,
  chainEnabled: true,
  navigationVisible: false,
  moduleRegistered: true,
  blockers: [],
}
const access = {
  user,
  grantEffective: true,
  accessEligible: true,
  moduleAccess: true,
  sourceCount: 1,
  sourcePreview: [role],
  blockers: [],
}
const result = {
  items: [access],
  page: 1,
  size: 20,
  total: 1,
  checkedAt: '2026-10-18T08:00:00Z',
  subjectUser: user,
  subjectPage: menu,
  subjectAccess: access,
}

describe('read-only permission query contract', () => {
  it('keeps string identities and separates hidden navigation from effective access', () => {
    expect(parseUser(user).id).toBe('9007199254740993')
    expect(parseUser(user).departments).toEqual([{ id: '2002', name: '研发部门' }])
    const row = parseUserPage({ ...access, menu, accountUsable: true })
    expect(row.menu.navigationVisible).toBe(false)
    expect(row.accessEligible).toBe(true)
  })
  it('parses the checked timestamp, selected identity and same-request explanation', () => {
    const parsed = parseResult(result, parsePageUser)
    expect(parsed.subjectAccess?.sourceCount).toBe(1)
    expect(parsed.subjectUser?.id).toBe(user.id)
    expect(parsed.subjectPage?.moduleKey).toBe('camera')
    expect(parsed.checkedAt).toBe('2026-10-18T08:00:00Z')
  })
  it('keeps disabled role sources rather than silently making them enabled', () => {
    const parsed = parsePageRole({
      role: { ...role, status: 'DISABLED' },
      grantEffective: false,
      userCount: 2,
      blockers: [{ code: 'ROLE_DISABLED', objectId: '8', name: '观察员' }],
    })
    expect(parsed.grantEffective).toBe(false)
    expect(reasonText(parsed.blockers[0]!)).toBe('角色已停用：观察员')
  })
  it('validates role and page overview counts and keeps page reasons', () => {
    expect(
      parseRoleOverview({
        role,
        assignedUserCount: 3,
        usableUserCount: 2,
        assignedPageCount: 4,
        effectivePageCount: 3,
      }).usableUserCount,
    ).toBe(2)
    expect(
      parsePageOverview({
        menu,
        assignedRoleCount: 2,
        activeRoleCount: 1,
        assignedUserCount: 1,
        eligibleUserCount: 1,
      }).assignedUserCount,
    ).toBe(1)
    expect(
      parseRolePage({
        menu,
        grantEffective: false,
        blockers: [{ code: 'ROLE_DISABLED', objectId: '8', name: '观察员' }],
      }).grantEffective,
    ).toBe(false)
    expect(() =>
      parseRoleOverview({
        role,
        assignedUserCount: 1,
        usableUserCount: 2,
        assignedPageCount: 1,
        effectivePageCount: 1,
      }),
    ).toThrow()
    expect(() =>
      parsePageOverview({
        menu,
        assignedRoleCount: 1,
        activeRoleCount: 1,
        assignedUserCount: 1,
        eligibleUserCount: 2,
      }),
    ).toThrow()
  })
  it('does not call shared-module access a page grant', () => {
    const parsed = parsePageUser({
      ...access,
      sourceCount: 0,
      sourcePreview: [],
      grantEffective: false,
      accessEligible: false,
      blockers: [{ code: 'NO_PAGE_GRANT', objectId: '1101', name: '相机管理' }],
    })
    expect(parsed.moduleAccess).toBe(true)
    expect(eligibilityText(parsed.grantEffective, parsed.accessEligible, parsed.sourceCount)).toBe(
      '未分配此页面',
    )
    expect(reasonText(parsed.blockers[0]!)).toBe('未分配此页面：相机管理')
  })
  it('distinguishes account restrictions and ineffective assignments', () => {
    expect(eligibilityText(true, true)).toBe('有效')
    expect(eligibilityText(true, false)).toBe('账号受限')
    expect(eligibilityText(false, false)).toBe('已分配但未生效')
    expect(parseUser({ ...user, loginRestricted: true }).accountUsable).toBe(true)
    expect(parseUser({ ...user, departments: [] }).departments).toEqual([])
    expect(reasonText({ code: 'NEW_REASON', objectId: '1', name: '对象' })).toBe('NEW_REASON：对象')
  })
  it.each([
    { ...access, sourceCount: -1 },
    { ...access, sourceCount: 0 },
    { ...access, sourceCount: 4, sourcePreview: [role, role, role, role] },
    { ...access, sourcePreview: [{ ...role, status: 'UNKNOWN' }] },
    { ...access, grantEffective: undefined },
    { ...access, user: { ...user, status: 3 } },
  ])('rejects malformed or unbounded source summaries %#', (value) => {
    expect(() => parsePageUser(value)).toThrow()
  })
  it.each([
    { ...result, checkedAt: 'not-a-date' },
    { ...result, size: 101 },
    { ...result, total: 0 },
    { ...result, size: 1, total: 2, items: [access, access] },
  ])('rejects invalid timestamps or pagination %#', (value) => {
    expect(() => parseResult(value, parsePageUser)).toThrow()
  })
  it('rejects incomplete page diagnostic booleans', () => {
    expect(() => parsePage({ ...menu, chainEnabled: undefined })).toThrow()
  })
})
