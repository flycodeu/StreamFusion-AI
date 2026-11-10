<script setup lang="ts">
import { computed, nextTick, ref, watch } from "vue";
import { errorReference, findErrorReference, searchErrorReference } from "./errorReference";

const props = withDefaults(defineProps<{ initialCode?: string }>(), { initialCode: "" });
const query = ref("");
const category = ref("");
const selected = ref("");
const content = ref<HTMLElement>();
const navigation = ref<HTMLElement>();
const unresolvedCode = ref("");
const categories = [...new Set(errorReference.map(entry => entry.category))];
const rows = computed(() => searchErrorReference(query.value).filter(entry =>
  !category.value || entry.category === category.value));
const current = computed(() => rows.value.find(entry => entry.code === selected.value));

watch(() => props.initialCode, (code) => {
  const entry = findErrorReference(code);
  query.value = code.trim();
  category.value = "";
  selected.value = entry?.code || "";
  unresolvedCode.value = code.trim() && !entry ? code.trim() : "";
}, { immediate: true });
watch(rows, (entries) => {
  if (unresolvedCode.value && query.value === unresolvedCode.value) return;
  if (!entries.some(entry => entry.code === selected.value)) selected.value = entries[0]?.code || "";
}, { immediate: true });
watch(selected, () => {
  if (content.value) content.value.scrollTop = 0;
}, { flush: "post" });
function select(code: string) {
  unresolvedCode.value = "";
  selected.value = code;
}
function moveSelection(direction: number) {
  const index = rows.value.findIndex(entry => entry.code === selected.value);
  const entry = rows.value[Math.max(0, Math.min(rows.value.length - 1, index + direction))];
  if (!entry) return;
  select(entry.code);
  void nextTick(() => navigation.value?.querySelector<HTMLButtonElement>('[aria-current="page"]')?.focus());
}
</script>

<template>
  <section class="error-reference" aria-label="错误说明列表">
    <aside class="reference-navigation" aria-label="错误条目导航">
      <div class="reference-search">
        <input v-model="query" type="search" placeholder="搜索错误码或关键词" aria-label="查询错误说明" @input="unresolvedCode = ''" />
        <select v-model="category" aria-label="错误分类" @change="unresolvedCode = ''">
          <option value="">全部分类</option>
          <option v-for="name in categories" :key="name" :value="name">{{ name }}</option>
        </select>
      </div>
      <div ref="navigation" class="reference-list" @keydown.up.prevent="moveSelection(-1)" @keydown.down.prevent="moveSelection(1)">
        <button v-for="entry in rows" :key="entry.code" type="button" class="reference-heading"
          :aria-label="`${entry.name} ${entry.code}`" :aria-current="selected === entry.code ? 'page' : undefined"
          :tabindex="selected === entry.code || (!selected && entry === rows[0]) ? 0 : -1" @click="select(entry.code)">
          <strong>{{ entry.name }}</strong><code>{{ entry.code }}</code>
        </button>
      </div>
    </aside>
    <article v-if="current" ref="content" class="reference-content" aria-label="错误说明正文" tabindex="0">
      <header class="reference-title">
        <span class="reference-category">{{ current.category }}</span>
        <h2>{{ current.name }}</h2>
        <code>{{ current.code }}</code>
      </header>
      <section class="reference-section">
        <h3>错误含义</h3>
        <p>{{ current.meaning }}</p>
      </section>
      <section class="reference-section">
        <h3>检查步骤</h3>
        <ol><li v-for="step in current.steps" :key="step">{{ step }}</li></ol>
      </section>
    </article>
    <div v-else class="reference-empty" role="status">
      <strong>{{ unresolvedCode ? "未收录此错误" : "没有匹配的错误说明" }}</strong>
      <code v-if="unresolvedCode">{{ unresolvedCode }}</code>
    </div>
  </section>
</template>

<style scoped>
.error-reference {
  display: grid;
  grid-template-columns: 290px minmax(0, 1fr);
  height: min(72dvh, 720px);
  min-width: 0;
  overflow: hidden;
  border: 1px solid var(--border);
  border-radius: 8px;
  background: var(--surface);
}
.reference-navigation {
  display: flex;
  flex-direction: column;
  min-height: 0;
  min-width: 0;
  border-right: 1px solid var(--border);
  background: #f8fafc;
}
.reference-search {
  display: grid;
  gap: 10px;
  padding: 16px;
  border-bottom: 1px solid var(--border);
}
.reference-search input,
.reference-search select {
  width: 100%;
  min-width: 0;
  margin: 0;
  height: 36px;
  font-size: 12px;
}
.reference-list {
  min-height: 0;
  overflow-y: auto;
  padding: 6px;
}
.reference-heading {
  display: grid;
  gap: 6px;
  width: 100%;
  padding: 12px 10px;
  border: 0;
  border-left: 3px solid transparent;
  border-radius: 4px;
  background: transparent;
  color: var(--text);
  text-align: left;
}
.reference-heading:hover { background: #f1f5f9; }
.reference-heading[aria-current="page"] {
  border-left-color: var(--accent);
  background: var(--accent-soft);
  color: var(--accent);
}
.reference-heading strong { font-size: 13px; font-weight: 500; }
.reference-heading code { color: var(--muted); font: 10px/1.5 var(--mono); overflow-wrap: anywhere; }
.reference-content {
  min-width: 0;
  overflow-y: auto;
  padding: 30px 34px;
  color: var(--text);
}
.reference-title { display: block; padding-bottom: 26px; border-bottom: 1px solid var(--border); }
.reference-category { display: inline-block; padding: 4px 8px; border-radius: 4px; background: var(--accent-soft); color: var(--accent); font-size: 12px; }
.reference-title h2 { margin: 16px 0 12px; font-size: 23px; line-height: 1.4; font-weight: 600; }
.reference-title code { font: 12px/1.7 var(--mono); color: var(--muted); overflow-wrap: anywhere; }
.reference-section { margin-top: 28px; }
.reference-section h3 { margin: 0 0 13px; font-size: 15px; font-weight: 600; }
.reference-section p,
.reference-section li { font-size: 13px; line-height: 1.95; overflow-wrap: anywhere; }
.reference-section p { margin: 0; }
.reference-section ol { margin: 0; padding-left: 22px; }
.reference-section li { padding-left: 5px; }
.reference-section li + li { margin-top: 12px; }
.reference-section li::marker { color: var(--accent); font-weight: 600; }
.reference-empty { display: flex; flex-direction: column; align-items: center; justify-content: center; gap: 12px; min-width: 0; padding: 24px; color: var(--muted); text-align: center; }
.reference-empty strong { font-size: 14px; font-weight: 500; }
.reference-empty code { max-width: 100%; font: 12px/1.8 var(--mono); overflow-wrap: anywhere; }
@media (max-width: 760px) {
  .error-reference { grid-template-columns: minmax(0, 1fr); grid-template-rows: minmax(120px, 32%) minmax(0, 1fr); }
  .reference-navigation { border-right: 0; border-bottom: 1px solid var(--border); }
  .reference-search { grid-template-columns: minmax(0, 1fr) 120px; gap: 8px; padding: 12px; }
  .reference-list { padding: 4px 8px; }
  .reference-heading { gap: 4px; padding: 9px 8px; }
  .reference-content { padding: 20px 16px; }
  .reference-title { padding-bottom: 18px; }
  .reference-title h2 { font-size: 19px; margin-top: 12px; }
  .reference-section { margin-top: 22px; }
}
</style>
