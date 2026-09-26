import { id, integer, object, optionalString, string } from '../parse'
import { isUtcTimestamp } from '../../lib/http/error'

export type IpBlockStatus = 'BLOCKED' | 'RELEASED'

export interface IpBlock {
  id: string
  sourceIp: string
  status: IpBlockStatus
  reasonCode: string
  failedAttempts: number
  windowSeconds: number
  blockedAt: string
  unblockedAt: string | null
  unblockedBy: string | null
  version: string
}

function timestamp(value: unknown): string {
  const result = string(value)
  if (!isUtcTimestamp(result)) throw new Error('timestamp')
  return result
}

export function parseIpBlock(value: unknown): IpBlock {
  const row = object(value)
  const status = string(row.status)
  if (status !== 'BLOCKED' && status !== 'RELEASED') throw new Error('status')
  const failedAttempts = integer(row.failedAttempts)
  const windowSeconds = integer(row.windowSeconds)
  if (failedAttempts < 0 || windowSeconds < 1) throw new Error('failure window')
  const sourceIp = string(row.sourceIp)
  if (!sourceIp || sourceIp.length > 64) throw new Error('sourceIp')
  const unblockedAt = optionalString(row.unblockedAt)
  return {
    id: id(row.id),
    sourceIp,
    status,
    reasonCode: string(row.reasonCode),
    failedAttempts,
    windowSeconds,
    blockedAt: timestamp(row.blockedAt),
    unblockedAt: unblockedAt === null ? null : timestamp(unblockedAt),
    unblockedBy: row.unblockedBy == null ? null : id(row.unblockedBy),
    version: id(row.version),
  }
}
