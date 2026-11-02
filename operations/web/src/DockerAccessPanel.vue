<script setup lang="ts">
import { computed, onUnmounted, ref, watch } from "vue";
import { ElDialog } from "element-plus";
import { ApiError, request } from "./api";
import { hostTime, type Host } from "./hosts";
import {
  softwareJobStatusLabel,
  type SoftwareInstance,
} from "./software";
import {
  dockerAccountAction,
  dockerAccessRejected,
  managedDockerAccess,
  type DockerAccessAction,
  type DockerAccessSnapshot,
  type DockerAccessJob,
} from "./dockerAccess";
import ErrorHelpLink from "./ErrorHelpLink.vue";
import HostPreparationPanel from "./HostPreparationPanel.vue";

const props = defineProps<{
  detail: SoftwareInstance;
  host?: Host;
  active: boolean;
  disabled?: boolean;
}>();
const emit = defineEmits<{ changed: [] }>();
const snapshot = ref<DockerAccessSnapshot>();
const selectedAccount = ref("");
const localHost = ref<Host>();
const localDetail = ref<SoftwareInstance>();
const busy = ref(false);
const attempted = ref(false);
const confirmation = ref<DockerAccessAction>();
const job = ref<DockerAccessJob>();
const jobId = ref("");
const preparationOpen = ref(false);
const error = ref<{
  message: string;
  code: string;
  traceId: string;
  at: string;
}>();
const account = computed(() =>
  snapshot.value?.observation.accounts.find(
    (item) => item.name === selectedAccount.value,
  ),
);
const pending = computed(
  () =>
    !!jobId.value &&
    (!job.value ||
      ["QUEUED", "RUNNING", "UNKNOWN"].includes(job.value.status) ||
      job.value.targetAckPending),
);
const writable = computed(
  () =>
    managedDockerAccess(localDetail.value || props.detail) &&
    !busy.value &&
    !error.value &&
    !props.disabled &&
    !localHost.value?.maintenance &&
    !pending.value &&
    !attempted.value,
);
const requiresUpdate = computed(() =>
  ["EXECUTOR_DOCKER_ACCESS_UNSUPPORTED", "EXECUTOR_UPGRADE_REQUIRED"].includes(
    error.value?.code || "",
  ),
);
let generation = 0;
let stopped = false;
let pollFailures = 0;
let timer: ReturnType<typeof setTimeout> | undefined;
let abort: AbortController | undefined;
let intent:
  | {
      action: "DOCKER_ACCESS";
      expectedVersion: string;
      expectedHostVersion: string;
      idempotencyKey: string;
      confirmed: true;
      dockerAccess: {
        action: DockerAccessAction;
        account: string;
        expectedStateHash: string;
      };
    }
  | undefined;
const current = (revision: number) =>
  !stopped && props.active && revision === generation;

function stop() {
  generation++;
  abort?.abort();
  clearTimeout(timer);
  busy.value = false;
}
async function act(
  action: (signal: AbortSignal, revision: number) => Promise<void>,
) {
  if (busy.value || !props.active) return;
  const revision = generation;
  const controller = new AbortController();
  abort = controller;
  busy.value = true;
  error.value = undefined;
  try {
    await action(controller.signal, revision);
  } catch (cause) {
    if (current(revision))
      error.value = {
        message: cause instanceof Error ? cause.message : "请求未完成",
        code: cause instanceof ApiError ? cause.code : "REQUEST_NOT_CONFIRMED",
        traceId: cause instanceof ApiError ? cause.traceId : "",
        at:
          cause instanceof ApiError
            ? cause.timestamp
            : new Date().toISOString(),
      };
  } finally {
    if (abort === controller) abort = undefined;
    if (current(revision)) busy.value = false;
  }
}
async function readAccess(signal: AbortSignal, revision: number) {
  const value = await request<DockerAccessSnapshot>(
    `/software-instances/${props.detail.id}/docker-access`,
    "GET",
    undefined,
    signal,
  );
  if (!current(revision)) return;
  if (
    !attempted.value &&
    intent &&
    intent.dockerAccess.expectedStateHash !== value.observation.fingerprint
  ) {
    confirmation.value = undefined;
    intent = undefined;
  }
  snapshot.value = value;
  if (
    !value.observation.accounts.some(
      (item) => item.name === selectedAccount.value,
    )
  )
    selectedAccount.value =
      value.observation.accounts.find(
        (item) => item.name === value.sshAccount && item.eligible,
      )?.name || "";
  if (value.lastJobId && !jobId.value && !attempted.value) {
    const previous = await request<DockerAccessJob>(
      `/jobs/${value.lastJobId}`,
      "GET",
      undefined,
      signal,
    );
    if (current(revision) && previous.kind === "SOFTWARE_DOCKER_ACCESS") {
      job.value = previous;
      jobId.value = previous.id;
    }
  }
}
async function readJob(signal: AbortSignal, revision: number) {
  if (!jobId.value) return;
  const value = await request<DockerAccessJob>(
    `/jobs/${jobId.value}`,
    "GET",
    undefined,
    signal,
  );
  if (!current(revision)) return;
  job.value = value;
  pollFailures = 0;
  if (!pending.value) {
    attempted.value = false;
    confirmation.value = undefined;
    intent = undefined;
    snapshot.value = undefined;
    await readAccess(signal, revision);
    if (current(revision)) emit("changed");
  }
}
function schedule() {
  clearTimeout(timer);
  if (
    !props.active ||
    !jobId.value ||
    !pending.value ||
    job.value?.status === "UNKNOWN" ||
    pollFailures >= 3
  )
    return;
  timer = setTimeout(async () => {
    if (!busy.value && !document.hidden) {
      await act(async (signal, revision) => {
        try {
          await readJob(signal, revision);
        } catch (cause) {
          pollFailures++;
          throw cause;
        }
      });
    }
    schedule();
  }, 3000);
}
async function refresh() {
  if (attempted.value && !jobId.value) return;
  pollFailures = 0;
  await act(async (signal, revision) => {
    if (jobId.value && pending.value) await readJob(signal, revision);
    else await readAccess(signal, revision);
  });
  schedule();
}
function chooseAccount() {
  if (attempted.value) return;
  confirmation.value = undefined;
  intent = undefined;
}
function prepare(action: DockerAccessAction) {
  if (
    !writable.value ||
    !snapshot.value ||
    !dockerAccountAction(
      account.value,
      action,
      snapshot.value.observation.socket.ready,
    )
  )
    return;
  jobId.value = "";
  job.value = undefined;
  confirmation.value = action;
  intent = {
    action: "DOCKER_ACCESS",
    expectedVersion: snapshot.value.expectedVersion,
    expectedHostVersion: snapshot.value.expectedHostVersion,
    idempotencyKey: crypto.randomUUID(),
    confirmed: true,
    dockerAccess: {
      action,
      account: selectedAccount.value,
      expectedStateHash: snapshot.value.observation.fingerprint,
    },
  };
}
async function submit() {
  if (
    !intent ||
    !confirmation.value ||
    pending.value ||
    (!attempted.value && !writable.value)
  )
    return;
  await act(async (signal, revision) => {
    attempted.value = true;
    let receipt: { jobId: string; softwareInstanceId: string };
    try {
      receipt = await request<{
        jobId: string;
        softwareInstanceId: string;
      }>(
        `/software-instances/${props.detail.id}/actions`,
        "POST",
        intent,
        signal,
      );
    } catch (cause) {
      if (
        current(revision) &&
        cause instanceof ApiError &&
        dockerAccessRejected(cause.code, cause.status)
      ) {
        attempted.value = false;
        confirmation.value = undefined;
        intent = undefined;
        snapshot.value = undefined;
      }
      throw cause;
    }
    if (!current(revision)) return;
    jobId.value = receipt.jobId;
    emit("changed");
    await readJob(signal, revision);
  });
  schedule();
}
async function jobAction(action: "reconcile" | "cancel") {
  if (!jobId.value) return;
  await act(async (signal, revision) => {
    await request(`/jobs/${jobId.value}/${action}`, "POST", {}, signal);
    if (current(revision)) await readJob(signal, revision);
  });
  schedule();
}
async function preparationUpdated() {
  await act(async (signal, revision) => {
    const [hosts, software] = await Promise.all([
      request<Host[]>("/hosts", "GET", undefined, signal),
      request<SoftwareInstance[]>(
        "/software-instances",
        "GET",
        undefined,
        signal,
      ),
    ]);
    if (!current(revision)) return;
    const host = hosts.find((item) => item.id === props.detail.hostId);
    const item = software.find((item) => item.id === props.detail.id);
    if (!host || !item) throw new Error("主机或软件记录已变化，请重新打开详情");
    localHost.value = host;
    localDetail.value = item;
    confirmation.value = undefined;
    intent = undefined;
    await readAccess(signal, revision);
    if (current(revision)) {
      preparationOpen.value = false;
      emit("changed");
    }
  });
}
watch(
  [() => props.detail.id, () => props.host?.id],
  () => {
    stop();
    snapshot.value = undefined;
    selectedAccount.value = "";
    localHost.value = props.host;
    localDetail.value = props.detail;
    attempted.value = false;
    confirmation.value = undefined;
    intent = undefined;
    job.value = undefined;
    jobId.value = "";
    preparationOpen.value = false;
    error.value = undefined;
    if (props.active) void refresh();
  },
  { immediate: true },
);
watch(
  () => props.active,
  (active) => {
    stop();
    if (active) void refresh();
  },
);
watch(
  () => props.detail,
  (value) => {
    if (value.id === localDetail.value?.id) localDetail.value = value;
  },
);
watch(
  () => props.host,
  (value) => {
    if (value?.id === localHost.value?.id) localHost.value = value;
  },
);
onUnmounted(() => {
  stopped = true;
  stop();
});
</script>

<template>
  <section class="docker-access" aria-label="Docker 账号权限">
    <header class="access-header">
      <h3>Docker 账号权限</h3>
      <button
        class="secondary"
        :disabled="busy || (attempted && !jobId)"
        @click="refresh"
      >
        {{ busy ? "检查中…" : "检查权限" }}
      </button>
    </header>
    <p v-if="error" role="alert" class="access-error">{{ error.message }}</p>
    <button
      v-if="requiresUpdate && localHost"
      class="secondary"
      :disabled="busy || attempted || pending"
      @click="preparationOpen = true"
    >
      更新接入组件
    </button>
    <details v-if="error" class="access-diagnostics">
      <summary>诊断信息</summary>
      <ErrorHelpLink :code="error.code" /><code
        >{{ error.code }} · {{ hostTime(error.at) }}</code
      ><code>traceId={{ error.traceId }}</code>
    </details>
    <template v-if="snapshot">
      <p v-if="!snapshot.observation.socket.ready" class="access-warning">
        Docker 连接入口未就绪，组成员身份不代表当前可连接引擎。
      </p>
      <label
        >服务器账号<select
          v-model="selectedAccount"
          aria-label="Docker 服务器账号"
          :disabled="busy || attempted || pending"
          @change="chooseAccount"
        >
          <option value="">请选择现有普通账号</option>
          <option
            v-for="item in snapshot.observation.accounts"
            :key="item.name"
            :value="item.name"
            :disabled="!item.eligible"
          >
            {{ item.name
            }}{{
              !item.eligible
                ? "（禁止登录）"
                : item.member
                  ? "（已在 docker 组）"
                  : ""
            }}
          </option>
        </select></label
      >
      <p v-if="!snapshot.observation.accounts.length" class="muted">
        未找到可分配的普通账号。
      </p>
      <template v-if="account"
        ><div class="account-status">
          <strong>{{
            account.member ? "已加入 docker 组" : "未加入 docker 组"
          }}</strong
          ><span>UID {{ account.uid }}</span>
        </div>
        <p v-if="account.primaryGroup" class="access-warning">
          docker 是该账号的主组，不能在此收回。
        </p>
        <div v-if="!confirmation" class="access-actions">
          <button
            v-if="!account.member"
            class="primary"
            :disabled="
              busy ||
              !writable ||
              !dockerAccountAction(
                account,
                'GRANT',
                snapshot.observation.socket.ready,
              )
            "
            @click="prepare('GRANT')"
          >
            授予访问权限
          </button>
          <button
            v-else
            class="secondary danger"
            :disabled="
              busy || !writable || !dockerAccountAction(account, 'REVOKE')
            "
            @click="prepare('REVOKE')"
          >
            收回访问权限
          </button>
        </div>
      </template>
      <div
        v-if="confirmation"
        class="access-confirmation"
        aria-label="Docker 权限确认"
      >
        <strong
          >{{ confirmation === "GRANT" ? "授权" : "收回" }}账号
          {{ selectedAccount }}</strong
        >
        <p>
          {{
            confirmation === "GRANT"
              ? "Docker 组可获得相当于 root 的主机控制能力。"
              : "仅收回 docker 组授权；已有会话和其他授权可能仍可访问，请退出旧会话。"
          }}
        </p>
        <p v-if="attempted && !jobId">提交结果尚未确认，请核对原请求。</p>
        <div class="access-actions">
          <button
            class="secondary"
            :disabled="busy || attempted"
            @click="
              confirmation = undefined;
              intent = undefined;
            "
          >
            取消
          </button>
          <button
            class="primary"
            :disabled="busy || pending || (!attempted && !writable)"
            @click="submit"
          >
            {{
              attempted
                ? "重试并核对原请求"
                : confirmation === "GRANT"
                  ? "确认授权"
                  : "确认收回"
            }}
          </button>
        </div>
      </div>
      <details class="access-diagnostics">
        <summary>
          检查详情 · {{ hostTime(snapshot.observation.observedAt) }}
        </summary>
        <dl>
          <dt>Docker 组</dt>
          <dd>docker · GID {{ snapshot.observation.dockerGid }}</dd>
          <dt>连接入口</dt>
          <dd>
            {{ snapshot.observation.socket.path }} ·
            {{ snapshot.observation.socket.ready ? "已就绪" : "未就绪" }}
          </dd>
          <dt>权限</dt>
          <dd>{{ snapshot.observation.socket.mode || "未确认" }}</dd>
        </dl>
        <code v-if="snapshot.observation.socket.errorCode">{{
          snapshot.observation.socket.errorCode
        }}</code
        ><code v-if="account?.reason">{{ account.reason }}</code>
      </details>
    </template>
    <div v-if="job" class="access-job" role="status">
      <strong>{{ softwareJobStatusLabel(job.status) }}</strong>
      <p v-if="job.status === 'SUCCEEDED' && !job.targetAckPending">
        {{
          job.result?.dockerAccess?.action === "REVOKE"
            ? "已移出 docker 组；已有会话需退出，其他授权需另行核对。"
            : "docker 组权限已更新，请重新登录该账号。"
        }}
      </p>
      <p v-if="job.status === 'UNKNOWN' || job.targetAckPending">
        结果待确认，请核对原作业。
      </p>
      <button
        v-if="job.status === 'UNKNOWN' || job.targetAckPending"
        class="secondary"
        :disabled="busy"
        @click="jobAction('reconcile')"
      >
        核对原作业
      </button>
      <button
        v-if="job.status === 'QUEUED'"
        class="secondary"
        :disabled="busy"
        @click="jobAction('cancel')"
      >
        取消排队
      </button>
      <details class="access-diagnostics">
        <summary>任务详情</summary>
        <ErrorHelpLink v-if="job.errorCode" :code="job.errorCode" /><code
          >{{ job.status }} · {{ job.phase }} · {{ job.errorCode }}</code
        ><code>{{ job.id }} · traceId={{ job.traceId }}</code
        ><time>{{ hostTime(job.updatedAt) }}</time>
      </details>
    </div>
    <ElDialog
      v-model="preparationOpen"
      title="更新接入组件"
      width="min(720px,94vw)"
      append-to-body
      destroy-on-close
      :close-on-click-modal="false"
    >
      <HostPreparationPanel
        v-if="preparationOpen && localHost"
        :host="localHost"
        update-only
        @updated="preparationUpdated"
      />
    </ElDialog>
  </section>
</template>

<style scoped>
.docker-access {
  display: grid;
  gap: 18px;
  min-width: 0;
  font-size: 13px;
}
.access-header,
.account-status,
.access-actions {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 12px;
  justify-content: space-between;
}
h3 {
  margin: 0 0 8px;
  font-size: 15px;
}
.access-header span,
.account-status span,
.muted {
  color: var(--muted);
}
label {
  display: grid;
  gap: 8px;
}
select {
  width: 100%;
  box-sizing: border-box;
}
p {
  margin: 0;
  line-height: 1.7;
}
.access-actions {
  justify-content: flex-start;
}
.access-actions .primary {
  width: auto;
  margin: 0;
}
.access-warning,
.access-confirmation {
  padding: 14px;
  background: #fff8e8;
  border: 1px solid #f1dfb8;
  border-radius: 6px;
  color: #825d1d;
}
.access-confirmation {
  display: grid;
  gap: 14px;
}
.access-error {
  color: #b23d34;
}
.access-job {
  display: grid;
  gap: 12px;
  padding: 16px;
  border: 1px solid var(--border);
  border-radius: 6px;
}
.access-job button {
  justify-self: start;
}
.access-diagnostics {
  color: var(--muted);
  font-size: 12px;
}
summary {
  cursor: pointer;
}
.access-diagnostics[open] summary {
  margin-bottom: 12px;
}
code {
  display: block;
  overflow-wrap: anywhere;
  font: 11px/1.8 var(--mono);
}
dl {
  display: grid;
  grid-template-columns: 85px minmax(0, 1fr);
  gap: 8px;
}
dd {
  margin: 0;
  overflow-wrap: anywhere;
}
</style>
