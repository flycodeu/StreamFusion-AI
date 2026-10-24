import { request } from '../client'
import { boolean, id, integer, object, string } from '../parse'

export interface TargetSelection {
  groupKey: string
  cameraId: string
  streamProfileId: string
}

export interface PlanTarget {
  groupKey: string
  name: string
  reasonCode: string | null
  groupIds?: string[]
  cameraId?: string | null
  channelName?: string | null
  streamProfileId?: string | null
  profileName?: string | null
  videoSupported?: boolean
}

export type TargetScope = (
  | { scope: 'PAGE'; groupKeys: string[] }
  | { scope: 'ALL' }
  | { scope: 'PLAN'; planId: string }
  | { scope: 'GROUPS'; groupIds: string[] }
) & { targetSelections?: TargetSelection[] }

export function parsePlanTargets(value: unknown) {
  const row = object(value)
  if (!Array.isArray(row.targets) || row.targets.length > 2000)
    throw new Error('plan target bounds')
  const targets = row.targets.map((value): PlanTarget => {
    const item = object(value),
      groupKey = string(item.groupKey)
    if (!/^[dc][1-9][0-9]{0,18}$/.test(groupKey)) throw new Error('plan target key')
    let groupIds: string[] | undefined
    if (item.groupIds != null) {
      if (!Array.isArray(item.groupIds) || item.groupIds.length > 10000)
        throw new Error('target groups')
      groupIds = item.groupIds.map(id)
    }
    return {
      groupKey,
      name: string(item.name),
      reasonCode: item.reasonCode == null ? null : string(item.reasonCode),
      groupIds,
      cameraId: item.cameraId == null ? null : id(item.cameraId),
      channelName: item.channelName == null ? null : string(item.channelName),
      streamProfileId: item.streamProfileId == null ? null : id(item.streamProfileId),
      profileName: item.profileName == null ? null : string(item.profileName),
      videoSupported: item.videoSupported == null ? undefined : boolean(item.videoSupported),
    }
  })
  const limitExceeded = boolean(row.limitExceeded)
  if (
    targets.length > 2000 ||
    new Set(targets.map((item) => item.groupKey)).size !== targets.length ||
    (limitExceeded && targets.length > 0)
  )
    throw new Error('plan target bounds')
  const totalCount = row.totalCount == null ? undefined : integer(row.totalCount)
  if (totalCount != null && totalCount < 0) throw new Error('plan total count')
  return { targets, limitExceeded, ...(totalCount == null ? {} : { totalCount }) }
}

export async function inspectPlanTargets(scope: TargetScope) {
  return (
    await request({
      path: '/camera-status/plans/targets/preview',
      method: 'POST',
      body: scope,
      successStatus: 200,
      decode: parsePlanTargets,
    })
  ).data
}
