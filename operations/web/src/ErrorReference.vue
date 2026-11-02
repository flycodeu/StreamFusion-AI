<script setup lang="ts">
import { computed, ref, watch } from "vue";
import { errorReference, findErrorReference, searchErrorReference } from "./errorReference";

const props = withDefaults(defineProps<{ initialCode?: string }>(), { initialCode: "" });
const query = ref("");
const selected = ref("");
watch(() => props.initialCode, (code) => {
  query.value = code;
  selected.value = findErrorReference(code)?.code || "";
}, { immediate: true });
const rows = computed(() => searchErrorReference(query.value));
</script>

<template>
  <section class="error-reference" aria-label="错误说明列表">
    <div class="reference-search">
      <label>查询错误<input v-model="query" type="search" placeholder="错误码、中文名称或关键词" aria-label="查询错误说明" /></label>
      <button v-if="query" type="button" class="secondary" @click="query = ''; selected = ''">全部说明</button>
      <small>{{ rows.length }} / {{ errorReference.length }}</small>
    </div>
    <p class="reference-intro">点击条目查看检查步骤。具体原因需结合出错主机、时间和 traceId 核对。</p>
    <div v-if="!rows.length" class="empty">
      未收录此错误。请保留原始错误码、时间与 traceId，查看对应作业或主机接入诊断。
    </div>
    <div v-else class="reference-list">
      <article v-for="entry in rows" :key="entry.code" class="reference-entry">
        <button type="button" class="reference-heading" :aria-expanded="selected === entry.code" @click="selected = selected === entry.code ? '' : entry.code">
          <span><strong>{{ entry.name }}</strong><code>{{ entry.code }}</code></span>
          <small>{{ entry.category }}</small><span aria-hidden="true">{{ selected === entry.code ? '−' : '+' }}</span>
        </button>
        <div v-if="selected === entry.code" class="reference-detail">
          <p>{{ entry.meaning }}</p>
          <ol><li v-for="step in entry.steps" :key="step">{{ step }}</li></ol>
        </div>
      </article>
    </div>
  </section>
</template>

<style scoped>
.error-reference { min-width: 0; }
.reference-search { display: flex; align-items: flex-end; flex-wrap: wrap; gap: 12px; }
.reference-search label { display: grid; gap: 7px; flex: 1; min-width: min(240px, 100%); }
.reference-search input { width: 100%; }
.reference-search small { align-self: center; color: var(--muted); white-space: nowrap; }
.reference-intro { color: var(--muted); font-size: 13px; margin: 14px 0; }
.reference-list { display: grid; border: 1px solid var(--border); border-radius: var(--control-radius); overflow: hidden; }
.reference-entry + .reference-entry { border-top: 1px solid var(--border); }
.reference-heading { width: 100%; border: 0; border-radius: 0; background: var(--surface); color: var(--text); display: flex; align-items: center; gap: 12px; text-align: left; padding: 14px 16px; }
.reference-heading:hover, .reference-heading[aria-expanded="true"] { background: #f8fafc; }
.reference-heading > span:first-child { flex: 1; min-width: 0; display: grid; gap: 5px; }
.reference-heading strong { font-size: 14px; font-weight: 600; }
.reference-heading code { font: 12px var(--mono); color: var(--muted); overflow-wrap: anywhere; }
.reference-heading small { color: var(--muted); white-space: nowrap; }
.reference-detail { padding: 0 16px 16px; font-size: 13px; line-height: 1.8; overflow-wrap: anywhere; }
.reference-detail p { margin: 0; padding-top: 10px; }
.reference-detail ol { padding-left: 20px; margin: 8px 0 0; }
.reference-detail li + li { margin-top: 5px; }
@media (max-width: 500px) { .reference-heading { gap: 8px; padding: 12px; } .reference-heading small { display: none; } .reference-detail { padding: 0 12px 12px; } }
</style>
