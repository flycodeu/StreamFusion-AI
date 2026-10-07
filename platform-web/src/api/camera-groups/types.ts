import { id, integer, object, optionalString, string, boolean } from '../parse'
export interface CameraGroup {
  groupId: string
  parentId: string | null
  name: string
  sortOrder: number
  remark: string | null
  version: string
  hasChildren: boolean
  visibleCameraCount: number
  countObservedAt: string | null
}
export interface Impact {
  confirmation: string
  expiresAt: string
  fromPath: string | null
  toPath: string | null
  targetGroupId: string | null
  targetLifecycle: 'ENABLED' | 'DISABLED' | null
  affectedCameraCount: number | null
  gainedUserCount: number
  lostUserCount: number
}
export function parseGroup(value: unknown): CameraGroup {
  const row = object(value)
  return {
    groupId: id(row.groupId),
    parentId: row.parentId == null ? null : id(row.parentId),
    name: string(row.name),
    sortOrder: integer(row.sortOrder),
    remark: optionalString(row.remark),
    version: id(row.version),
    hasChildren: boolean(row.hasChildren),
    visibleCameraCount: integer(row.visibleCameraCount),
    countObservedAt: optionalString(row.countObservedAt),
  }
}
export function parseImpact(value: unknown): Impact {
  const row = object(value),
    auth = object(row.authorizationImpact)
  const target = optionalString(row.targetLifecycle) || null
  if (target !== null && target !== 'ENABLED' && target !== 'DISABLED') throw new Error('lifecycle')
  return {
    confirmation: string(row.confirmation),
    expiresAt: string(row.expiresAt),
    fromPath: optionalString(row.fromPath),
    toPath: optionalString(row.toPath),
    targetGroupId: row.targetGroupId == null ? null : id(row.targetGroupId),
    targetLifecycle: target,
    affectedCameraCount: row.affectedCameraCount == null ? null : integer(row.affectedCameraCount),
    gainedUserCount: integer(auth.gainedUserCount),
    lostUserCount: integer(auth.lostUserCount),
  }
}
