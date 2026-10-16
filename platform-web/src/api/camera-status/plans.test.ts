import { describe, expect, it } from 'vitest'
import { parseStatusPlan } from './plans'

const plan = {
  planId: '9007199254740993',
  state: 'ACTIVE',
  intervalMinutes: 60,
  targetCount: 2,
  startAt: '2026-10-13T01:00:00Z',
  endAt: '2026-10-14T01:00:00Z',
  version: '0',
  blockedCount: 1,
  nextAt: '2026-10-13T02:00:00Z',
  lastReasonCode: 'CONFIGURATION_CHANGED',
  layer: 'CONTROL',
  dispatchMode: 'POLLING',
  parallelism: 1,
}
describe('finite patrol contract', () => {
  it('retains target diagnostics and decimal identifiers', () => {
    expect(parseStatusPlan(plan)).toEqual(plan)
  })
  it('validates latest receipt counts and server time without confusing skipped with failed', () => {
    const progress = { success: 0, failed: 0, pending: 0, skipped: 1, unknown: 1 }
    expect(parseStatusPlan({ ...plan, progress, serverNow: plan.startAt }).progress).toEqual(
      progress,
    )
    expect(() => parseStatusPlan({ ...plan, progress: { ...progress, failed: 3 } })).toThrow()
    expect(() => parseStatusPlan({ ...plan, progress: { ...progress, skipped: -1 } })).toThrow()
    expect(() => parseStatusPlan({ ...plan, serverNow: 'invalid' })).toThrow()
  })
  it.each([
    ['layer', 'INVALID'],
    ['dispatchMode', 'UNLIMITED'],
    ['parallelism', 0],
    ['parallelism', 5],
    ['parallelism', 2],
    ['blockedCount', -1],
    ['blockedCount', 3],
    ['nextAt', 'invalid'],
    ['intervalMinutes', 0],
    ['targetCount', 2001],
    ['endAt', plan.startAt],
  ])('rejects invalid %s', (field, value) => {
    expect(() => parseStatusPlan({ ...plan, [field]: value })).toThrow()
  })
})
