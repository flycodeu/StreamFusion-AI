<script setup lang="ts">
import { ElCheckbox, ElPopover, ElButton } from 'element-plus'
defineProps<{ modelValue: string[]; options: { key: string; label: string }[] }>()
const emit = defineEmits<{ 'update:modelValue': [value: string[]] }>()
function toggle(key: string, checked: boolean, current: string[]): void {
  emit(
    'update:modelValue',
    checked ? [...new Set([...current, key])] : current.filter((item) => item !== key),
  )
}
</script>

<template>
  <ElPopover trigger="click" placement="bottom-end" :width="170">
    <template #reference><ElButton>显示列</ElButton></template>
    <div class="column-list">
      <ElCheckbox
        v-for="option in options"
        :key="option.key"
        :model-value="modelValue.includes(option.key)"
        @change="
          (value: string | number | boolean) => toggle(option.key, Boolean(value), modelValue)
        "
        >{{ option.label }}</ElCheckbox
      >
    </div>
  </ElPopover>
</template>

<style scoped>
.column-list {
  display: grid;
  gap: 2px;
}
</style>
