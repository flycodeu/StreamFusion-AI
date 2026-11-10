<script setup lang="ts">
import { computed } from "vue";
import {
  materialOriginLabel,
  materialSize,
  type InstallationMaterial,
  type MaterialSource,
  type RuntimeMode,
  type ServiceRecipe,
} from "./serviceInstallation";

const props = defineProps<{
  recipe: ServiceRecipe;
  runtimeMode: RuntimeMode;
  material?: InstallationMaterial;
  online?: boolean;
}>();
const customCommands = computed(() => !!props.recipe.commands && !props.recipe.commands.builtin);
const sources = computed<MaterialSource[]>(() => {
  if (customCommands.value) return props.material ? [{
    name: props.material.name, url: "", sha256: props.material.sha256, byteSize: props.material.byteSize, role: "MAIN",
  }] : [];
  if (props.material?.sources?.length) return props.material.sources;
  if (props.runtimeMode === "NATIVE") {
    if (props.recipe.sources?.length) return props.recipe.sources;
    return (props.recipe.native?.parts || []).map((part, index) => ({
      ...part,
      displayName: index === 0 ? props.recipe.name : part.name,
      version: index === 0 ? props.recipe.version : undefined,
      publisher: index === 0 ? props.recipe.publisher : undefined,
      role: index === 0 ? "MAIN" : "DEPENDENCY",
    }));
  }
  return [{
    name: props.recipe.image,
    displayName: props.recipe.name,
    version: props.recipe.version,
    publisher: props.recipe.publisher,
    url: props.recipe.officialUrl,
    sha256: props.recipe.digest,
    byteSize: props.material?.byteSize || 0,
    role: "MAIN",
  }];
});
function sourceHost(url: string) {
  try { return new URL(url).hostname; } catch { return "配方固定来源"; }
}
const toolLabels: Record<string, string> = {
  cc: "C 编译器", gcc: "GCC 编译器", "g++": "C++ 编译器", make: "Make 构建工具",
  cmake: "CMake 构建工具", "pkg-config": "pkg-config", dpkg: "Debian 包管理器",
};
</script>

<template>
  <section class="material-contents" aria-label="安装材料内容">
    <header>
      <h3>将安装的内容</h3>
      <span :class="['state-tag', material?.available ? 'state-success' : 'state-muted']">
        {{ material?.available ? material.origin?.verification === 'UPLOAD_SHA256' ? '文件摘要已核验' : "本地材料已就绪" : online ? customCommands ? '按命令执行' : "需要在线获取" : "本地材料未就绪" }}
      </span>
    </header>
    <div v-for="source in sources" :key="source.name" class="material-source">
      <div class="material-source-name">
        <strong>{{ source.displayName || source.name }}</strong>
        <span v-if="source.version">{{ source.version }}</span>
        <small>{{ customCommands ? '上传文件' : source.role === "DEPENDENCY" ? "依赖组件" : runtimeMode === "DOCKER" ? "容器镜像" : "软件主件" }}</small>
      </div>
      <p v-if="source.publisher || recipe.publisher">发布方：{{ source.publisher || recipe.publisher }}</p>
      <a v-if="source.url" :href="source.url" target="_blank" rel="noopener noreferrer">
        {{ sourceHost(source.url) }} · {{ source.displayName || source.name }} 下载来源 ↗
      </a>
      <ul v-if="source.requirements?.length" class="source-requirements" aria-label="目标主机依赖">
        <li v-for="requirement in source.requirements" :key="requirement">{{ requirement }}</li>
      </ul>
    </div>
    <p class="material-network">
      {{ material?.available ? `${materialOriginLabel(material)} · 由 Ops 分发` :
        customCommands ? '使用已发布的命令配置处理材料。' :
        online && runtimeMode === "DOCKER" ? "目标服务器需要联网拉取固定镜像。" :
        online ? "目标服务器联网下载官方发行包。" :
        "请选择或上传匹配的安装材料。" }}
    </p>
    <details v-if="recipe.key === 'docker'"><summary>Docker 安装位置与权限</summary><p class="material-network">数据位于本实例 data；主机配置为 /etc/docker/daemon.json，日志由 docker.service 管理。不修改用户组。</p></details>
    <p v-if="runtimeMode === 'NATIVE' && recipe.native?.requiredTools?.length" class="material-network">
      所需工具：{{ recipe.native.requiredTools.map((tool) => toolLabels[tool] || tool).join("、") }}
    </p>
    <details>
      <summary>文件与校验详情 · {{ recipe.platform }}</summary>
      <div v-for="source in sources" :key="source.name" class="material-file">
        <strong>{{ source.name }}</strong>
        <small v-if="source.byteSize">{{ materialSize(source.byteSize) }}</small>
        <code>{{ source.sha256 }}</code>
      </div>
      <a v-if="recipe.officialUrl" :href="recipe.officialUrl" target="_blank" rel="noopener noreferrer">{{ customCommands ? '配置来源' : '软件官方说明' }} ↗</a>
    </details>
  </section>
</template>

<style scoped>
.material-contents { border: 1px solid var(--border); border-radius: 7px; padding: 16px; min-width: 0; }
header, .material-source-name { display: flex; align-items: center; flex-wrap: wrap; gap: 9px; }
header { justify-content: space-between; margin-bottom: 14px; }
h3 { margin: 0; font-size: 13px; }
.material-source { padding: 12px 0; border-top: 1px solid var(--border-subtle, #edf0f4); }
.material-source-name strong { font-size: 13px; font-weight: 500; }
.material-source-name span, .material-source-name small { font-size: 11px; color: var(--muted); }
.material-source-name small { margin-left: auto; }
p { color: var(--muted); font-size: 12px; line-height: 1.8; margin: 6px 0; }
a { color: var(--accent); font-size: 12px; line-height: 1.8; overflow-wrap: anywhere; }
.material-network { padding-top: 7px; }
.source-requirements { margin: 8px 0 0; padding-left: 18px; color: var(--muted); font-size: 12px; line-height: 1.8; }
details { margin-top: 12px; border-top: 1px solid var(--border); padding-top: 12px; }
summary { font-size: 12px; color: var(--muted); cursor: pointer; }
.material-file { margin-top: 14px; overflow-wrap: anywhere; }
.material-file strong, .material-file small { font-size: 11px; margin-right: 10px; }
code { display: block; margin-top: 6px; font: 11px/1.7 var(--mono); overflow-wrap: anywhere; color: var(--muted); }
</style>
