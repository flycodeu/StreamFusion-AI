import { describe, expect, it, vi } from 'vitest'
import { deleteStatusPlan, parseStatusPlan } from './plans'

import { request } from '../client'
vi.mock('../client', () => ({ request: vi.fn() }))

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
  it('deletes with a quoted version header and no fabricated success response', async () => {
    vi.mocked(request).mockResolvedValue({ data: null } as never)
    await deleteStatusPlan(plan)
    expect(request).toHaveBeenCalledWith(
      expect.objectContaining({
        path: '/camera-status/plans/9007199254740993',
        method: 'DELETE',
        ifMatch: '"0"',
        successStatus: 200,
      }),
    )
    await expect(deleteStatusPlan({ planId: '../hidden', version: '0' })).rejects.toThrow()
  })
  it('retains target diagnostics and decimal identifiers', () => {
    expect(parseStatusPlan(plan)).toEqual({ ...plan, checkVideo: false })
  })
  it('supports sequential video plans while retaining legacy media-only plans', () => {
    expect(parseStatusPlan({ ...plan, checkVideo: true }).checkVideo).toBe(true)
    expect(parseStatusPlan({ ...plan, layer: 'MEDIA' }).checkVideo).toBe(false)
    expect(() => parseStatusPlan({ ...plan, layer: 'MEDIA', checkVideo: true })).toThrow()
    expect(() =>
      parseStatusPlan({ ...plan, checkVideo: true, dispatchMode: 'PARALLEL', parallelism: 2 }),
    ).toThrow()
    expect(() => parseStatusPlan({ ...plan, checkVideo: 'true' })).toThrow()
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
