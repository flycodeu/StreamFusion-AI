<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref, watch } from "vue";
import { ElTabs, ElTabPane, ElMessage } from "element-plus";
import { request, ApiError } from "./api";
import type { PageResult } from "./jobs";
import ErrorHelpLink from "./ErrorHelpLink.vue";
import { findErrorReference } from "./errorReference";
import { hostTime, type Host } from "./hosts";
import OpsPagination from "./OpsPagination.vue";
import PortRuleDialog from "./PortRuleDialog.vue";
import { usePagination } from "./pagination";
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
const emit = defineEmits<{ error: [unknown]; preparation: [string] }>();
const hosts = ref<Host[]>([]),
  hostId = ref(""),
  view = ref<View>(),
  jobs = ref<Job[]>([]);
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
async function refresh() {
  if (loading.value) return;
  loading.value = true;
  const selected = hostId.value,
    rev = generation;
  try {
    const [all] = await Promise.all([
      request<Host[]>("/hosts"),
      selected ? loadHistory() : Promise.resolve(),
    ]);
    if (disposed || rev !== generation) return;
    hosts.value = all;
    if (!hostId.value && all.length) {
      hostId.value = all[0]!.id;
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
      <label
        ><span>管理主机</span><select
          v-model="hostId"
          aria-label="端口管理主机"
          :disabled="busy"
        >
          <option v-for="h in hosts" :key="h.id" :value="h.id">
            {{ h.name }} · {{ h.address || "本机" }}
          </option>
        </select></label
      >
      <div class="port-actions">
        <button
          class="secondary"
          :disabled="busy || !host"
          @click="act(detect)"
        >
          {{ busy ? "检测中…" : "检测端口" }}</button
        ><button
          class="primary"
          :disabled="busy || !ready"
          @click="open('ADD')"
        >
          开放 / 禁止
        </button>
      </div>
    </header>
    <div v-if="!hosts.length" class="empty">请先添加主机</div>
    <div v-if="error && !dialog" role="alert" class="port-error">
      {{ error }}
      <details v-if="errorDetails"><summary>诊断信息</summary><ErrorHelpLink :code="errorDetails.code" /><code>{{ errorDetails.code }} · {{ hostTime(errorDetails.timestamp) }}</code><code>{{ errorDetails.traceId }}</code></details>
    </div>
    <template v-if="hosts.length">
      <div class="port-summary">
        <div>
          <small>UFW 防火墙</small
          ><strong :class="{ enabled: snapshot?.status === 'ACTIVE' }">{{
            states[snapshot?.status || "UNOBSERVED"]
          }}</strong>
        </div>
        <div>
          <small>监听端口</small
          ><strong>{{ snapshot?.ports?.length ?? "—" }}</strong>
        </div>
        <div>
          <small>受管规则</small
          ><strong>{{ snapshot?.managedRules?.length ?? "—" }}</strong>
        </div>
        <div>
          <small>最近确认</small
          ><span
            >{{ hostTime(snapshot?.observedAt) }}
            <em v-if="view?.freshness === 'STALE'">待更新</em></span
          >
        </div>
      </div>
      <div v-if="view?.code" class="port-notice" role="status">
        <span>{{
          view.code === "FIREWALL_NOT_OBSERVED"
            ? "点击“检测端口”获取主机状态"
            : view.code === "FIREWALL_EXECUTOR_REQUIRED"
              ? "防火墙读取需要当前受管执行器；仍可查看可读的监听端口"
              : view.code === "UFW_NOT_INSTALLED"
                ? "目标未安装 UFW"
                : findErrorReference(view.code)?.name || '检测未完成，请查看技术详情'
        }}</span
        ><button
          v-if="view.code.includes('EXECUTOR')"
          class="quiet"
          @click="emit('preparation', hostId)"
        >
          安装准备</button
        ><small>{{ hostTime(view.attemptAt) }}</small>
        <details>
          <summary>技术详情</summary>
          <code>{{ view.code }}</code>
          <ErrorHelpLink :code="view.code" />
          <code v-if="view.traceId">traceId={{ view.traceId }}</code>
        </details>
      </div>
      <div class="port-panel">
        <ElTabs v-model="tab">
          <ElTabPane label="监听端口" name="ports">
            <p v-if="snapshot?.portCode" class="port-notice">
              {{ findErrorReference(snapshot.portCode)?.name || "暂时无法读取监听端口" }}
              <ErrorHelpLink :code="snapshot.portCode" />
            </p>
            <div class="port-filter">
              <input
                v-model="search"
                type="search"
                aria-label="搜索端口"
                placeholder="端口、协议、地址或程序"
              />
            </div>
            <div class="port-table">
              <table>
                <thead>
                  <tr>
                    <th>端口</th>
                    <th>协议</th>
                    <th>监听地址</th>
                    <th>程序</th>
                    <th>管理保护</th>
                  </tr>
                </thead>
                <tbody>
                  <tr v-for="(p, i) in portRows" :key="i">
                    <td>
                      <strong>{{ p.port }}</strong>
                    </td>
                    <td>{{ p.protocol.toUpperCase() }}</td>
                    <td>
                      <code>{{ p.address }}</code>
                    </td>
                    <td>{{ p.process || "程序不可见" }}</td>
                    <td>
                      {{
                        snapshot?.protectedPorts?.includes(p.port) &&
                        p.protocol === "tcp"
                          ? "管理端口"
                          : "—"
                      }}
                    </td>
                  </tr>
                </tbody>
              </table>
              <div v-if="!filtered.length" class="empty">
                暂无已确认监听端口
              </div>
            </div>
            <OpsPagination v-model:page="portPage" v-model:page-size="portPageSize" :total="portTotal" label="监听端口分页" />
            <details v-if="snapshot?.dockerPorts" class="port-docker">
              <summary>Docker 发布端口</summary>
              <pre>{{ snapshot.dockerPorts }}</pre>
            </details>
          </ElTabPane>
          <ElTabPane label="访问规则" name="rules">
            <p v-if="snapshot?.status === 'INACTIVE'" class="port-notice">
              防火墙已停用，保存的访问规则在启用后生效。
            </p>
            <div class="port-rule-tools">
              <div class="port-actions">
                <button
                  v-if="snapshot?.managementSource"
                  class="secondary"
                  :disabled="busy || !ready"
                  @click="guardSSH"
                >
                  保护 SSH</button
                ><button
                  class="secondary"
                  :disabled="busy || !ready || snapshot?.status === 'ACTIVE'"
                  @click="open('ENABLE')"
                >
                  启用防火墙</button
                ><button
                  class="secondary danger"
                  :disabled="busy || !ready || snapshot?.status !== 'ACTIVE'"
                  @click="open('DISABLE')"
                >
                  停用防火墙
                </button>
              </div>
            </div>
            <div class="port-table">
              <table>
                <thead>
                  <tr>
                    <th>策略</th>
                    <th>端口</th>
                    <th>协议</th>
                    <th>来源</th>
                    <th>操作</th>
                  </tr>
                </thead>
                <tbody>
                  <tr v-for="r in ruleRows" :key="r.id">
                    <td>
                      <span :class="r.action === 'deny' ? 'deny' : 'enabled'">{{
                        r.action === "allow" ? "允许" : "禁止"
                      }}</span>
                    </td>
                    <td>
                      {{ r.start }}{{ r.end !== r.start ? "–" + r.end : "" }}
                    </td>
                    <td>{{ r.protocol.toUpperCase() }}</td>
                    <td>
                      <code>{{ r.source }}</code>
                    </td>
                    <td>
                      <button
                        class="quiet danger"
                        :disabled="busy || !ready"
                        @click="open('DELETE', r)"
                      >
                        撤销
                      </button>
                    </td>
                  </tr>
                </tbody>
              </table>
              <div v-if="!snapshot?.managedRules?.length" class="empty">
                暂无 Ops 访问规则
              </div>
            </div>
            <OpsPagination v-model:page="rulePage" v-model:page-size="rulePageSize" :total="ruleTotal" label="访问规则分页" />
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
            <div class="port-table">
              <table>
                <thead>
                  <tr>
                    <th>状态</th>
                    <th>时间</th>
                    <th>结果</th>
                    <th>操作</th>
                  </tr>
                </thead>
                <tbody>
                  <tr v-for="j in currentJobs" :key="j.id">
                    <td>{{ states[j.status] || '状态待确认' }}</td>
                    <td>{{ hostTime(j.updatedAt) }}</td>
                    <td>
                      <details><summary>{{ j.errorCode ? (findErrorReference(j.errorCode)?.name || '执行未完成') : j.phase === 'FIREWALL_AWAITING_CONFIRM' ? '等待确认规则' : (states[j.status] || '结果待确认') }}</summary>
                        <ErrorHelpLink v-if="j.errorCode" :code="j.errorCode" />
                        <code>{{ j.errorCode || j.phase }}</code>
                        <code>作业 {{ j.id }}</code>
                        <code>traceId={{ j.traceId }}</code>
                      </details>
                    </td>
                    <td>
                      <div class="port-actions">
                        <button
                          v-if="
                            j.status === 'UNKNOWN' &&
                            j.phase === 'FIREWALL_AWAITING_CONFIRM'
                          "
                          class="primary"
                          :disabled="busy || confirmationExpired(j)"
                          @click="act(() => jobAction(j, 'firewall-confirm'))"
                        >
                          确认保持</button
                        ><button
                          v-if="j.status === 'UNKNOWN' || j.targetAckPending"
                          class="secondary"
                          :disabled="busy"
                          @click="act(() => jobAction(j, 'reconcile'))"
                        >
                          核对原作业</button
                        ><button
                          v-if="j.status === 'QUEUED'"
                          class="quiet"
                          :disabled="busy"
                          @click="act(() => jobAction(j, 'cancel'))"
                        >
                          取消</button
                        ><button class="quiet" @click="act(() => logs(j))">
                          记录
                        </button>
                      </div>
                      <small v-if="j.result?.firewallConfirmBy"
                        >确认期限
                        {{
                          hostTime(j.result.firewallConfirmBy)
                        }}；逾期由目标尝试恢复</small
                      >
                    </td>
                  </tr>
                </tbody>
              </table>
              <div v-if="!currentJobs.length" class="empty">
                暂无防火墙操作
              </div>
            </div>
            <OpsPagination :page="historyPage" :page-size="historyPageSize" :has-next="!!nextCursor" :disabled="busy || loading || historyLoading" label="防火墙操作分页" @update:page="changeHistoryPage" @update:page-size="changeHistorySize" />
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
