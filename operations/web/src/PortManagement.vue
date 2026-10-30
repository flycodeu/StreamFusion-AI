<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref, watch } from "vue";
import { ElDialog, ElTabs, ElTabPane, ElMessage } from "element-plus";
import { request, ApiError } from "./api";
import { hostTime, type Host } from "./hosts";

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
let disposed = false,
  generation = 0,
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
  busy.value = true;
  error.value = "";
  try {
    await fn();
  } catch (e) {
    error.value =
      e instanceof ApiError
        ? `${e.message} · ${e.code} · ${e.timestamp} · traceId=${e.traceId}`
        : e instanceof Error
          ? e.message
          : "操作未确认";
    emit("error", e);
  } finally {
    busy.value = false;
  }
}
async function refresh() {
  if (loading.value) return;
  loading.value = true;
  const selected = hostId.value,
    rev = generation;
  try {
    const [all, entries] = await Promise.all([
      request<Host[]>("/hosts"),
      request<Job[]>("/jobs"),
    ]);
    if (disposed || rev !== generation) return;
    hosts.value = all;
    jobs.value = entries;
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
    loading.value = false;
  }
}
function schedule() {
  if (poll) clearTimeout(poll);
  if (disposed) return;
  poll = setTimeout(async () => {
    try {
      if (!document.hidden) await refresh();
    } catch (e) {
      emit("error", e);
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
  if (rev === generation && selected === hostId.value) view.value = result;
}
function open(op: string, selected?: Rule) {
  operation.value = op;
  dialog.value = true;
  plan.value = undefined;
  receipt.value = "";
  submitKey.value = crypto.randomUUID();
  submissionAttempted.value = false;
  error.value = "";
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
  plan.value = await request<Plan>(
    `/hosts/${hostId.value}/firewall-plans`,
    "POST",
    {
      expectedVersion: host.value.version,
      snapshotHash: snapshot.value.hash,
      operation: operation.value,
      rule: r,
    },
  );
}
async function submit() {
  if (!plan.value || (expired.value && !submissionAttempted.value)) return;
  // The server may have accepted a request whose response was lost. Keep the
  // same plan/key so retry can find that job even after the plan expires.
  submissionAttempted.value = true;
  const accepted = await request<{ jobId: string }>(
    `/firewall-plans/${plan.value.id}/submit`,
    "POST",
    { confirmed: true, idempotencyKey: submitKey.value },
  );
  receipt.value = accepted.jobId;
  dialog.value = false;
  tab.value = "jobs";
  await refresh();
  ElMessage.success("已提交，执行结果见操作记录");
}
async function jobAction(j: Job, action: string) {
  await request(`/jobs/${j.id}/${action}`, "POST", {});
  await refresh();
}
async function logs(j: Job) {
  const result = await request<{ text: string }>(`/jobs/${j.id}/logs`);
  jobLogs.value = result.text;
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
  view.value = undefined;
  plan.value = undefined;
  dialog.value = false;
  jobLogs.value = "";
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
        >管理主机<select
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
    <template v-else>
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
                : view.code
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
          <code v-if="view.traceId">traceId={{ view.traceId }}</code>
        </details>
      </div>
      <div class="port-panel">
        <ElTabs v-model="tab">
          <ElTabPane label="监听端口" name="ports">
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
                  <tr v-for="(p, i) in filtered" :key="i">
                    <td>
                      <strong>{{ p.port }}</strong>
                    </td>
                    <td>{{ p.protocol.toUpperCase() }}</td>
                    <td>
                      <code>{{ p.address }}</code>
                    </td>
                    <td>{{ p.process || "不可读" }}</td>
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
                  <tr v-for="r in snapshot?.managedRules || []" :key="r.id">
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
                    <th>作业</th>
                    <th>结果</th>
                    <th>操作</th>
                  </tr>
                </thead>
                <tbody>
                  <tr v-for="j in currentJobs" :key="j.id">
                    <td>{{ states[j.status] || j.status }}</td>
                    <td>{{ hostTime(j.updatedAt) }}</td>
                    <td>
                      <code>{{ j.id.slice(0, 8) }}</code>
                    </td>
                    <td>
                      <code>{{ j.errorCode || j.phase }}</code>
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
            <pre v-if="jobLogs" class="port-log">{{ jobLogs }}</pre>
          </ElTabPane>
        </ElTabs>
      </div>
    </template>
    <ElDialog
      v-model="dialog"
      :title="actions[operation]"
      width="min(560px,94vw)"
      align-center
      :close-on-click-modal="!busy"
      :show-close="!busy"
    >
      <div class="port-form">
        <template v-if="!plan && operation === 'ADD'"
          ><div class="port-form-row">
            <label
              >策略<select v-model="rule.action" aria-label="访问策略">
                <option value="allow">允许访问</option>
                <option value="deny">禁止新连接</option>
              </select></label
            ><label
              >协议<select v-model="rule.protocol" aria-label="端口协议">
                <option value="tcp">TCP</option>
                <option value="udp">UDP</option>
              </select></label
            >
          </div>
          <div class="port-form-row">
            <label
              >端口<input
                v-model.number="rule.start"
                type="number"
                min="1"
                max="65535"
                aria-label="端口" /></label
            ><label v-if="range"
              >结束端口<input
                v-model.number="rule.end"
                type="number"
                min="1"
                max="65535"
                aria-label="结束端口"
            /></label>
          </div>
          <label class="range-toggle"
            ><input v-model="range" type="checkbox" />连续端口范围</label
          ><label
            >访问来源<select v-model="sourceMode" aria-label="访问来源">
              <option value="any4">任意 IPv4 来源</option>
              <option value="any6">任意 IPv6 来源</option>
              <option value="custom">指定 IP / 网段</option>
            </select></label
          ><label v-if="sourceMode === 'custom'"
            >IP / CIDR<input
              v-model="sourceText"
              aria-label="来源 IP 或网段"
              placeholder="例如 192.168.1.0/24" /></label
        ></template>
        <template v-else-if="!plan"
          ><p>
            {{
              operation === "DELETE"
                ? "撤销此规则后，是否可访问仍取决于其他规则和默认策略。"
                : operation === "ENABLE"
                  ? "启用后按照已有规则过滤流量。先为检测到的管理来源放行实际 SSH 端口。"
                  : "停用后，UFW 将不再过滤入站访问，现有规则保留。"
            }}
          </p>
          <p v-if="operation !== 'DELETE'" class="port-warning">
            提交后需在 120 秒内确认保持；失联时目标尝试恢复原状态。
          </p></template
        >
        <template v-if="plan"
          ><dl class="port-plan">
            <dt>主机</dt>
            <dd>{{ host?.name }}</dd>
            <dt>操作</dt>
            <dd>{{ actions[plan.change.operation] }}</dd>
            <template v-if="['ADD', 'DELETE'].includes(operation)"
              ><dt>策略 / 端口</dt>
              <dd>
                {{ plan.change.rule.action === "allow" ? "允许" : "禁止" }} ·
                {{ plan.change.rule.protocol.toUpperCase() }}
                {{ plan.change.rule.start }}–{{ plan.change.rule.end }}
              </dd>
              <dt>来源</dt>
              <dd>
                <code>{{ plan.change.rule.source }}</code>
              </dd></template
            >
            <dt>当前防火墙</dt>
            <dd>{{ states[plan.snapshot.status] }}</dd>
            <dt>计划有效期</dt>
            <dd>{{ hostTime(plan.expiresAt) }}</dd>
          </dl>
          <p v-if="submissionAttempted" class="port-warning">
            已尝试提交；响应未确认时，使用原标识重试并找回作业。
          </p></template
        >
        <p v-if="error" role="alert" class="port-error">{{ error }}</p>
      </div>
      <template #footer
        ><div class="port-actions">
          <button class="secondary" :disabled="busy" @click="dialog = false">
            取消</button
          ><button
            v-if="plan"
            class="quiet"
            :disabled="busy || submissionAttempted"
            @click="plan = undefined"
          >
            返回修改</button
          ><button
            v-if="!plan"
            class="primary"
            :disabled="busy"
            @click="act(preflight)"
          >
            预检规则</button
          ><button
            v-else
            class="primary"
            :disabled="busy || (expired && !submissionAttempted)"
            @click="act(submit)"
          >
            {{
              submissionAttempted
                ? "重试并找回作业"
                : expired
                  ? "计划已过期"
                  : "确认执行"
            }}
          </button>
        </div></template
      >
    </ElDialog>
  </section>
</template>

<style scoped>
.port-page {
  display: grid;
  grid-template-columns: minmax(0, 1fr);
  gap: 24px;
  min-width: 0;
}
.port-toolbar,
.port-actions,
.port-rule-tools,
.port-filter {
  display: flex;
  align-items: center;
  gap: 12px;
  flex-wrap: wrap;
}
.port-toolbar {
  justify-content: space-between;
  align-items: flex-end;
}
.port-rule-tools {
  justify-content: space-between;
}
.port-toolbar label,
.port-form label {
  display: grid;
  gap: 8px;
  font-size: 13px;
  color: var(--muted);
}
.port-toolbar select {
  min-width: 240px;
  margin-top: 0;
  height: 38px;
}
.port-toolbar .port-actions button {
  min-height: 38px;
}
.port-summary {
  display: grid;
  grid-template-columns: repeat(4, minmax(0, 1fr));
  border: 1px solid var(--border);
  border-radius: 8px;
  background: white;
  box-shadow: var(--card-shadow);
}
.port-panel {
  background: var(--surface);
  border: 1px solid var(--border);
  border-radius: 8px;
  padding: 20px 24px 24px;
  box-shadow: var(--card-shadow);
  min-width: 0;
}
.port-summary > div {
  display: grid;
  align-content: start;
  gap: 14px;
  padding: 22px;
  border-right: 1px solid var(--border);
}
.port-summary > div:last-child {
  border-right: 0;
}
.port-summary small {
  color: var(--muted);
}
.port-summary strong {
  font-size: 24px;
  font-weight: 600;
  line-height: 1.25;
}
.port-summary span {
  font-size: 14px;
  font-weight: 500;
  line-height: 30px;
}
.port-summary em {
  margin-left: 7px;
  color: var(--muted);
  font-size: 11px;
  font-style: normal;
  font-weight: 400;
}
.enabled {
  color: var(--success);
}
.deny,
.danger,
.port-error {
  color: var(--danger);
}
.port-notice {
  display: flex;
  gap: 8px;
  flex-wrap: wrap;
  align-items: center;
  padding: 12px 16px;
  background: #f6f8fb;
  border-left: 3px solid #b3c6dc;
  border-radius: 4px;
  font-size: 13px;
}
.port-notice small {
  width: 100%;
  font-size: 11px;
  color: var(--muted);
  overflow-wrap: anywhere;
}
.port-notice details {
  width: 100%;
  color: var(--muted);
  font-size: 11px;
}
.port-notice summary {
  cursor: pointer;
}
.port-notice details code {
  display: block;
  margin-top: 5px;
}
.port-filter {
  justify-content: space-between;
  margin: 4px 0 16px;
}
.port-filter input {
  max-width: 320px;
  margin: 0;
}
.port-table {
  overflow-x: auto;
  overflow-y: auto;
  max-height: 480px;
  border: 1px solid var(--border);
  border-radius: 6px;
}
.port-table table {
  width: 100%;
  border-collapse: collapse;
  background: white;
}
.port-table th {
  position: sticky;
  top: 0;
  z-index: 1;
  font-weight: 500;
  color: var(--muted);
  background: #f8fafc;
}
.port-table th,
.port-table td {
  text-align: left;
  padding: 13px 16px;
  white-space: nowrap;
  border-bottom: 1px solid var(--border-subtle);
}
.port-table td small {
  display: block;
  margin-top: 8px;
  font-size: 11px;
  color: var(--muted);
}
.port-table tbody tr:last-child td {
  border-bottom: 0;
}
.port-rule-tools {
  justify-content: flex-end;
  margin: 4px 0 16px;
}
.port-docker {
  margin-top: 20px;
  border: 1px solid var(--border);
  border-radius: 6px;
  padding: 14px;
  font-size: 12px;
}
.port-docker p {
  color: var(--muted);
}
.port-docker pre,
.port-log {
  max-height: 300px;
  overflow: auto;
  white-space: pre-wrap;
  overflow-wrap: anywhere;
  background: #f6f8fb;
  padding: 12px;
  font-size: 12px;
  line-height: 1.7;
}
.port-form {
  display: grid;
  gap: 20px;
}
.port-form-row {
  display: grid;
  grid-template-columns: 1fr 1fr;
  gap: 16px;
}
.port-form input,
.port-form select {
  width: 100%;
  box-sizing: border-box;
}
.port-form .range-toggle {
  display: flex;
  align-items: center;
}
.range-toggle input {
  width: auto;
}
.port-plan {
  display: grid;
  grid-template-columns: 100px minmax(0, 1fr);
  gap: 14px;
  margin: 0;
}
.port-plan dt {
  color: var(--muted);
}
.port-plan dd {
  margin: 0;
  overflow-wrap: anywhere;
}
.port-warning {
  padding: 12px;
  background: #fcf7ec;
  color: #85652c;
  font-size: 12px;
  line-height: 1.7;
}
.port-error {
  font-size: 12px;
  overflow-wrap: anywhere;
}
.port-page :deep(.el-tabs),
.port-page :deep(.el-tabs__content) {
  min-width: 0;
}
@media (max-width: 760px) {
  .port-panel {
    padding: 16px 14px;
  }
  .port-summary {
    grid-template-columns: 1fr 1fr;
  }
  .port-summary > div {
    padding: 16px;
  }
  .port-summary strong {
    font-size: 21px;
  }
  .port-toolbar label,
  .port-toolbar select {
    width: 100%;
    min-width: 0;
  }
  .port-filter input {
    max-width: none;
  }
  .port-filter input {
    width: 100%;
  }
  .port-form-row {
    grid-template-columns: 1fr;
  }
  .port-plan {
    grid-template-columns: 80px minmax(0, 1fr);
  }
}
</style>
