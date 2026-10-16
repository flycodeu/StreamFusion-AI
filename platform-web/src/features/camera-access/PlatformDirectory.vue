<script setup lang="ts">
import { ref } from 'vue'
import { ElButton, ElTree } from 'element-plus'
import type { TreeInstance } from 'element-plus'
import type Node from 'element-plus/es/components/tree/src/model/node'
import type { LoadFunction } from 'element-plus/es/components/tree/src/tree.type'
import { getPlatformRegions } from '../../api/camera-access/api'
import type { PlatformRegion } from '../../api/camera-access/api'
import type { AccessConnection } from '../../api/camera-access/types'
import RequestError from '../../components/feedback/RequestError.vue'
import { usePageScope } from '../../composables/usePageScope'

const props = defineProps<{ connection: AccessConnection; disabled: boolean; selected?: string }>()
const emit = defineEmits<{ select: [region: PlatformRegion] }>()
interface Entry extends PlatformRegion {
  more?: boolean
  nextPage?: number
  leaf?: boolean
}
const tree = ref<TreeInstance>()
const error = ref<unknown>(null)
const treeKey = ref(0)
const loadingMore = ref(false)
const capture = usePageScope()
let loaded = 0
let generation = 0
async function read(parentCode?: string, page = 1): Promise<Entry[]> {
  const current = generation
  if (loaded >= 2000) throw new Error('本次已展开 2000 个区域，请刷新目录后按需展开。')
  const result = await getPlatformRegions({
    connection: props.connection,
    parentCode,
    page,
    size: 100,
  })
  if (current !== generation) return []
  if (loaded + result.items.length > 2000)
    throw new Error('本次已展开接近 2000 个区域，请刷新目录后按需展开。')
  loaded += result.items.length
  const entries: Entry[] = [...result.items]
  if (parentCode && page * result.size < result.total)
    entries.push({
      indexCode: `more:${parentCode}:${page}`,
      parentIndexCode: parentCode,
      name: '加载更多区域',
      more: true,
      nextPage: page + 1,
      leaf: true,
    })
  return entries
}
const load: LoadFunction = async (node, resolve, reject) => {
  const active = capture()
  const current = generation
  error.value = null
  try {
    const rows = await read(node.level === 0 ? undefined : node.data.indexCode)
    if (active() && current === generation) resolve(rows)
  } catch (cause) {
    if (active() && current === generation) {
      error.value = cause
      reject()
    }
  }
}
async function select(entry: Entry, node: Node) {
  if (props.disabled || loadingMore.value) return
  if (!entry.more) {
    emit('select', entry)
    return
  }
  const active = capture()
  const current = generation
  loadingMore.value = true
  error.value = null
  try {
    const rows = await read(entry.parentIndexCode!, entry.nextPage!)
    if (!active() || current !== generation) return
    tree.value?.remove(node)
    for (const row of rows) tree.value?.append(row, node.parent!)
  } catch (cause) {
    if (active()) error.value = cause
  } finally {
    if (active()) loadingMore.value = false
  }
}
function reset() {
  generation++
  loaded = 0
  error.value = null
  treeKey.value++
}
</script>

<template>
  <aside class="platform-directory" aria-label="平台区域目录">
    <header>
      <strong>平台区域</strong
      ><ElButton link :disabled="disabled || loadingMore" @click="reset">刷新目录</ElButton>
    </header>
    <RequestError :error="error" />
    <ElTree
      ref="tree"
      :key="treeKey"
      node-key="indexCode"
      lazy
      :load="load"
      :props="{ label: 'name', isLeaf: 'leaf' }"
      :default-expand-all="false"
      :expand-on-click-node="false"
      :highlight-current="true"
      :current-node-key="selected"
      :class="{ 'is-disabled': disabled }"
      @node-click="select"
    >
      <template #default="{ data }"
        ><span :class="{ 'load-more': data.more }" :title="data.name">{{
          data.name
        }}</span></template
      >
    </ElTree>
  </aside>
</template>

<style scoped>
.platform-directory {
  min-width: 0;
  border-right: 1px solid var(--el-border-color-lighter);
  padding-right: 16px;
}
header {
  display: flex;
  justify-content: space-between;
  align-items: center;
  gap: 8px;
  margin-bottom: 12px;
}
.el-tree {
  max-height: 55vh;
  overflow: auto;
}
.el-tree.is-disabled {
  pointer-events: none;
  opacity: 0.65;
}
.el-tree span {
  overflow: hidden;
  text-overflow: ellipsis;
}
.load-more {
  color: var(--el-color-primary);
}
@media (max-width: 760px) {
  .platform-directory {
    border-right: 0;
    border-bottom: 1px solid var(--el-border-color-lighter);
    padding: 0 0 16px;
  }
  .el-tree {
    max-height: 200px;
  }
}
</style>
