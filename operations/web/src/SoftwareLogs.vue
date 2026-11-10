<script setup lang="ts">
import { computed, onUnmounted, ref, watch } from "vue";
import { ApiError, request } from "./api";
import { hostTime, type Host } from "./hosts";
import { jobKindLabel, type JobSummary, type PageResult } from "./jobs";
import SoftwareLogViewer from "./SoftwareLogViewer.vue";
import { softwareJobStatusLabel, softwareLifecycleAvailable, softwareState, type SoftwareInstance } from "./software";

const props = defineProps<{ mode?: "jobs" | "runtime"; jobId?: string; softwareId?: string }>();
const emit = defineEmits<{ selected: [id: string, source: "jobs" | "runtime"] }>();
const mode = computed(() => props.mode || "jobs");
const requestedId = computed(() => (mode.value === "jobs" ? props.jobId : props.softwareId) || "");
const hosts = ref<Host[]>([]), software = ref<SoftwareInstance[]>([]), jobs = ref<JobSummary[]>([]);
const pinnedJob = ref<JobSummary>();
const selectedId = ref(""), query = ref(""), hostId = ref(""), status = ref(""), kind = ref("SOFTWARE");
const loading = ref(false), listError = ref("");
const listFailure = ref<{ code: string; traceId: string; at: string }>();
const pageSize = ref(20), page = ref(0), cursors = ref([""]), nextCursor = ref("");
let disposed = false, generation = 0;
let listAbort: AbortController | undefined;
let searchTimer: ReturnType<typeof setTimeout> | undefined;
let observedJob: JobSummary | undefined;
const hostName = (id: string) => hosts.value.find(host => host.id === id)?.name || "主机已移除";
const jobRecord = (job: JobSummary) => {
  const item = software.value.find(item => item.id === job.softwareInstanceId || item.installationJobId === job.id || item.lastJobId === job.id);
  return { id: job.id, hostId: job.targetId, name: job.subjectName || item?.name || jobKindLabel(job.kind), kind: jobKindLabel(job.kind),
    state: job.status, stateLabel: job.phase === "CANCELLED" ? "已取消" : softwareJobStatusLabel(job.status), at: job.createdAt, hostName: job.hostName || hostName(job.targetId) };
};
const runtimeRecords = computed(() => software.value
  .filter(item => item.runtimeKind !== "TOOL" && item.managementMode !== "RELEASED" && item.actualState !== "REMOVED" && softwareLifecycleAvailable(item))
  .map(item => ({ id: item.id, hostId: item.hostId, name: item.name, kind: "运行日志", state: item.actualState, stateLabel: softwareState(item).label, at: item.updatedAt, hostName: item.hostName || hostName(item.hostId) }))
  .filter(item => (!hostId.value || item.hostId === hostId.value) && `${item.name} ${item.hostName}`.toLowerCase().includes(query.value.trim().toLowerCase()))
  .sort((a, b) => b.at.localeCompare(a.at) || b.id.localeCompare(a.id)));
const records = computed(() => mode.value === "jobs"
  ? jobs.value.map(jobRecord)
  : runtimeRecords.value.slice(page.value * pageSize.value, (page.value + 1) * pageSize.value));
const selected = computed(() => records.value.find(item => item.id === selectedId.value) ||
  (mode.value === "jobs" && pinnedJob.value?.id === selectedId.value ? jobRecord(pinnedJob.value) : undefined));
const hasNext = computed(() => mode.value === "jobs" ? !!nextCursor.value : (page.value + 1) * pageSize.value < runtimeRecords.value.length);

function selectRecord(id: string) {
  if (id === selectedId.value) return;
  listAbort?.abort();
  listAbort = undefined;
  loading.value = false;
  selectedId.value = id;
  observedJob = undefined;
  listError.value = "";
  listFailure.value = undefined;
  emit("selected", id, mode.value);
}
function updated(job: JobSummary) {
  if (job.id !== selectedId.value) return;
  observedJob = job;
  jobs.value = jobs.value.map(item => item.id === job.id ? job : item);
  if (pinnedJob.value?.id === job.id) pinnedJob.value = job;
}
function matchesFilters(job: JobSummary) {
  const record = jobRecord(job);
  return (!hostId.value || record.hostId === hostId.value) && (!status.value || job.status === status.value) &&
    `${record.name} ${record.hostName}`.toLowerCase().includes(query.value.trim().toLowerCase());
}
async function loadRecords(preferredId = selectedId.value) {
  listAbort?.abort();
  const abort = new AbortController(), revision = generation, beforeRead = observedJob;
  listAbort = abort;
  loading.value = true;
  listError.value = "";
  listFailure.value = undefined;
  const params = new URLSearchParams({ limit: String(pageSize.value), kind: kind.value });
  if (cursors.value[page.value]) params.set("before", cursors.value[page.value]!);
  if (hostId.value) params.set("hostId", hostId.value);
  if (status.value) params.set("status", status.value);
  if (query.value.trim()) params.set("query", query.value.trim());
  try {
    const [targets, instances, history] = await Promise.all([
      request<Host[]>("/hosts", "GET", undefined, abort.signal),
      request<SoftwareInstance[]>(hostId.value ? `/software-instances?hostId=${encodeURIComponent(hostId.value)}` : "/software-instances", "GET", undefined, abort.signal),
      mode.value === "jobs" ? request<PageResult<JobSummary>>(`/jobs?${params}`, "GET", undefined, abort.signal) : Promise.resolve({ items: [], nextCursor: "" }),
    ]);
    if (disposed || revision !== generation || abort.signal.aborted) return;
    let pinned: JobSummary | undefined;
    if (mode.value === "jobs" && preferredId && !history.items.some(job => job.id === preferredId)) {
      try {
        const candidate = await request<JobSummary>(`/jobs/${preferredId}`, "GET", undefined, abort.signal);
        if (matchesFilters(candidate)) pinned = candidate;
      }
      catch (cause) {
        if (abort.signal.aborted) return;
        listError.value = "该记录暂不可读取，请选择其他记录或刷新重试。";
        listFailure.value = { code: cause instanceof ApiError ? cause.code : "REQUEST_NOT_CONFIRMED", traceId: cause instanceof ApiError ? cause.traceId : "", at: cause instanceof ApiError ? cause.timestamp : new Date().toISOString() };
      }
    }
    if (disposed || revision !== generation || abort.signal.aborted) return;
    if (observedJob && observedJob !== beforeRead && observedJob.id === preferredId) {
      const index = history.items.findIndex(item => item.id === observedJob!.id);
      if (index >= 0) history.items[index] = observedJob;
      else pinned = observedJob;
    }
    hosts.value = targets;
    software.value = instances;
    jobs.value = history.items;
    pinnedJob.value = pinned;
    nextCursor.value = history.nextCursor;
    loading.value = false;
    listAbort = undefined;
    const wanted = records.value.some(item => item.id === preferredId) || pinned?.id === preferredId ? preferredId : records.value[0]?.id || "";
    const lookupError = listError.value, lookupFailure = listFailure.value;
    selectRecord(wanted);
    if (lookupError) { listError.value = lookupError; listFailure.value = lookupFailure; }
  } catch (cause) {
    if (!disposed && revision === generation && !abort.signal.aborted) {
      listError.value = "日志记录加载未完成，请刷新重试。";
      listFailure.value = { code: cause instanceof ApiError ? cause.code : "REQUEST_NOT_CONFIRMED", traceId: cause instanceof ApiError ? cause.traceId : "", at: cause instanceof ApiError ? cause.timestamp : new Date().toISOString() };
    }
  } finally {
    if (!disposed && revision === generation && listAbort === abort) loading.value = false;
  }
}
function clearSelection() {
  listAbort?.abort();
  listAbort = undefined;
  loading.value = false;
  jobs.value = [];
  pinnedJob.value = undefined;
  listError.value = "";
  listFailure.value = undefined;
  selectRecord("");
}
function resetFilters() {
  generation++;
  page.value = 0;
  cursors.value = [""];
  nextCursor.value = "";
  clearSelection();
  if (searchTimer) clearTimeout(searchTimer);
  searchTimer = setTimeout(() => { searchTimer = undefined; void loadRecords(""); }, 250);
}
function turnPage(direction: number) {
  if (loading.value || direction > 0 && !hasNext.value || direction < 0 && !page.value) return;
  generation++;
  if (direction > 0 && mode.value === "jobs") cursors.value = [...cursors.value.slice(0, page.value + 1), nextCursor.value];
  page.value += direction;
  clearSelection();
  void loadRecords("");
}
watch([query, hostId, status, kind, pageSize], resetFilters);
watch([mode, requestedId], ([source, id], previous) => {
  if (previous?.[0] === source && id === selectedId.value) return;
  generation++;
  if (searchTimer) clearTimeout(searchTimer);
  listAbort?.abort();
  page.value = 0;
  cursors.value = [""];
  nextCursor.value = "";
  selectedId.value = "";
  observedJob = undefined;
  jobs.value = [];
  pinnedJob.value = undefined;
  listError.value = "";
  listFailure.value = undefined;
  void loadRecords(id);
}, { immediate: true });
onUnmounted(() => { disposed = true; generation++; listAbort?.abort(); if (searchTimer) clearTimeout(searchTimer); });
</script>

<template>
  <section class="software-logs" :aria-label="mode === 'jobs' ? '执行记录' : '运行日志'">
    <div class="logs-toolbar">
      <input v-model="query" type="search" aria-label="搜索日志" placeholder="搜索软件或主机" />
      <select v-model="hostId" aria-label="筛选日志主机"><option value="">全部主机</option><option v-for="host in hosts" :key="host.id" :value="host.id">{{ host.name }}</option></select>
      <select v-if="mode === 'jobs'" v-model="status" aria-label="筛选执行状态"><option value="">全部状态</option><option value="QUEUED">排队中</option><option value="RUNNING">执行中</option><option value="SUCCEEDED">已完成</option><option value="FAILED">未完成</option><option value="UNKNOWN">待确认</option></select>
      <button class="secondary" :disabled="loading" @click="loadRecords()">{{ loading ? '加载中…' : '刷新记录' }}</button>
    </div>
    <p v-if="listError" class="logs-error" role="status">{{ listError }}</p>
    <div class="logs-workspace">
      <aside class="logs-list" aria-label="日志列表">
        <p v-if="!records.length" class="empty">{{ loading ? '正在加载' : query || hostId || status ? '没有匹配的日志' : '暂无日志记录' }}</p>
        <button v-if="pinnedJob && selected?.id === pinnedJob.id" class="log-record selected" :aria-label="selected.name" aria-pressed="true" @click="selectRecord(pinnedJob.id)">
          <span><strong>{{ selected.name }}</strong><small>{{ selected.hostName }} · {{ selected.kind }}</small><small>{{ hostTime(selected.at) }}</small></span><span>{{ selected.stateLabel }}</span>
        </button>
        <button v-for="item in records" :key="item.id" class="log-record" :class="{ selected: item.id === selectedId }" :aria-label="item.name" :aria-pressed="item.id === selectedId" @click="selectRecord(item.id)">
          <span><strong>{{ item.name }}</strong><small>{{ item.hostName }} · {{ item.kind }}</small><small>{{ hostTime(item.at) }}</small></span><span>{{ item.stateLabel }}</span>
        </button>
      </aside>
      <SoftwareLogViewer :mode="mode" :job-id="mode === 'jobs' ? selectedId : undefined" :software-id="mode === 'runtime' ? selectedId : undefined" :title="selected?.name" :host-name="selected?.hostName" @updated="updated" />
    </div>
    <footer class="logs-pagination" aria-label="日志分页">
      <label>每页<select v-model.number="pageSize" aria-label="日志每页条数"><option :value="20">20</option><option :value="50">50</option><option :value="100">100</option></select>条</label>
      <span>第 {{ page + 1 }} 页</span><button class="secondary" :disabled="loading || !page" @click="turnPage(-1)">上一页</button><button class="secondary" :disabled="loading || !hasNext" @click="turnPage(1)">下一页</button>
    </footer>
    <details v-if="listFailure" class="logs-list-diagnostic"><summary>诊断信息</summary><p>{{ listFailure.code }} · {{ listFailure.traceId }} · {{ hostTime(listFailure.at) }}</p></details>
  </section>
</template>

<style scoped>
.software-logs { min-width: 0; }
.logs-toolbar { display: flex; flex-wrap: wrap; gap: 10px; margin-bottom: 18px; }
.logs-toolbar input { width: 240px; min-width: 0; margin: 0; }.logs-toolbar select { width: 150px; margin: 0; }.logs-toolbar button { margin-left: auto; }
.logs-workspace { display: grid; grid-template-columns: minmax(220px, 280px) minmax(0, 1fr); gap: 20px; }
.logs-list { max-height: 620px; overflow-y: auto; border: 1px solid var(--border); border-radius: 7px; }
.log-record { display: flex; justify-content: space-between; gap: 12px; width: 100%; padding: 15px 12px; border: 0; border-bottom: 1px solid var(--border); border-radius: 0; background: transparent; text-align: left; }
.log-record:hover, .log-record.selected { background: #eff6ff; }.log-record.selected { box-shadow: inset 3px 0 #2563eb; }
.log-record > span:first-child { min-width: 0; }.log-record strong { font-size: 13px; font-weight: 500; overflow-wrap: anywhere; }
.log-record small { display: block; margin-top: 7px; color: var(--muted); font-size: 11px; overflow-wrap: anywhere; }.log-record > span:last-child { white-space: nowrap; font-size: 12px; color: var(--muted); }
.logs-error { margin: 0 0 16px; color: #b45309; font-size: 13px; }
.logs-pagination { display: flex; flex-wrap: wrap; justify-content: flex-end; align-items: center; gap: 10px; margin-top: 18px; color: var(--muted); font-size: 12px; }
.logs-pagination label { display: flex; align-items: center; gap: 8px; margin-right: auto; }.logs-pagination select { width: 70px; margin: 0; }
.logs-list-diagnostic { margin-top: 16px; color: var(--muted); font-size: 12px; }.logs-list-diagnostic p { overflow-wrap: anywhere; }
@media (max-width: 900px) { .logs-workspace { grid-template-columns: 1fr; }.logs-list { max-height: 220px; } }
@media (max-width: 550px) { .logs-toolbar input { width: 100%; }.logs-toolbar select { flex: 1; } }
</style>
