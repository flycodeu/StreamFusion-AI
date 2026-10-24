import { describe, expect, it } from 'vitest'
import { effectiveUsage, streamLabel, usageLabel, usageLabels, usageOptions } from './streamLabels'
import { streamLabel as formStreamLabel } from './form'
import { usageLabels as previewLabels } from '../preview/model'

describe('shared stream display names', () => {
  it.each([
    ['mainStream', '主码流'],
    ['MainStream', '主码流'],
    ['MAIN_STREAM', '主码流'],
    ['main-stream', '主码流'],
    ['subStream', '子码流'],
    [' sub stream ', '子码流'],
    ['辅码流', '子码流'],
    ['thirdStream', '第三码流'],
    ['MediaProfile_Channel1_MainStream', '主码流'],
    ['MediaProfile_Channel2_SubStream1', '子码流'],
    ['MediaProfile_Channel2_SubStream2', '第三码流'],
  ])('displays %s consistently even when a summary has no classification', (label, expected) => {
    expect(streamLabel({ label })).toBe(expected)
  })

  it('shares one type dictionary across camera forms and preview', () => {
    expect(previewLabels).toBe(usageLabels)
    expect(formStreamLabel).toBe(streamLabel)
    expect(usageOptions.find((item) => item.value === 'SUB')?.label).toBe('子码流')
    expect(usageLabel('FUTURE_USAGE')).toBe('FUTURE_USAGE')
  })

  it('uses the effective type instead of an obsolete generic device label', () => {
    const profile = {
      label: 'mainStream',
      usageHint: 'UNKNOWN',
      classification: { usageHint: 'SUB', origin: 'MANUAL' },
    }
    expect(effectiveUsage(profile)).toBe('SUB')
    expect(streamLabel(profile)).toBe('子码流')
    expect(streamLabel({ label: 'profile_001', usageHint: 'THIRD' })).toBe('第三码流')
  })

  it('preserves meaningful custom names even when the original source matched a naming rule', () => {
    expect(
      streamLabel({
        label: '冷床全貌',
        usageHint: 'UNKNOWN',
        classification: { usageHint: 'SUB', origin: 'NAME_RULE' },
      }),
    ).toBe('冷床全貌')
    expect(streamLabel({ label: 'mainStream', usageHint: 'CUSTOM' })).toBe('mainStream')
    expect(streamLabel({ label: 'mainStream备用', usageHint: 'MAIN' })).toBe('mainStream备用')
    expect(streamLabel({ label: 'constructor' })).toBe('constructor')
    expect(usageLabel('constructor')).toBe('constructor')
    expect(streamLabel({ label: '未知厂商配置' })).toBe('未知厂商配置')
  })

  it('distinguishes duplicate names without modifying raw labels, IDs or types', () => {
    const profiles = Object.freeze([
      Object.freeze({ streamProfileId: '101', label: 'mainStream', usageHint: 'MAIN' }),
      Object.freeze({ streamProfileId: '102', label: 'Main Stream', usageHint: 'MAIN' }),
      Object.freeze({ streamProfileId: '103', label: 'subStream', usageHint: 'SUB' }),
    ])
    expect(profiles.map((profile) => streamLabel(profile, profiles))).toEqual([
      '主码流 1',
      '主码流 2',
      '子码流',
    ])
    expect(streamLabel({ ...profiles[1]! }, profiles)).toBe('主码流 2')
    expect(profiles[0]).toEqual({ streamProfileId: '101', label: 'mainStream', usageHint: 'MAIN' })
  })

  it('falls back safely when device names or type hints are absent', () => {
    expect(streamLabel({ label: '', usageHint: 'SUB' })).toBe('子码流')
    expect(streamLabel({ label: null })).toBe('未指定')
    expect(streamLabel({ label: '相机一', usageHint: 'MAIN' }, [], '相机一')).toBe('主码流')
  })
})
