import type { CameraGroup } from '../../api/camera-groups/types'

export interface GroupNode extends CameraGroup {
  path: string
  children: GroupNode[]
}

export function groupTree(groups: CameraGroup[], excludeId?: string): GroupNode[] {
  const nodes = new Map(
    groups.map((group) => [group.groupId, { ...group, path: '', children: [] as GroupNode[] }]),
  )
  const roots: GroupNode[] = []
  for (const node of nodes.values()) {
    const parent = node.parentId ? nodes.get(node.parentId) : null
    if (parent) parent.children.push(node)
    else roots.push(node)
  }
  const prepare = (items: GroupNode[], prefix = '', depth = 0): GroupNode[] => {
    if (depth >= 16) return []
    return items
      .filter((node) => node.groupId !== excludeId)
      .sort((a, b) => a.sortOrder - b.sortOrder || (BigInt(a.groupId) < BigInt(b.groupId) ? -1 : 1))
      .map((node) => {
        node.path = prefix ? `${prefix} / ${node.name}` : node.name
        node.children = prepare(node.children, node.path, depth + 1)
        return node
      })
  }
  return prepare(roots)
}
