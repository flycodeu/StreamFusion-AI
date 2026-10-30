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
const sources = computed<MaterialSource[]>(() => {
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
        {{ material?.available ? "本地材料已就绪" : online ? "需要在线获取" : "本地材料未就绪" }}
      </span>
    </header>
    <div v-for="source in sources" :key="source.name" class="material-source">
      <div class="material-source-name">
        <strong>{{ source.displayName || source.name }}</strong>
        <span v-if="source.version">{{ source.version }}</span>
        <small>{{ source.role === "DEPENDENCY" ? "依赖组件" : runtimeMode === "DOCKER" ? "容器镜像" : "软件主件" }}</small>
      </div>
      <p>发布方：{{ source.publisher || recipe.publisher || "以下载来源说明为准" }}</p>
      <a :href="source.url" target="_blank" rel="noopener noreferrer">
        {{ sourceHost(source.url) }} · {{ source.displayName || source.name }} 下载来源 ↗
      </a>
      <ul v-if="source.requirements?.length" class="source-requirements" aria-label="目标主机依赖">
        <li v-for="requirement in source.requirements" :key="requirement">{{ requirement }}</li>
      </ul>
    </div>
    <p class="material-network">
      {{ material?.available ? `${materialOriginLabel(material)}，将从 Ops 分发；目标服务器无需外网下载。` :
        online && runtimeMode === "DOCKER" ? "目标服务器需要联网拉取固定镜像。" :
        online ? "Ops 需要联网获取固定材料，入库后可重复使用；目标服务器无需外网下载。" :
        "请先准备匹配当前版本和运行方式的材料，再继续安装。" }}
    </p>
    <p v-if="recipe.key === 'docker'" class="material-network">
      使用 Docker 官方 DEB 软件包安装主机 Engine（含 containerd 等依赖）。数据目录使用本实例 data；主机配置写入 /etc/docker/daemon.json，服务日志由 docker.service 管理。不会自动将用户加入 docker 用户组。
    </p>
    <p v-if="runtimeMode === 'NATIVE' && recipe.native?.requiredTools?.length" class="material-network">
      目标机还需具备：{{ recipe.native.requiredTools.map((tool) => toolLabels[tool] || tool).join("、") }}；安装预检会检查。
    </p>
    <details>
      <summary>文件与校验详情 · {{ recipe.platform }}</summary>
      <div v-for="source in sources" :key="source.name" class="material-file">
        <strong>{{ source.name }}</strong>
        <small v-if="source.byteSize">{{ materialSize(source.byteSize) }}</small>
        <code>{{ source.sha256 }}</code>
      </div>
      <p>{{ runtimeMode === "DOCKER" ? "校验固定镜像身份。" : "核对材料内容与固定配方 SHA-256 一致。" }}</p>
      <a :href="recipe.officialUrl" target="_blank" rel="noopener noreferrer">软件官方说明 ↗</a>
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
