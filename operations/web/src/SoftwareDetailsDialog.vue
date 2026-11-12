<script setup lang="ts">
import { computed, ref, watch } from "vue";
import { ElDrawer, ElTabs, ElTabPane, ElMessageBox } from "element-plus";
import { hostTime, type Host } from "./hosts";
import ServiceConfigurationPanel from "./ServiceConfigurationPanel.vue";
import ErrorHelpLink from "./ErrorHelpLink.vue";
import DockerAccessPanel from "./DockerAccessPanel.vue";
import GitManagementPanel from "./GitManagementPanel.vue";
import SoftwareManagementPanel from "./SoftwareManagementPanel.vue";
import SoftwareWebAccess from "./SoftwareWebAccess.vue";
import SoftwareRuntimePolicyPanel from "./SoftwareRuntimePolicyPanel.vue";
import { runtimePolicyStopConfirmed } from "./runtimePolicy";
import { softwareConfigurationAvailable } from "./softwareManagement";
import { managedDockerAccess } from "./dockerAccess";
import type { ServiceInstallReceipt } from "./serviceInstallation";
import {
  softwareDesiredLabels,
  softwareActualLabels,
  softwareJobStatusLabel,
  softwareJobErrorMessages,
  softwareJobPhaseLabel,
  softwareObservationMessage,
  softwareModes,
  softwareRuntimeLabels,
  softwareState,
  softwareIsTool,
  softwareInstallationReady,
  softwareLifecycleAvailable,
  softwareUninstallAvailable,
  softwareTypeLabel,
  softwareVersionLabel,
  softwareVersionReason,
  softwareVersionText,
  type SoftwareInstance,
  type SoftwareJob,
  type SoftwareAction,
} from "./software";
const props = defineProps<{
  detail?: SoftwareInstance;
  host?: Host;
  busy: boolean;
  observing?: boolean;
  job?: SoftwareJob;
  pendingJob: boolean;
  canAdopt: boolean;
  canChange: boolean;
}>();
const emit = defineEmits<{
  close: [];
  observe: [SoftwareInstance];
  unmanage: [SoftwareInstance];
  uninstall: [SoftwareInstance];
  adopt: [SoftwareInstance];
  changeState: [SoftwareInstance, SoftwareAction];
  readLogs: ["software" | "job"];
  operationLogs: [string];
  reconcile: [];
  cancel: [];
  configurationSubmitted: [ServiceInstallReceipt];
  preparation: [string];
  changed: [];
  configure: [SoftwareInstance]; closed: [];
}>();
const detailTab = ref("overview");
watch(
  () => props.detail?.id,
  () => {
    detailTab.value = "overview";
  },
);
const active = (item: SoftwareInstance) => item.managementMode !== "RELEASED";
const configCloseState = ref({ blocked: false, dirty: false });
const accessCloseBlocked = ref(false);
const gitCloseState = ref({ blocked: false, dirty: false });
const managementCloseState = ref({ blocked: false, dirty: false });
const policyCloseState = ref({ blocked: false, dirty: false });
const configurationBlocked = computed(() => configCloseState.value.blocked || accessCloseBlocked.value || gitCloseState.value.blocked || managementCloseState.value.blocked || policyCloseState.value.blocked);
const editingBlocked = computed(() => props.busy || configurationBlocked.value);
const closeBlocked = computed(() => (props.busy && !props.observing) || configurationBlocked.value);
const heldConfirmed = computed(() => !!props.detail && props.detail.freshness === "FRESH" && runtimePolicyStopConfirmed(props.detail.observation?.runtimePolicy, props.detail.actualState === "STOPPED", props.detail.runtimeKind === "DOCKER" ? "DOCKER" : "SYSTEMD"));
const hasManagement = computed(() => !!props.detail?.installation?.definition?.commands && props.detail.managementMode === "MANAGED" && (!!props.detail.installation.definition.management || ["NGINX", "MYSQL"].includes(props.detail.softwareType) || !props.detail.installation.definition.commands.builtin));
watch(() => props.detail?.id, () => { configCloseState.value = { blocked: false, dirty: false }; accessCloseBlocked.value = false; gitCloseState.value = { blocked: false, dirty: false }; managementCloseState.value = { blocked: false, dirty: false }; policyCloseState.value = { blocked: false, dirty: false }; });
async function close(done: () => void) {
  if (closeBlocked.value) return;
  if (await confirmDiscard()) done();
}
async function confirmDiscard() {
  if (configCloseState.value.dirty || gitCloseState.value.dirty || managementCloseState.value.dirty || policyCloseState.value.dirty) {
    try { await ElMessageBox.confirm("放弃未保存的软件配置？", "关闭详情", { confirmButtonText: "放弃修改", cancelButtonText: "继续编辑" }); } catch { return false; }
  }
  return true;
}
async function loadLogs(source: "software" | "job") {
  if (editingBlocked.value || !(await confirmDiscard())) return;
  emit("readLogs", source);
}
async function operationLogs(id: string) {
  if (editingBlocked.value || !(await confirmDiscard())) return;
  emit("operationLogs", id);
}
async function operationPreparation() {
  if (editingBlocked.value || !props.detail || !(await confirmDiscard())) return;
  emit("preparation", props.detail.hostId);
}
async function editConfiguration() {
  if (editingBlocked.value || !props.detail || !(await confirmDiscard())) return;
  emit("configure", props.detail);
}
const service = (item: SoftwareInstance) => !softwareIsTool(item);
</script>

<template>
  <ElDrawer
    :model-value="!!detail"
    :title="detail?.name || '软件详情'"
    size="min(820px,100vw)"
    destroy-on-close
    :close-on-click-modal="!closeBlocked" :close-on-press-escape="!closeBlocked" :show-close="!closeBlocked" :before-close="close"
    @close="emit('close')"
    @closed="emit('closed')"
  >
    <div v-if="detail" class="software-detail">
      <div class="detail-status">
        <span class="state-tag" :class="softwareState(detail).tone">
          {{ softwareState(detail).label }}
        </span>
        <span>{{ softwareModes[detail.managementMode] }}</span>
        <span class="spacer"></span>
        <button v-if="softwareConfigurationAvailable(detail)" class="secondary" :disabled="editingBlocked || !!host?.maintenance || pendingJob" @click="editConfiguration">编辑配置</button>
        <button v-if="active(detail) && service(detail)" class="secondary"
          :disabled="editingBlocked || !softwareLifecycleAvailable(detail)" @click="loadLogs('software')">运行日志</button>
        <button
          v-if="active(detail)"
          class="secondary"
          :disabled="editingBlocked || !softwareLifecycleAvailable(detail)"
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
            <dd>{{ softwareTypeLabel(detail) }}</dd>
            <template v-if="detail.installation?.official">
              <dt>安装方式</dt><dd>官方系统软件包</dd>
            </template>
            <dt>生命周期对象</dt>
            <dd>
              {{ softwareRuntimeLabels[detail.runtimeKind] }}
            </dd>
            <template v-if="detail.softwareType === 'JAVA' && detail.installationStatus === 'READY' && detail.locator.startsWith('/') && detail.locator.endsWith('/bin/java')">
              <dt>项目 JAVA_HOME</dt>
              <dd><code>{{ detail.locator.slice(0, -'/bin/java'.length) }}</code></dd>
            </template>
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
                  安装回执</small
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
            <template v-if="softwareObservationMessage(detail.observation?.message, detail.observation?.errorCode || detail.errorCode)">
              <dt>检查结果</dt>
              <dd>
                {{ softwareObservationMessage(detail.observation?.message, detail.observation?.errorCode || detail.errorCode) }}
              </dd>
            </template>
          </dl>
        </ElTabPane>
        <ElTabPane v-if="detail.installation?.definition?.web && detail.installation.config" label="访问界面" name="web-access"><SoftwareWebAccess :item="detail" :host="host" :active="detailTab === 'web-access'" /></ElTabPane>
        <ElTabPane :label="service(detail) ? '配置与运行' : '安装详情'" name="configuration"
          ><SoftwareRuntimePolicyPanel v-if="service(detail)" :key="detail.id" :detail="detail" :active="detailTab === 'configuration'" :disabled="busy || pendingJob || !!host?.maintenance" @observe="emit('observe', detail)" @preparation="operationPreparation" @submitted="receipt => receipt.jobId && emit('configurationSubmitted', { softwareInstanceId: receipt.softwareInstanceId, jobId: receipt.jobId })" @close-state="policyCloseState = $event" />
          <ServiceConfigurationPanel @close-state="configCloseState = $event"
            :detail="detail"
            :host="host"
            :disabled="busy || pendingJob || !canChange"
            @submitted="emit('configurationSubmitted', $event)"
            @preparation="emit('preparation', $event)"
        /></ElTabPane>
        <ElTabPane v-if="hasManagement" :label="detail.softwareType === 'NGINX' ? '站点管理' : detail.softwareType === 'MYSQL' ? '数据库管理' : '管理操作'" name="management"><SoftwareManagementPanel :key="detail.id" :detail="detail" :active="detailTab === 'management'" :disabled="busy || pendingJob || !!host?.maintenance" @close-state="managementCloseState = $event" @submitted="receipt => receipt.jobId && emit('configurationSubmitted', { softwareInstanceId: receipt.softwareInstanceId, jobId: receipt.jobId })" @logs="operationLogs" @preparation="operationPreparation" /></ElTabPane>
        <ElTabPane v-if="managedDockerAccess(detail)" label="账号权限" name="docker-access">
          <DockerAccessPanel @close-blocked="accessCloseBlocked = $event" :detail="detail" :host="host" :active="detailTab === 'docker-access'" :disabled="busy || pendingJob || !canChange" @changed="emit('changed')" />
        </ElTabPane>
        <ElTabPane label="执行记录" name="job">
          <div v-if="job" class="software-job">
            <header>
              <strong>{{ softwareJobStatusLabel(job.status) }}</strong>
              <time>{{ hostTime(job.updatedAt) }}</time>
            </header>
            <small class="job-phase">
              {{ softwareJobPhaseLabel(job.phase) }}
            </small>
            <p v-if="job.status === 'UNKNOWN' || job.targetAckPending">
              {{
                job.targetAckPending
                  ? "等待目标确认收尾"
                  : "结果未确认，请核对原作业"
              }}
            </p>
            <p v-if="softwareJobErrorMessages[job.errorCode]">{{ softwareJobErrorMessages[job.errorCode] }}</p>
            <p v-else-if="job.errorCode">执行未完成，请查看诊断信息。</p>
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
                class="quiet"
                :disabled="editingBlocked"
                @click="loadLogs('job')"
              >
                查看日志
              </button>
            </div>
          </div>
          <div v-else class="empty">暂无执行记录</div>
        </ElTabPane>
        <ElTabPane label="来源与归属" name="registration">
          <div class="software-identifiers">
            <dl class="software-facts">
              <dt>登记版本</dt>
              <dd>{{ detail.softwareVersion || "未标记" }}</dd>
              <dt>文件归属</dt>
              <dd>
                {{
                  detail.dataOwnership === "EXTERNAL"
                    ? "服务器原有文件"
                    : detail.dataOwnership === "OPS" ? "Ops 安装文件" : "未确认"
                }}
              </dd>
            </dl>
            <details class="software-diagnostics"><summary>排障信息</summary>
              <ErrorHelpLink :code="detail.observation?.errorCode || detail.errorCode || detail.latestAttempt?.code || ''" />
              <dl class="software-facts">
              <dt>登记标识</dt><dd><code>{{ detail.id }}</code></dd>
              <dt>记录版本</dt><dd>{{ detail.version }}</dd>
              <template v-if="detail.locator"><dt>运行对象</dt><dd><code>{{ detail.locator }}</code></dd></template>
              <template v-if="detail.observation?.source"><dt>观测来源</dt><dd><code>{{ detail.observation.source }}</code></dd></template>
              <template v-if="detail.observation?.identityHash"><dt>身份摘要</dt><dd><code>{{ detail.observation.identityHash }}</code></dd></template>
              <template v-if="detail.observation?.message"><dt>原始检查结果</dt><dd><code>{{ detail.observation.message }}</code></dd></template>
              <template v-if="detail.observation?.errorCode || detail.errorCode"><dt>错误码</dt><dd><code>{{ detail.observation?.errorCode || detail.errorCode }}</code></dd></template>
              <template v-if="detail.latestAttempt?.code"><dt>最近检测</dt><dd><code>{{ detail.latestAttempt.code }}</code>{{ detail.latestAttempt.at ? hostTime(detail.latestAttempt.at) : '' }}</dd></template>
              <template v-if="detail.latestAttempt?.traceId"><dt>请求追踪</dt><dd><code>{{ detail.latestAttempt.traceId }}</code></dd></template>
              <dt v-if="job">作业追踪</dt><dd v-if="job"><code>{{ job.traceId }}</code></dd>
            </dl></details>
          </div>
        </ElTabPane>
        <ElTabPane v-if="detail.softwareType === 'GIT' && host" label="Git 账号" name="git-accounts" :disabled="gitCloseState.blocked && detailTab !== 'git-accounts'" />
      </ElTabs>
      <GitManagementPanel v-if="detail.softwareType === 'GIT' && host" v-show="detailTab === 'git-accounts'" :host="host" :active="detailTab === 'git-accounts'" @close-state="gitCloseState = $event" @changed="emit('changed')" />
    </div>
    <template #footer>
      <footer v-if="detail" class="software-detail-actions">
        <button
          v-if="active(detail)"
          class="quiet danger"
          :disabled="editingBlocked || pendingJob || !!host?.maintenance"
          @click="emit('unmanage', detail)"
        >
          {{ detail.managementMode === "OBSERVE" ? "移除登记" : "解除管理" }}
        </button>
        <button v-if="softwareUninstallAvailable(detail)" class="quiet danger"
          :disabled="editingBlocked || pendingJob || !!host?.maintenance" @click="emit('uninstall', detail)">
          卸载软件
        </button>
        <span class="spacer"></span>
        <button
          v-if="detail.managementMode === 'OBSERVE' && service(detail)"
          class="primary"
          :disabled="editingBlocked || pendingJob || !canAdopt"
          @click="emit('adopt', detail)"
        >
          接管生命周期
        </button>
        <template v-if="detail.managementMode === 'MANAGED' && service(detail)">
          <button v-if="detail.runtimeKind === 'DOCKER' && detail.actualState === 'PAUSED'" class="primary" :disabled="editingBlocked || !canChange" @click="emit('changeState', detail, 'UNPAUSE')">继续</button>
          <button v-if="detail.runtimeKind === 'DOCKER' && detail.actualState === 'RUNNING'" class="secondary" :disabled="editingBlocked || !canChange" @click="emit('changeState', detail, 'PAUSE')">暂停</button>
          <button class="secondary" :disabled="editingBlocked || !canChange" @click="emit('changeState', detail, 'RESTART')">重启</button>
          <button
            class="secondary"
            :disabled="
              editingBlocked ||
              !canChange ||
              heldConfirmed
            "
            @click="emit('changeState', detail, 'STOP')"
          >
            停止
          </button>
          <button
            v-if="detail.actualState !== 'PAUSED'"
            class="primary"
            :disabled="
              editingBlocked ||
              !canChange ||
              !softwareInstallationReady(detail) ||
              (!detail.observation?.runtimePolicy?.held && detail.desiredState === 'RUNNING' &&
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
.software-job header {
  display: flex;
  gap: 12px;
  justify-content: space-between;
  flex-wrap: wrap;
  align-items: center;
  font-size: 13px;
}
.software-job strong {
  font-weight: 500;
}
.software-diagnostics {
  margin-top: 16px;
  font-size: 12px;
  color: var(--muted);
}
.software-diagnostics summary {
  cursor: pointer;
  margin-bottom: 10px;
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
.software-detail :deep(.el-tab-pane) > .empty:not(:first-child) {
  margin-top: 14px;
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
