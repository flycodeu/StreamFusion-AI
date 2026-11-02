<script setup lang="ts">
import { computed } from "vue";
import type { OfficialInstallation } from "./serviceInstallation";
const props = defineProps<{ installation: OfficialInstallation; commands?: boolean }>();
const mainPackages = computed(() => props.installation.packages.filter((item) => item.direct));
const dependencies = computed(() => props.installation.packages.filter((item) => !item.direct));
function argument(value: string) {
  return /^[A-Za-z0-9_./:=@+,-]+$/.test(value) ? value : "'" + value.replaceAll("'", "'\\''") + "'";
}
</script>

<template>
  <section class="official-details" aria-label="官方安装详情">
    <p class="official-environment">{{ installation.os }} · {{ installation.architecture }} · {{ installation.manager }}</p>
    <h3>软件包与实际版本</h3>
    <div v-for="item in mainPackages" :key="item.name" class="official-package">
      <strong>{{ item.name }}</strong>
      <code>{{ item.version }}</code><span>{{ item.origin }}</span>
    </div>
    <details v-if="dependencies.length" class="package-dependencies">
      <summary>查看 {{ dependencies.length }} 项依赖</summary>
      <div v-for="item in dependencies" :key="item.name" class="official-package">
        <strong>{{ item.name }}</strong><code>{{ item.version }}</code><span>{{ item.origin }}</span>
      </div>
    </details>
    <template v-if="commands">
      <h3>将在目标服务器执行</h3>
      <p>目标服务器需要联网；命令由固定官方配方生成，使用系统软件包与默认配置。</p>
      <pre v-for="(command, index) in installation.commands" :key="index">{{ [command.executable, ...command.args].map(argument).join(' ') }}</pre>
    </template>
    <h3>系统位置与服务</h3>
    <dl>
      <template v-for="(label, key) in { binary: '程序', config: '配置', data: '数据', logs: '日志', unit: '服务' }" :key="key">
        <template v-if="installation.paths[key]"><dt>{{ label }}</dt><dd><code>{{ installation.paths[key] }}</code></dd></template>
      </template>
    </dl>
  </section>
</template>

<style scoped>
.official-details { display: grid; gap: 12px; min-width: 0; }
h3 { margin: 8px 0 0; font-size: 13px; }
p, span, small { margin: 0; color: var(--muted); font-size: 12px; line-height: 1.8; overflow-wrap: anywhere; }
.official-package { display: grid; gap: 5px; border-bottom: 1px solid var(--border); padding-bottom: 12px; }
strong, summary { font-size: 12px; font-weight: 500; }
summary { cursor: pointer; color: var(--accent); }
.package-dependencies[open] { display: grid; gap: 12px; }
.package-dependencies .official-package { margin-top: 12px; }
small { margin-left: 8px; }
pre { padding: 12px; margin: 0; background: #f6f8fb; border: 1px solid var(--border); border-radius: 5px; white-space: pre-wrap; overflow-wrap: anywhere; font: 11px/1.8 var(--mono); }
code { font: 11px/1.8 var(--mono); overflow-wrap: anywhere; }
dl { display: grid; grid-template-columns: 52px minmax(0,1fr); gap: 12px; margin: 0; }
dt, dd { margin: 0; font-size: 12px; } dt { color: var(--muted); }
</style>
