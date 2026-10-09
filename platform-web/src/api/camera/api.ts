import { request } from '../client'
import { boolean, empty, id, object, page } from '../parse'
import { parseCamera, parseProfile, parseDeviceGroup } from './types'
import type {
  Camera,
  ManualCameraCreate,
  CameraUpdate,
  InitialProfile,
  Lifecycle,
  ProfileUpdate,
} from './types'

export async function getCameraOptions(): Promise<{
  canManageShared: boolean
  manualStorageReady: boolean
}> {
  return (
    await request({
      path: '/cameras/options',
      method: 'GET',
      successStatus: 200,
      decode: (v) => ({
        canManageShared: boolean(object(v).canManageShared),
        manualStorageReady: boolean(object(v).manualStorageReady),
      }),
    })
  ).data
}
export async function getCameras(query: {
  page: number
  size: number
  name?: string
  lifecycle?: Lifecycle
  groupId?: string
  includeDescendants?: boolean
  sourceId?: string
}) {
  return (
    await request({
      path: '/cameras/page',
      method: 'GET',
      params: query,
      successStatus: 200,
      decode: (v) => page(v, parseCamera),
    })
  ).data
}
export async function getCamera(cameraId: string): Promise<Camera> {
  return (
    await request({
      path: `/cameras/${id(cameraId)}`,
      method: 'GET',
      successStatus: 200,
      decode: parseCamera,
    })
  ).data
}
export async function createCamera(input: ManualCameraCreate): Promise<{ cameraId: string }> {
  return (
    await request({
      path: '/cameras',
      method: 'POST',
      body: input,
      successStatus: 201,
      decode: (v) => ({ cameraId: id(object(v).cameraId) }),
    })
  ).data
}
export async function updateCamera(cameraId: string, input: CameraUpdate): Promise<Camera> {
  return (
    await request({
      path: `/cameras/${id(cameraId)}`,
      method: 'PUT',
      body: input,
      successStatus: 200,
      decode: parseCamera,
    })
  ).data
}
export async function deleteCamera(camera: Pick<Camera, 'cameraId' | 'version'>): Promise<void> {
  await request({
    path: `/cameras/${id(camera.cameraId)}`,
    method: 'DELETE',
    ifMatch: `"${id(camera.version)}"`,
    successStatus: 200,
    decode: empty,
  })
}
export async function createProfile(
  cameraId: string,
  input: Omit<InitialProfile, 'clientKey'> & { cameraVersion: string; clientRequestId: string },
) {
  return (
    await request({
      path: `/cameras/${id(cameraId)}/profiles`,
      method: 'POST',
      body: input,
      successStatus: 201,
      decode: (v) => ({ streamProfileId: id(object(v).streamProfileId) }),
    })
  ).data
}
export async function updateProfile(
  cameraId: string,
  streamProfileId: string,
  input: ProfileUpdate,
) {
  return (
    await request({
      path: `/cameras/${id(cameraId)}/profiles/${id(streamProfileId)}`,
      method: 'PUT',
      body: input,
      successStatus: 200,
      decode: parseProfile,
    })
  ).data
}
export async function deleteProfile(cameraId: string, streamProfileId: string, version: string) {
  await request({
    path: `/cameras/${id(cameraId)}/profiles/${id(streamProfileId)}`,
    method: 'DELETE',
    ifMatch: `"${id(version)}"`,
    successStatus: 200,
    decode: empty,
  })
}

export type CameraSearch = Parameters<typeof getCameras>[0]
export async function getCameraDevices(query: CameraSearch) {
  return (
    await request({
      path: '/cameras/devices/page',
      method: 'GET',
      params: query,
      successStatus: 200,
      decode: (v) => page(v, parseDeviceGroup),
    })
  ).data
}
export async function getDeviceChannels(groupKey: string, query: CameraSearch) {
  if (!/^[dc][1-9][0-9]{0,18}$/.test(groupKey)) throw new Error('groupKey')
  return (
    await request({
      path: `/cameras/devices/${groupKey}/channels`,
      method: 'GET',
      params: query,
      successStatus: 200,
      decode: (v) => page(v, parseCamera),
    })
  ).data
}
