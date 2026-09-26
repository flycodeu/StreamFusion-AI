import { request } from '../client'
import { empty, list } from '../parse'
import { parseMenuNode } from './types'
import type { MenuNode } from './types'

export interface MenuWrite {
  parentId: string | null
  name: string
  type: 'DIRECTORY' | 'PAGE'
  icon: string | null
  sortOrder: number
  visible: boolean
  enabled: boolean
  routeName: string | null
  path: string | null
  componentKey: string | null
  moduleKey: string | null
}

export async function getMenus(query: {
  name?: string
  type?: string
  enabled?: boolean
}): Promise<MenuNode[]> {
  return (
    await request({
      path: '/menus',
      method: 'GET',
      params: query,
      successStatus: 200,
      decode: (v) => list(v, parseMenuNode),
    })
  ).data
}

export async function createMenu(input: MenuWrite): Promise<MenuNode> {
  return (
    await request({
      path: '/menus',
      method: 'POST',
      body: input,
      successStatus: 201,
      decode: parseMenuNode,
    })
  ).data
}

export async function updateMenu(id: string, version: string, input: MenuWrite): Promise<MenuNode> {
  return (
    await request({
      path: `/menus/${id}`,
      method: 'PUT',
      body: { ...input, version },
      successStatus: 200,
      decode: parseMenuNode,
    })
  ).data
}

export async function deleteMenu(input: MenuNode): Promise<void> {
  await request({
    path: `/menus/${input.id}`,
    method: 'DELETE',
    ifMatch: `"${input.version}"`,
    successStatus: 200,
    decode: empty,
  })
}
