<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref } from "vue";
import {
  ElAlert,
  ElButton,
  ElDescriptions,
  ElDescriptionsItem,
  ElEmpty,
  ElPopover,
  ElTable,
  ElTableColumn,
  ElTag,
} from "element-plus";
import { ApiError, request } from "./api";
import { hostTime } from "./hosts";
import { moduleJenkinsPath, jenkinsBuildLink, jenkinsElapsed, jenkinsState, type JenkinsBuild, type JenkinsBuildLog, type JenkinsInstanceContext, type JenkinsMonitor } from "./projectJenkins";

interface ErrorMeta {
  code?: string;
  traceId?: string;
  timestamp?: string;
}

const props = defineProps<{ projectId: string; context: JenkinsInstanceContext; monitor: JenkinsMonitor }>();
const emit = defineEmits<{ error: [unknown]; observed: [build: JenkinsBuild] }>();
const base = moduleJenkinsPath(props.projectId, props.context.moduleId, props.context.serviceInstanceId);
const lifetime = new AbortController();
const build = ref<JenkinsBuild>();
const loadingStatus = ref(false);
const statusError = ref("");
const statusErrorMeta = ref<ErrorMeta>();
const jobUrl = computed(() => props.monitor.jobUrl || "");
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

const logOpen = ref(props.context.action === "logs"), loadingLog = ref(false), logMore = ref(false), logLimited = ref(false);
const log = ref(""), logError = ref("");
const logErrorMeta = ref<ErrorMeta>();
const submitting = ref(false), submitError = ref("");
const submitErrorMeta = ref<ErrorMeta>();
type QaTracking = { version: string; requestId: string; queueId?: number; number?: number };
const tracking = ref<QaTracking>();
const awaitingReceipt = computed(() => !!tracking.value && !tracking.value.queueId);
const trackingKey = `ops.qa:${base}`;
function remember(value?: QaTracking) {
  tracking.value = value;
  try {
    if (value) sessionStorage.setItem(trackingKey, JSON.stringify(value));
    else sessionStorage.removeItem(trackingKey);
  } catch { /* Current-panel tracking remains available. */ }
}
function restore(version: string) {
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

function extractError(error: unknown, fallback: string): { text: string; meta?: ErrorMeta } {
  if (error instanceof ApiError) {
    if (error.code === "AUTH_REQUIRED") emit("error", error);
    const meta: ErrorMeta | undefined =
      error.code || error.traceId || error.timestamp
        ? { code: error.code, traceId: error.traceId, timestamp: error.timestamp }
        : undefined;
    return { text: error.message || fallback, meta };
  }
  return {
    text: error instanceof Error && /[\u4e00-\u9fff]/.test(error.message) ? error.message : fallback,
  };
}

function jenkinsTagType(tone: string): "success" | "danger" | "warning" | "primary" | "info" {
  if (tone === "state-success") return "success";
  if (tone === "state-error") return "danger";
  if (tone === "state-warning") return "warning";
  if (tone === "state-running") return "primary";
  return "info";
}

function stopTimer() {
  if (timer !== undefined) clearTimeout(timer);
  timer = undefined;
}

function schedule() {
  stopTimer();
  if (!running.value || statusError.value || lifetime.signal.aborted) return;
  timer = setTimeout(() => {
    if (document.hidden) schedule();
    else void refreshStatus();
  }, 5000);
}

async function refreshStatus() {
  if (!props.monitor.configured || lifetime.signal.aborted || loadingStatus.value || submitting.value) return;
  stopTimer();
  const controller = statusRequest = new AbortController();
  loadingStatus.value = true;
  statusError.value = "";
  statusErrorMeta.value = undefined;
  try {
    const pinned = tracking.value;
    const query = pinned?.number ? `?number=${pinned.number}` : pinned?.queueId ? `?queueId=${pinned.queueId}` : "";
    const data = await request<JenkinsBuild>(`${base}/jenkins-build${query}`, "GET", undefined, controller.signal);
    if (controller.signal.aborted) return;
    const changedRun = build.value?.number !== data.number;
    const wasBuilding = build.value?.building;
    if (changedRun) clearLog();
    build.value = data;
    emit("observed", data);
    if (pinned?.queueId && data.number && data.status !== "QUEUED") remember({ ...pinned, number: data.number });
    if (logOpen.value && data.number && (changedRun || logMore.value || data.building || wasBuilding)) void readLog();
  } catch (error) {
    if (!controller.signal.aborted) {
      const diag = extractError(error, "无法读取 Jenkins 构建状态，请检查连接后重试。");
      statusError.value = diag.text;
      statusErrorMeta.value = diag.meta;
    }
  } finally {
    if (statusRequest === controller) {
      statusRequest = undefined;
      loadingStatus.value = false;
      schedule();
    }
  }
}

async function startQa() {
  if (props.context.action !== "qa" || props.context.readOnly || lifetime.signal.aborted || !props.monitor.configured || loadingStatus.value || submitting.value) return;
  const pending = tracking.value && !tracking.value.queueId;
  if (!pending && (!build.value || running.value || statusError.value)) return;
  stopTimer();
  submitting.value = true;
  submitError.value = "";
  submitErrorMeta.value = undefined;
  let queued = false;
  try {
    const intent = pending ? tracking.value! : { version: props.monitor.version, requestId: crypto.randomUUID() };
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
    emit("observed", build.value);
    queued = true;
  } catch (error) {
    if (!lifetime.signal.aborted) {
      const diag = extractError(error, "无法确认 QA 是否已提交，请核对提交结果。");
      submitError.value = diag.text;
      submitErrorMeta.value = diag.meta;
    }
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
  submitErrorMeta.value = undefined;
  build.value = undefined;
  clearLog();
  void refreshStatus();
}

function clearLog() {
  logRequest?.abort();
  logRequest = undefined;
  loadingLog.value = logLimited.value = logMore.value = false;
  log.value = logError.value = "";
  logErrorMeta.value = undefined;
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
  logErrorMeta.value = undefined;
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
    if (!controller.signal.aborted) {
      const diag = extractError(error, "读取 Jenkins 日志失败，请重试。");
      logError.value = diag.text;
      logErrorMeta.value = diag.meta;
    }
  } finally {
    if (logRequest === controller) {
      loadingLog.value = false;
      logRequest = undefined;
    }
  }
}

// A row click is the operator's intent. Refresh and log navigation only read.
async function openRun() {
  restore(props.monitor.version);
  await refreshStatus();
  if (lifetime.signal.aborted || props.context.action !== "qa" || props.context.readOnly || statusError.value || running.value) return;
  if (tracking.value?.queueId) {
    remember();
    await refreshStatus();
  }
  await startQa();
}
onMounted(openRun);
onUnmounted(() => {
  lifetime.abort();
  statusRequest?.abort();
  logRequest?.abort();
  stopTimer();
});
</script>

<template>
  <section class="jenkins-build" aria-label="构建详情" :aria-busy="submitting">
    <header class="build-toolbar">
      <div class="build-heading">
        <span v-if="submitting || !build && loadingStatus" class="status-hint" role="status">
          {{ submitting ? '正在提交 QA…' : '正在读取构建…' }}
        </span>
        <ElTag
          v-else-if="build"
          size="small"
          effect="light"
          :type="statusError ? 'info' : jenkinsTagType(state.tone)"
        >
          {{ state.label }}
        </ElTag>
        <ElTag v-if="tracking?.queueId && build?.status === 'QUEUED'" size="small" type="info" effect="plain">
          队列 #{{ tracking.queueId }}
        </ElTag>
      </div>
      <div class="build-toolbar-actions">
        <ElButton
          v-if="tracking?.number && !running"
          size="small"
          :disabled="loadingStatus || submitting"
          @click="showLatest"
        >
          查看最新构建
        </ElButton>
        <ElButton
          size="small"
          :loading="loadingStatus && !submitting"
          :disabled="submitting"
          @click="refreshStatus"
        >
          刷新
        </ElButton>
      </div>
    </header>

    <ElAlert v-if="submitError" type="error" :closable="false" show-icon :title="submitError">
      <div v-if="awaitingReceipt || submitErrorMeta" class="alert-actions">
        <ElButton v-if="awaitingReceipt" link type="primary" size="small" :disabled="loadingStatus || submitting" @click="startQa">
          重试查询
        </ElButton>
        <ElButton v-if="awaitingReceipt" link size="small" :disabled="loadingStatus || submitting" @click="showLatest">
          重置跟踪
        </ElButton>
        <ElPopover v-if="submitErrorMeta" trigger="click" placement="bottom-start" :width="340">
          <div class="diag-meta-list">
            <div v-if="submitErrorMeta.code"><span>错误码</span><code>{{ submitErrorMeta.code }}</code></div>
            <div v-if="submitErrorMeta.traceId"><span>追踪标识</span><code>{{ submitErrorMeta.traceId }}</code></div>
            <div v-if="submitErrorMeta.timestamp"><span>发生时间</span><code>{{ submitErrorMeta.timestamp }}</code></div>
          </div>
          <template #reference>
            <ElButton link type="primary" size="small">排查详情</ElButton>
          </template>
        </ElPopover>
      </div>
    </ElAlert>

    <ElAlert v-if="statusError" type="error" :closable="false" show-icon :title="statusError">
      <div v-if="statusErrorMeta" class="alert-actions">
        <ElPopover trigger="click" placement="bottom-start" :width="340">
          <div class="diag-meta-list">
            <div v-if="statusErrorMeta.code"><span>错误码</span><code>{{ statusErrorMeta.code }}</code></div>
            <div v-if="statusErrorMeta.traceId"><span>追踪标识</span><code>{{ statusErrorMeta.traceId }}</code></div>
            <div v-if="statusErrorMeta.timestamp"><span>发生时间</span><code>{{ statusErrorMeta.timestamp }}</code></div>
          </div>
          <template #reference>
            <ElButton link type="primary" size="small">排查详情</ElButton>
          </template>
        </ElPopover>
      </div>
    </ElAlert>

    <template v-if="build?.configured">
      <ElEmpty v-if="build.status === 'NEVER_BUILT'" description="此作业尚无构建记录" :image-size="56" />
      <ElAlert v-if="build.queueReason" type="info" :closable="false" show-icon :title="build.queueReason" />

      <ElDescriptions v-if="build.number" :column="2" border size="small">
        <ElDescriptionsItem :label="queuedWithPrevious ? '上次构建号' : '构建号'">
          <a v-if="runUrl" :href="runUrl" target="_blank" rel="noopener noreferrer">#{{ build.number }} ↗</a>
          <span v-else>#{{ build.number }}</span>
        </ElDescriptionsItem>
        <ElDescriptionsItem :label="queuedWithPrevious ? '上次构建阶段' : build.building ? '当前阶段' : '最后阶段'">
          {{ stageLabel }}
        </ElDescriptionsItem>
        <ElDescriptionsItem v-if="build.startedAt" label="开始时间">
          {{ hostTime(build.startedAt) }}
        </ElDescriptionsItem>
        <ElDescriptionsItem v-if="jenkinsElapsed(build.durationMs)" label="耗时">
          {{ jenkinsElapsed(build.durationMs) }}
        </ElDescriptionsItem>
      </ElDescriptions>

      <div v-if="context.action !== 'logs' && build.stagesAvailable && build.stages?.length" class="build-section">
        <h4 class="build-section-title">{{ queuedWithPrevious ? '上次构建阶段' : '流水线阶段' }}</h4>
        <ElTable :data="build.stages" size="small" border style="width: 100%">
          <ElTableColumn prop="name" label="阶段" min-width="160" />
          <ElTableColumn label="状态" width="110">
            <template #default="{ row }">
              <ElTag size="small" :type="jenkinsTagType(jenkinsState(row.status).tone)">
                {{ jenkinsState(row.status).label }}
              </ElTag>
            </template>
          </ElTableColumn>
          <ElTableColumn label="耗时" width="100" align="right">
            <template #default="{ row }">
              <span class="muted">{{ jenkinsElapsed(row.durationMs) || '—' }}</span>
            </template>
          </ElTableColumn>
        </ElTable>
      </div>

      <section v-if="context.action !== 'logs' && build.number" class="build-section" aria-label="构建产物">
        <h4 class="build-section-title">{{ queuedWithPrevious ? '上次构建产物' : '归档产物' }}</h4>
        <ElTable v-if="build.artifacts?.length" :data="build.artifacts" size="small" border style="width: 100%">
          <ElTableColumn label="产物文件" min-width="200">
            <template #default="{ row }">
              <a
                v-if="jenkinsBuildLink(jobUrl, row.url)"
                :href="jenkinsBuildLink(jobUrl, row.url)"
                target="_blank"
                rel="noopener noreferrer"
                :title="row.path"
              >
                {{ row.name }} ↗
              </a>
              <span v-else>{{ row.name }}</span>
            </template>
          </ElTableColumn>
        </ElTable>
        <p v-else class="build-note">暂无归档产物。</p>
      </section>

      <div v-if="build.number" class="build-actions">
        <ElButton
          v-if="context.action !== 'logs'"
          size="small"
          :type="logOpen ? 'default' : 'primary'"
          plain
          :aria-expanded="logOpen"
          @click="toggleLog"
        >
          {{ logOpen ? '收起日志' : queuedWithPrevious ? '查看上次构建日志' : '查看构建日志' }}
        </ElButton>
        <ElButton
          v-if="consoleUrl"
          tag="a"
          :href="consoleUrl"
          target="_blank"
          rel="noopener noreferrer"
          size="small"
          link
          type="primary"
        >
          {{ queuedWithPrevious ? '上次构建控制台' : 'Jenkins 控制台' }} ↗
        </ElButton>
      </div>

      <section v-if="logOpen && build.number" class="build-log" aria-label="Jenkins 构建日志">
        <header>
          <strong>#{{ build.number }} 控制台输出</strong>
          <span v-if="logLimited">部分输出，完整日志可在 Jenkins 查看</span>
        </header>
        <pre tabindex="0">{{ log || (loadingLog ? "正在读取日志…" : "暂无日志输出") }}</pre>
        <footer v-if="logMore || build.building || logError">
          <template v-if="logError">
            <span class="log-error-text" role="alert">{{ logError }}</span>
            <ElPopover v-if="logErrorMeta" trigger="click" placement="top" :width="340">
              <div class="diag-meta-list">
                <div v-if="logErrorMeta.code"><span>错误码</span><code>{{ logErrorMeta.code }}</code></div>
                <div v-if="logErrorMeta.traceId"><span>追踪标识</span><code>{{ logErrorMeta.traceId }}</code></div>
                <div v-if="logErrorMeta.timestamp"><span>发生时间</span><code>{{ logErrorMeta.timestamp }}</code></div>
              </div>
              <template #reference>
                <ElButton link type="primary" size="small">排查详情</ElButton>
              </template>
            </ElPopover>
          </template>
          <ElButton size="small" link type="primary" :loading="loadingLog" @click="readLog">
            {{ loadingLog ? "读取中…" : logError ? "重试" : "读取后续日志" }}
          </ElButton>
        </footer>
      </section>

      <p v-if="build.observedAt" class="build-note">最近读取 {{ hostTime(build.observedAt) }}</p>
    </template>
  </section>
</template>

<style scoped>
.jenkins-build { display: grid; gap: 14px; padding: 0; min-width: 0; }
.build-toolbar, .build-heading, .build-toolbar-actions, .build-actions, .alert-actions { display: flex; align-items: center; flex-wrap: wrap; gap: 8px; }
.build-toolbar { justify-content: space-between; }
.status-hint { font-size: 13px; color: var(--muted); }
.build-section { display: grid; gap: 8px; }
.build-section-title { margin: 0; font-size: 13px; font-weight: 600; color: #334155; }
.build-note { margin: 0; font-size: 12px; color: var(--muted); overflow-wrap: anywhere; }
.muted { color: var(--muted); font-size: 12px; }
.jenkins-build a { color: var(--el-color-primary); text-decoration: none; overflow-wrap: anywhere; }
.jenkins-build a:hover { text-decoration: underline; }
.alert-actions { margin-top: 4px; }
.diag-meta-list { display: grid; gap: 6px; font-size: 12px; }
.diag-meta-list > div { display: grid; grid-template-columns: 64px 1fr; gap: 8px; align-items: baseline; }
.diag-meta-list span { color: var(--muted); }
.diag-meta-list code { overflow-wrap: anywhere; word-break: break-all; }
.build-log { overflow: hidden; border: 1px solid #26374a; border-radius: 6px; background: #111d2b; color: #d6e3ef; }
.build-log header, .build-log footer { display: flex; align-items: center; flex-wrap: wrap; gap: 8px 12px; padding: 8px 12px; font-size: 12px; background: #19293b; }
.build-log header span { color: #a4b9cf; }
.log-error-text { color: #fecaca; overflow-wrap: anywhere; }
.build-log pre { margin: 0; padding: 12px; min-height: 70px; max-height: min(360px, 50vh); overflow: auto; overscroll-behavior: contain; white-space: pre-wrap; overflow-wrap: anywhere; font: 12px/1.75 var(--mono); }
@media (max-width: 540px) {
  .jenkins-build { padding: 4px; }
}
</style>
