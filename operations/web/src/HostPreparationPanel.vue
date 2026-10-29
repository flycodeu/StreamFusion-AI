<script setup lang="ts">
import { computed, onUnmounted, ref, watch } from "vue";
import { ApiError, request } from "./api";
import { hostTime, type Host } from "./hosts";
interface PreparationJob {
  id: string; status: string; phase: string; errorCode: string;
  message?: string; traceId: string; updatedAt: string;
  result?: { message?: string };
}
interface HostPreparation {
  hostId: string; expectedHostVersion: string; controllerId: string;
  targetId: string; installRoot: string; account: string; platform: string;
  package: { available: boolean; code: string; message: string; fileName: string; sha256: string; byteSize: number; downloadUrl?: string };
  commands: { preview: string; apply: string; upgrade: string; authorize?: string };
  authorization: { scope: string; command: string };
  steps: { key: string; label: string; status: string; code: string; message: string }[];
  remoteJob?: PreparationJob;
}
const props = defineProps<{ host: Host }>();
const emit = defineEmits<{ checked: [] }>();
const data = ref<HostPreparation>();
const busy = ref(false);
const mode = ref<"online" | "offline">("online");
const confirmed = ref(false);
const sudoPassword = ref("");
const installing = ref(false);
const error = ref<{ message: string; code: string; at: string; traceId: string; source?: "poll" }>();
const checkResult = ref<{ ready: boolean; message: string; code: string; at: string }>();
const copied = ref("");
const job = computed(() => data.value?.remoteJob);
const blocked = computed(() => ["RUNNING", "UNKNOWN"].includes(job.value?.status || ""));
const phaseLabels: Record<string, string> = {
  CONNECTING: "正在连接并检查服务器", UPLOADING_EXECUTOR: "正在传输安装材料",
  INSTALLING_EXECUTOR: "正在安装并配置权限", VERIFYING_EXECUTOR: "正在检查接入结果",
  PREPARATION_RESULT: "安装结果",
};
let generation = 0;
let hostGeneration = 0;
let operation = 0;
let pollFailures = 0;
let installationIntent: { key: string; previousJobId?: string; jobId?: string } | undefined;
let polling: ReturnType<typeof setTimeout> | undefined;
let installAbort: AbortController | undefined;
function report(cause: unknown, source?: "poll") {
  error.value = { message: cause instanceof Error ? cause.message : "操作未完成，请刷新安装记录",
    code: cause instanceof ApiError ? cause.code : "REQUEST_NOT_CONFIRMED",
    at: cause instanceof ApiError ? cause.timestamp : new Date().toISOString(),
    traceId: cause instanceof ApiError ? cause.traceId : "", source };
}
function beginOperation() {
  busy.value = true;
  error.value = undefined;
  return { host: hostGeneration, id: ++operation };
}
function currentOperation(value: { host: number; id: number }) {
  return value.host === hostGeneration && value.id === operation;
}
function observeJob(value?: PreparationJob) {
  if (!value || !installationIntent) return;
  if (value.id !== installationIntent.previousJobId) installationIntent.jobId = value.id;
  if (value.id === installationIntent.jobId && ["SUCCEEDED", "FAILED"].includes(value.status)) installationIntent = undefined;
}
function schedulePoll(delay = 1500) {
  clearTimeout(polling);
  const revision = hostGeneration;
  if (job.value?.status === "RUNNING" || installing.value) polling = setTimeout(() => {
    if (revision === hostGeneration) void load(true);
  }, delay);
}
async function load(quiet = false) {
  const revision = ++generation;
  const hostRevision = hostGeneration;
  const hostId = props.host.id;
  const active = quiet ? undefined : beginOperation();
  try {
    const value = await request<HostPreparation>(`/hosts/${hostId}/preparation`);
    if (revision !== generation || hostRevision !== hostGeneration) return;
    data.value = value;
    observeJob(value.remoteJob);
    pollFailures = 0;
    if (error.value?.source === "poll") error.value = undefined;
    schedulePoll();
  } catch (cause) {
    if (revision !== generation || hostRevision !== hostGeneration) return;
    if (quiet && (job.value?.status === "RUNNING" || installing.value)) {
      report(cause, "poll");
      pollFailures++;
      if (pollFailures < 3) {
        error.value!.message += `；安装进度将在稍后重试（${pollFailures}/3）`;
        schedulePoll(1500 * 2 ** pollFailures);
      } else {
        error.value!.message += "；进度刷新已暂停，请点击刷新获取最新安装状态";
      }
    } else report(cause);
  } finally {
    if (active && currentOperation(active)) busy.value = false;
  }
}
watch([() => props.host.id, () => props.host.version], () => {
  hostGeneration++; generation++; operation++;
  installAbort?.abort(); clearTimeout(polling); data.value = undefined;
  busy.value = false; installing.value = false; pollFailures = 0; installationIntent = undefined;
  checkResult.value = undefined; copied.value = "";
  confirmed.value = false; sudoPassword.value = ""; error.value = undefined;
  mode.value = props.host.kind === "SSH" ? "online" : "offline";
  void load();
}, { immediate: true });
onUnmounted(() => { generation++; hostGeneration++; clearTimeout(polling); installAbort?.abort(); sudoPassword.value = ""; });
async function copy(label: string, text: string) {
  const revision = hostGeneration;
  try { await navigator.clipboard.writeText(text); if (revision === hostGeneration) copied.value = label; }
  catch (cause) { if (revision === hostGeneration) report(cause); }
}
async function download() {
  if (!data.value?.package.available || !data.value.package.downloadUrl || busy.value) return;
  const active = beginOperation();
  const hostId = props.host.id;
  const { downloadUrl, fileName } = data.value.package;
  try {
    const target = new URL(downloadUrl, location.origin);
    if (target.origin !== location.origin || target.pathname !== `/ops/api/v1/hosts/${hostId}/preparation/download`)
      throw new Error("安装包地址不匹配当前主机，请刷新后重试");
    const response = await fetch(target, { credentials: "same-origin", signal: AbortSignal.timeout(60000) });
    if (!response.ok) { const body = await response.json(); throw new ApiError(body.msg || "安装包下载未完成", body.code, body.traceId, body.timestamp); }
    const blob = await response.blob();
    if (!currentOperation(active)) return;
    if (!blob.size) throw new Error("安装包为空，请检查发行材料");
    const url = URL.createObjectURL(blob);
    const anchor = document.createElement("a"); anchor.href = url;
    anchor.download = fileName.replace(/[\\/]/g, "_"); anchor.click(); setTimeout(() => URL.revokeObjectURL(url), 0);
  } catch (cause) { if (currentOperation(active)) report(cause); }
  finally { if (currentOperation(active)) busy.value = false; }
}
async function install() {
  if (!data.value || busy.value || blocked.value || !confirmed.value) return;
  const hostId = props.host.id;
  const active = beginOperation();
  installing.value = true; pollFailures = 0; checkResult.value = undefined;
  installAbort = new AbortController();
  installationIntent ??= { key: crypto.randomUUID(), previousJobId: job.value?.id };
  const input = { expectedVersion: data.value.expectedHostVersion, idempotencyKey: installationIntent.key, confirmed: true, sudoPassword: sudoPassword.value };
  sudoPassword.value = ""; schedulePoll(1000);
  try {
    const result = await request<PreparationJob>(`/hosts/${hostId}/preparation/install`, "POST", input, installAbort.signal);
    if (!currentOperation(active) || !data.value) return;
    data.value.remoteJob = result;
    observeJob(result);
    if (result.status === "SUCCEEDED") emit("checked");
  } catch (cause) { if (currentOperation(active)) report(cause); }
  finally {
    input.sudoPassword = "";
    if (currentOperation(active)) {
      installing.value = false; installAbort = undefined;
      clearTimeout(polling);
      await load(true);
      if (currentOperation(active)) busy.value = false;
    }
  }
}
async function reconcile() {
  if (!data.value || !job.value || busy.value) return;
  const active = beginOperation();
  const hostId = props.host.id;
  const input = { jobId: job.value.id, expectedVersion: data.value.expectedHostVersion };
  try {
    await request(`/hosts/${hostId}/preparation/reconcile`, "POST", input);
    if (!currentOperation(active)) return;
    emit("checked"); await load(true);
  } catch (cause) { if (currentOperation(active)) report(cause); }
  finally { if (currentOperation(active)) busy.value = false; }
}
async function check() {
  if (busy.value) return;
  const active = beginOperation();
  const hostId = props.host.id;
  checkResult.value = undefined;
  try {
    const checked = await request<Host>(`/hosts/${hostId}/checks`, "POST", {});
    if (!currentOperation(active)) return;
    const capabilities = checked.capabilities;
    const facts = [capabilities?.reachability, capabilities?.readOnly, capabilities?.mutation];
    const unready = facts.find((fact) => fact?.status !== "AVAILABLE");
    const ready = facts.every((fact) => fact?.status === "AVAILABLE");
    checkResult.value = { ready, message: ready ? "主机接入完成，可以继续安装软件。" : unready?.message || "接入状态尚未确认，请查看接入检查。",
      code: ready ? "HOST_PREPARATION_READY" : unready?.code || "HOST_CHECK_REQUIRED", at: checked.observedAt || new Date().toISOString() };
    emit("checked"); await load(true);
  } catch (cause) { if (currentOperation(active)) report(cause); }
  finally { if (currentOperation(active)) busy.value = false; }
}
</script>

<template>
  <div class="host-preparation">
    <header><div><h3>接入服务器</h3><p>选择一种安装方式，完成后即可在 Ops 中安装和管理软件。</p></div><button class="quiet" :disabled="busy" @click="load()">刷新</button></header>
    <div v-if="error" class="preparation-error" role="alert"><strong>{{ error.message }}</strong><small>{{ error.code }} · {{ hostTime(error.at) }}</small><code>hostId={{ host.id }}</code><code v-if="error.traceId">traceId={{ error.traceId }}</code></div>
    <p v-if="!data && busy">读取安装信息…</p>
    <template v-if="data">
      <div class="mode-switch" role="group" aria-label="安装方式">
        <button :class="{ selected: mode === 'online' }" :aria-pressed="mode === 'online'" :disabled="busy || host.kind !== 'SSH'" @click="mode = 'online'">远程安装 <small>通过 SSH 自动完成</small></button>
        <button :class="{ selected: mode === 'offline' }" :aria-pressed="mode === 'offline'" :disabled="busy" @click="mode = 'offline'">离线安装 <small>上传完整安装包</small></button>
      </div>
      <section v-if="mode === 'online'" class="install-card">
        <h3>连接服务器，直接安装</h3><p>Ops 使用已保存的 SSH 连接传输材料并安装，目标服务器无需访问外网。</p>
        <dl><dt>目标服务器</dt><dd>{{ host.address }}:{{ host.sshPort }}</dd><dt>连接账号</dt><dd>{{ data.account }}</dd><dt>安装目录</dt><dd><code>{{ data.installRoot }}</code></dd></dl>
        <form @submit.prevent="install">
          <label v-if="data.account !== 'root'" class="password-field">sudo 密码 <span>仅用于本次安装；已有免密管理员权限可留空</span><input v-model="sudoPassword" type="password" autocomplete="off" maxlength="1024" :disabled="busy || blocked" aria-label="本次安装的 sudo 密码" /></label>
          <label class="confirm-line"><input v-model="confirmed" type="checkbox" :disabled="busy || blocked" />允许在此服务器安装接入组件并配置该账号的受限操作权限</label>
          <button class="primary" type="submit" :disabled="busy || blocked || !confirmed || !data.package.available || host.maintenance">{{ busy ? '正在处理…' : '连接并安装' }}</button>
        </form><p v-if="installing || job?.status === 'RUNNING'">安装期间请保持此页面打开；若中断，重新进入后核对安装结果。</p><p v-if="host.maintenance">主机正在维护，请先退出维护模式。</p>
      </section>
      <section v-else class="install-card">
        <header><h3>上传一次，按提示安装</h3><button class="primary" :disabled="busy || !data.package.available" @click="download">下载离线安装包</button></header>
        <p>适用主机：{{ host.name }} · {{ host.address || '控制器本机' }} · {{ data.account }} · <code>{{ data.installRoot }}</code></p>
        <ol class="offline-steps">
          <li><strong>下载 Linux amd64 安装包</strong><p>包含当前主机的接入信息和安装程序。</p></li>
          <li><strong>上传并解压</strong><p>上传到服务器当前账号的主目录，用文件管理器解压。主目录通常有写入权限，可避免向 /opt 上传失败。</p></li>
          <li><strong>进入解压目录，运行安装</strong><div class="single-command"><code>bash install.sh</code><button class="quiet" @click="copy('install', 'bash install.sh')">{{ copied === 'install' ? '已复制' : '复制' }}</button></div><p>按提示确认，普通账号输入 sudo 密码。安装程序自动完成文件校验、接入和权限配置。</p></li>
          <li><strong>回到 Ops 检查结果</strong><p>接入组件由 SSH 按需调用，无需另行启动常驻服务。安装 MySQL 等软件时，再选择对应的软件离线材料。</p></li>
        </ol><button class="secondary" :disabled="busy" @click="check">检查接入结果</button>
      </section>
      <p v-if="!data.package.available" class="material-error" role="alert">{{ data.package.message }} <code>{{ data.package.code }}</code></p>
      <section v-if="checkResult" class="job-result" :class="{ success: checkResult.ready, attention: !checkResult.ready }" aria-live="polite">
        <strong>{{ checkResult.ready ? '接入检查通过' : '主机接入尚未就绪' }}</strong><p>{{ checkResult.message }}</p><small>{{ hostTime(checkResult.at) }}</small><code v-if="!checkResult.ready">{{ checkResult.code }}</code>
      </section>
      <section v-if="job" class="job-result" :class="{ success: job.status === 'SUCCEEDED', attention: ['FAILED','UNKNOWN'].includes(job.status) }" aria-live="polite">
        <strong>{{ job.status === 'SUCCEEDED' ? '主机接入完成' : job.status === 'RUNNING' ? phaseLabels[job.phase] || '安装进行中' : job.status === 'UNKNOWN' ? '需要核对安装结果' : '本次安装未完成' }}</strong>
        <p>{{ job.message || job.result?.message }}</p><small>{{ hostTime(job.updatedAt) }} · jobId={{ job.id }}</small><code v-if="job.errorCode && job.status !== 'SUCCEEDED'">{{ job.errorCode }} · traceId={{ job.traceId }}</code>
        <button v-if="job.status === 'UNKNOWN'" class="secondary" :disabled="busy" @click="reconcile">核对安装结果</button><button v-if="job.status === 'SUCCEEDED' && mode === 'online'" class="secondary" :disabled="busy" @click="check">检查接入结果</button>
      </section>
      <details class="diagnostics"><summary>接入检查与高级维护</summary>
        <div class="preparation-steps"><article v-for="step in data.steps" :key="step.key"><strong>{{ step.label }}</strong><span :class="['state-tag', step.status === 'AVAILABLE' ? 'state-success' : 'state-muted']">{{ step.status === 'AVAILABLE' ? '已就绪' : step.status === 'UNAVAILABLE' ? '待处理' : '待检查' }}</span><p>{{ step.message }}</p><code>{{ step.code }}</code></article></div>
        <button class="secondary" :disabled="busy" @click="check">重新检查主机能力</button>
        <dl><dt>系统架构</dt><dd>{{ data.platform }}（当前支持）</dd><dt>包文件</dt><dd>{{ data.package.fileName }}</dd><dt>执行器大小</dt><dd>{{ (data.package.byteSize / 1024 / 1024).toFixed(1) }} MiB</dd><dt>SHA-256</dt><dd><code>{{ data.package.sha256 }}</code></dd></dl>
        <div class="command-block"><header><strong>仅查看安装信息</strong><button class="quiet" :disabled="!data.commands.preview" @click="copy('preview', data.commands.preview)">{{ copied === 'preview' ? '已复制' : '复制' }}</button></header><pre>{{ data.commands.preview }}</pre></div>
        <p>授权范围：{{ data.authorization.scope }}</p><code>{{ data.authorization.command }}</code><code>controllerId={{ data.controllerId }}</code><code>targetId={{ data.targetId }}</code>
      </details>
    </template>
  </div>
</template>

<style scoped>
.host-preparation { display:grid; gap:20px; padding-top:12px; min-width:0; }
.host-preparation header { display:flex; align-items:center; justify-content:space-between; gap:14px; }
.host-preparation>header>div { min-width:0; }
.host-preparation>header>.quiet { flex-shrink:0; white-space:nowrap; }
h3 { font-size:14px; margin:0; }
p { font-size:12px; line-height:1.8; color:var(--muted); margin:8px 0 0; }
code { font:11px/1.8 var(--mono); overflow-wrap:anywhere; }
small { font-size:11px; color:var(--muted); overflow-wrap:anywhere; }
.mode-switch { display:grid; grid-template-columns:1fr 1fr; gap:10px; }
.mode-switch button { display:grid; gap:6px; text-align:left; background:var(--surface,#fff); border:1px solid var(--border); border-radius:6px; padding:16px; color:var(--text); }
.mode-switch button.selected { border-color:var(--accent,#315a97); box-shadow:inset 3px 0 var(--accent,#315a97); }
.mode-switch button:focus-visible { outline:2px solid var(--accent,#315a97); outline-offset:2px; }
.install-card,.diagnostics,.job-result { border:1px solid var(--border); border-radius:6px; padding:20px; min-width:0; }
dl { display:grid; grid-template-columns:85px minmax(0,1fr); gap:10px; font-size:12px; margin:18px 0; }
dt { color:var(--muted); } dd { margin:0; overflow-wrap:anywhere; }
form { display:grid; gap:18px; margin-top:18px; } form>.primary { justify-self:start; }
.password-field { display:grid; gap:8px; font-size:12px; }
.password-field span { font-size:11px; color:var(--muted); }
.password-field input { max-width:340px; width:100%; min-width:0; box-sizing:border-box; }
.confirm-line { display:flex; align-items:flex-start; gap:8px; font-size:12px; line-height:1.7; }
.confirm-line input { width:auto; flex-shrink:0; margin-top:4px; }
.offline-steps { padding-left:22px; margin:22px 0; display:grid; gap:20px; font-size:12px; }
.offline-steps li::marker { color:var(--muted); font-weight:600; }
.single-command { display:flex; align-items:center; justify-content:space-between; margin-top:10px; padding:8px 12px; background:var(--surface-soft,#f6f8fb); border:1px solid var(--border); border-radius:5px; }
.job-result { display:grid; gap:10px; font-size:12px; } .job-result .secondary { justify-self:start; }
.job-result.success { border-left:3px solid #478362; } .job-result.attention { border-left:3px solid #ba8642; }
.preparation-error,.material-error { display:grid; gap:8px; padding:14px; background:#fff5f3; border:1px solid #efd2c9; border-radius:6px; font-size:12px; }
.diagnostics summary { cursor:pointer; font-size:12px; color:var(--muted); }
.preparation-steps { display:grid; grid-template-columns:1fr 1fr; gap:12px; margin:20px 0; }
.preparation-steps article { min-width:0; padding:12px; border:1px solid var(--border); border-radius:4px; font-size:12px; }
.preparation-steps .state-tag { margin-left:8px; }
.command-block { margin-top:18px; font-size:12px; }
.command-block pre { white-space:pre-wrap; overflow-wrap:anywhere; padding:12px; background:#f6f8fb; border:1px solid var(--border); border-radius:5px; font:11px/1.9 var(--mono); }
@media(max-width:550px) { .mode-switch,.preparation-steps { grid-template-columns:1fr; } .install-card,.diagnostics,.job-result { padding:14px; } .install-card>header { align-items:flex-start; flex-direction:column; } }
</style>
