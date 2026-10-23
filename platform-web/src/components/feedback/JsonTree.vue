<script setup lang="ts">
import { computed } from 'vue'

const props = withDefaults(
  defineProps<{ value: unknown; name?: string; depth?: number; expanded?: boolean }>(),
  { depth: 0, expanded: true },
)
const branch = computed(() => props.value !== null && typeof props.value === 'object')
const entries = computed(() =>
  branch.value ? Object.entries(props.value as Record<string, unknown>) : [],
)
const literal = computed(() => JSON.stringify(props.value) ?? 'null')
const kind = computed(() => (props.value === null ? 'null' : typeof props.value))
</script>

<template>
  <details v-if="branch" class="json-branch" :open="expanded && depth < 3">
    <summary>
      <span v-if="name !== undefined" class="json-key"
        >{{ JSON.stringify(name) }}<span class="json-punctuation">: </span></span
      >
      <span class="json-punctuation">{{ Array.isArray(value) ? '[' : '{' }}</span>
      <span class="json-count"
        >{{ entries.length }} {{ Array.isArray(value) ? '项' : '个字段' }}</span
      >
    </summary>
    <div class="json-children">
      <JsonTree
        v-for="[key, entry] in entries"
        :key="key"
        :value="entry"
        :name="key"
        :depth="depth + 1"
        :expanded="expanded"
      />
    </div>
    <div class="json-punctuation json-close">{{ Array.isArray(value) ? ']' : '}' }}</div>
  </details>
  <div v-else class="json-leaf">
    <span v-if="name !== undefined" class="json-key"
      >{{ JSON.stringify(name) }}<span class="json-punctuation">: </span></span
    >
    <span :class="`json-${kind}`">{{ literal }}</span>
  </div>
</template>

<style scoped>
.json-branch,
.json-leaf {
  min-width: 0;
  line-height: 1.9;
  overflow-wrap: anywhere;
  white-space: pre-wrap;
}
summary {
  cursor: pointer;
  border-radius: 4px;
}
summary:hover {
  background: #eff6ff;
}
summary::marker {
  color: #94a3b8;
  font-size: 10px;
}
.json-children {
  margin-left: 6px;
  padding-left: 18px;
  border-left: 1px solid #e2e8f0;
}
.json-close {
  padding-left: 13px;
}
.json-key {
  color: #475569;
}
.json-string {
  color: #157347;
}
.json-number {
  color: #b45309;
}
.json-boolean {
  color: #1d4ed8;
}
.json-null,
.json-punctuation {
  color: #64748b;
}
.json-count {
  margin-left: 10px;
  color: #94a3b8;
  font-size: 11px;
  font-family: var(--el-font-family);
}
</style>
