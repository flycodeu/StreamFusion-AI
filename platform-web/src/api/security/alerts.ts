import { request } from '../client'
import { boolean, id, integer, list, object, page, string } from '../parse'
import { isUtcTimestamp } from '../../lib/http/error'

export const securityRuleCodes = [
  'LOGIN_FAILURE_BURST',
  'ACCESS_DENIAL_BURST',
  'PATH_SCAN',
  'CSRF_FAILURE_BURST',
  'MIXED_PROBING',
] as const
export const securityCategoryCodes = [
  'LOGIN_FAILURE',
  'ACCESS_DENIED',
  'PATH_PROBE',
  'CSRF_FAILURE',
] as const
export type SecurityRule = (typeof securityRuleCodes)[number]
export type SecurityCategory = (typeof securityCategoryCodes)[number]
export interface SecurityAlert {
  id: string
  rule: SecurityRule
  sourceIp: string
  firstSeen: string
  lastSeen: string
  windowSeconds: number
  eventCount: number
  distinctPaths: number
  categories: Partial<Record<SecurityCategory, number>>
  actorIds: string[]
  traceIds: string[]
}
export interface SecurityFeed {
  enabled: boolean
  instanceId: string
  revision: number
  retainedAlerts: number
  windowSeconds: number
  cooldownSeconds: number
  droppedEvents: number
  evictedSources: number
  truncatedWindows: number
  processingFailures: number
  persistenceHealthy: boolean
}
function uuid(value: unknown): string {
  const result = string(value)
  if (!/^[a-f0-9]{8}(-[a-f0-9]{4}){3}-[a-f0-9]{12}$/.test(result)) throw new Error('uuid')
  return result
}
function bounded(value: unknown, min: number, max = Number.MAX_SAFE_INTEGER): number {
  const result = integer(value)
  if (result < min || result > max) throw new Error('count')
  return result
}
function timestamp(value: unknown): string {
  const result = string(value)
  if (!isUtcTimestamp(result)) throw new Error('timestamp')
  return result
}
export function parseSecurityFeed(value: unknown): SecurityFeed {
  const row = object(value)
  return {
    enabled: boolean(row.enabled),
    instanceId: uuid(row.instanceId),
    revision: bounded(row.revision, 0),
    retainedAlerts: bounded(row.retainedAlerts, 0, 1000),
    windowSeconds: bounded(row.windowSeconds, 60, 1800),
    cooldownSeconds: bounded(row.cooldownSeconds, 60, 3600),
    droppedEvents: bounded(row.droppedEvents, 0),
    evictedSources: bounded(row.evictedSources, 0),
    truncatedWindows: bounded(row.truncatedWindows, 0),
    processingFailures: bounded(row.processingFailures, 0),
    persistenceHealthy: boolean(row.persistenceHealthy),
  }
}
export function parseSecurityAlert(value: unknown): SecurityAlert {
  const row = object(value)
  const rule = string(row.rule) as SecurityRule
  if (!securityRuleCodes.includes(rule)) throw new Error('rule')
  const categories: SecurityAlert['categories'] = {}
  for (const [key, count] of Object.entries(object(row.categories))) {
    if (!securityCategoryCodes.includes(key as SecurityCategory)) throw new Error('category')
    categories[key as SecurityCategory] = bounded(count, 1, 128)
  }
  const result: SecurityAlert = {
    id: uuid(row.id),
    rule,
    sourceIp: string(row.sourceIp),
    firstSeen: timestamp(row.firstSeen),
    lastSeen: timestamp(row.lastSeen),
    windowSeconds: bounded(row.windowSeconds, 60, 1800),
    eventCount: bounded(row.eventCount, 1, 128),
    distinctPaths: bounded(row.distinctPaths, 1, 128),
    categories,
    actorIds: list(row.actorIds, id),
    traceIds: list(row.traceIds, string),
  }
  if (
    !/^[0-9a-fA-F:.]{2,45}$/.test(result.sourceIp) ||
    result.actorIds.length > 8 ||
    result.traceIds.length > 5 ||
    result.traceIds.some((trace) => !/^[a-f0-9]{32}$/.test(trace)) ||
    Object.values(categories).reduce((sum, count) => sum + count, 0) !== result.eventCount ||
    result.distinctPaths > result.eventCount ||
    Date.parse(result.lastSeen) < Date.parse(result.firstSeen)
  )
    throw new Error('evidence')
  return result
}
export function securityDegraded(feed: SecurityFeed): boolean {
  return (
    !feed.persistenceHealthy ||
    feed.droppedEvents > 0 ||
    feed.evictedSources > 0 ||
    feed.truncatedWindows > 0 ||
    feed.processingFailures > 0
  )
}
export async function getSecurityFeed(signal?: AbortSignal): Promise<SecurityFeed> {
  return (
    await request({
      path: '/audit/security-alerts/feed',
      method: 'GET',
      successStatus: 200,
      decode: parseSecurityFeed,
      timeoutMs: 8000,
      signal,
    })
  ).data
}
export async function getSecurityAlerts(
  query: { page: number; size: number },
  signal?: AbortSignal,
) {
  return (
    await request({
      path: '/audit/security-alerts/page',
      method: 'GET',
      params: query,
      successStatus: 200,
      signal,
      decode: (value) => ({
        ...page(value, parseSecurityAlert),
        monitor: parseSecurityFeed(object(value).monitor),
      }),
    })
  ).data
}
