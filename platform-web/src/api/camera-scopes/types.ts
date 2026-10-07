import { boolean, id, integer, list, object, optionalString, string } from '../parse'
export interface ScopeUser {
  userId: string
  username: string
  nickname: string | null
  enabled: boolean
  isSuperAdmin: boolean
}
export interface ScopeGroup {
  groupId: string
  parentId: string | null
  name: string
  hasChildren: boolean
  path: string | null
}
export interface ScopeCamera {
  cameraId: string
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
  groupGrants: { groupId: string; name: string; path: string | null }[]
  cameraGrants: ScopeCamera[]
  effectiveSummary: { effectiveCameraCount: number; enabledCameraCount: number; computedAt: string }
  updatedAt: string | null
}
export function parseScopeUser(value: unknown): ScopeUser {
  const row = object(value)
  return {
    userId: id(row.userId),
    username: string(row.username),
    nickname: optionalString(row.nickname),
    enabled: boolean(row.enabled),
    isSuperAdmin: boolean(row.isSuperAdmin),
  }
}
export function parseScopeGroup(value: unknown): ScopeGroup {
  const row = object(value)
  return {
    groupId: id(row.groupId),
    parentId: row.parentId == null ? null : id(row.parentId),
    name: string(row.name),
    hasChildren: boolean(row.hasChildren),
    path: optionalString(row.path),
  }
}
export function parseScopeCamera(value: unknown): ScopeCamera {
  const row = object(value)
  return {
    cameraId: id(row.cameraId),
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
  const bounded = <T>(value: unknown, parse: (entry: unknown) => T, max: number) => {
    if (!Array.isArray(value) || value.length > max) throw new Error('scope size')
    return value.map(parse)
  }
  return {
    userId: id(row.userId),
    version: id(row.version),
    mode: row.mode,
    groupIds: bounded(row.groupIds, id, 200),
    cameraIds: bounded(row.cameraIds, id, 2000),
    groupGrants: list(row.groupGrants, (value) => {
      const grant = object(value)
      return {
        groupId: id(grant.groupId),
        name: string(grant.name),
        path: optionalString(grant.path),
      }
    }),
    cameraGrants: bounded(row.cameraGrants, parseScopeCamera, 2000),
    effectiveSummary: {
      effectiveCameraCount: integer(summary.effectiveCameraCount),
      enabledCameraCount: integer(summary.enabledCameraCount),
      computedAt: string(summary.computedAt),
    },
    updatedAt: optionalString(row.updatedAt),
  }
}
