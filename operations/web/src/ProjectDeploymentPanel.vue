<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref } from "vue";
import {
  ElAlert, ElButton, ElDialog, ElEmpty, ElInputNumber, ElSkeleton, ElTag,
} from "element-plus";
import { ApiError, request } from "./api";
import { jenkinsState, moduleJenkinsPath, type JenkinsBuild, type JenkinsMonitor } from "./projectJenkins";
import type { LogSelection } from "./jobs";
import ProjectJenkinsBuild from "./ProjectJenkinsBuild.vue";
import {
  deploymentActionText, deploymentHealthText, deploymentPhaseText, deploymentTime,
  isPublishableBuild, jobPending, jobStatusText, projectDeploymentErrorText, projectDeploymentPath,
  type DeploymentJob, type DeploymentPlan, type DeploymentStatus, type DeploymentVersion,
} from "./projectDeployments";

const props = defineProps<{
  projectId: string;
  moduleId: string;
  moduleName: string;
  deploymentId: string;
  environment: string;
  hostName: string;
  archived?: boolean;
}>();
const emit = defineEmits<{
  close: [];
  error: [error: unknown];
  observed: [status: DeploymentStatus];
  buildObserved: [build: JenkinsBuild];
  executionLogs: [selection: LogSelection];
  editSite: [];
}>();

const lifetime = new AbortController();
const base = projectDeploymentPath(props.projectId, props.deploymentId);
const qaBase = moduleJenkinsPath(props.projectId, props.moduleId);
const qaPath = `${qaBase}/jenkins-build`;
const status = ref<DeploymentStatus>();
const loading = ref(true);
const busy = ref("");
const error = ref("");
const buildError = ref("");
const selectedNumber = ref<number>();
const selectedBuild = ref<JenkinsBuild>();
const qaMonitor = ref<JenkinsMonitor>();
const plan = ref<DeploymentPlan>();
const logs = ref<{ output: string; observedAt: string }>();
const rollbackTarget = ref<DeploymentVersion>();
let pollTimer: ReturnType<typeof setTimeout> | undefined;
let refreshRevision = 0;

const lastJob = computed(() => status.value?.latest);
const lastJobError = computed(() => projectDeploymentErrorText(lastJob.value?.errorCode));
const pending = computed(() => jobPending(lastJob.value));
const needsCheck = computed(() => lastJob.value?.status === "UNKNOWN");
const supported = computed(() => status.value?.kind === "JAVA" || status.value?.kind === "STATIC");
const available = computed(() => !pending.value && !busy.value);
const hasCheckTarget = computed(() => !!status.value?.current || needsCheck.value);
const canCheck = computed(() => available.value && hasCheckTarget.value);
const canDeploy = computed(() => supported.value && available.value && !props.archived && !needsCheck.value);
const siteConfigPath = computed(() => status.value?.kind === "STATIC" && status.value.current
  ? `/etc/nginx/conf.d/sfops-project-${props.deploymentId}.conf` : "");
const qualifies = computed(() =>
  isPublishableBuild(selectedBuild.value) && selectedBuild.value?.number === selectedNumber.value,
);
const qaContext = {
  action: "qa" as const,
  moduleId: props.moduleId,
  moduleName: props.moduleName,
};

function describe(problem: unknown, fallback: string) {
  if (problem instanceof ApiError) {
    if (problem.code === "AUTH_REQUIRED") emit("error", problem);
    return [problem.message, problem.code, problem.traceId, deploymentTime(problem.timestamp)].filter(Boolean).join(" · ");
  }
  return fallback;
}

function schedulePoll(delay = 2500) {
  if (pollTimer) clearTimeout(pollTimer);
  if (lifetime.signal.aborted) return;
  pollTimer = setTimeout(async () => {
    if (document.hidden || busy.value) return schedulePoll();
    await refresh();
  }, delay);
}

async function refresh(expectedJob?: string) {
  const revision = ++refreshRevision;
  try {
    const data = await request<DeploymentStatus>(base, "GET", undefined, lifetime.signal);
    if (lifetime.signal.aborted || refreshRevision !== revision) return;
    if (expectedJob && data.latest?.jobId !== expectedJob && lastJob.value?.jobId === expectedJob)
      data.latest = lastJob.value;
    status.value = data;
    error.value = "";
    if (data.latest?.status === "UNKNOWN") {
      plan.value = undefined;
      rollbackTarget.value = undefined;
    }
    emit("observed", data);
    if (jobPending(data.latest)) schedulePoll();
  } catch (problem) {
    if (lifetime.signal.aborted || refreshRevision !== revision) return;
    error.value = describe(problem, "读取部署状态失败，请刷新后重试。");
    if (pending.value) schedulePoll(5000);
  } finally {
    if (refreshRevision === revision) loading.value = false;
  }
}

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
      error.value = "当前版本或 Jenkins 制品已变化。请刷新状态后重新选择构建。";
      return;
    }
    plan.value = data;
  } catch (problem) {
    if (!lifetime.signal.aborted) error.value = describe(problem, "部署预检失败。请核对构建产物与目标配置。");
  } finally {
    busy.value = "";
  }
}

async function submit(action: "submit" | "verify" | "rollback") {
  const prior = status.value;
  const confirmedPlan = plan.value;
  const confirmedVersion = rollbackTarget.value;
  if (!prior || !(action === "verify" ? canCheck.value : canDeploy.value)) return;
  if (action === "submit" && !confirmedPlan) return;
  if (action === "rollback" && !confirmedVersion) return;
  busy.value = action;
  error.value = "";
  try {
    const payload = action === "submit" ? { token: confirmedPlan?.token }
      : action === "rollback" ? { previousReleaseId: confirmedVersion?.releaseId } : {};
    const receipt = await request<{ jobId: string }>(
      `${base}/${action}`, "POST", payload, lifetime.signal,
    );
    if (lifetime.signal.aborted) return;
    const latest: DeploymentJob = {
      jobId: receipt.jobId, status: "QUEUED", phase: "QUEUED",
      action: { submit: "DEPLOY", verify: "VERIFY", rollback: "ROLLBACK" }[action],
      ...(action === "submit" && confirmedPlan ? { buildNumber: confirmedPlan.buildNumber } : {}),
    };
    status.value = { ...prior, latest };
    emit("observed", status.value);
    plan.value = undefined;
    rollbackTarget.value = undefined;
    await refresh(receipt.jobId);
  } catch (problem) {
    if (!lifetime.signal.aborted) error.value = describe(problem, "作业未能确认，请刷新部署状态后再试。");
  } finally {
    busy.value = "";
  }
}

function readExecutionLogs() {
  const job = lastJob.value;
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

onMounted(() => { void refresh(); });
onUnmounted(() => {
  lifetime.abort();
  if (pollTimer) clearTimeout(pollTimer);
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
      <ElAlert v-if="error" :title="error" type="error" show-icon :closable="false" />
      <ElButton v-if="!status" size="small" @click="refresh()">重新读取</ElButton>
      <template v-else>
        <div class="deployment-target">
          <span>{{ hostName || "未选择服务器" }}</span>
          <ElTag v-if="supported" size="small" effect="plain">
            {{ status.kind === "JAVA" ? "Java JAR · systemd" : "Nginx 静态站点" }}
          </ElTag>
          <ElTag v-if="supported" size="small" :type="status.health === 'READY' ? 'success' : status.health === 'UNHEALTHY' ? 'danger' : 'info'">
            {{ deploymentHealthText(status.health) }}
          </ElTag>
          <ElButton link type="primary" :disabled="!!busy" @click="error = ''; refresh()">刷新状态</ElButton>
        </div>
        <div v-if="lastJob" class="deployment-block">
          <div class="deployment-actions">
            <strong>最近作业：{{ deploymentActionText(lastJob.action) }}</strong>
            <ElTag :type="lastJob.status === 'SUCCEEDED' ? 'success' : lastJob.status === 'FAILED' ? 'danger' : 'info'">
              {{ jobStatusText(lastJob.status) }}
            </ElTag>
            <span v-if="lastJob.buildNumber">构建 #{{ lastJob.buildNumber }}</span>
            <ElButton v-if="lastJob.jobId" link type="primary" @click="readExecutionLogs">执行日志</ElButton>
          </div>
          <small v-if="lastJob.phase || lastJob.observedAt || lastJob.errorCode || lastJob.traceId">
            {{ [deploymentPhaseText(lastJob.phase), deploymentTime(lastJob.observedAt), lastJob.errorCode, lastJob.traceId].filter(Boolean).join(" · ") }}
          </small>
          <span v-if="lastJobError">{{ lastJobError }}</span>
          <ElAlert v-if="needsCheck" title="上一次操作结果待核对。请先检查服务，再发布或回退。" type="warning" show-icon :closable="false" />
        </div>
        <div class="deployment-block">
          <strong>发布记录</strong>
          <dl class="deployment-versions">
            <div>
              <dt>当前版本</dt>
              <dd v-if="status.current">
                #{{ status.current.buildNumber }} · <code>{{ status.current.commit.slice(0, 12) }}</code>
                <small>{{ deploymentTime(status.current.observedAt) }}</small>
              </dd>
              <dd v-else>尚未发布</dd>
            </div>
            <div v-if="status.previous">
              <dt>上一版本</dt>
              <dd>
                #{{ status.previous.buildNumber }} · <code>{{ status.previous.commit.slice(0, 12) }}</code>
                <small>{{ deploymentTime(status.previous.observedAt) }}</small>
              </dd>
            </div>
          </dl>
          <div v-if="hasCheckTarget" class="deployment-actions">
            <ElButton size="small" :disabled="!canCheck" @click="submit('verify')">检查服务</ElButton>
            <ElButton v-if="status.current" size="small" :disabled="!!busy" @click="readLogs">服务日志</ElButton>
            <ElButton v-if="status.previous && !archived" size="small" :disabled="!canDeploy" @click="rollbackTarget = rollbackTarget ? undefined : status.previous">回退上一版</ElButton>
          </div>
          <div v-if="rollbackTarget" class="deployment-actions">
            <span>回退至构建 #{{ rollbackTarget.buildNumber }}？</span>
            <ElButton size="small" @click="rollbackTarget = undefined">取消</ElButton>
            <ElButton size="small" type="danger" :disabled="!canDeploy" :loading="busy === 'rollback'" @click="submit('rollback')">确认回退</ElButton>
          </div>
        </div>
        <ElEmpty v-if="!supported" description="此模块暂不支持按项目草稿部署" :image-size="56" />
        <div v-if="siteConfigPath" class="deployment-block">
          <strong>站点配置</strong>
          <span>服务器上的生效文件：<code class="site-config-path">{{ siteConfigPath }}</code></span>
          <template v-if="!archived">
            <small>保存站点配置后，可用当前构建发布，让新配置生效。</small>
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
