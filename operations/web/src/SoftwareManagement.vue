<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref } from "vue";
import { ElMessage, ElMessageBox } from "element-plus";
import { request } from "./api";
import { hostTime, type Host } from "./hosts";
import ObservationBatchPanel from "./ObservationBatchPanel.vue";
import type { ObservationBatch } from "./observation";
import SoftwareIcon from "./SoftwareIcon.vue";
import SoftwareRecipes from "./SoftwareRecipes.vue";
import SoftwareDetailsDialog from "./SoftwareDetailsDialog.vue";
import SoftwareRegistrationDialog from "./SoftwareRegistrationDialog.vue";
import SoftwareDiscoveryDialog from "./SoftwareDiscoveryDialog.vue";
import type { ServiceInstallReceipt } from "./serviceInstallation";
import {
  softwareDesiredLabels,
  softwareModes,
  softwareRuntimeLabels,
  softwareState,
  softwareIsTool,
  softwareInstallationReady,
  softwareLifecycleAvailable,
  softwareTypes,
  softwareVersionLabel,
  softwareVersionReason,
  softwareVersionText,
  type SoftwareInstance,
  type SoftwareJob,
  type SoftwareMutationResult,
  type SoftwareRegistrationDraft,
} from "./software";

const emit = defineEmits<{ error: [unknown]; preparation: [string] }>();
const tab = ref<"instances" | "recipes">("instances");
const rows = ref<SoftwareInstance[]>([]);
const hosts = ref<Host[]>([]);
const hostId = ref("");
const query = ref("");
const mode = ref("ACTIVE");
const loading = ref(false);
const batch = ref<ObservationBatch>();
const detectionScope = ref("FILTERED");
const detectionCount = computed(
  () =>
    (detectionScope.value === "ALL" ? rows.value : visible.value).filter(
      (i) => i.managementMode !== "RELEASED" && softwareLifecycleAvailable(i),
    ).length,
);
const busy = ref(false);
const registration = ref(false);
const discovery = ref(false);
const registrationPrefill = ref<SoftwareRegistrationDraft>();
const detailId = ref("");
const job = ref<SoftwareJob>();
const logs = ref("");
const logSource = ref<"software" | "job">("software");
const logsLimited = ref(false);
const logsAt = ref("");
let disposed = false;
let timer: ReturnType<typeof setTimeout> | undefined;
let generation = 0;
let detailRevision = 0;
let batchGeneration = 0;

function currentDetail(id: string, revision: number) {
  return !disposed && detailId.value === id && detailRevision === revision;
}

function selectDetail(id: string) {
  detailRevision += 1;
  detailId.value = id;
  job.value = undefined;
  logs.value = "";
  logsLimited.value = false;
  logsAt.value = "";
  return detailRevision;
}

const visible = computed(() => {
  const term = query.value.trim().toLocaleLowerCase();
  return rows.value.filter(
    (item) =>
      (!hostId.value || item.hostId === hostId.value) &&
      (mode.value === "ALL" ||
        (mode.value === "ACTIVE"
          ? item.managementMode !== "RELEASED"
          : item.managementMode === mode.value)) &&
      (!term ||
        `${item.name} ${softwareTypes[item.softwareType]} ${item.locator}`
          .toLocaleLowerCase()
          .includes(term)),
  );
});
const detail = computed(() =>
  rows.value.find((item) => item.id === detailId.value),
);
const detailHost = computed(() =>
  hosts.value.find((item) => item.id === detail.value?.hostId),
);
const pendingJob = computed(
  () =>
    !!job.value &&
    (["QUEUED", "RUNNING", "UNKNOWN"].includes(job.value.status) ||
      job.value.targetAckPending),
);
const active = (item: SoftwareInstance) => item.managementMode !== "RELEASED";
const service = (item: SoftwareInstance) => !softwareIsTool(item);
const canAdopt = (item: SoftwareInstance) =>
  softwareInstallationReady(item) &&
  item.managementMode === "OBSERVE" &&
  service(item) &&
  item.freshness === "FRESH" &&
  !item.errorCode &&
  !!item.observation?.identityHash &&
  !["UNKNOWN", "MISSING"].includes(item.actualState) &&
  !hosts.value.find((host) => host.id === item.hostId)?.maintenance;
const canChange = (item: SoftwareInstance) =>
  softwareLifecycleAvailable(item) &&
  item.managementMode === "MANAGED" &&
  service(item) &&
  !hosts.value.find((host) => host.id === item.hostId)?.maintenance &&
  !pendingJob.value;

async function refresh() {
  if (loading.value) return;
  loading.value = true;
  const currentGeneration = generation;
  const currentBatchGeneration = batchGeneration;
  try {
    const [items, targetHosts] = await Promise.all([
      request<SoftwareInstance[]>("/software-instances"),
      request<Host[]>("/hosts"),
    ]);
    if (disposed) return;
    if (currentGeneration === generation) rows.value = items;
    hosts.value = targetHosts;
    const latestBatch = await request<ObservationBatch | null>(
      "/observation-batches",
    );
    if (!disposed && currentBatchGeneration === batchGeneration)
      batch.value = latestBatch || undefined;
    const selected = detail.value;
    const revision = detailRevision;
    if (selected?.lastJobId) {
      const refreshedJob = await request<SoftwareJob>(
        "/jobs/" + selected.lastJobId,
      );
      if (currentDetail(selected.id, revision)) job.value = refreshedJob;
    }
  } finally {
    loading.value = false;
  }
}
async function act(action: () => Promise<void>) {
  if (busy.value) return;
  busy.value = true;
  try {
    await action();
  } catch (error) {
    emit("error", error);
  } finally {
    busy.value = false;
  }
}
function update(item: SoftwareInstance) {
  generation += 1;
  rows.value = rows.value.some((row) => row.id === item.id)
    ? rows.value.map((row) => (row.id === item.id ? item : row))
    : [...rows.value, item];
}
async function showDetails(item: SoftwareInstance) {
  const revision = selectDetail(item.id);
  if (item.lastJobId)
    await act(async () => {
      const selectedJob = await request<SoftwareJob>("/jobs/" + item.lastJobId);
      if (currentDetail(item.id, revision)) job.value = selectedJob;
    });
}
async function saved(item: SoftwareInstance) {
  update(item);
  tab.value = "instances";
  mode.value = "ACTIVE";
  query.value = "";
  hostId.value = item.hostId;
  await showDetails(item);
  ElMessage.success("已登记为只读软件");
}
function openRegistration(prefill?: SoftwareRegistrationDraft) {
  registrationPrefill.value = prefill;
  discovery.value = false;
  registration.value = true;
}
async function detectAll() {
  batchGeneration += 1;
  await act(async () => {
    const result = await request<{ id: string }>(
      "/observation-batches",
      "POST",
      {
        kind: "SOFTWARE",
        hostId: detectionScope.value === "ALL" ? "" : hostId.value,
        query: detectionScope.value === "ALL" ? "" : query.value,
        mode: detectionScope.value === "ALL" ? "ACTIVE" : mode.value,
        idempotencyKey: crypto.randomUUID(),
      },
    );
    const resultBatch = await request<ObservationBatch>(
      "/observation-batches/" + result.id,
    );
    if (!disposed) batch.value = resultBatch;
  });
}
async function cancelDetection() {
  if (!batch.value) return;
  batchGeneration += 1;
  await act(async () => {
    const result = await request<ObservationBatch>(
      "/observation-batches/" + batch.value!.id + "/cancel",
      "POST",
      {},
    );
    if (!disposed) batch.value = result;
  });
}
async function observe(item: SoftwareInstance) {
  if (busy.value || !softwareLifecycleAvailable(item)) return;
  if (detailId.value !== item.id) selectDetail(item.id);
  await act(async () => {
    const updated = await request<SoftwareInstance>(
      `/software-instances/${item.id}/observe`,
      "POST",
      {},
    );
    update(updated);
  });
}
async function mutationDone(result: SoftwareMutationResult, revision: number) {
  const acceptedJob = result.jobId
    ? await request<SoftwareJob>("/jobs/" + result.jobId)
    : undefined;
  if (currentDetail(result.softwareInstanceId, revision))
    job.value = acceptedJob;
  await refresh();
}
async function configurationSubmitted(receipt: ServiceInstallReceipt) {
  const revision = detailRevision;
  await act(() => mutationDone(receipt, revision));
}
async function adopt(item: SoftwareInstance) {
  try {
    await ElMessageBox.confirm(
      `接管“${item.name}”的 ${softwareRuntimeLabels[item.runtimeKind]}（${item.locator}）？此后 Ops 可启停该对象。配置和业务数据保留。`,
      "接管生命周期",
      {
        confirmButtonText: "确认接管",
        cancelButtonText: "取消",
        customClass: "software-confirm",
        type: "warning",
      },
    );
  } catch {
    return;
  }
  if (busy.value) return;
  const revision = selectDetail(item.id);
  await act(async () =>
    mutationDone(
      await request<SoftwareMutationResult>(
        `/software-instances/${item.id}/adopt`,
        "POST",
        {
          expectedVersion: item.version,
          identityHash: item.observation?.identityHash,
          confirmed: true,
          idempotencyKey: crypto.randomUUID(),
        },
      ),
      revision,
    ),
  );
}
async function changeState(item: SoftwareInstance, action: "START" | "STOP") {
  if (action === "START" && !softwareInstallationReady(item)) return;
  if (action === "STOP") {
    try {
      await ElMessageBox.confirm(
        `停止“${item.name}”？使用该服务的业务可能受到影响。`,
        "停止服务",
        {
          confirmButtonText: "停止服务",
          cancelButtonText: "取消",
          type: "warning",
        },
      );
    } catch {
      return;
    }
  }
  if (busy.value) return;
  const revision = selectDetail(item.id);
  await act(async () =>
    mutationDone(
      await request<SoftwareMutationResult>(
        `/software-instances/${item.id}/actions`,
        "POST",
        {
          expectedVersion: item.version,
          action,
          idempotencyKey: crypto.randomUUID(),
        },
      ),
      revision,
    ),
  );
}
async function unmanage(item: SoftwareInstance) {
  try {
    await ElMessageBox.confirm(
      `解除“${item.name}”的管理？服务保持当前运行状态，远端文件和数据保留。`,
      "解除管理",
      {
        confirmButtonText: "解除管理",
        cancelButtonText: "取消",
        type: "warning",
      },
    );
  } catch {
    return;
  }
  if (busy.value) return;
  const revision = selectDetail(item.id);
  await act(async () => {
    await mutationDone(
      await request<SoftwareMutationResult>(
        `/software-instances/${item.id}/unmanage`,
        "POST",
        {
          expectedVersion: item.version,
          idempotencyKey: crypto.randomUUID(),
        },
      ),
      revision,
    );
    ElMessage.success(
      item.managementMode === "MANAGED" ? "解除管理已提交" : "已解除管理",
    );
  });
}
async function readLogs(source: "software" | "job") {
  const item = detail.value;
  if (!item) return;
  if (source === "software" && !softwareLifecycleAvailable(item)) return;
  const revision = detailRevision;
  const selectedJobId = job.value?.id;
  if (source === "job" && !selectedJobId) return;
  await act(async () => {
    const path =
      source === "job"
        ? "/jobs/" + selectedJobId + "/logs"
        : `/software-instances/${item.id}/logs`;
    const result = await request<{
      text: string;
      limited?: boolean;
      observedAt?: string;
    }>(path);
    if (!currentDetail(item.id, revision)) return;
    logs.value = result.text || "暂无日志";
    logsLimited.value = !!result.limited;
    logsAt.value = result.observedAt || "";
    logSource.value = source;
  });
}
async function reconcile() {
  if (!job.value) return;
  await act(async () => {
    await request("/jobs/" + job.value!.id + "/reconcile", "POST");
    await refresh();
  });
}
async function cancel() {
  if (!job.value) return;
  await act(async () => {
    await request("/jobs/" + job.value!.id + "/cancel", "POST");
    await refresh();
  });
}
function poll() {
  timer = setTimeout(async () => {
    if (disposed) return;
    if (
      !document.hidden &&
      !busy.value &&
      !registration.value &&
      !discovery.value &&
      tab.value === "instances"
    )
      await act(refresh);
    if (!disposed) poll();
  }, 5000);
}
onMounted(async () => {
  await act(refresh);
  if (!disposed) poll();
});
onUnmounted(() => {
  disposed = true;
  if (timer) clearTimeout(timer);
});
</script>

<template>
  <section class="content software-management" aria-label="软件管理">
    <div class="software-tabs" role="tablist" aria-label="软件管理视图">
      <button
        role="tab"
        aria-label="软件中心"
        :aria-selected="tab === 'instances'"
        :class="{ active: tab === 'instances' }"
        @click="tab = 'instances'"
      >
        软件中心
        <span class="tab-count">
          {{ rows.filter((item) => item.managementMode !== "RELEASED").length }}
        </span>
      </button>
      <button
        role="tab"
        :aria-selected="tab === 'recipes'"
        :class="{ active: tab === 'recipes' }"
        @click="tab = 'recipes'"
      >
        软件目录
      </button>
    </div>
    <SoftwareRecipes
      v-if="tab === 'recipes'"
      :host-id="hostId"
      @error="emit('error', $event)"
      @preparation="emit('preparation', $event)"
    />
    <div v-else role="tabpanel" aria-label="软件中心">
      <div class="software-toolbar">
        <div class="software-filters">
          <input
            v-model="query"
            type="search"
            placeholder="搜索软件或服务标识"
            aria-label="搜索软件或服务标识"
          />
          <select v-model="hostId" aria-label="筛选软件主机">
            <option value="">全部主机</option>
            <option v-for="host in hosts" :key="host.id" :value="host.id">
              {{ host.name }}
            </option>
          </select>
          <select v-model="mode" aria-label="筛选管理模式">
            <option value="ACTIVE">当前软件</option>
            <option value="OBSERVE">只读登记</option>
            <option value="MANAGED">已接管</option>
            <option value="RELEASED">已解除</option>
            <option value="ALL">全部登记</option>
          </select>
        </div>
        <div class="software-toolbar-actions">
          <select v-model="detectionScope" aria-label="检测范围">
            <option value="FILTERED">当前筛选</option>
            <option value="ALL">全部软件</option>
          </select>
          <button
            class="secondary"
            :disabled="busy || !detectionCount"
            @click="detectAll"
          >
            一键检测（{{ detectionCount }}）
          </button>
          <button
            class="secondary"
            :disabled="busy || !hosts.length"
            @click="discovery = true"
          >
            自动识别
          </button>
          <button
            class="secondary"
            :disabled="busy || loading"
            @click="act(refresh)"
          >
            刷新
          </button>
          <button
            class="primary"
            :disabled="busy || !hosts.length"
            @click="openRegistration()"
          >
            登记已有
          </button>
        </div>
      </div>
      <div
        v-if="query || hostId || mode !== 'ACTIVE'"
        class="software-count"
        aria-live="polite"
      >
        {{ visible.length }} 项软件
        <span v-if="loading">· 加载中…</span>
      </div>
      <ObservationBatchPanel
        :batch="batch"
        :busy="busy"
        @cancel="cancelDetection"
      />
      <div v-if="!rows.length" class="empty software-empty">
        <strong>{{ loading ? "正在加载软件" : "尚未添加软件" }}</strong>
        <span v-if="!loading">
          {{
            hosts.length
              ? "登记已有服务，或从软件目录安装"
              : "先在主机管理添加服务器"
          }}
        </span>
        <div v-if="!loading" class="software-empty-actions">
          <button
            class="primary"
            :disabled="!hosts.length"
            @click="openRegistration()"
          >
            登记已有
          </button>
          <button class="secondary" @click="tab = 'recipes'">
            浏览软件目录
          </button>
        </div>
      </div>
      <div v-else-if="!visible.length" class="empty">没有匹配的软件</div>
      <div v-else class="software-cards">
        <article
          v-for="item in visible"
          :key="item.id"
          class="software-card"
          :aria-label="item.name"
        >
          <header class="software-card-head">
            <span class="state-tag" :class="softwareState(item).tone">
              {{ softwareState(item).label }}
            </span>
            <span class="management-label">
              {{ softwareModes[item.managementMode] }}
            </span>
          </header>
          <div class="software-identity">
            <SoftwareIcon :type="item.softwareType" />
            <div>
              <h3>{{ item.name }}</h3>
              <span>
                {{ softwareTypes[item.softwareType] }} ·
                {{ softwareRuntimeLabels[item.runtimeKind] }}
              </span>
            </div>
          </div>
          <dl class="card-facts">
            <dt>所在主机</dt>
            <dd>
              {{
                item.hostName ||
                hosts.find((host) => host.id === item.hostId)?.name ||
                item.hostId
              }}
            </dd>
            <dt>
              {{ softwareVersionLabel(item.observation, item.runtimeKind) }}
            </dt>
            <dd
              class="card-version"
              :title="softwareVersionText(item.observation?.version)"
            >
              {{
                softwareVersionText(item.observation?.version) ||
                softwareVersionReason(item.observation, item.runtimeKind)
              }}
            </dd>
            <dt>期望状态</dt>
            <dd>{{ softwareDesiredLabels[item.desiredState] }}</dd>
            <dt>监听端口</dt>
            <dd>
              {{
                softwareIsTool(item)
                  ? "无需监听"
                  : item.observation?.listeningPorts?.join("、") || "未获取"
              }}
            </dd>
          </dl>
          <footer class="software-card-footer">
            <span
              v-if="
                item.latestAttempt?.code && item.latestAttempt.code !== 'READY'
              "
              :title="`${item.latestAttempt.code} · ${hostTime(item.latestAttempt.at)}`"
              >当前未确认</span
            >
            <span :title="hostTime(item.observation?.observedAt)">
              {{
                item.observation?.observedAt
                  ? `检查于 ${new Date(item.observation.observedAt).toLocaleTimeString("zh-CN", { timeZone: "Asia/Shanghai", hour12: false })}`
                  : "尚无检查"
              }}
            </span>
            <div class="software-row-actions">
              <button
                v-if="active(item)"
                class="quiet"
                :disabled="busy || !softwareLifecycleAvailable(item)"
                @click="observe(item)"
              >
                检查
              </button>
              <button class="quiet" :disabled="busy" @click="showDetails(item)">
                详情
              </button>
              <button
                v-if="item.managementMode === 'OBSERVE' && service(item)"
                class="quiet"
                :disabled="busy || !canAdopt(item)"
                @click="adopt(item)"
              >
                接管
              </button>
            </div>
          </footer>
        </article>
      </div>
    </div>
  </section>
  <SoftwareRegistrationDialog
    :open="registration"
    :hosts="hosts"
    :host-id="hostId"
    :prefill="registrationPrefill"
    @close="registration = false"
    @saved="saved"
    @error="emit('error', $event)"
  />
  <SoftwareDiscoveryDialog
    :open="discovery"
    :hosts="hosts"
    :host-id="hostId"
    @close="discovery = false"
    @register="openRegistration"
    @error="emit('error', $event)"
  />
  <SoftwareDetailsDialog
    :detail="detail"
    :host="detailHost"
    :busy="busy"
    :job="job"
    :pending-job="pendingJob"
    :logs="logs"
    :log-source="logSource"
    :logs-limited="logsLimited"
    :logs-at="logsAt"
    :can-adopt="!!detail && canAdopt(detail)"
    :can-change="!!detail && canChange(detail)"
    @close="selectDetail('')"
    @observe="observe"
    @adopt="adopt"
    @unmanage="unmanage"
    @change-state="changeState"
    @read-logs="readLogs"
    @reconcile="reconcile"
    @cancel="cancel"
    @configuration-submitted="configurationSubmitted"
    @preparation="emit('preparation', $event)"
  />
</template>

<style scoped>
.software-management {
  background: var(--surface);
  border: 1px solid var(--border);
  border-radius: 8px;
  padding: 0 24px 24px;
  box-shadow: var(--card-shadow);
}
.tab-count {
  display: inline-block;
  margin-left: 7px;
  padding: 1px 6px;
  border-radius: 4px;
  background: #f0f3f7;
  color: #7e8b9d;
  font: 11px var(--mono);
}
.software-identity {
  display: flex;
  align-items: center;
  gap: 12px;
}
.software-identity > div {
  min-width: 0;
}
.software-identity :deep(.software-icon) {
  width: 36px;
  height: 36px;
  border-radius: 8px;
  font-size: 17px;
}
.software-identity :deep(svg) {
  width: 30px;
  height: 30px;
}
.software-tabs {
  display: flex;
  gap: 28px;
  border-bottom: 1px solid var(--border);
  margin-bottom: 24px;
}
:global(.software-confirm .el-message-box__message) {
  overflow-wrap: anywhere;
}
.software-tabs button {
  padding: 20px 0 16px;
  border: 0;
  border-bottom: 2px solid transparent;
  background: transparent;
  color: var(--muted);
  font-size: 14px;
}
.software-tabs button.active {
  color: var(--accent);
  border-color: var(--accent);
  font-weight: 600;
}
.software-toolbar {
  display: grid;
  grid-template-columns: minmax(0, 1fr) auto;
  gap: 14px;
  align-items: center;
}
.software-filters {
  display: grid;
  grid-template-columns: minmax(140px, 240px) 132px 132px;
  gap: 10px;
}
.software-toolbar-actions {
  display: flex;
  align-items: center;
  gap: 10px;
  justify-content: flex-end;
  flex-wrap: wrap;
}
.software-toolbar-actions select {
  width: 120px;
  height: 38px;
  margin: 0;
}
.software-filters input,
.software-filters select {
  width: 100%;
  min-width: 0;
  margin: 0;
  height: 38px;
}
.software-toolbar-actions button {
  min-height: 38px;
}
.software-toolbar-actions .primary,
.software-empty-actions .primary {
  width: auto;
  margin: 0;
}
.software-count {
  color: var(--muted);
  font-size: 12px;
  margin: 14px 0 0;
}
.software-empty,
.software-cards,
.software-management [role="tabpanel"] > .empty {
  margin-top: 18px;
}
.software-empty {
  display: grid;
  gap: 16px;
  justify-items: center;
  padding: 60px 20px;
}
.software-empty strong {
  color: var(--text);
  font-size: 15px;
  font-weight: 500;
}
.software-empty > span {
  font-size: 13px;
}
.software-empty-actions {
  display: flex;
  gap: 10px;
  margin-top: 6px;
}
.software-cards {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(min(100%, 310px), 1fr));
  gap: 18px;
}
.software-card {
  display: flex;
  flex-direction: column;
  min-width: 0;
  border: 1px solid var(--border);
  border-radius: 8px;
  overflow: hidden;
  background: var(--surface);
  transition:
    border-color 0.15s ease,
    box-shadow 0.15s ease;
}
.software-card:hover {
  border-color: #cbd5e1;
  box-shadow: 0 2px 8px rgba(15, 23, 42, 0.04);
}
.software-card-head {
  display: flex;
  justify-content: space-between;
  align-items: center;
  gap: 10px;
  padding: 16px 18px 0;
}
.management-label {
  font-size: 11px;
  color: var(--muted);
}
.software-card .software-identity {
  padding: 22px 18px 18px;
}
.software-identity h3 {
  font-size: 15px;
  font-weight: 600;
  margin: 0 0 7px;
  overflow-wrap: anywhere;
}
.software-identity span {
  font-size: 12px;
  color: var(--muted);
}
.card-facts {
  display: grid;
  grid-template-columns: 84px minmax(0, 1fr);
  gap: 12px;
  margin: 0;
  padding: 0 18px 22px;
  font-size: 12px;
}
.card-facts dt {
  color: var(--muted);
}
.card-facts dd {
  margin: 0;
  overflow-wrap: anywhere;
}
.card-version {
  display: -webkit-box;
  -webkit-line-clamp: 2;
  -webkit-box-orient: vertical;
  overflow: hidden;
  font: 11px/1.7 var(--mono);
}
.software-card-footer {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 6px;
  padding: 10px 12px 10px 18px;
  border-top: 1px solid var(--border);
  margin-top: auto;
  background: #fafbfd;
}
.software-card-footer > span {
  font-size: 11px;
  color: var(--muted);
}
.software-row-actions {
  display: flex;
  flex-wrap: wrap;
}
.software-row-actions .quiet {
  font-size: 12px;
  padding: 5px 7px;
}
@media (max-width: 1250px) {
  .software-toolbar {
    grid-template-columns: 1fr;
  }
  .software-filters {
    grid-template-columns: minmax(140px, 1fr) 150px 150px;
  }
}
@media (max-width: 650px) {
  .software-management {
    padding: 0 14px 16px;
  }
  .software-tabs {
    gap: 24px;
    margin-bottom: 18px;
  }
  .software-tabs button {
    font-size: 13px;
  }
  .software-filters {
    grid-template-columns: 1fr 1fr;
  }
  .software-filters input {
    grid-column: 1/-1;
  }
  .software-toolbar-actions {
    gap: 8px;
  }
  .software-toolbar-actions button {
    flex: 1;
    padding-inline: 10px;
  }
}
</style>
