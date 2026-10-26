import { describe, expect, it, vi } from 'vitest'
import { brandOptions, secret, sourceBrandLabel, useCreateRequest } from './form'
import type { AccessAdapter } from '../../api/camera-access/types'

describe('camera configuration commands', () => {
  it('offers brands from registered vendor adapters without presenting protocols as brands', () => {
    const adapters = [
      { type: 'ONVIF', category: 'DEVICE' },
      { type: 'HIKVISION', category: 'DEVICE' },
      { type: 'DAHUA', category: 'DEVICE' },
      { type: 'HIKVISION', category: 'DEVICE' },
      { type: 'HIK_PLATFORM', category: 'PLATFORM' },
    ] as AccessAdapter[]
    expect(brandOptions(adapters)).toEqual(['海康', '大华'])
    expect(brandOptions([])).toEqual([])
  })
  it('keeps the original request body and key after a lost create response', () => {
    const create = useCreateRequest<{ name: string; profiles: { path: string }[] }>()
    const input = { name: '炉前', profiles: [{ path: '/encoded%2Fpath' }] }
    const first = create.capture(input)
    input.profiles[0]!.path = '/changed'
    vi.spyOn(Date, 'now').mockReturnValue(Date.now() + 600_000)
    expect(create.capture({ name: '另一个名称', profiles: [] })).toBe(first)
    expect(first.profiles[0]?.path).toBe('/encoded%2Fpath')
    expect(first.clientRequestId).toMatch(/^\d+-[a-f\d-]{36}$/)
    create.reset()
    expect(create.capture(input).clientRequestId).not.toBe(first.clientRequestId)
    vi.restoreAllMocks()
  })
  it('never sends hidden saved values with KEEP or CLEAR credential actions', () => {
    expect(secret('KEEP', 'secret')).toEqual({ action: 'KEEP' })
    expect(secret('CLEAR', 'secret')).toEqual({ action: 'CLEAR' })
    expect(secret('REPLACE', 'exact%20value')).toEqual({
      action: 'REPLACE',
      value: 'exact%20value',
    })
  })
  it('presents source brands without repeating protocol or source names', () => {
    expect(
      sourceBrandLabel({
        sourceType: 'HIKVISION',
        sourceDisplayName: '海康接入',
        manufacturer: null,
      }),
    ).toBe('海康')
    expect(
      sourceBrandLabel({ sourceType: 'DAHUA', sourceDisplayName: '大华接入', manufacturer: null }),
    ).toBe('大华')
    expect(
      sourceBrandLabel({ sourceType: 'RTSP', sourceDisplayName: '现场地址', manufacturer: null }),
    ).toBe('未识别')
    expect(
      sourceBrandLabel({
        sourceType: 'ONVIF',
        sourceDisplayName: '门口设备',
        manufacturer: 'Hikvision Digital Technology',
      }),
    ).toBe('海康')
    expect(
      sourceBrandLabel({
        sourceType: null,
        sourceDisplayName: '未知来源',
        manufacturer: '自定义品牌',
      }),
    ).toBe('自定义品牌')
  })
})
