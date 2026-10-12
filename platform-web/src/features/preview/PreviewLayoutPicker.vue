<script setup lang="ts">
import { computed, ref } from 'vue'
import { ElButton, ElPopover } from 'element-plus'
import { ArrowDown, Check } from '@element-plus/icons-vue'
import { getPreviewLayout, previewLayouts, type PreviewLayout } from './layouts'
import PreviewLayoutIcon from './PreviewLayoutIcon.vue'

const props = defineProps<{ modelValue: PreviewLayout }>()
const emit = defineEmits<{ 'update:modelValue': [layout: PreviewLayout] }>()
const open = ref(false)
const current = computed(() => getPreviewLayout(props.modelValue))
function choose(layout: PreviewLayout) {
  emit('update:modelValue', layout)
  open.value = false
}
</script>

<template>
  <ElPopover
    v-model:visible="open"
    trigger="click"
    placement="top-end"
    :width="280"
    :popper-style="{
      maxWidth: 'calc(100vw - 24px)',
      maxHeight: 'calc(100dvh - 100px)',
      overflowY: 'auto',
      padding: '12px',
      borderRadius: '8px',
    }"
  >
    <template #reference>
      <ElButton
        size="small"
        class="layout-trigger"
        aria-label="选择画面布局"
        :aria-expanded="open"
        :title="`当前布局：${current.label}`"
        @keydown.esc="open = false"
      >
        <PreviewLayoutIcon :layout="modelValue" />
        <span>布局</span>
        <ArrowDown class="layout-arrow" />
      </ElButton>
    </template>
    <div class="layout-picker" @keydown.esc.stop="open = false">
      <div class="layout-picker-title">画面布局</div>
      <div class="layout-choices" role="group" aria-label="画面布局">
        <button
          v-for="option in previewLayouts"
          :key="option.value"
          class="layout-choice"
          :class="{ active: modelValue === option.value }"
          type="button"
          :aria-label="option.label"
          :aria-pressed="modelValue === option.value"
          @click="choose(option.value)"
        >
          <PreviewLayoutIcon :layout="option.value" />
          <span>{{ option.label }}</span>
          <Check v-if="modelValue === option.value" class="layout-check" />
        </button>
      </div>
    </div>
  </ElPopover>
</template>

<style scoped>
.layout-trigger :deep(> span) {
  display: flex;
  align-items: center;
  gap: 7px;
}
.layout-trigger :deep(.layout-icon) {
  width: 24px;
  height: 18px;
}
.layout-arrow {
  width: 12px;
  height: 12px;
  color: #647686;
}
.layout-picker-title {
  margin-bottom: 10px;
  color: #263744;
  font-size: 13px;
  font-weight: 600;
}
.layout-choices {
  display: grid;
  grid-template-columns: repeat(3, minmax(0, 1fr));
  gap: 8px;
}
.layout-choice {
  position: relative;
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  min-width: 0;
  min-height: 76px;
  gap: 8px;
  padding: 10px 4px;
  border: 1px solid #e1e7ee;
  border-radius: 5px;
  background: #f8fafc;
  color: #647686;
  font: inherit;
  font-size: 11px;
  cursor: pointer;
}
.layout-choice:hover {
  border-color: #90b6d7;
  background: #f0f6fc;
  color: #3574ae;
}
.layout-choice.active {
  border-color: #3574ae;
  background: #eaf3fc;
  color: #3574ae;
}
.layout-choice:focus-visible {
  outline: 2px solid #3574ae;
  outline-offset: 2px;
}
.layout-check {
  position: absolute;
  top: 4px;
  right: 4px;
  width: 12px;
  height: 12px;
}
</style>
