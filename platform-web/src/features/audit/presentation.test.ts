import { describe, expect, it } from 'vitest'
import { parseAuditDetail, parseAuditReference } from '../../api/audit/types'
import {
  auditChanges,
  normalizedTraceId,
  referenceName,
  referenceSource,
  traceDiagnostic,
} from './presentation'

describe('readable historical audit evidence', () => {
  it('retains the supplied historical name independently of a current name for the same ID', () => {
    const snapshot = parseAuditReference({
      id: '9007199254740993',
      type: 'ROLE',
      name: '值班运维',
      code: 'OPERATIONS',
      source: 'SNAPSHOT',
    })
    const current = { ...snapshot, name: '维护人员', source: 'CURRENT' as const }
    expect(referenceName(snapshot)).toBe('值班运维（OPERATIONS）')
    expect(referenceName(current)).toBe('维护人员（OPERATIONS）')
    expect(referenceSource(snapshot)).toBe('操作时名称')
    expect(referenceSource(current)).toBe('当前名称')
    expect(referenceName({ ...snapshot, name: null, code: null, source: 'MISSING' })).toContain(
      '已删除或未知对象（ID 9007199254740993）',
    )
  })
  it('pairs original relationship IDs with readable references without replacing evidence', () => {
    const detail = parseAuditDetail({
      record: {
        id: '1',
        actorId: null,
        username: null,
        nickname: null,
        module: 'USER',
        targetId: '2',
        action: 'USER_ROLES_UPDATE',
        result: 'SUCCESS',
        reasonCode: null,
        traceId: null,
        createdAt: '2026-09-26T00:00:00Z',
      },
      sourceIp: '127.0.0.1',
      changes: { afterRoleIds: ['3'], nickname: '张三' },
      relations: {
        afterRoleIds: [
          { id: '3', type: 'ROLE', name: '管理员', code: 'ADMIN', source: 'SNAPSHOT' },
        ],
      },
    })
    expect(auditChanges(detail)[0]).toMatchObject({
      name: '变更后角色',
      value: '3',
      references: [{ name: '管理员' }],
    })
    expect(detail.changes.afterRoleIds).toEqual(['3'])
    expect(auditChanges(detail)[1]?.references).toEqual([])
  })
  it('accepts only an exact hexadecimal request ID for log searches', () => {
    expect(normalizedTraceId(' AABBCCDDEEFF00112233445566778899 ')).toBe(
      'aabbccddeeff00112233445566778899',
    )
    expect(traceDiagnostic('a'.repeat(32))).toBe(`traceId=${'a'.repeat(32)}`)
    for (const text of ['abc', 'a'.repeat(33), `a'.*`, 'a'.repeat(31) + '\n', '$(echo secret)']) {
      expect(traceDiagnostic(text)).toBeNull()
    }
  })
})
