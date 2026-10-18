import { boolean, id, integer, list, object, optionalString, page, string } from '../parse'
import type { Page } from '../parse'

export interface Blocker {
  code: string
  objectId: string
  name: string
}
export interface UserIdentity {
  id: string
  username: string
  nickname: string | null
  departments: { id: string; name: string }[]
  status: number
  mustChangePassword: boolean
  loginRestricted: boolean
  accountUsable: boolean
}
export interface RoleIdentity {
  id: string
  code: string
  name: string
  status: string
}
export interface PageInfo {
  id: string
  name: string
  path: string
  menuPath: string
  moduleKey: string
  enabled: boolean
  chainEnabled: boolean
  navigationVisible: boolean
  moduleRegistered: boolean
  blockers: Blocker[]
}
export interface UserPage {
  menu: PageInfo
  grantEffective: boolean
  accountUsable: boolean
  accessEligible: boolean
  moduleAccess: boolean
  sourceCount: number
  sourcePreview: RoleIdentity[]
  blockers: Blocker[]
}
export interface PageUser {
  user: UserIdentity
  grantEffective: boolean
  accessEligible: boolean
  moduleAccess: boolean
  sourceCount: number
  sourcePreview: RoleIdentity[]
  blockers: Blocker[]
}
export interface PageRole {
  role: RoleIdentity
  grantEffective: boolean
  userCount: number
  blockers: Blocker[]
}
export interface RoleOverview {
  role: RoleIdentity
  assignedUserCount: number
  usableUserCount: number
  assignedPageCount: number
  effectivePageCount: number
}
export interface RolePage {
  menu: PageInfo
  grantEffective: boolean
  blockers: Blocker[]
}
export interface PageOverview {
  menu: PageInfo
  assignedRoleCount: number
  activeRoleCount: number
  assignedUserCount: number
  eligibleUserCount: number
}
export interface QueryResult<T> extends Page<T> {
  checkedAt: string
  subjectUser: UserIdentity | null
  subjectPage: PageInfo | null
  subjectAccess: PageUser | null
}
export interface Query {
  page?: number
  size?: number
  keyword?: string
  includeInactive?: boolean
  userId?: string
  moduleKey?: string
}
function blocker(value: unknown): Blocker {
  const row = object(value)
  return { code: string(row.code), objectId: id(row.objectId), name: string(row.name) }
}
export function parseUser(value: unknown): UserIdentity {
  const row = object(value)
  const status = integer(row.status)
  if (![0, 1, 2].includes(status)) throw new Error('user status')
  return {
    id: id(row.id),
    username: string(row.username),
    nickname: optionalString(row.nickname),
    departments: list(row.departments, (value) => {
      const department = object(value)
      return { id: id(department.id), name: string(department.name) }
    }),
    status,
    mustChangePassword: boolean(row.mustChangePassword),
    loginRestricted: boolean(row.loginRestricted),
    accountUsable: boolean(row.accountUsable),
  }
}
function role(value: unknown): RoleIdentity {
  const row = object(value),
    status = string(row.status)
  if (!['ENABLED', 'DISABLED'].includes(status)) throw new Error('role status')
  return { id: id(row.id), code: string(row.code), name: string(row.name), status }
}
export function parsePage(value: unknown): PageInfo {
  const row = object(value)
  return {
    id: id(row.id),
    name: string(row.name),
    path: string(row.path),
    menuPath: string(row.menuPath),
    moduleKey: string(row.moduleKey),
    enabled: boolean(row.enabled),
    chainEnabled: boolean(row.chainEnabled),
    navigationVisible: boolean(row.navigationVisible),
    moduleRegistered: boolean(row.moduleRegistered),
    blockers: list(row.blockers, blocker),
  }
}
function sourceFields(row: Record<string, unknown>) {
  const sourceCount = integer(row.sourceCount),
    sourcePreview = list(row.sourcePreview, role)
  if (sourceCount < 0 || sourcePreview.length > 3 || sourcePreview.length > sourceCount)
    throw new Error('source count')
  return {
    grantEffective: boolean(row.grantEffective),
    accessEligible: boolean(row.accessEligible),
    moduleAccess: boolean(row.moduleAccess),
    sourceCount,
    sourcePreview,
    blockers: list(row.blockers, blocker),
  }
}
export function parseUserPage(value: unknown): UserPage {
  const row = object(value)
  return {
    menu: parsePage(row.menu),
    accountUsable: boolean(row.accountUsable),
    ...sourceFields(row),
  }
}
export function parsePageUser(value: unknown): PageUser {
  const row = object(value)
  return { user: parseUser(row.user), ...sourceFields(row) }
}
export function parsePageRole(value: unknown): PageRole {
  const row = object(value),
    userCount = integer(row.userCount)
  if (userCount < 0) throw new Error('user count')
  return {
    role: role(row.role),
    grantEffective: boolean(row.grantEffective),
    userCount,
    blockers: list(row.blockers, blocker),
  }
}
function count(row: Record<string, unknown>, key: string): number {
  const value = integer(row[key])
  if (value < 0) throw new Error(key)
  return value
}
export function parseRoleOverview(value: unknown): RoleOverview {
  const row = object(value)
  const assignedUserCount = count(row, 'assignedUserCount')
  const usableUserCount = count(row, 'usableUserCount')
  const assignedPageCount = count(row, 'assignedPageCount')
  const effectivePageCount = count(row, 'effectivePageCount')
  if (usableUserCount > assignedUserCount || effectivePageCount > assignedPageCount)
    throw new Error('role overview counts')
  return {
    role: role(row.role),
    assignedUserCount,
    usableUserCount,
    assignedPageCount,
    effectivePageCount,
  }
}
export function parseRolePage(value: unknown): RolePage {
  const row = object(value)
  return {
    menu: parsePage(row.menu),
    grantEffective: boolean(row.grantEffective),
    blockers: list(row.blockers, blocker),
  }
}
export function parsePageOverview(value: unknown): PageOverview {
  const row = object(value)
  const assignedRoleCount = count(row, 'assignedRoleCount')
  const activeRoleCount = count(row, 'activeRoleCount')
  const assignedUserCount = count(row, 'assignedUserCount')
  const eligibleUserCount = count(row, 'eligibleUserCount')
  if (activeRoleCount > assignedRoleCount || eligibleUserCount > assignedUserCount)
    throw new Error('page overview counts')
  return {
    menu: parsePage(row.menu),
    assignedRoleCount,
    activeRoleCount,
    assignedUserCount,
    eligibleUserCount,
  }
}
export function parseResult<T>(value: unknown, parse: (value: unknown) => T): QueryResult<T> {
  const row = object(value),
    result = page(row, parse),
    checkedAt = string(row.checkedAt)
  if (
    !Number.isFinite(Date.parse(checkedAt)) ||
    result.items.length > result.size ||
    result.items.length > result.total
  )
    throw new Error('query result')
  return {
    ...result,
    checkedAt,
    subjectUser: row.subjectUser == null ? null : parseUser(row.subjectUser),
    subjectPage: row.subjectPage == null ? null : parsePage(row.subjectPage),
    subjectAccess: row.subjectAccess == null ? null : parsePageUser(row.subjectAccess),
  }
}
const reasons: Record<string, string> = {
  ROLE_DISABLED: '角色已停用',
  PAGE_DISABLED: '页面已停用',
  ANCESTOR_DISABLED: '上级目录已停用',
  INVALID_MENU_TREE: '菜单层级异常',
  MODULE_UNREGISTERED: '后端模块未注册',
  NO_ACTIVE_ROLE: '授予角色均已停用',
  USER_DISABLED: '账号已停用',
  PASSWORD_CHANGE_REQUIRED: '需先完成改密',
  NO_PAGE_GRANT: '未分配此页面',
}
export const reasonText = (reason: Blocker) =>
  `${reasons[reason.code] ?? reason.code}：${reason.name}`
export const eligibilityText = (effective: boolean, eligible: boolean, sourceCount = 1) =>
  eligible ? '有效' : sourceCount === 0 ? '未分配此页面' : effective ? '账号受限' : '已分配但未生效'
