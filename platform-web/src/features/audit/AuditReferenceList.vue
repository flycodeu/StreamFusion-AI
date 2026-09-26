<script setup lang="ts">
import type { AuditReference } from '../../api/audit/types'
import { referenceName, referenceSource } from './presentation'
defineProps<{ references: AuditReference[] }>()
</script>
<template>
  <ul class="audit-references">
    <li v-for="reference in references" :key="`${reference.type}:${reference.id}`">
      <span class="reference-name">{{ referenceName(reference) }}</span>
      <span class="reference-context"
        >{{ referenceSource(reference) }} · ID {{ reference.id }}</span
      >
    </li>
  </ul>
</template>
<style scoped>
.audit-references {
  list-style: none;
  padding: 0;
  margin: 0;
}
.audit-references li + li {
  border-top: 1px solid var(--el-border-color-lighter);
  padding-top: 10px;
  margin-top: 10px;
}
.reference-name {
  display: block;
  color: var(--el-text-color-primary);
  overflow-wrap: anywhere;
}
.reference-context {
  display: block;
  margin-top: 4px;
  color: var(--el-text-color-regular);
  font-size: 13px;
  overflow-wrap: anywhere;
}
</style>
