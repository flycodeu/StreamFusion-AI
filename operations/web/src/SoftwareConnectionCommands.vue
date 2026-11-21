<script setup lang="ts">
import { computed } from "vue";
import type { Host } from "./hosts";
import type { SoftwareInstance } from "./software";
import { softwareConnectionCommands } from "./softwareConnectionCommands";
import TerminalCommandEntry from "./TerminalCommandEntry.vue";

const props = defineProps<{ detail: SoftwareInstance; host?: Host }>();
const commands = computed(() => softwareConnectionCommands(props.detail));
</script>

<template>
  <section v-if="commands.length" class="terminal-commands" aria-label="终端命令">
    <header><h3>终端命令</h3><span>在 {{ detail.hostName || host?.name || "软件所在主机" }} 的终端执行</span></header>
    <TerminalCommandEntry v-for="entry in commands" :key="entry.label" v-bind="entry" />
  </section>
</template>

<style scoped>
.terminal-commands {
  display: grid;
  grid-template-columns: minmax(0, 1fr);
  gap: 16px;
  margin-bottom: 24px;
  min-width: 0;
}
.terminal-commands header { display: flex; align-items: center; justify-content: space-between; flex-wrap: wrap; gap: 4px 12px; }
.terminal-commands h3 { margin: 0; font-size: 15px; }
.terminal-commands header span { color: var(--muted); font-size: 12px; }
</style>
