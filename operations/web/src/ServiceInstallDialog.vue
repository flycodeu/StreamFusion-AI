<script setup lang="ts">
import { computed, nextTick, onUnmounted, reactive, ref, watch } from "vue";
import { ElCard, ElDrawer, ElMessageBox } from "element-plus";
import HostPreparationPanel from "./HostPreparationPanel.vue";
import ErrorHelpLink from "./ErrorHelpLink.vue";
import OfficialInstallationDetails from "./OfficialInstallationDetails.vue";
import { hostTime, type Host } from "./hosts";
import { request } from "./api";
import SoftwareIcon from "./SoftwareIcon.vue";
import ServiceNetworkFields from "./ServiceNetworkFields.vue";
import InstallationMaterialDetails from "./InstallationMaterialDetails.vue";
import { softwareJobPhaseLabel } from "./software";
import {
  defaultInstallationRuntime,
  type RuntimeMode,
  materialChoiceLabel,
  serviceEndpoint,
  runtimeLabels,
  isTool,
  isDockerEngine,
  type ServiceInstallReceipt,
  type ServiceRecipe,
  type InstallationMethod,
} from "./serviceInstallation";
import {
  useServiceInstallation,
  type ServiceInstallationProps,
} from "./useServiceInstallation";

const props = defineProps<ServiceInstallationProps & {
  recipes?: ServiceRecipe[];
  officialRecipes?: ServiceRecipe[];
  dockerRecipe?: ServiceRecipe;
  dockerOfficialRecipes?: ServiceRecipe[];
  embedded?: boolean;
}>();
const emit = defineEmits<{
  close: [];
  submitted: [ServiceInstallReceipt];
  error: [unknown];
  preparation: [string];
  register: [hostId: string, softwareType: string];
}>();
const mode = ref<RuntimeMode | "SELF">();
const nativeMethod = ref<InstallationMethod>("OFFICIAL");
const selectedVersion = ref("");
const checkedHost = ref<Host>();
const host = computed(() => checkedHost.value || props.host);
const checkingHost = ref(false);
const hostCheckError = ref("");
const engineInstalling = ref(false);
let manualMode = false;
let checkGeneration = 0;
let hostCheckAbort: AbortController | undefined;
const variants = computed(() => props.recipes || (props.recipe ? [props.recipe] : []));
const nativeAvailable = computed(() => !!props.officialRecipes?.length || variants.value.some((item) => item.runtimeModes.includes("NATIVE")));
const dockerAvailable = computed(() => variants.value.some((item) => item.runtimeModes.includes("DOCKER")));
const method = computed<InstallationMethod>(() => mode.value === "SELF" ? "SELF" : mode.value === "NATIVE" ? nativeMethod.value : "OFFLINE");
const choices = computed(() => method.value === "OFFICIAL" ? props.officialRecipes || [] : variants.value.filter((item) => item.runtimeModes.includes(mode.value || "DOCKER")));
const recipe = computed(() => choices.value.find((item) => item.revision === selectedVersion.value) || choices.value[0] || props.recipe);
const officialRecipe = computed(() => method.value === "OFFICIAL" ? choices.value.find((item) => item.revision === selectedVersion.value) : undefined);
const selfDetectionAvailable = computed(() => props.recipe?.kind !== "TOOL" || ["java", "python", "ffmpeg", "git"].includes(props.recipe?.key || ""));
const steps = ["安装设置", "确认安装", "执行结果"];
const flowProps = reactive({ open: computed(() => props.open), recipe, host, method, runtimeMode: computed(() => mode.value === "SELF" ? undefined : mode.value) });
function selectMode(value: RuntimeMode | "SELF") {
  manualMode = true;
  mode.value = value;
}
function applyDefault() {
  mode.value = defaultInstallationRuntime([...variants.value, ...(props.officialRecipes || [])], host.value);
  selectedVersion.value = (choices.value.find((item) => item.revision === props.recipe?.revision) || choices.value[0])?.revision || "";
}
async function checkHost() {
  if (!props.host || checkingHost.value) return;
  const generation = checkGeneration, id = props.host.id;
  checkingHost.value = true;
  hostCheckError.value = "";
  const abort = new AbortController();
  hostCheckAbort = abort;
  try {
    const updated = await request<Host>(`/hosts/${id}/checks`, "POST", {}, abort.signal);
    if (generation !== checkGeneration || !props.open) return;
    checkedHost.value = updated;
    if (!manualMode) applyDefault();
  } catch (error) {
    if (generation === checkGeneration && props.open && !abort.signal.aborted)
      hostCheckError.value = error instanceof Error ? error.message : "主机检测未完成";
  } finally {
    if (generation === checkGeneration) checkingHost.value = false;
  }
}
watch([() => props.open, () => props.host?.id, () => props.recipe?.key], () => {
  checkGeneration++;
  hostCheckAbort?.abort();
  checkingHost.value = false;
  checkedHost.value = undefined;
  hostCheckError.value = "";
  manualMode = false;
  engineInstalling.value = false;
  nativeMethod.value = props.officialRecipes?.length ? "OFFICIAL" : "OFFLINE";
  if (!props.open) return;
  applyDefault();
  if (!mode.value) void checkHost();
}, { immediate: true });
watch(choices, (items) => {
  if (!items.some((item) => item.revision === selectedVersion.value))
    selectedVersion.value = items.length === 1 ? items[0]!.revision : "";
});
onUnmounted(() => { checkGeneration++; hostCheckAbort?.abort(); });
async function resumeSoftware() {
  engineInstalling.value = false;
  await checkHost();
}
const {
  step,
  busy,
  configuration,
  sourceType,
  preparationOpen,
  automaticUpdate,
  readinessHost,
  preparationChecked,
  materials,
  materialId,
  credentialChoices,
  credentialRole,
  credentialName,
  credentialSecret,
  revealSecret,
  selectedFile,
  archiveSHA,
  materialProgress,
  officialProgress,
  prepareOfficialMaterial,
  officialNeedsImport,
  prepareOfficialAgain,
  materialNeedsImport,
  prepareMaterialImport,
  uploadController,
  plan,
  receipt,
  submissionAttempted,
  job,
  logs,
  logsLimited, logsAt, logsLoading, logsPaused, logError,
  fieldError,
  actionError,
  credentialRoles,
  selectedMaterial,
  planExpired,
  submitted,
  credentialLabel,
  credentialOptions,
  editCredential,
  generateSecret,
  saveCredential,
  fileChanged,
  uploadMaterial,
  next,
  preflight,
  submit,
  jobAction,
  loadLogs,
  clearError,
  close: closeFlow,
} = useServiceInstallation(
  flowProps,
  () => emit("close"),
  (receipt) => emit("submitted", receipt),
);
const touched = ref(false);
watch(() => props.open, () => { touched.value = false; });
const closeBlocked = computed(() => engineInstalling.value || busy.value || preparationOpen.value || (submissionAttempted.value && !receipt.value) || !!job.value && (['QUEUED', 'RUNNING', 'UNKNOWN'].includes(job.value.status) || job.value.targetAckPending));
async function close(done?: () => void) {
  if (closeBlocked.value) return;
  if (touched.value && !submitted.value) {
    try { await ElMessageBox.confirm("放弃本次安装设置？", "关闭窗口", { confirmButtonText: "放弃修改", cancelButtonText: "继续编辑" }); } catch { return; }
  }
  if (done) done(); else closeFlow();
}
const visibleStep = computed(() => step.value === 4 ? 2 : step.value === 3 ? 1 : 0);
const logPane = ref<HTMLElement>();
const followOutput = ref(true);
watch(logs, async () => {
  if (!followOutput.value) return;
  await nextTick();
  if (logPane.value) logPane.value.scrollTop = logPane.value.scrollHeight;
});
watch(() => receipt.value?.jobId, () => { followOutput.value = true; });
function outputScrolled() {
  const pane = logPane.value;
  if (pane) followOutput.value = pane.scrollHeight - pane.scrollTop - pane.clientHeight < 32;
}
function followLatest() {
  followOutput.value = true;
  if (logPane.value) logPane.value.scrollTop = logPane.value.scrollHeight;
}
function previous() { step.value = 0; clearError(); }
</script>

<template>
  <component
    :is="embedded ? ElCard : ElDrawer"
    :model-value="open"
    :title="`安装 ${recipe?.name || '服务'}`"
    size="min(960px,100vw)"
    destroy-on-close
    :close-on-click-modal="!closeBlocked"
    :before-close="close"
    :close-on-press-escape="!closeBlocked"
    :show-close="!closeBlocked"
    @close="closeFlow"
  >
    <div v-if="recipe && host && configuration" v-show="!engineInstalling && !preparationOpen" class="service-install" @input="touched = true" @change="touched = true">
      <header class="install-context">
        <SoftwareIcon :type="recipe.key" />
        <div>
          <strong
            >{{ recipe.name }} <span>{{ mode === 'SELF' ? '已有软件' : '' }}</span></strong
          ><small>{{ host.name }} · {{ host.address || "本机" }}</small>
        </div>
        <span class="context-kind">{{
          mode === 'SELF' ? '只读检测登记' : mode ? runtimeLabels[mode] : '选择安装方式'
        }}</span>
      </header>
      <div class="installation-methods" role="group" aria-label="安装方式">
        <button v-if="dockerAvailable" :class="{ active: mode === 'DOCKER' }" :aria-pressed="mode === 'DOCKER'" :disabled="busy || submissionAttempted || submitted" @click="selectMode('DOCKER')">Docker 安装</button>
        <button v-if="nativeAvailable" :class="{ active: mode === 'NATIVE' }" :aria-pressed="mode === 'NATIVE'" :disabled="busy || submissionAttempted || submitted" @click="selectMode('NATIVE')">原生安装</button>
        <button :class="{ active: mode === 'SELF' }" :aria-pressed="mode === 'SELF'" :disabled="busy || submissionAttempted || submitted" @click="selectMode('SELF')">管理已有软件</button>
      </div>
      <div v-if="checkingHost || hostCheckError || !mode" class="host-readiness" role="status">
        <span>{{ checkingHost ? '正在检测安装环境…' : hostCheckError || '请选择安装方式，或重新检测主机' }}</span>
        <button v-if="!checkingHost" class="quiet" :disabled="busy || submitted" @click="checkHost">重新检测</button>
      </div>
      <div v-if="mode === 'DOCKER' && host.capabilities?.docker.status !== 'AVAILABLE' && step < 3" class="host-readiness">
        <span>{{ host.capabilities?.docker.code === 'DOCKER_NOT_INSTALLED' ? '未安装 Docker' : host.capabilities?.docker.message || 'Docker 状态待确认' }}</span>
        <button v-if="host.capabilities?.docker.code === 'DOCKER_NOT_INSTALLED' && props.dockerRecipe" class="secondary" :disabled="busy" @click="engineInstalling = true">安装 Docker</button>
      </div>
      <div v-if="mode && mode !== 'SELF' && step < 3" class="form-row installation-selection">
        <label v-if="mode === 'NATIVE'">安装来源<select v-model="nativeMethod" aria-label="原生安装来源" :disabled="busy">
          <option v-if="props.officialRecipes?.length" value="OFFICIAL">官方在线</option>
          <option v-if="variants.some((item) => item.runtimeModes.includes('NATIVE'))" value="OFFLINE">Ops 离线材料</option>
        </select></label>
        <label>版本<select v-model="selectedVersion" aria-label="安装版本" :disabled="busy"><option value="" disabled>请选择此安装方式的版本</option><option v-for="item in choices" :key="item.revision" :value="item.revision">{{ method === 'OFFICIAL' ? item.name : item.version }}</option></select></label>
      </div>
      <ol v-if="mode && method !== 'SELF'" class="install-steps official-steps" aria-label="安装步骤">
        <li
          v-for="(label, index) in steps"
          :key="label"
          :class="{ current: visibleStep === index, complete: visibleStep > index }"
          :aria-current="visibleStep === index ? 'step' : undefined"
        >
          <span>{{ index + 1 }}</span
          ><small>{{ label }}</small>
        </li>
      </ol>
      <div v-if="fieldError" class="install-error" role="alert">
        {{ fieldError }}
      </div>
      <div v-if="actionError" class="install-error" role="alert">
        <ErrorHelpLink :code="actionError.code" />
        <strong>{{ actionError.message }}</strong
        ><small>{{ actionError.code }} · {{ hostTime(actionError.at) }}</small
        ><code v-if="actionError.traceId"
          >traceId={{ actionError.traceId }}</code
        >
        <button v-if="actionError.code === 'EXECUTOR_UPGRADE_REQUIRED'" class="secondary" :disabled="busy" @click="automaticUpdate = undefined; preparationOpen = true">更新并继续</button>
        <button
          v-else-if="
            /EXECUTOR|SUDO|DOCKER|AUTHORIZATION|TARGET_PACKAGE|PREPARATION|HOST_BUSY|HOST_MAINTENANCE/.test(
              actionError.code,
            )
          "
          class="quiet"
          :disabled="busy"
          @click="emit('preparation', host.id)"
        >
          查看主机安装准备
        </button>
        <small v-if="submissionAttempted && !receipt"
          >提交回执尚未取得；重试会使用原提交标识，也可在软件中心查看原作业。</small
        >
      </div>

      <section v-if="method === 'SELF'" class="install-panel self-install" aria-label="自行安装">
        <h3>检测服务器已有软件</h3>
        <a :href="props.recipe?.officialUrl" target="_blank" rel="noopener noreferrer">查看官方安装说明 ↗</a>
        <p v-if="!selfDetectionAvailable">此软件暂不支持自动工具登记，可先按官方说明在服务器使用。</p>
        <button v-if="selfDetectionAvailable" class="primary" @click="emit('register', host.id, props.recipe?.key.toUpperCase() || 'OTHER')">检测并登记</button>
      </section>
      <div v-if="mode && mode !== 'SELF' && step < 3" class="settings-grid" :class="{ 'official-settings': method === 'OFFICIAL' }">
      <section v-if="method === 'OFFICIAL'" class="install-panel" aria-label="官方安装配置">
        <label>实例名称<input v-model="configuration.name" aria-label="实例名称" :disabled="busy" maxlength="120" /></label>
        <a :href="officialRecipe?.officialUrl" target="_blank" rel="noopener noreferrer">官方安装说明 ↗</a>
      </section>
      <section v-else class="install-panel source-panel" aria-label="安装来源">
        <h3>安装材料</h3>
        <div class="install-options">
          <label
            class="option-card"
            :class="{ selected: sourceType === 'ARCHIVE' }"
            ><input
              v-model="sourceType"
              type="radio"
              value="ARCHIVE"
              :disabled="busy || !recipe.sourceModes.includes('ARCHIVE')"
            /><span
              ><strong>平台离线材料</strong
              ><small>复用已校验材料</small></span
            ></label
          ><label
            class="option-card"
            :class="{ selected: sourceType !== 'ARCHIVE' }"
            ><input
              v-model="sourceType"
              type="radio"
              :value="configuration.runtimeMode === 'NATIVE' ? 'ONLINE' : 'REGISTRY'"
              :disabled="busy"
            /><span
              ><strong>{{ configuration.runtimeMode === 'NATIVE' ? '补充材料到离线库' : '目标在线拉取镜像' }}</strong
              ><small>{{ configuration.runtimeMode === 'NATIVE' ? 'Ops 联网下载并入库，目标仍接收离线分发' : '目标联网拉取固定镜像' }}</small></span
            ></label
          >
        </div>
        <details class="source-details"><summary>来源与校验信息</summary><InstallationMaterialDetails
          :recipe="recipe"
          :runtime-mode="configuration.runtimeMode"
          :material="sourceType === 'REGISTRY' ? undefined : selectedMaterial"
          :online="sourceType !== 'ARCHIVE'"
        /></details>
        <div v-if="sourceType === 'ONLINE'" class="archive-picker">
          <label
            >已准备材料<select
              v-model="materialId"
              aria-label="已准备的官方材料"
              :disabled="busy"
            >
              <option value="">准备官方材料，或复用已校验归档</option>
              <option
                v-for="item in materials"
                :key="item.id"
                :value="item.id"
                :disabled="!item.available"
              >
                {{ materialChoiceLabel(item) }}
              </option>
            </select></label
          >
          <div class="inline-actions">
            <button
              class="secondary"
              :disabled="busy || officialNeedsImport"
              @click="prepareOfficialMaterial"
            >
              下载材料到离线库</button
            ><button
              v-if="officialNeedsImport"
              class="secondary"
              :disabled="busy"
              @click="prepareOfficialAgain"
            >
              重新准备为新材料</button
            ><button
              v-if="uploadController"
              class="quiet danger"
              @click="uploadController.abort()"
            >
              中断等待</button
            ><span aria-live="polite">{{
              selectedMaterial?.available ? "材料已就绪" : officialProgress
            }}</span>
          </div>
        </div>
        <div v-if="sourceType === 'ARCHIVE'" class="archive-picker">
          <label
            >离线材料<select
              v-model="materialId"
              aria-label="可用安装包"
              :disabled="busy"
            >
              <option value="">当前版本暂无就绪材料，请补充材料或上传</option>
              <option
                v-for="item in materials"
                :key="item.id"
                :value="item.id"
                :disabled="!item.available"
              >
                {{ materialChoiceLabel(item) }}
              </option>
            </select></label
          >
          <details>
            <summary>
              {{
                configuration.runtimeMode === "NATIVE"
                  ? "上传离线安装包"
                  : "上传离线镜像"
              }}
            </summary>
            <div class="archive-upload">
              <label
                >{{
                  configuration.runtimeMode === "NATIVE"
                    ? "原生安装归档"
                    : "镜像归档"
                }}<input
                  type="file"
                  :accept="
                    configuration.runtimeMode === 'NATIVE'
                      ? recipe.native?.parts[0]?.format === 'RAW'
                        ? ''
                        : '.tar,.tar.gz,.tgz,.tar.xz,.xz'
                      : '.tar'
                  "
                  :aria-label="
                    configuration.runtimeMode === 'NATIVE'
                      ? '原生安装归档'
                      : '镜像归档'
                  "
                  :disabled="busy"
                  @change="fileChanged" /></label
              ><label
                >归档 SHA-256<input
                  v-model="archiveSHA"
                  aria-label="归档 SHA-256"
                  maxlength="64"
                  spellcheck="false"
                  :disabled="busy"
                  placeholder="填写可信归档的完整 SHA-256" /></label
              ><small
                >{{
                  configuration.runtimeMode === "NATIVE"
                    ? recipe.native?.parts[0]?.format === "RAW"
                      ? "选择官方原始二进制文件，保持原始内容，最大2 GiB。"
                      : "使用当前配方要求的官方材料归档，最大2 GiB。"
                    : "使用 docker save 生成 .tar，最大2 GiB。"
                }}校验通过后可重复使用。</small
              >
              <div class="inline-actions">
                <button
                  class="secondary"
                  :disabled="busy || !selectedFile || materialNeedsImport"
                  @click="uploadMaterial"
                >
                  上传并校验</button
                ><button
                  v-if="uploadController"
                  class="quiet danger"
                  @click="uploadController.abort()"
                >
                  中断操作</button
                ><button
                  v-if="materialNeedsImport"
                  class="secondary"
                  :disabled="busy"
                  @click="prepareMaterialImport"
                >
                  重新导入为新材料</button
                ><span aria-live="polite">{{ materialProgress }}</span>
              </div>
            </div>
          </details>
          <p
            v-if="selectedMaterial && !selectedMaterial.available"
            class="install-error"
          >
            材料尚未校验通过
          </p>
          <details v-if="selectedMaterial?.errorCode"><summary>材料错误详情</summary><code>{{ selectedMaterial.errorCode }}</code><ErrorHelpLink :code="selectedMaterial.errorCode" /></details>
          <details
            v-if="
              configuration.runtimeMode === 'NATIVE' &&
              recipe.native?.parts.length
            "
          >
            <summary>准备原生离线归档</summary>
            <p class="install-note">
              {{
                recipe.native.parts.length === 1
                  ? "直接上传下面的原始官方安装包，无需解压或再次打包。SHA-256已按官方材料预填。"
                  : "在可联网电脑下载以下官方材料，另存为指定文件名，保持原包内容。放在同一目录后打成一个tar归档，再计算归档SHA-256。"
              }}
            </p>
            <div
              v-for="part in recipe.native.parts"
              :key="part.name"
              class="native-part"
            >
              <a :href="part.url" target="_blank" rel="noopener noreferrer"
                >{{ part.name }} ↗</a
              ><code>{{ part.sha256 }}</code>
            </div>
            <code v-if="recipe.native.parts.length > 1"
              >tar -cf {{ recipe.key }}-offline.tar
              {{ recipe.native.parts.map((part) => part.name).join(" ") }}</code
            ><code
              >Get-FileHash .\{{
                recipe.native.parts.length === 1
                  ? recipe.native.parts[0]!.name
                  : recipe.key + "-offline.tar"
              }}
              -Algorithm SHA256</code
            >
          </details>
        </div>
      </section>

      <section
        v-if="method !== 'OFFICIAL'"
        class="install-panel instance-panel"
        aria-label="实例配置"
      >
        <label
          >实例名称<input
            v-model="configuration.name"
            aria-label="实例名称"
            :disabled="busy"
            maxlength="120"
        /></label>
        <label
          >项目目录<code class="path-value">{{ host.installRoot }}</code></label
        >
        <p v-if="recipe.key === 'mysql'" class="install-note">
          管理员账号为 root，仅初始化新的空数据目录。
        </p>
        <p v-if="recipe.category === 'PROGRAMMING'" class="install-note">
          仅安装程序并核对版本。安装后在自己的 SSH 会话完成登录。
        </p>
        <p
          v-if="recipe.key === 'ffmpeg' || recipe.key === 'python'"
          class="install-note"
        >
          原生方式使用专用账号编译，缺少依赖时预检提示，不修改系统环境。
        </p>
        <div v-if="recipe.key === 'mysql'" class="form-row">
          <label
            >初始业务数据库 <small>可选</small
            ><input
              v-model="configuration.database"
              aria-label="初始业务数据库"
              :disabled="busy"
              placeholder="例如 streamfusion"
              maxlength="64" /></label
          ><label
            >业务账号 <small>可选</small
            ><input
              v-model="configuration.username"
              aria-label="业务账号"
              :disabled="busy"
              placeholder="填写后需配置业务密码"
              maxlength="64"
          /></label>
        </div>
        <label v-if="recipe.key === 'mediamtx'"
          >服务账号<input
            v-model="configuration.username"
            aria-label="MediaMTX 服务账号"
            :disabled="busy"
            maxlength="32"
            autocomplete="username"
        /></label>
        <div
          v-for="role in credentialRoles"
          :key="role"
          class="credential-field"
        >
          <label
            >{{ credentialLabel(role)
            }}<select
              v-model="credentialChoices[role]"
              :aria-label="credentialLabel(role)"
              :disabled="busy"
            >
              <option value="">选择已保存凭据</option>
              <option
                v-for="item in credentialOptions(role)"
                :key="`${item.id}:${item.revision}`"
                :value="`${item.id}:${item.revision}`"
              >
                {{ item.name }} · 修订{{ item.revision }}
              </option>
            </select></label
          ><button class="quiet" :disabled="busy" @click="editCredential(role)">
            新建凭据
          </button>
        </div>
        <p v-if="recipe.key === 'mysql'" class="install-note">
          此安装配方的密码为8–128位可打印字符，不能含引号、反斜线或换行。
        </p>
        <div v-if="credentialRole" class="credential-editor">
          <header>
            <strong>新建{{ credentialLabel(credentialRole) }}</strong
            ><button
              class="quiet"
              :disabled="busy"
              @click="
                credentialRole = undefined;
                credentialSecret = '';
              "
            >
              收起
            </button>
          </header>
          <label
            >凭据名称<input
              v-model="credentialName"
              aria-label="凭据名称"
              maxlength="60"
              :disabled="busy" /></label
          ><label
            >{{ credentialLabel(credentialRole) }}
            <div class="secret-input">
              <input
                v-model="credentialSecret"
                :type="revealSecret ? 'text' : 'password'"
                aria-label="新凭据内容"
                autocomplete="new-password"
                :disabled="busy"
              /><button
                class="quiet"
                :disabled="busy"
                @click="revealSecret = !revealSecret"
              >
                {{ revealSecret ? "隐藏" : "显示" }}
              </button>
            </div></label
          >
          <div class="inline-actions">
            <button class="secondary" :disabled="busy" @click="generateSecret">
              生成新值</button
            ><button
              class="primary"
              :disabled="busy || !credentialSecret"
              @click="saveCredential"
            >
              加密保存并选用
            </button>
          </div>
        </div>
        <p v-if="recipe.key === 'nginx'" class="install-note">
          Nginx 无初始登录账号；站点配置放在独立配置目录。
        </p>
      </section>

      <section
        v-if="method !== 'OFFICIAL'"
        class="install-panel network-panel"
        aria-label="网络存储"
      >
        <ServiceNetworkFields
          :recipe="recipe"
          :config="configuration"
          :disabled="busy"
          storage-editable
          :project-root="host.installRoot"
        />
      </section>

      </div>
      <section
        v-if="step === 3 && plan"
        class="install-panel"
        aria-label="预检确认"
      >
        <div class="plan-status" :class="{ expired: planExpired }">
          <strong>{{ planExpired ? "预检已过期" : "预检完成" }}</strong
          ><small>有效至 {{ hostTime(plan.expiresAt) }}</small>
        </div>
        <ul class="preflight-checks">
          <li
            v-for="check in plan.checks"
            :key="check.key"
            :class="{ warning: check.status === 'WARNING' }"
          >
            <b>{{ check.status === "WARNING" ? "!" : "✓" }}</b
            ><span>{{ check.message || "检查结果未提供说明" }}</span>
          </li>
        </ul>
        <OfficialInstallationDetails v-if="plan.official" :installation="plan.official" commands />
        <dl v-else class="install-facts">
          <dt>运行方式</dt>
          <dd>
            {{ runtimeLabels[plan.config.runtimeMode] }} ·
            {{ plan.definition.version }}
          </dd>
          <dt>安装来源</dt>
          <dd>
            {{
              plan.source.type !== "ARCHIVE"
                ? plan.source.type === "ONLINE"
                  ? "Ops 联网获取后离线分发"
                  : "在线镜像"
                : selectedMaterial ? materialChoiceLabel(selectedMaterial) : "离线材料库"
            }}
          </dd>
          <dt>访问入口</dt>
          <dd>
            <code>{{ serviceEndpoint(recipe, plan.config) }}</code>
          </dd>
          <dt>数据目录</dt>
          <dd>
            <code>{{ plan.paths.data }}</code>
          </dd>
          <dt>配置目录</dt>
          <dd>
            <code>{{ plan.paths.config }}</code>
          </dd>
          <template v-if="!isTool(recipe) && !isDockerEngine(recipe)"
            ><dt>内存上限</dt>
            <dd>{{ plan.config.memoryMiB }} MiB</dd></template
          >
          <template v-if="plan.paths.bin"
            ><dt>程序目录</dt>
            <dd>
              <code>{{ plan.paths.bin }}</code>
            </dd></template
          >
          <template v-if="plan.paths.logs"
            ><dt>日志目录</dt>
            <dd>
              <code>{{ plan.paths.logs }}</code>
            </dd></template
          >
        </dl>
        <details v-if="!plan.official">
          <summary>完整映射与安装身份</summary>
          <code
            v-for="mapping in plan.portMappings"
            :key="`${mapping.address}:${mapping.hostPort}/${mapping.protocol}/${mapping.key || ''}`"
            >{{ mapping.address }}:{{ mapping.hostPort }} →
            {{ mapping.containerPort }}/{{ mapping.protocol }}</code
          ><code v-if="plan.config.runtimeMode === 'DOCKER'"
            >{{ plan.definition.image }}@{{ plan.definition.digest }}</code
          >
        </details>
      </section>

      <section
        v-else-if="step === 4 && receipt"
        class="install-panel execution-panel"
        aria-label="执行结果"
      >
        <div class="job-result" :class="job?.status?.toLowerCase()">
          <strong>{{
            job?.status === "SUCCEEDED"
              ? isTool(recipe)
                ? "工具版本校验通过"
                : "服务检查通过"
              : job?.status === "FAILED"
                ? job.phase === "CANCELLED"
                  ? "安装已取消"
                  : "安装未完成"
                : job?.status === "UNKNOWN"
                  ? "结果待核对"
                  : "安装正在执行"
          }}</strong
          ><span>{{
            softwareJobPhaseLabel(job?.phase)
          }}</span>
        </div>
        <p
          v-if="job?.status === 'UNKNOWN' || job?.targetAckPending"
          class="install-note"
        >
          {{
            job.targetAckPending
              ? "结果已记录，等待目标确认收尾。"
              : "目标结果未确认，保留原作业与资源锁，请先核对。"
          }}
        </p>
        <details class="execution-details"><summary>任务详情</summary><dl class="install-facts">
          <dt>目标主机</dt>
          <dd>{{ host.name }}</dd>
          <dt v-if="!plan?.official">访问入口</dt>
          <dd v-if="!plan?.official">
            <code>{{
              serviceEndpoint(recipe, plan?.config || configuration)
            }}</code>
          </dd>
          <dt>实例标识</dt>
          <dd>
            <code>{{ receipt.softwareInstanceId }}</code>
          </dd>
          <dt>作业标识</dt>
          <dd>
            <code>{{ receipt.jobId }}</code>
          </dd>
          <dt>阶段标识</dt><dd><code>{{ job?.phase || '—' }}</code></dd>
          <dt v-if="job?.errorCode">错误码</dt>
          <dd v-if="job?.errorCode">
            <code>{{ job.errorCode }}</code><ErrorHelpLink :code="job.errorCode" />
          </dd>
          <dt v-if="job?.traceId">追踪标识</dt>
          <dd v-if="job?.traceId">
            <code>{{ job.traceId }}</code>
          </dd>
          <dt>最近更新</dt>
          <dd>{{ hostTime(job?.updatedAt) }}</dd>
        </dl></details>
        <div v-if="job?.status === 'UNKNOWN' || job?.targetAckPending || job?.status === 'QUEUED'" class="inline-actions">
          <button
            v-if="job?.status === 'UNKNOWN' || job?.targetAckPending"
            class="secondary"
            :disabled="busy"
            @click="jobAction('reconcile')"
          >
            核对原作业</button
          ><button
            v-if="job?.status === 'QUEUED'"
            class="quiet danger"
            :disabled="busy"
            @click="jobAction('cancel')"
          >
            取消排队</button
          >
        </div>
        <section class="execution-terminal" aria-label="安装输出">
          <header><strong>安装输出</strong><span>{{ logsPaused ? '已暂停' : ['QUEUED', 'RUNNING', 'UNKNOWN'].includes(job?.status || '') || job?.targetAckPending ? '自动刷新 · 3 秒' : '执行结束' }}</span>
            <button v-if="!logsPaused && (['QUEUED', 'RUNNING', 'UNKNOWN'].includes(job?.status || '') || job?.targetAckPending)" class="quiet" @click="logsPaused = true">暂停刷新</button>
            <button class="quiet" :disabled="logsLoading" @click="loadLogs">{{ logsLoading ? '读取中…' : logsPaused ? '继续刷新' : '刷新输出' }}</button>
          </header>
          <pre ref="logPane" class="execution-output" tabindex="0" aria-label="安装过程输出" @scroll="outputScrolled">{{ logs || (job?.status === 'QUEUED' ? '等待开始安装…' : '等待安装输出…') }}</pre>
          <footer v-if="logsAt || !followOutput"><span>{{ logsLimited ? '最近输出 · ' : '' }}{{ hostTime(logsAt) }}</span><button v-if="!followOutput" class="quiet" @click="followLatest">回到最新</button></footer>
        </section>
        <div v-if="logError" class="execution-log-error" role="status">
          <span>{{ logsPaused ? '自动刷新已暂停。' : '' }}{{ logError.message }}</span>
          <details><summary>错误详情</summary><code>{{ logError.code }} · {{ hostTime(logError.at) }}</code><code v-if="logError.traceId">traceId={{ logError.traceId }}</code><ErrorHelpLink :code="logError.code" /></details>
        </div>
      </section>
    </div>
    <ServiceInstallDialog v-if="engineInstalling && props.dockerRecipe" embedded :open="true" :recipe="props.dockerRecipe" :recipes="[props.dockerRecipe]" :official-recipes="props.dockerOfficialRecipes" :host="host" @close="resumeSoftware" @submitted="emit('submitted', $event)" />
    <section v-if="preparationOpen && host" class="inline-preparation">
      <h3>{{ automaticUpdate ? '正在准备安装环境' : '更新接入组件' }}</h3>
      <HostPreparationPanel :host="readinessHost || host" :automatic-update="automaticUpdate" update-only @updated="preparationChecked" />
      <button v-if="!automaticUpdate" class="secondary" @click="preparationOpen = false">返回安装设置</button>
    </section>
    <template #footer
      ><div v-if="!engineInstalling && !preparationOpen" class="install-footer">
        <button class="secondary" :disabled="busy" @click="close()">
          {{ embedded ? "返回软件安装" : submitted ? "关闭" : "取消" }}</button
        ><span class="footer-spacer"></span
        ><button
          v-if="method !== 'SELF' && step === 3"
          class="secondary"
          :disabled="busy || submissionAttempted"
          @click="previous"
        >
          上一步</button
        ><button
          v-if="method !== 'SELF' && step < 3"
          class="primary"
          :disabled="busy || !mode || !selectedVersion || !recipe || !host || (method === 'OFFICIAL' && !officialRecipe)"
          @click="next"
        >
          {{
            busy ? "处理中…" : "检查安装条件"
          }}</button
        ><button
          v-if="step === 3 && planExpired && !submissionAttempted"
          class="primary"
          :disabled="busy"
          @click="preflight"
        >
          重新预检</button
        ><button
          v-else-if="step === 3"
          class="primary"
          :disabled="busy"
          @click="submit"
        >
          {{ busy ? "提交中…" : "确认安装" }}
        </button>
      </div></template
    >
  </component>
</template>

<style scoped>
.execution-panel { gap: 14px; }
.execution-details { margin: 0; color: var(--text-secondary, #64748b); }
.execution-details summary { cursor: pointer; }
.execution-terminal { overflow: hidden; border: 1px solid #334155; border-radius: 8px; background: #172334; color: #dce5f2; }
.execution-terminal header, .execution-terminal footer { display: flex; flex-wrap: wrap; align-items: center; gap: 10px; padding: 12px 16px; }
.execution-terminal header { border-bottom: 1px solid #334155; }
.execution-terminal header span { flex: 1; color: #a9bbd1; font-size: 12px; }
.execution-terminal .quiet { color: #dce5f2; border-color: #52637a; background: transparent; }
.execution-output { min-height: 160px; max-height: 420px; overflow: auto; margin: 0; padding: 16px; white-space: pre-wrap; overflow-wrap: anywhere; font: 12px/1.8 Consolas, monospace; tab-size: 2; }
.execution-terminal footer { justify-content: space-between; color: #a9bbd1; font-size: 12px; border-top: 1px solid #334155; }
.execution-log-error { margin-top: 10px; font-size: 13px; color: #b45309; overflow-wrap: anywhere; }
.execution-log-error code { display: block; }
</style>

<style scoped src="./service-installation.css"></style>
<style scoped>
.installation-methods { display: flex; gap: 6px; padding: 4px; border: 1px solid var(--border); border-radius: 6px; background: #f7f9fc; }
.installation-methods button { flex: 1; border: 0; background: transparent; padding: 11px 6px; color: var(--muted); font-size: 12px; }
.installation-methods button.active { background: #fff; color: var(--accent); box-shadow: 0 1px 4px #10203a12; border-radius: 4px; }
.official-steps { grid-template-columns: repeat(3, minmax(0, 1fr)); }
.self-install p { margin: 0; font-size: 13px; line-height: 1.9; color: var(--muted); }
.self-install .primary { justify-self: start; width: auto; }
.install-panel > a { font-size: 12px; overflow-wrap: anywhere; }
</style>

<style scoped>
.host-readiness { display: flex; flex-wrap: wrap; align-items: center; gap: 12px; font-size: 13px; color: var(--muted); }
.settings-grid { display: grid; grid-template-columns: minmax(0, 1fr) minmax(0, 1fr); gap: 18px; align-items: start; }
.source-panel, .instance-panel { grid-column: 1; }
.network-panel { grid-column: 2; grid-row: 1 / 3; }
.official-settings { grid-template-columns: minmax(0, 1fr); }
.source-details { overflow-wrap: anywhere; }
.source-details summary { cursor: pointer; font-size: 13px; color: var(--muted); }
.inline-preparation { display: grid; gap: 16px; }
@media (max-width: 720px) {
  .settings-grid { grid-template-columns: minmax(0, 1fr); }
  .source-panel, .instance-panel, .network-panel { grid-column: auto; grid-row: auto; }
}
</style>
