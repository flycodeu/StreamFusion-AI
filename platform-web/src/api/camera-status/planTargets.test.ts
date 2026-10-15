import { describe, expect, it, vi } from 'vitest'
import { request } from '../client'
import { inspectPlanTargets, parsePlanTargets } from './planTargets'

vi.mock('../client', () => ({ request: vi.fn() }))
const target = { groupKey: 'd100', name: '模拟设备', reasonCode: null }
describe('patrol scope preview', () => {
  it('accepts two thousand distinct targets without the generic list limit', () => {
    const targets = Array.from({ length: 2000 }, (_, i) => ({ ...target, groupKey: 'd' + (i + 1) }))
    expect(parsePlanTargets({ targets, limitExceeded: false }).targets).toHaveLength(2000)
  })
  it('requests the complete authorized scope once without submitting a check', async () => {
    vi.mocked(request).mockResolvedValue({
      data: { targets: [target], limitExceeded: false },
    } as never)
    expect(await inspectPlanTargets({ scope: 'ALL' })).toEqual({
      targets: [target],
      limitExceeded: false,
    })
    expect(request).toHaveBeenCalledWith(
      expect.objectContaining({
        path: '/camera-status/plans/targets/preview',
        method: 'POST',
        body: { scope: 'ALL' },
      }),
    )
  })
  it('keeps exclusions separate from offline facts', () => {
    expect(
      parsePlanTargets({
        targets: [{ ...target, reasonCode: 'LOCATOR_NOT_CONFIGURED' }],
        limitExceeded: false,
      }).targets[0]?.reasonCode,
    ).toBe('LOCATOR_NOT_CONFIGURED')
  })
  it.each([
    { targets: [target, target], limitExceeded: false },
    { targets: [{ ...target, groupKey: 'invalid' }], limitExceeded: false },
    { targets: [target], limitExceeded: true },
    { targets: [], limitExceeded: 'true' },
    {
      targets: Array.from({ length: 2001 }, (_, i) => ({ ...target, groupKey: 'd' + (i + 1) })),
      limitExceeded: false,
    },
  ])('rejects invalid or silently truncated previews', (value) => {
    expect(() => parsePlanTargets(value)).toThrow()
  })
})
