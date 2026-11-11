<script setup lang="ts">
import { computed, nextTick, onUnmounted, ref, watch } from "vue";
import { ApiError, request } from "./api";
import { hostTime } from "./hosts";
import { jobKindLabel, type JobSummary } from "./jobs";
import { softwareJobErrorMessages, softwareJobPhaseLabel, softwareJobStatusLabel, softwareState, type SoftwareInstance } from "./software";

const props = defineProps<{ mode?: "jobs" | "runtime"; jobId?: string; softwareId?: string; title?: string; hostName?: string }>();
const emit = defineEmits<{ updated: [JobSummary] }>();
const mode = computed(() => props.jobId ? "jobs" : props.softwareId ? "runtime" : props.mode || "jobs");
const id = computed(() => props.jobId || props.softwareId || "");
const job = ref<JobSummary>();
const software = ref<SoftwareInstance>();
const reading = ref(false), paused = ref(false), follow = ref(true);
const text = ref(""), readAt = ref(""), source = ref(""), error = ref("");
const limited = ref(false);
const failure = ref<{ code: string; traceId: string; at: string }>();
const output = ref<HTMLElement>();
let disposed = false, generation = 0, failures = 0;
let timer: ReturnType<typeof setTimeout> | undefined;
let controller: AbortController | undefined;
const live = computed(() => !!id.value && (mode.value === "runtime" || !!job.value &&
  (["QUEUED", "RUNNING", "UNKNOWN"].includes(job.value.status) || job.value.targetAckPending)));
const title = computed(() => props.title || job.value?.subjectName || software.value?.name || (job.value ? jobKindLabel(job.value.kind) : "执行记录"));
const host = computed(() => props.hostName || job.value?.hostName || software.value?.hostName || "");
const state = computed(() => job.value ? softwareJobStatusLabel(job.value.status) : software.value ? softwareState(software.value).label : "");
const receipt = computed(() => source.value === "JOB_RECEIPT");

function stopTimer() { if (timer) clearTimeout(timer); timer = undefined; }
function schedule() {
  stopTimer();
  if (disposed || paused.value || !id.value || (!live.value && !failures)) return;
  timer = setTimeout(() => {
    if (document.hidden) schedule();
    else void readOutput();
  }, 3000);
}
async function readOutput(retry = false) {
  if (!id.value || reading.value || disposed) return;
  stopTimer();
  if (retry) { paused.value = false; failures = 0; }
  const revision = generation, selected = id.value, selectedMode = mode.value;
  const abort = new AbortController();
  controller = abort;
  reading.value = true;
  const current = () => !disposed && revision === generation && selected === id.value && !abort.signal.aborted;
  try {
    if (selectedMode === "jobs") {
      const actual = await request<JobSummary>(`/jobs/${selected}`, "GET", undefined, abort.signal);
      if (!current()) return;
      job.value = actual;
      emit("updated", actual);
      if (actual.status === "QUEUED") {
        source.value = "JOB_RECEIPT";
        text.value = "";
        error.value = "";
        failure.value = undefined;
        failures = 0;
        return;
      }
    } else {
      const actual = await request<SoftwareInstance>(`/software-instances/${selected}`, "GET", undefined, abort.signal);
      if (!current()) return;
      software.value = actual;
    }
    const result = await request<{ text: string; limited?: boolean; observedAt?: string; source?: string }>(
      selectedMode === "jobs" ? `/jobs/${selected}/logs` : `/software-instances/${selected}/logs`, "GET", undefined, abort.signal,
    );
    if (!current()) return;
    source.value = result.source || "";
    text.value = receipt.value ? "" : result.text;
    limited.value = !!result.limited;
    readAt.value = result.observedAt || new Date().toISOString();
    error.value = "";
    failure.value = undefined;
    failures = 0;
    await nextTick();
    if (current() && follow.value && output.value) output.value.scrollTop = output.value.scrollHeight;
  } catch (cause) {
    if (!current()) return;
    error.value = "日志读取未完成，请检查主机连接和权限后重试。";
    failure.value = {
      code: cause instanceof ApiError ? cause.code : "REQUEST_NOT_CONFIRMED",
      traceId: cause instanceof ApiError ? cause.traceId : "",
      at: cause instanceof ApiError ? cause.timestamp : new Date().toISOString(),
    };
    if (++failures >= 3) paused.value = true;
  } finally {
    if (!disposed && revision === generation && controller === abort) {
      reading.value = false;
      controller = undefined;
      schedule();
    }
  }
}
function pause() { paused.value = true; stopTimer(); controller?.abort(); }
function followLatest() { follow.value = true; if (output.value) output.value.scrollTop = output.value.scrollHeight; }
function scrolled() {
  const pane = output.value;
  if (pane) follow.value = pane.scrollHeight - pane.clientHeight - pane.scrollTop < 32;
}
watch([mode, id], () => {
  generation++;
  stopTimer();
  controller?.abort();
  controller = undefined;
  job.value = undefined;
  software.value = undefined;
  reading.value = paused.value = limited.value = false;
  follow.value = true;
  text.value = readAt.value = source.value = error.value = "";
  failure.value = undefined;
  failures = 0;
  if (id.value) void readOutput();
}, { immediate: true });
onUnmounted(() => { disposed = true; generation++; stopTimer(); controller?.abort(); });
</script>

<template>
  <section class="software-log-viewer" :aria-label="mode === 'jobs' ? '执行日志内容' : '运行日志内容'">
    <div v-if="id" class="log-context"><strong>{{ title }}</strong><span>{{ host }}</span><span>{{ state }}</span><span v-if="job">{{ jobKindLabel(job.kind) }}</span></div>
    <p v-if="error" class="logs-error" role="status">{{ error }}{{ paused ? ' 自动刷新已暂停。' : '' }}</p>
    <section :class="receipt ? 'receipt-output' : 'execution-terminal'" :aria-label="mode === 'jobs' ? '执行输出' : '运行输出'">
      <header><strong>{{ receipt ? '执行结果' : mode === 'jobs' ? '执行输出' : '运行日志' }}</strong><span v-if="id && !receipt">{{ paused ? '已暂停' : live ? '实时更新' : '执行结束' }}</span>
        <button v-if="id && live && !paused" class="quiet" @click="pause">暂停刷新</button>
        <button class="quiet" :disabled="reading || !id" @click="readOutput(true)">{{ reading ? '读取中…' : paused ? '继续刷新' : '刷新输出' }}</button>
      </header>
      <div v-if="receipt && job" class="receipt-summary" role="status">
        <strong>{{ softwareJobStatusLabel(job.status) }}</strong><p>{{ softwareJobPhaseLabel(job.phase) }}</p>
        <p v-if="job.errorCode">{{ softwareJobErrorMessages[job.errorCode] || '操作结果尚未确认，请核对原任务。' }}</p>
        <time>{{ hostTime(job.updatedAt) }}</time>
      </div>
      <pre v-else ref="output" class="execution-output" tabindex="0" :aria-label="mode === 'jobs' ? '执行过程输出' : '运行日志输出'" @scroll="scrolled">{{ text || (id ? '等待日志输出…' : '选择记录查看日志') }}</pre>
      <footer v-if="!receipt && (readAt || !follow)"><span>{{ limited ? '最近输出 · ' : '' }}{{ hostTime(readAt) }}</span><button v-if="!follow" class="quiet" @click="followLatest">回到最新</button></footer>
    </section>
    <details v-if="job || failure" class="log-diagnostics"><summary>诊断信息</summary>
      <dl><template v-if="job"><dt>作业标识</dt><dd><code>{{ job.id }}</code></dd><dt>阶段标识</dt><dd><code>{{ job.phase }}</code></dd><dt>追踪标识</dt><dd><code>{{ job.traceId }}</code></dd></template>
        <template v-if="failure || job?.errorCode"><dt>错误码</dt><dd><code>{{ failure?.code || job?.errorCode }}</code></dd></template>
        <template v-if="failure"><dt>请求追踪</dt><dd><code>{{ failure.traceId || '—' }}</code></dd><dt>发生时间</dt><dd>{{ hostTime(failure.at) }}</dd></template>
      </dl>
    </details>
  </section>
</template>

<style scoped>
.software-log-viewer { display: flex; flex-direction: column; min-width: 0; min-height: 0; }
.log-context { display: flex; flex-wrap: wrap; gap: 8px 16px; align-items: center; margin-bottom: 14px; font-size: 12px; color: var(--muted); }
.log-context strong { color: var(--text); font-size: 14px; overflow-wrap: anywhere; }
.logs-error { margin: 0 0 16px; color: #b45309; font-size: 13px; }
.execution-terminal { overflow: hidden; border: 1px solid #26374a; border-radius: 8px; background: #111d2b; color: #d6e3ef; }
.execution-terminal, .receipt-output { display: flex; flex-direction: column; flex: 1; min-height: 0; }
.log-context, .logs-error, .execution-terminal header, .execution-terminal footer, .receipt-output header { flex-shrink: 0; }
.execution-terminal header, .execution-terminal footer, .receipt-output header { display: flex; flex-wrap: wrap; align-items: center; gap: 10px; padding: 11px 16px; }
.execution-terminal header { border-bottom: 1px solid #2b3d50; background: #19293b; font-size: 12px; }
.execution-terminal header::before { content: ">_"; color: #72a9cd; font: 13px Consolas, monospace; margin-right: 2px; }
header span { flex: 1; font-size: 11px; }
.execution-terminal header span { color: #90a8bf; }
.execution-terminal .quiet { color: #d1e0ed; border-color: #41566e; background: transparent; font-size: 11px; padding: 5px 8px; }
.execution-output { flex: 1; min-height: min(360px, 45vh); max-height: min(600px, 60vh); overflow: auto; overscroll-behavior: contain; margin: 0; padding: 18px; white-space: pre-wrap; overflow-wrap: anywhere; font: 12px/1.85 Consolas, monospace; tab-size: 2; }
.execution-terminal footer { justify-content: space-between; color: #90a8bf; font-size: 11px; border-top: 1px solid #26384b; }
.receipt-output { border: 1px solid var(--border); border-radius: 8px; background: #fafcff; }
.receipt-output header { border-bottom: 1px solid var(--border); font-size: 12px; }
.receipt-output header strong { margin-right: auto; }
.receipt-summary { flex: 1; min-height: 0; overflow-y: auto; padding: 28px; color: var(--text); }.receipt-summary > strong { font-size: 20px; }.receipt-summary p, .receipt-summary time { color: var(--muted); font-size: 13px; }
.log-diagnostics { flex-shrink: 0; max-height: min(180px, 30vh); overflow-y: auto; overscroll-behavior: contain; margin-top: 16px; color: var(--muted); font-size: 12px; }.log-diagnostics summary { cursor: pointer; }.log-diagnostics dl { display: grid; grid-template-columns: 80px minmax(0, 1fr); gap: 10px; margin-top: 16px; }.log-diagnostics dd { margin: 0; overflow-wrap: anywhere; }
@media (max-width: 550px) { .execution-output { padding: 12px; }.execution-terminal header { padding: 10px; gap: 8px; }.receipt-summary { padding: 20px; } }
</style>
