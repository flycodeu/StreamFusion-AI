import { describe, expect, it, vi } from 'vitest'
import { secret, useCreateRequest } from './form'

describe('camera configuration commands', () => {
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
})
