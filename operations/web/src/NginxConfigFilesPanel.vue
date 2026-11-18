<script setup lang="ts">
import { computed, onUnmounted, ref, watch } from "vue";
import { ElMessageBox } from "element-plus";
import { ApiError, request } from "./api";
import { softwareJobErrorMessages, softwareJobStatusLabel, type SoftwareInstance, type SoftwareJob, type SoftwareMutationResult } from "./software";
import type { JobSummary } from "./jobs";
import ConfigurationTextEditor from "./ConfigurationTextEditor.vue";

interface ConfigFile { id: string; name: string; path: string; enabled: boolean; managed: boolean; editable: boolean; revision?: string; message?: string; }
interface ConfigFileIndex { files?: ConfigFile[]; canCreate?: boolean; status?: string; message?: string; listen?: string; revision?: string; }
interface ConfigFileContent { content?: string; revision?: string; editable?: boolean; status?: string; message?: string; }
interface CheckResult { status: "PASSED" | "FAILED"; message?: string; line?: number; planId?: string; loaded?: boolean; }
type Action = "save" | "enable" | "disable" | "delete";
type ConfigurationJob = SoftwareJob & Partial<JobSummary> & { result?: { error?: string; message?: string; management?: { output?: string } } };
interface FileTaskTracking { softwareId: string; hostId: string; jobId?: string; planId?: string; idempotencyKey?: string; }

const props = defineProps<{ detail: SoftwareInstance; active: boolean; disabled?: boolean }>();
const emit = defineEmits<{ submitted: [SoftwareMutationResult]; closeState: [{ blocked: boolean; dirty: boolean }]; logs: [string]; preparation: [] }>();
const files = ref<ConfigFile[]>([]), selected = ref<ConfigFile>(), canCreate = ref(false), creating = ref(false), registeredListen = ref(""), creationRevision = ref("");
const name = ref(""), content = ref(""), baseline = ref(""), revision = ref(""), readReady = ref(false), search = ref("");
const loading = ref(false), busy = ref(false), error = ref(""), success = ref(""), diagnostics = ref<ApiError>();
const checked = ref<CheckResult>(), checkedInput = ref(""), action = ref<Action>("save"), idempotencyKey = ref(""), submitting = ref(false);
const job = ref<ConfigurationJob>(), ackPaused = ref(false);
const restoring = ref(false), restoreFailed = ref(false);
const jobReadFailed = ref(false);
let restored = false;
let disposed = false, generation = 0, timer: ReturnType<typeof setTimeout> | undefined, abort: AbortController | undefined, ackPolls = 0, jobReadAttempts = 0;
const fileByteLimit = 32 * 1024;
const dirty = computed(() => !pending.value && !submitting.value && readReady.value && (content.value !== baseline.value || creating.value && !!name.value.trim()));
const pending = computed(() => !!job.value && (["QUEUED", "RUNNING", "UNKNOWN"].includes(job.value.status) || job.value.targetAckPending));
const blocked = computed(() => loading.value || busy.value || restoring.value || pending.value || submitting.value);
const writable = computed(() => readReady.value && (creating.value ? canCreate.value : selected.value?.editable) && !props.disabled && !restoreFailed.value);
const visibleFiles = computed(() => files.value.filter(file => `${file.name} ${file.path}`.toLowerCase().includes(search.value.trim().toLowerCase())));
const filename = computed(() => name.value.trim().replace(/\.conf$/, ""));
const validNewName = computed(() => /^[A-Za-z0-9][A-Za-z0-9_-]{0,47}$/.test(filename.value));
const payload = computed(() => ({ name: creating.value ? filename.value : selected.value?.id || filename.value, ...(action.value === "save" ? { content: content.value } : {}), revision: revision.value || "ABSENT", action: action.value }));
const fingerprint = computed(() => JSON.stringify(payload.value));
const validationPassed = computed(() => checked.value?.status === "PASSED" && checked.value.planId && checkedInput.value === fingerprint.value);
const stopped = computed(() => props.detail.actualState === "STOPPED" || props.detail.runtimeKind === "SYSTEMD" && props.detail.actualState === "FAILED");
function userMessage(message: string | undefined, fallback: string) {
  if (!message || !/[\u3400-\u9fff]/u.test(message)) return fallback;
  return message.replace(/[（(][A-Z][A-Z0-9_]{3,}[)）]/g, "").replace(/\b[A-Z][A-Z0-9]*(?:_[A-Z0-9]+)+\b/g, "").trim() || fallback;
}
const failureMessage = computed(() => {
  if (!job.value || !["FAILED", "CANCELLED", "UNKNOWN"].includes(job.value.status)) return "";
  if (job.value.phase === "SUBMITTED") return jobReadFailed.value ? "读取任务状态失败，请重试。" : "正在读取任务状态…";
  if (job.value.phase === "OPERATION_RESTORED") return "配置未生效，已恢复原配置和运行状态。";
  if (job.value.errorCode === "CONFIG_FILE_ROLLBACK_UNCONFIRMED") return "原配置恢复结果尚未确认，请核对原任务。";
  const known = softwareJobErrorMessages[job.value.errorCode];
  if (known) return userMessage(known, "配置操作未完成，请查看执行日志。");
  if (job.value.errorCode.startsWith("CONFIG_FILE_")) return "配置操作未完成，请核对原任务或查看执行日志。";
  const output = job.value.result?.message?.trim() || job.value.result?.error?.trim() || job.value.result?.management?.output?.trim();
  return userMessage(output?.slice(-2000), job.value.status === "UNKNOWN" ? "配置应用结果尚未确认，请核对原任务。" : "配置操作未完成，请查看执行日志");
});

function showError(cause: unknown) { error.value = userMessage(cause instanceof Error ? cause.message : undefined, "配置文件操作未完成，请检查连接后重试。"); diagnostics.value = cause instanceof ApiError ? cause : undefined; }
function showSizeLimit() { error.value = "单个配置文件不能超过 32 KiB"; }
function clearMessages() { error.value = ""; success.value = ""; diagnostics.value = undefined; }
function stopReading() { if (timer) clearTimeout(timer); timer = undefined; abort?.abort(); abort = undefined; }
function reader() { stopReading(); const controller = new AbortController(); abort = controller; return controller; }
async function discard(message: string) { if (!dirty.value) return true; try { await ElMessageBox.confirm(message, "未保存的修改", { confirmButtonText: "放弃修改", cancelButtonText: "继续编辑" }); return true; } catch { return false; } }
const taskStorageKey = (hostId = props.detail.hostId, softwareId = props.detail.id) => `ops:nginx-config-task:${hostId}:${softwareId}`;
function rememberTask(task: Omit<FileTaskTracking, "softwareId" | "hostId">, hostId = props.detail.hostId, softwareId = props.detail.id) {
  try { sessionStorage.setItem(taskStorageKey(hostId, softwareId), JSON.stringify({ ...task, softwareId, hostId })); } catch { /* Tracking still works while this panel remains open. */ }
}
function storedTask(): FileTaskTracking | undefined {
  try {
    const task = JSON.parse(sessionStorage.getItem(taskStorageKey()) || "null") as FileTaskTracking | null;
    if (task && task.softwareId === props.detail.id && task.hostId === props.detail.hostId &&
      (!task.jobId || /^[a-f0-9-]{36}$/.test(task.jobId)) && (!task.planId || /^[a-f0-9-]{36}$/.test(task.planId)) &&
      (!task.idempotencyKey || task.idempotencyKey.length >= 8 && task.idempotencyKey.length <= 80)) return task;
  } catch { /* An invalid browser record cannot identify a task. */ }
  return undefined;
}
function forgetTask(hostId = props.detail.hostId, softwareId = props.detail.id) { try { sessionStorage.removeItem(taskStorageKey(hostId, softwareId)); } catch { /* The server retains the job history. */ } }
function isFileTask(result: ConfigurationJob) { return result.kind === "SOFTWARE_OPERATION" && result.operationKey === "config-file-save" && result.softwareInstanceId === props.detail.id && result.targetId === props.detail.hostId; }
async function restoreTask() {
  if (!props.active || restoring.value) return;
  const current = generation, saved = storedTask(); restoring.value = true; restoreFailed.value = false;
  try {
    if (saved?.planId && saved.idempotencyKey && !saved.jobId) {
      checked.value = { status: "PASSED", planId: saved.planId }; idempotencyKey.value = saved.idempotencyKey; submitting.value = true;
    } else {
      const ids = [...new Set([saved?.jobId, props.detail.lastJobId].filter((id): id is string => !!id))];
      for (const id of ids) {
        const matched = await readJob(id, !!job.value);
        if (disposed || current !== generation || !props.active) return;
        if (matched === undefined) { if (!job.value) { restoreFailed.value = true; return; } break; }
        if (matched && pending.value) break;
        if (!matched && id === saved?.jobId) forgetTask();
      }
    }
    if (!disposed && current === generation) restored = true;
  } finally { if (!disposed && current === generation) restoring.value = false; }
}
async function resume() {
  if (!props.active || disposed) return;
  const current = generation;
  const didRestore = !restored;
  if (didRestore) await restoreTask();
  if (disposed || current !== generation || restoreFailed.value || !props.active) return;
  if (pending.value && job.value && !didRestore) await readJob(job.value.id);
  else if (!submitting.value && !readReady.value && !creating.value) await loadFiles();
}
async function refreshFiles() {
  if (restoreFailed.value) { restored = false; await resume(); }
  else if (jobReadFailed.value && job.value) { jobReadAttempts = 0; await readJob(job.value.id); }
  else await loadFiles();
}

async function readFile(file: ConfigFile, current: number, signal: AbortSignal) {
  if (!file.editable) {
    if (disposed || current !== generation || signal.aborted) return;
    selected.value = file; name.value = file.name; creating.value = false; content.value = baseline.value = ""; revision.value = file.revision || "ABSENT"; readReady.value = true; checked.value = undefined; checkedInput.value = "";
    return;
  }
  const result = await request<ConfigFileContent>(`/software-instances/${props.detail.id}/config-files/${encodeURIComponent(file.id)}`, "GET", undefined, signal);
  if (disposed || current !== generation || signal.aborted) return;
  if (result.status === "FAILED") throw new Error(result.message || "配置文件读取失败");
  const loaded = { ...file, editable: result.editable === true, message: result.message || file.message };
  selected.value = loaded; name.value = file.name; creating.value = false;
  files.value = files.value.map(item => item.id === file.id ? loaded : item);
  content.value = baseline.value = result.content || ""; revision.value = result.revision || file.revision || "ABSENT"; readReady.value = true; checked.value = undefined; checkedInput.value = "";
}
async function loadFiles() {
  if (!props.active || loading.value || busy.value || pending.value || !(await discard("放弃当前文件的修改并重新读取？"))) return;
  if (disposed || !props.active) return;
  const current = ++generation, controller = reader(); loading.value = true; clearMessages();
  try {
    const result = await request<ConfigFileIndex>(`/software-instances/${props.detail.id}/config-files`, "GET", undefined, controller.signal);
    if (disposed || current !== generation || controller.signal.aborted) return;
    if (result.status === "FAILED") throw new Error(result.message || "配置文件列表读取失败");
    files.value = (result.files || []).map(file => ({ ...file, id: file.id || file.name.replace(/\.conf$/, "") })); creationRevision.value = result.revision || ""; canCreate.value = result.canCreate === true && !!creationRevision.value; registeredListen.value = result.listen || "";
    const next = files.value.find(file => file.id === selected.value?.id) || files.value.find(file => file.editable) || files.value[0]; selected.value = undefined; readReady.value = false;
    content.value = baseline.value = ""; creating.value = false; if (next) await readFile(next, current, controller.signal);
  } catch (cause) {
    if (!disposed && current === generation && !controller.signal.aborted) { selected.value = undefined; readReady.value = false; canCreate.value = false; showError(cause); }
  }
  finally { if (!disposed && current === generation) loading.value = false; }
}
async function selectFile(file: ConfigFile) {
  if (blocked.value || readReady.value && selected.value?.id === file.id && !creating.value || !(await discard("放弃当前文件的修改并切换？"))) return;
  if (disposed || !props.active) return;
  const current = ++generation, controller = reader(); loading.value = true; readReady.value = false; clearMessages();
  try { await readFile(file, current, controller.signal); } catch (cause) { if (!disposed && current === generation && !controller.signal.aborted) showError(cause); }
  finally { if (!disposed && current === generation) loading.value = false; }
}
async function newFile() {
  if (blocked.value || props.disabled || !canCreate.value || !(await discard("放弃当前文件的修改并新建？"))) return;
  if (disposed || !props.active) return;
  selected.value = undefined; creating.value = true; readReady.value = true; name.value = ""; revision.value = creationRevision.value; content.value = baseline.value = ""; checked.value = undefined; checkedInput.value = ""; clearMessages();
}
function insertTemplate(kind: "static" | "proxy") {
  if (blocked.value || !writable.value || !creating.value) return;
  const installation = props.detail.installation;
  const address = installation?.config?.bindAddress || "127.0.0.1";
  const listen = registeredListen.value || (props.detail.runtimeKind === "DOCKER" ? `${installation?.definition?.ports?.[0]?.containerPort || 80}` : `${address.includes(":") ? `[${address}]` : address}:${installation?.config?.hostPort || 80}`);
  const webRoot = props.detail.runtimeKind === "DOCKER" ? "/usr/share/nginx/html" : installation?.official ? "/var/www/html" : `${props.detail.installationPaths?.data || `${props.detail.installationPaths?.root || '/opt/nginx'}/data`}/www`;
  const upstream = props.detail.runtimeKind === "DOCKER" ? "http://backend:8080/" : "http://127.0.0.1:8080/";
  const proxy = kind === "proxy" ? `\n    location /api/ {\n        proxy_pass ${upstream};\n        proxy_set_header Host $host;\n        proxy_set_header X-Real-IP $remote_addr;\n        proxy_http_version 1.1;\n        proxy_buffering off;\n        proxy_ignore_headers X-Accel-Redirect;\n    }\n` : "";
  content.value = `server {\n    listen ${listen};\n    server_name example.com;\n\n    root ${webRoot};\n    index index.html;\n    disable_symlinks on;\n    autoindex off;\n\n    location / {\n        try_files $uri $uri/ /index.html;\n    }\n${proxy}}\n`;
}
async function check(nextAction: Action = "save") {
  if (disposed || !props.active || blocked.value || !writable.value) return false;
  if (creating.value && !validNewName.value) { error.value = "文件名须为 1 至 48 位字母、数字、下划线或短横线，可带 .conf 后缀"; return false; }
  if (nextAction === "save" && !content.value.trim()) { error.value = "请输入配置内容"; return false; }
  if (nextAction === "save" && new TextEncoder().encode(content.value).length > fileByteLimit) { showSizeLimit(); return false; }
  action.value = nextAction; clearMessages(); job.value = undefined; jobReadFailed.value = false; jobReadAttempts = 0; ackPolls = 0; ackPaused.value = false; busy.value = true; const current = generation, input = fingerprint.value;
  try {
    const result = await request<CheckResult>(`/software-instances/${props.detail.id}/config-files/check`, "POST", payload.value);
    if (disposed || current !== generation) return false; checked.value = result; checkedInput.value = input;
    if (result.status !== "PASSED") { error.value = userMessage(result.message, "配置检查未通过，请修正后重试"); return false; }
    if (!result.planId) { error.value = "检查结果不完整，请重新检查"; return false; }
    idempotencyKey.value = crypto.randomUUID(); success.value = userMessage(result.message, "配置检查通过"); return true;
  } catch (cause) { if (!disposed && current === generation) showError(cause); return false; }
  finally { if (!disposed && current === generation) busy.value = false; }
}
async function apply() {
  if (disposed || !props.active || busy.value || pending.value || !checked.value?.planId || !idempotencyKey.value || props.disabled && !submitting.value) return;
  const current = generation, { id, hostId } = props.detail, planId = checked.value.planId, key = idempotencyKey.value;
  busy.value = true; submitting.value = true; clearMessages();
  rememberTask({ planId, idempotencyKey: key }, hostId, id);
  try {
    const receipt = await request<SoftwareMutationResult>(`/software-instances/${id}/config-files/apply`, "POST", { planId, idempotencyKey: key });
    if (receipt.jobId && receipt.softwareInstanceId === id) rememberTask({ jobId: receipt.jobId }, hostId, id);
    if (disposed || current !== generation) return; submitting.value = false;
    if (!receipt.jobId || receipt.softwareInstanceId !== id) {
      submitting.value = true; error.value = "提交回执不完整，请核对原提交结果。"; return;
    }
    job.value = { id: receipt.jobId, status: "UNKNOWN", phase: "SUBMITTED", errorCode: "", traceId: "", updatedAt: new Date().toISOString(), targetAckPending: false };
    emit("submitted", receipt); void readJob(receipt.jobId);
  } catch (cause) {
    if (!disposed && current === generation) {
      if (cause instanceof ApiError && cause.status && cause.status >= 400 && cause.status < 500 && cause.status !== 401 && cause.status !== 403) { submitting.value = false; checked.value = undefined; forgetTask(hostId, id); }
      showError(cause);
    }
  }
  finally { if (!disposed && current === generation) busy.value = false; }
}
async function save() {
  if (submitting.value) { await apply(); return; }
  if (!dirty.value || !writable.value || blocked.value) return;
  action.value = "save"; if (!validationPassed.value && !(await check())) return; await apply();
}
async function changeState(nextAction: Exclude<Action, "save">) {
  if (!selected.value?.editable || blocked.value || !writable.value || dirty.value) return;
  if (nextAction === "delete") { try { await ElMessageBox.confirm(`删除配置文件“${selected.value.name}”？`, "删除配置文件", { confirmButtonText: "删除", cancelButtonText: "取消", type: "warning" }); } catch { return; } }
  if (await check(nextAction)) await apply();
}
async function readJob(id: string, preferNewer = false) {
  if (disposed || !props.active) return undefined; const current = generation, controller = reader();
  try {
    const result = await request<ConfigurationJob>(`/jobs/${id}`, "GET", undefined, controller.signal); if (disposed || current !== generation || controller.signal.aborted) return undefined;
    if (!isFileTask(result)) { if (job.value?.id === id) job.value = undefined; return false; }
    if (preferNewer && job.value?.createdAt && result.createdAt && result.createdAt < job.value.createdAt) return false;
    if (jobReadFailed.value) clearMessages();
    jobReadFailed.value = false; jobReadAttempts = 0; job.value = result; rememberTask({ jobId: id });
    if (["QUEUED", "RUNNING"].includes(result.status) || result.targetAckPending && ++ackPolls <= 5) timer = setTimeout(() => void readJob(id), 2000); else if (result.targetAckPending) ackPaused.value = true;
    else if (result.status === "SUCCEEDED") {
      baseline.value = content.value; creating.value = false;
      if (!restoring.value && !busy.value) await loadFiles();
      success.value = stopped.value ? "配置已保存，启动后生效" : "配置操作已完成";
    }
    else if (["FAILED", "CANCELLED"].includes(result.status)) { checked.value = undefined; checkedInput.value = ""; }
    return true;
  } catch (cause) {
    if (disposed || current !== generation || controller.signal.aborted) return undefined;
    if (cause instanceof ApiError && cause.status === 404 && job.value?.id !== id) return false;
    if (job.value?.id === id) {
      jobReadFailed.value = true;
      if (++jobReadAttempts <= 3 && !(cause instanceof ApiError && [401, 403, 404].includes(cause.status || 0))) timer = setTimeout(() => void readJob(id), 3000);
    } else showError(cause);
    return undefined;
  }
}
async function reconcile() {
  if (!job.value || !isFileTask(job.value) || busy.value) return;
  const id = job.value.id, current = generation; busy.value = true; clearMessages();
  try {
    await request(`/jobs/${id}/reconcile`, "POST", {});
    if (disposed || current !== generation) return;
    ackPolls = 0; ackPaused.value = false; busy.value = false; await readJob(id);
  } catch (cause) { if (!disposed && current === generation) showError(cause); }
  finally { if (!disposed && current === generation) busy.value = false; }
}
async function refreshJob() { if (!job.value || busy.value) return; jobReadAttempts = 0; await readJob(job.value.id); }
watch([dirty, submitting], () => emit("closeState", { blocked: false, dirty: dirty.value }), { immediate: true });
watch([name, content], () => { if (!submitting.value) { checked.value = undefined; checkedInput.value = ""; success.value = ""; if (!loading.value) { error.value = ""; diagnostics.value = undefined; } } });
watch([() => props.detail.id, () => props.detail.hostId], () => { generation++; stopReading(); restored = false; restoring.value = restoreFailed.value = false; jobReadFailed.value = false; jobReadAttempts = 0; ackPolls = 0; ackPaused.value = false; files.value = []; selected.value = undefined; creating.value = false; readReady.value = false; content.value = baseline.value = ""; job.value = undefined; checked.value = undefined; submitting.value = false; loading.value = busy.value = false; if (props.active) void resume(); });
watch(() => props.active, active => {
  if (active) {
    void resume();
  } else {
    stopReading(); loading.value = false;
  }
}, { immediate: true });
onUnmounted(() => { disposed = true; generation++; stopReading(); });
</script>

<template>
  <section class="nginx-config-files" aria-label="Nginx 配置文件">
    <header class="files-heading"><h3>站点配置文件</h3><div class="files-actions"><button class="secondary" :disabled="blocked || disabled || !canCreate" @click="newFile">新增文件</button><button class="quiet" :disabled="blocked && !jobReadFailed" @click="refreshFiles">{{ restoreFailed || jobReadFailed ? '重新核对任务' : '重新读取' }}</button></div></header>
    <p v-if="error" class="config-error" role="alert">{{ error }}</p><p v-if="success" class="config-success" role="status">{{ success }}</p>
    <button v-if="diagnostics && ['EXECUTOR_UPGRADE_REQUIRED', 'COMMAND_TRUST_REQUIRED'].includes(diagnostics.code)" class="quiet" @click="emit('preparation')">更新接入组件</button>
    <div v-if="submitting && !readReady" class="config-job"><span>上次配置提交结果尚未确认。</span><button class="primary" :disabled="busy" @click="apply">{{ busy ? '正在核对…' : '核对提交结果' }}</button></div>
    <div class="files-layout">
      <aside class="file-list" aria-label="配置文件列表"><input v-model="search" aria-label="搜索配置文件" placeholder="搜索文件名或路径" /><p v-if="loading && !files.length" class="empty-small">正在读取…</p><button v-for="file in visibleFiles" :key="file.id" type="button" class="file-item" :class="{ selected: !creating && file.id === selected?.id }" :disabled="blocked" @click="selectFile(file)"><strong>{{ file.name }}</strong><small><span :class="file.editable ? 'managed' : 'readonly'">{{ file.editable ? '可编辑' : '只读' }}</span> · {{ file.enabled ? '已启用' : '已停用' }}</small><code :title="file.path">{{ file.path }}</code></button><p v-if="!loading && !visibleFiles.length" class="empty-small">{{ search ? '没有匹配的文件' : '暂无站点配置文件' }}</p></aside>
      <div class="file-editor">
        <template v-if="readReady">
          <div class="editor-heading"><label v-if="creating">文件名<input v-model="name" :disabled="blocked" placeholder="site.conf" maxlength="53" /></label><code v-else class="editor-path">{{ selected?.path }}</code><div v-if="creating" class="template-actions"><button class="quiet" :disabled="blocked" @click="insertTemplate('static')">静态站点示例</button><button class="quiet" :disabled="blocked" @click="insertTemplate('proxy')">前端与代理示例</button></div><span v-else :class="selected?.editable ? 'managed' : 'readonly'">{{ selected?.editable ? '可编辑' : '只读' }}</span></div>
          <p v-if="selected && !selected.editable" class="readonly-note">{{ userMessage(selected.message, '服务器原有配置只提供位置和归属信息，确认接管后才能编辑。') }}</p>
          <ConfigurationTextEditor v-else :key="creating ? 'new-file' : selected?.id" v-model="content" :readonly="!writable" :disabled="blocked" :error-line="checked?.line" :byte-limit="fileByteLimit" label="Nginx 配置内容" @save="save" @limit="showSizeLimit" />
          <details v-if="dirty" class="config-diff"><summary>修改对比</summary><div><section><strong>当前文件</strong><pre>{{ baseline || '（新文件）' }}</pre></section><section><strong>修改后</strong><pre>{{ content }}</pre></section></div></details>
          <div v-if="writable || submitting" class="editor-actions"><button class="secondary" :disabled="blocked" @click="check()">检查配置</button><button class="primary" :disabled="busy || pending || (!submitting && (!dirty || disabled))" @click="save">{{ submitting ? '核对提交结果' : stopped ? '保存配置' : '保存并重载' }}</button><template v-if="selected?.editable && !creating"><button class="quiet" :disabled="blocked || dirty" @click="changeState(selected.enabled ? 'disable' : 'enable')">{{ selected.enabled ? '停用' : '启用' }}</button><button class="quiet danger-text" :disabled="blocked || dirty" @click="changeState('delete')">删除文件</button></template></div>
        </template><div v-else class="editor-empty"><strong>{{ loading ? '正在读取配置…' : '选择一个配置文件' }}</strong><button v-if="!loading && !selected && canCreate" class="quiet" :disabled="blocked || disabled" @click="newFile">新增站点配置</button></div>
      </div>
    </div>
    <section v-if="job" class="config-job" aria-label="配置作业"><strong>{{ job.phase === 'SUBMITTED' ? '任务已受理' : softwareJobStatusLabel(job.status) }}</strong><span v-if="jobReadFailed && job.phase !== 'SUBMITTED'" class="muted">任务状态读取失败</span><span v-else-if="job.targetAckPending" class="muted">{{ ackPaused ? '请核对任务结果' : '正在确认执行结果…' }}</span><button v-if="isFileTask(job)" class="quiet" @click="emit('logs', job.id)">查看日志</button><button v-if="jobReadFailed || ['QUEUED', 'RUNNING'].includes(job.status)" class="quiet" :disabled="busy" @click="refreshJob">刷新任务</button><button v-if="isFileTask(job) && (job.status === 'UNKNOWN' || job.targetAckPending)" class="quiet" :disabled="busy" @click="reconcile">核对任务结果</button><p v-if="failureMessage" class="job-failure">{{ failureMessage }}</p></section>
    <details v-if="diagnostics" class="config-diagnostics"><summary>诊断信息</summary><code>{{ diagnostics.code }}</code><code>{{ diagnostics.traceId }}</code></details>
  </section>
</template>

<style scoped>
.nginx-config-files { display:grid; gap:16px; min-width:0; }.files-heading,.editor-heading,.editor-actions,.config-job { display:flex; align-items:center; gap:12px; flex-wrap:wrap; }.files-heading { justify-content:space-between; }.files-heading h3 { margin:0; font-size:15px; }.files-actions,.template-actions { display:flex; flex-wrap:wrap; gap:8px; }.files-layout { display:grid; grid-template-columns:minmax(170px,26%) minmax(0,1fr); gap:16px; min-height:390px; }.file-list { min-width:0; display:grid; align-content:start; gap:8px; padding-right:12px; border-right:1px solid var(--border); max-height:510px; overflow:auto; }.file-list input { min-width:0; width:100%; }.file-item { display:grid; gap:4px; width:100%; padding:10px; text-align:left; border:1px solid var(--border); border-radius:6px; color:var(--text); background:var(--surface); }.file-item:hover,.file-item.selected { border-color:var(--accent); background:var(--accent-soft); }.file-item strong,.file-item code { overflow:hidden; text-overflow:ellipsis; white-space:nowrap; }.file-item small { color:var(--muted); }.file-item code { font-size:11px; }.managed { color:#197047; }.readonly { color:#8b5a16; }.file-editor { min-width:0; display:grid; align-content:start; gap:12px; }.editor-heading label { display:grid; gap:6px; min-width:170px; flex:1; font-size:12px; color:var(--muted); }.editor-path { min-width:0; overflow-wrap:anywhere; flex:1; font-size:12px; }.editor-heading > span { font-size:12px; white-space:nowrap; }.readonly-note { margin:0; padding:9px 11px; border-left:3px solid #d79838; background:#fff9ec; color:var(--muted); font-size:12px; }.config-error,.config-success { margin:0; padding:10px 12px; border-radius:6px; font-size:13px; overflow-wrap:anywhere; }.config-error { color:#a83224; background:#fff2ef; }.config-success { color:#19663e; background:#effaf3; }.editor-actions { gap:8px; }.danger-text { margin-left:auto; color:var(--error); }.editor-empty { display:grid; place-content:center; min-height:300px; text-align:center; gap:12px; color:var(--muted); }.editor-empty strong { color:var(--text); }.empty-small { margin:10px 0; color:var(--muted); font-size:12px; }.config-diff { font-size:12px; min-width:0; }.config-diff summary,.config-diagnostics summary { cursor:pointer; color:var(--muted); }.config-diff > div { display:grid; grid-template-columns:1fr 1fr; gap:12px; margin-top:12px; }.config-diff section { min-width:0; }.config-diff pre { max-height:220px; margin:8px 0 0; padding:10px; overflow:auto; border-radius:6px; background:#122232; color:#dce8f5; font:12px/1.5 var(--mono); white-space:pre; }.config-job { padding-top:12px; border-top:1px solid var(--border); font-size:13px; }.job-failure { flex:1 0 100%; margin:0; color:var(--error); white-space:pre-wrap; overflow-wrap:anywhere; font:12px/1.5 var(--mono); }.config-diagnostics { font-size:12px; }.config-diagnostics code { display:block; margin-top:8px; overflow-wrap:anywhere; }
@media (max-width:700px) { .files-layout { grid-template-columns:1fr; }.file-list { padding:0 0 12px; border-right:0; border-bottom:1px solid var(--border); max-height:210px; }.editor-actions .danger-text { margin-left:0; }.config-diff > div { grid-template-columns:1fr; } }
</style>
