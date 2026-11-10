<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref } from "vue";
import { ApiError, request } from "./api";

interface Usage { available: boolean; usagePercent: number | null }
interface SystemSnapshot {
  hostname: string; platform: string; platformVersion: string; arch: string; observedAt: string;
  cpu: Usage & { cores: number; model: string };
  memory: Usage & { totalBytes: number; usedBytes: number };
  disk: Usage & { path: string; totalBytes: number; usedBytes: number; freeBytes: number };
}
const emit = defineEmits<{ error: [unknown] }>();
const snapshot = ref<SystemSnapshot>();
const uptime = ref(0), loading = ref(false), error = ref("");
let timer: ReturnType<typeof setTimeout> | undefined;
let abort: AbortController | undefined;
let disposed = false, failures = 0;

function bytes(value?: number) {
  if (value == null || !Number.isFinite(value)) return "—";
  const units = ["B", "KiB", "MiB", "GiB", "TiB"];
  const index = value > 0 ? Math.min(Math.floor(Math.log(value) / Math.log(1024)), units.length - 1) : 0;
  return `${(value / 1024 ** index).toFixed(index > 0 ? 1 : 0)} ${units[index]}`;
}
const elapsed = computed(() => {
  const seconds = uptime.value, days = Math.floor(seconds / 86400), hours = Math.floor(seconds / 3600) % 24, minutes = Math.floor(seconds / 60) % 60;
  return days ? `${days}天 ${hours}小时` : hours ? `${hours}小时 ${minutes}分钟` : minutes ? `${minutes}分钟` : `${seconds}秒`;
});
const platformNames: Record<string, string> = { windows: "Windows", linux: "Linux", darwin: "macOS" };
const cards = computed(() => {
  const s = snapshot.value;
  return [
    { id: "cpu", name: "CPU", data: s?.cpu, detail: s?.cpu.model || "", extra: s?.cpu.cores ? `${s.cpu.cores} 个逻辑处理器` : "" },
    { id: "memory", name: "内存", data: s?.memory, detail: s?.memory.available ? `已用 ${bytes(s.memory.usedBytes)} / 共 ${bytes(s.memory.totalBytes)}` : "", extra: "" },
    { id: "disk", name: "数据磁盘", data: s?.disk, detail: s?.disk.available ? `已用 ${bytes(s.disk.usedBytes)} / 共 ${bytes(s.disk.totalBytes)}` : "", extra: s?.disk.available ? `可用 ${bytes(s.disk.freeBytes)}` : "" },
  ];
});
function percent(usage?: Usage) {
  return usage?.available && usage.usagePercent != null && Number.isFinite(usage.usagePercent)
    ? Math.min(100, Math.max(0, usage.usagePercent)) : undefined;
}
function label(usage?: Usage) {
  const value = percent(usage);
  if (value != null) return `${value.toFixed(1)}%`;
  return usage?.available || (loading.value && !snapshot.value) ? "采集中" : "暂不可用";
}
function stopTimer() { if (timer) clearTimeout(timer); timer = undefined; }
function schedule() {
  stopTimer();
  if (!disposed && !document.hidden && failures < 3) timer = setTimeout(() => void refresh(), 5000);
}
async function refresh(manual = false) {
  if (disposed || document.hidden || loading.value) return;
  stopTimer();
  if (manual) failures = 0;
  const current = new AbortController();
  abort = current;
  loading.value = true;
  try {
    const signal = AbortSignal.any([current.signal, AbortSignal.timeout(10000)]);
    const result = await request<{ system?: SystemSnapshot; uptimeSeconds: number }>("/status?system=1", "GET", undefined, signal);
    if (disposed || current.signal.aborted) return;
    if (!result.system) throw new Error("系统指标不可用");
    snapshot.value = result.system;
    uptime.value = result.uptimeSeconds;
    failures = 0;
    error.value = "";
  } catch (cause) {
    if (disposed || current.signal.aborted) return;
    failures++;
    error.value = failures >= 3 ? "监控读取失败，请刷新重试" : "监控暂未更新";
    if (cause instanceof ApiError && cause.status === 401) emit("error", cause);
  } finally {
    if (abort === current) { loading.value = false; abort = undefined; schedule(); }
  }
}
function visibilityChanged() {
  stopTimer();
  if (document.hidden) abort?.abort();
  else if (failures < 3) void refresh();
}
onMounted(() => { document.addEventListener("visibilitychange", visibilityChanged); void refresh(); });
onUnmounted(() => { disposed = true; stopTimer(); abort?.abort(); document.removeEventListener("visibilitychange", visibilityChanged); });
</script>

<template>
  <section class="system-monitor" aria-label="运行监控">
    <header class="monitor-heading">
      <h2>运行监控</h2>
      <span class="monitor-update" v-if="snapshot">{{ new Date(snapshot.observedAt).toLocaleTimeString('zh-CN', { timeZone: 'Asia/Shanghai', hour12: false }) }}</span>
      <button class="secondary" :disabled="loading" @click="refresh(true)">{{ loading ? '读取中…' : '刷新监控' }}</button>
    </header>
    <p v-if="error" class="monitor-error" role="status">{{ error }}</p>
    <dl class="monitor-system">
      <div><dt>操作系统</dt><dd>{{ snapshot?.platformVersion || platformNames[snapshot?.platform || ''] || '—' }}<small v-if="snapshot?.arch">{{ snapshot.arch === 'amd64' ? 'x86_64' : snapshot.arch }}</small></dd></div>
      <div><dt>主机名称</dt><dd>{{ snapshot?.hostname || '—' }}</dd></div>
      <div><dt>服务运行时长</dt><dd>{{ snapshot ? elapsed : '—' }}</dd></div>
    </dl>
    <div class="monitor-resources">
      <article v-for="card in cards" :key="card.id" class="resource-card" :aria-label="card.name + ' 使用情况'">
        <h3>{{ card.name }}</h3>
        <strong class="resource-value" :class="{ unavailable: percent(card.data) == null }">{{ label(card.data) }}</strong>
        <progress v-if="percent(card.data) != null" :value="percent(card.data)" max="100" :aria-label="card.name + ' 使用率'" :class="{ warning: (percent(card.data) || 0) >= 85 }"></progress>
        <div v-else class="resource-track"></div>
        <p class="resource-detail" :title="card.detail">{{ card.detail || '—' }}</p>
        <small class="resource-extra">{{ card.extra || '\u00a0' }}</small>
      </article>
    </div>
    <div v-if="snapshot?.disk.path" class="monitor-path"><span>数据目录所在磁盘</span><code>{{ snapshot.disk.path }}</code></div>
  </section>
</template>

<style scoped>
.monitor-heading { display: flex; align-items: center; gap: 14px; margin-bottom: 20px; }
.monitor-heading h2 { margin: 0; font-size: 16px; color: var(--text); }
.monitor-update { margin-left: auto; color: var(--muted); font: 12px var(--mono); }
.monitor-heading button { margin-left: auto; }
.monitor-update + button { margin-left: 0; }
.monitor-system { display: grid; grid-template-columns: 1.3fr 1fr 1fr; gap: 20px; margin: 0 0 22px; }
.monitor-system > div { min-width: 0; }
dt { font-size: 12px; color: var(--muted); margin-bottom: 9px; }
dd { margin: 0; font-size: 14px; line-height: 1.6; overflow-wrap: anywhere; }
dd small { display: inline-block; margin-left: 10px; color: var(--muted); font: 11px var(--mono); }
.monitor-resources { display: grid; grid-template-columns: repeat(3, minmax(0, 1fr)); gap: 16px; }
.resource-card { min-width: 0; border: 1px solid var(--border); border-radius: var(--card-radius); padding: 20px; }
h3 { margin: 0 0 16px; font-size: 13px; font-weight: 500; color: var(--muted); }
.resource-value { display: block; font-size: 32px; line-height: 1.2; font-weight: 600; font-variant-numeric: tabular-nums; color: var(--text); margin-bottom: 18px; }
.resource-value.unavailable { font-size: 22px; line-height: 38px; color: var(--muted); }
progress, .resource-track { display: block; width: 100%; height: 6px; border: 0; border-radius: 3px; overflow: hidden; background: var(--border-subtle); accent-color: var(--accent); }
progress::-webkit-progress-bar { background: var(--border-subtle); }
progress::-webkit-progress-value { background: var(--accent); border-radius: 3px; }
progress::-moz-progress-bar { background: var(--accent); border-radius: 3px; }
progress.warning { accent-color: var(--warning); }
progress.warning::-webkit-progress-value { background: var(--warning); }
progress.warning::-moz-progress-bar { background: var(--warning); }
.resource-detail { margin: 16px 0 6px; color: var(--text); font-size: 12px; line-height: 1.6; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.resource-extra { color: var(--muted); font-size: 12px; }
.monitor-path { display: flex; flex-wrap: wrap; gap: 8px 16px; margin-top: 16px; color: var(--muted); font-size: 12px; }
.monitor-path code { font: 12px var(--mono); overflow-wrap: anywhere; }
.monitor-error { margin: -4px 0 16px; color: var(--warning); font-size: 12px; }
@media (max-width: 1000px) { .resource-card { padding: 16px; } .monitor-resources { gap: 12px; } }
@media (max-width: 700px) {
  .monitor-system { grid-template-columns: 1fr; gap: 16px; }
  .monitor-system > div { display: grid; grid-template-columns: 96px minmax(0, 1fr); align-items: baseline; }
  dt { margin: 0; } .monitor-resources { grid-template-columns: 1fr; }
  .monitor-heading { gap: 8px; } .monitor-update { font-size: 11px; }
  .resource-value { font-size: 28px; margin-bottom: 14px; }
}
</style>
