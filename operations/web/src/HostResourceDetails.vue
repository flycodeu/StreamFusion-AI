<script setup lang="ts">
import { computed, ref, watch } from "vue";
import { hostTime, type Host } from "./hosts";
import {
  appendCpuSample,
  metricBytes as bytes,
  metricDuration as duration,
  metricNotice,
  metricPercent,
  metricsAvailable,
  metricsLive,
  type CpuSample,
  type HostMetricSnapshot,
} from "./hostMetrics";

const props = defineProps<{
  host: Host;
  metrics?: HostMetricSnapshot;
  loading?: boolean;
  detecting?: boolean;
  monitoring?: boolean;
  readError?: string;
}>();
const emit = defineEmits<{ detect: []; monitoring: [] }>();
const current = computed(() =>
  props.metrics?.hostId === props.host.id ? props.metrics : undefined,
);
const live = computed(() => !props.readError && metricsLive(current.value));
const available = computed(() => metricsAvailable(current.value));
const notice = computed(() => props.readError || metricNotice(current.value));
const memoryUsed = computed(() =>
  current.value?.memoryTotalBytes !== undefined &&
  current.value.memoryAvailableBytes !== undefined
    ? current.value.memoryTotalBytes - current.value.memoryAvailableBytes
    : undefined,
);
const swapUsed = computed(() =>
  current.value?.swapTotalBytes !== undefined &&
  current.value.swapFreeBytes !== undefined
    ? current.value.swapTotalBytes - current.value.swapFreeBytes
    : undefined,
);
const gauges = computed(() => [
  {
    label: "CPU",
    value: current.value?.cpuPercent,
    detail: current.value?.cpuCores
      ? `${current.value.cpuCores} 逻辑核心`
      : "—",
  },
  {
    label: "内存",
    value: metricPercent(memoryUsed.value, current.value?.memoryTotalBytes),
    detail: `${bytes(memoryUsed.value)} / ${bytes(current.value?.memoryTotalBytes)}`,
  },
  {
    label: "根分区 /",
    value: metricPercent(
      current.value?.rootUsedBytes,
      current.value?.rootTotalBytes,
    ),
    detail: `${bytes(current.value?.rootUsedBytes)} / ${bytes(current.value?.rootTotalBytes)}`,
  },
]);
const samples = ref<CpuSample[]>([]);
let sampleHost = "";
watch(
  () => [props.host.id, current.value?.observedAt],
  () => {
    if (sampleHost !== props.host.id) {
      samples.value = [];
      sampleHost = props.host.id;
    }
    samples.value = appendCpuSample(samples.value, current.value);
  },
  { immediate: true },
);
const cpuStatistics = computed(() => {
  const values = samples.value.map((sample) => sample.value);
  return values.length
    ? {
        average: values.reduce((a, b) => a + b, 0) / values.length,
        min: Math.min(...values),
        max: Math.max(...values),
      }
    : undefined;
});
const cpuPoints = computed(() => {
  const list = samples.value;
  if (!list.length) return [];
  const first = Date.parse(list[0]!.at),
    span = Date.parse(list[list.length - 1]!.at) - first;
  return list.map((sample) => ({
    x: span ? 8 + ((Date.parse(sample.at) - first) / span) * 584 : 300,
    y: 98 - sample.value * 0.9,
  }));
});
const cpuLine = computed(() =>
  cpuPoints.value.map(({ x, y }) => `${x},${y}`).join(" "),
);
const percentage = (value?: number) =>
  value === undefined ? "—" : `${value.toFixed(1)}%`;
</script>

<template>
  <section class="host-resources" aria-label="主机系统信息">
    <header class="resource-toolbar">
      <div class="snapshot-time">
        <span
          class="state-tag"
          :class="live ? 'state-success' : 'state-muted'"
          >{{
            live ? "监测中" : current?.enabled === false ? "已暂停" : "待更新"
          }}</span
        >
        <time v-if="current?.observedAt">{{
          hostTime(current.observedAt)
        }}</time>
      </div>
      <div class="resource-actions">
        <button
          class="quiet"
          :disabled="monitoring || loading || !current"
          @click="emit('monitoring')"
        >
          {{ current?.enabled === false ? "恢复监测" : "暂停监测" }}
        </button>
        <button class="secondary" :disabled="detecting" @click="emit('detect')">
          {{ detecting ? "提交中…" : "立即检测" }}
        </button>
      </div>
    </header>
    <div v-if="notice" class="observation-notice" role="status">
      {{ loading && !current ? "正在读取主机信息…" : notice }}
    </div>
    <div class="resource-columns">
      <section class="resource-card">
        <header>
          <h2>{{ live ? "资源使用" : "上次资源" }}</h2>
        </header>
        <div class="resource-gauges">
          <article v-for="gauge in gauges" :key="gauge.label">
            <div
              class="resource-gauge"
              :class="{
                high: (gauge.value ?? 0) >= 80,
                critical: (gauge.value ?? 0) >= 90,
              }"
              :style="{ '--usage': `${available ? (gauge.value ?? 0) : 0}%` }"
              :aria-label="`${gauge.label} ${available ? percentage(gauge.value) : '未获取'}`"
            >
              <div>
                <strong
                  >{{
                    available && gauge.value !== undefined
                      ? gauge.value.toFixed(1)
                      : "—"
                  }}<small v-if="available && gauge.value !== undefined"
                    >%</small
                  ></strong
                ><span>{{ gauge.label }}</span>
              </div>
            </div>
            <small>{{ gauge.detail }}</small>
          </article>
        </div>
        <dl class="memory-facts">
          <div>
            <dt>内存可用</dt>
            <dd>{{ bytes(current?.memoryAvailableBytes) }}</dd>
          </div>
          <div>
            <dt>交换空间</dt>
            <dd>
              {{ bytes(swapUsed) }} / {{ bytes(current?.swapTotalBytes) }}
            </dd>
          </div>
        </dl>
        <div class="cpu-trend">
          <header>
            <h3>CPU 最近采样</h3>
            <span
              >平均 {{ percentage(cpuStatistics?.average) }} · 最低
              {{ percentage(cpuStatistics?.min) }} · 最高
              {{ percentage(cpuStatistics?.max) }}</span
            >
          </header>
          <svg
            v-if="cpuPoints.length"
            viewBox="0 0 600 108"
            role="img"
            aria-label="CPU 最近采样趋势"
            preserveAspectRatio="none"
          >
            <path d="M8 98 H592 M8 53 H592 M8 8 H592" class="chart-grid" />
            <polyline :points="cpuLine" class="chart-line" />
            <circle
              v-if="cpuPoints.length === 1"
              :cx="cpuPoints[0]!.x"
              :cy="cpuPoints[0]!.y"
              r="3"
              class="chart-point"
            />
          </svg>
          <div v-else class="chart-empty">等待 CPU 采样</div>
          <footer v-if="samples.length">
            <time>{{ hostTime(samples[0]!.at) }}</time
            ><time v-if="samples.length > 1">{{
              hostTime(samples[samples.length - 1]!.at)
            }}</time>
          </footer>
        </div>
        <div class="load-summary">
          <span
            >系统负载
            <b>{{
              current?.load?.map((value) => value.toFixed(2)).join(" / ") || "—"
            }}</b></span
          ><small>1 / 5 / 15 分钟</small>
        </div>
      </section>
      <section class="resource-card">
        <header><h2>系统信息</h2></header>
        <dl class="system-facts">
          <dt>主机名称</dt>
          <dd>{{ current?.hostname || "—" }}</dd>
          <dt>连接地址</dt>
          <dd>{{ host.kind === "LOCAL" ? "控制器本机" : host.address }}</dd>
          <dt>系统版本</dt>
          <dd>{{ current?.os || "—" }}</dd>
          <dt>内核版本</dt>
          <dd>{{ current?.kernel || "—" }}</dd>
          <dt>系统架构</dt>
          <dd>{{ current?.arch || "—" }}</dd>
          <dt>处理器</dt>
          <dd>{{ current?.cpuModel || "—" }}</dd>
          <dt>运行时间</dt>
          <dd>{{ duration(current?.uptimeSeconds) }}</dd>
          <dt>启动时间</dt>
          <dd>{{ hostTime(current?.bootTime) }}</dd>
        </dl>
      </section>
    </div>
    <div class="resource-bottom" :class="{ 'has-sidebar': !!$slots.software }">
      <section class="resource-card">
        <header>
          <h2>网络</h2>
          <span v-if="current?.primaryInterface"
            >主网卡 {{ current.primaryInterface }}</span
          >
        </header>
        <div v-if="!current?.network?.length" class="empty">尚无网络采样</div>
        <div v-else class="table-scroll bounded-table-scroll">
          <table class="network-table">
            <thead>
              <tr>
                <th>网卡</th>
                <th>地址</th>
                <th>上传</th>
                <th>下载</th>
                <th>累计发送</th>
                <th>累计接收</th>
              </tr>
            </thead>
            <tbody>
              <tr v-for="nic in current.network" :key="nic.name">
                <td class="mono">{{ nic.name }}</td>
                <td class="addresses">
                  <span v-for="address in nic.addresses || []" :key="address">{{
                    address
                  }}</span
                  ><span v-if="!nic.addresses?.length">—</span>
                </td>
                <td>
                  {{
                    nic.sendBytesPerSecond === undefined
                      ? "—"
                      : bytes(nic.sendBytesPerSecond) + "/s"
                  }}
                </td>
                <td>
                  {{
                    nic.receiveBytesPerSecond === undefined
                      ? "—"
                      : bytes(nic.receiveBytesPerSecond) + "/s"
                  }}
                </td>
                <td>{{ bytes(nic.sentBytes) }}</td>
                <td>{{ bytes(nic.receivedBytes) }}</td>
              </tr>
            </tbody>
          </table>
        </div>
      </section>
      <slot name="software" />
    </div>
    <section class="resource-card">
      <header><h2>进程</h2></header>
      <div v-if="!current?.processes?.length" class="empty">
        尚无可读取的进程信息
      </div>
      <div v-else class="table-scroll bounded-table-scroll">
        <table class="process-table">
          <thead>
            <tr>
              <th>PID</th>
              <th>用户</th>
              <th
                :title="
                  current.processCpuBasis === 'LIFETIME_AVERAGE'
                    ? '进程启动以来的平均 CPU，多核进程可超过 100%'
                    : '采样期间的 CPU 使用率，多核进程可超过 100%'
                "
              >
                {{
                  current.processCpuBasis === "LIFETIME_AVERAGE"
                    ? "平均 CPU"
                    : "CPU"
                }}
              </th>
              <th>内存</th>
              <th>进程名称</th>
            </tr>
          </thead>
          <tbody>
            <tr v-for="process in current.processes" :key="process.pid">
              <td class="mono">{{ process.pid }}</td>
              <td>{{ process.user }}</td>
              <td>{{ percentage(process.cpuPercent) }}</td>
              <td>{{ bytes(process.memoryBytes) }}</td>
              <td class="process-name mono">{{ process.name }}</td>
            </tr>
          </tbody>
        </table>
      </div>
    </section>
  </section>
</template>

<style scoped>
.host-resources {
  display: grid;
  gap: 20px;
  min-width: 0;
}
.resource-toolbar,
.snapshot-time,
.resource-actions,
.resource-card > header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  flex-wrap: wrap;
}
.snapshot-time time,
.resource-card > header > span {
  color: var(--muted);
  font-size: 11px;
}
.resource-columns {
  display: grid;
  grid-template-columns: minmax(0, 1.35fr) minmax(0, 1fr);
  gap: 20px;
}
.resource-bottom {
  display: grid;
  gap: 20px;
  min-width: 0;
}
.resource-bottom.has-sidebar {
  grid-template-columns: minmax(0, 1.35fr) minmax(0, 1fr);
}
.resource-card {
  min-width: 0;
  padding: 22px;
  background: var(--surface);
  border: 1px solid var(--border);
  border-radius: 8px;
  box-shadow: var(--card-shadow);
}
.resource-card > header {
  margin-bottom: 22px;
}
h2,
h3 {
  margin: 0;
  font-size: 15px;
  font-weight: 600;
}
.resource-gauges {
  display: grid;
  grid-template-columns: repeat(3, minmax(0, 1fr));
  gap: 16px;
  padding: 8px 0 20px;
}
.resource-gauges article {
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: 12px;
  min-width: 0;
}
.resource-gauge {
  width: 130px;
  max-width: 100%;
  aspect-ratio: 1;
  border-radius: 50%;
  background: conic-gradient(var(--accent) var(--usage), #edf2f8 0);
  padding: 5px;
}
.resource-gauge.high {
  background: conic-gradient(var(--warning) var(--usage), #edf2f8 0);
}
.resource-gauge.critical {
  background: conic-gradient(var(--danger) var(--usage), #edf2f8 0);
}
.resource-gauge > div {
  height: 100%;
  border: 7px solid var(--surface);
  border-radius: 50%;
  background: #fafbfd;
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  gap: 9px;
}
.resource-gauge strong {
  font-size: 25px;
  font-weight: 500;
}
.resource-gauge strong small {
  font-size: 12px;
  margin-left: 3px;
}
.resource-gauge span,
.resource-gauges article > small {
  font-size: 11px;
  color: var(--muted);
  text-align: center;
  line-height: 1.6;
}
.memory-facts {
  display: flex;
  justify-content: space-between;
  flex-wrap: wrap;
  gap: 12px;
  margin: 0 0 22px;
  font-size: 11px;
}
.memory-facts div {
  display: flex;
  gap: 8px;
}
dt {
  color: var(--muted);
}
dd {
  margin: 0;
  overflow-wrap: anywhere;
}
.cpu-trend {
  border-top: 1px solid var(--border);
  padding-top: 16px;
}
.cpu-trend header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  flex-wrap: wrap;
  gap: 8px;
}
.cpu-trend h3 {
  font-size: 12px;
}
.cpu-trend header > span,
.cpu-trend footer {
  color: var(--muted);
  font-size: 10px;
}
.cpu-trend svg {
  display: block;
  width: 100%;
  height: 108px;
  margin-top: 10px;
  overflow: visible;
}
.chart-grid {
  stroke: var(--border);
  stroke-width: 1;
  fill: none;
  vector-effect: non-scaling-stroke;
}
.chart-line {
  stroke: var(--accent);
  stroke-width: 2;
  stroke-linecap: round;
  stroke-linejoin: round;
  fill: none;
  vector-effect: non-scaling-stroke;
}
.chart-point {
  fill: var(--accent);
}
.chart-empty {
  display: grid;
  place-items: center;
  height: 108px;
  color: var(--muted);
  font-size: 12px;
}
.cpu-trend footer {
  display: flex;
  justify-content: space-between;
  flex-wrap: wrap;
  gap: 4px;
}
.load-summary {
  display: flex;
  justify-content: space-between;
  flex-wrap: wrap;
  gap: 8px;
  border-top: 1px solid var(--border);
  margin-top: 16px;
  padding-top: 16px;
  font-size: 11px;
  color: var(--muted);
}
.load-summary b {
  color: var(--text);
  margin-left: 8px;
  font: 11px var(--mono);
}
.system-facts {
  display: grid;
  grid-template-columns: 72px minmax(0, 1fr);
  gap: 17px 12px;
  font-size: 12px;
  line-height: 1.6;
  margin: 0;
}
.observation-notice {
  background: #fff9ed;
  color: #946619;
  border: 1px solid #f0dfbd;
  border-radius: 6px;
  padding: 12px 16px;
  font-size: 12px;
  line-height: 1.7;
}
.table-scroll {
  overflow: auto;
  max-width: 100%;
  min-width: 0;
}
table {
  font-size: 12px;
  width: 100%;
  border-collapse: collapse;
}
th {
  font-size: 11px;
  color: var(--muted);
  font-weight: 500;
  background: #fafbfd;
  text-align: left;
}
td,
th {
  padding: 12px 10px;
  border-bottom: 1px solid var(--border);
  vertical-align: top;
  white-space: nowrap;
}
tr:last-child td {
  border-bottom: 0;
}
.network-table {
  min-width: 590px;
}
.addresses span {
  display: block;
  font: 11px/1.7 var(--mono);
}
.process-table {
  min-width: 430px;
}
.bounded-table-scroll {
  max-height: 340px;
}
.bounded-table-scroll thead {
  position: sticky;
  top: 0;
  z-index: 1;
}
.process-name {
  white-space: normal;
  overflow-wrap: anywhere;
  max-width: 280px;
}
.mono {
  font-family: var(--mono);
}
@media (max-width: 1200px) {
  .resource-columns,
  .resource-bottom.has-sidebar {
    grid-template-columns: 1fr;
  }
  .resource-gauges {
    max-width: 620px;
    margin: auto;
  }
}
@media (max-width: 650px) {
  .host-resources {
    gap: 14px;
  }
  .resource-card {
    padding: 18px 14px;
  }
  .resource-card > header {
    margin-bottom: 16px;
  }
  .resource-gauge {
    width: 96px;
  }
  .resource-gauge > div {
    border-width: 4px;
  }
  .resource-gauge strong {
    font-size: 20px;
  }
  .resource-gauges {
    gap: 8px;
  }
  .resource-gauges article > small {
    font-size: 10px;
  }
  .resource-toolbar {
    gap: 10px;
  }
  .resource-actions {
    margin-left: auto;
    gap: 10px;
  }
  .snapshot-time {
    gap: 8px;
  }
  .system-facts {
    gap: 13px 10px;
  }
}
</style>
