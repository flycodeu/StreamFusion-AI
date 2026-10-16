import { describe, it, expect } from 'vitest'
import { deviceStatus } from './deviceStatus'
import type { CameraDeviceGroup } from '../../api/camera/types'

describe('device CONTROL summary', () => {
  it('retains the last result and distinguishes configuration changes from missing evidence', () => {
    const device = {
      statusSummary: { state: 'ACCESSIBLE', validUntil: '2026-10-13T01:00:00Z' },
    } as CameraDeviceGroup
    expect(deviceStatus(device).label).toBe('可达')
    expect(
      deviceStatus({ ...device, statusSummary: { ...device.statusSummary!, state: 'STALE' } })
        .label,
    ).toBe('配置已变更')
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
