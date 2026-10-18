import { request } from '../client'
import { id, object, string } from '../parse'
import {
  parsePage,
  parsePageOverview,
  parsePageRole,
  parsePageUser,
  parseResult,
  parseRoleOverview,
  parseRolePage,
  parseUser,
  parseUserPage,
} from './types'
import type { Query } from './types'

async function read<T>(
  path: string,
  query: Query,
  parse: (value: unknown) => T,
  signal?: AbortSignal,
) {
  return (
    await request({
      path,
      params: { ...query },
      method: 'GET',
      successStatus: 200,
      signal,
      decode: (value) => parseResult(value, parse),
    })
  ).data
}
export const getUsers = (query: Query, signal?: AbortSignal) =>
  read('/access-query/users/page', query, parseUser, signal)
export const getPages = (query: Query, signal?: AbortSignal) =>
  read('/access-query/pages/page', query, parsePage, signal)
export const getRoleOverview = (query: Query, signal?: AbortSignal) =>
  read('/access-query/roles/page', query, parseRoleOverview, signal)
export const getRolePages = (roleId: string, query: Query, signal?: AbortSignal) =>
  read(`/access-query/roles/${id(roleId)}/pages`, query, parseRolePage, signal)
export const getRoleUsers = (roleId: string, query: Query, signal?: AbortSignal) =>
  read(`/access-query/roles/${id(roleId)}/users`, query, parseUser, signal)
export const getPageOverview = (query: Query, signal?: AbortSignal) =>
  read('/access-query/pages/overview', query, parsePageOverview, signal)
export const getUserPages = (userId: string, query: Query, signal?: AbortSignal) =>
  read(`/access-query/users/${id(userId)}/pages`, query, parseUserPage, signal)
export const getPageUsers = (pageId: string, query: Query, signal?: AbortSignal) =>
  read(`/access-query/pages/${id(pageId)}/users`, query, parsePageUser, signal)
export const getPageRoles = (pageId: string, query: Query, signal?: AbortSignal) =>
  read(`/access-query/pages/${id(pageId)}/roles`, query, parsePageRole, signal)

export async function getPageExcel(keyword: string, signal?: AbortSignal) {
  return (
    await request({
      path: '/access-query/pages/export.xlsx',
      params: { keyword: keyword.trim() || undefined },
      method: 'GET',
      responseType: 'file',
      timeoutMs: 120000,
      successStatus: 200,
      signal,
      decode: (value) => {
        const row = object(value)
        if (!(row.blob instanceof Blob)) throw new Error('file')
        return { blob: row.blob, name: string(row.name) }
      },
    })
  ).data
}
