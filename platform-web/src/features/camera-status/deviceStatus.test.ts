import { describe, it, expect } from 'vitest'
import { deviceStatus } from './deviceStatus'
import type { CameraDeviceGroup } from '../../api/camera/types'

describe('device CONTROL summary', () => {
  it('keeps expired results and missing evidence out of reachable state', () => {
    const device = {
      statusSummary: { state: 'ACCESSIBLE', validUntil: '2026-10-13T01:00:00Z' },
    } as CameraDeviceGroup
    expect(deviceStatus(device, Date.parse('2026-10-13T00:59:59Z')).label).toBe('可达')
    expect(deviceStatus(device, Date.parse('2026-10-13T01:00:00Z')).label).toBe('结果已过期')
    expect(deviceStatus({} as CameraDeviceGroup).label).toBe('未检测')
  })
  it('does not label failed CONTROL or a partially observed recorder as offline', () => {
    expect(deviceStatus({ statusSummary: { state: 'FAILED' } } as CameraDeviceGroup).label).toBe(
      '连接异常',
    )
    expect(
      deviceStatus({ statusSummary: { state: 'PARTIAL' } } as CameraDeviceGroup).label,
    ).toContain('部分')
  })
})
