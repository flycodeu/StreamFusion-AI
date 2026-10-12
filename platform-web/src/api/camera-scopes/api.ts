import { request } from '../client'
import { id, page, list } from '../parse'
import { parseGroup } from '../camera-groups/types'
import { parseCameraScope, parseScopeCamera, parseScopeUser } from './types'
export async function getScopeUsers(query: { page: number; size: number; name?: string }) {
  return (
    await request({
      path: '/camera-scopes/user-options',
      method: 'GET',
      params: query,
      successStatus: 200,
      decode: (v) => page(v, parseScopeUser),
    })
  ).data
}
export async function getScopeGroupTree() {
  return (
    await request({
      path: '/camera-scopes/group-options/tree',
      method: 'GET',
      successStatus: 200,
      decode: (v) => list(v, parseGroup),
    })
  ).data
}
export async function getScopeCameras(query: {
  page: number
  size: number
  name?: string
  groupId?: string
}) {
  return (
    await request({
      path: '/camera-scopes/camera-options/page',
      method: 'GET',
      params: query,
      successStatus: 200,
      decode: (v) => page(v, parseScopeCamera),
    })
  ).data
}
export async function getCameraScope(userId: string) {
  return (
    await request({
      path: `/camera-scopes/users/${id(userId)}`,
      method: 'GET',
      successStatus: 200,
      decode: parseCameraScope,
    })
  ).data
}
export async function updateCameraScope(
  userId: string,
  input: { version: string; groupIds: string[]; cameraIds: string[] },
) {
  return (
    await request({
      path: `/camera-scopes/users/${id(userId)}`,
      method: 'PUT',
      body: input,
      successStatus: 200,
      decode: parseCameraScope,
    })
  ).data
}
