<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import {
  ElButton,
  ElCheckbox,
  ElDialog,
  ElInput,
  ElTable,
  ElTableColumn,
  ElTag,
} from 'element-plus'
import type { RoleOption } from '../../api/roles/types'
import RequestError from '../../components/feedback/RequestError.vue'
import {
  filterRoleChoices,
  protectedRoleSelection,
  roleChoices,
  updateVisibleRoleSelection,
} from './roleSelection'

const props = defineProps<{
  modelValue: boolean
  username: string
  options: RoleOption[]
  assigned: RoleOption[]
  selectedIds: string[]
  protectOwnSuperAdmin: boolean
  saving: boolean
  error: unknown
}>()
const emit = defineEmits<{
  'update:modelValue': [value: boolean]
  'update:selectedIds': [value: string[]]
  save: [value: string[]]
}>()
const keyword = ref('')
const choices = computed(() =>
  roleChoices(props.options, props.assigned, props.protectOwnSuperAdmin),
)
const selected = computed(() => protectedRoleSelection(props.selectedIds, choices.value))
const selectedSet = computed(() => new Set(selected.value))
const filtered = computed(() => filterRoleChoices(choices.value, keyword.value))
const selectable = computed(() => filtered.value.filter((role) => !role.locked))
const checkedCount = computed(
  () => selectable.value.filter((role) => selectedSet.value.has(role.id)).length,
)
const allChecked = computed(
  () => selectable.value.length > 0 && checkedCount.value === selectable.value.length,
)
const partiallyChecked = computed(
  () => checkedCount.value > 0 && checkedCount.value < selectable.value.length,
)

watch(
  () => props.modelValue,
  (visible) => {
    if (visible) keyword.value = ''
  },
)

function selectRole(id: string, checked: boolean): void {
  const role = choices.value.find((choice) => choice.id === id)
  if (props.saving || !role || role.locked) return
  emit(
    'update:selectedIds',
    updateVisibleRoleSelection(selected.value, [role], checked ? [role.id] : []),
  )
}
function selectFiltered(checked: boolean): void {
  if (props.saving) return
  emit(
    'update:selectedIds',
    updateVisibleRoleSelection(
      selected.value,
      filtered.value,
      checked ? filtered.value.map((role) => role.id) : [],
    ),
  )
}
</script>

<template>
  <ElDialog
    :model-value="modelValue"
    :title="`分配角色 · ${username}`"
    width="760px"
    destroy-on-close
    :close-on-click-modal="!saving"
    :close-on-press-escape="!saving"
    :show-close="!saving"
    @update:model-value="emit('update:modelValue', $event)"
  >
    <RequestError :error="error" />
    <div class="role-toolbar">
      <ElInput
        v-model="keyword"
        placeholder="搜索角色名称或编码"
        clearable
        :disabled="saving"
        aria-label="搜索角色"
      />
      <span class="selection-count">已选 {{ selected.length }} 项</span>
    </div>
    <ElTable :data="filtered" row-key="id" border :max-height="390" empty-text="暂无匹配角色">
      <ElTableColumn width="56" align="center" :resizable="false">
        <template #header>
          <ElCheckbox
            :model-value="allChecked"
            :indeterminate="partiallyChecked"
            :disabled="saving || !selectable.length"
            aria-label="选择当前搜索结果"
            @change="selectFiltered(Boolean($event))"
          />
        </template>
        <template #default="{ row }">
          <ElCheckbox
            :model-value="selectedSet.has(row.id)"
            :disabled="saving || row.locked"
            :aria-label="`选择角色 ${row.name}`"
            @change="selectRole(row.id, Boolean($event))"
          />
        </template>
      </ElTableColumn>
      <ElTableColumn prop="name" label="角色名称" min-width="180" show-overflow-tooltip />
      <ElTableColumn prop="code" label="角色编码" min-width="190" show-overflow-tooltip />
      <ElTableColumn label="状态" width="130">
        <template #default="{ row }">
          <ElTag
            :type="row.locked ? 'warning' : row.available ? 'success' : 'info'"
            effect="plain"
            >{{ row.locked ? '本人保护' : row.available ? '可分配' : '仅已有分配' }}</ElTag
          >
        </template>
      </ElTableColumn>
    </ElTable>
    <template #footer>
      <ElButton :disabled="saving" @click="emit('update:modelValue', false)">取消</ElButton>
      <ElButton type="primary" :loading="saving" @click="emit('save', selected)">保存</ElButton>
    </template>
  </ElDialog>
</template>

<style scoped>
.role-toolbar {
  display: flex;
  align-items: center;
  gap: 16px;
  margin-bottom: 16px;
}
.role-toolbar :deep(.el-input) {
  flex: 1;
  min-width: 0;
}
.selection-count {
  flex-shrink: 0;
  color: var(--el-text-color-regular);
  font-size: 14px;
}
</style>
