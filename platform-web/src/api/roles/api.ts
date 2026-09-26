import { request } from '../client'
import { empty, page } from '../parse'
import type { Page } from '../parse'
import { parseRole, parseRoleMenus } from './types'
import type { Role, RoleMenus } from './types'

export async function getRoles(query: {
  page: number
  size: number
  keyword?: string
  status?: string
}): Promise<Page<Role>> {
  return (
    await request({
      path: '/roles',
      method: 'GET',
      params: query,
      successStatus: 200,
      decode: (v) => page(v, parseRole),
    })
  ).data
}

export async function createRole(input: {
  code: string
  name: string
  description: string | null
}): Promise<Role> {
  return (
    await request({
      path: '/roles',
      method: 'POST',
      body: input,
      successStatus: 201,
      decode: parseRole,
    })
  ).data
}

export async function updateRole(role: Role): Promise<Role> {
  return (
    await request({
      path: `/roles/${role.id}`,
      method: 'PUT',
      body: { name: role.name, description: role.description, version: role.version },
      successStatus: 200,
      decode: parseRole,
    })
  ).data
}

export async function changeRoleStatus(role: Role, enabled: boolean): Promise<Role> {
  return (
    await request({
      path: `/roles/${role.id}/${enabled ? 'enable' : 'disable'}`,
      method: 'POST',
      body: { version: role.version },
      successStatus: 200,
      decode: parseRole,
    })
  ).data
}

export async function deleteRole(role: Role): Promise<void> {
  await request({
    path: `/roles/${role.id}`,
    method: 'DELETE',
    ifMatch: `"${role.version}"`,
    successStatus: 200,
    decode: empty,
  })
}

export async function getRoleMenus(roleId: string): Promise<RoleMenus> {
  return (
    await request({
      path: `/roles/${roleId}/menus`,
      method: 'GET',
      successStatus: 200,
      decode: parseRoleMenus,
    })
  ).data
}

export async function setRoleMenus(
  roleId: string,
  version: string,
  menuIds: string[],
): Promise<RoleMenus> {
  return (
    await request({
      path: `/roles/${roleId}/menus`,
      method: 'PUT',
      body: { version, menuIds },
      successStatus: 200,
      decode: parseRoleMenus,
    })
  ).data
}
