<script setup lang="ts">
import { computed, nextTick, onMounted, onUnmounted, ref, watch } from "vue";
import {
  ElDrawer,
  ElTabs,
  ElTabPane,
  ElMessage,
  ElMessageBox,
} from "element-plus";
import { request } from "./api";
import HostConnectionDialog from "./HostConnectionDialog.vue";
import HostPreparationPanel from "./HostPreparationPanel.vue";
import {
  hostCapabilityLabels,
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
}>();
const hosts = ref<Host[]>([]);
const credentials = ref<SSHCredential[]>([]);
const loading = ref(false);
const actionHost = ref("");
const query = ref("");
const stateFilter = ref("");
const dialog = ref(false);
const editingHost = ref<Host>();
const detailId = ref("");
const detailTab = ref("connection");
watch(detailId, () => {
  detailTab.value = "connection";
});
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
        detailTab.value = "preparation";
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
  const status = host.capabilities?.[key]?.status;
  const prefix = hostObservationExpired(host, now.value) ? "上次" : "";
  return status === "AVAILABLE"
    ? prefix + "可用"
    : status === "UNAVAILABLE"
      ? prefix + "不可用"
      : "未确认";
};
const capabilityTone = (host: Host, key: HostCapabilityKey) => {
  if (hostObservationExpired(host, now.value)) return "capability-unknown";
  const status = host.capabilities?.[key]?.status;
  return status === "AVAILABLE"
    ? "capability-available"
    : status === "UNAVAILABLE"
      ? "capability-unavailable"
      : "capability-unknown";
};

async function load() {
  if (loading.value) return;
  loading.value = true;
  const revision = dataRevision;
  try {
    const [items, keys] = await Promise.all([
      request<Host[]>("/hosts"),
      request<SSHCredential[]>("/credentials"),
    ]);
    if (disposed) return;
    if (revision === dataRevision) hosts.value = items;
    credentials.value = keys;
    now.value = Date.now();
  } catch (error) {
    emit("error", error);
  } finally {
    loading.value = false;
  }
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
  updateHost(host);
  await load();
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
    <div class="host-toolbar">
      <div class="host-filters">
        <input
          v-model="query"
          type="search"
          aria-label="搜索主机名称或地址"
          placeholder="搜索名称、地址或用户"
        />
        <select v-model="stateFilter" aria-label="筛选主机状态">
          <option value="">全部状态</option>
          <option value="CONNECTED">已连接</option>
          <option value="ATTENTION">待检查 / 异常</option>
          <option value="MAINTENANCE">维护中</option>
        </select>
      </div>
      <div class="host-toolbar-actions">
        <button
          class="secondary"
          :disabled="loading || !!actionHost"
          @click="load"
        >
          刷新
        </button>
        <button
          class="primary"
          :disabled="!!actionHost"
          @click="openConnection()"
        >
          新增主机
        </button>
      </div>
    </div>
    <div v-if="query || stateFilter" class="host-count" aria-live="polite">
      找到 {{ visibleHosts.length }} / {{ hosts.length }} 台主机
      <span v-if="loading">· 加载中…</span>
    </div>
    <div v-if="!hosts.length" class="empty host-empty">
      <strong>{{ loading ? "正在加载主机" : "尚未添加主机" }}</strong>
    </div>
    <div v-else-if="!visibleHosts.length" class="empty">没有匹配的主机</div>
    <div v-else class="table-scroll host-table">
      <table>
        <thead>
          <tr>
            <th>主机状态</th>
            <th>主机 / 连接</th>
            <th>能力</th>
            <th>最近检查</th>
            <th>操作</th>
          </tr>
        </thead>
        <tbody>
          <tr v-for="host in visibleHosts" :key="host.id">
            <td data-label="主机状态">
              <div class="host-status">
                <span class="state-tag" :class="connectionState(host).tone">
                  {{ connectionState(host).label }}
                </span>
                <span v-if="host.maintenance" class="maintenance-tag">
                  维护中
                </span>
              </div>
            </td>
            <td data-label="主机 / 连接">
              <strong class="host-name">{{ host.name }}</strong>
              <span class="host-address">
                {{
                  host.kind === "LOCAL"
                    ? "控制器本机"
                    : `${host.address}:${host.sshPort}`
                }}
              </span>
              <small v-if="host.kind === 'SSH'">
                {{ host.sshUser }} · {{ credentialName(host) }}
              </small>
            </td>
            <td data-label="能力">
              <div class="capability-grid">
                <span
                  v-for="key in capabilityKeys"
                  :key="key"
                  :class="capabilityTone(host, key)"
                >
                  <i aria-hidden="true"></i>
                  {{
                    host.kind === "LOCAL" && key === "reachability"
                      ? "本机连通"
                      : hostCapabilityLabels[key]
                  }}
                  <b>{{ capabilityLabel(host, key) }}</b>
                </span>
              </div>
            </td>
            <td data-label="最近检查">
              <span class="host-time">{{ hostTime(host.observedAt) }}</span>
              <small v-if="host.capabilities?.expiresAt">
                {{
                  hostObservationExpired(host, now)
                    ? "上次检查 · 待更新"
                    : "观测有效"
                }}
              </small>
              <small v-else>尚无观测</small>
            </td>
            <td data-label="操作">
              <div class="host-actions">
                <button
                  class="quiet"
                  :disabled="!!actionHost"
                  @click="checkHost(host)"
                >
                  {{ checking(host) ? "检查中…" : "检查能力" }}
                </button>
                <button
                  class="quiet"
                  :disabled="!!actionHost"
                  @click="openConnection(host)"
                >
                  编辑连接
                </button>
                <button class="quiet" @click="detailId = host.id">详情</button>
              </div>
            </td>
          </tr>
        </tbody>
      </table>
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
    :model-value="!!detail"
    :title="detail?.name || '主机详情'"
    size="min(760px, 100vw)"
    @close="detailId = ''"
  >
    <div v-if="detail" class="host-detail">
      <div class="detail-status">
        <span class="state-tag" :class="connectionState(detail).tone">
          {{ connectionState(detail).label }}
        </span>
        <span v-if="detail.maintenance" class="maintenance-tag">维护中</span>
        <span class="spacer"></span>
        <button
          class="secondary"
          :disabled="!!actionHost"
          @click="checkHost(detail)"
        >
          {{ checking(detail) ? "检查中…" : "重新检查" }}
        </button>
      </div>
      <ElTabs v-model="detailTab">
        <ElTabPane label="连接信息" name="connection">
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
            <dt>检查时间</dt>
            <dd>{{ hostTime(detail.observedAt) }}</dd>
            <dt>运行环境</dt>
            <dd>{{ detail.capabilities?.platform || "未检查" }}</dd>
            <dt>项目目录</dt>
            <dd>
              <code>{{ detail.installRoot }}</code>
            </dd>
            <dt>有效至</dt>
            <dd>{{ hostTime(detail.capabilities?.expiresAt) }}</dd>
          </dl>
        </ElTabPane>
        <ElTabPane label="能力检查" name="capabilities">
          <div class="capability-details">
            <article v-for="key in capabilityKeys" :key="key">
              <header>
                <strong>
                  {{
                    detail.kind === "LOCAL" && key === "reachability"
                      ? "本机连通"
                      : hostCapabilityLabels[key]
                  }}
                </strong>
                <span :class="capabilityTone(detail, key)">
                  {{ capabilityLabel(detail, key) }}
                </span>
              </header>
              <p>{{ detail.capabilities?.[key]?.message || "尚未检查" }}</p>
              <details
                v-if="detail.capabilities?.[key]?.code"
                class="capability-evidence"
              >
                <summary>检查依据</summary>
                <span>
                  来源：{{
                    observationSources[
                      detail.capabilities?.[key]?.source || "NONE"
                    ] || detail.capabilities?.[key]?.source
                  }}
                </span>
                <code>{{ detail.capabilities?.[key]?.code }}</code>
                <time>
                  {{ hostTime(detail.capabilities?.[key]?.observedAt) }}
                </time>
              </details>
            </article>
          </div>
        </ElTabPane>
        <ElTabPane label="安装准备" name="preparation"
          ><HostPreparationPanel
            v-if="detailTab === 'preparation'"
            :host="detail"
            @checked="load"
        /></ElTabPane>
        <ElTabPane label="项目目录" name="directory">
          <div class="host-identifiers">
            <dl class="host-facts">
              <dt>应用与版本</dt>
              <dd>
                <code>{{ detail.installRoot }}/instances/</code>
              </dd>
              <dt>环境记录</dt>
              <dd>
                <code>{{ detail.installRoot }}/plugins/</code>
              </dd>
              <dt>离线材料</dt>
              <dd>
                <code>{{ detail.installRoot }}/packages/</code>
              </dd>
              <dt>作业回执</dt>
              <dd>
                <code>{{ detail.installRoot }}/jobs/</code>
              </dd>
            </dl>
          </div>
        </ElTabPane>
        <ElTabPane label="登记信息" name="registration">
          <dl class="host-facts">
            <dt>主机标识</dt>
            <dd>
              <code>{{ detail.id }}</code>
            </dd>
            <dt>记录版本</dt>
            <dd>{{ detail.version }}</dd>
            <template v-if="detail.kind === 'SSH'">
              <dt>凭据修订</dt>
              <dd>r{{ detail.credentialRevision }}</dd>
            </template>
          </dl>
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
.host-name {
  display: block;
  margin-bottom: 7px;
  font-size: 14px;
  font-weight: 600;
  overflow-wrap: anywhere;
}
.host-address {
  display: block;
  color: #435269;
  font: 12px/1.5 var(--mono);
}
.host-table td {
  white-space: normal;
}
.host-table td small {
  display: block;
  margin-top: 6px;
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
  gap: 4px;
  margin-left: -8px;
}
.host-actions .quiet {
  margin: 0;
  white-space: nowrap;
}
.host-detail {
  display: grid;
  grid-template-columns: minmax(0, 1fr);
  gap: 22px;
}
.detail-status {
  display: flex;
  align-items: center;
  gap: 12px;
  flex-wrap: wrap;
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
