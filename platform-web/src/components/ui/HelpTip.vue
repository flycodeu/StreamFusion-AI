<script setup lang="ts">
import { ElIcon, ElTooltip } from 'element-plus'
import { QuestionFilled } from '@element-plus/icons-vue'

withDefaults(
  defineProps<{
    content: string
    label?: string
    placement?: 'top' | 'bottom' | 'left' | 'right' | 'top-start' | 'bottom-start'
  }>(),
  {
    label: '查看说明',
    placement: 'top',
  },
)
</script>

<template>
  <ElTooltip
    :content="content"
    :placement="placement"
    effect="dark"
    popper-class="sf-help-tooltip"
    :show-after="150"
  >
    <button type="button" class="sf-help-tip" :aria-label="label" @click.stop.prevent>
      <ElIcon aria-hidden="true"><QuestionFilled /></ElIcon>
    </button>
  </ElTooltip>
</template>

<style scoped>
.sf-help-tip {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 20px;
  height: 20px;
  padding: 0;
  border: 0;
  border-radius: var(--sf-radius-pill);
  background: transparent;
  color: var(--sf-text-muted);
  font-size: 14px;
  line-height: 1;
  cursor: help;
  vertical-align: middle;
  flex-shrink: 0;
  transition:
    color 0.15s ease,
    background-color 0.15s ease;
}

.sf-help-tip:hover,
.sf-help-tip:focus-visible {
  color: var(--sf-primary);
  background: var(--sf-primary-soft);
}
</style>
