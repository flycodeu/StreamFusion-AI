<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref, watch } from "vue";
import { request } from "./api";
import { ElMessage } from "element-plus";
import type { ObservationAttempt } from "./observation";
import { hostTime, type Host } from "./hosts";
import { softwareState, type SoftwareInstance } from "./software";
import SoftwareIcon from "./SoftwareIcon.vue";

interface Metrics {
  latestAttempt?: ObservationAttempt;
  freshness?: string;
  enabled?: boolean;
  code: string;
  observedAt: string;
  hostname?: string;
  os?: string;
  kernel?: string;
  cpuModel?: string;
  cpuCores?: number;
  cpuPercent?: number;
  uptimeSeconds?: number;
  memoryTotalBytes?: number;
  memoryAvailableBytes?: number;
  rootTotalBytes?: number;
  rootUsedBytes?: number;
  load?: number[];
  network?: {
    name: string;
    kind: string;
    receivedBytes: number;
    sentBytes: number;
    receiveBytesPerSecond?: number;
    sendBytesPerSecond?: number;
  }[];
}
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
const emit = defineEmits<{ error: [unknown] }>();
const hosts = ref<Host[]>([]),
  software = ref<SoftwareInstance[]>([]),
  metrics = ref<Record<string, Metrics>>({});
const selectedId = ref(""),
  loading = ref(false),
  now = ref(Date.now());
const selected = computed(() =>
  hosts.value.find((h) => h.id === selectedId.value),
);
const current = computed(() => metrics.value[selectedId.value]);
const softwareOnHost = computed(() =>
  software.value.filter(
    (s) => s.hostId === selectedId.value && s.managementMode !== "RELEASED",
  ),
);
const available = (m?: Metrics) => !!m && ["READY", "PARTIAL"].includes(m.code);
const live = (m?: Metrics) =>
  available(m) &&
  m?.freshness === "FRESH" &&
  now.value - Date.parse(m.observedAt) < 90000 &&
  ["READY", "PARTIAL"].includes(m.latestAttempt?.code || "") &&
  m.enabled !== false;
const observedCount = computed(
  () => hosts.value.filter((h) => live(metrics.value[h.id])).length,
);
const percent = (used?: number, total?: number) =>
  used !== undefined && total && total > 0
    ? Math.max(0, Math.min(100, (used / total) * 100))
    : undefined;
const gauges = computed(() => [
  {
    label: "CPU",
    value: current.value?.cpuPercent,
    detail: current.value?.cpuCores
      ? `${current.value.cpuCores} 逻辑核心`
      : "核心数未获取",
  },
  {
    label: "内存",
    value: percent(
      current.value?.memoryTotalBytes !== undefined &&
        current.value.memoryAvailableBytes !== undefined
        ? current.value.memoryTotalBytes - current.value.memoryAvailableBytes
        : undefined,
      current.value?.memoryTotalBytes,
    ),
    detail: `${bytes(current.value?.memoryTotalBytes !== undefined && current.value.memoryAvailableBytes !== undefined ? current.value.memoryTotalBytes - current.value.memoryAvailableBytes : undefined)} / ${bytes(current.value?.memoryTotalBytes)}`,
  },
  {
    label: "根分区 /",
    value: percent(current.value?.rootUsedBytes, current.value?.rootTotalBytes),
    detail: `${bytes(current.value?.rootUsedBytes)} / ${bytes(current.value?.rootTotalBytes)}`,
  },
]);
const interfaces = computed(() => current.value?.network || []);
const interfaceName = ref("");
watch(selectedId, () => {
  interfaceName.value = "";
});
const visibleInterface = computed(() => {
  const nic =
    interfaces.value.find((item) => item.name === interfaceName.value) ||
    interfaces.value.find(
      (item) =>
        item.kind === "PHYSICAL" && item.receivedBytes + item.sentBytes > 0,
    ) ||
    interfaces.value[0];
  return nic ? [nic] : [];
});
const codeLabels: Record<string, string> = {
  PLATFORM_UNSUPPORTED: "当前仅支持 Linux 资源采集",
  PROBE_FAILED: "只读采集失败，请检查主机命令权限",
  RESPONSE_INVALID: "采集结果格式异常，请检查 Linux 环境",
  SSH_TIMEOUT: "SSH 超时，请检查网络与端口",
  SSH_UNAVAILABLE: "SSH 无法连接，请检查主机连接",
  SSH_AUTH_FAILED: "SSH 认证失败，请检查凭据",
  SSH_HOST_KEY_CHANGED: "主机指纹已变化，请重新核对",
  CREDENTIAL_UNAVAILABLE: "连接凭据不可用，请检查凭据",
  HOST_METRICS_BUSY: "主机正在采集，请稍后刷新",
};
function bytes(n?: number): string {
  if (n === undefined || !Number.isFinite(n)) return "—";
  const units = ["B", "KB", "MB", "GB", "TB"];
  let i = 0;
  while (n >= 1024 && i < units.length - 1) {
    n /= 1024;
    i++;
  }
  return `${n.toFixed(i ? 1 : 0)} ${units[i]}`;
}
function duration(n?: number) {
  return n === undefined
    ? "—"
    : n >= 86400
      ? `${Math.floor(n / 86400)} 天 ${Math.floor((n % 86400) / 3600)} 小时`
      : `${Math.floor(n / 3600)} 小时 ${Math.floor((n % 3600) / 60)} 分钟`;
}
let disposed = false,
  timer: ReturnType<typeof setTimeout> | undefined;
async function refresh() {
  if (loading.value) return;
  loading.value = true;
  try {
    const [hs, ss] = await Promise.all([
      request<Host[]>("/hosts"),
      request<SoftwareInstance[]>("/software-instances"),
    ]);
    if (disposed) return;
    hosts.value = hs;
    software.value = ss;
    if (!hs.some((h) => h.id === selectedId.value))
      selectedId.value = hs[0]?.id || "";
    const snapshots = await request<
      {
        hostId: string;
        lastConfirmed?: Metrics;
        latestAttempt: ObservationAttempt;
        freshness: string;
        enabled: boolean;
      }[]
    >("/host-observations");
    if (disposed) return;
    metrics.value = Object.fromEntries(
      snapshots.map((o) => [
        o.hostId,
        {
          ...(o.lastConfirmed || {
            code: o.latestAttempt.code || "NOT_CHECKED",
            observedAt: "",
          }),
          latestAttempt: o.latestAttempt,
          freshness: o.freshness,
          enabled: o.enabled,
        },
      ]),
    );
    now.value = Date.now();
  } catch (error) {
    if (!disposed) emit("error", error);
  } finally {
    loading.value = false;
  }
}
const detecting = ref(false);
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
  if (!selected.value) return;
  try {
    await request("/hosts/" + selected.value.id + "/monitoring", "PATCH", {
      enabled: current.value?.enabled === false,
      expectedVersion: selected.value.version,
    });
    await refresh();
  } catch (error) {
    emit("error", error);
  }
}
function poll() {
  timer = setTimeout(async () => {
    now.value = Date.now();
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
  if (timer) clearTimeout(timer);
});
</script>
<template>
  <section class="content host-overview" aria-label="主机概览">
    <div class="overview-summary">
      <article>
        <span>接入主机</span>
        <strong>{{ hosts.length }}</strong>
      </article>
      <article>
        <span>资源采集</span>
        <strong>
          {{ observedCount }}
          <i>/ {{ hosts.length }}</i>
        </strong>
        <small v-if="loading">正在检查…</small>
      </article>
      <article>
        <span>软件实例</span>
        <strong>
          {{ software.filter((s) => s.managementMode !== "RELEASED").length }}
        </strong>
      </article>
      <article>
        <span>Ops 控制器</span>
        <strong class="controller-state">
          {{ connected ? "运行中" : "连接中断" }}
        </strong>
        <small>{{ uptime }} · {{ bytes(controller?.heapBytes) }} 堆内存</small>
      </article>
    </div>
    <section class="overview-panel">
      <header class="overview-panel-head">
        <h2>服务器状态</h2>
        <span class="spacer"></span>
        <button
          class="secondary"
          :disabled="detecting || !selectedId"
          @click="detect"
        >
          {{ detecting ? "提交中…" : "立即检测" }}
        </button>
      </header>
      <div v-if="!hosts.length" class="empty">
        尚未接入主机，请在主机管理添加服务器
      </div>
      <div v-else class="host-switcher" role="group" aria-label="选择监测主机">
        <button
          v-for="host in hosts"
          :key="host.id"
          :class="{ selected: host.id === selectedId }"
          @click="selectedId = host.id"
        >
          <span
            class="host-dot"
            :class="{ available: live(metrics[host.id]) }"
          ></span>
          <span>
            <strong>{{ host.name }}</strong>
            <small>
              {{ host.kind === "LOCAL" ? "控制器本机" : host.address }}
            </small>
          </span>
          <span v-if="host.maintenance" class="muted">维护</span>
        </button>
      </div>
    </section>
    <template v-if="selected">
      <div
        v-if="current && !live(current)"
        class="observation-notice"
        role="status"
      >
        {{
          current.enabled === false
            ? "监测已暂停"
            : current.latestAttempt?.code &&
                !["READY", "PARTIAL", "NOT_CHECKED"].includes(
                  current.latestAttempt.code,
                )
              ? codeLabels[current.latestAttempt.code] ||
                "当前无法确认，请检查连接与读取权限"
              : available(current)
                ? "上次结果 · 等待后台更新"
                : "尚未采集 · 等待后台检测"
        }}
        <small v-if="current.observedAt"
          >上次确认 {{ hostTime(current.observedAt) }}</small
        >
        <small v-if="current.latestAttempt?.at"
          >最近尝试 {{ hostTime(current.latestAttempt.at) }} ·
          {{ current.latestAttempt.code }}</small
        >
      </div>
      <div class="overview-columns">
        <section class="overview-panel resource-panel">
          <header class="overview-panel-head">
            <h2>{{ live(current) ? "资源使用" : "上次资源" }}</h2>
            <button class="quiet" @click="toggleMonitoring">
              {{ current?.enabled === false ? "恢复监测" : "暂停监测" }}
            </button>
            <small>
              {{ current ? hostTime(current.observedAt) : "等待采集" }}
            </small>
          </header>
          <div class="resource-gauges">
            <article v-for="gauge in gauges" :key="gauge.label">
              <div
                class="resource-gauge"
                :class="{
                  'usage-critical':
                    available(current) && (gauge.value ?? 0) >= 90,
                  'usage-high':
                    available(current) &&
                    (gauge.value ?? 0) >= 80 &&
                    (gauge.value ?? 0) < 90,
                }"
                :style="{
                  '--usage': `${available(current) ? (gauge.value ?? 0) : 0}%`,
                }"
                :aria-label="`${gauge.label} ${available(current) && gauge.value !== undefined ? gauge.value.toFixed(1) + '%' : '未获取'}`"
              >
                <div>
                  <strong>
                    {{
                      available(current) && gauge.value !== undefined
                        ? gauge.value.toFixed(1)
                        : "—"
                    }}
                    <small
                      v-if="available(current) && gauge.value !== undefined"
                    >
                      %
                    </small>
                  </strong>
                  <span>{{ gauge.label }}</span>
                </div>
              </div>
              <small>{{ gauge.detail }}</small>
            </article>
          </div>
          <div class="load-summary">
            <span>
              系统负载
              <b>
                {{
                  available(current) && current?.load?.length
                    ? current.load.map((n) => n.toFixed(2)).join(" / ")
                    : "—"
                }}
              </b>
            </span>
            <small>1 / 5 / 15 分钟</small>
          </div>
        </section>
        <section class="overview-panel">
          <header class="overview-panel-head">
            <h2>系统信息</h2>
            <span class="state-tag state-muted">
              {{ selected.maintenance ? "维护中" : "已登记" }}
            </span>
          </header>
          <dl class="system-facts">
            <dt>主机名称</dt>
            <dd>{{ current?.hostname || selected.name }}</dd>
            <dt>系统版本</dt>
            <dd>{{ current?.os || "未获取" }}</dd>
            <dt>内核版本</dt>
            <dd>{{ current?.kernel || "未获取" }}</dd>
            <dt>处理器</dt>
            <dd>{{ current?.cpuModel || "未获取" }}</dd>
            <dt>运行时间</dt>
            <dd>{{ duration(current?.uptimeSeconds) }}</dd>
            <dt>项目目录</dt>
            <dd>
              <code>{{ selected.installRoot }}</code>
            </dd>
          </dl>
        </section>
      </div>
      <div class="overview-columns">
        <section class="overview-panel">
          <header class="overview-panel-head">
            <h2>网络流量</h2>
            <select
              v-if="interfaces.length"
              v-model="interfaceName"
              aria-label="监测网卡"
            >
              <option value="">自动选择</option>
              <option
                v-for="nic in interfaces"
                :key="nic.name"
                :value="nic.name"
              >
                {{ nic.name }}{{ nic.kind === "PHYSICAL" ? " · 物理网卡" : "" }}
              </option>
            </select>
          </header>
          <div v-if="!interfaces.length" class="empty">
            {{ loading ? "正在采集流量" : "尚无网络采样" }}
          </div>
          <div
            v-for="nic in visibleInterface"
            :key="nic.name"
            class="network-interface"
          >
            <strong>{{ nic.name }}</strong>
            <div>
              <span>
                接收
                <b>
                  {{
                    available(current) ? bytes(nic.receiveBytesPerSecond) : "—"
                  }}/s
                </b>
              </span>
              <span>
                发送
                <b>
                  {{
                    available(current) ? bytes(nic.sendBytesPerSecond) : "—"
                  }}/s
                </b>
              </span>
              <small>
                累计接收 {{ bytes(nic.receivedBytes) }} · 发送
                {{ bytes(nic.sentBytes) }}
              </small>
            </div>
          </div>
        </section>
        <section class="overview-panel">
          <header class="overview-panel-head">
            <h2>软件与端口</h2>
            <small>{{ softwareOnHost.length }} 个实例</small>
          </header>
          <div v-if="!softwareOnHost.length" class="empty">
            此主机尚未登记软件
          </div>
          <div v-else class="overview-software-list">
            <div
              v-for="item in softwareOnHost"
              :key="item.id"
              class="overview-software"
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
            </div>
          </div>
        </section>
      </div>
    </template>
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
.overview-panel-head select {
  width: auto;
  max-width: 210px;
  min-width: 0;
  margin: 0;
  height: 34px;
  font-size: 12px;
}
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
.overview-summary i {
  font-size: 16px;
  font-style: normal;
  color: var(--muted);
  font-weight: 400;
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
.overview-columns {
  display: grid;
  grid-template-columns: minmax(0, 1.35fr) minmax(0, 1fr);
  gap: 20px;
  align-items: stretch;
}
.resource-panel {
  display: flex;
  flex-direction: column;
}
.resource-gauges {
  display: grid;
  grid-template-columns: repeat(3, minmax(0, 1fr));
  gap: 16px;
  padding: 12px 0 26px;
}
.resource-gauges article {
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: 17px;
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
.resource-gauge.usage-high {
  background: conic-gradient(var(--warning) var(--usage), #edf2f8 0);
}
.resource-gauge.usage-critical {
  background: conic-gradient(var(--danger) var(--usage), #edf2f8 0);
}
.resource-gauge > div {
  height: 100%;
  border: 7px solid white;
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
  color: var(--muted);
}
.resource-gauge span,
.resource-gauges article > small {
  font-size: 11px;
  color: var(--muted);
  text-align: center;
  line-height: 1.7;
}
.load-summary {
  margin-top: auto;
  border-top: 1px solid var(--border);
  padding-top: 17px;
  display: flex;
  justify-content: space-between;
  gap: 12px;
  flex-wrap: wrap;
  font-size: 12px;
  color: var(--muted);
}
.load-summary b {
  font: 12px var(--mono);
  color: var(--text);
  margin-left: 12px;
}
.load-summary small {
  font-size: 11px;
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
.network-interface {
  display: flex;
  gap: 20px;
  padding: 15px 0;
  border-top: 1px solid var(--border);
}
.network-interface > strong {
  font: 12px var(--mono);
  width: 80px;
  overflow-wrap: anywhere;
}
.network-interface > div {
  display: flex;
  flex-wrap: wrap;
  gap: 12px 22px;
  flex: 1;
  min-width: 0;
}
.network-interface span {
  font-size: 12px;
  color: var(--muted);
}
.network-interface b {
  color: var(--text);
  font: 12px var(--mono);
  margin-left: 6px;
}
.network-interface small {
  font-size: 11px;
  color: var(--muted);
  flex-basis: 100%;
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
.observation-notice {
  background: #fff9ed;
  color: #946619;
  border: 1px solid #f0dfbd;
  border-radius: 6px;
  padding: 14px 18px;
  font-size: 13px;
  line-height: 1.8;
}
.observation-notice small {
  display: block;
  font: 11px/1.7 var(--mono);
}
@media (max-width: 1200px) {
  .overview-columns {
    grid-template-columns: 1fr;
  }
  .overview-summary {
    grid-template-columns: repeat(2, minmax(0, 1fr));
  }
  .resource-gauges {
    max-width: 620px;
    margin: auto;
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
  .overview-panel-head {
    gap: 10px;
  }
  .network-interface {
    gap: 10px;
  }
  .network-interface > strong {
    width: 60px;
  }
}
</style>
