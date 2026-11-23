<script setup lang="ts">
import { ElMessage } from "element-plus";
import type { SoftwareAccess } from "./webAccess";

defineProps<{ access: SoftwareAccess }>();

const plugins = [
  { name: "Pipeline", purpose: "定义和运行流水线" },
  { name: "Git", purpose: "从 Git 仓库拉取源码" },
  { name: "Pipeline: Stage View", purpose: "按需安装，用来查看各阶段的状态和耗时" },
  { name: "JUnit", purpose: "按需安装，用来查看测试报告" },
  { name: "Credentials Binding", purpose: "按需安装，向构建步骤提供凭据" },
  { name: "Docker Pipeline", purpose: "使用容器作为流水线构建环境时安装" },
];

async function copyName(name: string) {
  try {
    await navigator.clipboard.writeText(name);
    ElMessage.success("插件名称已复制");
  } catch {
    ElMessage.info("请选中插件名称后手动复制");
  }
}
</script>

<template>
  <section class="jenkins-plugins" aria-label="Jenkins 插件">
    <div class="plugins-toolbar">
      <a v-if="access.url" class="primary" :href="access.url" target="_blank" rel="noopener noreferrer">打开 Jenkins 插件管理 ↗</a>
      <p v-else class="muted">{{ access.reason }}</p>
      <p class="muted">登录 Jenkins 后，在「可用插件」搜索并安装。已有插件可在「已安装」中查找。</p>
    </div>
    <ul class="plugin-list">
      <li v-for="plugin in plugins" :key="plugin.name">
        <div><strong>{{ plugin.name }}</strong><p class="muted">{{ plugin.purpose }}</p></div>
        <button type="button" class="quiet" :aria-label="`复制 ${plugin.name} 名称`" @click="copyName(plugin.name)">复制名称</button>
      </li>
    </ul>
  </section>
</template>

<style scoped>
.jenkins-plugins { display: grid; gap: 14px; }
.plugins-toolbar { display: flex; flex-wrap: wrap; align-items: center; gap: 8px 14px; }
.plugins-toolbar .primary { width: auto; margin: 0; padding: 8px 12px; border-radius: var(--control-radius); text-decoration: none; }
.jenkins-plugins .muted { margin: 0; font-size: 12px; line-height: 1.55; }
.plugin-list { margin: 0; padding: 0; list-style: none; border: 1px solid var(--border); border-radius: var(--control-radius); }
.plugin-list li { display: flex; justify-content: space-between; align-items: center; gap: 12px; padding: 10px 12px; }
.plugin-list li + li { border-top: 1px solid var(--border); }
.plugin-list strong { display: block; margin-bottom: 3px; font-size: 13px; }
.plugin-list button { flex-shrink: 0; }
</style>
