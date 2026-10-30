<script setup lang="ts">
import { ref, watch } from "vue";
import { ElDrawer, ElTabs, ElTabPane } from "element-plus";
import { hostTime, type Host } from "./hosts";
import ServiceConfigurationPanel from "./ServiceConfigurationPanel.vue";
import type { ServiceInstallReceipt } from "./serviceInstallation";
import {
  softwareDesiredLabels,
  softwareActualLabels,
  softwareJobLabels,
  softwareJobErrorMessages,
  softwareJobPhaseLabels,
  softwareModes,
  softwareRuntimeLabels,
  softwareState,
  softwareIsTool,
  softwareInstallationReady,
  softwareLifecycleAvailable,
  softwareUninstallAvailable,
  softwareTypes,
  softwareVersionLabel,
  softwareVersionReason,
  softwareVersionText,
  type SoftwareInstance,
  type SoftwareJob,
} from "./software";
const props = defineProps<{
  detail?: SoftwareInstance;
  host?: Host;
  busy: boolean;
  job?: SoftwareJob;
  pendingJob: boolean;
  logs: string;
  logSource: "software" | "job";
  logsLimited: boolean;
  logsAt: string;
  canAdopt: boolean;
  canChange: boolean;
}>();
const emit = defineEmits<{
  close: [];
  observe: [SoftwareInstance];
  unmanage: [SoftwareInstance];
  uninstall: [SoftwareInstance];
  adopt: [SoftwareInstance];
  changeState: [SoftwareInstance, "START" | "STOP"];
  readLogs: ["software" | "job"];
  reconcile: [];
  cancel: [];
  configurationSubmitted: [ServiceInstallReceipt];
  preparation: [string];
}>();
const detailTab = ref("overview");
watch(
  () => props.detail?.id,
  () => {
    detailTab.value = "overview";
  },
);
function loadLogs(source: "software" | "job") {
  detailTab.value = source === "software" ? "logs" : "job";
  emit("readLogs", source);
}
const active = (item: SoftwareInstance) => item.managementMode !== "RELEASED";
const service = (item: SoftwareInstance) => !softwareIsTool(item);
</script>

<template>
  <ElDrawer
    :model-value="!!detail"
    :title="detail?.name || '软件详情'"
    size="min(820px,100vw)"
    destroy-on-close
    @close="emit('close')"
  >
    <div v-if="detail" class="software-detail">
      <div class="detail-status">
        <span class="state-tag" :class="softwareState(detail).tone">
          {{ softwareState(detail).label }}
        </span>
        <span>{{ softwareModes[detail.managementMode] }}</span>
        <span class="spacer"></span>
        <button
          v-if="active(detail)"
          class="secondary"
          :disabled="busy || !softwareLifecycleAvailable(detail)"
          @click="emit('observe', detail)"
        >
          {{ busy ? "处理中…" : "检查状态" }}
        </button>
      </div>
      <ElTabs v-model="detailTab">
        <ElTabPane label="软件概览" name="overview">
          <dl class="software-facts">
            <dt>所在主机</dt>
            <dd>
              {{ detail.hostName || host?.name || detail.hostId }}
              <span v-if="host?.maintenance" class="maintenance-note">
                · 维护中
              </span>
            </dd>
            <dt>软件类型</dt>
            <dd>{{ softwareTypes[detail.softwareType] }}</dd>
            <dt>生命周期对象</dt>
            <dd>
              {{ softwareRuntimeLabels[detail.runtimeKind] }}
              <code>{{ detail.locator }}</code>
            </dd>
            <dt>期望状态</dt>
            <dd>{{ softwareDesiredLabels[detail.desiredState] }}</dd>
            <dt>上次确认状态</dt>
            <dd>
              {{
                softwareActualLabels[
                  detail.observation?.state || detail.actualState
                ]
              }}
              <span class="muted">
                ·
                {{
                  detail.freshness === "FRESH"
                    ? "观测有效"
                    : detail.freshness === "STALE"
                      ? "待更新"
                      : "未检查"
                }}
              </span>
            </dd>
            <template
              v-if="
                detail.latestAttempt?.code &&
                detail.latestAttempt.code !== 'READY'
              "
            >
              <dt>最近检测</dt>
              <dd>
                当前无法确认 · {{ hostTime(detail.latestAttempt.at) }}
                <code>{{ detail.latestAttempt.code }}</code>
              </dd>
            </template>
            <dt>确认时间</dt>
            <dd>{{ hostTime(detail.observation?.observedAt) }}</dd>
            <dt>
              {{ softwareVersionLabel(detail.observation, detail.runtimeKind) }}
            </dt>
            <dd>
              <pre class="software-version">{{
                softwareVersionText(detail.observation?.version) ||
                softwareVersionReason(detail.observation, detail.runtimeKind)
              }}</pre>
            </dd>
            <template v-if="detail.installationEvidence?.observedVersion">
              <dt>安装时校验版本</dt>
              <dd>
                <pre class="software-version">{{
                  detail.installationEvidence.observedVersion
                }}</pre>
                <small class="muted"
                  >{{ hostTime(detail.installationEvidence.observedAt) }} ·
                  安装回执，非实时观测</small
                >
              </dd>
            </template>
            <template v-if="detail.installationEvidence?.binaryPath"
              ><dt>安装程序</dt>
              <dd>
                <code>{{ detail.installationEvidence.binaryPath }}</code>
              </dd></template
            >
            <template v-if="detail.observation?.packageName">
              <dt>关联软件包</dt>
              <dd>
                <code>{{ detail.observation.packageName }}</code>
              </dd>
            </template>
            <template v-if="detail.observation?.executablePath">
              <dt>运行文件</dt>
              <dd>
                <code>{{ detail.observation.executablePath }}</code>
              </dd>
            </template>
            <template v-if="detail.observation?.serviceFile">
              <dt>服务文件</dt>
              <dd>
                <code>{{ detail.observation.serviceFile }}</code>
              </dd>
            </template>
            <template v-if="detail.observation?.listeningPorts?.length">
              <dt>进程监听</dt>
              <dd>
                <code
                  v-for="port in detail.observation.listeningPorts"
                  :key="port"
                >
                  {{ port }}
                </code>
              </dd>
            </template>
            <template v-if="detail.observation?.message || detail.errorCode">
              <dt>检查结果</dt>
              <dd>
                {{ detail.observation?.message }}
                <code v-if="detail.observation?.errorCode || detail.errorCode">
                  {{ detail.observation?.errorCode || detail.errorCode }}
                </code>
              </dd>
            </template>
          </dl>
        </ElTabPane>
        <ElTabPane label="服务配置" name="configuration"
          ><ServiceConfigurationPanel
            :detail="detail"
            :host="host"
            :disabled="busy || pendingJob || !canChange"
            @submitted="emit('configurationSubmitted', $event)"
            @preparation="emit('preparation', $event)"
        /></ElTabPane>
        <ElTabPane label="执行记录" name="job">
          <div v-if="job" class="software-job">
            <header>
              <strong>{{ softwareJobLabels[job.status] || job.status }}</strong>
              <code>{{ job.id }}</code>
            </header>
            <small class="job-phase">
              {{ softwareJobPhaseLabels[job.phase] || job.phase }}
            </small>
            <p v-if="job.status === 'UNKNOWN' || job.targetAckPending">
              {{
                job.targetAckPending
                  ? "等待目标确认收尾"
                  : "结果未确认，请核对原作业"
              }}
            </p>
            <p v-if="softwareJobErrorMessages[job.errorCode]">{{ softwareJobErrorMessages[job.errorCode] }}</p>
            <code v-if="job.errorCode">{{ job.errorCode }}</code>
            <div class="software-job-actions">
              <button
                v-if="job.status === 'UNKNOWN' || job.targetAckPending"
                class="secondary"
                :disabled="busy"
                @click="emit('reconcile')"
              >
                核对原作业
              </button>
              <button
                v-if="job.status === 'QUEUED'"
                class="quiet"
                :disabled="busy"
                @click="emit('cancel')"
              >
                取消排队
              </button>
              <button
                v-if="job.status !== 'QUEUED'"
                class="quiet"
                :disabled="busy"
                @click="loadLogs('job')"
              >
                作业回执
              </button>
            </div>
          </div>
          <div v-else class="empty">暂无执行记录</div>
          <div v-if="logs && logSource === 'job'" class="software-logs">
            <pre>{{ logs }}</pre>
          </div>
        </ElTabPane>
        <ElTabPane label="运行日志" name="logs">
          <button
            v-if="active(detail) && service(detail)"
            class="secondary"
            :disabled="busy || !softwareLifecycleAvailable(detail)"
            @click="loadLogs('software')"
          >
            {{ busy ? "读取中…" : "读取日志" }}
          </button>
          <div v-if="logs && logSource === 'software'" class="software-logs">
            <header>
              <strong>服务日志</strong>
              <small>
                {{ logsLimited ? "已截取最近日志 · " : ""
                }}{{ hostTime(logsAt) }}
              </small>
            </header>
            <pre>{{ logs }}</pre>
          </div>
          <div v-else class="empty">
            {{
              service(detail) ? "点击读取最近服务日志" : "工具没有常驻服务日志"
            }}
          </div>
        </ElTabPane>
        <ElTabPane label="登记信息" name="registration">
          <div class="software-identifiers">
            <dl class="software-facts">
              <dt>登记标识</dt>
              <dd>
                <code>{{ detail.id }}</code>
              </dd>
              <dt>记录版本</dt>
              <dd>{{ detail.version }}</dd>
              <dt>登记版本</dt>
              <dd>{{ detail.softwareVersion || "未标记" }}</dd>
              <dt>文件归属</dt>
              <dd>
                {{
                  detail.dataOwnership === "EXTERNAL"
                    ? "服务器原有文件"
                    : detail.dataOwnership || "未确认"
                }}
              </dd>
              <dt>身份摘要</dt>
              <dd>
                <code>
                  {{ detail.observation?.identityHash || "尚未确认" }}
                </code>
              </dd>
              <dt>观测来源</dt>
              <dd>{{ detail.observation?.source || "尚无观测" }}</dd>
              <dt v-if="job">作业追踪</dt>
              <dd v-if="job">
                <code>{{ job.traceId }}</code>
              </dd>
            </dl>
          </div>
        </ElTabPane>
      </ElTabs>
    </div>
    <template #footer>
      <footer v-if="detail" class="software-detail-actions">
        <button
          v-if="active(detail)"
          class="quiet danger"
          :disabled="busy || pendingJob || !!host?.maintenance"
          @click="emit('unmanage', detail)"
        >
          解除管理
        </button>
        <button v-if="softwareUninstallAvailable(detail)" class="quiet danger"
          :disabled="busy || pendingJob || !!host?.maintenance" @click="emit('uninstall', detail)">
          卸载软件
        </button>
        <span class="spacer"></span>
        <button
          v-if="detail.managementMode === 'OBSERVE' && service(detail)"
          class="primary"
          :disabled="busy || pendingJob || !canAdopt"
          @click="emit('adopt', detail)"
        >
          接管生命周期
        </button>
        <template v-if="detail.managementMode === 'MANAGED' && service(detail)">
          <button
            class="secondary"
            :disabled="
              busy ||
              !canChange ||
              (detail.desiredState === 'STOPPED' &&
                detail.actualState === 'STOPPED')
            "
            @click="emit('changeState', detail, 'STOP')"
          >
            停止
          </button>
          <button
            class="primary"
            :disabled="
              busy ||
              !canChange ||
              !softwareInstallationReady(detail) ||
              (detail.desiredState === 'RUNNING' &&
                detail.actualState === 'RUNNING')
            "
            @click="emit('changeState', detail, 'START')"
          >
            启动
          </button>
        </template>
      </footer>
    </template>
  </ElDrawer>
</template>

<style scoped>
.software-detail {
  display: grid;
  grid-template-columns: minmax(0, 1fr);
  gap: 22px;
}
.detail-status {
  display: flex;
  align-items: center;
  gap: 12px;
  flex-wrap: wrap;
  font-size: 13px;
}
.software-facts {
  display: grid;
  grid-template-columns: 100px minmax(0, 1fr);
  gap: 14px;
  margin: 0;
  font-size: 13px;
}
.software-facts dt {
  color: var(--muted);
}
.software-facts dd {
  margin: 0;
  overflow-wrap: anywhere;
}
.software-facts code {
  display: block;
  font: 12px/1.7 var(--mono);
  overflow-wrap: anywhere;
}
.software-version {
  font: 12px/1.7 var(--mono);
  white-space: pre-wrap;
  margin: 0;
  overflow-wrap: anywhere;
  max-height: 120px;
  overflow: auto;
}
.maintenance-note {
  color: #946619;
}
.software-job {
  border: 1px solid var(--border);
  border-radius: 6px;
  padding: 16px;
  background: #f7f9fc;
}
.software-job header,
.software-logs header {
  display: flex;
  gap: 12px;
  justify-content: space-between;
  flex-wrap: wrap;
  align-items: center;
  font-size: 13px;
}
.software-job strong,
.software-logs strong {
  font-weight: 500;
}
.software-job code {
  font: 11px/1.7 var(--mono);
  overflow-wrap: anywhere;
}
.software-job p {
  font-size: 12px;
  color: #946619;
  margin: 12px 0 0;
}
.job-phase {
  display: block;
  margin-top: 10px;
  color: var(--muted);
  font-size: 12px;
}
.software-job-actions {
  display: flex;
  gap: 8px;
  align-items: center;
  flex-wrap: wrap;
  margin-top: 12px;
}
.software-logs header small {
  color: var(--muted);
}
.software-logs:not(:first-child),
.software-detail :deep(.el-tab-pane) > .empty:not(:first-child) {
  margin-top: 14px;
}
.software-logs pre {
  background: #edf3fa;
  padding: 14px;
  font: 12px/1.7 var(--mono);
  max-height: 300px;
  overflow: auto;
  white-space: pre-wrap;
  overflow-wrap: anywhere;
}
.software-identifiers {
  padding: 0;
}
.software-identifiers summary {
  color: var(--muted);
  font-size: 13px;
  cursor: pointer;
}
.software-identifiers dl {
  margin-top: 0;
}
.software-detail-actions {
  display: flex;
  gap: 10px;
  flex-wrap: wrap;
  align-items: center;
}
.software-detail-actions .primary {
  width: auto;
  margin: 0;
}
@media (max-width: 650px) {
  .software-facts {
    grid-template-columns: 90px minmax(0, 1fr);
    gap: 12px;
  }
  .software-detail-actions .spacer {
    flex-basis: 100%;
    height: 0;
  }
}
</style>
