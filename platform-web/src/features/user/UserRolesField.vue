<script setup lang="ts">
import { computed } from 'vue'
import { ElOption, ElSelect } from 'element-plus'
import type { RoleOption } from '../../api/roles/types'
import { protectedRoleSelection, roleChoices } from './roleSelection'

const props = defineProps<{
  modelValue: string[]
  options: RoleOption[]
  assigned: RoleOption[]
  protectOwnSuperAdmin: boolean
}>()
const emit = defineEmits<{ 'update:modelValue': [value: string[]] }>()
const choices = computed(() =>
  roleChoices(props.options, props.assigned, props.protectOwnSuperAdmin),
)
const selected = computed({
  get: () => protectedRoleSelection(props.modelValue, choices.value),
  set: (ids: string[]) => emit('update:modelValue', protectedRoleSelection(ids, choices.value)),
})
</script>

<template>
  <ElSelect
    v-model="selected"
    class="user-roles-select"
    multiple
    filterable
    clearable
    collapse-tags
    collapse-tags-tooltip
    :max-collapse-tags="3"
    placeholder="请选择角色"
    aria-label="用户角色"
  >
    <ElOption
      v-for="role in choices"
      :key="role.id"
      :value="role.id"
      :label="`${role.name}（${role.code}）${role.available ? '' : ' · 已有分配'}`"
      :disabled="role.locked"
    />
  </ElSelect>
</template>

<style scoped>
.user-roles-select {
  width: 100%;
}
</style>
