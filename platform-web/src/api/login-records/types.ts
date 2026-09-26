import { id, integer, object, optionalString, string } from '../parse'

export interface LoginRecord {
  id: string
  userId: string
  username: string
  nickname: string | null
  sourceIp: string | null
  region: string
  browser: string
  os: string
  loginAt: string
  lastActivityAt: string
  endedAt: string | null
  endReason: string | null
  durationSeconds: number
  status: string
}

export function parseLoginRecord(value: unknown): LoginRecord {
  const row = object(value)
  const durationSeconds = integer(row.durationSeconds)
  if (durationSeconds < 0 || !Number.isSafeInteger(durationSeconds))
    throw new Error('Invalid login duration')
  return {
    id: id(row.id),
    userId: id(row.userId),
    username: string(row.username),
    nickname: optionalString(row.nickname),
    sourceIp: optionalString(row.sourceIp),
    region: string(row.region),
    browser: string(row.browser),
    os: string(row.os),
    loginAt: string(row.loginAt),
    lastActivityAt: string(row.lastActivityAt),
    endedAt: optionalString(row.endedAt),
    endReason: optionalString(row.endReason),
    durationSeconds,
    status: string(row.status),
  }
}
