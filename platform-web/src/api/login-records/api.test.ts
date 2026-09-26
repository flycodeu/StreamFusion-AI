import { beforeEach, describe, expect, it, vi } from 'vitest'
import { request } from '../client'
import { getLoginRecords } from './api'
import { parseLoginRecord } from './types'
import { loginEndReason, sessionDuration } from '../../features/login-records/presentation'
vi.mock('../client', () => ({ request: vi.fn() }))

describe('login records access and data boundaries', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    vi.mocked(request).mockResolvedValue({
      data: { items: [], page: 1, size: 20, total: 0 },
    } as never)
  })
  it('uses self and managed-user endpoints and preserves cancellation', async () => {
    const controller = new AbortController()
    await getLoginRecords(undefined, { page: 1, size: 20 }, controller.signal)
    expect(request).toHaveBeenLastCalledWith(
      expect.objectContaining({ path: '/auth/login-records/page', signal: controller.signal }),
    )
    await getLoginRecords('9007199254740993', { page: 2, size: 50 })
    expect(request).toHaveBeenLastCalledWith(
      expect.objectContaining({
        path: '/user/9007199254740993/login-records/page',
        params: { page: 2, size: 50 },
      }),
    )
    await expect(getLoginRecords('../auth/me', { page: 1, size: 20 })).rejects.toThrow()
    expect(request).toHaveBeenCalledTimes(2)
  })
  it('preserves large IDs and rejects impossible duration data', () => {
    const value = {
      id: '9223372036854775806',
      userId: '9007199254740993',
      username: 'zhangsan',
      nickname: null,
      sourceIp: '::1',
      region: '本机',
      browser: 'Edge 140',
      os: 'Windows',
      loginAt: '2026-09-26T00:00:00Z',
      lastActivityAt: '2026-09-26T00:01:00Z',
      endedAt: null,
      endReason: null,
      durationSeconds: 60,
      status: 'ACTIVE',
    }
    expect(parseLoginRecord(value).id).toBe('9223372036854775806')
    expect(() => parseLoginRecord({ ...value, durationSeconds: -1 })).toThrow()
    expect(() =>
      parseLoginRecord({ ...value, durationSeconds: Number.MAX_SAFE_INTEGER + 1 }),
    ).toThrow()
    expect(sessionDuration(3671)).toBe('1小时 1分')
    expect(sessionDuration(61)).toBe('1分 1秒')
    expect(loginEndReason('IDLE_TIMEOUT')).toBe('空闲超时')
  })
})
