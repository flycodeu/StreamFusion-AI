<script setup lang="ts">
import { ElDialog } from "element-plus";
import type { Host } from "./hosts";
import type { ProjectReleaseSource } from "./applicationProjects";
import OpsPagination from "./OpsPagination.vue";
import PreviewConfigForm from "./PreviewConfigForm.vue";
import type { PreviewConfig } from "./preview";
import ZLMConfigForm from "./ZLMConfigForm.vue";
import type { ZLMConfig } from "./zlm";
import RuntimePolicyFields from "./RuntimePolicyFields.vue";
import {
  runtimePolicyStopConfirmed,
  runtimePolicySummary,
  type RuntimePolicy,
  type RuntimePolicyState,
} from "./runtimePolicy";
import { softwareJobDiagnostic, softwareJobErrorMessages } from "./software";

export interface WorkspaceRuntimeEnvironment {
  installationId: string;
  pluginVersion: string;
  imageDigest: string;
}
export interface WorkspaceArtifact {
  recipe: string;
  id: string;
  name: string;
  imageId: string;
  archiveSha256: string;
  byteSize: number;
  available: boolean;
  runtimeEnvironment?: WorkspaceRuntimeEnvironment | null;
}
export interface WorkspaceRuntimeInstallation {
  id: string;
  targetId: string;
  pluginKey: string;
  pluginVersion: string;
  status: string;
}
export interface WorkspaceCredential {
  id: string;
  revision: string;
  name: string;
  kind: string;
  revokedAt: string;
}
export interface WorkspaceBinding {
  id: string;
  kind: string;
  host: string;
  port: number;
  database: string;
  username: string;
  credentialId: string;
  credentialRevision: string;
}
export interface WorkspaceConfig {
  runtimePolicy?: RuntimePolicy;
  preview?: PreviewConfig;
  zlm?: ZLMConfig;
  initialPasswordCredentialId?: string;
  initialPasswordRevision?: string;
  environment: string;
  profile: string;
  bindAddress: string;
  hostPort: number;
  containerPort: number;
  memoryMiB: number;
  healthPath: string;
  healthSeconds: number;
  dataCompatibility: string;
  bindingIds: string[];
}
export interface WorkspaceInstance {
  pendingJob?: { id: string; status: string; kind: string; targetAckPending: boolean };
  id: string;
  name: string;
  targetId: string;
  version: string;
  draftRevision: string;
  appliedGeneration: string;
  config: WorkspaceConfig;
  currentReleaseId: string;
  previousReleaseId: string;
  observation: {
    runtimePolicy?: RuntimePolicyState;
    health?: string;
    releaseId?: string;
    preview?: {
      state: string;
      configVersion: string;
      maxSessions: number;
      maxTranscodes: number;
    };
  };
  observedAt: string;
}
export interface WorkspaceRelease {
  runtimeEnvironment?: WorkspaceRuntimeEnvironment;
  id: string;
  imageId: string;
  artifactId: string;
  config: WorkspaceConfig;
  bindings?: WorkspaceBinding[];
}
export interface WorkspacePlan {
  id: string;
  kind: string;
  candidate: WorkspaceRelease;
  baseline?: WorkspaceRelease;
  expiresAt: string;
  expectedVersion: string;
  expectedGeneration: string;
  maintenancePermit?: string;
}
export interface WorkspaceJob {
  id: string;
  kind: string;
  status: string;
  phase: string;
  errorCode: string;
  targetAckPending: boolean;
  traceId: string;
  createdAt: string;
  updatedAt: string;
  result: Record<string, unknown>;
}

defineProps<{
  dialog: string;
  busy: boolean;
  optionsLoading: boolean;
  bindingDraft: {
    kind: string;
    host: string;
    port: number;
    database: string;
    username: string;
    credentialId: string;
    credentialRevision: string;
  };
  credentialOptions: WorkspaceCredential[];
  recipe: string;
  selected?: WorkspaceInstance;
  instanceDraft: {
    name: string;
    targetId: string;
    version: string;
    config: WorkspaceConfig;
  };
  targets: Host[];
  bindingOptions: WorkspaceBinding[];
  runtimePolicyDraft?: RuntimePolicy;
  runtimePolicyDirty: boolean;
  runtimePolicyAttempted: boolean;
  runtimePolicyErrorMessage: string;
  importSource: string;
  importRecipe: string;
  importRuntime: string;
  importName: string;
  importSHA: string;
  uploadPhase: string;
  runtimeInstallations: WorkspaceRuntimeInstallation[];
  planKind: string;
  artifactChoice: string;
  eligibleArtifacts: WorkspaceArtifact[];
  planInstance?: WorkspaceInstance;
  projectRelease?: ProjectReleaseSource;
  maintenancePermit: string;
  plan?: WorkspacePlan;
  artifactOptions: WorkspaceArtifact[];
  releases: WorkspaceRelease[];
  releasePage: number;
  releasePageSize: number;
  releaseTotal: number;
  releasesLoading: boolean;
  registration: unknown;
  logs: string;
  detail?: WorkspaceJob;
  statusLabels: Record<string, string>;
  phaseLabels: Record<string, string>;
}>();

const emit = defineEmits<{
  close: [];
  saveBinding: [];
  saveInstance: [];
  recipeChanged: [];
  "update:recipe": [value: string];
  "update:importSource": [value: string];
  "update:importRecipe": [value: string];
  "update:importRuntime": [value: string];
  "update:importName": [value: string];
  "update:importSHA": [value: string];
  "update:artifactChoice": [value: string];
  "update:maintenancePermit": [value: string];
  pickFile: [event: Event];
  importArtifact: [];
  applyRuntimePolicy: [];
  editRuntimePolicy: [value?: RuntimePolicy];
  openRuntimePolicy: [instance: WorkspaceInstance];
  instanceAction: [instance: WorkspaceInstance, action: "START" | "STOP" | "RESTART"];
  preview: [];
  submit: [];
  changeReleasePage: [page: number];
  changeReleaseSize: [size: number];
  reconcile: [job: WorkspaceJob];
  cancelJob: [job: WorkspaceJob];
}>();

const time = (v: string) =>
  v
    ? new Date(v).toLocaleString("zh-CN", {
        timeZone: "Asia/Shanghai",
        hour12: false,
      })
    : "—";

const jobStatus = (job: WorkspaceJob) =>
  job.phase === "CANCELLED" ? "CANCELLED" : job.status;

function credentialSelected(
  d: { credentialId: string; credentialRevision: string },
  e: Event,
) {
  const key = (e.target as HTMLSelectElement).value;
  [d.credentialId, d.credentialRevision] = key ? key.split(":") : ["", ""];
}

function initialPasswordSelected(config: WorkspaceConfig, e: Event) {
  const value = (e.target as HTMLSelectElement).value;
  const [id, revision] = value ? value.split(":") : ["", ""];
  config.initialPasswordCredentialId = id;
  config.initialPasswordRevision = revision;
}

function onImportSourceChange(e: Event, currentRecipe: string) {
  const value = (e.target as HTMLSelectElement).value;
  emit("update:importSource", value);
  emit("update:importRecipe", value === "JAR" ? "java-compose-v1" : currentRecipe);
  emit("update:importSHA", "");
}
</script>

<template>
  <ElDialog
    :model-value="!!dialog"
    class="ops-workspace-dialog"
    :title="
      (
        {
          binding: '绑定已有依赖',
          instance: '实例配置草稿',
          'runtime-policy': '应用运行管理',
          registration: '预览节点登记与维护',
          import: '导入制品',
          candidate: '选择候选',
          plan: '发布计划',
          history: '版本记录',
          logs: '实例日志',
          job: '作业详情',
        } as Record<string, string>
      )[dialog]
    "
    width="min(640px,94vw)"
    align-center
    @close="emit('close')"
  >
    <form
      v-if="dialog === 'binding'"
      class="dialog-form"
      @submit.prevent="emit('saveBinding')"
    >
      <div class="form-grid">
        <label
          >类型<select
            v-model="bindingDraft.kind"
            aria-label="类型"
            @change="
              bindingDraft.port = bindingDraft.kind === 'MYSQL' ? 3306 : 6379
            "
          >
            <option>MYSQL</option>
            <option>REDIS</option>
          </select></label
        ><label
          >端口<input v-model.number="bindingDraft.port" type="number" required
        /></label>
      </div>
      <label>地址<input v-model="bindingDraft.host" required /></label>
      <div class="form-grid">
        <label>数据库<input v-model="bindingDraft.database" /></label
        ><label>用户<input v-model="bindingDraft.username" /></label>
      </div>
      <label
        >密码修订<select @change="credentialSelected(bindingDraft, $event)">
          <option value="">无密码</option>
          <option
            v-for="c in credentialOptions.filter((c) => c.kind === 'PASSWORD')"
            :key="c.id + c.revision"
            :value="c.id + ':' + c.revision"
          >
            {{ c.name }} r{{ c.revision }}
          </option>
        </select></label
      >
      <div class="dialog-footer">
        <button
          type="button"
          class="secondary"
          :disabled="busy"
          @click="emit('close')"
        >
          取消
        </button>
        <button class="primary" :disabled="busy">登记绑定</button>
      </div>
    </form>
    <form
      v-else-if="dialog === 'instance'"
      class="dialog-form"
      @submit.prevent="emit('saveInstance')"
    >
      <label
        >部署配方<select
          :value="recipe"
          :disabled="!!selected"
          @change="
            emit('update:recipe', ($event.target as HTMLSelectElement).value);
            emit('recipeChanged');
          "
        >
          <option value="java-compose-v1">Java 服务</option>
          <option value="preview-node-v1">预览节点 · ZLM / FFmpeg</option>
          <option value="zlm-direct-v1">独立 ZLM</option>
        </select></label
      >
      <label>实例名称<input v-model="instanceDraft.name" required /></label
      ><label
        >目标<select
          v-model="instanceDraft.targetId"
          :disabled="!!selected"
          required
        >
          <option v-for="t in targets" :key="t.id" :value="t.id">
            {{ t.name }}
          </option>
        </select></label
      >
      <template v-if="instanceDraft.config.zlm">
        <div class="form-grid">
          <label
            >目标绑定 IPv4<input
              v-model="instanceDraft.config.bindAddress"
              :disabled="!!selected?.currentReleaseId"
              required
          /></label>
          <label
            >内存 MiB<input
              v-model.number="instanceDraft.config.memoryMiB"
              type="number"
              min="512"
              max="65536"
              required
          /></label>
        </div>
        <ZLMConfigForm
          v-model="instanceDraft.config.zlm"
          :credentials="credentialOptions"
          :deployed="!!selected?.currentReleaseId"
        />
      </template>
      <template v-else-if="instanceDraft.config.preview">
        <div class="form-grid">
          <label
            >目标绑定 IPv4<input
              v-model="instanceDraft.config.bindAddress"
              :disabled="!!selected?.currentReleaseId"
              required /></label
          ><label
            >内存 MiB<input
              v-model.number="instanceDraft.config.memoryMiB"
              type="number"
              min="512"
              max="65536"
              required
          /></label>
        </div>
        <PreviewConfigForm
          v-model="instanceDraft.config.preview"
          :deployed="!!selected?.currentReleaseId"
        />
      </template>
      <template v-else>
        <div class="form-grid">
          <label
            >环境标签<input
              v-model="instanceDraft.config.environment"
              required /></label
          ><label
            >Spring profile<input
              v-model="instanceDraft.config.profile" /></label
          ><label
            >绑定地址<input
              v-model="instanceDraft.config.bindAddress"
              required /></label
          ><label
            >宿主端口<input
              v-model.number="instanceDraft.config.hostPort"
              type="number"
              min="1024"
              max="65535"
              required /></label
          ><label
            >容器端口<input
              v-model.number="instanceDraft.config.containerPort"
              type="number"
              min="1024"
              max="65535"
              required /></label
          ><label
            >内存 MiB<input
              v-model.number="instanceDraft.config.memoryMiB"
              type="number"
              min="256"
              max="8192"
              required
          /></label>
        </div>
        <label
          >外部依赖<select v-model="instanceDraft.config.bindingIds" multiple>
            <option v-for="b in bindingOptions" :key="b.id" :value="b.id">
              {{ b.kind }} · {{ b.host }}:{{ b.port }}
            </option>
          </select></label
        >
        <details>
          <summary>健康与恢复</summary>
          <div class="form-grid">
            <label
              >应用初始密码修订<select
                :value="
                  instanceDraft.config.initialPasswordCredentialId
                    ? instanceDraft.config.initialPasswordCredentialId +
                      ':' +
                      instanceDraft.config.initialPasswordRevision
                    : ''
                "
                @change="initialPasswordSelected(instanceDraft.config, $event)"
              >
                <option value="">不注入</option>
                <option
                  v-for="c in credentialOptions.filter(
                    (c) => c.kind === 'PASSWORD',
                  )"
                  :key="c.id + c.revision"
                  :value="c.id + ':' + c.revision"
                >
                  {{ c.name }} r{{ c.revision }}
                </option>
              </select></label
            >
            <label
              >健康路径<input
                v-model="instanceDraft.config.healthPath"
                required /></label
            ><label
              >健康超时 秒<input
                v-model.number="instanceDraft.config.healthSeconds"
                type="number"
                min="5"
                max="120"
                required /></label
            ><label
              >数据兼容标识<input
                v-model="instanceDraft.config.dataCompatibility"
                required
            /></label>
          </div>
        </details>
      </template>
      <RuntimePolicyFields
        v-if="!instanceDraft.config.preview && !instanceDraft.config.zlm"
        v-model="instanceDraft.config.runtimePolicy"
        kind="DOCKER"
        :disabled="busy"
        optional
        inherit-label="沿用当前实例策略"
      />
      <div class="dialog-footer">
        <button
          type="button"
          class="secondary"
          :disabled="busy"
          @click="emit('close')"
        >
          取消
        </button>
        <button class="primary" :disabled="busy">保存草稿</button>
      </div>
    </form>
    <form
      v-else-if="dialog === 'runtime-policy' && selected"
      class="dialog-form"
      @submit.prevent="emit('applyRuntimePolicy')"
    >
      <div class="runtime-actions">
        <strong>{{ selected.name }}</strong
        ><span class="spacer"></span
        ><button
          type="button"
          class="secondary"
          :disabled="busy || runtimePolicyDirty"
          @click="emit('instanceAction', selected, 'START')"
        >
          启动</button
        ><button
          type="button"
          class="secondary"
          :disabled="busy || runtimePolicyDirty"
          @click="emit('instanceAction', selected, 'STOP')"
        >
          停止</button
        ><button
          type="button"
          class="secondary"
          :disabled="busy || runtimePolicyDirty"
          @click="emit('instanceAction', selected, 'RESTART')"
        >
          重启
        </button>
      </div>
      <p v-if="selected.observation.runtimePolicy?.held">
        {{
          runtimePolicyDraft &&
          runtimePolicyStopConfirmed(
            selected.observation.runtimePolicy,
            selected.observation.health === "STOPPED",
            "DOCKER",
          )
            ? "已保持停止，修改策略后仍保持停止。"
            : "停止结果待核对"
        }}
      </p>
      <p v-else>
        {{
          runtimePolicySummary(selected.observation.runtimePolicy?.actual, "DOCKER")
        }}
      </p>
      <RuntimePolicyFields
        v-if="runtimePolicyDraft"
        :model-value="runtimePolicyDraft"
        kind="DOCKER"
        :stop-timeout-editable="false"
        :disabled="
          busy ||
          runtimePolicyAttempted ||
          !selected.observation.runtimePolicy?.editable
        "
        @update:model-value="emit('editRuntimePolicy', $event)"
      />
      <p v-else class="muted">
        {{ busy ? "正在读取当前策略…" : "尚未确认运行策略，请重新读取" }}
      </p>
      <p
        v-if="
          selected.observation.runtimePolicy &&
          !selected.observation.runtimePolicy.editable
        "
        class="muted"
      >
        当前运行对象不支持修改策略
      </p>
      <p v-if="runtimePolicyErrorMessage" class="error" role="alert">
        {{ runtimePolicyErrorMessage }}
      </p>
      <div class="dialog-footer">
        <button
          type="button"
          class="secondary"
          :disabled="busy"
          @click="emit('close')"
        >
          关闭</button
        ><button
          type="button"
          class="secondary"
          :disabled="busy || runtimePolicyAttempted"
          @click="emit('openRuntimePolicy', selected)"
        >
          重新读取</button
        ><button
          class="primary"
          :disabled="
            busy ||
            !runtimePolicyDirty ||
            !selected.observation.runtimePolicy?.editable
          "
        >
          {{ runtimePolicyAttempted ? "重试原请求" : "应用策略" }}
        </button>
      </div>
    </form>
    <form
      v-else-if="dialog === 'import'"
      class="dialog-form"
      @submit.prevent="emit('importArtifact')"
    >
      <label
        >来源<select
          :value="importSource"
          aria-label="来源"
          @change="onImportSourceChange($event, importRecipe)"
        >
          <option value="DOCKER_ARCHIVE">完整 Docker 镜像</option>
          <option value="JAR">可执行 JAR + 已安装 Java21</option>
        </select></label
      >
      <label
        >配方<select
          :value="importRecipe"
          aria-label="配方"
          :disabled="importSource === 'JAR'"
          @change="
            emit('update:importRecipe', ($event.target as HTMLSelectElement).value)
          "
        >
          <option value="java-compose-v1">Java 服务</option>
          <option value="preview-node-v1">预览节点</option>
          <option value="zlm-direct-v1">独立 ZLM</option>
        </select></label
      >
      <label v-if="importSource === 'JAR'"
        >Java21 运行环境<select
          :value="importRuntime"
          aria-label="Java21 运行环境"
          required
          @change="
            emit(
              'update:importRuntime',
              ($event.target as HTMLSelectElement).value,
            )
          "
        >
          <option value="">选择已校验就绪环境</option>
          <option
            v-for="runtime in runtimeInstallations.filter(
              (item) =>
                item.pluginKey === 'java-runtime' &&
                item.pluginVersion === '21-jre' &&
                item.status === 'READY',
            )"
            :key="runtime.id"
            :value="runtime.id"
          >
            {{
              targets.find((target) => target.id === runtime.targetId)?.name ||
              runtime.targetId
            }}
            · Java21
          </option>
        </select></label
      >
      <label
        >名称<input
          :value="importName"
          required
          @input="
            emit('update:importName', ($event.target as HTMLInputElement).value)
          " /></label
      ><label
        >{{ importSource === "JAR" ? "可执行 JAR" : "Docker save 归档"
        }}<input
          type="file"
          :accept="importSource === 'JAR' ? '.jar' : '.tar'"
          required
          @change="emit('pickFile', $event)" /></label
      ><label
        >预期 SHA-256<input
          :value="importSHA"
          required
          pattern="[a-fA-F0-9]{64}"
          spellcheck="false"
          @input="
            emit('update:importSHA', ($event.target as HTMLInputElement).value)
          " /></label
      ><small>{{
        importSource === "JAR"
          ? "最大128MiB；验证SHA-256、Main-Class和Java21字节码边界。只添加固定JAR层，不执行上传代码；封装结果冻结运行环境，完整镜像不能替换JRE。"
          : importRecipe === "zlm-direct-v1"
            ? "选择独立 ZLM 镜像归档，使用可信 SHA-256；单镜像 linux/amd64，最大 2GiB。"
            : importRecipe === "preview-node-v1"
              ? "选择 S4 节点包内 preview-node-image.tar，使用该文件的可信 SHA-256；最大 2GiB，须支持维护封闭。"
              : "单镜像 linux/amd64，标签以 sfops/ 开头，最大512MiB。"
      }}</small>
      <div class="dialog-footer">
        <button
          type="button"
          class="secondary"
          :disabled="busy"
          @click="emit('close')"
        >
          取消
        </button>
        <button class="primary" :disabled="busy">
          {{ uploadPhase || "上传并校验" }}
        </button>
      </div>
    </form>
    <form
      v-else-if="dialog === 'candidate'"
      class="dialog-form"
      @submit.prevent="emit('preview')"
    >
      <p v-if="planKind === 'ROLLBACK'">回退到上一成功版本，恢复其冻结配置。</p>
      <p v-if="projectRelease"><strong>{{ projectRelease.moduleName }}</strong> · {{ projectRelease.environmentName }} → {{ planInstance?.name }}</p>
      <label v-if="planKind === 'PUBLISH'"
        >候选制品<select
          :value="artifactChoice"
          required
          @change="
            emit(
              'update:artifactChoice',
              ($event.target as HTMLSelectElement).value,
            )
          "
        >
          <option v-if="projectRelease" value="" disabled>请选择本次上线的制品</option>
          <option v-for="a in eligibleArtifacts" :key="a.id" :value="a.id">
            {{ a.name }}
          </option>
        </select></label
      >
      <p v-if="projectRelease && !optionsLoading && !eligibleArtifacts.length">请先在「版本制品」导入适用于此实例的构建制品。</p>
      <template
        v-if="planInstance?.config.preview && planInstance.currentReleaseId"
      >
        <p>
          在平台“服务信息 →
          预览执行节点”停止接新预览；资源归零后获取维护许可。作业完成后，节点仍保持排空。
        </p>
        <label
          >维护许可编号<input
            :value="maintenancePermit"
            required
            pattern="[a-f0-9-]{36}"
            autocomplete="off"
            @input="
              emit(
                'update:maintenancePermit',
                ($event.target as HTMLInputElement).value,
              )
            "
        /></label>
      </template>
      <div class="dialog-footer">
        <button
          type="button"
          class="secondary"
          :disabled="busy"
          @click="emit('close')"
        >
          取消
        </button>
        <button class="primary" :disabled="busy || optionsLoading || (planKind === 'PUBLISH' && !artifactChoice)">
          生成预检计划
        </button>
      </div>
    </form>
    <div v-else-if="dialog === 'plan' && plan" class="dialog-form">
      <p v-if="projectRelease"><strong>{{ projectRelease.moduleName }}</strong> · {{ projectRelease.environmentName }} · {{ targets.find(host => host.id === projectRelease?.hostId)?.name }}</p>
      <dl class="plan-detail">
        <dt>操作</dt>
        <dd>
          {{ plan.kind === "ROLLBACK" ? "回滚" : "发布" }} ·
          {{ planInstance?.name }}
        </dd>
        <dt>版本来源</dt>
        <dd>
          {{
            artifactOptions.find((item) => item.id === plan?.candidate.artifactId)
              ?.name || "所选制品"
          }}
        </dd>
        <dt>失败处理</dt>
        <dd>{{ plan.baseline ? "恢复上一成功版本" : "停止未成功的候选" }}</dd>
        <template v-if="plan.candidate.runtimeEnvironment">
          <dt>冻结运行环境</dt>
          <dd>
            Java {{ plan.candidate.runtimeEnvironment.pluginVersion }}
          </dd>
        </template>
        <dt>端口</dt>
        <dd>
          {{ plan.candidate.config.bindAddress }}:{{
            plan.candidate.config.hostPort
          }}
          → {{ plan.candidate.config.containerPort }}
        </dd>
        <dt>健康检查</dt>
        <dd>
          {{ plan.candidate.config.healthPath }} ·
          {{ plan.candidate.config.healthSeconds }}秒
        </dd>
        <dt>影响</dt>
        <dd>
          {{
            plan.candidate.config.preview
              ? "只替换此预览节点；已有资源必须清零。失败恢复上一成功版本，作业完成后需到平台检查并明确启用。"
              : "替换该实例，发布期间会短暂中断；数据库不随应用回滚。"
          }}
        </dd>
        <template v-if="plan.maintenancePermit"
          ><dt>维护许可</dt>
          <dd>
            <code>{{ plan.maintenancePermit }}</code>
          </dd></template
        >
        <dt>有效期</dt>
        <dd>{{ time(plan.expiresAt) }}</dd>
      </dl>
      <details>
        <summary>版本技术详情</summary>
        <dl class="plan-detail">
          <dt>候选</dt>
          <dd><code>{{ plan.candidate.id }}</code></dd>
          <dt>镜像</dt>
          <dd><code>{{ plan.candidate.imageId }}</code></dd>
          <dt>恢复基线</dt>
          <dd><code>{{ plan.baseline?.id || "停止未成功的候选" }}</code></dd>
          <template v-if="plan.candidate.runtimeEnvironment">
            <dt>运行环境摘要</dt>
            <dd>
              <code>{{ plan.candidate.runtimeEnvironment.imageDigest }}</code>
            </dd>
          </template>
        </dl>
      </details>
      <div class="dialog-footer">
        <button
          type="button"
          class="secondary"
          :disabled="busy"
          @click="emit('close')"
        >
          取消
        </button>
        <button class="primary" :disabled="busy" @click="emit('submit')">
          确认执行
        </button>
      </div>
    </div>
    <div v-else-if="dialog === 'history'" class="version-list">
      <article v-for="r in releases" :key="r.id">
        <strong>{{
          r.id === selected?.currentReleaseId
            ? "当前成功"
            : r.id === selected?.previousReleaseId
              ? "上一成功"
              : "历史候选"
        }}</strong
        ><span v-if="r.runtimeEnvironment"
          >Java {{ r.runtimeEnvironment.pluginVersion }}</span
        >
        <details>
          <summary>版本详情</summary>
          <code>{{ r.id }}</code
          ><small>{{ r.imageId }}</small
          ><code v-if="r.runtimeEnvironment">{{
            r.runtimeEnvironment.imageDigest
          }}</code>
        </details>
        <details v-if="r.config.preview">
          <summary>冻结的节点配置</summary>
          <pre class="log-output">{{ JSON.stringify(r.config, null, 2) }}</pre>
        </details>
      </article>
      <div v-if="!releases.length" class="empty">暂无版本</div>
      <OpsPagination
        :page="releasePage"
        :page-size="releasePageSize"
        :total="releaseTotal"
        :disabled="releasesLoading"
        label="发布版本分页"
        @update:page="emit('changeReleasePage', $event)"
        @update:page-size="emit('changeReleaseSize', $event)"
      />
    </div>
    <div v-else-if="dialog === 'registration'" class="dialog-form">
      <p>
        部署就绪后，将目标机上的登记文件和机器凭据安全转交平台管理员，导入节点；浏览器预览验证与明确启用在平台进行。
      </p>
      <pre class="log-output">{{ JSON.stringify(registration, null, 2) }}</pre>
      <p>
        升级或回退：平台排空 → 获取维护许可 → Ops 预检执行 → 核对作业及版本 →
        平台检查 / 重新登记 → 解除维护 → 参与调度。
      </p>
    </div>
    <pre v-else-if="dialog === 'logs'" class="log-output">{{
      logs || "暂无日志"
    }}</pre>
    <div v-else-if="dialog === 'job' && detail" class="dialog-form">
      <div v-if="detail.status === 'UNKNOWN'" class="error">
        结果尚未确认，相关变更已锁定
      </div>
      <p v-if="detail.errorCode" class="error">
        {{
          softwareJobErrorMessages[detail.errorCode] ||
          "操作未完成，请核对原操作和执行日志。"
        }}
      </p>
      <dl class="plan-detail">
        <dt>状态</dt>
        <dd>
          {{ statusLabels[jobStatus(detail)] }} ·
          {{ phaseLabels[detail.phase] || "阶段待确认" }}
        </dd>
        <dt>时间</dt>
        <dd>{{ time(detail.updatedAt) }}</dd>
      </dl>
      <div class="runtime-actions">
        <button
          v-if="detail.status === 'UNKNOWN' || detail.targetAckPending"
          class="secondary"
          :disabled="busy"
          @click="emit('reconcile', detail)"
        >
          核对原操作
        </button>
        <button
          v-if="
            detail.status === 'QUEUED' &&
            detail.phase !== 'UPLOADING' &&
            !detail.targetAckPending
          "
          class="quiet"
          :disabled="busy"
          @click="emit('cancelJob', detail)"
        >
          取消排队
        </button>
      </div>
      <details>
        <summary>诊断信息</summary>
        <dl class="plan-detail">
          <dt>作业</dt>
          <dd><code>{{ detail.id }}</code></dd>
          <dt>状态 / 阶段</dt>
          <dd>
            <code>{{
              softwareJobDiagnostic(detail.status, detail.phase, phaseLabels)
            }}</code>
          </dd>
          <dt>错误码</dt>
          <dd>{{ detail.errorCode || "—" }}</dd>
          <dt>追踪</dt>
          <dd>
            <code>{{ detail.traceId }}</code>
          </dd>
          <dt>结果</dt>
          <dd>
            <pre>{{ JSON.stringify(detail.result, null, 2) }}</pre>
          </dd>
        </dl>
      </details>
    </div>
  </ElDialog>
</template>
