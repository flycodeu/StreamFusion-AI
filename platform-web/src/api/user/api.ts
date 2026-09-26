import { request } from '../client'
import { empty, list, page } from '../parse'
import type { Page } from '../parse'
import { parseDepartmentOption, parseUserDepartments } from '../departments/types'
import type { DepartmentOption } from '../departments/types'
import { parseRoleOption } from '../roles/types'
import type { RoleOption } from '../roles/types'
import {
  parseUserCredentialResult,
  parseUserDetail,
  parseUserRoles,
  parseUserSummary,
} from './types'
import type { UserCredentialResult, UserDetail, UserSummary } from './types'

export async function getUsers(query: {
  page: number
  size: number
  keyword?: string
  status?: number
}): Promise<Page<UserSummary>> {
  return (
    await request({
      path: '/user/page',
      method: 'GET',
      params: query,
      successStatus: 200,
      decode: (v) => page(v, parseUserSummary),
    })
  ).data
}

export async function getUser(id: string): Promise<UserDetail> {
  return (
    await request({
      path: `/user/${id}`,
      method: 'GET',
      successStatus: 200,
      decode: parseUserDetail,
    })
  ).data
}

export async function createUser(input: {
  username: string
  nickname: string | null
  phone: string | null
  email: string | null
  gender: number
  avatarKey: string | null
  departmentIds?: string[]
}): Promise<UserCredentialResult> {
  return (
    await request({
      path: '/user',
      method: 'POST',
      body: input,
      successStatus: 201,
      decode: parseUserCredentialResult,
    })
  ).data
}

export async function updateUser(
  input: UserDetail & { departmentIds?: string[] },
): Promise<UserDetail> {
  return (
    await request({
      path: `/user/${input.id}`,
      method: 'PUT',
      body: {
        nickname: input.nickname,
        avatarKey: input.avatarKey,
        phone: input.phone,
        email: input.email,
        gender: input.gender,
        departmentIds: input.departmentIds,
        version: input.version,
      },
      successStatus: 200,
      decode: parseUserDetail,
    })
  ).data
}

export async function changeUserStatus(input: UserSummary, enabled: boolean): Promise<UserDetail> {
  return (
    await request({
      path: `/user/${input.id}/${enabled ? 'enable' : 'disable'}`,
      method: 'POST',
      body: { version: input.version },
      successStatus: 200,
      decode: parseUserDetail,
    })
  ).data
}

export async function resetUserPassword(input: UserSummary): Promise<UserCredentialResult> {
  return (
    await request({
      path: `/user/${input.id}/reset-password`,
      method: 'POST',
      body: { version: input.version },
      successStatus: 200,
      decode: parseUserCredentialResult,
    })
  ).data
}

export async function deleteUser(input: UserSummary): Promise<void> {
  await request({
    path: `/user/${input.id}`,
    method: 'DELETE',
    ifMatch: `"${input.version}"`,
    successStatus: 200,
    decode: empty,
  })
}

export async function forceLogoutUser(input: UserSummary): Promise<UserDetail> {
  return (
    await request({
      path: `/user/${input.id}/force-logout`,
      method: 'POST',
      ifMatch: `"${input.version}"`,
      successStatus: 200,
      decode: parseUserDetail,
    })
  ).data
}

export async function getUserRoles(
  id: string,
): Promise<{ userId: string; version: string; roles: RoleOption[] }> {
  return (
    await request({
      path: `/user/${id}/roles`,
      method: 'GET',
      successStatus: 200,
      decode: parseUserRoles,
    })
  ).data
}

export async function setUserRoles(id: string, version: string, roleIds: string[]): Promise<void> {
  await request({
    path: `/user/${id}/roles`,
    method: 'PUT',
    body: { version, roleIds },
    successStatus: 200,
    decode: parseUserRoles,
  })
}

export async function setUserDepartments(
  id: string,
  version: string,
  departmentIds: string[],
): Promise<void> {
  await request({
    path: `/user/${id}/departments`,
    method: 'PUT',
    body: { version, departmentIds },
    successStatus: 200,
    decode: parseUserDepartments,
  })
}

export async function getRoleOptions(): Promise<RoleOption[]> {
  return (
    await request({
      path: '/user/role-options',
      method: 'GET',
      successStatus: 200,
      decode: (v) => list(v, parseRoleOption),
    })
  ).data
}

export async function getDepartmentOptions(): Promise<DepartmentOption[]> {
  return (
    await request({
      path: '/user/department-options',
      method: 'GET',
      successStatus: 200,
      decode: (v) => list(v, parseDepartmentOption),
    })
  ).data
}
