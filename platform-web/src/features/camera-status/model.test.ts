import { describe, expect, it } from 'vitest'
import type { ProfileStatus, StatusCheck } from '../../api/camera-status/types'
import { executionLabels, factPresentation, isTerminal, reasonLabel } from './model'

const profile: ProfileStatus = {
  streamProfileId: '1',
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
    checkId: '2',
    observedAt: '2026-10-13T01:00:00Z',
    readAt: '2026-10-13T01:00:00Z',
    validUntil: '2026-10-13T01:01:00Z',
  },
}
describe('CONTROL fact presentation', () => {
  it('does not call DESCRIBE accessibility online or media health', () => {
    expect(factPresentation(profile)).toMatchObject({ label: '连接检查通过', tone: 'success' })
    expect(reasonLabel('WORKER_BUSY')).toBe('检查容量已满')
  })
  it('explains backend eligibility and interruption reasons without claiming device failure', () => {
    expect(reasonLabel('ONVIF_MEDIA_NOT_CONFIGURED')).toBe('缺少 ONVIF 媒体信息，请重新发现设备')
    expect(reasonLabel('STREAM_URI_AUTH_REJECTED')).toBe('获取 RTSP 地址时认证未通过')
    expect(reasonLabel('STREAM_URI_TIMEOUT')).toBe('获取 RTSP 地址超时')
    expect(reasonLabel('SOURCE_PROTOCOL_NOT_SUPPORTED')).toBe('此接入方式的连接检测尚未适配')
    expect(reasonLabel('ACTOR_REVOKED')).toBe('检查资格已失效')
    expect(reasonLabel('SERVICE_RESTARTED')).toBe('服务重启，检查已中断')
  })
  it.each([
    { ...profile, freshness: 'STALE' as const },
    { ...profile, configurationCurrent: false },
  ])('never displays stale/configuration-changed accessibility as current success', (fact) => {
    expect(factPresentation(fact)).toMatchObject({ tone: 'info', historical: true })
    expect(factPresentation(fact).label).not.toBe('连接检查通过')
  })
  it('keeps execution state separate from observation and stops all terminal states', () => {
    for (const executionState of ['COMPLETED', 'SKIPPED', 'CANCELLED', 'INTERRUPTED'] as const)
      expect(isTerminal({ executionState } as StatusCheck)).toBe(true)
    expect(isTerminal({ executionState: 'QUEUED' } as StatusCheck)).toBe(false)
    expect(executionLabels.SKIPPED).toBe('未执行')
    expect(factPresentation(null).label).toBe('状态未读取')
    expect(factPresentation({ ...profile, observation: null, freshness: 'NEVER' }).label).toBe(
      '尚未检查',
    )
  })
})
