<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref, watch } from "vue";
import {
  ElAlert, ElButton, ElDialog, ElEmpty, ElInputNumber, ElSkeleton, ElTag,
} from "element-plus";
import { ApiError, request } from "./api";
import { jenkinsState, moduleJenkinsPath, type JenkinsBuild, type JenkinsMonitor } from "./projectJenkins";
import type { LogSelection } from "./jobs";
import ProjectJenkinsBuild from "./ProjectJenkinsBuild.vue";
import type { DeploymentStatusReader } from "./features/project-delivery/deploymentStatusReader";
import {
  deploymentActionText, deploymentHealthAt, deploymentHealthText, deploymentPhaseText, deploymentTime,
  isPublishableBuild, jobPending, jobStatusText, projectDeploymentErrorText, projectDeploymentPath,
  type DeploymentJob, type DeploymentPlan, type DeploymentVersion,
} from "./projectDeployments";

const props = defineProps<{
  projectId: string;
  moduleId: string;
  moduleName: string;
  deploymentId: string;
  environment: string;
  hostName: string;
  archived?: boolean;
  delivery: DeploymentStatusReader;
}>();
const emit = defineEmits<{
  close: [];
  error: [error: unknown];
  buildObserved: [build: JenkinsBuild];
  executionLogs: [selection: LogSelection];
  editSite: [];
}>();

const lifetime = new AbortController();
const base = projectDeploymentPath(props.projectId, props.deploymentId);
const qaBase = moduleJenkinsPath(props.projectId, props.moduleId);
const qaPath = `${qaBase}/jenkins-build`;
const observation = computed(() => props.delivery.observation(base));
const status = computed(() => observation.value?.status);
const loading = computed(() => observation.value?.loading ?? true);
const busy = ref("");
const error = ref("");
const readError = computed(() => observation.value?.error === undefined ? ""
  : describe(observation.value.error, "读取部署状态失败，请刷新后重试。", false));
const buildError = ref("");
const selectedNumber = ref<number>();
const selectedBuild = ref<JenkinsBuild>();
const qaMonitor = ref<JenkinsMonitor>();
const plan = ref<DeploymentPlan>();
const logs = ref<{ output: string; observedAt: string }>();
const rollbackTarget = ref<DeploymentVersion>();
const decommissionTarget = ref<DeploymentVersion>();
const recoveryTarget = ref<DeploymentJob>();
const healthClock = ref(Date.now());
let healthTimer: ReturnType<typeof setInterval> | undefined;

const lastJob = computed(() => status.value?.latest);
const lastJobError = computed(() => projectDeploymentErrorText(lastJob.value?.errorCode));
const health = computed(() => deploymentHealthAt(status.value?.health, status.value?.healthObservedAt, healthClock.value));
const mutation = computed(() => status.value?.pending);
const pending = computed(() => jobPending(lastJob.value) || jobPending(mutation.value) || jobPending(status.value?.activeCheck));
const needsRecovery = computed(() => mutation.value?.status === "UNKNOWN");
const supported = computed(() => status.value?.kind === "JAVA" || status.value?.kind === "STATIC");
const available = computed(() => !pending.value && !busy.value);
const hasCheckTarget = computed(() => !!status.value?.current || needsRecovery.value);
const canCheck = computed(() => available.value && hasCheckTarget.value);
const canDeploy = computed(() => supported.value && available.value && !props.archived && !needsRecovery.value);
const canRecover = computed(() => available.value && needsRecovery.value);
const canDecommission = computed(() => available.value && !!status.value?.current && !needsRecovery.value);
const currentKind = computed(() => status.value?.current?.kind ?? status.value?.kind);
const siteConfigPath = computed(() => currentKind.value === "STATIC" && status.value?.current
  ? `/etc/nginx/conf.d/sfops-project-${props.deploymentId}.conf` : "");
const qualifies = computed(() =>
  isPublishableBuild(selectedBuild.value) && selectedBuild.value?.number === selectedNumber.value,
);
const qaContext = {
  action: "qa" as const,
  moduleId: props.moduleId,
  moduleName: props.moduleName,
};

function describe(problem: unknown, fallback: string, notify = true) {
  if (problem instanceof ApiError) {
    if (notify && problem.code === "AUTH_REQUIRED") emit("error", problem);
    return [problem.message, problem.code, problem.traceId, deploymentTime(problem.timestamp)].filter(Boolean).join(" · ");
  }
  return fallback;
}

function refresh() {
  error.value = "";
  return props.delivery.refresh(base);
}

watch(status, (data) => {
  if (data?.pending?.status === "UNKNOWN") {
    plan.value = undefined;
    rollbackTarget.value = undefined;
    decommissionTarget.value = undefined;
  }
  if (data?.pending?.jobId !== recoveryTarget.value?.jobId) recoveryTarget.value = undefined;
  if (data?.current?.releaseId !== decommissionTarget.value?.releaseId) decommissionTarget.value = undefined;
}, { flush: "sync" });

async function openQa() {
  if (!canDeploy.value) return;
  busy.value = "qa";
  buildError.value = "";
  plan.value = undefined;
  try {
    const data = await request<JenkinsMonitor>(`${qaBase}/jenkins-monitor`, "GET", undefined, lifetime.signal);
    if (lifetime.signal.aborted) return;
    if (data.configured) {
      chooseNumber(undefined);
      qaMonitor.value = data;
    }
    else buildError.value = data.reason || "请先在项目详情设置 Jenkins 连接。";
  } catch (problem) {
    if (!lifetime.signal.aborted) buildError.value = describe(problem, "无法读取项目 Jenkins 配置。");
  } finally {
    busy.value = "";
  }
}

function observeBuild(build: JenkinsBuild) {
  emit("buildObserved", build);
  if (!isPublishableBuild(build)) {
    chooseNumber(undefined);
    return;
  }
  selectedNumber.value = build.number;
  selectedBuild.value = build;
}

function chooseNumber(number: number | undefined) {
  selectedNumber.value = number;
  selectedBuild.value = undefined;
  plan.value = undefined;
  buildError.value = "";
}

async function readBuild(useLatest = false) {
  if (!canDeploy.value) return;
  const number = useLatest ? undefined : selectedNumber.value;
  if (!useLatest && (!Number.isSafeInteger(number) || (number ?? 0) < 1)) return;
  buildError.value = "";
  plan.value = undefined;
  selectedBuild.value = undefined;
  busy.value = "build";
  try {
    const result = await request<JenkinsBuild>(
      `${qaPath}${number ? "?number=" + number : ""}`, "GET", undefined, lifetime.signal,
    );
    if (lifetime.signal.aborted) return;
    selectedNumber.value = result.number;
    selectedBuild.value = result;
    if (!result.configured) buildError.value = "请先在项目详情配置 Jenkins，再运行模块 QA。";
    else if (!qualifies.value) buildError.value = "请选择已通过 QA 并归档制品的构建。";
  } catch (problem) {
    if (!lifetime.signal.aborted) buildError.value = describe(problem, "读取 QA 构建失败，请核对构建号。");
  } finally {
    busy.value = "";
  }
}

async function preflight() {
  if (!canDeploy.value || !qualifies.value || !selectedNumber.value) return;
  await checkPlan(selectedNumber.value);
}

async function preflightCurrent() {
  const current = status.value?.current;
  if (!siteConfigPath.value || !current || !canDeploy.value) return;
  chooseNumber(undefined);
  await checkPlan(current.buildNumber, current);
}

async function checkPlan(buildNumber: number, reuse?: DeploymentVersion) {
  busy.value = "preflight";
  error.value = "";
  plan.value = undefined;
  try {
    const data = await request<DeploymentPlan>(
      `${base}/preflight`, "POST", { buildNumber, ...(reuse && { currentReleaseId: reuse.releaseId }) }, lifetime.signal,
    );
    if (lifetime.signal.aborted) return;
    if (reuse && (data.buildNumber !== reuse.buildNumber || data.commit !== reuse.commit ||
      status.value?.current?.releaseId !== reuse.releaseId)) {
      error.value = "当前版本或 Jenkins 制品已变化。请读取发布记录后重新选择构建。";
      return;
    }
    plan.value = data;
  } catch (problem) {
    if (!lifetime.signal.aborted) error.value = describe(problem, "部署预检失败。请核对构建产物与目标配置。");
  } finally {
    busy.value = "";
  }
}

type DeploymentAction = "submit" | "verify" | "rollback" | "recover" | "decommission";

function confirmOperation(action: "rollback" | "recover" | "decommission") {
  if (action !== "rollback") rollbackTarget.value = undefined;
  if (action !== "recover") recoveryTarget.value = undefined;
  if (action !== "decommission") decommissionTarget.value = undefined;
  if (action === "rollback") rollbackTarget.value = rollbackTarget.value ? undefined : status.value?.previous;
  if (action === "recover") recoveryTarget.value = recoveryTarget.value ? undefined : mutation.value;
  if (action === "decommission") decommissionTarget.value = decommissionTarget.value ? undefined : status.value?.current;
}

async function submit(action: DeploymentAction) {
  const prior = status.value;
  const confirmedPlan = plan.value;
  const confirmedVersion = rollbackTarget.value;
  const confirmedRecovery = recoveryTarget.value;
  const confirmedDecommission = decommissionTarget.value;
  if (!prior || !({
    submit: canDeploy, verify: canCheck, rollback: canDeploy, recover: canRecover, decommission: canDecommission,
  })[action].value) return;
  if (action === "submit" && !confirmedPlan) return;
  if (action === "rollback" && !confirmedVersion) return;
  if (action === "recover" && !confirmedRecovery) return;
  if (action === "decommission" && !confirmedDecommission) return;
  busy.value = action;
  error.value = "";
  try {
    const payload = action === "submit" ? { token: confirmedPlan?.token }
      : action === "rollback" ? { previousReleaseId: confirmedVersion?.releaseId }
        : action === "recover" ? { expectedJobId: confirmedRecovery?.jobId }
          : action === "decommission" ? { expectedReleaseId: confirmedDecommission?.releaseId } : {};
    const receipt = await request<{ jobId: string }>(
      `${base}/${action}`, "POST", payload, lifetime.signal,
    );
    if (lifetime.signal.aborted) return;
    const latest: DeploymentJob = {
      jobId: receipt.jobId, status: "QUEUED", phase: "QUEUED",
      action: { submit: "DEPLOY", verify: "VERIFY", rollback: "ROLLBACK", recover: confirmedRecovery?.action, decommission: "DECOMMISSION" }[action],
      ...(action === "submit" && confirmedPlan ? { buildNumber: confirmedPlan.buildNumber } : {}),
    };
    props.delivery.recordQueued(base, {
      ...(status.value ?? prior), latest, ...(action === "verify" ? { activeCheck: latest } : { pending: latest }),
    });
    plan.value = undefined;
    rollbackTarget.value = undefined;
    recoveryTarget.value = undefined;
    decommissionTarget.value = undefined;
  } catch (problem) {
    if (!lifetime.signal.aborted) error.value = describe(problem, "作业未能确认，请读取发布记录后再试。");
  } finally {
    busy.value = "";
  }
}

function readExecutionLogs(job = lastJob.value) {
  if (job?.jobId) emit("executionLogs", {
    jobId: job.jobId,
    title: `${props.moduleName} · ${props.environment} · ${deploymentActionText(job.action)}执行日志`,
  });
}

async function readLogs() {
  if (busy.value) return;
  busy.value = "logs";
  error.value = "";
  try {
    logs.value = await request<typeof logs.value>(`${base}/logs`, "GET", undefined, lifetime.signal);
  } catch (problem) {
    if (!lifetime.signal.aborted) error.value = describe(problem, "读取服务日志失败，请核对目标服务器。");
  } finally {
    busy.value = "";
  }
}

onMounted(() => {
  healthTimer = setInterval(() => { healthClock.value = Date.now(); }, 15_000);
  void props.delivery.refresh(base, 2500);
});
onUnmounted(() => {
  lifetime.abort();
  if (healthTimer) clearInterval(healthTimer);
});
</script>

<template>
  <ElDialog
    :model-value="true"
    :title="`${moduleName} · ${environment}`"
    width="min(680px, 96vw)"
    append-to-body
    :close-on-click-modal="false"
    :before-close="(done) => { if (!busy) done(); }"
    @close="emit('close')"
  >
    <div v-if="qaMonitor" class="deployment-panel">
      <div class="deployment-actions">
        <ElButton @click="qaMonitor = undefined">返回发布</ElButton>
        <strong>Jenkins 构建与日志</strong>
      </div>
      <ProjectJenkinsBuild
        :project-id="projectId"
        :context="qaContext"
        :monitor="qaMonitor"
        @observed="observeBuild"
        @error="emit('error', $event)"
      />
    </div>
    <ElSkeleton v-else-if="loading" :rows="4" animated />
    <div v-else class="deployment-panel">
      <ElAlert v-if="error || readError" :title="error || readError" type="error" show-icon :closable="false" />
      <ElButton v-if="!status" size="small" @click="refresh()">重新读取</ElButton>
      <template v-else>
        <div class="deployment-target">
          <span>{{ status.current?.hostName || hostName || "未选择服务器" }}</span>
          <ElTag v-if="currentKind" size="small" effect="plain">
            {{ currentKind === "JAVA" ? "Java JAR · systemd" : "Nginx 静态站点" }}
          </ElTag>
          <ElButton link type="primary" :disabled="!!busy" @click="refresh()">读取发布记录</ElButton>
        </div>
        <div v-if="lastJob" class="deployment-block">
          <div class="deployment-actions">
            <strong>最近作业：{{ deploymentActionText(lastJob.action) }}</strong>
            <ElTag :type="lastJob.status === 'SUCCEEDED' ? 'success' : lastJob.status === 'FAILED' ? 'danger' : 'info'">
              {{ jobStatusText(lastJob.status) }}
            </ElTag>
            <span v-if="lastJob.buildNumber">构建 #{{ lastJob.buildNumber }}</span>
            <ElButton v-if="lastJob.jobId" link type="primary" @click="readExecutionLogs()">执行日志</ElButton>
          </div>
          <small v-if="lastJob.phase || lastJob.observedAt || lastJob.errorCode || lastJob.traceId">
            {{ [deploymentPhaseText(lastJob.phase), deploymentTime(lastJob.observedAt), lastJob.errorCode, lastJob.traceId].filter(Boolean).join(" · ") }}
          </small>
          <span v-if="lastJobError">{{ lastJobError }}</span>
        </div>
        <div class="deployment-block">
          <strong>线上版本</strong>
          <dl class="deployment-versions">
            <div>
              <dt>当前版本</dt>
              <dd v-if="status.current">
                #{{ status.current.buildNumber }} · <code>{{ status.current.commit.slice(0, 12) }}</code>
                <small>{{ deploymentTime(status.current.observedAt) }}</small>
              </dd>
              <dd v-else>{{ lastJob?.action === "DECOMMISSION" && lastJob.status === "SUCCEEDED" ? "已下线" : "没有已确认的线上版本" }}</dd>
            </div>
            <div v-if="status.previous">
              <dt>上一版本</dt>
              <dd>
                #{{ status.previous.buildNumber }} · <code>{{ status.previous.commit.slice(0, 12) }}</code>
                <small>{{ deploymentTime(status.previous.observedAt) }}</small>
              </dd>
            </div>
          </dl>
          <div v-if="status.current" class="deployment-actions">
            <ElButton v-if="status.current" size="small" :disabled="!!busy" @click="readLogs">服务日志</ElButton>
            <ElButton v-if="status.previous && !archived" size="small" :disabled="!canDeploy" @click="confirmOperation('rollback')">回退上一版</ElButton>
            <ElButton size="small" :disabled="!canDecommission" @click="confirmOperation('decommission')">下线实例</ElButton>
          </div>
          <div v-if="rollbackTarget" class="deployment-actions">
            <span>回退至构建 #{{ rollbackTarget.buildNumber }}？</span>
            <ElButton size="small" @click="rollbackTarget = undefined">取消</ElButton>
            <ElButton size="small" type="danger" :disabled="!canDeploy" :loading="busy === 'rollback'" @click="submit('rollback')">确认回退</ElButton>
          </div>
          <div v-if="decommissionTarget" class="deployment-plan">
            <strong>下线 {{ decommissionTarget.hostName || hostName }} 上的构建 #{{ decommissionTarget.buildNumber }}？</strong>
            <span>{{ decommissionTarget.kind === "STATIC" ? "当前站点将从 Nginx 移除。" : "当前服务将停止并移除受管服务单元。" }}业务数据和发布记录会保留。</span>
            <div class="deployment-actions">
              <ElButton size="small" @click="decommissionTarget = undefined">取消</ElButton>
              <ElButton size="small" type="danger" :disabled="!canDecommission" :loading="busy === 'decommission'" @click="submit('decommission')">确认下线</ElButton>
            </div>
          </div>
        </div>
        <div v-if="hasCheckTarget || status.lastCheck" class="deployment-block">
          <div class="deployment-actions">
            <strong>现场检查</strong>
            <ElTag size="small" :type="health === 'READY' ? 'success' : health === 'UNHEALTHY' ? 'danger' : 'info'">
              {{ deploymentHealthText(health) }}
            </ElTag>
            <ElButton size="small" :disabled="!canCheck" :loading="busy === 'verify'" @click="submit('verify')">立即检查</ElButton>
          </div>
          <small>{{ status.healthObservedAt ? `检查时间：${deploymentTime(status.healthObservedAt)} · ` : "" }}从目标机检查服务，不会重启或修改配置。</small>
          <small v-if="status.activeCheck">{{ jobStatusText(status.activeCheck.status) }}</small>
          <div v-else-if="status.lastCheck" class="deployment-actions">
            <small>{{ [status.lastCheck.health ? deploymentHealthText(deploymentHealthAt(status.lastCheck.health, status.lastCheck.observedAt, healthClock)) : jobStatusText(status.lastCheck.status), deploymentTime(status.lastCheck.observedAt), status.lastCheck.errorCode].filter(Boolean).join(" · ") }}</small>
            <ElButton link type="primary" @click="readExecutionLogs(status.lastCheck)">检查日志</ElButton>
          </div>
          <ElAlert v-if="needsRecovery" title="上一次变更的结果待核对。可先只读检查，然后确认是否恢复。" type="warning" show-icon :closable="false" />
          <div v-if="needsRecovery" class="deployment-actions">
            <ElButton size="small" :disabled="!canRecover" @click="confirmOperation('recover')">恢复未完成操作</ElButton>
            <ElButton link type="primary" @click="readExecutionLogs(mutation)">原作业日志</ElButton>
          </div>
          <div v-if="recoveryTarget" class="deployment-plan">
            <strong>{{ deploymentActionText(recoveryTarget.action) }} · {{ recoveryTarget.jobId }}</strong>
            <span>{{ recoveryTarget.action === "DECOMMISSION" ? "将继续下线此实例。" : "将按目标机记录完成恢复，必要时恢复上一版、重启服务或重载 Nginx。" }}</span>
            <div class="deployment-actions">
              <ElButton size="small" @click="recoveryTarget = undefined">取消</ElButton>
              <ElButton size="small" type="warning" :disabled="!canRecover" :loading="busy === 'recover'" @click="submit('recover')">确认恢复</ElButton>
            </div>
          </div>
        </div>
        <ElEmpty v-if="!supported" description="此模块暂不支持按项目草稿部署" :image-size="56" />
        <div v-if="siteConfigPath" class="deployment-block">
          <strong>站点配置</strong>
          <span>服务器上的生效文件：<code class="site-config-path">{{ siteConfigPath }}</code></span>
          <template v-if="status.current?.serverName && status.current?.kind === 'STATIC' && status.current?.siteHealthPath">
            <small>站点域名或 IP：<code>{{ status.current.serverName }}</code><template v-if="status.current.port"> · Nginx 监听端口：{{ status.current.port }}</template></small>
            <small>整站验收：使用网关配置的实际访问地址，核对 <code>{{ status.current.siteHealthPath }}</code> 返回 JSON <code>UP</code>，再测试登录、页面刷新和预览首帧。</small>
          </template>
          <template v-if="!archived">
            <small>保存站点配置后，如果 Jenkins 仍保留兼容的归档，可用当前构建让配置生效。</small>
            <div class="deployment-actions">
              <ElButton size="small" :disabled="!canDeploy" @click="plan = undefined; emit('editSite')">编辑站点配置</ElButton>
              <ElButton size="small" :disabled="!canDeploy" @click="preflightCurrent">用当前构建预检</ElButton>
            </div>
          </template>
        </div>
        <div v-if="supported && !archived" class="deployment-block">
          <strong>发布新版本</strong>
          <div class="deployment-actions">
            <ElButton :disabled="!canDeploy" :loading="busy === 'qa'" @click="openQa">构建</ElButton>
            <ElInputNumber
              :model-value="selectedNumber"
              :min="1"
              :max="2147483647"
              :precision="0"
              :disabled="!canDeploy"
              aria-label="QA 构建号"
              placeholder="QA 构建号"
              @update:model-value="chooseNumber"
            />
            <ElButton :disabled="!canDeploy || !selectedNumber" :loading="busy === 'build' && !!selectedNumber" @click="readBuild()">核对构建</ElButton>
            <ElButton :disabled="!canDeploy" @click="readBuild(true)">读取最近构建</ElButton>
          </div>
          <small v-if="selectedBuild && !buildError">构建 #{{ selectedBuild.number }} · {{ jenkinsState(selectedBuild.status).label }}</small>
          <ElAlert v-if="buildError" :title="buildError" type="warning" show-icon :closable="false" />
          <div v-if="plan" class="deployment-plan">
            <strong>{{ plan.moduleName }} · {{ plan.environment }} · {{ plan.hostName }}</strong>
            <span>构建 #{{ plan.buildNumber }} · <code>{{ plan.commit.slice(0, 12) }}</code></span>
            <ul v-if="plan.summary?.length">
              <li v-for="(line, index) in plan.summary" :key="index">{{ line }}</li>
            </ul>
          </div>
          <div class="deployment-actions">
            <ElButton v-if="plan" type="primary" :disabled="!canDeploy" :loading="busy === 'submit'" @click="submit('submit')">确认发布</ElButton>
            <ElButton v-else type="primary" :disabled="!canDeploy || !qualifies" :loading="busy === 'preflight'" @click="preflight">部署预检</ElButton>
          </div>
        </div>
        <div v-if="logs" class="deployment-block">
          <div class="deployment-actions">
            <strong>服务日志</strong>
            <small>{{ deploymentTime(logs.observedAt) }}</small>
            <ElButton size="small" :loading="busy === 'logs'" @click="readLogs">刷新日志</ElButton>
            <ElButton link type="primary" @click="logs = undefined">收起</ElButton>
          </div>
          <pre>{{ logs.output || "暂无日志" }}</pre>
        </div>
      </template>
    </div>
  </ElDialog>
</template>

<style scoped>
.deployment-panel { display: grid; gap: 16px; }
.deployment-target, .deployment-actions { display: flex; flex-wrap: wrap; align-items: center; gap: 8px; }
.deployment-block, .deployment-plan { display: grid; gap: 12px; border: 1px solid var(--el-border-color); border-radius: 8px; padding: 12px; min-width: 0; }
.deployment-block small { color: var(--el-text-color-secondary); }
.site-config-path { overflow-wrap: anywhere; }
.deployment-versions { display: grid; grid-template-columns: repeat(auto-fit, minmax(144px, 1fr)); gap: 8px; margin: 0; }
.deployment-versions div { display: grid; gap: 4px; }
.deployment-versions dt { color: var(--el-text-color-secondary); }
.deployment-versions dd { margin: 0; }
.deployment-versions small { display: block; }
.deployment-plan { background: var(--el-fill-color-light); }
.deployment-plan ul { margin: 0; padding-inline-start: 24px; }
pre { margin: 0; padding: 8px; max-height: 32vh; overflow: auto; background: var(--el-fill-color-light); border-radius: 4px; white-space: pre-wrap; overflow-wrap: anywhere; }
</style>
