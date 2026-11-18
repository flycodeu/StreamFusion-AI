<script setup lang="ts">
import { computed, nextTick, ref, watch } from "vue";

const props = defineProps<{ modelValue: string; readonly?: boolean; disabled?: boolean; errorLine?: number; label?: string; byteLimit?: number }>();
const emit = defineEmits<{ "update:modelValue": [string]; save: []; limit: [] }>();
const input = ref<HTMLTextAreaElement>(), gutter = ref<HTMLDivElement>();
const lines = computed(() => props.modelValue.split("\n"));
const firstLine = ref(1);
const lineHeight = 21;
const visibleLines = computed(() => Math.min(48, lines.value.length - firstLine.value + 1));

function syncScroll() {
  if (!gutter.value || !input.value) return;
  firstLine.value = Math.min(lines.value.length, Math.floor(input.value.scrollTop / lineHeight) + 1);
  gutter.value.style.setProperty("--line-offset", `${-(input.value.scrollTop % lineHeight)}px`);
}
function update(event: Event) {
  const field = event.target as HTMLTextAreaElement;
  if (props.byteLimit && new TextEncoder().encode(field.value).length > props.byteLimit) {
    field.value = props.modelValue;
    emit("limit");
    return;
  }
  emit("update:modelValue", field.value);
}
function keydown(event: KeyboardEvent) {
  if ((event.ctrlKey || event.metaKey) && event.key.toLowerCase() === "s") {
    event.preventDefault();
    if (!props.readonly && !props.disabled) emit("save");
  }
  if (event.key !== "]" || !(event.ctrlKey || event.metaKey) || props.readonly || props.disabled || !input.value) return;
  event.preventDefault();
  const start = input.value.selectionStart, end = input.value.selectionEnd;
  const next = props.modelValue.slice(0, start) + "  " + props.modelValue.slice(end);
  if (props.byteLimit && new TextEncoder().encode(next).length > props.byteLimit) { emit("limit"); return; }
  emit("update:modelValue", next);
  void nextTick(() => input.value?.setSelectionRange(start + 2, start + 2));
}
watch(lines, async () => { await nextTick(); syncScroll(); });
watch(() => props.errorLine, async line => {
  if (!line || line < 1 || line > lines.value.length) return;
  await nextTick();
  if (!input.value) return;
  const start = lines.value.slice(0, line - 1).reduce((offset, text) => offset + text.length + 1, 0);
  input.value.focus();
  input.value.setSelectionRange(start, start + lines.value[line - 1].length);
  input.value.scrollTop = Math.max(0, (line - 4) * lineHeight);
  syncScroll();
});
</script>

<template>
  <div class="configuration-text-editor" :class="{ 'has-error': errorLine, readonly }">
    <div ref="gutter" class="editor-gutter" aria-hidden="true"><div class="gutter-lines"><div v-for="offset in visibleLines" :key="offset" :class="{ invalid: firstLine + offset - 1 === errorLine }">{{ firstLine + offset - 1 }}</div></div></div>
    <textarea ref="input" :value="modelValue" :readonly="readonly" :disabled="disabled" :aria-label="label || '配置文件内容'" spellcheck="false" autocapitalize="off" autocomplete="off" wrap="off" @input="update" @scroll="syncScroll" @keydown="keydown"></textarea>
  </div>
</template>

<style scoped>
.configuration-text-editor { display:grid; grid-template-columns:auto minmax(0,1fr); min-width:0; height:350px; max-height:52vh; min-height:180px; overflow:hidden; border:1px solid #284158; border-radius:6px; background:#122232; color:#dce8f5; }
.configuration-text-editor.has-error { border-color:var(--error); }
.editor-gutter { min-width:48px; min-height:0; padding:14px 8px; overflow:hidden; text-align:right; user-select:none; color:#7890a6; background:#10202f; font:12px/21px var(--mono); }
.gutter-lines { transform:translateY(var(--line-offset, 0px)); }
.editor-gutter .invalid { color:#ffb9ae; background:#643834; }
.configuration-text-editor textarea { display:block; margin:0; width:100%; min-width:0; height:100%; min-height:0; resize:none; padding:14px 16px; border:0; border-radius:0; outline:0; background:transparent; color:inherit; font:12px/21px var(--mono); tab-size:2; white-space:pre; overflow:auto; }
.configuration-text-editor textarea:focus { box-shadow:inset 0 0 0 1px #5b8dac; }
.configuration-text-editor textarea:disabled { opacity:.7; background:transparent; }
.readonly textarea { color:#b8cadb; }
@media (max-width:600px) { .configuration-text-editor { height:300px; max-height:50vh; }.configuration-text-editor textarea { padding:12px 10px; }.editor-gutter { padding-top:12px; min-width:34px; padding-inline:6px; } }
</style>
