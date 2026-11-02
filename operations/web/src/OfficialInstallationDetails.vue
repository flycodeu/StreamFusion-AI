<script setup lang="ts">
import { computed, onUnmounted, ref, watch } from "vue";
import type { OfficialInstallation } from "./serviceInstallation";
import {
  officialCommandText,
  officialCommandTitle,
  officialCommandTokens,
  officialPackageSource,
} from "./officialInstallation";
const props = defineProps<{
  installation: OfficialInstallation;
  commands?: boolean;
}>();
const packages = computed(() =>
  props.installation.packages.map((item) => ({
    ...item,
    source: officialPackageSource(item.origin),
  })),
);
const mainPackages = computed(() =>
  packages.value.filter((item) => item.direct),
);
const dependencies = computed(() =>
  packages.value.filter((item) => !item.direct),
);
const copied = ref<number>();
const copyError = ref<number>();
let copyTimer: ReturnType<typeof setTimeout> | undefined;
async function copyCommand(index: number) {
  copied.value = undefined;
  copyError.value = undefined;
  try {
    await navigator.clipboard.writeText(
      officialCommandText(props.installation.commands[index]!),
    );
    copied.value = index;
    clearTimeout(copyTimer);
    copyTimer = setTimeout(() => {
      copied.value = undefined;
    }, 2000);
  } catch {
    copyError.value = index;
  }
}
watch(
  () => props.installation.commands,
  () => {
    clearTimeout(copyTimer);
    copied.value = undefined;
    copyError.value = undefined;
  },
);
onUnmounted(() => clearTimeout(copyTimer));
</script>

<template>
  <section class="official-details" aria-label="官方安装详情">
    <div class="section-heading">
      <h3>软件包</h3>
      <span
        >{{ installation.os }} · {{ installation.architecture }} ·
        {{ installation.manager }}</span
      >
    </div>
    <ul class="package-list" aria-label="安装软件包">
      <li v-for="item in mainPackages" :key="item.name">
        <div class="package-identity">
          <strong>{{ item.name }}</strong
          ><code>{{ item.version }}</code>
        </div>
        <a
          v-if="item.source.href"
          class="package-source"
          :href="item.source.href"
          :title="item.origin"
          target="_blank"
          rel="noopener noreferrer"
          >{{ item.source.label }} ↗</a
        >
        <span v-else class="package-source" :title="item.origin">{{
          item.source.label
        }}</span>
      </li>
    </ul>
    <details v-if="dependencies.length" class="package-dependencies">
      <summary>
        依赖 <span>{{ dependencies.length }}</span>
      </summary>
      <ul class="package-list">
        <li v-for="item in dependencies" :key="item.name">
          <div class="package-identity">
            <strong>{{ item.name }}</strong
            ><code>{{ item.version }}</code>
          </div>
          <a
            v-if="item.source.href"
            class="package-source"
            :href="item.source.href"
            :title="item.origin"
            target="_blank"
            rel="noopener noreferrer"
            >{{ item.source.label }} ↗</a
          ><span v-else class="package-source" :title="item.origin">{{
            item.source.label
          }}</span>
        </li>
      </ul>
    </details>
    <details class="full-sources">
      <summary>完整来源</summary>
      <dl>
        <template v-for="item in packages" :key="item.name"
          ><dt>{{ item.name }}</dt>
          <dd>
            <a
              v-if="item.source.href"
              :href="item.source.href"
              target="_blank"
              rel="noopener noreferrer"
              >{{ item.origin }}</a
            ><code v-else>{{ item.origin }}</code>
          </dd></template
        >
      </dl>
    </details>
    <template v-if="commands && installation.commands.length">
      <h3>执行步骤</h3>
      <ol class="terminal" aria-label="安装命令">
        <li v-for="(command, index) in installation.commands" :key="index">
          <div class="command-heading">
            <span class="step-number">{{
              String(index + 1).padStart(2, "0")
            }}</span
            ><strong>{{ officialCommandTitle(command) }}</strong
            ><button
              type="button"
              :aria-label="`复制第 ${index + 1} 步命令`"
              @click="copyCommand(index)"
            >
              {{ copied === index ? "已复制" : "复制" }}
            </button>
          </div>
          <pre><code><span v-for="(token, tokenIndex) in officialCommandTokens(command)" :key="tokenIndex" :class="`token-${token.kind}`">{{ token.text }}</span></code></pre>
          <p v-if="copyError === index" role="status" class="copy-error">
            复制未完成，请选择命令文本复制。
          </p>
        </li>
      </ol>
    </template>
    <h3>系统位置与服务</h3>
    <dl class="system-paths" aria-label="系统位置与服务">
      <template
        v-for="(label, key) in {
          binary: '程序',
          config: '配置',
          data: '数据',
          logs: '日志',
          unit: '服务',
        }"
        :key="key"
        ><template v-if="installation.paths[key]"
          ><dt>{{ label }}</dt>
          <dd>
            <code>{{ installation.paths[key] }}</code>
          </dd></template
        ></template
      >
    </dl>
  </section>
</template>

<style scoped>
.official-details {
  display: grid;
  gap: 13px;
  min-width: 0;
  color: var(--text);
}
.section-heading {
  display: flex;
  align-items: baseline;
  justify-content: space-between;
  gap: 10px;
  flex-wrap: wrap;
}
h3 {
  margin: 6px 0 0;
  font-size: 13px;
  font-weight: 600;
  line-height: 1.6;
}
.section-heading span {
  font-size: 11px;
  color: var(--muted);
  line-height: 1.6;
}
.package-list {
  list-style: none;
  padding: 0;
  margin: 0;
  min-width: 0;
}
.package-list li {
  display: grid;
  grid-template-columns: minmax(0, 1fr) minmax(0, 160px);
  gap: 12px;
  align-items: baseline;
  padding: 9px 0;
  border-bottom: 1px solid var(--border);
}
.package-list li:first-child {
  padding-top: 1px;
}
.package-identity {
  display: flex;
  align-items: baseline;
  gap: 5px 12px;
  flex-wrap: wrap;
  min-width: 0;
}
.package-identity strong {
  font-size: 12px;
  font-weight: 500;
  overflow-wrap: anywhere;
}
.package-identity code {
  color: var(--muted);
}
.package-source {
  font-size: 11px;
  line-height: 1.7;
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  text-align: right;
  color: var(--muted);
}
a.package-source,
a {
  color: var(--accent);
  text-decoration: none;
}
a:hover {
  text-decoration: underline;
}
summary {
  font-size: 12px;
  line-height: 1.8;
  cursor: pointer;
  color: var(--muted);
}
summary span {
  font: 11px var(--mono);
  margin-left: 4px;
}
.package-dependencies .package-list {
  margin-top: 9px;
}
.full-sources dl {
  margin-top: 10px;
  padding: 12px;
  background: var(--surface, #f7f9fc);
  border-radius: 5px;
  grid-template-columns: minmax(74px, 130px) minmax(0, 1fr);
}
.full-sources a {
  font: 11px/1.7 var(--mono);
  overflow-wrap: anywhere;
}
.terminal {
  list-style: none;
  counter-reset: step;
  margin: 0;
  padding: 0;
  background: #172233;
  border: 1px solid #26354a;
  border-radius: 7px;
  overflow: hidden;
  min-width: 0;
}
.terminal li {
  padding: 14px 16px;
  min-width: 0;
}
.terminal li + li {
  border-top: 1px solid #2b384c;
}
.command-heading {
  display: flex;
  align-items: center;
  gap: 10px;
  margin-bottom: 10px;
  font-size: 11px;
  line-height: 1.5;
  color: #c5cfde;
}
.command-heading strong {
  font-weight: 500;
}
.step-number {
  font: 10px var(--mono);
  color: #7c8da7;
}
.command-heading button {
  margin-left: auto;
  border: 1px solid #42526a;
  background: transparent;
  border-radius: 4px;
  color: #cad6e8;
  padding: 3px 8px;
  cursor: pointer;
  font-size: 11px;
  line-height: 1.5;
  flex: none;
  min-height: 0;
}
.command-heading button:hover {
  background: #293a50;
  color: #fff;
}
.command-heading button:focus-visible {
  outline: 2px solid #8dc3ff;
  outline-offset: 2px;
}
pre {
  margin: 0;
  min-width: 0;
  white-space: pre-wrap;
  overflow-wrap: anywhere;
  font: 11px/1.9 var(--mono);
  tab-size: 2;
}
code {
  font: 11px/1.7 var(--mono);
  overflow-wrap: anywhere;
}
.terminal pre code {
  font: inherit;
  color: #e2e9f3;
}
.token-executable {
  color: #83c5f6;
}
.token-option {
  color: #e5ba78;
}
.token-argument {
  color: #d9e4ef;
}
.token-continuation {
  color: #73869f;
}
.copy-error {
  margin: 8px 0 0;
  font-size: 11px;
  color: #f0c89b;
}
dl {
  display: grid;
  grid-template-columns: 42px minmax(0, 1fr);
  column-gap: 14px;
  row-gap: 7px;
  margin: 0;
}
dt,
dd {
  margin: 0;
  font-size: 12px;
  min-width: 0;
  line-height: 1.7;
}
dt {
  color: var(--muted);
}
.system-paths dd code {
  color: var(--text);
}
@media (max-width: 480px) {
  .package-list li {
    grid-template-columns: minmax(0, 1fr) minmax(0, 118px);
    gap: 8px;
  }
  .package-identity {
    gap: 2px 7px;
  }
  .terminal li {
    padding: 12px;
  }
  .full-sources dl {
    grid-template-columns: 1fr;
    gap: 4px;
  }
  .full-sources dd + dt {
    margin-top: 9px;
  }
  .section-heading {
    gap: 4px;
  }
}
</style>
