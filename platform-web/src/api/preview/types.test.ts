import { describe, expect, it } from 'vitest'
import { parsePreviewCapacity } from './types'

const sample = {
  maxSessions: 36,
  occupiedSessions: 12,
  closingSessions: 2,
  maxTranscodes: 2,
  occupiedTranscodes: 1,
  transcodesAvailable: true,
  observedAt: '2026-10-16T01:00:00Z',
}
describe('preview capacity decoding', () => {
  it('reads known usage, including disabled or exceeded configured transcode budgets', () => {
    expect(parsePreviewCapacity(sample)).toEqual(sample)
    expect(parsePreviewCapacity({ ...sample, maxTranscodes: 0 }).occupiedTranscodes).toBe(1)
  })
  it('keeps an unreadable journal unknown, never zero', () => {
    expect(
      parsePreviewCapacity({ ...sample, occupiedTranscodes: null, transcodesAvailable: false })
        .occupiedTranscodes,
    ).toBeNull()
  })
  it.each([
    { maxSessions: 65 },
    { maxSessions: 0 },
    { occupiedSessions: -1 },
    { closingSessions: 13 },
    { maxTranscodes: 9 },
    { occupiedTranscodes: 0.5 },
    { transcodesAvailable: false },
    { occupiedTranscodes: null },
    { observedAt: 'not-time' },
    { occupiedSessions: '2' },
  ])('rejects malformed usage %o', (patch) => {
    expect(() => parsePreviewCapacity({ ...sample, ...patch })).toThrow()
  })
})
