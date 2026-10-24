import { describe, expect, it } from 'vitest'
import { parseAuditDetail, parseAuditReference } from '../../api/audit/types'
import {
  auditChanges,
  auditReason,
  normalizedTraceId,
  referenceName,
  referenceSource,
  traceDiagnostic,
} from './presentation'

describe('readable historical audit evidence', () => {
  it('interprets shared field names only in their owning module', () => {
    const changes = { mode: 'COUNT', beforeMode: 'LOCAL', afterMode: 'SEALED', state: 'ACTIVE' }
    const detail = (module: string) => ({ record: { module }, changes, relations: {} })
    expect(auditChanges(detail('AUDIT_RETENTION')).map((item) => item.value)).toEqual([
      '按数量',
      'LOCAL',
      'SEALED',
      'ACTIVE',
    ])
    expect(auditChanges(detail('PREVIEW_NODE')).map((item) => item.value)).toEqual([
      'COUNT',
      '不接新预览',
      '已签发，等待 Ops 领取',
      'ACTIVE',
    ])
    expect(auditChanges(detail('USER')).map((item) => item.value)).toEqual([
      'COUNT',
      'LOCAL',
      'SEALED',
      'ACTIVE',
    ])
    expect(auditChanges(detail('CAMERA_STATUS_PLAN'))[3]?.value).toBe('运行中')
    expect(changes).toEqual({
      mode: 'COUNT',
      beforeMode: 'LOCAL',
      afterMode: 'SEALED',
      state: 'ACTIVE',
    })
  })
  it('covers current and historical lifecycle values without interpreting names as codes', () => {
    const detail = {
      changes: { beforeLifecycle: 'DRAFT', afterLifecycle: 'PENDING_ASSIGNMENT', name: 'ENABLED' },
      relations: {},
    }
    expect(auditChanges(detail).map((item) => item.value)).toEqual(['待配置', '待归档', 'ENABLED'])
  })
  it('retains unfamiliar field names and codes without resolving inherited properties', () => {
    const changes = { ['__proto__']: 'constructor', futureStatus: 'FUTURE' }
    expect(auditChanges({ changes, relations: {} })).toEqual([
      { key: '__proto__', name: '__proto__', value: 'constructor', references: [] },
      { key: 'futureStatus', name: 'futureStatus', value: 'FUTURE', references: [] },
    ])
  })
  it('uses the shared Chinese stream types without rewriting historical evidence', () => {
    const detail = {
      changes: { usageHint: 'SUB', beforeUsageHint: 'MAIN', afterUsageHint: 'CUSTOM' },
      relations: {},
    }
    expect(auditChanges(detail).map((change) => change.value)).toEqual([
      '子码流',
      '主码流',
      '自定义',
    ])
    expect(detail.changes.usageHint).toBe('SUB')
  })
  it('describes known reasons and preserves unknown reason codes for troubleshooting', () => {
    expect(auditReason('INVALID_CREDENTIALS')).toBe('账号或密码不正确')
    expect(auditReason('LOGIN_FAILURE_THRESHOLD')).toBe('登录失败次数超限')
    expect(auditReason('FUTURE_REASON')).toBe('FUTURE_REASON')
    expect(auditReason(null)).toBe('—')
  })
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
