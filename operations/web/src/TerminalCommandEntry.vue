<script setup lang="ts">
import { ElMessage } from "element-plus";

defineProps<{ label: string; command: string; note?: string }>();

async function copy(value: string) {
  try {
    if (navigator.clipboard?.writeText) {
      await navigator.clipboard.writeText(value);
      ElMessage.success("命令已复制");
      return;
    }
  } catch { /* Fall back to copying the visible text when clipboard access is denied. */ }
  const previous = document.activeElement;
  const input = document.createElement("textarea");
  input.value = value;
  input.readOnly = true;
  input.style.cssText = "position:fixed;left:-9999px;top:0";
  const parent = previous instanceof HTMLElement ? previous.closest('[role="dialog"]') : null;
  (parent || document.body).append(input);
  try {
    input.select();
    if (!document.execCommand("copy")) throw new Error();
    ElMessage.success("命令已复制");
  } catch { ElMessage.info("请选中命令后手动复制"); }
  finally {
    input.remove();
    if (previous instanceof HTMLElement) previous.focus({ preventScroll: true });
  }
}
</script>

<template>
  <div class="command-entry">
    <div class="command-label">
      <strong>{{ label }}</strong>
      <button type="button" class="quiet" :aria-label="'复制' + label + '命令'" @click="copy(command)">复制命令</button>
    </div>
    <pre class="command-line" tabindex="0" :aria-label="label"><span aria-hidden="true">$</span><code>{{ command }}</code></pre>
    <small v-if="note">{{ note }}</small>
  </div>
</template>

<style scoped>
.command-label { display: flex; align-items: center; justify-content: space-between; flex-wrap: wrap; gap: 4px 12px; }
.command-entry small { color: var(--muted); font-size: 12px; line-height: 1.6; }
.command-entry {
  display: grid;
  grid-template-columns: minmax(0, 1fr);
  gap: 6px;
  min-width: 0;
}
.command-label strong { font-size: 13px; font-weight: 600; }
.command-line {
  display: flex;
  gap: 10px;
  margin: 0;
  padding: 10px 14px;
  overflow-x: auto;
  border-radius: 6px;
  background: #111c2e;
  color: #f1f5f9;
  font: 12px/1.7 var(--mono);
}
.command-line:focus-visible { outline: 2px solid var(--accent); outline-offset: 2px; }
.command-line span { color: #9bb6e1; user-select: none; }
.command-line code { font: inherit; }
</style>
