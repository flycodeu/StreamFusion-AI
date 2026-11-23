<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref, watch } from "vue";
import { ElDialog } from "element-plus";
import { ApiError, request } from "./api";
import { hostTime } from "./hosts";
import { jenkinsLink } from "./applicationProjects";
import { jenkinsBuildLink, jenkinsElapsed, jenkinsState, type JenkinsBuild, type JenkinsBuildLog, type JenkinsInstanceContext, type JenkinsMonitor } from "./projectJenkins";

const props = defineProps<{ projectId: string; jobUrl?: string; context?: JenkinsInstanceContext }>();
const emit = defineEmits<{ error: [unknown] }>();
const projectBase = `/application-projects/${props.projectId}`;
const base = props.context ? `${projectBase}/service-instances/${encodeURIComponent(props.context.serviceInstanceId)}` : projectBase;
const lifetime = new AbortController();
const monitor = ref<JenkinsMonitor>();
const build = ref<JenkinsBuild>();
const loadingMonitor = ref(false), loadingStatus = ref(false), settingsOpen = ref(false), saving = ref(false);
const statusError = ref(""), settingsError = ref("");
const softwareId = ref(""), username = ref(""), token = ref("");
const jobDraft = ref("");
const jobUrl = computed(() => (props.context ? monitor.value?.jobUrl : props.jobUrl) || "");
const validJob = computed(() => !props.context || !!jenkinsLink(jobDraft.value.trim()) && /\/job\//.test(new URL(jobDraft.value.trim()).pathname));
const retainedToken = computed(() => monitor.value?.hasToken && monitor.value.softwareId === softwareId.value && monitor.value.username === username.value.trim() && (!props.context || jobDraft.value.trim() === jobUrl.value));
const state = computed(() => jenkinsState(build.value?.status));
const running = computed(() => build.value?.status === "QUEUED" || build.value?.status === "RUNNING");
const queuedWithPrevious = computed(() => build.value?.status === "QUEUED" && !!build.value.number);
const stageLabel = computed(() => {
  const selected = build.value;
  if (!selected?.stagesAvailable) return "阶段待确认";
  if (selected.currentStage) return selected.currentStage;
  if (selected.building) return "等待阶段输出";
  return selected.stages?.filter(stage => !["NOT_EXECUTED", "NOT_BUILT", "QUEUED"].includes(stage.status)).at(-1)?.name || "阶段待确认";
});
const consoleUrl = computed(() => jenkinsBuildLink(jobUrl.value, build.value?.consoleUrl));
const runUrl = computed(() => jenkinsBuildLink(jobUrl.value, build.value?.buildUrl));

const logOpen = ref(props.context?.action === "logs"), loadingLog = ref(false), logMore = ref(false), logLimited = ref(false);
const log = ref(""), logError = ref("");
const submitting = ref(false), submitError = ref("");
type QaTracking = { version: string; requestId: string; queueId?: number; number?: number };
const tracking = ref<QaTracking>();
const awaitingReceipt = computed(() => !!tracking.value && !tracking.value.queueId);
const trackingKey = `ops.qa:${base}`;
function remember(value?: QaTracking) {
  tracking.value = value;
  if (!props.context) return;
  try {
    if (value) sessionStorage.setItem(trackingKey, JSON.stringify(value));
    else sessionStorage.removeItem(trackingKey);
  } catch { /* Current-panel tracking remains available. */ }
}
function restore(version: string) {
  if (!props.context) return;
  try {
    const item: QaTracking | null = JSON.parse(sessionStorage.getItem(trackingKey) || "null");
    if (item?.version === version && typeof item.requestId === "string" && /^[a-f0-9-]{36}$/i.test(item.requestId)
      && [item.queueId, item.number].every(n => n === undefined || Number.isSafeInteger(n) && n > 0)) {
      tracking.value = item;
    } else if (item) remember();
  } catch { /* Storage may be unavailable. */ }
}
let logOffset = 0;
let logRequest: AbortController | undefined;
let statusRequest: AbortController | undefined;
let timer: ReturnType<typeof setTimeout> | undefined;

function message(error: unknown, fallback: string) {
  if (error instanceof ApiError) {
    if (error.code === "AUTH_REQUIRED") emit("error", error);
    return [error.message || fallback, error.code, error.traceId, error.timestamp].filter(Boolean).join(" · ");
  }
  return error instanceof Error && /[\u4e00-\u9fff]/.test(error.message) ? error.message : fallback;
}

function stopTimer() {
  if (timer !== undefined) clearTimeout(timer);
  timer = undefined;
}

function schedule() {
  stopTimer();
  if (!running.value || statusError.value || settingsOpen.value || lifetime.signal.aborted) return;
  timer = setTimeout(() => {
    if (document.hidden) schedule();
    else void refreshStatus();
  }, 5000);
}

async function loadMonitor() {
  if (loadingMonitor.value || loadingStatus.value || submitting.value) return;
  stopTimer();
  loadingMonitor.value = true;
  statusError.value = "";
  try {
    const data = await request<JenkinsMonitor>(`${base}/jenkins-monitor`, "GET", undefined, lifetime.signal);
    if (lifetime.signal.aborted) return;
    if (monitor.value && monitor.value.version !== data.version) {
      remember();
      clearLog();
      build.value = undefined;
      submitError.value = "";
    }
    monitor.value = data;
    restore(data.version);
    if (data.configured) await refreshStatus();
    else {
      build.value = undefined;
      logOpen.value = false;
      clearLog();
    }
  } catch (error) {
    if (!lifetime.signal.aborted) statusError.value = message(error, "无法读取 Jenkins 监控设置，请刷新后重试。");
  } finally {
    loadingMonitor.value = false;
  }
}

async function refreshStatus() {
  if (!monitor.value?.configured || loadingStatus.value || submitting.value) return;
  stopTimer();
  const controller = statusRequest = new AbortController();
  loadingStatus.value = true;
  statusError.value = "";
  try {
    const pinned = tracking.value;
    const query = pinned?.number ? `?number=${pinned.number}` : pinned?.queueId ? `?queueId=${pinned.queueId}` : "";
    const data = await request<JenkinsBuild>(`${base}/jenkins-build${query}`, "GET", undefined, controller.signal);
    if (controller.signal.aborted) return;
    const changedRun = build.value?.number !== data.number;
    const wasBuilding = build.value?.building;
    if (changedRun) clearLog();
    build.value = data;
    if (pinned?.queueId && data.number && data.status !== "QUEUED") remember({ ...pinned, number: data.number });
    if (logOpen.value && data.number && (changedRun || logMore.value || data.building || wasBuilding)) void readLog();
  } catch (error) {
    if (!controller.signal.aborted) statusError.value = message(error, "无法读取 Jenkins 构建状态，请检查连接后重试。");
  } finally {
    if (statusRequest === controller) {
      statusRequest = undefined;
      loadingStatus.value = false;
      schedule();
    }
  }
}

async function startQa() {
  if (props.context?.action !== "qa" || props.context.readOnly || !monitor.value?.configured || loadingStatus.value || submitting.value || settingsOpen.value) return;
  const pending = tracking.value && !tracking.value.queueId;
  if (!pending && (!build.value || running.value || statusError.value)) return;
  stopTimer();
  submitting.value = true;
  submitError.value = "";
  let queued = false;
  try {
    const intent = pending ? tracking.value! : { version: monitor.value.version, requestId: crypto.randomUUID() };
    remember(intent);
    const receipt = await request<{ queueId: number; status: string }>(`${base}/jenkins-build`, "POST", {
      expectedVersion: intent.version, requestId: intent.requestId,
    }, lifetime.signal);
    if (lifetime.signal.aborted) return;
    if (!Number.isSafeInteger(receipt.queueId) || receipt.queueId <= 0 || receipt.status !== "QUEUED") throw new Error("Jenkins 排队回执无效，请核对提交结果。");
    remember({ ...intent, queueId: receipt.queueId });
    clearLog();
    build.value = { configured: true, status: "QUEUED", stagesAvailable: false, observedAt: "" };
    logOpen.value = true;
    queued = true;
  } catch (error) {
    if (!lifetime.signal.aborted) submitError.value = message(error, "无法确认 QA 是否已提交，请核对提交结果。");
  } finally {
    submitting.value = false;
    if (!lifetime.signal.aborted) {
      if (queued) await refreshStatus();
      else schedule();
    }
  }
}

function showLatest() {
  remember();
  submitError.value = "";
  build.value = undefined;
  clearLog();
  void refreshStatus();
}

function editMonitor() {
  if (props.context?.readOnly || !monitor.value || saving.value || submitting.value) return;
  settingsError.value = "";
  jobDraft.value = jobUrl.value;
  softwareId.value = monitor.value.softwareId || (monitor.value.instances.length === 1 ? monitor.value.instances[0]?.id ?? "" : "");
  username.value = monitor.value.username;
  token.value = "";
  settingsOpen.value = true;
  stopTimer();
}

async function saveMonitor() {
  if (props.context?.readOnly || !monitor.value || !validJob.value || !softwareId.value || !username.value.trim() || (!retainedToken.value && !token.value.trim()) || saving.value) return;
  const data = {
    expectedVersion: monitor.value.version,
    softwareId: softwareId.value,
    username: username.value.trim(),
    token: token.value.trim(),
    ...(props.context ? { jobUrl: jobDraft.value.trim() } : {}),
  };
  saving.value = true;
  settingsError.value = "";
  try {
    const saved = await request<JenkinsMonitor>(`${base}/jenkins-monitor`, "POST", data, lifetime.signal);
    if (lifetime.signal.aborted) return;
    statusRequest?.abort();
    statusRequest = undefined;
    loadingStatus.value = false;
    monitor.value = saved;
    remember();
    submitError.value = "";
    build.value = undefined;
    logOpen.value = props.context?.action === "logs";
    clearLog();
    settingsOpen.value = false;
    void refreshStatus();
  } catch (error) {
    if (!lifetime.signal.aborted) settingsError.value = message(error, "保存 Jenkins 监控设置失败，请检查后重试。");
  } finally {
    data.token = "";
    saving.value = false;
  }
}

function clearLog() {
  logRequest?.abort();
  logRequest = undefined;
  loadingLog.value = logLimited.value = logMore.value = false;
  log.value = logError.value = "";
  logOffset = 0;
}

function toggleLog() {
  logOpen.value = !logOpen.value;
  clearLog();
  if (logOpen.value) void readLog();
}

async function readLog() {
  const number = build.value?.number;
  if (!number || !logOpen.value || loadingLog.value) return;
  const controller = logRequest = new AbortController();
  const offset = logOffset;
  const params = new URLSearchParams({ number: String(number), offset: String(offset) });
  loadingLog.value = true;
  logError.value = "";
  try {
    const chunk = await request<JenkinsBuildLog>(`${base}/jenkins-build/log?${params}`, "GET", undefined, controller.signal);
    if (controller.signal.aborted) return;
    if (chunk.number !== number || chunk.offset !== offset || !Number.isSafeInteger(chunk.nextOffset) || chunk.nextOffset < offset) {
      logError.value = "Jenkins 返回的日志位置不一致，请重新打开日志。";
      logMore.value = false;
      return;
    }
    const combined = log.value + chunk.text;
    const limit = 96 * 1024;
    log.value = combined.slice(-limit);
    logLimited.value ||= chunk.truncated || combined.length > limit;
    logMore.value = chunk.more;
    logOffset = chunk.nextOffset;
  } catch (error) {
    if (!controller.signal.aborted) logError.value = message(error, "读取 Jenkins 日志失败，请重试。");
  } finally {
    if (logRequest === controller) {
      loadingLog.value = false;
      logRequest = undefined;
    }
  }
}

watch(settingsOpen, open => {
  if (open) return;
  token.value = "";
  schedule();
});
onMounted(loadMonitor);
onUnmounted(() => {
  lifetime.abort();
  statusRequest?.abort();
  logRequest?.abort();
  stopTimer();
  token.value = "";
});
</script>

<template>
  <section class="jenkins-build" :class="{ 'panel-card': !context }" aria-label="Jenkins 构建">
    <header class="build-toolbar">
      <div class="build-heading">
        <h3>{{ context ? 'QA 作业' : '项目构建' }}</h3>
        <span v-if="build" class="state-tag" :class="statusError ? 'state-muted' : state.tone">{{ state.label }}</span>
      </div>
      <div class="build-actions">
        <button v-if="context?.action === 'qa' && !context.readOnly && monitor?.configured" type="button" class="primary"
          :disabled="submitting || loadingStatus || loadingMonitor || (!awaitingReceipt && (!build || running || !!statusError))" @click="startQa">{{ submitting ? '提交中…' : awaitingReceipt ? '核对提交结果' : '运行 QA' }}</button>
        <button v-if="monitor?.configured && !context?.readOnly" type="button" class="secondary" :disabled="submitting" @click="editMonitor">{{ context ? '作业设置' : '监控设置' }}</button>
        <button type="button" class="quiet" :disabled="loadingMonitor || loadingStatus || submitting" @click="loadMonitor">{{ loadingMonitor || loadingStatus ? "读取中…" : "刷新" }}</button>
      </div>
    </header>

    <div v-if="submitError" class="build-error" role="alert">
      {{ submitError }}
      <button v-if="awaitingReceipt" type="button" class="quiet" :disabled="submitting || loadingStatus" @click="showLatest">已核对 Jenkins 记录，结束本次跟踪</button>
    </div>
    <p v-if="statusError" class="build-error" role="alert">{{ build ? "本次读取失败，仍显示上次结果。" : "" }}{{ statusError }}</p>
    <p v-else-if="loadingMonitor && !monitor" class="build-note" role="status">正在读取 Jenkins 构建…</p>
    <div v-if="monitor && !monitor.configured" class="build-unconfigured">
      <p class="build-note">{{ context ? '此环境尚未绑定模块的 QA 作业。' : '绑定已安装的 Jenkins，并填写用于读取此作业的 API Token。' }}</p>
      <button v-if="!context?.readOnly" type="button" class="primary" @click="editMonitor">{{ context ? '配置 QA 作业' : '配置构建监控' }}</button>
    </div>

    <div v-if="context && monitor?.configured" class="build-source">
      <span>Jenkins 作业</span>
      <a v-if="jenkinsLink(jobUrl)" :href="jenkinsLink(jobUrl)" target="_blank" rel="noopener noreferrer">{{ jobUrl }}</a>
      <span v-if="tracking?.queueId && build?.status === 'QUEUED'">队列 #{{ tracking.queueId }}</span>
      <button v-if="tracking?.queueId && (statusError || !running)" type="button" class="quiet" :disabled="loadingStatus || submitting" @click="showLatest">查看最新构建</button>
    </div>
    <template v-if="build?.configured">
      <p v-if="build.status === 'NEVER_BUILT'" class="build-note">此作业尚无构建记录。</p>
      <p v-if="build.queueReason" class="build-note">{{ build.queueReason }}</p>
      <dl v-if="build.number" class="build-facts">
        <div><dt>{{ queuedWithPrevious ? '上次构建号' : '构建号' }}</dt><dd v-if="runUrl"><a :href="runUrl" target="_blank" rel="noopener noreferrer">#{{ build.number }} ↗</a></dd><dd v-else>#{{ build.number }}</dd></div>
        <div><dt>{{ queuedWithPrevious ? '上次构建阶段' : build.building ? '当前阶段' : '最后阶段' }}</dt><dd>{{ stageLabel }}</dd></div>
        <div v-if="build.startedAt"><dt>开始时间</dt><dd>{{ hostTime(build.startedAt) }}</dd></div>
        <div v-if="jenkinsElapsed(build.durationMs)"><dt>耗时</dt><dd>{{ jenkinsElapsed(build.durationMs) }}</dd></div>
      </dl>
      <ol v-if="context?.action !== 'logs' && build.stagesAvailable && build.stages?.length" class="build-stages" :aria-label="queuedWithPrevious ? '上次构建阶段' : '构建阶段'">
        <li v-for="(stage, index) in build.stages" :key="index">
          <strong>{{ stage.name }}</strong>
          <span class="state-tag" :class="jenkinsState(stage.status).tone">{{ jenkinsState(stage.status).label }}</span>
          <small v-if="jenkinsElapsed(stage.durationMs)">{{ jenkinsElapsed(stage.durationMs) }}</small>
        </li>
      </ol>
      <section v-if="context?.action !== 'logs' && build.number" class="build-artifacts" aria-label="构建产物">
        <h4>{{ queuedWithPrevious ? '上次构建产物' : '归档产物' }}</h4>
        <ul v-if="build.artifacts?.length">
          <li v-for="artifact in build.artifacts" :key="artifact.path">
            <a v-if="jenkinsBuildLink(jobUrl, artifact.url)" :href="jenkinsBuildLink(jobUrl, artifact.url)" target="_blank" rel="noopener noreferrer" :title="artifact.path">{{ artifact.name }} ↗</a>
            <span v-else>{{ artifact.name }}</span>
          </li>
        </ul>
        <p v-else class="build-note">暂无归档产物。</p>
      </section>
      <div v-if="build.number" class="build-actions">
        <button v-if="context?.action !== 'logs'" type="button" class="secondary" :aria-expanded="logOpen" @click="toggleLog">{{ logOpen ? '收起日志' : queuedWithPrevious ? '查看上次构建日志' : '查看构建日志' }}</button>
        <a v-if="consoleUrl" :href="consoleUrl" target="_blank" rel="noopener noreferrer">{{ queuedWithPrevious ? '上次构建控制台' : 'Jenkins 控制台' }} ↗</a>
      </div>
      <section v-if="logOpen && build.number" class="build-log" aria-label="Jenkins 构建日志">
        <header><strong>#{{ build.number }} 控制台输出</strong><span v-if="logLimited">部分输出，完整日志可在 Jenkins 查看</span></header>
        <pre tabindex="0">{{ log || (loadingLog ? "正在读取日志…" : "暂无日志输出") }}</pre>
        <footer v-if="logMore || build.building || logError">
          <p v-if="logError" role="alert">{{ logError }}</p>
          <button type="button" class="quiet" :disabled="loadingLog" @click="readLog">{{ loadingLog ? "读取中…" : logError ? "重试" : "读取后续日志" }}</button>
        </footer>
      </section>
      <p v-if="build.observedAt" class="build-note">最近读取 {{ hostTime(build.observedAt) }}</p>
    </template>
  </section>

  <ElDialog v-model="settingsOpen" :title="context ? '配置 QA 作业' : 'Jenkins 构建监控'" width="min(510px, 94vw)" align-center append-to-body destroy-on-close :close-on-click-modal="false" :close-on-press-escape="!saving" :show-close="!saving">
    <form class="dialog-form monitor-form" @submit.prevent="saveMonitor">
      <p v-if="context" class="build-note">为 {{ context.moduleName }}{{ context.environmentName ? ' · ' + context.environmentName : '' }} 选择 QA 流水线。Jenkins 账号需具备该作业的读取和构建权限。</p>
      <p v-else class="build-note">选择与项目作业链接对应的 Jenkins 实例。API Token 使用具备作业读取权限的 Jenkins 账户。</p>
      <p v-if="!monitor?.instances.length" class="build-note">没有可绑定的 Jenkins 实例。请先核对软件安装与项目的作业链接。</p>
      <label>Jenkins 实例
        <select v-model="softwareId" aria-label="Jenkins 实例" required :disabled="saving">
          <option value="" disabled>选择实例</option>
          <option v-for="instance in monitor?.instances" :key="instance.id" :value="instance.id">{{ instance.name }} · {{ instance.hostName }}</option>
        </select>
      </label>
      <label v-if="context">QA 作业地址 <input v-model="jobDraft" type="url" required :disabled="saving" placeholder="https://jenkins.example.com/job/platform-api-qa/" /></label>
      <label>Jenkins 用户名 <input v-model="username" required maxlength="128" :disabled="saving" autocomplete="off" /></label>
      <label>Jenkins API Token
        <input v-model="token" type="password" :required="!retainedToken" :disabled="saving" maxlength="512" autocomplete="new-password" :placeholder="retainedToken ? '留空保留已有 Token' : ''" />
      </label>
      <p v-if="settingsError" class="build-error" role="alert">{{ settingsError }}</p>
      <div class="dialog-footer">
        <button type="button" class="secondary" :disabled="saving" @click="settingsOpen = false">取消</button>
        <button type="submit" class="primary" :disabled="saving || !validJob || !softwareId || !username.trim() || (!retainedToken && !token.trim())">{{ saving ? "保存中…" : "保存" }}</button>
      </div>
    </form>
  </ElDialog>
</template>

<style scoped>
.jenkins-build { display: grid; gap: 12px; padding: 12px 16px; min-width: 0; }
.build-toolbar, .build-heading, .build-actions, .build-unconfigured { display: flex; align-items: center; flex-wrap: wrap; gap: 8px 12px; }
.build-toolbar { justify-content: space-between; }
.build-heading h3 { margin: 0; font-size: 16px; }
.jenkins-build:not(.panel-card) { padding: 0; }
.build-source { display: flex; align-items: baseline; flex-wrap: wrap; gap: 4px 10px; padding-bottom: 10px; border-bottom: 1px solid var(--border-subtle); font-size: 13px; }
.build-source > span { color: var(--muted); }
.build-note, .build-artifacts h4 { margin: 0; font-size: 13px; }
.build-note { color: var(--muted); overflow-wrap: anywhere; }
.jenkins-build a { color: var(--accent); text-underline-offset: 2px; overflow-wrap: anywhere; }
.state-running { color: var(--accent); background: var(--accent-soft); border-color: #bfdbfe; }
.build-error { margin: 0; padding: 9px 12px; border: 1px solid #fecaca; border-radius: var(--control-radius); color: #991b1b; background: #fef2f2; font-size: 13px; overflow-wrap: anywhere; }
.build-facts { display: grid; gap: 8px 16px; grid-template-columns: repeat(auto-fit, minmax(138px, 1fr)); margin: 0; }
.build-facts dt { color: var(--muted); font-size: 12px; }
.build-facts dd { margin: 2px 0 0; font-weight: 600; overflow-wrap: anywhere; }
.build-stages, .build-artifacts ul { margin: 0; padding: 0; list-style: none; }
.build-stages { display: grid; border: 1px solid var(--border); border-radius: var(--control-radius); }
.build-stages li { display: grid; grid-template-columns: minmax(80px, 1fr) max-content 90px; align-items: center; gap: 6px 12px; padding: 6px 10px; font-size: 13px; }
.build-stages li + li { border-top: 1px solid var(--border); }
.build-stages strong { overflow-wrap: anywhere; }
.build-stages small { color: var(--muted); text-align: right; }
.build-artifacts ul { display: grid; gap: 4px; margin-top: 6px; font-size: 13px; }
.build-artifacts p { margin-top: 6px; }
.build-log { overflow: hidden; border: 1px solid #26374a; border-radius: var(--control-radius); background: #111d2b; color: #d6e3ef; }
.build-log header, .build-log footer { display: flex; align-items: center; flex-wrap: wrap; gap: 8px 12px; padding: 8px 12px; font-size: 12px; background: #19293b; }
.build-log header span { color: #a4b9cf; }
.build-log footer p { margin: 0; color: #fecaca; overflow-wrap: anywhere; }
.build-log .quiet { color: #d1e0ed; }
.build-log pre { margin: 0; padding: 12px; min-height: 70px; max-height: min(360px, 50vh); overflow: auto; overscroll-behavior: contain; white-space: pre-wrap; overflow-wrap: anywhere; font: 12px/1.75 var(--mono); }
.monitor-form label { display: grid; gap: 6px; }
.monitor-form input, .monitor-form select { width: 100%; margin: 0; }
@media (max-width: 540px) {
  .jenkins-build { padding: 12px; }
  .build-stages li { grid-template-columns: minmax(0, 1fr) max-content; }
  .build-stages small { grid-column: 1 / -1; text-align: left; }
}
</style>
