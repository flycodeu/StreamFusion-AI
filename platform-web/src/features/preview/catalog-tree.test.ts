import { describe, expect, it } from 'vitest'
import type { CameraGroup } from '../../api/camera-groups/types'
import type { PreviewChannel } from '../../api/preview/types'
import { buildPreviewTree } from './catalog-tree'

function group(groupId: string, name: string, parentId: string | null = null): CameraGroup {
  return {
    groupId,
    parentId,
    name,
    sortOrder: 0,
    remark: null,
    version: '1',
    hasChildren: false,
    visibleCameraCount: 0,
    countObservedAt: null,
  }
}
function channel(cameraId: string, groupId: string | null, deviceGroupKey = 'd1'): PreviewChannel {
  return {
    cameraId,
    groupId,
    deviceGroupKey,
    name: `通道${cameraId}`,
    deviceName: '双通道相机',
    groupPath: '园区 / 出入口',
    lifecycle: 'ENABLED',
    profiles: [],
  }
}

describe('preview group hierarchy', () => {
  it('places devices by group identity when group names repeat or contain slashes', () => {
    const result = buildPreviewTree(
      [channel('1', '2'), channel('2', '3', 'd2')],
      [group('1', '园区'), group('2', '出入口 / 东', '1'), group('3', '出入口 / 东', '1')],
    )
    expect(result).toHaveLength(1)
    expect(result[0]?.children.map((node) => node.children[0]?.key)).toEqual([
      'device:d1',
      'device:d2',
    ])
    expect(result[0]?.children.map((node) => node.label)).toEqual(['出入口 / 东', '出入口 / 东'])
  })

  it('keeps a physical camera with multiple channels in one device node', () => {
    const result = buildPreviewTree([channel('1', '1'), channel('2', '1')], [group('1', '园区')])
    const devices = result[0]?.children
    expect(devices).toHaveLength(1)
    const device = devices?.[0]
    expect(device?.kind).toBe('device')
    if (device?.kind === 'device') {
      expect(device.channels.map((row) => row.cameraId)).toEqual(['1', '2'])
      expect(device.children.map((node) => node.key)).toEqual(['channel:1', 'channel:2'])
    }
  })

  it('keeps visible cameras available when group data is unavailable', () => {
    const ungrouped = { ...channel('2', null, 'd2'), groupPath: null }
    const result = buildPreviewTree([channel('1', '1'), ungrouped], [])
    expect(result.map((node) => node.label)).toEqual(['园区 / 出入口', '未分组'])
    expect(result.flatMap((node) => node.children).map((node) => node.key)).toEqual([
      'device:d1',
      'device:d2',
    ])
  })

  it('does not repeat a camera whose channels have different group memberships', () => {
    const result = buildPreviewTree(
      [channel('1', '1'), channel('2', '2')],
      [group('1', '南门'), group('2', '北门'), group('3', '无可见相机')],
    )
    expect(result).toHaveLength(1)
    expect(result[0]?.label).toBe('多个分组')
    expect(result[0]?.children).toHaveLength(1)
    expect(result[0]?.children[0]?.children).toHaveLength(2)
  })
})
