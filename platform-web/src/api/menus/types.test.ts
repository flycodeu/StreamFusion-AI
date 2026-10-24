import { describe, expect, it } from 'vitest'
import { parseMenuNode } from './types'

const directory = {
  id: '1',
  parentId: null,
  name: '系统管理',
  type: 'DIRECTORY',
  icon: null,
  sortOrder: 0,
  visible: true,
  enabled: true,
  version: '0',
  children: [],
}

describe('menu layer response', () => {
  it('keeps a lazy directory expandable without rendering its children', () => {
    const node = parseMenuNode({ ...directory, hasChildren: true })
    expect(node.children).toEqual([])
    expect(node.hasChildren).toBe(true)
  })

  it('preserves the full-tree response from an older server', () => {
    const node = parseMenuNode({
      ...directory,
      children: [{ ...directory, id: '2', parentId: '1' }],
    })
    expect(node.hasChildren).toBe(true)
    expect(node.children[0]?.hasChildren).toBe(false)
  })

  it('rejects a string child indicator instead of making a leaf expandable', () => {
    expect(() => parseMenuNode({ ...directory, hasChildren: 'false' })).toThrow('boolean')
  })
})
