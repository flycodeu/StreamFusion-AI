import { describe, expect, it } from 'vitest'
import { groupTree } from './tree'
import type { CameraGroup } from '../../api/camera-groups/types'
const group = (groupId: string, parentId: string | null, name: string): CameraGroup => ({
  groupId,
  parentId,
  name,
  sortOrder: 0,
  version: '0',
  hasChildren: false,
  visibleCameraCount: 1,
  remark: null,
  countObservedAt: null,
})
describe('camera group tree', () => {
  it('keeps same names distinguishable by complete path and excludes an edited subtree', () => {
    const rows = [
      group('1', null, '厂区甲'),
      group('2', null, '厂区乙'),
      group('3', '1', '入口'),
      group('4', '2', '入口'),
      group('5', '3', '内侧'),
    ]
    const tree = groupTree(rows)
    expect(tree[0]?.children[0]?.path).toBe('厂区甲 / 入口')
    expect(tree[1]?.children[0]?.path).toBe('厂区乙 / 入口')
    expect(groupTree(rows, '3')[0]?.children).toEqual([])
    expect(groupTree(rows, '1').map((g) => g.groupId)).toEqual(['2'])
  })
})
