import { describe, expect, it } from 'vitest'
import { formatBytes } from './formatBytes'
import { bytes } from '../features/storage/display'

describe('shared byte formatting', () => {
  it('uses binary units consistently with explicit units for fixed-scale metrics', () => {
    expect(formatBytes(1024)).toBe('1.00 KiB')
    expect(formatBytes(1024 ** 3)).toBe('1.00 GiB')
    expect(formatBytes(1024 ** 2, { unit: 'GiB', precision: 3 })).toBe('0.001 GiB')
    expect(formatBytes(1536, { unit: 'KiB', precision: 0 })).toBe('2 KiB')
    expect(formatBytes(1280, { unit: 'KiB', precision: 1 })).toBe('1.3 KiB')
  })
  it('retains exact integers beyond JavaScript safe-number precision', () => {
    expect(formatBytes('9223372036854775807', { unit: 'B', precision: 0 })).toBe(
      '9223372036854775807 B',
    )
    expect(formatBytes('9007199254740993', { unit: 'KiB', precision: 3 })).toBe(
      '8796093022208.001 KiB',
    )
    expect(formatBytes(9223372036854775807n)).toBe('8.00 EiB')
    expect(formatBytes(Number.MAX_SAFE_INTEGER + 1)).toBe('—')
  })
  it('rejects invalid counts and bounds precision while preserving empty-state wording', () => {
    for (const value of [null, undefined, '', '1.2', ' 1024', '-1', -1, 1.2, NaN, Infinity])
      expect(formatBytes(value, { empty: '暂无' })).toBe('暂无')
    expect(formatBytes(1024, { precision: Infinity })).toBe('1.00 KiB')
    expect(formatBytes(1024, { precision: 100 })).toBe('1.000 KiB')
    expect(formatBytes(1024, { precision: -1 })).toBe('1 KiB')
    expect(bytes(null)).toBe('暂无统计')
    expect(bytes('invalid')).toBe('不可获取')
    expect(bytes(0)).toBe('0 B')
    expect(bytes('9223372036854775807')).toBe('8.00 EiB')
  })
})
