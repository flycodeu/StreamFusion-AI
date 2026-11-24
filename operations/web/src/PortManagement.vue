<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref, watch } from "vue";
import {
  ElAlert,
  ElButton,
  ElEmpty,
  ElInput,
  ElMessage,
  ElOption,
  ElPopover,
  ElSelect,
  ElTabPane,
  ElTable,
  ElTableColumn,
  ElTabs,
  ElTag,
} from "element-plus";
import { request, ApiError } from "./api";
import type { PageResult } from "./jobs";
import ErrorHelpLink from "./ErrorHelpLink.vue";
import { findErrorReference } from "./errorReference";
import { hostTime, type Host } from "./hosts";
import OpsPagination from "./OpsPagination.vue";
import PortRuleDialog from "./PortRuleDialog.vue";
import { usePagination } from "./pagination";
import { requestAllPages } from "./listPages";
import type { SoftwareInstance } from "./software";
import "./port-management.css";

type Rule = {
  id: string;
  action: "allow" | "deny";
  protocol: string;
  source: string;
  start: number;
  end: number;
};
type Snapshot = {
  incomingPolicy?: string;
  outgoingPolicy?: string;
  status: string;
  code?: string;
  hash: string;
  observedAt: string;
  rawRules: string;
  addedRules: string;
  ports: {
    protocol: string;
    address: string;
    port: number;
    process?: string;
  }[];
  dockerPorts: string;
  dockerCode?: string;
  portCode?: string;
  managedRules: Rule[];
  protectedPorts: number[];
  managementSource?: string;
  managementPort?: number;
};
type View = {
  snapshot: Snapshot;
  code: string;
  attemptAt: string;
  traceId: string;
  freshness: "UNOBSERVED" | "FRESH" | "STALE";
};
type Plan = {
  id: string;
  expiresAt: string;
  change: { operation: string; rule: Rule };
  snapshot: Snapshot;
};
type Job = {
  id: string;
  kind: string;
  targetId: string;
  status: string;
  phase: string;
  errorCode: string;
  traceId: string;
  updatedAt: string;
  targetAckPending: boolean;
  result?: { firewallConfirmBy?: string };
};
const props = defineProps<{ fixedHostId?: string; initialHostId?: string }>();
const emit = defineEmits<{ error: [unknown]; preparation: [string] }>();
const hosts = ref<Host[]>([]),
  hostId = ref(props.fixedHostId || props.initialHostId || ""),
  view = ref<View>(),
  jobs = ref<Job[]>([]),
  hostSoftware = ref<SoftwareInstance[]>([]);
const host = computed(() =>
  hosts.value.find((item) => item.id === hostId.value),
);
const snapshot = computed(() => view.value?.snapshot);
const busy = ref(false),
  loading = ref(false),
  tab = ref("ports"),
  search = ref(""),
  dialog = ref(false),
  plan = ref<Plan>();
const operation = ref("ADD"),
  rule = ref<Rule>({
    id: "",
    action: "allow",
    protocol: "tcp",
    source: "0.0.0.0/0",
    start: 8080,
    end: 8080,
  });
const sourceMode = ref("any4"),
  sourceText = ref(""),
  range = ref(false),
  error = ref(""),
  receipt = ref(""),
  submitKey = ref(""),
  submissionAttempted = ref(false);
const jobLogs = ref(""),
  now = ref(Date.now());
const errorDetails = ref<{ code: string; timestamp: string; traceId: string }>();
const historyPage = ref(1), historyPageSize = ref(10), historyCursors = ref([""]), nextCursor = ref(""), historyLoading = ref(false);
let disposed = false,
  generation = 0,
  historyGeneration = 0,
  logGeneration = 0,
  poll: ReturnType<typeof setTimeout> | undefined,
  clock: ReturnType<typeof setInterval> | undefined;
const states: Record<string, string> = {
  ACTIVE: "已启用",
  INACTIVE: "已停用",
  UNAVAILABLE: "未确认",
  UNOBSERVED: "未检测",
  QUEUED: "排队中",
  RUNNING: "执行中",
  UNKNOWN: "待确认",
  SUCCEEDED: "成功",
  FAILED: "失败",
};
const actions: Record<string, string> = {
  ADD: "新增访问规则",
  DELETE: "撤销访问规则",
  ENABLE: "启用防火墙",
  DISABLE: "停用防火墙",
};
const filtered = computed(() =>
  (snapshot.value?.ports || []).filter((p) =>
    `${p.port} ${p.protocol} ${p.address} ${p.process || ""}`
      .toLowerCase()
      .includes(search.value.trim().toLowerCase()),
  ),
);
const { page: portPage, pageSize: portPageSize, pageItems: portRows, total: portTotal } = usePagination(() => filtered.value, [hostId, search]);
const { page: rulePage, pageSize: rulePageSize, pageItems: ruleRows, total: ruleTotal } = usePagination(() => snapshot.value?.managedRules || [], [hostId]);
const currentJobs = computed(() =>
  jobs.value.filter(
    (j) => j.kind === "FIREWALL_CHANGE" && j.targetId === hostId.value,
  ),
);
const ready = computed(
  () =>
    !!snapshot.value?.hash &&
    !view.value?.code &&
    view.value?.freshness === "FRESH" &&
    !host.value?.maintenance,
);
const portOwners = computed(() => {
  const map = new Map<number, string[]>();
  const add = (port: number | undefined, name: string) => {
    if (!port || !Number.isInteger(port)) return;
    const list = map.get(port) || [];
    if (!list.includes(name)) list.push(name);
    map.set(port, list);
  };
  for (const item of hostSoftware.value) {
    if (item.hostId !== hostId.value || item.managementMode === "RELEASED" || item.actualState === "REMOVED") {
      continue;
    }
    const cfg = item.installation?.config;
    if (cfg?.hostPort) add(cfg.hostPort, item.name);
    for (const val of Object.values(cfg?.ports || {})) {
      add(val, item.name);
    }
    for (const raw of item.observation?.listeningPorts || []) {
      const m = raw.match(/(\d+)$/);
      if (m) add(Number(m[1]), item.name);
    }
  }
  return map;
});
function portAllowed(port: number, protocol: string): boolean {
  const proto = protocol.toLowerCase();
  return (snapshot.value?.managedRules || []).some(
    (r) =>
      r.action === "allow" &&
      (r.protocol === proto || r.protocol === "any") &&
      port >= r.start &&
      port <= r.end,
  );
}
function portNoticeTitle(code?: string): string {
  if (!code) return "";
  if (code === "FIREWALL_NOT_OBSERVED") return "尚未获取主机监听端口与防火墙状态";
  if (code === "FIREWALL_EXECUTOR_REQUIRED") return "防火墙规则需要受管执行器";
  if (code === "UFW_NOT_INSTALLED") return "当前主机未安装 UFW 防火墙";
  return findErrorReference(code)?.name || "端口检测未完全完成";
}
function portNoticeSummary(code?: string): string {
  if (!code) return "";
  if (code === "FIREWALL_NOT_OBSERVED") return "点击右上角「检测端口」即可采集最新端口监听与访问规则。";
  if (code === "FIREWALL_EXECUTOR_REQUIRED") return "完成「接入与能力」组件初始化后可管理防火墙规则；下方仍可查看只读监听端口。";
  if (code === "UFW_NOT_INSTALLED") return "若主机使用云安全组或其它防火墙，请结合软件端口归属直接确认入站放行策略。";
  if (code === "PORT_OBSERVATION_DENIED") return "受管执行器读取监听端口权限受限，请核对主机接入权限或重新执行接入准备后重试检测。";
  return findErrorReference(code)?.meaning || "暂时无法读取完整状态，可尝试重新检测或查看排查详情。";
}
const expired = computed(
  () => !plan.value || Date.parse(plan.value.expiresAt) <= now.value,
);
function confirmationExpired(job: Job) {
  const deadline = Date.parse(job.result?.firewallConfirmBy || "");
  return Number.isFinite(deadline) && deadline <= now.value;
}
async function act(fn: () => Promise<void>) {
  if (busy.value) return;
  const rev = generation;
  busy.value = true;
  error.value = "";
  errorDetails.value = undefined;
  try {
    await fn();
  } catch (e) {
    if (disposed || rev !== generation) return;
    error.value =
      e instanceof Error ? e.message : "操作未确认";
    if (e instanceof ApiError) errorDetails.value = { code: e.code, timestamp: e.timestamp, traceId: e.traceId };
    emit("error", e);
  } finally {
    if (!disposed && rev === generation) busy.value = false;
  }
}
async function loadHistory(page = historyPage.value, size = historyPageSize.value) {
  if (!hostId.value || historyLoading.value || page < 1 || page > historyPage.value + 1) return;
  const cursor = page === 1 ? "" : page > historyPage.value ? nextCursor.value : historyCursors.value[page - 1];
  if (page > 1 && !cursor) return;
  const selected = hostId.value, rev = generation, active = ++historyGeneration;
  historyLoading.value = true;
  const params = new URLSearchParams({ limit: String(size), kind: "FIREWALL_CHANGE", hostId: selected });
  if (cursor) params.set("before", cursor);
  try {
    const result = await request<PageResult<Job>>(`/jobs?${params}`);
    if (disposed || rev !== generation || active !== historyGeneration || selected !== hostId.value) return;
    if (page !== historyPage.value || size !== historyPageSize.value) { logGeneration++; jobLogs.value = ""; }
    jobs.value = result.items;
    nextCursor.value = result.nextCursor;
    historyCursors.value = [...historyCursors.value.slice(0, page - 1), cursor || ""];
    historyPage.value = page;
    historyPageSize.value = size;
  } finally {
    if (!disposed && rev === generation && active === historyGeneration) historyLoading.value = false;
  }
}
async function changeHistoryPage(page: number) {
  if (page === historyPage.value) return;
  await act(() => loadHistory(page));
}
async function changeHistorySize(size: number) {
  if (size !== historyPageSize.value) await act(() => loadHistory(1, size));
}
function resetHistory() {
  historyGeneration++;
  historyLoading.value = false;
  jobs.value = [];
  historyPage.value = 1;
  historyCursors.value = [""];
  nextCursor.value = "";
  logGeneration++;
  jobLogs.value = "";
}
async function loadHostSoftware() {
  if (!requestAllPages) return;
  try {
    const items = await requestAllPages<SoftwareInstance>("/software-instances");
    if (!disposed) hostSoftware.value = items;
  } catch {
    // Owning software tags are best-effort enrichment.
  }
}
async function refresh() {
  if (loading.value) return;
  loading.value = true;
  if (props.fixedHostId && hostId.value !== props.fixedHostId) {
    hostId.value = props.fixedHostId;
  }
  const selected = hostId.value,
    rev = generation;
  try {
    const [all] = await Promise.all([
      request<Host[]>("/hosts"),
      selected ? loadHistory() : Promise.resolve(),
    ]);
    if (disposed || rev !== generation) return;
    hosts.value = all;
    if (props.fixedHostId) {
      if (hostId.value !== props.fixedHostId) {
        hostId.value = props.fixedHostId;
        return;
      }
    } else if ((!hostId.value || !all.some((h) => h.id === hostId.value)) && all.length) {
      hostId.value =
        (props.initialHostId && all.some((h) => h.id === props.initialHostId)
          ? props.initialHostId
          : all[0]!.id);
      return;
    }
    if (selected) {
      const result = await request<View>(`/hosts/${selected}/ports`);
      if (!disposed && rev === generation && selected === hostId.value)
        view.value = result;
    }
  } finally {
    if (!disposed && rev === generation) loading.value = false;
  }
}
function schedule() {
  if (poll) clearTimeout(poll);
  if (disposed) return;
  poll = setTimeout(async () => {
    const rev = generation;
    try {
      if (!document.hidden) await refresh();
    } catch (e) {
      if (!disposed && rev === generation) emit("error", e);
    } finally {
      schedule();
    }
  }, 4000);
}
async function detect() {
  if (!host.value) return;
  const selected = hostId.value,
    rev = generation;
  const result = await request<View>(
    `/hosts/${selected}/ports-observe`,
    "POST",
    { expectedVersion: host.value.version },
  );
  if (!disposed && rev === generation && selected === hostId.value) view.value = result;
}
function open(op: string, selected?: Rule) {
  operation.value = op;
  dialog.value = true;
  plan.value = undefined;
  receipt.value = "";
  submitKey.value = crypto.randomUUID();
  submissionAttempted.value = false;
  error.value = "";
  errorDetails.value = undefined;
  rule.value = selected
    ? { ...selected }
    : {
        id: "",
        action: "allow",
        protocol: "tcp",
        source: "0.0.0.0/0",
        start: 8080,
        end: 8080,
      };
  sourceMode.value = "any4";
  sourceText.value = "";
  range.value = !!selected && selected.start !== selected.end;
}
async function preflight() {
  if (!host.value || !snapshot.value) return;
  const selected = hostId.value, rev = generation;
  const r = { ...rule.value };
  if (operation.value === "ADD") {
    if (!range.value) r.end = r.start;
    r.source =
      sourceMode.value === "any4"
        ? "0.0.0.0/0"
        : sourceMode.value === "any6"
          ? "::/0"
          : sourceText.value.trim();
    if (!r.source.includes("/"))
      r.source += r.source.includes(":") ? "/128" : "/32";
  }
  const result = await request<Plan>(
    `/hosts/${selected}/firewall-plans`,
    "POST",
    {
      expectedVersion: host.value.version,
      snapshotHash: snapshot.value.hash,
      operation: operation.value,
      rule: r,
    },
  );
  if (!disposed && rev === generation && selected === hostId.value) plan.value = result;
}
async function submit() {
  if (!plan.value || (expired.value && !submissionAttempted.value)) return;
  const selected = hostId.value, rev = generation;
  // The server may have accepted a request whose response was lost. Keep the
  // same plan/key so retry can find that job even after the plan expires.
  submissionAttempted.value = true;
  const accepted = await request<{ jobId: string }>(
    `/firewall-plans/${plan.value.id}/submit`,
    "POST",
    { confirmed: true, idempotencyKey: submitKey.value },
  );
  if (disposed || rev !== generation || selected !== hostId.value) return;
  receipt.value = accepted.jobId;
  dialog.value = false;
  tab.value = "jobs";
  resetHistory();
  await loadHistory();
  await refresh();
  if (!disposed && rev === generation && selected === hostId.value) ElMessage.success("已提交，执行结果见操作记录");
}
async function jobAction(j: Job, action: string) {
  const rev = generation;
  await request(`/jobs/${j.id}/${action}`, "POST", {});
  if (!disposed && rev === generation) await refresh();
}
async function logs(j: Job) {
  const rev = generation, active = ++logGeneration;
  const result = await request<{ text: string }>(`/jobs/${j.id}/logs`);
  if (!disposed && rev === generation && active === logGeneration) jobLogs.value = result.text;
}
function guardSSH() {
  if (!snapshot.value?.managementSource || !snapshot.value.managementPort)
    return;
  open("ADD");
  rule.value.start = rule.value.end = snapshot.value.managementPort;
  sourceMode.value = "custom";
  sourceText.value = snapshot.value.managementSource;
}
function quickAllow(port: number, protocol: string) {
  open("ADD");
  const normalizedProto = protocol.toLowerCase();
  rule.value.action = "allow";
  rule.value.protocol = normalizedProto === "udp" ? "udp" : "tcp";
  rule.value.start = port;
  rule.value.end = port;
  range.value = false;
  sourceMode.value = "any4";
  sourceText.value = "";
}
watch(() => props.fixedHostId, (val) => {
  if (val && hostId.value !== val) hostId.value = val;
});
watch(() => props.initialHostId, (val) => {
  if (!props.fixedHostId && val && hosts.value.some((h) => h.id === val)) {
    hostId.value = val;
  }
});
watch(hostId, () => {
  generation++;
  busy.value = false;
  loading.value = false;
  resetHistory();
  search.value = "";
  error.value = "";
  errorDetails.value = undefined;
  view.value = undefined;
  plan.value = undefined;
  dialog.value = false;
  void act(refresh);
});
onMounted(() => {
  void act(refresh);
  void loadHostSoftware();
  schedule();
  clock = setInterval(() => (now.value = Date.now()), 1000);
});
onUnmounted(() => {
  disposed = true;
  generation++;
  if (poll) clearTimeout(poll);
  if (clock) clearInterval(clock);
});
</script>

<template>
  <section class="content port-page">
    <header class="port-toolbar">
      <div v-if="!fixedHostId" class="ops-el-toolbar-group">
        <span class="port-host-label">管理主机</span>
        <ElSelect
          v-model="hostId"
          placeholder="选择主机"
          :disabled="busy"
          style="width: 280px"
        >
          <ElOption
            v-for="h in hosts"
            :key="h.id"
            :label="`${h.name} · ${h.address || '本机'}`"
            :value="h.id"
          />
        </ElSelect>
      </div>
      <div v-else class="fixed-host-caption">
        <strong>端口与防火墙（{{ host?.name || fixedHostId }}）</strong>
      </div>
      <div class="port-actions">
        <ElButton
          :loading="busy"
          :disabled="!host"
          @click="act(detect)"
        >
          {{ busy ? "检测中…" : "检测端口" }}
        </ElButton>
        <ElButton
          type="primary"
          :disabled="busy || !ready"
          @click="open('ADD')"
        >
          开放 / 禁止
        </ElButton>
      </div>
    </header>
    <div v-if="!hosts.length" class="empty">
      <ElEmpty description="请先添加服务器节点" />
    </div>
    <ElAlert
      v-if="error && !dialog"
      type="error"
      show-icon
      :closable="false"
    >
      <div class="ops-alert-row">
        <div class="ops-alert-copy">
          <span class="ops-alert-title">{{ error }}</span>
        </div>
        <div v-if="errorDetails" class="ops-alert-actions">
          <ElPopover trigger="click" :width="380" placement="bottom-end">
            <template #reference>
              <ElButton link type="danger" size="small">排查详情</ElButton>
            </template>
            <div class="ops-diag-popover">
              <div class="ops-diag-popover-head">
                <span>异常排查信息</span>
                <ErrorHelpLink :code="errorDetails.code" />
              </div>
              <dl class="ops-diag-dl">
                <dt>错误码</dt>
                <dd><code>{{ errorDetails.code }}</code></dd>
                <dt>时间</dt>
                <dd>{{ hostTime(errorDetails.timestamp) }}</dd>
                <dt>请求标识</dt>
                <dd><code>{{ errorDetails.traceId }}</code></dd>
              </dl>
            </div>
          </ElPopover>
        </div>
      </div>
    </ElAlert>
    <template v-if="hosts.length">
      <div class="port-summary">
        <div>
          <small>UFW 防火墙</small>
          <strong :class="{ enabled: snapshot?.status === 'ACTIVE' }">
            {{ states[snapshot?.status || "UNOBSERVED"] }}
          </strong>
        </div>
        <div>
          <small>监听端口</small>
          <strong>{{ snapshot?.ports?.length ?? "—" }}</strong>
        </div>
        <div>
          <small>受管规则</small>
          <strong>{{ snapshot?.managedRules?.length ?? "—" }}</strong>
        </div>
        <div>
          <small>最近确认</small>
          <span>
            {{ hostTime(snapshot?.observedAt) }}
            <em v-if="view?.freshness === 'STALE'">待更新</em>
          </span>
        </div>
      </div>
      <ElAlert
        v-if="view?.code"
        :type="view.code === 'FIREWALL_NOT_OBSERVED' ? 'info' : 'warning'"
        show-icon
        :closable="false"
      >
        <div class="ops-alert-row">
          <div class="ops-alert-copy">
            <span class="ops-alert-title">{{ portNoticeTitle(view.code) }}</span>
            <span class="ops-alert-desc">{{ portNoticeSummary(view.code) }}</span>
          </div>
          <div class="ops-alert-actions">
            <ElButton
              link
              type="primary"
              size="small"
              :disabled="busy || !host"
              @click="act(detect)"
            >
              重新检测
            </ElButton>
            <ElButton
              v-if="view.code.includes('EXECUTOR') || view.code === 'PORT_OBSERVATION_DENIED'"
              link
              type="primary"
              size="small"
              @click="emit('preparation', hostId)"
            >
              前往接入准备
            </ElButton>
            <ElPopover trigger="click" :width="380" placement="bottom-end">
              <template #reference>
                <ElButton link size="small">排查详情</ElButton>
              </template>
              <div class="ops-diag-popover">
                <div class="ops-diag-popover-head">
                  <span>检测诊断信息</span>
                  <ErrorHelpLink :code="view.code" />
                </div>
                <dl class="ops-diag-dl">
                  <dt>状态码</dt>
                  <dd><code>{{ view.code }}</code></dd>
                  <dt>检测时间</dt>
                  <dd>{{ hostTime(view.attemptAt) }}</dd>
                  <template v-if="view.traceId">
                    <dt>请求标识</dt>
                    <dd><code>{{ view.traceId }}</code></dd>
                  </template>
                </dl>
              </div>
            </ElPopover>
          </div>
        </div>
      </ElAlert>
      <div class="port-panel">
        <ElTabs v-model="tab">
          <ElTabPane label="监听端口" name="ports">
            <ElAlert
              v-if="snapshot?.portCode"
              type="warning"
              show-icon
              :closable="false"
              style="margin-bottom: 12px"
            >
              <div class="ops-alert-row">
                <div class="ops-alert-copy">
                  <span class="ops-alert-title">
                    {{ findErrorReference(snapshot.portCode)?.name || "暂时无法完整读取监听端口" }}
                  </span>
                  <span class="ops-alert-desc">
                    {{ findErrorReference(snapshot.portCode)?.meaning || "请核对执行器状态后点击右上角“检测端口”重试。" }}
                  </span>
                </div>
                <div class="ops-alert-actions">
                  <ErrorHelpLink :code="snapshot.portCode" />
                </div>
              </div>
            </ElAlert>
            <div class="port-filter">
              <ElInput
                v-model="search"
                clearable
                placeholder="搜索端口、协议、地址或归属程序"
                style="width: 320px"
              />
            </div>
            <ElTable :data="portRows" border stripe style="width: 100%">
              <ElTableColumn label="端口" width="90">
                <template #default="{ row }">
                  <strong>{{ row.port }}</strong>
                </template>
              </ElTableColumn>
              <ElTableColumn label="协议" width="84">
                <template #default="{ row }">
                  <ElTag size="small" type="info" effect="plain">
                    {{ row.protocol.toUpperCase() }}
                  </ElTag>
                </template>
              </ElTableColumn>
              <ElTableColumn label="监听地址" width="150" show-overflow-tooltip>
                <template #default="{ row }">
                  <code>{{ row.address }}</code>
                </template>
              </ElTableColumn>
              <ElTableColumn label="程序 / 归属软件" min-width="220">
                <template #default="{ row }">
                  <div class="port-process-cell">
                    <span>{{ row.process || "程序未识别" }}</span>
                    <ElTag
                      v-for="owner in portOwners.get(row.port) || []"
                      :key="owner"
                      size="small"
                      effect="light"
                    >
                      {{ owner }}
                    </ElTag>
                  </div>
                </template>
              </ElTableColumn>
              <ElTableColumn label="管理保护" width="110">
                <template #default="{ row }">
                  <ElTag
                    v-if="snapshot?.protectedPorts?.includes(row.port) && row.protocol === 'tcp'"
                    size="small"
                    type="warning"
                  >
                    管理端口
                  </ElTag>
                  <span v-else class="muted">—</span>
                </template>
              </ElTableColumn>
              <ElTableColumn label="快捷操作" width="120" fixed="right">
                <template #default="{ row }">
                  <ElButton
                    v-if="!portAllowed(row.port, row.protocol)"
                    link
                    type="primary"
                    size="small"
                    :disabled="busy || !ready"
                    @click="quickAllow(row.port, row.protocol)"
                  >
                    放行端口
                  </ElButton>
                  <ElTag v-else type="success" size="small">已放行</ElTag>
                </template>
              </ElTableColumn>
              <template #empty>
                <ElEmpty description="暂无已确认监听端口" />
              </template>
            </ElTable>
            <OpsPagination
              v-model:page="portPage"
              v-model:page-size="portPageSize"
              :total="portTotal"
              label="监听端口分页"
            />
            <details v-if="snapshot?.dockerPorts" class="port-docker">
              <summary>Docker 发布端口</summary>
              <pre>{{ snapshot.dockerPorts }}</pre>
            </details>
          </ElTabPane>
          <ElTabPane label="访问规则" name="rules">
            <ElAlert
              v-if="snapshot?.status === 'INACTIVE'"
              type="info"
              show-icon
              :closable="false"
              title="防火墙已停用，保存的访问规则在启用后生效。"
              style="margin-bottom: 12px"
            />
            <div class="port-rule-tools">
              <div class="port-actions">
                <ElButton
                  v-if="snapshot?.managementSource"
                  :disabled="busy || !ready"
                  @click="guardSSH"
                >
                  保护 SSH
                </ElButton>
                <ElButton
                  :disabled="busy || !ready || snapshot?.status === 'ACTIVE'"
                  @click="open('ENABLE')"
                >
                  启用防火墙
                </ElButton>
                <ElButton
                  type="danger"
                  plain
                  :disabled="busy || !ready || snapshot?.status !== 'ACTIVE'"
                  @click="open('DISABLE')"
                >
                  停用防火墙
                </ElButton>
              </div>
            </div>
            <ElTable :data="ruleRows" border stripe style="width: 100%">
              <ElTableColumn label="策略" width="90">
                <template #default="{ row }">
                  <ElTag :type="row.action === 'deny' ? 'danger' : 'success'" size="small">
                    {{ row.action === "allow" ? "允许" : "禁止" }}
                  </ElTag>
                </template>
              </ElTableColumn>
              <ElTableColumn label="端口" width="130">
                <template #default="{ row }">
                  <strong>{{ row.start }}{{ row.end !== row.start ? "–" + row.end : "" }}</strong>
                </template>
              </ElTableColumn>
              <ElTableColumn label="协议" width="90">
                <template #default="{ row }">
                  {{ row.protocol.toUpperCase() }}
                </template>
              </ElTableColumn>
              <ElTableColumn label="来源" min-width="180">
                <template #default="{ row }">
                  <code>{{ row.source }}</code>
                </template>
              </ElTableColumn>
              <ElTableColumn label="操作" width="100" fixed="right">
                <template #default="{ row }">
                  <ElButton
                    link
                    type="danger"
                    size="small"
                    :disabled="busy || !ready"
                    @click="open('DELETE', row as Rule)"
                  >
                    撤销
                  </ElButton>
                </template>
              </ElTableColumn>
              <template #empty>
                <ElEmpty description="暂无 Ops 访问规则" />
              </template>
            </ElTable>
            <OpsPagination
              v-model:page="rulePage"
              v-model:page-size="rulePageSize"
              :total="ruleTotal"
              label="访问规则分页"
            />
            <details class="port-docker">
              <summary>系统规则与默认策略</summary>
              <p>
                默认入站：{{ snapshot?.incomingPolicy || "尚未确认" }} ·
                默认出站：{{ snapshot?.outgoingPolicy || "尚未确认" }}
              </p>
              <pre>{{ snapshot?.rawRules || "尚未确认" }}</pre>
              <pre>{{ snapshot?.addedRules }}</pre>
            </details>
          </ElTabPane>
          <ElTabPane label="操作记录" name="jobs">
            <ElTable :data="currentJobs" border stripe style="width: 100%">
              <ElTableColumn label="状态" width="100">
                <template #default="{ row }">
                  <ElTag
                    size="small"
                    :type="row.status === 'SUCCEEDED' ? 'success' : row.status === 'FAILED' ? 'danger' : 'info'"
                  >
                    {{ states[row.status] || "状态待确认" }}
                  </ElTag>
                </template>
              </ElTableColumn>
              <ElTableColumn label="时间" width="175">
                <template #default="{ row }">
                  {{ hostTime(row.updatedAt) }}
                </template>
              </ElTableColumn>
              <ElTableColumn label="执行结果" min-width="220">
                <template #default="{ row }">
                  <div class="port-job-result">
                    <span>
                      {{
                        row.errorCode
                          ? findErrorReference(row.errorCode)?.name || "执行未完成"
                          : row.phase === "FIREWALL_AWAITING_CONFIRM"
                            ? "等待确认规则"
                            : states[row.status] || "结果待确认"
                      }}
                    </span>
                    <ElPopover trigger="click" :width="360" placement="top">
                      <template #reference>
                        <ElButton link size="small">排查详情</ElButton>
                      </template>
                      <div class="ops-diag-popover">
                        <div class="ops-diag-popover-head">
                          <span>作业诊断</span>
                          <ErrorHelpLink v-if="row.errorCode" :code="row.errorCode" />
                        </div>
                        <dl class="ops-diag-dl">
                          <dt>阶段/代码</dt>
                          <dd><code>{{ row.errorCode || row.phase }}</code></dd>
                          <dt>作业标识</dt>
                          <dd><code>{{ row.id }}</code></dd>
                          <template v-if="row.traceId">
                            <dt>请求标识</dt>
                            <dd><code>{{ row.traceId }}</code></dd>
                          </template>
                        </dl>
                      </div>
                    </ElPopover>
                  </div>
                </template>
              </ElTableColumn>
              <ElTableColumn label="操作" width="210" fixed="right">
                <template #default="{ row }">
                  <div class="port-actions">
                    <ElButton
                      v-if="row.status === 'UNKNOWN' && row.phase === 'FIREWALL_AWAITING_CONFIRM'"
                      type="primary"
                      size="small"
                      :disabled="busy || confirmationExpired(row as Job)"
                      @click="act(() => jobAction(row as Job, 'firewall-confirm'))"
                    >
                      确认保持
                    </ElButton>
                    <ElButton
                      v-if="row.status === 'UNKNOWN' || row.targetAckPending"
                      size="small"
                      :disabled="busy"
                      @click="act(() => jobAction(row as Job, 'reconcile'))"
                    >
                      核对原作业
                    </ElButton>
                    <ElButton
                      v-if="row.status === 'QUEUED'"
                      link
                      size="small"
                      :disabled="busy"
                      @click="act(() => jobAction(row as Job, 'cancel'))"
                    >
                      取消
                    </ElButton>
                    <ElButton link type="primary" size="small" @click="act(() => logs(row as Job))">
                      记录
                    </ElButton>
                  </div>
                  <small v-if="row.result?.firewallConfirmBy" class="muted">
                    确认期限 {{ hostTime(row.result.firewallConfirmBy) }}；逾期将尝试恢复
                  </small>
                </template>
              </ElTableColumn>
              <template #empty>
                <ElEmpty description="暂无防火墙操作" />
              </template>
            </ElTable>
            <OpsPagination
              :page="historyPage"
              :page-size="historyPageSize"
              :has-next="!!nextCursor"
              :disabled="busy || loading || historyLoading"
              label="防火墙操作分页"
              @update:page="changeHistoryPage"
              @update:page-size="changeHistorySize"
            />
            <pre v-if="jobLogs" class="port-log">{{ jobLogs }}</pre>
          </ElTabPane>
        </ElTabs>
      </div>
    </template>
    <PortRuleDialog
      v-model:dialog="dialog"
      v-model:range="range"
      v-model:source-mode="sourceMode"
      v-model:source-text="sourceText"
      :operation="operation"
      :actions="actions"
      :states="states"
      :busy="busy"
      :plan="plan"
      :rule="rule"
      :host="host"
      :submission-attempted="submissionAttempted"
      :expired="expired"
      :error="error"
      :error-details="errorDetails"
      @clear-plan="plan = undefined"
      @preflight="act(preflight)"
      @submit="act(submit)"
    />
  </section>
</template>
