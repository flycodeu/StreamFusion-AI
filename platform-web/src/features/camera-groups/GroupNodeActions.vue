<script setup lang="ts">
import { ref } from 'vue'
import { ElDropdown, ElDropdownMenu, ElDropdownItem } from 'element-plus'
import type { CameraGroup } from '../../api/camera-groups/types'

defineProps<{ group: CameraGroup; disabled?: boolean }>()
const emit = defineEmits<{ action: [action: 'create' | 'edit' | 'delete'] }>()
const menu = ref<InstanceType<typeof ElDropdown>>()
</script>
<template>
  <ElDropdown
    ref="menu"
    class="group-node-actions"
    trigger="contextmenu"
    placement="bottom-end"
    :disabled="disabled"
    @command="emit('action', $event)"
  >
    <span class="group-node-trigger">
      <slot />
      <button
        type="button"
        class="group-more"
        :aria-label="`${group.name}的分组操作`"
        aria-haspopup="menu"
        :disabled="disabled"
        @click.stop="menu?.handleOpen()"
        @keydown.stop
      >
        ⋯
      </button>
    </span>
    <template #dropdown>
      <ElDropdownMenu>
        <ElDropdownItem disabled>{{ group.name }}</ElDropdownItem>
        <ElDropdownItem command="create">新建子分组</ElDropdownItem>
        <ElDropdownItem command="edit">编辑分组</ElDropdownItem>
        <ElDropdownItem command="delete" divided class="group-delete-action"
          >删除分组</ElDropdownItem
        >
      </ElDropdownMenu>
    </template>
  </ElDropdown>
</template>
<style scoped>
.group-node-actions,
.group-node-trigger {
  display: flex;
  flex: 1;
  align-items: center;
  min-width: 0;
  height: 100%;
}
.group-more {
  flex-shrink: 0;
  width: 30px;
  height: 30px;
  border: 0;
  border-radius: 4px;
  background: transparent;
  color: var(--text-secondary);
  font-size: 22px;
  cursor: pointer;
}
.group-more:hover,
.group-more:focus-visible {
  color: var(--el-color-primary);
  background: var(--el-color-primary-light-9);
}
.group-delete-action {
  color: var(--el-color-danger);
}
@media (pointer: coarse) {
  .group-more {
    width: 44px;
    height: 44px;
  }
}
</style>
