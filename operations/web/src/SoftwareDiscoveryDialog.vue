<script setup lang="ts">
import { computed, onUnmounted, ref, watch } from "vue";
import { ElDialog } from "element-plus";
import { ApiError, request } from "./api";
import { hostTime, type Host } from "./hosts";
import {
  softwareActualLabels,
  softwareRuntimeLabels,
  softwareTypes,
  softwareVersionLabel,
  softwareVersionReason,
  softwareVersionText,
  type SoftwareDiscoveryCandidate,
  type SoftwareDiscoveryReport,
  type SoftwareRegistrationDraft,
} from "./software";

const props = defineProps<{ open: boolean; hosts: Host[]; hostId: string }>();
const emit = defineEmits<{
  close: [];
  register: [SoftwareRegistrationDraft];
  error: [unknown];
}>();
const selectedId = ref("");
const busy = ref(false);
const report = ref<SoftwareDiscoveryReport>();
const failure = ref<Error>();
const expanded = ref("");
const host = computed(() =>
  props.hosts.find((item) => item.id === selectedId.value),
);
const sourceLabels = {
  SYSTEMD: "systemd 服务",
  DOCKER: "Docker 容器",
  TOOLS: "固定工具",
  CONFIG: "常见配置",
};
const failedChecks = computed(
  () =>
    report.value?.checks.filter((check) => check.status === "UNAVAILABLE") ||
    [],
);
const partialChecks = computed(
  () =>
    report.value?.checks.filter(
      (check) =>
        check.status === "UNAVAILABLE" ||
        ["SOFTWARE_SOURCE_PARTIAL", "CONFIG_PARTIAL_READ"].includes(check.code),
    ) || [],
);
const checkErrors = computed(() =>
  partialChecks.value.map((check) => sourceLabels[check.source]).join("、"),
);
const diagnostic = computed(() =>
  failure.value instanceof ApiError ? failure.value : undefined,
);
let revision = 0;
let disposed = false;

function reset() {
  revision += 1;
  report.value = undefined;
  failure.value = undefined;
  expanded.value = "";
}
watch(
  () => props.open,
  (open) => {
    reset();
    if (open)
      selectedId.value = props.hosts.some((item) => item.id === props.hostId)
        ? props.hostId
        : props.hosts[0]?.id || "";
  },
);
watch(() => [selectedId.value, host.value?.version], reset);
onUnmounted(() => {
  disposed = true;
  revision += 1;
});

async function discover() {
  const selected = host.value;
  if (!selected || busy.value) return;
  reset();
  const currentRevision = revision;
  busy.value = true;
  try {
    const result = await request<SoftwareDiscoveryReport>(
      `/hosts/${selected.id}/software-discovery`,
      "POST",
      {
        expectedVersion: selected.version,
      },
    );
    if (
      disposed ||
      !props.open ||
      currentRevision !== revision ||
      selectedId.value !== selected.id
    )
      return;
    if (
      result.hostId !== selected.id ||
      result.hostVersion !== selected.version ||
      host.value?.version !== selected.version
    ) {
      failure.value = new Error("主机连接已变化，请刷新主机后重新识别");
      return;
    }
    report.value = result;
  } catch (error) {
    if (disposed || !props.open || currentRevision !== revision) return;
    if (error instanceof ApiError && error.code === "AUTH_REQUIRED")
      emit("error", error);
    failure.value =
      error instanceof Error ? error : new Error("识别失败，请检查主机连接");
  } finally {
    busy.value = false;
  }
}
function register(candidate: SoftwareDiscoveryCandidate) {
  if (
    !report.value ||
    !host.value ||
    candidate.registeredId ||
    busy.value ||
    host.value.maintenance
  )
    return;
  emit("register", {
    name: candidate.name,
    hostId: report.value.hostId,
    softwareType: candidate.softwareType,
    runtimeKind: candidate.runtimeKind,
    locator: candidate.locator,
    softwareVersion:
      candidate.runtimeKind === "DOCKER" ? "" : candidate.version || "",
  });
}
const key = (candidate: SoftwareDiscoveryCandidate) =>
  `${candidate.runtimeKind}:${candidate.locator}`;
const stateTone = (candidate: SoftwareDiscoveryCandidate) =>
  ["RUNNING", "INSTALLED"].includes(candidate.state)
    ? "state-success"
    : ["FAILED", "MISSING"].includes(candidate.state)
      ? "state-error"
      : "state-muted";
</script>

<template>
  <ElDialog
    :model-value="open"
    title="自动识别软件"
    width="min(980px,96vw)"
    top="4vh"
    destroy-on-close
    @close="emit('close')"
  >
    <div class="software-discovery">
      <div class="discovery-toolbar">
        <label
          >识别主机<select
            v-model="selectedId"
            aria-label="识别主机"
            :disabled="busy"
          >
            <option value="" disabled>选择主机</option>
            <option v-for="item in hosts" :key="item.id" :value="item.id">
              {{ item.name }} ·
              {{ item.kind === "LOCAL" ? "本机" : item.address }}
            </option>
          </select></label
        ><button class="primary" :disabled="busy || !host" @click="discover">
          {{ busy ? "识别中…" : report ? "重新识别" : "开始识别" }}
        </button>
      </div>
      <p v-if="busy" class="discovery-wait" role="status">
        正在读取服务、容器和工具信息…
      </p>
      <div v-else-if="failure" class="discovery-error" role="alert">
        <strong>{{ failure.message }}</strong>
        <details v-if="diagnostic">
          <summary>诊断信息</summary>
          <code>{{ diagnostic.code }}</code
          ><time>{{ hostTime(diagnostic.timestamp) }}</time
          ><code>{{ diagnostic.traceId }}</code>
        </details>
      </div>
      <template v-else-if="report">
        <div class="discovery-summary">
          <span
            >{{ report.candidates.length }} 项候选<span v-if="report.limited">
              · 本次结果不完整</span
            ></span
          ><time>{{ hostTime(report.observedAt) }}</time>
        </div>
        <div
          v-if="partialChecks.length"
          class="discovery-partial"
          role="status"
        >
          {{ checkErrors
          }}{{
            failedChecks.length ? "全部或部分不可读取" : "仅识别到部分信息"
          }}，结果不完整。
          <details>
            <summary>检查依据</summary>
            <div v-for="check in partialChecks" :key="check.source">
              <span>{{ sourceLabels[check.source] }}</span
              ><code>{{ check.code }}</code>
            </div>
          </details>
        </div>
        <div v-if="!report.candidates.length" class="discovery-empty">
          <strong>{{
            partialChecks.length
              ? "尚未识别到可登记的软件"
              : "未发现受支持的软件"
          }}</strong
          ><span>{{
            partialChecks.length
              ? "部分来源不可读取，请检查连接与读取权限后重试"
              : "可手动登记明确的 unit、容器或已有工具"
          }}</span>
        </div>
        <div v-else class="table-scroll discovery-table">
          <table>
            <thead>
              <tr>
                <th>当前状态</th>
                <th>软件 / 生命周期对象</th>
                <th>版本 / 来源</th>
                <th>端口</th>
                <th>操作</th>
              </tr>
            </thead>
            <tbody>
              <template
                v-for="candidate in report.candidates"
                :key="key(candidate)"
                ><tr>
                  <td data-label="当前状态">
                    <span class="state-tag" :class="stateTone(candidate)">{{
                      softwareActualLabels[candidate.state] || "未确认"
                    }}</span>
                  </td>
                  <td data-label="软件" class="discovery-name">
                    <strong>{{ candidate.name }}</strong
                    ><small
                      >{{ softwareTypes[candidate.softwareType] }} ·
                      {{ softwareRuntimeLabels[candidate.runtimeKind] }}</small
                    >
                  </td>
                  <td data-label="版本">
                    <span class="discovery-version">{{
                      softwareVersionText(candidate.version) ||
                      softwareVersionReason(candidate, candidate.runtimeKind)
                    }}</span>
                    <small v-if="candidate.version">{{
                      softwareVersionLabel(candidate, candidate.runtimeKind)
                    }}</small>
                  </td>
                  <td data-label="端口" class="discovery-ports">
                    <code
                      v-for="port in (candidate.ports?.length
                        ? candidate.ports
                        : candidate.listeningPorts || []
                      ).slice(0, 2)"
                      :key="port"
                      >{{ port }}</code
                    >
                    <small
                      v-if="
                        !candidate.ports?.length &&
                        !candidate.listeningPorts?.length
                      "
                      >未识别</small
                    >
                  </td>
                  <td class="discovery-operation">
                    <div class="discovery-actions">
                      <button
                        class="quiet"
                        :disabled="
                          !!candidate.registeredId || !!host?.maintenance
                        "
                        @click="register(candidate)"
                      >
                        {{ candidate.registeredId ? "已登记" : "登记" }}</button
                      ><button
                        class="quiet"
                        @click="
                          expanded =
                            expanded === key(candidate) ? '' : key(candidate)
                        "
                      >
                        {{ expanded === key(candidate) ? "收起" : "详情" }}
                      </button>
                    </div>
                  </td>
                </tr>
                <tr
                  v-if="expanded === key(candidate)"
                  class="discovery-expanded"
                >
                  <td colspan="5">
                    <dl>
                      <dt>服务标识</dt>
                      <dd>
                        <code>{{ candidate.locator }}</code>
                      </dd>
                      <template v-if="candidate.packageName"
                        ><dt>关联软件包</dt>
                        <dd>
                          <code>{{ candidate.packageName }}</code
                          ><small
                            >安装包版本；运行中进程可能仍加载升级前的版本。</small
                          >
                        </dd></template
                      >
                      <template v-if="candidate.runtimeKind === 'DOCKER'"
                        ><dt>版本依据</dt>
                        <dd>镜像引用；未进入容器探测程序版本。</dd></template
                      >
                      <template v-if="candidate.executablePath"
                        ><dt>运行文件</dt>
                        <dd>
                          <code>{{ candidate.executablePath }}</code>
                        </dd></template
                      >
                      <template v-if="candidate.serviceFile"
                        ><dt>服务文件</dt>
                        <dd>
                          <code>{{ candidate.serviceFile }}</code>
                        </dd></template
                      >
                      <template v-if="candidate.listeningPorts?.length"
                        ><dt>进程监听</dt>
                        <dd>
                          <code
                            v-for="port in candidate.listeningPorts"
                            :key="port"
                            >{{ port }}</code
                          >
                        </dd></template
                      >
                      <template v-if="candidate.ports?.length"
                        ><dt>已识别端口</dt>
                        <dd>
                          <code v-for="port in candidate.ports" :key="port">{{
                            port
                          }}</code>
                        </dd></template
                      ><template v-if="candidate.configPaths?.length"
                        ><dt>配置位置</dt>
                        <dd>
                          <code
                            v-for="path in candidate.configPaths"
                            :key="path"
                            >{{ path }}</code
                          >
                        </dd></template
                      >
                    </dl>
                  </td>
                </tr></template
              >
            </tbody>
          </table>
        </div>
        <details
          v-if="report.configurations?.length"
          class="host-configurations"
        >
          <summary>主机配置线索（{{ report.configurations.length }}）</summary>
          <div
            v-for="item in report.configurations"
            :key="item.softwareType + item.path"
          >
            <strong>{{ softwareTypes[item.softwareType] }}</strong
            ><code>{{ item.path }}</code
            ><small v-if="item.port || item.bindAddress"
              >{{ item.bindAddress || "地址未识别"
              }}<span v-if="item.port"> : {{ item.port }}</span></small
            >
          </div>
        </details>
        <p v-if="host?.maintenance" class="discovery-note">
          主机维护中，可识别，暂不接受新登记。
        </p>
      </template>
      <div v-else class="discovery-empty">
        <strong>选择主机，识别已有软件</strong>
      </div>
    </div>
  </ElDialog>
</template>

<style scoped>
.software-discovery {
  display: grid;
  gap: 20px;
  min-width: 0;
  max-height: calc(90dvh - 90px);
  overflow-y: auto;
  overscroll-behavior: contain;
  padding-right: 4px;
}
.discovery-toolbar {
  display: flex;
  gap: 14px;
  align-items: flex-end;
  position: sticky;
  top: 0;
  z-index: 2;
  background: var(--surface);
  padding-bottom: 8px;
}
.discovery-toolbar label {
  flex: 1;
  min-width: 0;
  max-width: 460px;
}
.discovery-toolbar .primary {
  width: auto;
  margin: 0;
  min-height: 38px;
}
.discovery-toolbar select {
  height: 38px;
}
.discovery-wait {
  margin: 0;
  padding: 36px 16px;
  text-align: center;
  color: var(--muted);
  font-size: 13px;
}
.discovery-summary {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 10px;
  flex-wrap: wrap;
  font-size: 12px;
  color: var(--muted);
}
.discovery-partial {
  padding: 12px 14px;
  border: 1px solid #ead9b8;
  background: #fffbf2;
  color: #946619;
  font-size: 12px;
  line-height: 1.7;
}
.discovery-partial details {
  margin-top: 8px;
}
.discovery-partial summary,
.discovery-error summary {
  cursor: pointer;
}
.discovery-partial details > div {
  display: flex;
  gap: 12px;
  flex-wrap: wrap;
  margin-top: 6px;
}
.discovery-partial code {
  font: 11px/1.7 var(--mono);
  overflow-wrap: anywhere;
}
.discovery-empty {
  display: grid;
  gap: 14px;
  justify-items: center;
  padding: 36px 16px;
  text-align: center;
  color: var(--muted);
  font-size: 13px;
}
.discovery-empty strong {
  font-size: 14px;
  color: var(--text);
  font-weight: 500;
}
.discovery-error {
  padding: 16px;
  border: 1px solid #efd6d6;
  color: var(--error);
  background: #fcf0f0;
  font-size: 13px;
  line-height: 1.7;
}
.discovery-error strong {
  font-weight: 500;
}
.discovery-error details {
  margin-top: 10px;
  font-size: 12px;
}
.discovery-error code,
.discovery-error time {
  display: block;
  margin-top: 8px;
  overflow-wrap: anywhere;
}
.discovery-error code {
  font: 11px/1.7 var(--mono);
}
.discovery-table table {
  min-width: 0;
  table-layout: fixed;
}
.discovery-table td {
  white-space: normal;
}
.discovery-table th:first-child {
  width: 100px;
}
.discovery-table th:last-child {
  width: 110px;
}
.discovery-table strong {
  display: block;
  font-size: 13px;
  font-weight: 500;
  margin-bottom: 7px;
  overflow-wrap: anywhere;
}
.discovery-table small {
  display: block;
  font-size: 11px;
  color: var(--muted);
  margin-bottom: 5px;
}
.discovery-table code {
  display: block;
  max-width: 330px;
  font: 11px/1.7 var(--mono);
  color: var(--muted);
  overflow-wrap: anywhere;
}
.discovery-version {
  display: block;
  max-width: 220px;
  font: 12px/1.6 var(--mono);
  overflow-wrap: anywhere;
}
.discovery-table td {
  vertical-align: top;
  padding: 16px 12px;
}
.discovery-table th {
  padding: 13px 12px;
}
.discovery-ports code {
  font-size: 11px;
}
.discovery-table .quiet {
  margin: 0;
  padding: 6px 8px;
}
.discovery-expanded small {
  margin: 0;
  line-height: 1.6;
}
.discovery-actions {
  display: flex;
  gap: 4px;
  align-items: center;
  flex-wrap: wrap;
}
.discovery-expanded td {
  background: #f7f9fc;
  padding: 16px 20px;
}
.discovery-expanded dl {
  display: grid;
  grid-template-columns: 100px minmax(0, 1fr);
  gap: 12px;
  margin: 0;
  font-size: 12px;
}
.discovery-expanded dt {
  color: var(--muted);
}
.discovery-expanded dd {
  margin: 0;
  display: grid;
  gap: 6px;
}
.discovery-expanded code {
  max-width: none;
}
.host-configurations {
  padding-top: 14px;
  border-top: 1px solid var(--border);
}
.host-configurations summary {
  color: var(--muted);
  font-size: 12px;
  cursor: pointer;
}
.host-configurations > div {
  display: grid;
  grid-template-columns: 90px minmax(0, 1fr);
  gap: 8px 14px;
  margin-top: 16px;
  font-size: 12px;
}
.host-configurations strong {
  font-weight: 500;
}
.host-configurations code {
  font: 11px/1.7 var(--mono);
  overflow-wrap: anywhere;
}
.host-configurations small {
  grid-column: 2;
  color: var(--muted);
}
.discovery-note {
  color: #946619;
  font-size: 12px;
  margin: 0;
}
@media (max-width: 600px) {
  .discovery-toolbar {
    flex-wrap: wrap;
  }
  .discovery-toolbar label {
    flex-basis: 100%;
    max-width: none;
  }
  .discovery-toolbar button {
    margin-left: auto;
  }
  .host-configurations > div {
    grid-template-columns: 70px minmax(0, 1fr);
  }
}
@media (max-width: 760px) {
  .discovery-table {
    overflow: visible;
    background: transparent;
    border: 0;
  }
  .discovery-table table,
  .discovery-table tbody {
    display: block;
  }
  .discovery-table thead {
    position: absolute;
    width: 1px;
    height: 1px;
    overflow: hidden;
    clip-path: inset(50%);
  }
  .discovery-table tr {
    display: grid;
    grid-template-columns: 1fr 1fr;
    border: 1px solid var(--border);
    border-radius: 6px;
    margin-bottom: 12px;
    background: var(--surface);
    padding: 14px;
    gap: 14px;
  }
  .discovery-table td {
    display: block;
    border: 0;
    padding: 0;
    min-width: 0;
  }
  .discovery-table td[data-label]::before {
    content: attr(data-label);
    display: block;
    color: var(--muted);
    font-size: 11px;
    margin-bottom: 7px;
  }
  .discovery-table .discovery-name {
    grid-column: 1 / -1;
    grid-row: 1;
  }
  .discovery-table .discovery-name::before {
    display: none;
  }
  .discovery-table .discovery-operation {
    grid-column: 1 / -1;
    border-top: 1px solid var(--border);
    padding-top: 10px;
  }
  .discovery-table .discovery-expanded {
    margin-top: -16px;
    border-top: 0;
    border-radius: 0 0 6px 6px;
    background: #f7f9fc;
  }
  .discovery-expanded td {
    grid-column: 1 / -1;
  }
  .discovery-expanded dl {
    grid-template-columns: 76px minmax(0, 1fr);
  }
  .discovery-actions {
    justify-content: flex-end;
  }
  .discovery-version,
  .discovery-table code {
    max-width: none;
  }
}
</style>
