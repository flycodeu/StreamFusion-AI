<script setup lang="ts">
import { computed, onUnmounted, ref, watch } from "vue";
import { ApiError, request } from "./api";
import { type Host } from "./hosts";
interface PreparationJob {
  id: string; status: string; phase: string; errorCode: string;
  message?: string; traceId: string; createdAt?: string; updatedAt: string;
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
const sudoPassword = ref("");
const needsSudoPassword = ref(false);
const installing = ref(false);
const updateRequested = ref(false);
const repairing = ref(false);
const error = ref<{ message: string; source?: "poll" }>();
const checkResult = ref<{ ready: boolean; title: string; message: string; code: string }>();
const copied = ref("");
const job = computed(() => data.value?.remoteJob);
const blocked = computed(() => ["RUNNING", "UNKNOWN"].includes(job.value?.status || ""));
const recoverableCodes = ["EXECUTOR_PATH_UNSAFE", "EXECUTOR_PATH_UNREADABLE", "EXECUTOR_HASH_UNAVAILABLE", "EXECUTOR_MISSING", "EXECUTOR_NOT_EXECUTABLE", "PREPARATION_SUDO_REQUIRED"];
const canRecover = computed(() => job.value?.status === "UNKNOWN" && recoverableCodes.includes(checkResult.value?.code || ""));
const missingFiles = computed(() => checkResult.value?.code === "EXECUTOR_MISSING");
const phaseLabels: Record<string, string> = {
  CONNECTING: "连接服务器", UPLOADING_EXECUTOR: "传输安装材料",
  INSTALLING_EXECUTOR: "安装接入组件", VERIFYING_EXECUTOR: "验证接入结果",
};
const phaseOrder = ["CONNECTING", "UPLOADING_EXECUTOR", "INSTALLING_EXECUTOR", "VERIFYING_EXECUTOR"];
const visibleStatus = computed(() => job.value?.status || (installing.value ? "RUNNING" : ""));
const statusTone = computed(() => ({ RUNNING: "running", UNKNOWN: "unknown", SUCCEEDED: "success", FAILED: "failed" }[visibleStatus.value] || "running"));
const statusLabel = computed(() => ({ RUNNING: "执行中", UNKNOWN: "待确认", SUCCEEDED: "已完成", FAILED: "未完成" }[visibleStatus.value] || "执行中"));
const currentPhaseIndex = computed(() => phaseOrder.indexOf(job.value?.phase || ""));
const showPhaseTrack = computed(() => Boolean(visibleStatus.value));
const statusTitle = computed(() => {
  if (!job.value) return "正在创建安装任务";
  if (job.value.status === "RUNNING") return phaseLabels[job.value.phase] || "正在处理安装任务";
  if (job.value.status === "UNKNOWN") return "安装结果待确认";
  if (job.value.status === "SUCCEEDED") return "服务器已接入";
  return "服务器未接入";
});
const statusSummary = computed(() => {
  if (!job.value) return "正在建立安全连接。";
  if (job.value.status === "RUNNING") return "当前进度会自动更新。";
  if (job.value.status === "UNKNOWN") {
    return "请检查服务器上的安装结果。";
  }
  if (job.value.status === "SUCCEEDED") return "接入组件已安装并通过检查。";
  return "本次安装未完成，请根据下方提示处理后重试。";
});
const phaseNote = computed(() => {
  if (!job.value) return "";
  const label = phaseLabels[job.value.phase];
  if (!label && job.value.status === "UNKNOWN") return "历史进度不完整，正在以服务器实际状态为准";
  if (!label) return "";
  if (job.value.status === "UNKNOWN") return `最后确认到：${label}`;
  if (job.value.status === "FAILED") return `停止于：${label}`;
  if (job.value.status === "SUCCEEDED") return "四个接入步骤均已完成";
  return `当前阶段：${label}`;
});
function phaseState(index: number) {
  if (visibleStatus.value === "SUCCEEDED") return "done";
  if (currentPhaseIndex.value < 0) return "pending";
  if (index < currentPhaseIndex.value) return "done";
  if (index > currentPhaseIndex.value) return "pending";
  if (visibleStatus.value === "UNKNOWN") return "uncertain";
  if (visibleStatus.value === "FAILED") return "failed";
  return "active";
}
let generation = 0;
let hostGeneration = 0;
let operation = 0;
let pollFailures = 0;
let installationIntent: { key: string; previousJobId?: string; jobId?: string } | undefined;
let polling: ReturnType<typeof setTimeout> | undefined;
let installAbort: AbortController | undefined;
function report(cause: unknown, source?: "poll") {
  error.value = { message: cause instanceof Error ? cause.message : "操作未完成，请重新加载", source };
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
    const refreshStatus = value.remoteJob?.status === "SUCCEEDED" && job.value?.status !== "SUCCEEDED";
    data.value = value;
    if (value.remoteJob?.errorCode === "PREPARATION_SUDO_REQUIRED") needsSudoPassword.value = true;
    if (value.remoteJob?.status === "SUCCEEDED") needsSudoPassword.value = false;
    observeJob(value.remoteJob);
    pollFailures = 0;
    if (error.value?.source === "poll") error.value = undefined;
    schedulePoll();
    if (refreshStatus) await refreshHostStatus(hostRevision);
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
  busy.value = false; installing.value = false; repairing.value = false; pollFailures = 0; installationIntent = undefined;
  updateRequested.value = false;
  checkResult.value = undefined; copied.value = "";
  sudoPassword.value = ""; needsSudoPassword.value = false; error.value = undefined;
  mode.value = props.host.kind === "SSH" ? "online" : "offline";
  void load();
}, { immediate: true });
onUnmounted(() => { generation++; hostGeneration++; clearTimeout(polling); installAbort?.abort(); sudoPassword.value = ""; });
async function refreshHostStatus(revision = hostGeneration) {
  try {
    await request<Host>(`/hosts/${props.host.id}/checks`, "POST", {});
    if (revision === hostGeneration) emit("checked");
  } catch (cause) {
    if (revision === hostGeneration) report(cause);
  }
}
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
function openUpdate() {
  if (busy.value || blocked.value || job.value?.status !== "SUCCEEDED") return;
  updateRequested.value = true;
}
async function install() {
  if (!data.value || busy.value || blocked.value) return;
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
    if (result.status === "SUCCEEDED") {
      needsSudoPassword.value = false;
      updateRequested.value = false;
      await refreshHostStatus(active.host);
    }
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
    const result = await request<PreparationJob>(`/hosts/${hostId}/preparation/reconcile`, "POST", input);
    if (!currentOperation(active)) return;
    if (data.value) data.value.remoteJob = result;
    checkResult.value = undefined;
    await refreshHostStatus(active.host); await load(true);
  } catch (cause) {
    if (!currentOperation(active)) return;
    if (cause instanceof ApiError) {
      const titles: Record<string, string> = {
        EXECUTOR_PATH_UNSAFE: "接入目录权限不符合要求",
        EXECUTOR_IDENTITY_MISMATCH: "接入目录归属不匹配",
        EXECUTOR_HASH_MISMATCH: "接入文件版本不一致",
        EXECUTOR_UPGRADE_REQUIRED: "接入文件版本不一致",
        EXECUTOR_HASH_UNAVAILABLE: "接入文件不可读取",
        EXECUTOR_MISSING: "接入组件尚未安装",
        EXECUTOR_PATH_UNREADABLE: "接入目录不可读取",
        EXECUTOR_NOT_EXECUTABLE: "接入文件不可执行",
        SUDO_AUTHORIZATION_REQUIRED: "服务器授权未完成",
        SSH_UNAVAILABLE: "服务器连接失败",
        SSH_TIMEOUT: "服务器检查超时",
      };
      const messages: Record<string, string> = {
        EXECUTOR_PATH_UNSAFE: "接入目录权限有误，点击下方按钮修复。",
        EXECUTOR_MISSING: "点击下方按钮恢复安装。",
        SUDO_AUTHORIZATION_REQUIRED: "请使用 root 账号，或提供当前连接账号的 sudo 权限后再试。",
      };
      checkResult.value = { ready: false, title: titles[cause.code] || "安装结果仍待确认", message: messages[cause.code] || cause.message, code: cause.code };
    } else report(cause);
  }
  finally { if (currentOperation(active)) busy.value = false; }
}
async function repair() {
  if (!data.value || !job.value || busy.value || !canRecover.value) return;
  const active = beginOperation();
  const hostId = props.host.id;
  repairing.value = true;
  installAbort = new AbortController();
  const input = { jobId: job.value.id, expectedVersion: data.value.expectedHostVersion, confirmed: true, sudoPassword: sudoPassword.value };
  sudoPassword.value = "";
  try {
    const result = await request<PreparationJob>(`/hosts/${hostId}/preparation/repair`, "POST", input, installAbort.signal);
    if (!currentOperation(active) || !data.value) return;
    data.value.remoteJob = result;
    needsSudoPassword.value = false;
    checkResult.value = undefined;
    await refreshHostStatus(active.host);
  } catch (cause) {
    if (!currentOperation(active)) return;
    if (cause instanceof ApiError) {
      const titles: Record<string, string> = {
        PREPARATION_SUDO_REQUIRED: "请输入登录账号的密码",
        PREPARATION_REPAIR_UNCONFIRMED: "修复结果待确认",
        PREPARATION_CONNECT_FAILED: "服务器连接失败",
        VERSION_CONFLICT: "服务器配置已变化",
        EXECUTOR_RESPONSE_INVALID: "安装结果检查未完成",
      };
      if (cause.code === "PREPARATION_SUDO_REQUIRED") needsSudoPassword.value = true;
      checkResult.value = { ready: false, title: titles[cause.code] || "权限修复未完成", message: cause.message, code: cause.code };
    } else report(cause);
  } finally {
    input.sudoPassword = "";
    if (currentOperation(active)) {
      repairing.value = false; installAbort = undefined;
      await load(true);
      if (currentOperation(active)) busy.value = false;
    }
  }
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
    checkResult.value = { ready, title: ready ? "服务器已接入" : "服务器尚未接入", message: ready ? "现在可以继续安装和管理软件。" : unready?.message || "暂时无法确认接入状态，请重新检查。",
      code: unready?.code || "" };
    emit("checked"); await load(true);
  } catch (cause) { if (currentOperation(active)) report(cause); }
  finally { if (currentOperation(active)) busy.value = false; }
}
</script>

<template>
  <div class="host-preparation">
    <header class="preparation-heading"><h3>服务器接入</h3><button class="quiet" :disabled="busy" @click="load()">重新加载</button></header>
    <div v-if="error" class="preparation-error" role="alert"><strong>页面暂时未更新</strong><p>{{ error.message }}</p><button class="secondary" :disabled="busy" @click="load()">重新加载</button></div>
    <p v-if="!data && busy">读取安装信息…</p>
    <template v-if="data">
      <section class="target-overview" aria-label="安装信息">
        <div class="install-location"><span>安装位置</span><code>{{ data.installRoot }}</code></div>
        <div class="target-meta"><span><small>服务器</small>{{ host.address || '控制器本机' }}<template v-if="host.sshPort">:{{ host.sshPort }}</template></span><span><small>安装权限</small>{{ data.account === 'root' ? 'root' : `${data.account} + sudo` }}</span></div>
      </section>
      <section v-if="visibleStatus" class="operation-status" :class="`status-${statusTone}`" aria-live="polite">
        <div class="status-heading"><h3>{{ statusTitle }}</h3><span class="state-tag" :class="`state-${statusTone === 'success' ? 'success' : statusTone === 'failed' ? 'error' : 'muted'}`">{{ statusLabel }}</span></div>
        <p class="status-summary">{{ statusSummary }}</p>
        <div v-if="showPhaseTrack" class="phase-track" aria-label="接入进度">
          <div v-for="(phase, index) in phaseOrder" :key="phase" class="phase-item" :class="`phase-${phaseState(index)}`"><span class="phase-dot">{{ phaseState(index) === 'done' ? '✓' : index + 1 }}</span><span>{{ phaseLabels[phase] }}</span></div>
        </div>
        <p v-if="phaseNote && ['UNKNOWN', 'FAILED'].includes(visibleStatus)" class="phase-note">{{ phaseNote }}</p>
        <div class="status-actions">
          <button v-if="job?.status === 'UNKNOWN'" class="primary" :disabled="busy" @click="reconcile">{{ busy ? '正在检查…' : '检查安装结果' }}</button>
          <button v-if="job?.status === 'SUCCEEDED'" class="secondary" :disabled="busy" @click="check">重新检查</button>
          <button v-if="job?.status === 'SUCCEEDED' && !updateRequested" class="secondary" :disabled="busy || !data.package.available || host.maintenance" @click="openUpdate">更新接入组件</button>
          <button v-if="job?.status === 'RUNNING'" class="secondary" :disabled="busy" @click="load()">立即刷新进度</button>
        </div>
      </section>
      <section v-if="checkResult" class="verification-result" :class="{ success: checkResult.ready, attention: !checkResult.ready }" aria-live="polite">
        <div class="verification-copy"><strong>{{ checkResult.title }}</strong><p>{{ checkResult.message }}</p>
          <div v-if="canRecover" class="repair-panel">
            <label v-if="data.account !== 'root' && needsSudoPassword" class="password-field">{{ data.account }} 的登录密码 <span>用于 sudo 提权，仅本次使用</span><input v-model="sudoPassword" type="password" autocomplete="off" maxlength="1024" :disabled="busy" :aria-label="`${data.account} 的登录密码`" placeholder="请输入该账号的系统密码" /></label>
            <div class="repair-actions"><button class="primary" :disabled="busy" @click="repair">{{ repairing ? '正在恢复并检查…' : missingFiles ? '恢复接入并继续安装' : '修复权限并继续安装' }}</button><button class="quiet" :disabled="busy" @click="reconcile">只重新检查</button></div>
          </div>
        </div>
        <button v-if="!checkResult.ready && job?.status === 'UNKNOWN' && !canRecover" class="secondary" :disabled="busy" @click="reconcile">重新检查</button>
      </section>
      <section v-if="!blocked && (job?.status !== 'SUCCEEDED' || updateRequested)" class="install-workspace">
        <header class="install-heading"><div><span class="section-kicker">{{ updateRequested ? '接入组件更新' : mode === 'online' ? '在线安装' : '离线安装' }}</span><h3>{{ updateRequested ? '更新此服务器的接入组件' : mode === 'online' ? '一键安装到服务器' : '下载后运行一条命令' }}</h3><p>{{ updateRequested ? '新软件配方和卸载功能需要匹配的接入组件。确认后会传输当前版本并检查结果；业务软件和数据保留。' : mode === 'online' ? 'Ops 会连接服务器、上传材料、安装并自动检查结果。' : '适合服务器无法由 Ops 直接连接的情况。' }}</p></div><div class="method-switch"><button :class="{ active: mode === 'online' }" :disabled="busy || host.kind !== 'SSH'" @click="mode = 'online'">在线</button><button :class="{ active: mode === 'offline' }" :disabled="busy" @click="mode = 'offline'">离线</button></div></header>
        <div v-if="mode === 'online'" class="install-card">
          <form @submit.prevent="install">
            <label v-if="data.account !== 'root' && needsSudoPassword" class="password-field">{{ data.account }} 的登录密码 <span>用于 sudo 提权，仅本次使用</span><input v-model="sudoPassword" type="password" autocomplete="off" maxlength="1024" :disabled="busy" :aria-label="`${data.account} 的登录密码`" placeholder="请输入该账号的系统密码" /></label>
            <div class="install-action"><button class="primary" type="submit" :disabled="busy || !data.package.available || host.maintenance">{{ busy ? '正在处理…' : updateRequested ? '确认更新接入组件' : '开始安装' }}</button><button v-if="updateRequested" class="quiet" type="button" :disabled="busy" @click="updateRequested = false">取消更新</button></div>
          </form><p v-if="host.maintenance" class="maintenance-note">主机正在维护，请先退出维护模式。</p>
        </div>
        <div v-else class="install-card offline-card">
          <div class="offline-intro"><p>下载后上传到服务器、解压，并在解压目录运行：</p><button class="secondary" :disabled="busy || !data.package.available" @click="download">下载离线安装包</button></div>
          <div class="single-command"><code>bash install.sh</code><button class="quiet" @click="copy('install', 'bash install.sh')">{{ copied === 'install' ? '已复制' : '复制命令' }}</button></div>
          <p>命令会请求 root 或 sudo 权限，并自动识别首次安装、同一服务器升级或授权恢复。</p>
          <button class="secondary check-button" :disabled="busy" @click="check">检查安装结果</button>
        </div>
      </section>
      <p v-if="!data.package.available" class="material-error" role="alert">{{ data.package.message }}</p>
    </template>
  </div>
</template>

<style scoped>
.host-preparation { display:grid; gap:16px; padding:8px 0 6px; min-width:0; color:var(--text); }
.host-preparation header { display:flex; align-items:center; justify-content:space-between; gap:16px; }
.preparation-heading>div { min-width:0; }
.preparation-heading>.quiet { flex-shrink:0; white-space:nowrap; }
h3 { font-size:15px; line-height:1.5; margin:0; }
p { font-size:12px; line-height:1.75; color:var(--muted); margin:7px 0 0; }
code { font:11px/1.8 var(--mono); overflow-wrap:anywhere; }
small { font-size:11px; line-height:1.6; color:var(--muted); overflow-wrap:anywhere; }
.target-overview { display:grid; grid-template-columns:minmax(0,1.65fr) minmax(240px,1fr); gap:0; overflow:hidden; border:1px solid var(--border); border-radius:8px; background:#f8fafc; }
.install-location { display:grid; gap:7px; min-width:0; padding:16px 18px; border-right:1px solid var(--border); }
.install-location>span,.target-meta small,.section-kicker { font-size:10px; font-weight:700; line-height:1.4; letter-spacing:.08em; color:#718096; text-transform:uppercase; }
.install-location code { font-size:13px; color:#1f3a5f; }
.target-meta { display:grid; grid-template-columns:1fr 1fr; min-width:0; }
.target-meta>span { display:grid; gap:5px; align-content:center; min-width:0; padding:14px 16px; font-size:12px; color:#2d3c50; overflow-wrap:anywhere; }
.target-meta>span+span { border-left:1px solid var(--border); }
.operation-status { --status:#315a97; display:grid; gap:13px; padding:18px 20px; border:1px solid var(--border); border-left:3px solid var(--status); border-radius:8px; background:var(--surface,#fff); box-shadow:var(--card-shadow); }
.status-running { --status:#315a97; }
.status-unknown { --status:#a66b18; }
.status-success { --status:#397454; }
.status-failed { --status:#a0443d; }
.status-heading { display:flex; align-items:center; justify-content:space-between!important; gap:10px; }
.status-heading h3 { font-size:15px; }
.status-summary { margin:0; color:var(--text-secondary,#475569); }
.phase-track { display:grid; grid-template-columns:repeat(4,minmax(0,1fr)); gap:0; padding-top:3px; }
.phase-item { display:grid; grid-template-columns:24px minmax(0,1fr); align-items:center; gap:7px; position:relative; min-width:0; color:#8792a2; font-size:11px; }
.phase-item::after { content:""; position:absolute; left:24px; right:7px; top:11px; height:1px; background:#d7dde5; }
.phase-item:last-child::after { display:none; }
.phase-item>span:last-child { position:relative; z-index:1; width:max-content; max-width:calc(100% - 4px); padding-right:7px; background:#fff; }
.phase-dot { display:grid; place-items:center; position:relative; z-index:1; width:22px; height:22px; border:1px solid #cbd3dd; border-radius:50%; background:#fff; font:600 10px/1 var(--mono); }
.phase-done,.phase-active,.phase-uncertain,.phase-failed { color:#243247; font-weight:600; }
.phase-done .phase-dot { border-color:var(--status); background:var(--status); color:#fff; }
.phase-active .phase-dot { border:5px solid color-mix(in srgb,var(--status) 22%,#fff); background:var(--status); box-shadow:0 0 0 1px var(--status); color:transparent; }
.phase-uncertain .phase-dot,.phase-failed .phase-dot { border-color:var(--status); background:#fff; color:var(--status); }
.phase-note { margin:0; color:var(--status); font-weight:600; }
.status-actions { display:flex; align-items:center; gap:10px; flex-wrap:wrap; }
.install-workspace { display:grid; gap:18px; padding:20px; border:1px solid var(--border); border-radius:8px; min-width:0; background:#fff; }
.install-heading { align-items:flex-start!important; }
.install-heading>div:first-child { min-width:0; }
.install-heading h3 { margin-top:4px; font-size:16px; }
.method-switch { display:flex; flex-shrink:0; padding:3px; border-radius:6px; background:#eef2f6; }
.method-switch button { min-width:58px; padding:7px 10px; border:0; border-radius:4px; background:transparent; color:#64748b; font-size:11px; }
.method-switch button.active { background:#fff; color:#26364b; box-shadow:0 1px 3px #0f172a18; font-weight:700; }
.install-card { display:grid; gap:16px; min-width:0; }
form { display:grid; gap:17px; }
.password-field { display:grid; gap:7px; max-width:430px; font-size:12px; font-weight:600; }
.password-field span { font-size:11px; font-weight:400; color:var(--muted); }
.password-field input { width:100%; min-width:0; box-sizing:border-box; }
.install-action { display:flex; align-items:center; gap:12px; flex-wrap:wrap; }.install-action span { font-size:11px; color:var(--muted); }
.maintenance-note { color:#946619; }
.offline-intro { display:flex; align-items:center; justify-content:space-between; gap:14px; }
.offline-intro p { margin:0; }
.single-command { display:flex; align-items:center; justify-content:space-between; padding:10px 12px; background:#f4f7fa; border:1px solid var(--border); border-radius:6px; }
.single-command code { font-size:13px; color:#1e3a5f; }
.check-button { justify-self:start; }
.verification-result { display:flex; align-items:flex-start; justify-content:space-between; gap:18px; padding:14px 16px; border-left:3px solid #ba8642; background:#fafafa; border-radius:4px; font-size:12px; }
.verification-result.success { border-left-color:#478362; }.verification-result p { margin-top:3px; }
.verification-copy { flex:1; min-width:0; }
.repair-panel { display:grid; gap:13px; max-width:720px; margin-top:14px; padding:15px; border:1px solid #e8d4b4; border-radius:7px; background:#fffaf1; }
.repair-panel p { color:#66543a; }
.repair-panel .password-field { max-width:420px; }
.repair-actions { display:flex; align-items:center; gap:10px; flex-wrap:wrap; }
.preparation-error,.material-error { display:grid; gap:7px; padding:14px 16px; background:#fff5f3; border:1px solid #efd2c9; border-radius:6px; font-size:12px; }
.preparation-error p { margin:0; }.preparation-error button { justify-self:start; }
@media(max-width:720px) { .target-overview { grid-template-columns:1fr; }.install-location { border-right:0; border-bottom:1px solid var(--border); }.phase-track { grid-template-columns:1fr 1fr; gap:12px 0; }.phase-item:nth-child(2)::after { display:none; } }
@media(max-width:550px) { .host-preparation { gap:14px; }.preparation-heading,.install-heading { align-items:flex-start!important; }.operation-status,.install-workspace { padding:16px; }.status-heading h3 { font-size:15px; }.status-summary { font-size:12px; }.install-heading { flex-direction:column; }.method-switch { width:100%; }.method-switch button { flex:1; }.offline-intro,.verification-result { align-items:flex-start; flex-direction:column; gap:9px; }.target-meta { grid-template-columns:1fr; }.target-meta>span+span { border-left:0; border-top:1px solid var(--border); } }
</style>
