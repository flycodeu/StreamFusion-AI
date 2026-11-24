<script setup lang="ts">
import { computed, nextTick, onMounted, onUnmounted, ref, watch } from "vue";
import {
  ElButton,
  ElDialog,
  ElDrawer,
  ElEmpty,
  ElInput,
  ElMessage,
  ElMessageBox,
  ElOption,
  ElPagination,
  ElSelect,
  ElTabPane,
  ElTable,
  ElTableColumn,
  ElTabs,
  ElTag,
} from "element-plus";
import { request } from "./api";
import HostConnectionDialog from "./HostConnectionDialog.vue";
import HostPreparationPanel from "./HostPreparationPanel.vue";
import HostTerminalToolsPanel from "./HostTerminalToolsPanel.vue";
import HostSystemPanel from "./HostSystemPanel.vue";
import PortManagement from "./PortManagement.vue";
import OpsPagination from "./OpsPagination.vue";
import { usePagination } from "./pagination";
import {
  metricBytes,
  metricPercent,
  metricSnapshot,
  metricsLive,
  type HostMetricSnapshot,
  type HostObservation,
} from "./hostMetrics";
import {
  hostCapabilityLabels,
  hostCapabilityTimedOut,
  hostObservationExpired,
  hostTime,
  type Host,
  type HostCapabilityKey,
  type SSHCredential,
} from "./hosts";

const props = defineProps<{ preparationHostId?: string }>();
const emit = defineEmits<{
  error: [unknown];
  preparationOpened: [];
  credentialSaved: [];
  docker: [string];
}>();
const hosts = ref<Host[]>([]);
const credentials = ref<SSHCredential[]>([]);
const hostMetrics = ref<Record<string, HostMetricSnapshot>>({});
const loading = ref(false);
const actionHost = ref("");
const query = ref("");
const stateFilter = ref("");
const dialog = ref(false);
const editingHost = ref<Host>();
const detailId = ref("");
const detailTab = ref("system");
const terminalAdminId = ref("");
watch(detailId, (id) => {
  const target = hosts.value.find((h) => h.id === id);
  detailTab.value =
    target?.kind === "SSH" && target.capabilities?.mutation?.status !== "AVAILABLE"
      ? "access"
      : "system";
  terminalAdminId.value = "";
});
async function openTerminalMaintenance() {
  const hostId = detailId.value;
  try {
    const me = await request<{ id: string }>("/auth/me");
    if (!disposed && detailId.value === hostId) terminalAdminId.value = me.id;
  } catch (error) {
    if (!disposed && detailId.value === hostId) emit("error", error);
  }
}
watch(
  () => [props.preparationHostId, hosts.value.length],
  async () => {
    if (
      props.preparationHostId &&
      hosts.value.some((host) => host.id === props.preparationHostId)
    ) {
      detailId.value = props.preparationHostId;
      await nextTick();
      if (detailId.value === props.preparationHostId) {
        detailTab.value = "access";
        emit("preparationOpened");
      }
    }
  },
  { immediate: true },
);
const now = ref(Date.now());
const capabilityKeys: HostCapabilityKey[] = [
  "reachability",
  "readOnly",
  "mutation",
  "docker",
];
const observationSources: Record<string, string> = {
  SSH: "SSH 握手",
  SSH_COMMAND: "SSH 只读命令",
  LOCAL: "本机检查",
  DOCKER: "Docker 探针",
  EXECUTOR: "受管执行器",
  NONE: "尚无观测",
};
let timer: ReturnType<typeof setTimeout> | undefined;
let disposed = false;
let dataRevision = 0;

function connectionState(host: Host): { label: string; tone: string } {
  if (!host.observedAt || !host.capabilities?.expiresAt)
    return { label: "未检查", tone: "state-muted" };
  if (hostObservationExpired(host, now.value))
    return {
      label:
        host.capabilities.reachability?.status === "AVAILABLE"
          ? "上次已连接"
          : "当前未确认",
      tone: "state-muted",
    };
  const status = host.capabilities.reachability?.status;
  if (status === "AVAILABLE") return { label: "已连接", tone: "state-success" };
  if (status === "UNAVAILABLE")
    return { label: "连接异常", tone: "state-error" };
  return { label: "未确认", tone: "state-muted" };
}

const visibleHosts = computed(() => {
  const term = query.value.trim().toLocaleLowerCase();
  return hosts.value.filter(
    (host) =>
      (!term ||
        `${host.name} ${host.address} ${host.sshUser}`
          .toLocaleLowerCase()
          .includes(term)) &&
      (!stateFilter.value ||
        (stateFilter.value === "MAINTENANCE"
          ? host.maintenance
          : stateFilter.value === "CONNECTED"
            ? connectionState(host).label === "已连接"
            : connectionState(host).label !== "已连接")),
  );
});
const { page, pageSize, total, pageItems: pagedHosts } = usePagination(() => visibleHosts.value, [query, stateFilter]);
const detail = computed(() =>
  hosts.value.find((host) => host.id === detailId.value),
);
const checking = (host: Host) => actionHost.value === host.id;
const credentialName = (host: Host) =>
  credentials.value.find(
    (item) =>
      item.id === host.credentialId &&
      item.revision === host.credentialRevision,
  )?.name || "凭据不可用";
const capabilityLabel = (host: Host, key: HostCapabilityKey) => {
  const fact = host.capabilities?.[key];
  const prefix = hostObservationExpired(host, now.value) ? "上次" : "";
  if (hostCapabilityTimedOut(fact)) return prefix + "检查超时";
  return fact?.status === "AVAILABLE"
    ? prefix + "可用"
    : fact?.status === "UNAVAILABLE"
      ? prefix + "不可用"
      : "未确认";
};
const capabilityTone = (host: Host, key: HostCapabilityKey) => {
  const fact = host.capabilities?.[key];
  if (hostObservationExpired(host, now.value) || hostCapabilityTimedOut(fact)) return "capability-unknown";
  return fact?.status === "AVAILABLE"
    ? "capability-available"
    : fact?.status === "UNAVAILABLE"
      ? "capability-unavailable"
      : "capability-unknown";
};
function capabilityMessage(host: Host, key: HostCapabilityKey) {
  const fact = host.capabilities?.[key];
  if (key === "mutation" && hostCapabilityTimedOut(fact)) return "执行器检查未在时限内完成，暂无法确认变更权限。可以重新检查。";
  return fact?.message || "尚未检查";
}

async function load() {
  if (loading.value) return;
  loading.value = true;
  const revision = dataRevision;
  try {
    const [items, keys, observations] = await Promise.all([
      request<Host[]>("/hosts"),
      request<SSHCredential[]>("/credentials"),
      request<HostObservation[]>("/host-observations").catch(() => []),
    ]);
    if (disposed) return;
    if (revision === dataRevision) hosts.value = items;
    credentials.value = keys;
    hostMetrics.value = Object.fromEntries(
      observations.map((obs) => [obs.hostId, metricSnapshot(obs)]),
    );
    now.value = Date.now();
  } catch (error) {
    emit("error", error);
  } finally {
    loading.value = false;
  }
}

function hostResourceLine(hostId: string) {
  const snap = hostMetrics.value[hostId];
  if (!snap || !metricsLive(snap)) return "";
  const cpu = snap.cpuPercent !== undefined ? `CPU ${snap.cpuPercent.toFixed(0)}%` : "";
  const memUsed =
    snap.memoryTotalBytes !== undefined && snap.memoryAvailableBytes !== undefined
      ? snap.memoryTotalBytes - snap.memoryAvailableBytes
      : undefined;
  const memPct = metricPercent(memUsed, snap.memoryTotalBytes);
  const mem = memPct !== undefined ? `内存 ${memPct.toFixed(0)}%` : "";
  const diskPct = metricPercent(snap.rootUsedBytes, snap.rootTotalBytes);
  const disk = diskPct !== undefined ? `磁盘 ${diskPct.toFixed(0)}%` : "";
  return [cpu, mem, disk].filter(Boolean).join(" · ");
}

async function act(host: Host, action: () => Promise<void>) {
  if (actionHost.value) return;
  actionHost.value = host.id;
  try {
    await action();
  } catch (error) {
    emit("error", error);
  } finally {
    actionHost.value = "";
  }
}

function openConnection(host?: Host) {
  editingHost.value = host;
  dialog.value = true;
}

function updateHost(host: Host) {
  dataRevision += 1;
  const existing = hosts.value.some((item) => item.id === host.id);
  hosts.value = existing
    ? hosts.value.map((item) => (item.id === host.id ? host : item))
    : [...hosts.value, host];
}

async function connectionSaved(host: Host) {
  const isNew = !hosts.value.some((item) => item.id === host.id);
  updateHost(host);
  await load();
  if (
    isNew &&
    host.kind === "SSH" &&
    host.capabilities?.mutation?.status !== "AVAILABLE"
  ) {
    detailId.value = host.id;
    await nextTick();
    detailTab.value = "access";
  }
}

async function refreshCredentials() {
  try {
    const keys = await request<SSHCredential[]>("/credentials");
    if (!disposed) credentials.value = keys;
    emit("credentialSaved");
  } catch (error) {
    emit("error", error);
  }
}

async function checkHost(host: Host) {
  await act(host, async () => {
    const updated = await request<Host>(`/hosts/${host.id}/checks`, "POST");
    updateHost(updated);
    now.value = Date.now();
    detailId.value = updated.id;
  });
}

async function setMaintenance(host: Host) {
  if (!host.maintenance) {
    try {
      await ElMessageBox.confirm(
        "进入维护后，该主机暂停接收 Ops 新的变更操作。已运行的服务继续运行。",
        "进入维护",
        {
          confirmButtonText: "进入维护",
          cancelButtonText: "取消",
          type: "warning",
        },
      );
    } catch {
      return;
    }
  }
  await act(host, async () => {
    const updated = await request<Host>(
      `/hosts/${host.id}/maintenance`,
      "PATCH",
      {
        maintenance: !host.maintenance,
        expectedVersion: host.version,
      },
    );
    updateHost(updated);
    ElMessage.success(host.maintenance ? "已退出维护" : "主机已进入维护");
  });
}

async function removeHost(host: Host) {
  try {
    await ElMessageBox.confirm(
      `移除“${host.name}”的登记？服务器上的软件和文件会保留；存在作业历史或关联记录的主机无法移除。`,
      "移除主机",
      {
        confirmButtonText: "移除登记",
        cancelButtonText: "取消",
        type: "warning",
      },
    );
  } catch {
    return;
  }
  await act(host, async () => {
    await request(
      `/hosts/${host.id}?expectedVersion=${encodeURIComponent(host.version)}`,
      "DELETE",
    );
    if (detailId.value === host.id) detailId.value = "";
    dataRevision += 1;
    hosts.value = hosts.value.filter((item) => item.id !== host.id);
    ElMessage.success("主机登记已移除");
  });
}

function poll() {
  timer = setTimeout(async () => {
    if (disposed) return;
    now.value = Date.now();
    if (!document.hidden && !dialog.value && !actionHost.value) await load();
    if (!disposed) poll();
  }, 15000);
}

function hostStateTag(tone: string): "success" | "danger" | "warning" | "info" {
  if (tone === "state-success") return "success";
  if (tone === "state-error") return "danger";
  if (tone === "state-warning") return "warning";
  return "info";
}

onMounted(async () => {
  await load();
  if (!disposed) poll();
});
onUnmounted(() => {
  disposed = true;
  if (timer) clearTimeout(timer);
});
</script>

<template>
  <section class="content host-management" aria-label="主机管理">
    <div class="ops-el-toolbar">
      <div class="ops-el-toolbar-group">
        <ElInput
          v-model="query"
          clearable
          placeholder="搜索服务器名称、IP 或 SSH 用户"
          style="width: 240px"
        />
        <ElSelect
          v-model="stateFilter"
          clearable
          :value-on-clear="''"
          placeholder="全部状态"
          style="width: 155px"
        >
          <ElOption label="全部状态" value="" />
          <ElOption label="已连接" value="CONNECTED" />
          <ElOption label="待检查 / 异常" value="ATTENTION" />
          <ElOption label="维护中" value="MAINTENANCE" />
        </ElSelect>
        <span v-if="query || stateFilter" class="host-count" aria-live="polite">
          {{ visibleHosts.length }} / {{ hosts.length }} 台主机
        </span>
      </div>
      <div class="ops-el-toolbar-group">
        <ElButton
          :loading="loading"
          :disabled="!!actionHost"
          @click="load"
        >
          刷新
        </ElButton>
        <ElButton
          type="primary"
          :disabled="!!actionHost"
          @click="openConnection()"
        >
          新增主机
        </ElButton>
      </div>
    </div>

    <ElTable
      :data="pagedHosts"
      row-key="id"
      border
      stripe
      style="width: 100%; margin-top: 16px"
    >
      <ElTableColumn label="状态" width="118">
        <template #default="{ row }">
          <div class="host-status">
            <ElTag :type="hostStateTag(connectionState(row as Host).tone)" size="small">
              {{ connectionState(row as Host).label }}
            </ElTag>
            <ElTag v-if="row.maintenance" type="warning" size="small" effect="plain">
              维护中
            </ElTag>
          </div>
        </template>
      </ElTableColumn>
      <ElTableColumn label="服务器 / 连接地址" min-width="230" show-overflow-tooltip>
        <template #default="{ row }">
          <div class="host-cell">
            <strong class="host-name">{{ row.name }}</strong>
            <span class="host-address">
              {{ row.kind === "LOCAL" ? "控制器本机" : `${row.address}:${row.sshPort}` }}
            </span>
            <small v-if="row.capabilities?.platform || hostResourceLine(row.id)" class="muted">
              {{ hostMetrics[row.id]?.os || row.capabilities?.platform || "未检测系统" }}
              <template v-if="hostResourceLine(row.id)">
                · {{ hostResourceLine(row.id) }}
              </template>
            </small>
            <small v-if="row.kind === 'SSH'" class="muted">
              {{ row.sshUser }} · {{ credentialName(row as Host) }}
            </small>
          </div>
        </template>
      </ElTableColumn>
      <ElTableColumn label="运维能力" min-width="240">
        <template #default="{ row }">
          <div class="capability-grid">
            <span
              v-for="key in capabilityKeys"
              :key="key"
              :class="capabilityTone(row as Host, key)"
            >
              <i aria-hidden="true"></i>
              {{
                row.kind === "LOCAL" && key === "reachability"
                  ? "本机连通"
                  : hostCapabilityLabels[key]
              }}
              <b>{{ capabilityLabel(row as Host, key) }}</b>
            </span>
          </div>
        </template>
      </ElTableColumn>
      <ElTableColumn label="最近检查" width="165">
        <template #default="{ row }">
          <div>
            <span class="host-time">{{ hostTime(row.observedAt) }}</span>
            <small v-if="row.capabilities?.expiresAt" class="muted" style="display: block">
              {{ hostObservationExpired(row as Host, now) ? "上次检查 · 待更新" : "观测有效" }}
            </small>
            <small v-else class="muted" style="display: block">尚无观测</small>
          </div>
        </template>
      </ElTableColumn>
      <ElTableColumn label="操作" width="235" fixed="right">
        <template #default="{ row }">
          <div class="host-actions">
            <ElButton
              link
              type="primary"
              size="small"
              :disabled="!!actionHost"
              @click="checkHost(row as Host)"
            >
              {{ checking(row as Host) ? "检查中…" : "检查能力" }}
            </ElButton>
            <ElButton
              link
              type="primary"
              size="small"
              :disabled="!!actionHost || row.maintenance"
              @click="emit('docker', row.id)"
            >
              装软件
            </ElButton>
            <ElButton
              link
              size="small"
              :disabled="!!actionHost"
              @click="openConnection(row as Host)"
            >
              编辑
            </ElButton>
            <ElButton link type="primary" size="small" @click="detailId = row.id">
              管理
            </ElButton>
          </div>
        </template>
      </ElTableColumn>
      <template #empty>
        <ElEmpty
          :description="
            hosts.length
              ? '没有匹配的主机'
              : loading
                ? '正在加载主机…'
                : '尚未添加服务器，点击右上角新增主机'
          "
        />
      </template>
    </ElTable>

    <div v-if="hosts.length" class="ops-el-pager">
      <ElPagination
        v-model:current-page="page"
        v-model:page-size="pageSize"
        :page-sizes="[10, 20, 50]"
        :total="total"
        background
        layout="total, sizes, prev, pager, next"
      />
    </div>
  </section>
  <HostConnectionDialog
    :open="dialog"
    :host="editingHost"
    @close="dialog = false"
    @saved="connectionSaved"
    @credential-saved="refreshCredentials"
    @error="emit('error', $event)"
  />
  <ElDrawer
    class="host-detail-drawer"
    :model-value="!!detail"
    :title="detail?.name || '主机详情'"
    size="min(900px, 100vw)"
    :close-on-click-modal="true" :close-on-press-escape="true" :show-close="true"
    @close="detailId = ''"
  >
    <div v-if="detail" class="host-detail">
      <div class="detail-status">
        <span class="state-tag" :class="connectionState(detail).tone">
          {{ connectionState(detail).label }}
        </span>
        <span v-if="detail.maintenance" class="maintenance-tag">维护中</span>
        <span class="spacer"></span>
        <button class="quiet" @click="openTerminalMaintenance">终端工具维护</button>
        <button
          class="secondary"
          :disabled="!!actionHost"
          @click="checkHost(detail)"
        >
          {{ checking(detail) ? "检查中…" : "重新检查" }}
        </button>
      </div>
      <ElTabs v-model="detailTab">
        <ElTabPane label="资源监控" name="system">
          <HostSystemPanel v-if="detailTab === 'system'" :key="detail.id" :host="detail"
            @error="emit('error', $event)" />
        </ElTabPane>
        <ElTabPane label="端口与防火墙" name="ports">
          <PortManagement
            v-if="detailTab === 'ports'"
            :key="detail.id"
            :fixed-host-id="detail.id"
            @error="emit('error', $event)"
            @preparation="detailTab = 'access'"
          />
        </ElTabPane>
        <ElTabPane label="接入与能力" name="access">
          <div class="access-tab-stack">
            <div
              v-if="detail.kind === 'SSH' && detail.capabilities?.mutation?.status !== 'AVAILABLE'"
              class="ops-guide-banner"
            >
              <strong>启用全部运维能力：</strong>
              安装接入组件后，本台服务器即可支持软件安装、启停控制、实时日志与防火墙配置。
            </div>
            <HostPreparationPanel
              v-if="detailTab === 'access'"
              :host="detail"
              @checked="load"
            />
            <div class="capability-details">
              <article v-for="key in capabilityKeys" :key="key">
                <header>
                  <div class="capability-heading"><strong>
                    {{
                      detail.kind === "LOCAL" && key === "reachability"
                        ? "本机连通"
                        : hostCapabilityLabels[key]
                    }}
                  </strong>
                  <button v-if="key === 'docker'" class="quiet capability-action" :disabled="!!actionHost || detail.maintenance"
                    @click="emit('docker', detail.id)">安装与管理</button></div>
                  <span :class="capabilityTone(detail, key)">
                    {{ capabilityLabel(detail, key) }}
                  </span>
                </header>
                <p v-if="detail.capabilities?.[key]?.status !== 'AVAILABLE'">{{ capabilityMessage(detail, key) }}</p>
                <details
                  v-if="
                    detail.capabilities?.[key]?.observedAt ||
                    (detail.capabilities?.[key]?.source &&
                      detail.capabilities?.[key]?.source !== 'NONE')
                  "
                  class="capability-evidence"
                >
                  <summary>检查依据</summary>
                  <p v-if="detail.capabilities?.[key]?.status === 'AVAILABLE'">{{ detail.capabilities?.[key]?.message }}</p>
                  <span>
                    来源：{{
                      observationSources[
                        detail.capabilities?.[key]?.source || "NONE"
                      ] || '其他检查方式'
                    }}
                  </span>
                  <time>
                    {{ hostTime(detail.capabilities?.[key]?.observedAt) }}
                  </time>
                </details>
              </article>
            </div>
          </div>
        </ElTabPane>
        <ElTabPane label="连接与目录" name="connection">
          <div class="connection-tab-stack">
            <dl class="host-facts">
              <dt>连接地址</dt>
              <dd>
                {{
                  detail.kind === "LOCAL"
                    ? "控制器本机"
                    : `${detail.address}:${detail.sshPort}`
                }}
              </dd>
              <template v-if="detail.kind === 'SSH'">
                <dt>SSH 用户</dt>
                <dd>{{ detail.sshUser }}</dd>
                <dt>连接凭据</dt>
                <dd>{{ credentialName(detail) }}</dd>
                <dt>已确认指纹</dt>
                <dd>
                  <code>{{ detail.hostFingerprint }}</code>
                </dd>
              </template>
              <dt>运行环境</dt>
              <dd>{{ detail.capabilities?.platform || "未检查" }}</dd>
              <dt>Ops 工作目录</dt>
              <dd>
                <code>{{ detail.installRoot }}</code>
              </dd>
              <dt>最近检查</dt>
              <dd>{{ hostTime(detail.observedAt) }} · 有效至 {{ hostTime(detail.capabilities?.expiresAt) }}</dd>
            </dl>
            <details class="host-identifiers">
              <summary>工作目录结构与登记标识</summary>
              <dl class="host-facts">
                <dt>应用与版本</dt>
                <dd><code>{{ detail.installRoot }}/instances/</code></dd>
                <dt>环境记录</dt>
                <dd><code>{{ detail.installRoot }}/plugins/</code></dd>
                <dt>离线材料</dt>
                <dd><code>{{ detail.installRoot }}/packages/</code></dd>
                <dt>作业回执</dt>
                <dd><code>{{ detail.installRoot }}/jobs/</code></dd>
                <dt>主机标识</dt>
                <dd><code>{{ detail.id }}</code></dd>
                <dt>记录版本</dt>
                <dd>{{ detail.version }}<template v-if="detail.kind === 'SSH'"> · 凭据 r{{ detail.credentialRevision }}</template></dd>
              </dl>
            </details>
          </div>
        </ElTabPane>
      </ElTabs>
    </div>
    <template #footer>
      <footer v-if="detail" class="detail-actions">
        <button
          class="quiet danger"
          :disabled="!!actionHost"
          @click="removeHost(detail)"
        >
          移除登记
        </button>
        <span class="spacer"></span>
        <button
          class="secondary"
          :disabled="!!actionHost"
          @click="setMaintenance(detail)"
        >
          {{ detail.maintenance ? "退出维护" : "进入维护" }}
        </button>
        <button
          class="secondary"
          :disabled="!!actionHost"
          @click="openConnection(detail)"
        >
          编辑连接
        </button>
      </footer>
    </template>
  </ElDrawer>
  <ElDialog v-if="detail" :model-value="!!terminalAdminId" :title="`${detail.name} · 终端工具维护`" width="min(680px, 94vw)" align-center destroy-on-close append-to-body @close="terminalAdminId = ''">
    <HostTerminalToolsPanel v-if="terminalAdminId" :key="terminalAdminId + detail.id" :host="detail" :admin-id="terminalAdminId" />
  </ElDialog>
</template>

<style scoped>
.host-management {
  padding: 22px 24px 24px;
  background: var(--surface);
  border: 1px solid var(--border);
  border-radius: 8px;
  box-shadow: var(--card-shadow);
}
.host-toolbar {
  display: flex;
  justify-content: space-between;
  align-items: center;
  gap: 16px;
  flex-wrap: wrap;
}
.host-filters,
.host-toolbar-actions {
  display: flex;
  align-items: center;
  gap: 10px;
}
.host-filters {
  flex: 1;
  min-width: 260px;
}
.host-filters input {
  width: 280px;
  min-width: 0;
  height: 38px;
  margin: 0;
}
.host-filters select {
  width: 150px;
  height: 38px;
  margin: 0;
}
.host-toolbar-actions .primary {
  width: auto;
  margin: 0;
  min-height: 38px;
}
.host-toolbar-actions .secondary {
  min-height: 38px;
}
.host-count {
  margin: 14px 0 0;
  color: var(--muted);
  font-size: 12px;
}
.host-empty,
.host-management > .empty,
.host-table {
  margin-top: 16px;
}
.host-empty {
  display: grid;
  gap: 14px;
  justify-items: center;
  padding: 64px 20px;
}
.host-empty strong {
  color: var(--text);
  font-size: 15px;
  font-weight: 500;
}
.host-empty span {
  font-size: 13px;
}
.host-table table {
  min-width: 960px;
}
.host-table th:first-child {
  width: 140px;
}
.host-status {
  display: flex;
  flex-direction: column;
  align-items: start;
  gap: 8px;
}
.maintenance-tag {
  display: inline-flex;
  align-items: center;
  font-size: 12px;
  color: #946619;
  white-space: nowrap;
}
.maintenance-tag::before {
  content: "";
  display: block;
  width: 5px;
  height: 5px;
  border-radius: 50%;
  background: currentColor;
  margin-right: 6px;
}
.host-cell {
  display: grid;
  gap: 2px;
}
.host-name {
  display: block;
  font-size: 14px;
  font-weight: 600;
  overflow-wrap: anywhere;
}
.host-address {
  display: block;
  color: #435269;
  font: 12px/1.5 var(--mono);
}
.capability-grid {
  display: grid;
  grid-template-columns: repeat(2, max-content);
  gap: 10px 20px;
}
.capability-grid > span {
  display: flex;
  align-items: center;
  font-size: 12px;
  gap: 6px;
  white-space: nowrap;
}
.capability-grid b {
  font-size: 11px;
  font-weight: 400;
  opacity: 0.9;
}
.capability-grid i {
  width: 5px;
  height: 5px;
  border-radius: 50%;
  background: currentColor;
}
.capability-available {
  color: var(--success);
}
.capability-unavailable {
  color: #946619;
}
.capability-unknown {
  color: var(--muted);
}
.host-time {
  white-space: nowrap;
  font-size: 12px;
}
.host-actions {
  display: flex;
  flex-wrap: nowrap;
  align-items: center;
  gap: 8px;
}
.host-detail {
  display: grid;
  grid-template-columns: minmax(0, 1fr);
  gap: 22px;
  min-width: 0;
  max-width: 100%;
}
.host-detail :deep(.el-tabs),
.host-detail :deep(.el-tabs__header),
.host-detail :deep(.el-tabs__content) {
  min-width: 0;
  max-width: 100%;
}
.detail-status {
  display: flex;
  align-items: center;
  gap: 12px;
  flex-wrap: wrap;
}
.access-tab-stack,
.connection-tab-stack {
  display: grid;
  gap: 20px;
}
.host-facts {
  display: grid;
  grid-template-columns: 95px minmax(0, 1fr);
  gap: 14px;
  margin: 0;
  font-size: 13px;
}
.host-facts dt {
  color: var(--muted);
}
.host-facts dd {
  margin: 0;
  overflow-wrap: anywhere;
}
.host-facts code {
  font: 12px/1.6 var(--mono);
}
.capability-details {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  border: 1px solid var(--border);
  border-radius: 6px;
  overflow: hidden;
}
.capability-details article {
  padding: 16px;
  min-width: 0;
}
.capability-details article:nth-child(odd) {
  border-right: 1px solid var(--border);
}
.capability-details article:nth-child(-n + 2) {
  border-bottom: 1px solid var(--border);
}
.capability-details header {
  display: flex;
  justify-content: space-between;
  gap: 10px;
  align-items: center;
  font-size: 12px;
}
.capability-details strong {
  font-size: 13px;
  font-weight: 500;
}
.capability-heading { display: flex; align-items: center; gap: 12px; min-width: 0; }
.capability-heading strong, .capability-details header > span { white-space: nowrap; }
.capability-action { padding: 0; font-size: 12px; white-space: nowrap; }
.capability-details p {
  color: #435269;
  font-size: 12px;
  line-height: 1.7;
  margin: 12px 0 0;
  overflow-wrap: anywhere;
}
.capability-evidence {
  margin-top: 12px;
  color: var(--muted);
  font-size: 11px;
}
.capability-evidence summary {
  cursor: pointer;
}
.capability-evidence > :not(summary) {
  display: block;
  margin-top: 8px;
}
.capability-evidence code {
  overflow-wrap: anywhere;
  font-family: var(--mono);
}
.host-identifiers {
  padding: 0;
}
.host-identifiers summary {
  color: var(--muted);
  cursor: pointer;
  font-size: 13px;
}
.host-identifiers dl {
  margin-top: 18px;
}
.detail-actions {
  flex-wrap: wrap;
  display: flex;
  align-items: center;
  gap: 10px;
}
@media (max-width: 1100px) {
  .host-table {
    border: 0;
    overflow: visible;
  }
  .host-table table,
  .host-table tbody {
    display: block;
    min-width: 0;
  }
  .host-table thead {
    position: absolute;
    width: 1px;
    height: 1px;
    overflow: hidden;
    clip-path: inset(50%);
  }
  .host-table tr {
    display: grid;
    grid-template-columns: minmax(0, 1fr) minmax(0, 1fr);
    gap: 20px;
    padding: 18px;
    border: 1px solid var(--border);
    border-radius: 6px;
    margin-bottom: 12px;
  }
  .host-table td {
    min-width: 0;
    border: 0;
    padding: 0;
  }
  .host-table td::before {
    content: attr(data-label);
    display: block;
    color: var(--muted);
    font-size: 11px;
    margin-bottom: 9px;
  }
  .host-table td:last-child {
    grid-column: 1/-1;
    border-top: 1px solid var(--border);
    padding-top: 10px;
  }
  .host-table td:last-child::before {
    display: none;
  }
  .host-table .capability-grid {
    grid-template-columns: 1fr;
    gap: 8px;
  }
  .host-actions {
    max-width: none;
    justify-content: flex-end;
  }
}
@media (max-width: 650px) {
  .host-management {
    padding: 16px 14px;
  }
  .host-table tr {
    padding: 16px;
    gap: 18px 12px;
  }
  .host-table .host-time {
    white-space: normal;
    line-height: 1.8;
  }

  .host-filters {
    flex-basis: 100%;
    min-width: 0;
  }
  .host-filters input {
    flex: 1;
    width: 0;
  }
  .host-filters select {
    width: 132px;
  }
  .host-toolbar-actions {
    margin-left: auto;
  }
  .capability-details {
    grid-template-columns: 1fr;
  }
  .capability-details article:nth-child(odd) {
    border-right: 0;
  }
  .capability-details article:not(:last-child) {
    border-bottom: 1px solid var(--border);
  }
}
</style>
<style>
.host-detail-drawer.el-drawer {
  min-width: 0;
  min-inline-size: 0;
  max-width: 100vw;
}
.host-detail-drawer .el-drawer__body {
  min-width: 0;
  max-width: 100%;
}
</style>
