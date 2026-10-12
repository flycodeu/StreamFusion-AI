import type { CameraGroup } from '../../api/camera-groups/types'
import type { PreviewChannel } from '../../api/preview/types'
import { groupTree } from '../camera-groups/tree'
import { groupCatalog } from './model'

export type PreviewCatalogNode = {
  key: string
  label: string
  children: PreviewCatalogNode[]
} & (
  | { kind: 'group' }
  | { kind: 'device'; channels: PreviewChannel[] }
  | { kind: 'channel'; channel: PreviewChannel }
)

/** Group IDs preserve hierarchy even when names repeat or contain a slash. */
export function buildPreviewTree(
  channels: PreviewChannel[],
  groups: CameraGroup[],
): PreviewCatalogNode[] {
  const byGroup = new Map<string, PreviewCatalogNode>()
  const convert = (items: ReturnType<typeof groupTree>): PreviewCatalogNode[] =>
    items.map((group) => {
      const node: PreviewCatalogNode = {
        key: `group:${group.groupId}`,
        label: group.name,
        kind: 'group',
        children: convert(group.children),
      }
      byGroup.set(group.groupId, node)
      return node
    })
  const roots = convert(groupTree(groups))
  const fallback = new Map<string, PreviewCatalogNode>()
  for (const device of groupCatalog(channels).flatMap((group) => group.devices)) {
    const ids = new Set(device.channels.map((channel) => channel.groupId))
    const groupId = ids.size === 1 ? [...ids][0] : undefined
    let parent = groupId ? byGroup.get(groupId) : undefined
    if (!parent) {
      const key = groupId === undefined ? 'mixed' : groupId || 'ungrouped'
      parent = fallback.get(key)
      if (!parent) {
        parent = {
          key: `fallback:${key}`,
          label: groupId === undefined ? '多个分组' : device.channels[0]?.groupPath || '未分组',
          kind: 'group',
          children: [],
        }
        fallback.set(key, parent)
        roots.push(parent)
      }
    }
    parent.children.push({
      key: `device:${device.key}`,
      label: device.name,
      kind: 'device',
      channels: device.channels,
      children:
        device.channels.length > 1
          ? device.channels.map((channel) => ({
              key: `channel:${channel.cameraId}`,
              label: channel.name,
              kind: 'channel' as const,
              channel,
              children: [],
            }))
          : [],
    })
  }
  const prune = (items: PreviewCatalogNode[]): PreviewCatalogNode[] =>
    items.filter((node) => {
      node.children = prune(node.children)
      return node.kind !== 'group' || node.children.length > 0
    })
  return prune(roots)
}
