import { describe, expect, it } from 'vitest'
import { deviceLayout, getPreviewLayout, layoutCount, previewLayouts } from './layouts'

describe('preview layout capacity', () => {
  it('fits a camera into the smallest regular layout without skipping medium grids', () => {
    expect([3, 5, 7, 10, 17, 26, 37, 50].map(deviceLayout)).toEqual([
      '4',
      '6',
      '8',
      '12',
      '25',
      '36',
      '49',
      '64',
    ])
  })

  it('counts a large tile once even though it covers several grid cells', () => {
    expect(layoutCount('1+5')).toBe(6)
    expect(layoutCount('1+7')).toBe(8)
    expect(layoutCount('1+12')).toBe(13)
    expect(getPreviewLayout('1+7').leadSpan).toBe(3)
  })

  it('keeps the configured grids within the existing 64-player limit', () => {
    expect(new Set(previewLayouts.map((layout) => layout.value)).size).toBe(previewLayouts.length)
    for (const layout of previewLayouts) {
      const capacity = layoutCount(layout.value)
      expect(capacity).toBeGreaterThan(0)
      expect(capacity).toBeLessThanOrEqual(64)
      const definition = getPreviewLayout(layout.value)
      expect(definition.leadSpan || 1).toBeLessThanOrEqual(Math.min(layout.columns, layout.rows))
    }
  })
})
