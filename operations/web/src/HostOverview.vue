<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref } from "vue";
import { request } from "./api";
import { requestAllPages } from "./listPages";
import {
  ElButton,
  ElEmpty,
  ElMessage,
  ElProgress,
  ElTable,
  ElTableColumn,
  ElTag,
} from "element-plus";
import {
  metricBytes as bytes,
  metricSnapshot,
  metricsLive as live,
  type HostObservation,
  type HostMetricSnapshot,
} from "./hostMetrics";
import HostResourceDetails from "./HostResourceDetails.vue";
import { hostTime, type Host } from "./hosts";
import {
  softwareIsHistoryOnly,
  softwareState,
  type SoftwareInstance,
} from "./software";
import SoftwareIcon from "./SoftwareIcon.vue";

const props = defineProps<{
  controller?: {
    release: string;
    listen: string;
    heapBytes: number;
    startedAt: string;
    stateDirectory: string;
  };
  connected: boolean;
  uptime: string;
}>();
const emit = defineEmits<{
  error: [unknown];
  navigate: [target: { view: string; hostId?: string; softwareId?: string; search?: string }];
}>();
const hosts = ref<Host[]>([]),
  software = ref<SoftwareInstance[]>([]),
  metrics = ref<Record<string, HostMetricSnapshot>>({});
const selectedId = ref(""),
  loading = ref(false);
const selected = computed(() =>
  hosts.value.find((h) => h.id === selectedId.value),
);
const current = computed(() => metrics.value[selectedId.value]);
const activeSoftware = computed(() =>
  software.value.filter(
    (s) =>
      s.managementMode !== "RELEASED" &&
      s.actualState !== "REMOVED" &&
      !softwareIsHistoryOnly(s),
  ),
);
const softwareOnHost = computed(() =>
  activeSoftware.value.filter((s) => s.hostId === selectedId.value),
);
const runningSoftwareCount = computed(
  () =>
    activeSoftware.value.filter((s) => {
      const state = s.observation?.state || s.actualState;
      return state === "RUNNING" || state === "INSTALLED";
    }).length,
);
const observedCount = computed(
  () => hosts.value.filter((host) => live(metrics.value[host.id])).length,
);
const maintenanceCount = computed(
  () => hosts.value.filter((host) => host.maintenance).length,
);

function usageRatio(used?: number, total?: number): number | undefined {
  if (used === undefined || total === undefined || used < 0 || total <= 0) {
    return undefined;
  }
  return Math.max(0, Math.min(100, (used / total) * 100));
}
function usageTone(value?: number): string {
  if (value === undefined) return "";
  if (value >= 85) return "bad";
  if (value >= 70) return "warn";
  return "ok";
}
function progressStatus(value?: number): "" | "success" | "warning" | "exception" {
  if (value === undefined) return "";
  if (value >= 85) return "exception";
  if (value >= 70) return "warning";
  return "success";
}

const serverRows = computed(() =>
  hosts.value.map((host) => {
    const snap = metrics.value[host.id];
    const isLive = live(snap);
    const hasMetrics = !!snap && ["READY", "PARTIAL"].includes(snap.code);
    const cpu = hasMetrics ? snap.cpuPercent : undefined;
    const memUsed =
      hasMetrics &&
      snap.memoryTotalBytes !== undefined &&
      snap.memoryAvailableBytes !== undefined
        ? snap.memoryTotalBytes - snap.memoryAvailableBytes
        : undefined;
    const mem = usageRatio(memUsed, hasMetrics ? snap.memoryTotalBytes : undefined);
    const disk = hasMetrics
      ? usageRatio(snap.rootUsedBytes, snap.rootTotalBytes)
      : undefined;
    const hostSoft = activeSoftware.value.filter((s) => s.hostId === host.id);
    const runningSoft = hostSoft.filter((s) => {
      const st = s.observation?.state || s.actualState;
      return st === "RUNNING" || st === "INSTALLED";
    }).length;
    const executorReady =
      host.capabilities?.mutation?.status === "AVAILABLE" &&
      host.capabilities?.reachability?.status === "AVAILABLE";
    return {
      host,
      snap,
      isLive,
      hasMetrics,
      cpu,
      mem,
      memUsed,
      memTotal: hasMetrics ? snap.memoryTotalBytes : undefined,
      disk,
      diskUsed: hasMetrics ? snap.rootUsedBytes : undefined,
      diskTotal: hasMetrics ? snap.rootTotalBytes : undefined,
      softwareCount: hostSoft.length,
      runningSoft,
      executorReady,
    };
  }),
);

const attentionItems = computed(() => {
  const items: {
    id: string;
    kind: "HOST" | "SOFTWARE";
    title: string;
    detail: string;
    actionLabel: string;
    target: { view: string; hostId?: string; softwareId?: string };
  }[] = [];
  for (const row of serverRows.value) {
    if (row.host.maintenance) {
      items.push({
        id: `maint-${row.host.id}`,
        kind: "HOST",
        title: `主机「${row.host.name}」处于维护模式`,
        detail: "暂停接收软件安装与防火墙变更",
        actionLabel: "管理主机",
        target: { view: "hosts", hostId: row.host.id },
      });
    } else if (!row.isLive) {
      items.push({
        id: `offline-${row.host.id}`,
        kind: "HOST",
        title: `主机「${row.host.name}」资源采集离线`,
        detail: row.host.kind === "LOCAL" ? "控制器本机" : row.host.address,
        actionLabel: "检查接入",
        target: { view: "hosts", hostId: row.host.id },
      });
    } else if (!row.executorReady) {
      items.push({
        id: `exec-${row.host.id}`,
        kind: "HOST",
        title: `主机「${row.host.name}」接入组件未就绪`,
        detail: "完成安装准备后可执行软件安装与端口管理",
        actionLabel: "去接入",
        target: { view: "hosts", hostId: row.host.id },
      });
    }
  }
  for (const s of activeSoftware.value) {
    if (s.managementMode !== "MANAGED") continue;
    const state = s.observation?.state || s.actualState;
    if (["STOPPED", "FAILED", "MISSING"].includes(state)) {
      const hostName =
        s.hostName ||
        hosts.value.find((h) => h.id === s.hostId)?.name ||
        s.hostId;
      items.push({
        id: `soft-${s.id}`,
        kind: "SOFTWARE",
        title: `${s.name}（${hostName}）状态异常`,
        detail:
          state === "STOPPED"
            ? "托管服务当前已停止"
            : state === "FAILED"
              ? "服务运行或安装失败"
              : "未检测到运行对象",
        actionLabel: "查看实例",
        target: { view: "software", softwareId: s.id },
      });
    }
  }
  return items;
});
let disposed = false,
  timer: ReturnType<typeof setTimeout> | undefined,
  refreshController: AbortController | undefined;
async function refresh() {
  if (loading.value || disposed) return;
  refreshController?.abort();
  const controller = new AbortController();
  refreshController = controller;
  loading.value = true;
  try {
    const [hs, ss, snapshots] = await Promise.all([
      request<Host[]>("/hosts", "GET", undefined, controller.signal),
      requestAllPages<SoftwareInstance>("/software-instances", controller.signal),
      request<HostObservation[]>("/host-observations", "GET", undefined, controller.signal),
    ]);
    if (disposed || controller.signal.aborted) return;
    hosts.value = hs;
    software.value = ss;
    if (!hs.some((h) => h.id === selectedId.value))
      selectedId.value = hs[0]?.id || "";
    metrics.value = Object.fromEntries(
      snapshots.map((observation) => [
        observation.hostId,
        metricSnapshot(observation),
      ]),
    );
  } catch (error) {
    if (!disposed && !controller.signal.aborted) emit("error", error);
  } finally {
    if (refreshController === controller) {
      refreshController = undefined;
      loading.value = false;
    }
  }
}
const detecting = ref(false),
  monitoring = ref(false);
async function detect() {
  if (!selectedId.value || detecting.value) return;
  detecting.value = true;
  try {
    await request("/observation-batches", "POST", {
      kind: "HOST",
      hostId: selectedId.value,
      idempotencyKey: crypto.randomUUID(),
    });
    ElMessage.success("已加入后台检测");
  } catch (error) {
    emit("error", error);
  } finally {
    detecting.value = false;
  }
}
async function toggleMonitoring() {
  if (!selected.value || monitoring.value) return;
  monitoring.value = true;
  try {
    await request("/hosts/" + selected.value.id + "/monitoring", "PATCH", {
      enabled: current.value?.enabled === false,
      expectedVersion: selected.value.version,
    });
    await refresh();
  } catch (error) {
    emit("error", error);
  } finally {
    monitoring.value = false;
  }
}
function poll() {
  timer = setTimeout(async () => {
    if (!document.hidden) await refresh();
    if (!disposed) poll();
  }, 5000);
}
onMounted(async () => {
  await refresh();
  if (!disposed) poll();
});
onUnmounted(() => {
  disposed = true;
  refreshController?.abort();
  if (timer) clearTimeout(timer);
});
</script>
<template>
  <section class="content host-overview" aria-label="主机概览">
    <div class="ops-kpi-grid">
      <button
        type="button"
        class="ops-kpi-card clickable"
        @click="emit('navigate', { view: 'hosts' })"
      >
        <span>服务器节点（在线 / 总数）</span>
        <strong>
          {{ observedCount }}
          <i class="kpi-sub-total">/ {{ hosts.length }}</i>
        </strong>
        <small>
          {{
            loading
              ? "正在检查资源…"
              : maintenanceCount
                ? `维护中 ${maintenanceCount} 台 · 点击管理`
                : "点击进入服务器管理"
          }}
        </small>
      </button>
      <button
        type="button"
        class="ops-kpi-card clickable"
        @click="emit('navigate', { view: 'software' })"
      >
        <span>软件实例（正常 / 总活跃）</span>
        <strong>
          {{ runningSoftwareCount }}
          <i class="kpi-sub-total">/ {{ activeSoftware.length }}</i>
        </strong>
        <small>点击管理已部署软件与状态</small>
      </button>
      <article class="ops-kpi-card">
        <span>需关注异常</span>
        <strong :class="attentionItems.length ? 'kpi-warn' : 'kpi-ok'">
          {{ attentionItems.length }}
        </strong>
        <small>
          {{
            attentionItems.length
              ? "存在离线主机、未接入或停止服务"
              : "所有已监测主机与服务运行正常"
          }}
        </small>
      </article>
      <article class="ops-kpi-card">
        <span>Ops 控制器</span>
        <strong class="controller-state" :class="connected ? 'kpi-ok' : 'kpi-bad'">
          {{ connected ? "运行中" : "连接中断" }}
        </strong>
        <small>{{ uptime }} · {{ bytes(controller?.heapBytes) }} 堆内存</small>
      </article>
    </div>

    <section v-if="attentionItems.length" class="overview-panel attention-panel" aria-label="异常与待处理事项">
      <header class="overview-panel-head" style="margin-bottom: 12px">
        <h2>待处理事项（{{ attentionItems.length }}）</h2>
        <small>优先恢复接入或启动异常服务</small>
      </header>
      <div class="attention-list">
        <div v-for="item in attentionItems.slice(0, 6)" :key="item.id" class="attention-row">
          <span class="state-tag state-warning">{{ item.kind === 'HOST' ? '主机' : '软件' }}</span>
          <div class="attention-copy">
            <strong>{{ item.title }}</strong>
            <small>{{ item.detail }}</small>
          </div>
          <button class="quiet" @click="emit('navigate', item.target)">
            {{ item.actionLabel }} →
          </button>
        </div>
      </div>
    </section>

    <section class="overview-panel" aria-label="多服务器对比总览">
      <header class="overview-panel-head">
        <div>
          <h2>服务器资源对比</h2>
          <small>点击任意行查看该服务器的详细进程、网络与挂载指标</small>
        </div>
        <div class="ops-el-toolbar-group">
          <ElButton :loading="loading" @click="refresh">
            {{ loading ? "刷新中…" : "刷新数据" }}
          </ElButton>
          <ElButton type="primary" @click="emit('navigate', { view: 'hosts' })">
            服务器管理
          </ElButton>
        </div>
      </header>
      <ElTable
        :data="serverRows"
        row-key="host.id"
        border
        stripe
        highlight-current-row
        style="width: 100%"
        @row-click="(row) => (selectedId = row.host.id)"
      >
        <ElTableColumn label="服务器" min-width="180" show-overflow-tooltip>
          <template #default="{ row }">
            <div class="server-cell">
              <span class="host-dot" :class="{ available: row.isLive }"></span>
              <div>
                <strong>{{ row.host.name }}</strong>
                <small>{{ row.host.kind === "LOCAL" ? "控制器本机" : row.host.address }}</small>
              </div>
            </div>
          </template>
        </ElTableColumn>
        <ElTableColumn label="状态与系统" min-width="175" show-overflow-tooltip>
          <template #default="{ row }">
            <div class="status-cell">
              <ElTag
                size="small"
                :type="row.host.maintenance ? 'warning' : row.isLive ? 'success' : 'info'"
              >
                {{ row.host.maintenance ? "维护中" : row.isLive ? "在线" : "离线/待检查" }}
              </ElTag>
              <small>
                {{ row.snap?.os || row.host.capabilities?.platform || "未检测系统" }}
                <template v-if="row.snap?.arch"> · {{ row.snap.arch }}</template>
              </small>
            </div>
          </template>
        </ElTableColumn>
        <ElTableColumn label="CPU 使用率" width="155">
          <template #default="{ row }">
            <div class="metric-bar-cell">
              <div class="metric-bar-head">
                <span>{{ row.cpu !== undefined ? `${row.cpu.toFixed(1)}%` : "—" }}</span>
                <small v-if="row.snap?.cpuCores">{{ row.snap.cpuCores }} 核</small>
              </div>
              <ElProgress
                :percentage="Math.round(row.cpu ?? 0)"
                :status="progressStatus(row.cpu)"
                :show-text="false"
                :stroke-width="6"
              />
            </div>
          </template>
        </ElTableColumn>
        <ElTableColumn label="内存使用率" width="175">
          <template #default="{ row }">
            <div class="metric-bar-cell">
              <div class="metric-bar-head">
                <span>{{ row.mem !== undefined ? `${row.mem.toFixed(1)}%` : "—" }}</span>
                <small v-if="row.memTotal">{{ bytes(row.memUsed) }} / {{ bytes(row.memTotal) }}</small>
              </div>
              <ElProgress
                :percentage="Math.round(row.mem ?? 0)"
                :status="progressStatus(row.mem)"
                :show-text="false"
                :stroke-width="6"
              />
            </div>
          </template>
        </ElTableColumn>
        <ElTableColumn label="根分区磁盘" width="175">
          <template #default="{ row }">
            <div class="metric-bar-cell">
              <div class="metric-bar-head">
                <span>{{ row.disk !== undefined ? `${row.disk.toFixed(1)}%` : "—" }}</span>
                <small v-if="row.diskTotal">{{ bytes(row.diskUsed) }} / {{ bytes(row.diskTotal) }}</small>
              </div>
              <ElProgress
                :percentage="Math.round(row.disk ?? 0)"
                :status="progressStatus(row.disk)"
                :show-text="false"
                :stroke-width="6"
              />
            </div>
          </template>
        </ElTableColumn>
        <ElTableColumn label="软件" width="90" align="center">
          <template #default="{ row }">
            <strong>{{ row.runningSoft }}</strong>
            <span class="muted"> / {{ row.softwareCount }}</span>
          </template>
        </ElTableColumn>
        <ElTableColumn label="操作" width="175" fixed="right">
          <template #default="{ row }">
            <div class="row-quick-actions" @click.stop>
              <ElButton
                link
                type="primary"
                size="small"
                @click="emit('navigate', { view: 'hosts', hostId: row.host.id })"
              >
                接入
              </ElButton>
              <ElButton
                link
                type="primary"
                size="small"
                @click="emit('navigate', { view: 'ports', hostId: row.host.id })"
              >
                端口
              </ElButton>
              <ElButton
                link
                type="primary"
                size="small"
                @click="emit('navigate', { view: 'software-install', hostId: row.host.id })"
              >
                装软件
              </ElButton>
            </div>
          </template>
        </ElTableColumn>
        <template #empty>
          <ElEmpty description="尚未接入服务器，请在服务器管理中添加首台 Linux 主机">
            <ElButton type="primary" @click="emit('navigate', { view: 'hosts' })">
              前往添加服务器
            </ElButton>
          </ElEmpty>
        </template>
      </ElTable>
    </section>

    <HostResourceDetails
      v-if="selected"
      :host="selected"
      :metrics="current"
      :loading="loading"
      :detecting="detecting"
      :monitoring="monitoring"
      @detect="detect"
      @monitoring="toggleMonitoring"
    >
      <template #software>
        <section class="overview-panel">
          <header class="overview-panel-head" style="margin-bottom: 14px">
            <h2>软件与端口（{{ selected.name }}）</h2>
            <small>{{ softwareOnHost.length }} 个实例</small>
          </header>
          <div v-if="!softwareOnHost.length" class="empty">
            此服务器尚未登记软件
          </div>
          <div v-else class="overview-software-list">
            <button
              v-for="item in softwareOnHost"
              :key="item.id"
              type="button"
              class="overview-software clickable-soft"
              @click="emit('navigate', { view: 'software', softwareId: item.id })"
            >
              <SoftwareIcon :type="item.softwareType" />
              <div>
                <strong>{{ item.name }}</strong>
                <small>
                  {{
                    item.observation?.listeningPorts?.join("、") ||
                    "未获取监听端口"
                  }}
                </small>
              </div>
              <span class="state-tag" :class="softwareState(item).tone">
                {{ softwareState(item).label }}
              </span>
            </button>
          </div>
        </section>
      </template>
    </HostResourceDetails>
    <details class="overview-controller">
      <summary>控制器信息</summary>
      <dl class="system-facts">
        <dt>服务版本</dt>
        <dd>{{ controller?.release }}</dd>
        <dt>监听地址</dt>
        <dd>{{ controller?.listen }}</dd>
        <dt>启动时间</dt>
        <dd>{{ hostTime(controller?.startedAt) }}</dd>
        <dt>状态目录</dt>
        <dd>
          <code>{{ controller?.stateDirectory }}</code>
        </dd>
      </dl>
    </details>
  </section>
</template>
<style scoped>
.host-overview {
  display: grid;
  gap: 20px;
}
.overview-summary {
  display: grid;
  grid-template-columns: repeat(4, minmax(0, 1fr));
  gap: 18px;
}
.overview-summary article,
.overview-panel {
  background: var(--surface);
  border: 1px solid var(--border);
  border-radius: 8px;
  box-shadow: var(--card-shadow);
}
.overview-summary article {
  display: grid;
  align-content: start;
  gap: 13px;
  padding: 22px;
}
.overview-summary article > span {
  font-size: 13px;
  color: var(--muted);
}
.overview-summary strong {
  font-size: 28px;
  font-weight: 600;
  letter-spacing: -0.7px;
}
.overview-summary i,
.kpi-sub-total {
  font-size: 16px;
  font-style: normal;
  color: var(--muted);
  font-weight: 400;
}
.kpi-ok { color: var(--success) !important; }
.kpi-warn { color: var(--warning) !important; }
.kpi-bad { color: var(--error) !important; }
.attention-panel {
  border-color: #fde68a;
  background: #fffdf5;
}
.attention-list {
  display: grid;
  gap: 8px;
}
.attention-row {
  display: flex;
  align-items: center;
  gap: 12px;
  padding: 10px 12px;
  background: #ffffff;
  border: 1px solid #fef3c7;
  border-radius: 6px;
}
.attention-copy {
  display: grid;
  gap: 2px;
  min-width: 0;
  flex: 1;
}
.attention-copy strong {
  font-size: 13px;
  font-weight: 500;
}
.overview-compare-table {
  min-width: 780px;
}
.clickable-row {
  cursor: pointer;
}
.selected-row td {
  background: #eff6ff !important;
}
.server-cell {
  display: flex;
  align-items: center;
  gap: 10px;
}
.server-cell strong,
.status-cell small {
  display: block;
}
.server-cell small,
.status-cell small {
  margin-top: 3px;
  font-size: 11px;
  color: var(--muted);
}
.metric-bar-cell {
  display: grid;
  gap: 6px;
  min-width: 132px;
}
.metric-bar-head {
  display: flex;
  align-items: baseline;
  justify-content: space-between;
  gap: 6px;
  font-size: 12px;
  font-variant-numeric: tabular-nums;
}
.metric-bar-head small {
  font-size: 11px;
}
.row-quick-actions {
  display: flex;
  align-items: center;
  gap: 2px;
}
.clickable-soft {
  width: 100%;
  background: transparent;
  border-left: 0;
  border-right: 0;
  border-bottom: 0;
  text-align: left;
  padding-inline: 6px;
  border-radius: 4px;
}
.clickable-soft:hover {
  background: #f8fafc;
}
.overview-summary small {
  font-size: 11px;
  color: var(--muted);
}
.overview-summary .controller-state {
  font-size: 21px;
  letter-spacing: 0;
}
.overview-panel {
  padding: 22px;
  min-width: 0;
}
.overview-panel-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 14px;
  flex-wrap: wrap;
  margin-bottom: 22px;
}
.overview-panel-head h2 {
  font-size: 15px;
  font-weight: 600;
  margin: 0;
}
.overview-panel-head small,
.overview-panel-head > .muted {
  font-size: 11px;
  color: var(--muted);
}
.host-switcher {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(180px, 1fr));
  gap: 12px;
}
.host-switcher button {
  display: flex;
  align-items: center;
  gap: 12px;
  background: #fafbfd;
  border: 1px solid var(--border);
  border-radius: 6px;
  padding: 14px;
  text-align: left;
  color: var(--text);
  transition:
    border-color 0.15s ease,
    background-color 0.15s ease;
}
.host-switcher button:hover {
  border-color: #cbd5e1;
  background: #f8fafc;
}
.host-switcher button.selected {
  border-color: var(--accent);
  background: #f4f8fe;
}
.host-switcher button > span:nth-child(2) {
  display: grid;
  gap: 7px;
  min-width: 0;
  flex: 1;
}
.host-switcher strong {
  font-size: 13px;
  font-weight: 500;
  overflow-wrap: anywhere;
}
.host-switcher small {
  font: 11px var(--mono);
  color: var(--muted);
  overflow-wrap: anywhere;
}
.host-dot {
  width: 7px;
  height: 7px;
  border-radius: 50%;
  background: #a5adba;
  flex-shrink: 0;
}
.host-dot.available {
  background: var(--success);
}
.system-facts {
  display: grid;
  grid-template-columns: 78px minmax(0, 1fr);
  gap: 18px;
  margin: 0;
  font-size: 12px;
  line-height: 1.6;
}
.system-facts dt {
  color: var(--muted);
}
.system-facts dd {
  margin: 0;
  overflow-wrap: anywhere;
}
.system-facts code {
  font: 11px var(--mono);
}
.overview-software-list {
  max-height: 280px;
  overflow-y: auto;
  padding-right: 2px;
}
.overview-software {
  display: flex;
  align-items: center;
  gap: 13px;
  padding: 14px 0;
  border-top: 1px solid var(--border);
}
.overview-software:first-child {
  border-top: 0;
  padding-top: 4px;
}
.overview-software :deep(.software-icon) {
  width: 34px;
  height: 34px;
  font-size: 14px;
  border-radius: 7px;
}
.overview-software :deep(svg) {
  width: 27px;
  height: 27px;
}
.overview-software > div {
  display: grid;
  gap: 7px;
  min-width: 0;
  flex: 1;
}
.overview-software strong {
  font-size: 12px;
  font-weight: 500;
  overflow-wrap: anywhere;
}
.overview-software small {
  font: 11px/1.5 var(--mono);
  color: var(--muted);
  overflow-wrap: anywhere;
}
.overview-controller {
  font-size: 12px;
  color: var(--muted);
  background: var(--surface);
  border: 1px solid var(--border);
  border-radius: 8px;
  padding: 14px 18px;
  box-shadow: var(--card-shadow);
}
.overview-controller summary {
  cursor: pointer;
  font-weight: 500;
  color: var(--text-secondary);
}
.overview-controller dl {
  margin: 16px 0 4px;
  padding-top: 14px;
  border-top: 1px solid var(--border-subtle);
}
@media (max-width: 1200px) {
  .overview-summary {
    grid-template-columns: repeat(2, minmax(0, 1fr));
  }
}
@media (max-width: 650px) {
  .overview-panel {
    padding: 18px 14px;
  }
  .overview-summary {
    gap: 12px;
  }
  .overview-summary article {
    padding: 17px 14px;
  }
  .overview-summary strong {
    font-size: 25px;
  }
  .overview-summary .controller-state {
    font-size: 18px;
  }
  .overview-panel-head {
    gap: 10px;
  }
}
</style>
