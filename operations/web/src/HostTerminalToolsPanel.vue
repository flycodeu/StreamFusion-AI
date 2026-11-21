<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref } from "vue";
import { ApiError, request } from "./api";
import { hostTime, type Host } from "./hosts";
import { softwareJobStatusLabel, type SoftwareJob } from "./software";
import TerminalCommandEntry from "./TerminalCommandEntry.vue";

interface ToolsSnapshot {
  hostId: string;
  expectedVersion: string;
  downloadUrl: string;
  onlineAvailable: boolean;
  job?: SoftwareJob & { message?: string };
}
interface InstallIntent { expectedVersion: string; idempotencyKey: string }

const props = defineProps<{ host: Host; adminId: string }>();
const path = `/hosts/${props.host.id}/terminal-tools`;
const storageKey = "ops.terminal-tools.pending." + props.adminId + "." + props.host.id;
const data = ref<ToolsSnapshot>();
const job = computed(() => data.value?.job);
const activeJob = computed(() => ["RUNNING", "QUEUED", "UNKNOWN"].includes(job.value?.status || ""));
const pending = ref<InstallIntent>();
const canReconcile = computed(() => job.value?.status === "UNKNOWN" && !pending.value);
const showJobDiagnostics = computed(() => {
  const current = job.value;
  return !!current && (current.status === "FAILED" || current.status === "UNKNOWN") && !!(current.errorCode || current.traceId);
});
const confirming = ref("");
const busy = ref(false);
const refreshing = ref(false);
const password = ref("");
const loadError = ref<Error>();
const actionError = ref<Error>();
const error = computed(() => actionError.value || loadError.value);
const diagnostic = computed(() => error.value instanceof ApiError ? error.value : undefined);
const readSignal = new AbortController();
let closed = false;
let readRevision = 0;
let timer: ReturnType<typeof setTimeout> | undefined;

const download = computed(() => {
  if (!data.value?.downloadUrl) return "";
  const expected = "/ops/api/v1" + path + "/download";
  return data.value.downloadUrl === expected ? expected : "";
});

async function refresh() {
  const revision = ++readRevision;
  clearTimeout(timer);
  refreshing.value = true;
  try {
    const snapshot = await request<ToolsSnapshot>(path, "GET", undefined, readSignal.signal);
    if (closed || revision !== readRevision) return;
    if (snapshot.hostId !== props.host.id || !snapshot.expectedVersion) throw new Error("主机信息已变化，请重新读取");
    loadError.value = undefined;
    if (confirming.value && (confirming.value !== snapshot.expectedVersion || !snapshot.onlineAvailable)) {
      confirming.value = "";
      password.value = "";
    }
    if (pending.value && !sessionStorage.getItem(storageKey)) pending.value = undefined;
    data.value = snapshot;
    if (snapshot.job?.status === "RUNNING" || snapshot.job?.status === "QUEUED" || busy.value)
      timer = setTimeout(refresh, 2500);
  } catch (cause) {
    if (!closed && revision === readRevision) loadError.value = cause instanceof Error ? cause : new Error("无法读取主机终端工具状态");
  } finally {
    if (!closed && revision === readRevision) refreshing.value = false;
  }
}

function remember(intent: InstallIntent) {
  pending.value = intent;
  sessionStorage.setItem(storageKey, JSON.stringify(intent));
}
function forget(intent: InstallIntent) {
  // A response can arrive after the dialog has closed and a new one has opened.
  if (sessionStorage.getItem(storageKey) === JSON.stringify(intent)) sessionStorage.removeItem(storageKey);
  if (pending.value?.idempotencyKey === intent.idempotencyKey) pending.value = undefined;
}

async function submit() {
  if (!data.value || busy.value || (!pending.value && (activeJob.value || !data.value.onlineAvailable || props.host.maintenance))) return;
  if (!pending.value && confirming.value !== data.value.expectedVersion) return;
  const retrying = !!pending.value;
  const intent = pending.value || { expectedVersion: confirming.value, idempotencyKey: crypto.randomUUID() };
  busy.value = true;
  confirming.value = "";
  actionError.value = undefined;
  clearTimeout(timer);
  timer = setTimeout(refresh, 1000);
  const suppliedPassword = password.value;
  password.value = "";
  try {
    remember(intent);
    const receipt = await request<{ jobId: string }>(path + "/install", "POST", {
      ...intent, confirmed: true, ...(suppliedPassword ? { sudoPassword: suppliedPassword } : {}),
    });
    if (!receipt.jobId) throw new Error("安装回执不完整，请核对原提交。");
    forget(intent);
  } catch (cause) {
    // A rejected challenge, expired session or busy controller says nothing about an earlier submission.
    if (cause instanceof ApiError && cause.status && cause.status >= 400 && cause.status < 500 &&
        (!retrying || ["HOST_BUSY", "HOST_MAINTENANCE", "VERSION_CONFLICT", "IDEMPOTENCY_CONFLICT"].includes(cause.code))) forget(intent);
    if (!closed) actionError.value = cause instanceof Error ? cause : new Error("无法确认终端工具维护结果");
  } finally {
    if (!closed) { busy.value = false; await refresh(); }
  }
}

async function reconcile() {
  if (!data.value || job.value?.status !== "UNKNOWN" || busy.value || pending.value) return;
  busy.value = true;
  actionError.value = undefined;
  try {
    await request(path + "/reconcile", "POST", { expectedVersion: data.value.expectedVersion, jobId: job.value.id });
  } catch (cause) {
    if (!closed) actionError.value = cause instanceof Error ? cause : new Error("无法核对终端工具作业");
  } finally {
    if (!closed) { busy.value = false; await refresh(); }
  }
}

onMounted(() => {
  const saved = sessionStorage.getItem(storageKey);
  if (saved) {
    try {
      const value = JSON.parse(saved) as InstallIntent;
      if (typeof value.expectedVersion === "string" && typeof value.idempotencyKey === "string") pending.value = value;
    } catch { sessionStorage.removeItem(storageKey); }
  }
  void refresh();
});
onUnmounted(() => { closed = true; readSignal.abort(); clearTimeout(timer); password.value = ""; });
</script>

<template>
  <section class="host-terminal-tools" aria-label="主机终端工具">
    <p>Ubuntu 24.04 的接入组件会自动准备 <code>mysql</code>、<code>redis-cli</code>、<code>psql</code>、<code>curl</code> 和 <code>sf-docker</code>。主机上的命令缺失时，可在这里检查并修复。</p>
    <p v-if="!data && refreshing" role="status">正在读取主机状态…</p>
    <template v-if="data">
      <div class="terminal-actions">
        <button v-if="!confirming && !pending && data.onlineAvailable" type="button" class="secondary" :disabled="busy || activeJob || host.maintenance" @click="confirming = data.expectedVersion">检查并修复工具</button>
        <a v-if="download" class="secondary" :href="download" download="install-sf-docker.sh">下载安装脚本</a>
        <button type="button" class="quiet" :disabled="refreshing" @click="refresh">刷新作业记录</button>
      </div>
      <form v-if="confirming" class="terminal-confirm" @submit.prevent="submit">
        <p>检查 <strong>{{ host.name }}</strong> 的终端命令。APT 会从该主机的软件源补装缺少的客户端，并按需更新运行依赖。</p>
        <label v-if="host.sshUser !== 'root'">sudo 密码（按需填写）<input v-model="password" type="password" autocomplete="off" placeholder="免密 sudo 可留空" /></label>
        <div class="terminal-actions"><button class="primary" type="submit" :disabled="busy || activeJob || !data.onlineAvailable || host.maintenance">确认修复</button><button class="quiet" type="button" @click="confirming = ''; password = ''">取消</button></div>
      </form>
      <p v-if="busy" role="status">正在与主机核对安装作业…</p>
      <div v-else-if="pending" class="terminal-actions">
        <span>有一次提交尚待确认。</span><button class="secondary" @click="submit">核对原提交</button>
      </div>
      <div v-if="job" class="terminal-job">
        <div class="terminal-result" role="status">
          <div class="terminal-job-heading">
            <strong>最近一次手动维护 · {{ softwareJobStatusLabel(job.status) }}</strong>
            <time v-if="job.updatedAt" :datetime="job.updatedAt">{{ hostTime(job.updatedAt) }}</time>
          </div>
          <p v-if="job.message" class="terminal-job-message">{{ job.message }}</p>
        </div>
        <div v-if="showJobDiagnostics || canReconcile" class="terminal-actions">
          <button v-if="canReconcile" class="quiet" :disabled="busy" @click="reconcile">核对原作业</button>
          <details v-if="showJobDiagnostics" class="terminal-diagnostics">
            <summary>诊断信息</summary>
            <code v-if="job.errorCode">{{ job.errorCode }}</code>
            <code v-if="job.traceId">{{ job.traceId }}</code>
          </details>
        </div>
      </div>
      <TerminalCommandEntry label="在主机手动修复" command="sudo bash ./install-sf-docker.sh" note="将下载的脚本放到目标服务器，在所在目录执行。" />
      <TerminalCommandEntry label="查看本机容器" command="sf-docker list" note="用有本机 Docker 访问权限的账号列出容器和镜像。用 sf-docker ports 容器名 查看发布端口。" />
      <TerminalCommandEntry label="查看终端命令用法" command="sf-docker --help" note="列出数据库连接、日志、容器内执行命令，以及使用 Java、Maven、Python 和自定义镜像的示例。" />
    </template>
    <div v-if="error" class="terminal-error">
      <p role="alert">{{ error.message }}</p>
      <details v-if="diagnostic && (diagnostic.code || diagnostic.timestamp || diagnostic.traceId)" class="terminal-diagnostics">
        <summary>请求诊断</summary>
        <code v-if="diagnostic.code">{{ diagnostic.code }}</code>
        <time v-if="diagnostic.timestamp" :datetime="diagnostic.timestamp">{{ hostTime(diagnostic.timestamp) }}</time>
        <code v-if="diagnostic.traceId">{{ diagnostic.traceId }}</code>
      </details>
      <button v-if="!data" type="button" class="quiet" :disabled="refreshing" @click="refresh">重新读取</button>
    </div>
  </section>
</template>

<style scoped>
.host-terminal-tools { display: grid; gap: 14px; margin-top: 16px; }
.host-terminal-tools p { margin: 0; line-height: 1.6; }
.terminal-actions, .terminal-job-heading { display: flex; flex-wrap: wrap; align-items: center; gap: 8px 12px; }
.terminal-actions .primary { width: auto; margin: 0; }
.terminal-actions a { text-decoration: none; }
.terminal-confirm { display: grid; gap: 12px; padding: 14px; border: 1px solid var(--border); border-radius: 6px; }
.terminal-confirm label { display: grid; gap: 6px; }
.terminal-job { display: grid; gap: 8px; padding: 12px 14px; border: 1px solid var(--border); border-radius: 6px; }
.terminal-result { display: grid; gap: 4px; }
.terminal-job-heading time, .terminal-diagnostics { color: var(--muted); font-size: 12px; }
.terminal-job-message, .terminal-diagnostics { overflow-wrap: anywhere; }
.terminal-diagnostics summary { cursor: pointer; }
.terminal-diagnostics code, .terminal-diagnostics time { display: block; margin-top: 4px; }
.terminal-error .terminal-diagnostics { margin-top: 6px; }
</style>
