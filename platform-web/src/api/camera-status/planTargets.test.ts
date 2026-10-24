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
  it('retains membership and the effective channel/profile with decimal identifiers', () => {
    const selected = {
      ...target,
      groupIds: ['9007199254740993', '5'],
      cameraId: '21',
      channelName: '通道 A',
      streamProfileId: '22',
      profileName: '主码流',
      videoSupported: true,
    }
    expect(parsePlanTargets({ targets: [selected], limitExceeded: false }).targets[0]).toEqual(
      selected,
    )
    expect(() =>
      parsePlanTargets({ targets: [{ ...selected, groupIds: ['invalid'] }], limitExceeded: false }),
    ).toThrow()
    expect(() =>
      parsePlanTargets({
        targets: [{ ...selected, cameraId: 9007199254740992 }],
        limitExceeded: false,
      }),
    ).toThrow()
    expect(() =>
      parsePlanTargets({ targets: [{ ...selected, videoSupported: 'yes' }], limitExceeded: false }),
    ).toThrow()
  })
  it('retains the global authorized device count independently of the selected range', () => {
    expect(
      parsePlanTargets({ targets: [target], totalCount: 6, limitExceeded: false }).totalCount,
    ).toBe(6)
    expect(parsePlanTargets({ targets: [], totalCount: 0, limitExceeded: false }).totalCount).toBe(
      0,
    )
    expect(() => parsePlanTargets({ targets: [], totalCount: -1, limitExceeded: false })).toThrow()
    expect(() => parsePlanTargets({ targets: [], totalCount: '6', limitExceeded: false })).toThrow()
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
