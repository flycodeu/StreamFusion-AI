<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref } from "vue";
import { ElDialog, ElMessage } from "element-plus";
import { ApiError, request } from "./api";
import type { JenkinsMonitor } from "./projectJenkins";

const props = defineProps<{ projectId: string; archived: boolean }>();
const emit = defineEmits<{ error: [unknown]; install: [] }>();
const lifetime = new AbortController();
const connection = ref<JenkinsMonitor>();
const open = ref(false), saving = ref(false), reading = ref(false);
const readError = ref(""), saveError = ref("");
const softwareId = ref(""), username = ref(""), token = ref("");
const endpoint = () => `/application-projects/${props.projectId}/jenkins-connection`;
const selected = computed(() => connection.value?.instances.find(item => item.id === connection.value?.softwareId));
const reuseToken = computed(() => !!connection.value?.hasToken && connection.value.softwareId === softwareId.value && connection.value.username === username.value.trim());
const valid = computed(() => !!softwareId.value && !!username.value.trim() && (reuseToken.value || !!token.value.trim()));

function explain(error: unknown, fallback: string) {
  if (error instanceof ApiError) {
    if (error.code === "AUTH_REQUIRED") emit("error", error);
    return [error.message, error.code, error.timestamp, error.traceId].filter(Boolean).join(" · ");
  }
  return fallback;
}
async function refresh() {
  if (reading.value || saving.value) return;
  reading.value = true;
  readError.value = "";
  try {
    const data = await request<JenkinsMonitor>(endpoint(), "GET", undefined, lifetime.signal);
    if (!lifetime.signal.aborted) connection.value = data;
  } catch (error) {
    if (!lifetime.signal.aborted) readError.value = explain(error, "无法读取项目 Jenkins 设置");
  } finally { reading.value = false; }
}
function edit() {
  const current = connection.value;
  if (!current || props.archived || saving.value) return;
  softwareId.value = current.softwareId || (current.instances.length === 1 ? current.instances[0].id : "");
  username.value = current.username || "";
  token.value = "";
  saveError.value = "";
  open.value = true;
}
async function save() {
  if (!connection.value || props.archived || !valid.value || saving.value) return;
  saving.value = true;
  saveError.value = "";
  const input = { expectedVersion: connection.value.version, softwareId: softwareId.value, username: username.value.trim(), token: token.value.trim() };
  try {
    const data = await request<JenkinsMonitor>(endpoint(), "POST", input, lifetime.signal);
    if (lifetime.signal.aborted) return;
    connection.value = data;
    open.value = false;
    token.value = "";
    ElMessage.success("项目 Jenkins 已保存");
  } catch (error) {
    if (!lifetime.signal.aborted) saveError.value = explain(error, "无法保存项目 Jenkins 设置");
  } finally { input.token = ""; saving.value = false; }
}
onMounted(refresh);
onUnmounted(() => { lifetime.abort(); token.value = ""; });
</script>

<template>
  <section class="jenkins-setting" aria-label="项目 Jenkins">
    <span class="setting-name">Jenkins</span>
    <template v-if="readError"><span class="setting-error" role="alert">{{ readError }}</span><button type="button" @click="refresh">重试</button></template>
    <span v-else-if="!connection || reading" role="status">正在读取…</span>
    <template v-else>
      <span v-if="connection.configured">{{ selected ? selected.name + " · " + selected.hostName : "已配置" }}</span>
      <span v-else class="setting-name">未设置</span>
      <button v-if="!archived" type="button" @click="edit">{{ connection.configured ? "更改 Jenkins" : "设置 Jenkins" }}</button>
    </template>
  </section>
  <ElDialog v-model="open" title="项目 Jenkins" width="min(450px, 94vw)" append-to-body destroy-on-close :close-on-click-modal="false" :close-on-press-escape="!saving" :show-close="!saving" @close="token = ''">
    <form v-if="connection" class="jenkins-form" @submit.prevent="save">
      <p class="muted">此项目的模块共用该连接。QA 按模块类型生成流水线。</p>
      <template v-if="connection.instances.length">
        <label>Jenkins 实例<select v-model="softwareId" required :disabled="saving">
          <option value="" disabled>选择 Jenkins</option>
          <option v-for="item in connection.instances" :key="item.id" :value="item.id">{{ item.name }} · {{ item.hostName }}</option>
        </select></label>
        <label>Jenkins 用户名<input v-model="username" required maxlength="128" autocomplete="off" :disabled="saving" /></label>
        <label>API Token<input v-model="token" type="password" :required="!reuseToken" maxlength="256" autocomplete="new-password" :disabled="saving" :placeholder="reuseToken ? '已保存，留空继续使用' : '填写该账号的 API Token'" /></label>
        <p class="muted">Jenkins 账号需能读取、创建、配置并构建项目作业。</p>
      </template>
      <p v-else>暂无可用的 Jenkins 实例。<button type="button" class="quiet" @click="open = false; emit('install')">安装 Jenkins</button></p>
      <p v-if="saveError" class="setting-error" role="alert">{{ saveError }}</p>
      <footer><button type="button" class="secondary" :disabled="saving" @click="open = false">取消</button>
        <button v-if="connection.instances.length" type="submit" class="primary" :disabled="saving || !valid">{{ saving ? "保存中…" : "保存" }}</button></footer>
    </form>
  </ElDialog>
</template>

<style scoped>
.jenkins-setting { display:flex; align-items:baseline; flex-wrap:wrap; gap:4px 8px; font-size:12px; }
.setting-name,.muted { color:var(--muted); }
.jenkins-setting button { font:inherit; color:var(--accent); background:transparent; padding:0 2px; border:0; }
.jenkins-setting button:hover { text-decoration:underline; }
.jenkins-form { display:grid; gap:14px; }
.jenkins-form label { display:grid; gap:6px; }
.jenkins-form input,.jenkins-form select { width:100%; margin:0; }
.jenkins-form p { margin:0; line-height:1.5; font-size:13px; }
.jenkins-form footer { display:flex; justify-content:flex-end; gap:8px; }
.setting-error { color:#a62c22; overflow-wrap:anywhere; }
</style>
