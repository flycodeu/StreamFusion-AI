import { describe, expect, it } from 'vitest'
import { parseEvent } from '../../api/diagnostics/api'
import { eventReason, eventResult } from './presentation'
import { moduleNames, actionNames } from '../audit/labels'
import { changeNames, auditChanges } from '../audit/presentation'

const base = {
  occurredAt: '2026-10-14T12:00:00Z',
  kind: 'REQUEST',
  level: 'WARN',
  status: 409,
  stack: [],
}
describe('diagnostic and audit labels', () => {
  it('preserves IDs as strings and distinguishes a successful request from a camera result', () => {
    const request = parseEvent({ ...base, status: 201, level: 'INFO' })
    expect(eventResult(request)).toBe('请求成功')
    const check = parseEvent({
      ...base,
      kind: 'CAMERA_CHECK',
      state: 'COMPLETED',
      result: 'FAILED',
      planId: '9223372036854775806',
      reasonCode: 'RTSP_TIMEOUT',
    })
    expect(eventResult(check)).toBe('连接异常')
    expect(eventReason(check)).toBe('RTSP 检查超时')
    expect(check.planId).toBe('9223372036854775806')
    expect(eventResult({ ...check, state: 'QUEUED', result: null })).toBe('等待检查')
  })
  it('explains plan conflicts and keeps technical codes outside ordinary labels', () => {
    expect(eventReason(parseEvent({ ...base, reasonCode: 'PLAN_PROFILE_MISSING' }))).toBe(
      '部分设备缺少代表码流',
    )
    expect(eventReason(parseEvent({ ...base, errorCode: 'CONFLICT' }))).toBe('操作与当前状态冲突')
  })
  it('covers camera modules, operations and safe change fields', () => {
    for (const name of [
      'CAMERA',
      'CAMERA_DEVICE',
      'CAMERA_PROFILE',
      'CAMERA_SOURCE',
      'CAMERA_GROUP',
      'CAMERA_SCOPE',
      'CAMERA_STATUS_PLAN',
    ])
      expect(moduleNames[name]).toMatch(/[\u4e00-\u9fff]/)
    for (const name of [
      'CAMERA_CREATE',
      'CAMERA_DEVICE_MOVE',
      'CAMERA_PROFILE_UPDATE',
      'CAMERA_STATUS_PLAN_CREATE',
      'CAMERA_STATUS_PLAN_STOP',
    ])
      expect(actionNames[name]).toMatch(/[\u4e00-\u9fff]/)
    expect(changeNames.intervalMinutes).toBe('检测间隔（分钟）')
    const changes = auditChanges({
      changes: {
        afterState: 'ACTIVE',
        beforeLifecycle: 'DISABLED',
        enabled: 'true',
        name: 'ACTIVE',
      },
      relations: {},
    })
    expect(changes.map((item) => item.value)).toEqual(['执行中', '已停用', '是', 'ACTIVE'])
  })
  it('rejects malformed rows', () => {
    expect(() => parseEvent({ ...base, durationMs: '12' })).toThrow()
  })
})
