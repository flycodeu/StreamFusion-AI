<script setup lang="ts">
import { ElButton, ElForm, ElFormItem } from 'element-plus'

withDefaults(
  defineProps<{
    loading?: boolean
    searchLabel?: string
    resetLabel?: string
    showReset?: boolean
    embedded?: boolean
  }>(),
  {
    loading: false,
    searchLabel: '查询',
    resetLabel: '重置',
    showReset: true,
    embedded: false,
  },
)

const emit = defineEmits<{
  search: []
  reset: []
}>()
</script>

<template>
  <ElForm
    class="search-form sf-filter-bar"
    :class="{ 'is-embedded': embedded }"
    inline
    @submit.prevent="emit('search')"
  >
    <slot />
    <ElFormItem class="sf-filter-bar__actions">
      <div class="search-actions">
        <ElButton type="primary" native-type="submit" :loading="loading">{{
          searchLabel
        }}</ElButton>
        <ElButton v-if="showReset" @click="emit('reset')">{{ resetLabel }}</ElButton>
        <slot name="extra-actions" />
      </div>
    </ElFormItem>
  </ElForm>
</template>

<style scoped>
.sf-filter-bar.is-embedded {
  margin: 0;
  padding: 0;
  border: 0;
  background: transparent;
  box-shadow: none;
  border-radius: 0;
}
</style>
