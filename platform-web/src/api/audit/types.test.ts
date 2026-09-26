import { describe, expect, it } from 'vitest'
import { parseAuditDetail, parseAuditEntry } from './types'

const entry = {
  id: '9007199254740993',
  actorId: '999',
  username: null,
  nickname: null,
  module: 'DEPT',
  targetId: '2002',
  action: 'UPDATE',
  result: 'SUCCESS',
  reasonCode: null,
  traceId: null,
  createdAt: '2026-09-26T00:00:00Z',
}

describe('operation record response parsing', () => {
  it('preserves large and deleted actor IDs without numeric conversion', () => {
    expect(parseAuditEntry(entry)).toEqual({ ...entry, actor: null, target: null })
  })
  it('accepts nullable object summaries and bounded string relationship IDs', () => {
    const detail = {
      record: entry,
      sourceIp: '127.0.0.1',
      changes: { name: null, beforeDepartmentIds: ['2001'], afterDepartmentIds: ['2002'] },
    }
    expect(parseAuditDetail(detail)).toEqual({
      ...detail,
      record: { ...entry, actor: null, target: null },
      relations: {},
    })
  })
  it('rejects numeric IDs and unexpected nested summary objects', () => {
    expect(() => parseAuditEntry({ ...entry, id: 9007199254740992 })).toThrow()
    expect(() =>
      parseAuditDetail({ record: entry, changes: { name: { password: 'private' } } }),
    ).toThrow()
    expect(() =>
      parseAuditDetail({
        record: entry,
        changes: { roleIds: ['1'].concat(Array(1000).fill('2')) },
      }),
    ).toThrow()
  })
})
