<script setup lang="ts">
import { computed, nextTick, ref, watch } from 'vue'
import { ElTree } from 'element-plus'
import { Folder, VideoCamera, VideoPlay } from '@element-plus/icons-vue'
import type { PreviewChannel } from '../../api/preview/types'
import type { CameraGroup } from '../../api/camera-groups/types'
import { buildPreviewTree, type PreviewCatalogNode } from './catalog-tree'

const props = defineProps<{
  channels: PreviewChannel[]
  groups: CameraGroup[]
  searching: boolean
}>()
const emit = defineEmits<{
  device: [channels: PreviewChannel[]]
  channel: [channel: PreviewChannel]
}>()
const tree = ref<InstanceType<typeof ElTree>>()
const nodes = computed(() => buildPreviewTree(props.channels, props.groups))
const expanded = new Set<string>()
let synchronizing = false
interface ExpandableNode {
  expanded: boolean
  expand(): void
  collapse(): void
}
function select(data: PreviewCatalogNode, node: ExpandableNode) {
  if (data.kind === 'group') {
    const open = !node.expanded
    if (open) node.expand()
    else node.collapse()
    remember(data, open)
  } else if (data.kind === 'device') emit('device', data.channels)
  else emit('channel', data.channel)
}
function onExpand(data: PreviewCatalogNode) {
  remember(data, true)
}
function onCollapse(data: PreviewCatalogNode) {
  remember(data, false)
}
function remember(data: PreviewCatalogNode, open: boolean) {
  if (synchronizing || props.searching) return
  if (open) expanded.add(data.key)
  else expanded.delete(data.key)
}
watch(
  [nodes, () => props.searching],
  async () => {
    await nextTick()
    synchronizing = true
    const sync = (items: PreviewCatalogNode[]) => {
      for (const item of items) {
        if (!item.children.length) continue
        const node = tree.value?.getNode(item.key)
        if (props.searching || expanded.has(item.key)) node?.expand()
        else node?.collapse()
        sync(item.children)
      }
    }
    sync(nodes.value)
    synchronizing = false
  },
  { immediate: true },
)
</script>

<template>
  <ElTree
    ref="tree"
    class="preview-camera-tree"
    :data="nodes"
    node-key="key"
    :props="{ label: 'label', children: 'children' }"
    :expand-on-click-node="false"
    highlight-current
    empty-text="暂无可见相机"
    aria-label="相机分组树"
    @node-click="select"
    @node-expand="onExpand"
    @node-collapse="onCollapse"
  >
    <template #default="{ data }">
      <span class="catalog-node" :class="`catalog-${data.kind}`" :title="data.label">
        <component
          :is="data.kind === 'group' ? Folder : data.kind === 'device' ? VideoCamera : VideoPlay"
          class="catalog-icon"
        />
        <span class="catalog-label">{{ data.label }}</span>
        <small v-if="data.kind === 'device'">{{ data.channels.length }} 通道</small>
        <small v-if="data.kind === 'channel' && data.channel.lifecycle !== 'ENABLED'">未启用</small>
      </span>
    </template>
  </ElTree>
</template>

<style scoped>
.preview-camera-tree {
  background: transparent;
  color: var(--preview-ink);
  --el-tree-node-content-height: 38px;
  --el-tree-node-hover-bg-color: var(--preview-hover);
}
.preview-camera-tree :deep(.el-tree-node__content) {
  border-radius: 5px;
  padding-right: 8px;
}
.preview-camera-tree :deep(.el-tree-node__expand-icon) {
  color: var(--preview-muted);
}
.preview-camera-tree :deep(.el-tree-node__expand-icon.is-leaf) {
  color: transparent;
}
.preview-camera-tree :deep(.is-current > .el-tree-node__content) {
  background: var(--preview-hover);
}
.catalog-node {
  display: flex;
  align-items: center;
  gap: 8px;
  flex: 1;
  min-width: 0;
}
.catalog-label {
  flex: 1;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  font-size: 13px;
}
.catalog-group .catalog-label {
  font-weight: 600;
}
.catalog-icon {
  width: 16px;
  height: 16px;
  flex-shrink: 0;
  color: var(--preview-muted);
}
.catalog-device .catalog-icon {
  color: var(--preview-accent);
}
.catalog-node small {
  color: var(--preview-muted);
  font-size: 11px;
  white-space: nowrap;
}
@media (pointer: coarse) {
  .preview-camera-tree {
    --el-tree-node-content-height: 44px;
  }
}
</style>
