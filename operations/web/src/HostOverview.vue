<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref } from "vue";
import { request } from "./api";
import { ElMessage } from "element-plus";
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
const emit = defineEmits<{ error: [unknown] }>();
const hosts = ref<Host[]>([]),
  software = ref<SoftwareInstance[]>([]),
  metrics = ref<Record<string, HostMetricSnapshot>>({});
const selectedId = ref(""),
  loading = ref(false);
const selected = computed(() =>
  hosts.value.find((h) => h.id === selectedId.value),
);
const current = computed(() => metrics.value[selectedId.value]);
const softwareOnHost = computed(() =>
  software.value.filter(
    (s) =>
      s.hostId === selectedId.value &&
      s.managementMode !== "RELEASED" &&
      !softwareIsHistoryOnly(s),
  ),
);
const observedCount = computed(
  () => hosts.value.filter((host) => live(metrics.value[host.id])).length,
);
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
    const snapshots = await request<HostObservation[]>("/host-observations");
    if (disposed) return;
    metrics.value = Object.fromEntries(
      snapshots.map((observation) => [
        observation.hostId,
        metricSnapshot(observation),
      ]),
    );
  } catch (error) {
    if (!disposed) emit("error", error);
  } finally {
    loading.value = false;
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
          {{
            software.filter(
              (s) =>
                s.managementMode !== "RELEASED" && !softwareIsHistoryOnly(s),
            ).length
          }}
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
