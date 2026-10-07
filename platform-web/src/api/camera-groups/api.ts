import { request } from '../client'
import { empty, id, page } from '../parse'
import { parseGroup, parseImpact } from './types'
import type { CameraGroup } from './types'
export interface GroupQuery {
  parentId?: string
  name?: string
  page: number
  size: number
}
export interface GroupInput {
  name: string
  parentId: string | null
  sortOrder: number
  remark: string | null
}
export async function getGroups(query: GroupQuery) {
  return (
    await request({
      path: '/camera-groups',
      method: 'GET',
      params: { ...query },
      successStatus: 200,
      decode: (v) => page(v, parseGroup),
    })
  ).data
}
export async function getGroup(groupId: string) {
  return (
    await request({
      path: `/camera-groups/${id(groupId)}`,
      method: 'GET',
      successStatus: 200,
      decode: parseGroup,
    })
  ).data
}
export async function createGroup(input: GroupInput) {
  return (
    await request({
      path: '/camera-groups',
      method: 'POST',
      body: input,
      successStatus: 201,
      decode: parseGroup,
    })
  ).data
}
export async function updateGroup(
  groupId: string,
  input: GroupInput & { version: string; confirmation?: string },
) {
  return (
    await request({
      path: `/camera-groups/${id(groupId)}`,
      method: 'PUT',
      body: input,
      successStatus: 200,
      decode: parseGroup,
    })
  ).data
}
export async function previewGroupMove(
  group: Pick<CameraGroup, 'groupId' | 'version'>,
  targetParentId: string | null,
) {
  return (
    await request({
      path: `/camera-groups/${id(group.groupId)}/move-preview`,
      method: 'POST',
      body: { version: group.version, targetParentId },
      successStatus: 200,
      decode: parseImpact,
    })
  ).data
}
export async function deleteGroup(group: Pick<CameraGroup, 'groupId' | 'version'>) {
  await request({
    path: `/camera-groups/${id(group.groupId)}`,
    method: 'DELETE',
    ifMatch: `"${id(group.version)}"`,
    successStatus: 200,
    decode: empty,
  })
}
export async function previewCameraMove(
  cameraId: string,
  cameraVersion: string,
  targetGroupId: string,
) {
  return (
    await request({
      path: `/cameras/${id(cameraId)}/move-preview`,
      method: 'POST',
      body: { cameraVersion, targetGroupId },
      successStatus: 200,
      decode: parseImpact,
    })
  ).data
}
export async function previewCameraLifecycle(
  cameraId: string,
  cameraVersion: string,
  targetLifecycle: 'ENABLED' | 'DISABLED',
) {
  return (
    await request({
      path: `/cameras/${id(cameraId)}/lifecycle-preview`,
      method: 'POST',
      body: { cameraVersion, targetLifecycle },
      successStatus: 200,
      decode: parseImpact,
    })
  ).data
}
