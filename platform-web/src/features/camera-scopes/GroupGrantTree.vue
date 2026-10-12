<script setup lang="ts">
import { computed, nextTick, onMounted, ref, watch } from 'vue'
import { ElButton, ElInput, ElTree, vLoading } from 'element-plus'
import type { CameraGroup } from '../../api/camera-groups/types'
import { getScopeGroupTree } from '../../api/camera-scopes/api'
import RequestError from '../../components/feedback/RequestError.vue'
import { usePageScope } from '../../composables/usePageScope'
import { groupTree, type GroupNode } from '../camera-groups/tree'

const props = defineProps<{ disabled: boolean }>()
const selected = defineModel<string[]>({ required: true })
const emit = defineEmits<{ label: [id: string, label: string] }>()
const groups = ref<CameraGroup[]>([]),
  loading = ref(false),
  error = ref<unknown>(null),
  term = ref(''),
  tree = ref<InstanceType<typeof ElTree>>()
const captureScope = usePageScope()
const nodes = computed(() => groupTree(groups.value))
const selectedSet = computed(() => new Set(selected.value))
const expanded = computed(() => {
  const keys = new Set(nodes.value.map((node) => node.groupId))
  const byId = new Map(groups.value.map((row) => [row.groupId, row]))
  for (const id of selected.value) {
    let node = byId.get(id)
    for (let depth = 0; node && depth < 16; depth++) {
      keys.add(node.groupId)
      node = node.parentId ? byId.get(node.parentId) : undefined
    }
  }
  return [...keys]
})
let sequence = 0

async function load() {
  const current = ++sequence,
    active = captureScope()
  loading.value = true
  error.value = null
  try {
    const result = await getScopeGroupTree()
    if (!active() || current !== sequence) return
    groups.value = result
    const publish = (items: GroupNode[]) => {
      for (const node of items) {
        emit('label', node.groupId, node.path)
        publish(node.children)
      }
    }
    publish(nodes.value)
  } catch (cause) {
    if (active() && current === sequence) error.value = cause
  } finally {
    if (active() && current === sequence) loading.value = false
  }
}
function toggle(data: GroupNode, state: { checkedKeys: (string | number)[] }) {
  if (props.disabled || loading.value) return
  const ids = new Set(selected.value)
  if (state.checkedKeys.includes(data.groupId)) {
    if (ids.size >= 200 && !ids.has(data.groupId)) {
      tree.value?.setCheckedKeys(selected.value)
      return
    }
    ids.add(data.groupId)
  } else ids.delete(data.groupId)
  selected.value = [...ids]
}
watch([selected, nodes], async () => {
  await nextTick()
  tree.value?.setCheckedKeys(selected.value)
  tree.value?.filter(term.value.trim())
})
watch(term, (value) => tree.value?.filter(value.trim()))
onMounted(load)
</script>

<template>
  <div class="group-grant-tree">
    <ElInput v-model="term" clearable placeholder="搜索分组" aria-label="搜索授权分组" />
    <RequestError :error="error" />
    <ElButton v-if="error" link :loading="loading" @click="load">重新读取分组</ElButton>
    <div v-loading="loading" class="group-grant-tree__body">
      <ElTree
        ref="tree"
        :data="nodes"
        node-key="groupId"
        show-checkbox
        check-strictly
        :indent="16"
        :default-expanded-keys="expanded"
        :default-checked-keys="selected"
        :props="{
          label: 'name',
          children: 'children',
          disabled: (data) =>
            disabled ||
            loading ||
            (selected.length >= 200 && !selectedSet.has(String(data.groupId))),
        }"
        :filter-node-method="
          (value: string, data) =>
            !value || String(data.path).toLocaleLowerCase().includes(value.toLocaleLowerCase())
        "
        empty-text="暂无可授权分组"
        aria-label="分组授权树"
        @check="toggle"
      >
        <template #default="{ data }">
          <span class="group-grant-node" :title="data.path">
            <span>{{ data.name }}</span>
            <small>{{ data.visibleCameraCount }} 通道</small>
          </span>
        </template>
      </ElTree>
    </div>
  </div>
</template>

<style scoped>
.group-grant-tree__body {
  min-height: 140px;
  max-height: min(48dvh, 440px);
  margin-top: 12px;
  overflow: auto;
}
.group-grant-tree :deep(.el-tree) {
  background: transparent;
  --el-tree-node-content-height: 38px;
}
.group-grant-tree :deep(.el-tree-node__content) {
  padding-right: 8px;
  border-radius: 4px;
}
.group-grant-node {
  display: flex;
  align-items: center;
  min-width: 0;
  flex: 1;
  gap: 8px;
}
.group-grant-node > span {
  flex: 1;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.group-grant-node small {
  flex-shrink: 0;
  color: var(--text-secondary);
  font-size: 12px;
}
</style>
