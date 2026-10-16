import { describe, expect, it } from 'vitest'
import type { PreviewChannel } from '../../api/preview/types'
import { groupCatalog, selectProfile, previewReason } from './model'
import { deviceLayout, layoutCount, MAX_PREVIEW_SLOTS } from './layouts'

const channel = (cameraId: string, deviceGroupKey = 'd1'): PreviewChannel => ({
  cameraId,
  name: `通道${cameraId}`,
  deviceGroupKey,
  deviceName: '双通道相机',
  groupId: '1',
  groupPath: '园区 / 南门',
  lifecycle: 'ENABLED',
  profiles: [
    {
      streamProfileId: `${cameraId}1`,
      label: 'Profile',
      usage: 'MAIN',
      enabled: true,
      configured: true,
      videoCodec: 'H264',
      width: 1920,
      height: 1080,
      frameRate: 25,
      bitrateKbps: 4000,
    },
  ],
})
describe('preview catalog selection', () => {
  it('explains restart reconciliation without promising playback restoration', () => {
    expect(previewReason('PREVIEW_RECOVERY_REQUIRED')).toContain('重启后的预览资源正在核对')
    expect(previewReason('PREVIEW_RECOVERY_REQUIRED')).toContain('管理员')
  })
  it('keeps two device channels under the same physical camera', () => {
    const result = groupCatalog([channel('1'), channel('2'), channel('3', 'd2')])
    expect(result).toHaveLength(1)
    expect(result[0]?.devices.map((device) => device.channels.length)).toEqual([2, 1])
  })
  it('does not silently play main stream when auxiliary stream is missing', () => {
    expect(selectProfile(channel('1'), 'SUB')).toBeNull()
    expect(selectProfile(channel('1'), 'MAIN')?.streamProfileId).toBe('11')
  })
  it('keeps one physical camera even when its channels have different groups', () => {
    const second = { ...channel('2'), groupPath: '园区 / 北门' }
    const result = groupCatalog([channel('1'), second])
    expect(result).toHaveLength(1)
    expect(result[0]?.path).toBe('多个分组')
    expect(result[0]?.devices[0]?.channels).toHaveLength(2)
  })
  it('ignores disabled or unconfigured profiles', () => {
    const row = channel('1')
    row.profiles[0]!.enabled = false
    expect(selectProfile(row, 'MAIN')).toBeNull()
    row.profiles[0]!.enabled = true
    row.profiles[0]!.configured = false
    expect(selectProfile(row, 'MAIN')).toBeNull()
  })
  it('fits two channels in a 1 by 2 layout and keeps the 1 plus 5 layout to six slots', () => {
    expect(deviceLayout(2)).toBe('2')
    expect(deviceLayout(7)).toBe('8')
    expect(layoutCount('1+5')).toBe(6)
  })
  it('uses eight by eight for larger cameras and caps the grid at 64 slots', () => {
    expect(deviceLayout(16)).toBe('16')
    expect(deviceLayout(17)).toBe('25')
    expect(deviceLayout(100)).toBe('64')
    expect(layoutCount('64')).toBe(MAX_PREVIEW_SLOTS)
  })
})
