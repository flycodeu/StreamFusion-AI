<script setup lang="ts">
import { computed } from 'vue'
import { resolveIcon } from './catalog'
const props = defineProps<{ name?: string | null }>()
const icon = computed(() => resolveIcon(props.name))
</script>
<template><component :is="icon" class="app-icon" aria-hidden="true" /></template>
<style scoped>
.app-icon {
  width: 18px;
  height: 18px;
  flex-shrink: 0;
}
</style>
