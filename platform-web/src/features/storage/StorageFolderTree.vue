<script setup lang="ts">
import { computed, onBeforeUnmount, ref, watch } from 'vue'
import { ElTree, ElIcon, type TreeInstance } from 'element-plus'
import type Node from 'element-plus/es/components/tree/src/model/node'
import { Folder, MoreFilled } from '@element-plus/icons-vue'
import * as api from '../../api/storage/api'
import type { StorageInstance, StorageScope, ObjectPage } from '../../api/storage/types'
import { folderName } from './file-browser'

const props = defineProps<{
  instance: StorageInstance
  scopeId: string
  prefix: string
  disabled?: boolean
}>()
const emit = defineEmits<{
  navigate: [scopeId: string, prefix: string]
  contextMenu: [event: InstanceType<typeof globalThis.MouseEvent>, scopeId: string, prefix: string]
  accessError: [error: unknown]
  retry: []
}>()
interface FolderNode {
  key: string
  label: string
  scopeId: string
  prefix: string
  more?: boolean
  cursor?: string
  parentKey?: string
}
const tree = ref<TreeInstance>(),
  error = ref<unknown>(null),
  loadingMore = ref(false)
watch(error, (value) => emit('accessError', value))
let disposed = false
onBeforeUnmount(() => {
  disposed = true
})
const expanded = computed(() => [
  key(props.scopeId, ''),
  ...props.prefix
    .split('/')
    .filter(Boolean)
    .map((_, i, all) => key(props.scopeId, all.slice(0, i + 1).join('/') + '/')),
])
function key(scopeId: string, prefix: string) {
  return `${scopeId}:${prefix}`
}
function children(scope: StorageScope, prefix: string, value: ObjectPage): FolderNode[] {
  const nodes: FolderNode[] = value.commonPrefixes.map((full) => ({
    key: key(scope.scopeId, full.slice(scope.prefix.length)),
    label: folderName(full),
    scopeId: scope.scopeId,
    prefix: full.slice(scope.prefix.length),
  }))
  if (value.nextCursor)
    nodes.push({
      key: `${key(scope.scopeId, prefix)}:more`,
      label: '加载更多目录',
      scopeId: scope.scopeId,
      prefix,
      more: true,
      cursor: value.nextCursor,
      parentKey: key(scope.scopeId, prefix),
    })
  return nodes
}
async function load(node: Node, resolve: (nodes: FolderNode[]) => void, reject?: () => void) {
  if (node.level === 0) {
    resolve(
      props.instance.scopes.map((scope) => ({
        key: key(scope.scopeId, ''),
        label:
          scope.displayName ||
          scope.bucket + (scope.prefix ? ` / ${folderName(scope.prefix)}` : ''),
        scopeId: scope.scopeId,
        prefix: '',
      })),
    )
    return
  }
  const item = node.data as FolderNode
  const scope = props.instance.scopes.find((row) => row.scopeId === item.scopeId)
  if (!scope) {
    resolve([])
    return
  }
  try {
    const value = await api.listObjects(item.scopeId, item.prefix)
    if (!disposed) {
      error.value = null
      resolve(children(scope, item.prefix, value))
    }
  } catch (e) {
    if (!disposed) {
      error.value = e
      if (reject) reject()
      else resolve([])
    }
  }
}
async function select(item: FolderNode) {
  if (props.disabled) return
  if (!item.more) {
    emit('navigate', item.scopeId, item.prefix)
    return
  }
  if (loadingMore.value) return
  const scope = props.instance.scopes.find((row) => row.scopeId === item.scopeId)
  if (!scope) return
  loadingMore.value = true
  try {
    const value = await api.listObjects(item.scopeId, item.prefix, item.cursor)
    if (disposed) return
    const parent = tree.value?.getNode(item.parentKey!)
    if (parent) {
      const old = parent.childNodes
        .map((child) => child.data as FolderNode)
        .filter((child) => !child.more)
      const merged = [...old, ...children(scope, item.prefix, value)]
      tree.value?.updateKeyChildren(item.parentKey!, [
        ...new Map(merged.map((child) => [child.key, child])).values(),
      ])
    }
    error.value = null
  } catch (e) {
    if (!disposed) error.value = e
  } finally {
    if (!disposed) loadingMore.value = false
  }
}
function contextMenu(event: InstanceType<typeof globalThis.Event>, item: FolderNode) {
  event.preventDefault()
  if (event instanceof globalThis.MouseEvent && !props.disabled)
    emit('contextMenu', event, item.scopeId, item.more ? '' : item.prefix)
}
</script>

<template>
  <nav class="folder-tree" aria-label="文件夹目录">
    <span class="tree-heading">存储目录</span>
    <ElTree
      ref="tree"
      node-key="key"
      lazy
      :load="load"
      :props="{ label: 'label', isLeaf: 'more' }"
      :default-expanded-keys="expanded"
      :current-node-key="key(scopeId, prefix)"
      highlight-current
      :expand-on-click-node="false"
      @node-click="select"
      @node-contextmenu="contextMenu"
    >
      <template #default="{ data }"
        ><span class="tree-node" :title="data.label"
          ><ElIcon><MoreFilled v-if="data.more" /><Folder v-else /></ElIcon
          ><span>{{ data.more && loadingMore ? '正在加载…' : data.label }}</span></span
        ></template
      >
    </ElTree>
    <span v-if="error" class="tree-unavailable"
      ><i />目录暂不可用<button type="button" :disabled="disabled" @click="emit('retry')">
        重试
      </button></span
    >
  </nav>
</template>

<style scoped>
.folder-tree {
  min-width: 0;
  overflow: auto;
  padding: 16px 12px;
  border-right: 1px solid #e2e8f0;
  background: #fafbfc;
}
.tree-heading {
  display: block;
  color: #475569;
  font-size: 12px;
  font-weight: 600;
  padding: 0 8px 10px;
}
.tree-unavailable {
  display: flex;
  align-items: center;
  gap: 7px;
  margin: 14px 8px 0;
  color: #64748b;
  font-size: 12px;
}
.tree-unavailable i {
  width: 6px;
  height: 6px;
  border-radius: 50%;
  background: var(--el-color-warning);
}
.tree-unavailable button {
  border: 0;
  padding: 0;
  margin-left: auto;
  background: none;
  font: inherit;
  color: var(--el-color-primary);
  cursor: pointer;
}
.tree-node {
  display: flex;
  align-items: center;
  gap: 8px;
  min-width: 0;
  font-size: 13px;
}
.tree-node > span {
  white-space: nowrap;
}
.tree-node .el-icon {
  flex-shrink: 0;
  color: #f59e0b;
}
.folder-tree :deep(.el-tree) {
  background: transparent;
}
:deep(.el-tree-node__content) {
  height: 36px;
  width: max-content;
  min-width: 100%;
  border-radius: 6px;
  margin-bottom: 1px;
}
:deep(.is-current > .el-tree-node__content) {
  background: #eff6ff;
  color: #1d4ed8;
  font-weight: 500;
}
@media (max-width: 700px) {
  .folder-tree {
    border-right: 0;
    border-bottom: 1px solid #e2e8f0;
    padding: 12px;
    max-height: 220px;
    overflow: auto;
  }
}
</style>
