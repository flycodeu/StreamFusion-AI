<script setup lang="ts">
import { computed, onUnmounted, ref, watch } from "vue";
import { ElMessageBox } from "element-plus";
import { ApiError, request } from "./api";
import ErrorHelpLink from "./ErrorHelpLink.vue";
import { nodeRegistryURLValid, type NodeEnvironment, type NodeRegistry } from "./nodeEnvironment";
const props = defineProps<{ environment: NodeEnvironment; disabled: boolean }>();
const emit = defineEmits<{ select: [NodeRegistry]; closeState: [state: { busy: boolean; dirty: boolean }] }>();
const entries = ref<NodeRegistry[]>([]);
const busy = ref(false);
const name = ref("");
const editing = ref<NodeRegistry>();
function cancelEdit() { editing.value = undefined; name.value = ""; url.value = ""; }
function edit(entry: NodeRegistry) { editing.value = entry; name.value = entry.name; url.value = entry.url; error.value = undefined; }
const url = ref("");
const error = ref<{ message: string; code: string; traceId: string }>();
let generation = 0;
const writable = computed(() => props.environment.managementMode === "MANAGED" && !props.disabled);
const observed = computed(() => props.environment.observation?.registry);
const registryStatus = computed(() => ({ CONFIGURED: "当前账号镜像", DEFAULT: "账号未设置", REDACTED: "地址含受保护配置", UNSAFE: "配置文件不可安全读取", UNAVAILABLE: props.environment.observation?.versions.length ? "npm 配置暂不可读" : "请先安装 Node" }[observed.value?.status || ""] || "尚未检测"));
const currentURL = computed(() => ["CONFIGURED", "DEFAULT"].includes(observed.value?.status || "") ? observed.value?.url : "");
watch(() => [busy.value, name.value, url.value], () => emit("closeState", { busy: busy.value, dirty: !!(name.value || url.value) }), { immediate: true });
async function act(action: () => Promise<void>) {
  if (busy.value) return;
  const current = generation;
  busy.value = true; error.value = undefined;
  try { await action(); }
  catch (cause) { if (current === generation) error.value = { message: cause instanceof Error ? cause.message : "请求未完成", code: cause instanceof ApiError ? cause.code : "REQUEST_NOT_CONFIRMED", traceId: cause instanceof ApiError ? cause.traceId : "" }; }
  finally { if (current === generation) busy.value = false; }
}
async function refresh() {
  const current = generation;
  const result = await request<NodeRegistry[]>("/node-registries");
  if (current === generation) entries.value = result;
}
async function add() {
  if (!name.value.trim() || !nodeRegistryURLValid(url.value)) return;
  const current = generation;
  await act(async () => {
    await request(editing.value ? `/node-registries/${editing.value.id}` : "/node-registries", editing.value ? "PATCH" : "POST", { name: name.value.trim(), url: url.value, ...(editing.value ? { expectedVersion: editing.value.version } : {}) });
    if (current !== generation) return;
    cancelEdit(); await refresh();
  });
}
async function remove(entry: NodeRegistry) {
  const current = generation;
  try { await ElMessageBox.confirm(`删除“${entry.name}”？服务器当前地址不变。`, "删除镜像地址", { confirmButtonText: "删除", cancelButtonText: "取消", type: "warning" }); }
  catch { return; }
  if (current !== generation) return;
  await act(async () => { await request(`/node-registries/${entry.id}`, "DELETE", { expectedVersion: entry.version }); if (current === generation) await refresh(); });
}
watch(() => props.environment.id, () => { generation++; busy.value = false; cancelEdit(); void act(refresh); }, { immediate: true });
onUnmounted(() => { generation++; emit("closeState", { busy: false, dirty: false }); });
</script>

<template>
  <section class="node-registries" aria-label="npm 镜像管理">
    <div class="current-registry">
      <strong>账号 {{ environment.account }} · ~/.npmrc</strong>
      <span>{{ registryStatus }}</span><code v-if="currentURL">{{ currentURL }}</code>
      <details><summary>配置范围</summary><code v-if="observed">{{ observed.status }}</code><p>同一账号的 Node 版本共用此配置；项目、环境变量及命令参数可覆盖它。未设置时仍可能使用全局配置。仅修改 registry，保留其他配置与认证。</p><code v-if="observed?.configPath">{{ observed.configPath }}</code></details>
    </div>
    <div class="registry-heading"><strong>平台地址</strong><button class="quiet" :disabled="busy" @click="act(refresh)">刷新列表</button></div>
    <article v-for="entry in entries" :key="entry.id" class="registry-row">
      <div><strong>{{ entry.name }}</strong><code>{{ entry.url }}</code></div>
      <div class="registry-actions">
        <span v-if="observed?.status === 'DEFAULT' && currentURL === entry.url" class="current-tag">默认</span>
        <span v-if="observed?.status === 'CONFIGURED' && currentURL === entry.url" class="current-tag">当前</span>
        <button v-else class="secondary" :disabled="busy || !writable" @click="emit('select', entry)">切换</button>
        <button v-if="!entry.builtIn" class="quiet" :disabled="busy || disabled" @click="edit(entry)">编辑</button>
        <button v-if="!entry.builtIn" class="quiet" :disabled="busy || disabled" @click="remove(entry)">删除</button>
      </div>
    </article>
    <form class="registry-add" @submit.prevent="add">
      <label>名称<input v-model="name" aria-label="npm 镜像名称" maxlength="80" :disabled="busy || disabled" /></label>
      <label>地址<input v-model="url" aria-label="npm 镜像地址" maxlength="2048" placeholder="https://registry.example.com/npm/" :disabled="busy || disabled" /></label>
      <button class="secondary" :disabled="busy || disabled || !name.trim() || !nodeRegistryURLValid(url)">{{ editing ? '保存修改' : '添加地址' }}</button>
      <button v-if="editing" type="button" class="quiet" :disabled="busy" @click="cancelEdit">取消编辑</button>
    </form>
    <p v-if="url && !nodeRegistryURLValid(url)" class="hint">使用 http/https 地址，不含账号密码、查询参数或片段。</p>
    <p v-if="error" role="alert">{{ error.message }}</p>
    <details v-if="error"><summary>诊断信息</summary><ErrorHelpLink :code="error.code" /><code>{{ error.code }} · {{ error.traceId }}</code></details>
  </section>
</template>

<style scoped>
.node-registries,.current-registry { display:grid; gap:12px; min-width:0; }
.current-registry { padding:16px; background:var(--background); border:1px solid var(--border); border-radius:6px; }
.registry-heading,.registry-row,.registry-actions { display:flex; align-items:center; justify-content:space-between; gap:12px; }
.registry-row { padding:12px 0; border-bottom:1px solid var(--border); }
.registry-row > div:first-child { min-width:0; display:grid; gap:7px; }
.registry-actions { flex-shrink:0; }
.registry-add { display:grid; grid-template-columns:minmax(120px,1fr) minmax(180px,2fr) auto; gap:12px; align-items:end; }
label { display:grid; gap:7px; font-size:13px; }
input { min-width:0; width:100%; box-sizing:border-box; padding:9px; border:1px solid var(--border); border-radius:5px; }
code { font:12px/1.7 var(--mono); overflow-wrap:anywhere; }
summary { cursor:pointer; font-size:12px; color:var(--muted); }
p,.current-registry > span,.current-tag { font-size:12px; color:var(--muted); margin:0; line-height:1.8; }
@media(max-width:640px) { .registry-add { grid-template-columns:1fr; } .registry-row { align-items:start; flex-direction:column; } }
</style>
