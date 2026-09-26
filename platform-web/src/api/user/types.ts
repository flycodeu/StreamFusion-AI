import { boolean, id, integer, list, object, optionalString, string } from '../parse'
import { parseRoleOption } from '../roles/types'
import type { RoleOption } from '../roles/types'

export interface UserSummary {
  id: string
  username: string
  nickname: string | null
  avatarKey: string | null
  status: number
  lockedUntil: string | null
  loginRestricted: boolean
  departments: { id: string; name: string }[]
  roles: RoleOption[]
  version: string
}

export interface UserDetail extends UserSummary {
  phone: string | null
  email: string | null
  gender: number
  mustChangePassword: boolean
}

export interface UserCredentialResult extends UserDetail {
  temporaryPassword: string
}

function departments(value: unknown): { id: string; name: string }[] {
  return list(value, (entry) => {
    const row = object(entry)
    return { id: id(row.id), name: string(row.name) }
  })
}

export function parseUserSummary(value: unknown): UserSummary {
  const row = object(value)
  return {
    id: id(row.id),
    username: string(row.username),
    nickname: optionalString(row.nickname),
    avatarKey: optionalString(row.avatarKey),
    status: integer(row.status),
    lockedUntil: optionalString(row.lockedUntil),
    loginRestricted: row.loginRestricted == null ? false : boolean(row.loginRestricted),
    departments: departments(row.departments),
    roles: list(row.roles, parseRoleOption),
    version: id(row.version),
  }
}

export function parseUserDetail(value: unknown): UserDetail {
  const row = object(value)
  return {
    ...parseUserSummary(row),
    phone: optionalString(row.phone),
    email: optionalString(row.email),
    gender: integer(row.gender),
    mustChangePassword: boolean(row.mustChangePassword),
  }
}

export function parseUserCredentialResult(value: unknown): UserCredentialResult {
  const row = object(value)
  return { ...parseUserDetail(row), temporaryPassword: string(row.temporaryPassword) }
}

export function parseUserRoles(value: unknown): {
  userId: string
  version: string
  roles: RoleOption[]
} {
  const row = object(value)
  return {
    userId: id(row.userId),
    version: id(row.version),
    roles: list(row.roles, parseRoleOption),
  }
}
