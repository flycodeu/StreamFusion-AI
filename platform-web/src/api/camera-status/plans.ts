import { request } from '../client'
import { id, integer, list, object, string } from '../parse'
import { isUtcTimestamp } from '../../lib/http/error'

export interface StatusPlan {
  planId: string
  state: 'ACTIVE' | 'STOPPED' | 'EXPIRED'
  intervalMinutes: number
  targetCount: number
  startAt: string
  endAt: string
  version: string
  blockedCount?: number
  nextAt?: string | null
  lastReasonCode?: string | null
  progress?: PlanProgress | null
  serverNow?: string
}
export interface PlanProgress {
  success: number
  failed: number
  pending: number
  skipped: number
  unknown: number
}
export interface StatusPlanInput {
  clientRequestId: string
  groupKeys: string[]
  intervalMinutes: number
  durationHours: number
}
export function parseStatusPlan(value: unknown): StatusPlan {
  const row = object(value),
    state = string(row.state)
  if (!['ACTIVE', 'STOPPED', 'EXPIRED'].includes(state)) throw new Error('plan state')
  const intervalMinutes = integer(row.intervalMinutes),
    targetCount = integer(row.targetCount)
  const startAt = string(row.startAt),
    endAt = string(row.endAt)
  const blockedCount = row.blockedCount == null ? 0 : integer(row.blockedCount)
  const nextAt = row.nextAt == null ? null : string(row.nextAt)
  const counts = row.progress == null ? null : object(row.progress)
  const progress = counts && {
    success: integer(counts.success),
    failed: integer(counts.failed),
    pending: integer(counts.pending),
    skipped: integer(counts.skipped),
    unknown: integer(counts.unknown),
  }
  if (
    progress &&
    (Object.values(progress).some((count) => count < 0) ||
      Object.values(progress).reduce((sum, count) => sum + count, 0) > targetCount)
  )
    throw new Error('plan progress bounds')
  if (row.serverNow != null && !isUtcTimestamp(row.serverNow)) throw new Error('plan server time')
  if (
    intervalMinutes < 5 ||
    intervalMinutes > 1440 ||
    targetCount < 1 ||
    targetCount > 2000 ||
    blockedCount < 0 ||
    blockedCount > targetCount ||
    (nextAt !== null && !Number.isFinite(Date.parse(nextAt))) ||
    !Number.isFinite(Date.parse(startAt)) ||
    !Number.isFinite(Date.parse(endAt)) ||
    Date.parse(endAt) <= Date.parse(startAt)
  )
    throw new Error('plan bounds')
  return {
    planId: id(row.planId),
    state: state as StatusPlan['state'],
    intervalMinutes,
    targetCount,
    startAt,
    endAt,
    version: id(row.version),
    blockedCount,
    nextAt,
    lastReasonCode: row.lastReasonCode == null ? null : string(row.lastReasonCode),
    ...(row.progress === undefined ? {} : { progress }),
    ...(row.serverNow == null ? {} : { serverNow: string(row.serverNow) }),
  }
}
export async function getStatusPlans() {
  return (
    await request({
      path: '/camera-status/plans',
      method: 'GET',
      successStatus: 200,
      decode: (value) => list(value, parseStatusPlan),
    })
  ).data
}
export async function createStatusPlan(input: StatusPlanInput) {
  return (
    await request({
      path: '/camera-status/plans',
      method: 'POST',
      successStatus: 201,
      body: input,
      decode: parseStatusPlan,
    })
  ).data
}
export async function stopStatusPlan(plan: StatusPlan) {
  return (
    await request({
      path: `/camera-status/plans/${id(plan.planId)}/stop`,
      method: 'POST',
      successStatus: 200,
      body: { version: id(plan.version) },
      decode: parseStatusPlan,
    })
  ).data
}
