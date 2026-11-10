<script setup lang="ts">
import { computed } from "vue";
import { recipeSystemStepDetails } from "./serviceInstallation";

const props = defineProps<{
  recipe: Parameters<typeof recipeSystemStepDetails>[0];
  step: Parameters<typeof recipeSystemStepDetails>[1];
  context?: Parameters<typeof recipeSystemStepDetails>[2];
}>();
const rows = computed(() => recipeSystemStepDetails(props.recipe, props.step, props.context));
</script>

<template>
  <dl class="system-step-details">
    <template v-for="(row, index) in rows" :key="index">
      <dt>{{ row.label }}</dt>
      <dd><a v-if="row.url" :href="row.value" target="_blank" rel="noopener noreferrer">{{ row.value }}</a><span v-else>{{ row.value }}</span></dd>
    </template>
  </dl>
</template>

<style scoped>
.system-step-details { display: grid; grid-template-columns: auto minmax(0, 1fr); gap: 8px 12px; margin: 10px 0 0; padding: 12px; border: 1px solid #dfe6ee; border-radius: 5px; background: #fff; font-size: 11px; line-height: 1.7; }
dt { color: var(--muted); }
dd { min-width: 0; margin: 0; overflow-wrap: anywhere; }
a { color: #356caa; }
</style>
