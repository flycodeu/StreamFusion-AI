<script setup lang="ts">
import { computed } from 'vue'
import { getPreviewLayout, type PreviewLayout } from './layouts'

const props = defineProps<{ layout: PreviewLayout }>()
const cells = computed(() => {
  const option = getPreviewLayout(props.layout)
  const span = option.leadSpan || 1
  const gap = option.columns >= 5 ? 1 : 2
  const width = (46 - gap * (option.columns - 1)) / option.columns
  const height = (34 - gap * (option.rows - 1)) / option.rows
  const rectangles = []
  for (let row = 0; row < option.rows; row++) {
    for (let column = 0; column < option.columns; column++) {
      const large = row === 0 && column === 0 && span > 1
      if (row < span && column < span && !large && span > 1) continue
      rectangles.push({
        x: 1 + column * (width + gap),
        y: 1 + row * (height + gap),
        width: large ? width * span + gap * (span - 1) : width,
        height: large ? height * span + gap * (span - 1) : height,
      })
    }
  }
  return rectangles
})
</script>

<template>
  <svg class="layout-icon" viewBox="0 0 48 36" aria-hidden="true" focusable="false">
    <rect
      v-for="(cell, index) in cells"
      :key="index"
      v-bind="cell"
      :rx="layout === '64' ? 0.5 : 1"
      fill="currentColor"
      fill-opacity="0.12"
      stroke="currentColor"
      :stroke-width="layout === '64' ? 0.7 : 1.2"
    />
  </svg>
</template>

<style scoped>
.layout-icon {
  display: block;
  width: 48px;
  height: 36px;
  flex-shrink: 0;
}
</style>
