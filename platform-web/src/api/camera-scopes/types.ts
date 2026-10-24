import { boolean, id, integer, list, object, optionalString, string } from '../parse'
export interface ScopeUser {
  userId: string
  username: string
  nickname: string | null
  enabled: boolean
  isSuperAdmin: boolean
  departments: { id: string; name: string }[]
}
export interface ScopeCamera {
  cameraId: string
  deviceId?: string | null
  groupIds?: string[]
  name: string
  groupId: string | null
  path: string | null
  lifecycle: string
}
export interface CameraScope {
  userId: string
  version: string
  mode: 'ALL' | 'CUSTOM'
  groupIds: string[]
  cameraIds: string[]
  deviceIds: string[]
  groupGrants: { groupId: string; name: string; path: string | null }[]
  cameraGrants: ScopeCamera[]
  deviceGrants: ScopeCamera[]
  effectiveSummary: {
    effectiveCameraCount: number
    enabledCameraCount: number
    effectiveDeviceCount: number | null
    enabledDeviceCount: number | null
    computedAt: string
  }
  updatedAt: string | null
}

function bounded<T>(value: unknown, parse: (entry: unknown) => T, max: number): T[] {
  if (!Array.isArray(value) || value.length > max) throw new Error('scope size')
  return value.map(parse)
}

export function parseScopeUser(value: unknown): ScopeUser {
  const row = object(value)
  return {
    userId: id(row.userId),
    username: string(row.username),
    nickname: optionalString(row.nickname),
    enabled: boolean(row.enabled),
    isSuperAdmin: boolean(row.isSuperAdmin),
    departments: list(row.departments ?? [], (value) => {
      const department = object(value)
      return { id: id(department.id), name: string(department.name) }
    }),
  }
}
export function parseScopeCamera(value: unknown): ScopeCamera {
  const row = object(value)
  return {
    cameraId: id(row.cameraId),
    deviceId: row.deviceId == null ? null : id(row.deviceId),
    groupIds: row.groupIds == null ? undefined : bounded(row.groupIds, id, 10000),
    name: string(row.name),
    groupId: row.groupId == null ? null : id(row.groupId),
    path: optionalString(row.path ?? row.groupPath),
    lifecycle: string(row.lifecycle),
  }
}
export function parseCameraScope(value: unknown): CameraScope {
  const row = object(value),
    summary = object(row.effectiveSummary)
  if (row.mode !== 'ALL' && row.mode !== 'CUSTOM') throw new Error('scope mode')
  // The domain explicitly permits 2,000 direct grants; the general list parser caps at 1,000.
  const cameraIds = bounded(row.cameraIds, id, 2000)
  const deviceIds = bounded(row.deviceIds ?? [], id, 2000)
  if (cameraIds.length + deviceIds.length > 2000) throw new Error('scope size')
  return {
    userId: id(row.userId),
    version: id(row.version),
    mode: row.mode,
    groupIds: bounded(row.groupIds, id, 200),
    cameraIds,
    deviceIds,
    groupGrants: list(row.groupGrants, (value) => {
      const grant = object(value)
      return {
        groupId: id(grant.groupId),
        name: string(grant.name),
        path: optionalString(grant.path),
      }
    }),
    cameraGrants: bounded(row.cameraGrants, parseScopeCamera, 2000),
    deviceGrants: bounded(
      row.deviceGrants ?? [],
      (value) => {
        const device = parseScopeCamera(value)
        if (!device.deviceId) throw new Error('device grant identity')
        return device
      },
      2000,
    ),
    effectiveSummary: {
      effectiveCameraCount: integer(summary.effectiveCameraCount),
      enabledCameraCount: integer(summary.enabledCameraCount),
      effectiveDeviceCount:
        summary.effectiveDeviceCount == null ? null : integer(summary.effectiveDeviceCount),
      enabledDeviceCount:
        summary.enabledDeviceCount == null ? null : integer(summary.enabledDeviceCount),
      computedAt: string(summary.computedAt),
    },
    updatedAt: optionalString(row.updatedAt),
  }
}
