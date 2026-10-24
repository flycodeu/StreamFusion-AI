import { describe, expect, it } from 'vitest'
import { parseGroup } from './types'

const group = {
  groupId: '1',
  parentId: null,
  name: '视频组',
  sortOrder: 0,
  remark: null,
  version: '0',
  hasChildren: false,
  visibleCameraCount: 16,
  countObservedAt: null,
}

describe('camera group count boundaries', () => {
  it('keeps device counts distinct from channel counts', () => {
    const result = parseGroup({ ...group, visibleDeviceCount: 1 })
    expect(result.visibleDeviceCount).toBe(1)
    expect(result.visibleCameraCount).toBe(16)
    expect(parseGroup({ ...group, visibleDeviceCount: 0 }).visibleDeviceCount).toBe(0)
  })

  it('leaves device counts unknown for responses without the new field', () => {
    expect(parseGroup(group).visibleDeviceCount).toBeNull()
    expect(parseGroup({ ...group, visibleDeviceCount: null }).visibleDeviceCount).toBeNull()
  })

  it.each(['1', 1.5, Number.MAX_SAFE_INTEGER + 1])('rejects malformed device count %s', (count) => {
    expect(() => parseGroup({ ...group, visibleDeviceCount: count })).toThrow()
  })
})
