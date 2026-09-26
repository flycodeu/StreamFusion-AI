import { id, list, object, optionalString, string } from '../parse'

export interface AuditReference {
  id: string
  type: string
  name: string | null
  code: string | null
  source: 'SNAPSHOT' | 'CURRENT' | 'MISSING'
}

export function parseAuditReference(value: unknown): AuditReference {
  const row = object(value)
  const source = string(row.source)
  if (source !== 'SNAPSHOT' && source !== 'CURRENT' && source !== 'MISSING')
    throw new Error('Invalid audit reference source')
  return {
    id: id(row.id),
    type: string(row.type),
    name: optionalString(row.name),
    code: optionalString(row.code),
    source,
  }
}

export interface AuditEntry {
  id: string
  actorId: string | null
  username: string | null
  nickname: string | null
  module: string
  targetId: string | null
  action: string
  result: string
  reasonCode: string | null
  traceId: string | null
  createdAt: string
  actor: AuditReference | null
  target: AuditReference | null
}

export interface AuditDetail {
  record: AuditEntry
  sourceIp: string | null
  changes: Record<string, string | string[] | null>
  relations: Record<string, AuditReference[]>
}

export function parseAuditEntry(value: unknown): AuditEntry {
  const row = object(value)
  return {
    id: id(row.id),
    actorId: row.actorId == null ? null : id(row.actorId),
    username: optionalString(row.username),
    nickname: optionalString(row.nickname),
    module: string(row.module),
    targetId: row.targetId == null ? null : id(row.targetId),
    action: string(row.action),
    result: string(row.result),
    reasonCode: optionalString(row.reasonCode),
    traceId: optionalString(row.traceId),
    createdAt: string(row.createdAt),
    actor: row.actor == null ? null : parseAuditReference(row.actor),
    target: row.target == null ? null : parseAuditReference(row.target),
  }
}

export function parseAuditDetail(value: unknown): AuditDetail {
  const row = object(value)
  const changes: AuditDetail['changes'] = {}
  for (const [key, item] of Object.entries(object(row.changes))) {
    if (item == null) changes[key] = null
    else if (Array.isArray(item) && item.length <= 1000) changes[key] = item.map(id)
    else changes[key] = string(item)
  }
  const relations: AuditDetail['relations'] = {}
  for (const [key, item] of Object.entries(object(row.relations ?? {}))) {
    if (!Array.isArray(item) || item.length > 1000) throw new Error('Invalid audit relations')
    relations[key] = list(item, parseAuditReference)
  }
  return {
    record: parseAuditEntry(row.record),
    sourceIp: optionalString(row.sourceIp),
    changes,
    relations,
  }
}
