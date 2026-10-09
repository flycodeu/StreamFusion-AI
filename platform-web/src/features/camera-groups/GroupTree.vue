<script setup lang="ts">
import { computed, nextTick, ref, watch } from 'vue'
import { ElButton, ElInput, ElTree } from 'element-plus'
import type { CameraGroup } from '../../api/camera-groups/types'
import { groupTree, type GroupNode } from './tree'
import GroupNodeActions from './GroupNodeActions.vue'

const props = withDefaults(
  defineProps<{
    groups: CameraGroup[]
    showAll?: boolean
    showPending?: boolean
    pending?: boolean
    disabled?: boolean
    counts?: boolean
    manageable?: boolean
    selection?: boolean
    placements?: { groupId: string | null; channelCount: number }[]
  }>(),
  { showAll: true, counts: true },
)
const model = defineModel<string | null>({ required: true })
const emit = defineEmits<{
  pending: []
  all: []
  action: [action: 'create' | 'edit' | 'delete', group: CameraGroup]
}>()
const term = ref(''),
  tree = ref<InstanceType<typeof ElTree>>()
const nodes = computed(() => groupTree(props.groups))
const expanded = computed(() => {
  const keys = nodes.value.flatMap((n) => [n.groupId, ...n.children.map((c) => c.groupId)])
  for (const id of [model.value, ...(props.placements ?? []).map((p) => p.groupId)]) {
    let group = props.groups.find((g) => g.groupId === id)
    for (let depth = 0; group && depth < 16; depth++) {
      keys.push(group.groupId)
      group = props.groups.find((g) => g.groupId === group?.parentId)
    }
  }
  return keys
})
watch([term, nodes, model], async () => {
  await nextTick()
  tree.value?.filter(term.value.trim())
  tree.value?.setCurrentKey(props.pending ? undefined : (model.value ?? undefined))
})
function select(node: GroupNode) {
  if (!props.disabled) model.value = node.groupId
}
</script>
<template>
  <div class="group-tree" :class="{ 'is-disabled': disabled }">
    <ElInput
      v-model="term"
      aria-label="搜索分组"
      placeholder="搜索分组"
      clearable
      :disabled="disabled"
    />
    <div v-if="showAll || showPending" class="group-shortcuts">
      <ElButton
        v-if="showAll"
        text
        :bg="!model && !pending"
        :type="!model && !pending ? 'primary' : 'default'"
        :disabled="disabled"
        @click="emit('all')"
        >全部相机</ElButton
      >
      <ElButton
        v-if="showPending"
        text
        :bg="pending"
        :type="pending ? 'primary' : 'default'"
        :disabled="disabled"
        @click="emit('pending')"
        >待归档</ElButton
      >
    </div>
    <ElTree
      ref="tree"
      :data="nodes"
      node-key="groupId"
      :props="{ label: 'name', children: 'children' }"
      :current-node-key="pending ? undefined : model || undefined"
      :default-expanded-keys="expanded"
      :expand-on-click-node="false"
      highlight-current
      empty-text="暂无可见分组"
      :filter-node-method="
        (value: string, data) =>
          !value || String(data.path).toLowerCase().includes(value.toLowerCase())
      "
      @node-click="select"
    >
      <template #default="{ data }">
        <GroupNodeActions
          v-if="manageable"
          :group="data"
          :disabled="disabled"
          @action="(action) => emit('action', action, data)"
        >
          <span class="group-node" :title="data.path"
            ><span class="group-node-name">{{ data.name }}</span>
            <small v-if="counts">{{ data.visibleCameraCount }} 通道</small></span
          >
        </GroupNodeActions>
        <span v-else class="group-node" :title="data.path"
          ><span
            v-if="selection"
            class="group-choice"
            :class="{ 'is-selected': model === data.groupId }"
            aria-hidden="true"
            >{{ model === data.groupId ? '✓' : '' }}</span
          ><span class="group-node-name">{{ data.name }}</span
          ><small
            v-if="placements?.some((p) => p.groupId === data.groupId)"
            class="current-placement"
            >当前 ·
            {{ placements.find((p) => p.groupId === data.groupId)?.channelCount }} 通道</small
          >
          <small v-if="counts">{{ data.visibleCameraCount }} 通道</small></span
        >
      </template>
    </ElTree>
  </div>
</template>
<style scoped>
.group-tree {
  min-width: 0;
}
.group-tree :deep(.el-tree) {
  margin-top: 12px;
  background: transparent;
  --el-tree-node-content-height: 38px;
}
.group-tree :deep(.el-tree-node__content) {
  border-radius: 4px;
}
.group-node {
  display: flex;
  align-items: center;
  gap: 8px;
  min-width: 0;
  flex: 1;
  padding-right: 8px;
}
.group-node-name {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  flex: 1;
}
.group-node small {
  color: var(--text-secondary);
  white-space: nowrap;
  font-size: 12px;
}
.group-choice {
  flex-shrink: 0;
  display: grid;
  place-items: center;
  width: 16px;
  height: 16px;
  border: 1px solid var(--el-border-color);
  border-radius: 3px;
  font-size: 12px;
}
.group-choice.is-selected {
  color: white;
  background: var(--el-color-primary);
  border-color: var(--el-color-primary);
}
.group-node .current-placement {
  color: var(--el-color-primary);
}
.group-shortcuts {
  display: flex;
  flex-wrap: wrap;
  gap: 4px;
  margin-top: 12px;
}
.group-shortcuts :deep(.el-button + .el-button) {
  margin-left: 0;
}
.is-disabled :deep(.el-tree) {
  pointer-events: none;
  opacity: 0.65;
}
@media (pointer: coarse) {
  .group-tree :deep(.el-tree) {
    --el-tree-node-content-height: 44px;
  }
}
</style>
