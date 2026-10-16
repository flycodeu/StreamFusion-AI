import { afterEach, describe, expect, it, vi } from 'vitest'
import { createSSRApp } from 'vue'
import { renderToString } from 'vue/server-renderer'
import type { LayerStatus } from '../../api/camera-status/types'
import StatusFact from './StatusFact.vue'

const profile: LayerStatus = {
  layer: 'CONTROL',
  probeKind: 'RTSP_DESCRIBE',
  freshness: 'CURRENT',
  configurationCurrent: true,
  latestCheck: null,
  timeline: null,
  canCheck: true,
  blockingReasonCode: null,
  observation: {
    value: 'ACCESSIBLE',
    reasonCode: 'RTSP_ACCESSIBLE',
    checkId: '100',
    mediaEvidence: null,
    observedAt: '2026-10-13T01:00:00Z',
    readAt: '2026-10-13T01:00:01Z',
    validUntil: '2026-10-13T01:01:00Z',
  },
}
afterEach(() => {
  vi.restoreAllMocks()
})
describe('passive CONTROL fact component', () => {
  it('shows bounded MEDIA receive evidence without claiming decoding or long-run stability', async () => {
    const media: LayerStatus = {
      ...profile,
      layer: 'MEDIA',
      probeKind: 'ZLM_RTP_FRAMES',
      observation: {
        ...profile.observation!,
        reasonCode: 'MEDIA_RECEIVING',
        mediaEvidence: {
          windowStartedAt: '2026-10-13T00:59:55Z',
          windowEndedAt: '2026-10-13T01:00:00Z',
          firstFrames: 8,
          lastFrames: 108,
          sampleCount: 3,
          maxGapMs: 2500,
        },
      },
    }
    const html = await renderToString(createSSRApp(StatusFact, { profile: media }))
    expect(html).toContain('视频收帧结果')
    expect(html).toContain('采样收帧正常')
    expect(html).toContain('5.0 秒')
    expect(html).toContain('100 帧')
    expect(html).toContain('3 次')
    expect(html).not.toContain('未增长间隔')
    expect(html).not.toMatch(/解码正常|持续稳定|已离线|>在线</)
  })
  it('renders the last observation without a validity deadline, requests or timers', async () => {
    const timer = vi.spyOn(globalThis, 'setTimeout'),
      fetchMock = vi.spyOn(globalThis, 'fetch')
    const html = await renderToString(createSSRApp(StatusFact, { profile }))
    expect(html).toContain('连接检查通过')
    expect(html).toContain('连接检查结果')
    expect(html).toContain('检查时间')
    expect(html).not.toContain('有效至')
    expect(html).not.toMatch(/在线|流正常/)
    expect(timer).not.toHaveBeenCalled()
    expect(fetchMock).not.toHaveBeenCalled()
  })
  it.each([
    [{ ...profile, freshness: 'STALE' as const }, '待刷新'],
    [{ ...profile, configurationCurrent: false }, '待刷新'],
  ])('visually demotes historic accessibility', async (fact, label) => {
    const html = await renderToString(createSSRApp(StatusFact, { profile: fact }))
    expect(html).toContain(label)
    expect(html).toContain('上次结果：')
    expect(html).not.toContain('el-tag--success')
  })
})
