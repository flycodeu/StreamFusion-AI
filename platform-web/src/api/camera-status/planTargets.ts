import { request } from '../client'
import { boolean, object, string } from '../parse'

export interface PlanTarget {
  groupKey: string
  name: string
  reasonCode: string | null
}

export type TargetScope =
  { scope: 'PAGE'; groupKeys: string[] } | { scope: 'ALL' } | { scope: 'PLAN'; planId: string }

export function parsePlanTargets(value: unknown) {
  const row = object(value)
  if (!Array.isArray(row.targets) || row.targets.length > 2000)
    throw new Error('plan target bounds')
  const targets = row.targets.map((value): PlanTarget => {
    const item = object(value),
      groupKey = string(item.groupKey)
    if (!/^[dc][1-9][0-9]{0,18}$/.test(groupKey)) throw new Error('plan target key')
    return {
      groupKey,
      name: string(item.name),
      reasonCode: item.reasonCode == null ? null : string(item.reasonCode),
    }
  })
  const limitExceeded = boolean(row.limitExceeded)
  if (
    targets.length > 2000 ||
    new Set(targets.map((item) => item.groupKey)).size !== targets.length ||
    (limitExceeded && targets.length > 0)
  )
    throw new Error('plan target bounds')
  return { targets, limitExceeded }
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
