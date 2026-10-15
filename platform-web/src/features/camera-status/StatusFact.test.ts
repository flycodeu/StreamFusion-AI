import { afterEach, describe, expect, it, vi } from 'vitest'
import { createSSRApp } from 'vue'
import { renderToString } from 'vue/server-renderer'
import type { ProfileStatus } from '../../api/camera-status/types'
import StatusFact from './StatusFact.vue'

const profile: ProfileStatus = {
  streamProfileId: '2',
  layer: 'CONTROL',
  probeKind: 'RTSP_DESCRIBE',
  freshness: 'CURRENT',
  configurationCurrent: true,
  latestCheck: null,
  canCheck: true,
  blockingReasonCode: null,
  observation: {
    value: 'ACCESSIBLE',
    reasonCode: 'RTSP_ACCESSIBLE',
    checkId: '100',
    observedAt: '2026-10-13T01:00:00Z',
    readAt: '2026-10-13T01:00:01Z',
    validUntil: '2026-10-13T01:01:00Z',
  },
}
afterEach(() => {
  vi.restoreAllMocks()
})
describe('passive CONTROL fact component', () => {
  it('renders observation and validity times without requests or timers', async () => {
    const timer = vi.spyOn(globalThis, 'setTimeout'),
      fetchMock = vi.spyOn(globalThis, 'fetch')
    const html = await renderToString(createSSRApp(StatusFact, { profile }))
    expect(html).toContain('连接检查通过')
    expect(html).toContain('连接检查结果')
    expect(html).toContain('检查时间')
    expect(html).toContain('有效至')
    expect(html).not.toMatch(/在线|流正常/)
    expect(timer).not.toHaveBeenCalled()
    expect(fetchMock).not.toHaveBeenCalled()
  })
  it.each([
    [{ ...profile, freshness: 'STALE' as const }, '结果已过期'],
    [{ ...profile, configurationCurrent: false }, '配置已变化'],
  ])('visually demotes historic accessibility', async (fact, label) => {
    const html = await renderToString(createSSRApp(StatusFact, { profile: fact }))
    expect(html).toContain(label)
    expect(html).toContain('上次结果：')
    expect(html).not.toContain('el-tag--success')
  })
})
