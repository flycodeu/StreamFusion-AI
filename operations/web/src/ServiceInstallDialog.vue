<script setup lang="ts">
import { computed, nextTick, onUnmounted, reactive, ref, watch } from "vue";
import { ElCard, ElDialog, ElMessageBox } from "element-plus";
import HostPreparationPanel from "./HostPreparationPanel.vue";
import BlockingJobAction from "./BlockingJobAction.vue";
import ErrorHelpLink from "./ErrorHelpLink.vue";
import OfficialInstallationDetails from "./OfficialInstallationDetails.vue";
import { hostTime, type Host } from "./hosts";
import { request } from "./api";
import SoftwareIcon from "./SoftwareIcon.vue";
import ServiceNetworkFields from "./ServiceNetworkFields.vue";
import InstallationMaterialDetails from "./InstallationMaterialDetails.vue";
import RecipeSystemStepDetails from "./RecipeSystemStepDetails.vue";
import SoftwareParameterFields from "./SoftwareParameterFields.vue";
import { softwareJobPhaseLabel } from "./software";
import { installationWebAccess, softwareWebAccess } from "./webAccess";
import {
  defaultInstallationRuntime,
  recipeCatalogKey,
  recipeChoiceId,
  recipeCommandSteps,
  installationNeedsComponentUpdate,
  type RuntimeMode,
  materialChoiceLabel,
  serviceEndpoint,
  runtimeLabels,
  isTool,
  isDockerEngine,
  type ServiceInstallReceipt,
  type ServiceRecipe,
  type InstallationMethod,
  type InstallationSource,
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
  knownPythonPaths?: string[];
  embedded?: boolean;
}>();
const emit = defineEmits<{
  close: [];
  submitted: [ServiceInstallReceipt];
  error: [unknown];
  preparation: [string];
  register: [hostId: string, softwareType: string];
  logs: [jobId: string];
}>();
const mode = ref<RuntimeMode | "SELF">();
const sourceChoice = ref<"ONLINE" | "LIBRARY" | "UPLOAD">("ONLINE");
const selectedVersion = ref("");
const javaVersion = ref("");
const retrying = ref(false);
const existing = computed(() => retrying.value ? undefined : props.existing);
const checkedHost = ref<Host>();
const host = computed(() => checkedHost.value || props.host);
const checkingHost = ref(false);
const hostCheckError = ref("");
const engineInstalling = ref(false);
const parameterBusy = ref(false);
const parameterFields = ref<InstanceType<typeof SoftwareParameterFields>>();
let manualMode = false;
let checkGeneration = 0;
let hostCheckAbort: AbortController | undefined;
const variants = computed(() => props.recipes || (props.recipe ? [props.recipe] : []));
const catalogKey = computed(() => props.recipe ? recipeCatalogKey(props.recipe) : "");
const javaNative = computed(() => catalogKey.value === "java" && mode.value === "NATIVE" && !existing.value);
const javaVersionOf = (item: ServiceRecipe) => item.version.split("-", 1)[0]!;
function supportsNative(item: ServiceRecipe) {
  const cap = host.value?.capabilities;
  if (cap?.platform && cap.platform.replace(/x86_64$/, "amd64") !== item.platform) return false;
  if (item.commands && !item.commands.builtin) return !cap?.distribution || !item.commands.distributions?.length || item.commands.distributions.includes(cap.distribution);
  return !cap?.distribution || cap.distribution === "ubuntu" && (!cap.distributionVersion || cap.distributionVersion === "24.04");
}
const nativeVariants = computed(() => variants.value.filter(item => item.runtimeModes.includes("NATIVE") && supportsNative(item)));
const nativeOfficial = computed(() => (props.officialRecipes || []).filter(supportsNative));
const javaVersions = computed(() => Array.from(new Set(
  [...nativeVariants.value, ...nativeOfficial.value].map(javaVersionOf),
)).sort((a, b) => Number(a) - Number(b)));
const officialChoices = computed(() => nativeOfficial.value.filter(item => !javaNative.value || javaVersionOf(item) === javaVersion.value));
const offlineChoices = computed(() => (mode.value === "NATIVE" ? nativeVariants.value : variants.value).filter(item =>
  item.runtimeModes.includes(mode.value || "DOCKER") && (!javaNative.value || javaVersionOf(item) === javaVersion.value),
));
const onlinePackages = computed(() => offlineChoices.value.filter(item => item.sourceModes.includes(mode.value === "NATIVE" ? "ONLINE" : "REGISTRY")));
const archivePackages = computed(() => offlineChoices.value.filter(item => item.sourceModes.includes("ARCHIVE")));
const onlineAvailable = computed(() => onlinePackages.value.length > 0 || mode.value === "NATIVE" && officialChoices.value.length > 0);
const nativeAvailable = computed(() => !!nativeVariants.value.length || !!nativeOfficial.value.length);
const dockerAvailable = computed(() => variants.value.some((item) => item.runtimeModes.includes("DOCKER")));
const choices = computed(() => sourceChoice.value === "ONLINE" ? [...(mode.value === "NATIVE" ? officialChoices.value : []), ...onlinePackages.value] : archivePackages.value);
const recipe = computed(() => existing.value?.installation?.definition || choices.value.find((item) => recipeChoiceId(item) === selectedVersion.value) || choices.value[0] || props.recipe);
const method = computed<InstallationMethod>(() => mode.value === "SELF" ? "SELF" : recipe.value?.sourceModes.includes("OFFICIAL") ? "OFFICIAL" : "OFFLINE");
const sourceMode = computed<InstallationSource["type"]>(() => method.value === "OFFICIAL" ? "OFFICIAL" : sourceChoice.value !== "ONLINE" ? "ARCHIVE" : mode.value === "NATIVE" ? "ONLINE" : "REGISTRY");
const commandDefinition = computed(() => plan.value?.definition?.commands || recipe.value?.commands);
const installCommands = computed(() => recipeCommandSteps(commandDefinition.value, configuration.value?.runtimeMode, sourceType.value)
  .filter(command => ["INSTALL", "VERIFY"].includes(command.phase)));
const officialRecipe = computed(() => method.value === "OFFICIAL" ? recipe.value : undefined);
function choiceLabel(item: ServiceRecipe) {
  if (item.commands && !item.commands.builtin) return `${item.version} · ${item.name}（自定义命令）`;
  if (item.sourceModes.includes("OFFICIAL")) return item.name;
  return `${item.version} · ${item.key === "java" ? "Eclipse Temurin" : item.publisher || item.name}（${mode.value === "DOCKER" ? "内置镜像" : "内置安装包"}）`;
}
const selfRecipe = computed(() => variants.value.find(item => !item.commands || item.commands.builtin));
const selfDetectionAvailable = computed(() => !!selfRecipe.value && (selfRecipe.value.kind !== "TOOL" || ["java", "python", "ffmpeg", "git", "codex", "claude"].includes(selfRecipe.value.key)));
const steps = ["安装设置", "确认安装", "执行结果"];
const flowProps = reactive({ includeLogs: false, open: computed(() => props.open), recipe, host, method, sourceMode, runtimeMode: computed(() => mode.value === "SELF" ? undefined : mode.value), existing });
function selectMode(value: RuntimeMode | "SELF") {
  manualMode = true;
  mode.value = value;
}
function applyDefault() {
  const compatible = [...variants.value, ...nativeOfficial.value].map(item => ({ ...item, runtimeModes: item.runtimeModes.filter(runtime => runtime !== "NATIVE" || supportsNative(item)) }));
  mode.value = catalogKey.value === "ytlabel" && props.recipe?.version === "4.0-python312" && supportsNative(props.recipe)
    ? "NATIVE" : defaultInstallationRuntime(compatible, host.value);
  if (!nativeAvailable.value && !dockerAvailable.value) mode.value = "SELF";
  const selected = choices.value.find(item => props.recipe && recipeChoiceId(item) === recipeChoiceId(props.recipe)) ||
    choices.value.find(item => item.version === props.recipe?.version);
  const preferred = ((!props.recipe?.commands || props.recipe.commands.builtin) && choices.value.find(item => item.sourceModes.includes("OFFICIAL"))) || selected || choices.value[0];
  selectedVersion.value = preferred ? recipeChoiceId(preferred) : "";
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
    if (!manualMode || mode.value === "NATIVE" && !nativeAvailable.value) applyDefault();
  } catch (error) {
    if (generation === checkGeneration && props.open && !abort.signal.aborted)
      hostCheckError.value = error instanceof Error ? error.message : "主机检测未完成";
  } finally {
    if (generation === checkGeneration) checkingHost.value = false;
  }
}
watch([() => props.open, () => props.host?.id, () => props.recipe?.key, () => props.existing?.id], () => {
  retrying.value = false;
  checkGeneration++;
  hostCheckAbort?.abort();
  checkingHost.value = false;
  checkedHost.value = undefined;
  hostCheckError.value = "";
  manualMode = false;
  engineInstalling.value = false;
  sourceChoice.value = "ONLINE";
  javaVersion.value = props.recipe ? javaVersionOf(props.recipe) : "21";
  if (!props.open) return;
  if (props.existing) {
    mode.value = props.existing.installation?.config?.runtimeMode;
    return;
  }
  applyDefault();
  if (!mode.value) void checkHost();
}, { immediate: true });
watch(javaVersions, versions => {
  if (versions.length && !versions.includes(javaVersion.value)) javaVersion.value = versions.includes("21") ? "21" : versions[0]!;
}, { immediate: true });
watch([onlineAvailable, archivePackages], ([online, archives]) => {
  if (!online && archives.length) sourceChoice.value = "LIBRARY";
  else if (online && !archives.length) sourceChoice.value = "ONLINE";
}, { flush: "sync", immediate: true });
watch(choices, (items, previous) => {
  if (!items.some((item) => recipeChoiceId(item) === selectedVersion.value)) {
    const version = previous?.find(item => recipeChoiceId(item) === selectedVersion.value)?.version || props.recipe?.version;
    const matching = items.find(item => item.version === version);
    selectedVersion.value = matching ? recipeChoiceId(matching) : items.length === 1 ? recipeChoiceId(items[0]!) : "";
  }
}, { immediate: true });
onUnmounted(() => { checkGeneration++; hostCheckAbort?.abort(); });
async function resumeSoftware() {
  if (!engineInstalling.value) return;
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
  canRetryInstallation,
  retryInstallation,
  clearError,
  close: closeFlow,
} = useServiceInstallation(
  flowProps,
  () => emit("close"),
  (receipt) => emit("submitted", receipt),
);
const retryRecipe = computed(() => [...variants.value, ...nativeOfficial.value].find(item => item.key === recipe.value?.key && item.version === recipe.value?.version));
async function retryFailedInstallation() {
  if (!retryRecipe.value || !configuration.value || !canRetryInstallation.value) return;
  const context = checkGeneration;
  const saved = JSON.parse(JSON.stringify(configuration.value));
  const source = existing.value?.installation?.source?.type || sourceType.value;
  const selected = recipeChoiceId(retryRecipe.value);
  const savedExisting = !!existing.value;
  if (!retryInstallation()) return;
  if (savedExisting) {
    retrying.value = true;
    sourceChoice.value = source === "ARCHIVE" ? "LIBRARY" : "ONLINE";
    selectedVersion.value = selected;
    await nextTick();
    if (context !== checkGeneration || !props.open || !retrying.value) return;
    if (configuration.value) Object.assign(configuration.value, saved);
    const choice = (id?: string, revision?: string) => id && revision ? `${id}:${revision}` : "";
    credentialChoices.value = {
      primary: choice(saved.credentialId, saved.credentialRevision),
      access: choice(saved.accessKeyCredentialId, saved.accessKeyCredentialRevision),
      user: choice(saved.userCredentialId, saved.userCredentialRevision),
    };
  }
  touched.value = true;
}
const touched = ref(false);
async function checkInstallation() {
  if (busy.value || parameterBusy.value) return;
  if (parameterFields.value && !(await parameterFields.value.saveCredentials())) return;
  await next();
}
watch(() => props.open, () => { touched.value = false; });
const operationPending = computed(() => busy.value || parameterBusy.value || preparationOpen.value || (submissionAttempted.value && !receipt.value));
const alreadyInstalled = computed(() => actionError.value?.code === "OFFICIAL_PACKAGE_ALREADY_INSTALLED");
async function close(done?: () => void) {
  const revision = checkGeneration;
  if (!operationPending.value && !engineInstalling.value && !(await confirmDiscard())) return;
  if (revision !== checkGeneration || !props.open) return;
  if (done) done(); else closeFlow();
}
async function confirmDiscard() {
  if (touched.value && !submitted.value) {
    try { await ElMessageBox.confirm("放弃本次安装设置？", "关闭窗口", { confirmButtonText: "放弃修改", cancelButtonText: "继续编辑" }); } catch { return false; }
  }
  return true;
}
const visibleStep = computed(() => step.value === 4 ? 2 : step.value === 3 ? 1 : 0);
let generatedWebURL = "";
let webURLDraft: typeof configuration.value;
watch([configuration, () => host.value?.address, () => configuration.value?.hostPort, () => configuration.value?.bindAddress,
  () => configuration.value?.ports[recipe.value?.web?.portKey || "main"]], () => {
  const config = configuration.value, definition = recipe.value;
  if (!config || !definition?.web || definition.web.urlParameter !== "EXTERNAL_URL" || existing.value || submitted.value) {
    generatedWebURL = "";
    webURLDraft = undefined;
    return;
  }
  const initial = webURLDraft !== config;
  webURLDraft = config;
  const url = installationWebAccess({ ...definition, web: { ...definition.web, urlParameter: undefined } }, config, host.value)?.url;
  if (!url) {
    if (config.parameters?.EXTERNAL_URL === generatedWebURL) config.parameters.EXTERNAL_URL = "";
    generatedWebURL = "";
    return;
  }
  const current = config.parameters?.EXTERNAL_URL || "";
  const declared = definition.parameters?.find(field => field.key === "EXTERNAL_URL")?.default;
  if (!current || current === generatedWebURL || initial && current === declared)
    (config.parameters ||= {}).EXTERNAL_URL = url;
  generatedWebURL = url;
}, { immediate: true });
const webPreview = computed(() => {
  const config = plan.value?.config || configuration.value;
  return recipe.value && config ? installationWebAccess(plan.value?.definition || recipe.value, config, host.value) : undefined;
});
const installedWeb = computed(() => existing.value ? softwareWebAccess(existing.value, host.value) : job.value?.status === "SUCCEEDED" && !job.value.targetAckPending ? webPreview.value : undefined);
const successfulChecks = computed(() => plan.value?.checks.filter(check => check.status !== "WARNING") || []);
const warningChecks = computed(() => plan.value?.checks.filter(check => check.status === "WARNING") || []);
function showJobLogs(id = receipt.value?.jobId) {
  if (!id) return;
  emit("logs", id);
  if (!props.embedded) closeFlow();
}
async function showEngineLogs(id: string) {
  if (!id || operationPending.value) return;
  const revision = checkGeneration;
  if (!(await confirmDiscard()) || revision !== checkGeneration || !props.open) return;
  engineInstalling.value = false;
  emit("logs", id);
  closeFlow();
}
function previous() { step.value = 0; clearError(); }
async function uploadInstallationPackage() {
  await uploadMaterial();
  if (!selectedFile.value && selectedMaterial.value?.available) sourceChoice.value = "LIBRARY";
}
</script>

<template>
  <component
    :is="embedded ? ElCard : ElDialog"
    class="service-install-dialog"
    :model-value="open"
    :title="`${existing ? '安装进度 ·' : '安装'} ${recipe?.name || '服务'}`"
    width="min(1120px, calc(100vw - 40px))"
    align-center
    destroy-on-close
    :close-on-click-modal="true"
    :before-close="close"
    :close-on-press-escape="true"
    :show-close="true"
    @close="closeFlow"
  >
    <template #header>
      <header v-if="recipe && host" class="install-context">
        <SoftwareIcon :type="recipeCatalogKey(recipe)" />
        <div>
          <strong
            >{{ recipe.name }} <span>{{ mode === 'SELF' ? '已有软件' : '' }}</span></strong
          ><small>{{ host.name }} · {{ host.address || "本机" }}</small>
        </div>
        <span class="context-kind">{{
          mode === 'SELF' ? '只读检测登记' : mode ? runtimeLabels[mode] : '选择安装方式'
        }}</span>
      </header>
    </template>
    <div v-if="recipe && host && configuration" v-show="!engineInstalling && !preparationOpen" class="service-install" @input="touched = true" @change="touched = true">
      <ol v-if="mode && method !== 'SELF' && !existing" class="install-steps official-steps" aria-label="安装步骤">
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
      <div class="installation-workspace" :class="{ 'execution-workspace': step === 4 || method === 'SELF' }">
      <main class="installation-main">

      <div v-if="!submitted && !existing" class="installation-methods" role="group" aria-label="安装方式">
        <button v-if="dockerAvailable" :class="{ active: mode === 'DOCKER' }" :aria-pressed="mode === 'DOCKER'" :disabled="busy || submissionAttempted || submitted" @click="selectMode('DOCKER')">Docker 安装</button>
        <button v-if="nativeAvailable" :class="{ active: mode === 'NATIVE' }" :aria-pressed="mode === 'NATIVE'" :disabled="busy || submissionAttempted || submitted" @click="selectMode('NATIVE')">原生安装</button>
        <button v-if="selfRecipe" :class="{ active: mode === 'SELF' }" :aria-pressed="mode === 'SELF'" :disabled="busy || submissionAttempted || submitted" @click="selectMode('SELF')">管理已有软件</button>
      </div>
      <div v-if="!existing && !nativeAvailable" class="host-readiness">
        当前系统暂无原生安装方案，可管理已有软件。
      </div>
      <div v-if="checkingHost || hostCheckError || !mode" class="host-readiness" role="status">
        <span>{{ checkingHost ? '正在检测安装环境…' : hostCheckError || '请选择安装方式，或重新检测主机' }}</span>
        <button v-if="!checkingHost" class="quiet" :disabled="busy || submitted" @click="checkHost">重新检测</button>
      </div>
      <div v-if="mode === 'DOCKER' && host.capabilities?.docker.status !== 'AVAILABLE' && step < 3" class="host-readiness">
        <span>{{ host.capabilities?.docker.code === 'DOCKER_NOT_INSTALLED' ? '未安装 Docker' : host.capabilities?.docker.message || 'Docker 状态待确认' }}</span>
        <button v-if="host.capabilities?.docker.code === 'DOCKER_NOT_INSTALLED' && props.dockerRecipe" class="secondary" :disabled="busy" @click="engineInstalling = true">安装 Docker</button>
      </div>
      <div v-if="mode && mode !== 'SELF' && step < 3 && !existing" class="form-row installation-selection">
        <label v-if="javaNative">Java 版本<select v-model="javaVersion" aria-label="Java 版本" :disabled="busy">
          <option v-for="version in javaVersions" :key="version" :value="version">Java {{ version }}</option>
        </select></label>
        <label>安装来源<select v-model="sourceChoice" aria-label="安装来源" :disabled="busy">
          <option v-if="onlineAvailable" value="ONLINE">在线安装</option>
          <option v-if="archivePackages.length" value="LIBRARY">平台离线包</option>
          <option v-if="archivePackages.length" value="UPLOAD">上传安装包</option>
        </select></label>
        <label v-if="!javaNative || choices.length > 1">安装方案<select v-model="selectedVersion" aria-label="安装方案" :disabled="busy"><option value="" disabled>请选择安装方案</option><option v-for="item in choices" :key="recipeChoiceId(item)" :value="recipeChoiceId(item)">{{ choiceLabel(item) }}</option></select></label>
      </div>

      <div v-if="fieldError" class="install-error" role="alert">
        {{ fieldError }}
      </div>
      <div v-if="actionError" :class="alreadyInstalled ? 'host-readiness' : 'install-error'" :role="alreadyInstalled ? 'status' : 'alert'">
        <strong>{{ alreadyInstalled ? '服务器已安装此软件，无需重复安装。' : actionError.message }}</strong>
        <BlockingJobAction v-if="actionError.blockingJob?.id" :job="actionError.blockingJob" :disabled="busy" />
        <div class="install-error-actions">
          <button v-if="alreadyInstalled && selfDetectionAvailable" class="secondary" :disabled="busy" @click="emit('register', host.id, recipe.key.toUpperCase())">检测并登记</button>
          <button v-else-if="['COMMAND_TRUST_REQUIRED', 'EXECUTOR_MISSING', 'SUDO_AUTHORIZATION_REQUIRED', 'EXECUTOR_PRIVILEGE_REQUIRED'].includes(actionError.code) || installationNeedsComponentUpdate(actionError.code)" class="secondary" :disabled="busy" @click="preflight">{{ installationNeedsComponentUpdate(actionError.code) ? '重新检查' : '准备权限并继续' }}</button>
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
        </div>
        <small v-if="submissionAttempted && !receipt"
          >提交结果待确认，可重试本次提交，或到软件管理查看安装日志。</small
        >
      </div>

      <section v-if="method === 'SELF'" class="install-panel self-install" aria-label="自行安装">
        <h3>检测服务器已有软件</h3>
        <a v-if="selfRecipe?.officialUrl" :href="selfRecipe.officialUrl" target="_blank" rel="noopener noreferrer">查看官方安装说明 ↗</a>
        <p v-if="!selfDetectionAvailable">此软件暂不支持自动工具登记，可先按官方说明在服务器使用。</p>
        <button v-if="selfDetectionAvailable" class="primary" @click="emit('register', host.id, selfRecipe?.key.toUpperCase() || 'OTHER')">检测并登记</button>
      </section>
      <div v-if="mode && mode !== 'SELF' && step < 3 && !existing" class="settings-grid" :class="{ 'official-settings': method === 'OFFICIAL' }">
      <section v-if="method === 'OFFICIAL'" class="install-panel" aria-label="官方安装配置">
        <label>实例名称<input v-model="configuration.name" aria-label="实例名称" :disabled="busy" maxlength="120" /></label>
      </section>
      <section v-else-if="sourceChoice !== 'ONLINE'" class="install-panel source-panel" aria-label="安装来源">
        <h3>安装包</h3>
        <div v-if="sourceChoice === 'LIBRARY'" class="archive-picker">
          <label>安装包<select v-model="materialId" aria-label="可用安装包" :disabled="busy">
            <option value="">{{ materials.some(item => item.available) ? '请选择安装包' : '当前版本暂无离线包' }}</option>
            <option v-for="item in materials" :key="item.id" :value="item.id" :disabled="!item.available">{{ materialChoiceLabel(item) }}</option>
          </select></label>
          <div v-if="configuration.runtimeMode === 'NATIVE' && recipe.native?.parts.length" class="inline-actions">
            <button class="secondary" :disabled="busy || officialNeedsImport" @click="prepareOfficialMaterial">下载到平台</button>
            <button v-if="officialNeedsImport" class="secondary" :disabled="busy" @click="prepareOfficialAgain">重新下载</button>
            <button v-if="uploadController" class="quiet danger" @click="uploadController.abort()">中断等待</button>
            <span aria-live="polite">{{ officialProgress }}</span>
          </div>
        </div>
        <div v-if="sourceChoice === 'UPLOAD'" class="archive-upload">
          <label>{{ configuration.runtimeMode === 'NATIVE' ? '安装包' : '镜像归档' }}<input
            type="file" :accept="recipe.commands && !recipe.commands.builtin ? '' : configuration.runtimeMode === 'NATIVE' ? recipe.native?.parts[0]?.format === 'RAW' ? '' : '.tar,.tar.gz,.tgz,.tar.xz,.xz' : '.tar'"
            aria-label="上传安装包" :disabled="busy" @change="fileChanged" /></label>
          <label>SHA-256<input v-model="archiveSHA" aria-label="归档 SHA-256" maxlength="64" spellcheck="false" :disabled="busy" placeholder="安装包的完整 SHA-256" /></label>
          <div class="inline-actions">
            <button class="secondary" :disabled="busy || !selectedFile || materialNeedsImport" @click="uploadInstallationPackage">上传并校验</button>
            <button v-if="uploadController" class="quiet danger" @click="uploadController.abort()">中断操作</button>
            <button v-if="materialNeedsImport" class="secondary" :disabled="busy" @click="prepareMaterialImport">重新导入</button>
            <span aria-live="polite">{{ materialProgress }}</span>
          </div>
          <details v-if="configuration.runtimeMode === 'NATIVE' && recipe.native?.parts.length">
            <summary>下载地址与打包方法</summary>
            <p>{{ recipe.native.parts.length === 1 ? '直接上传官方原包，SHA-256 已预填。' : '下载以下文件后打成 tar 归档，再计算 SHA-256。' }}</p>
            <div v-for="part in recipe.native.parts" :key="part.name" class="native-part">
              <a :href="part.url" target="_blank" rel="noopener noreferrer">{{ part.name }} ↗</a><code>{{ part.sha256 }}</code>
            </div>
            <code v-if="recipe.native.parts.length > 1">tar -cf {{ recipe.key }}-offline.tar {{ recipe.native.parts.map(part => part.name).join(' ') }}</code>
          </details>
          <p v-else-if="configuration.runtimeMode === 'DOCKER' && (!recipe.commands || recipe.commands.builtin)" class="source-summary">上传 docker save 导出的 .tar 文件。</p>
        </div>
        <details v-if="sourceType === 'ARCHIVE' && selectedMaterial?.errorCode"><summary>安装包校验详情</summary><code>{{ selectedMaterial.errorCode }}</code><ErrorHelpLink :code="selectedMaterial.errorCode" /></details>

      </section>

      <section
        v-if="method !== 'OFFICIAL'"
        class="install-panel instance-panel"
        aria-label="实例配置"
      >
        <h3>{{ credentialRoles.length ? '名称与账号' : '安装设置' }}</h3>
        <label
          >实例名称<input
            v-model="configuration.name"
            aria-label="实例名称"
            :disabled="busy"
            maxlength="120"
        /></label>
        <label v-if="recipe.commands && !recipe.commands.builtin && recipe.commands.steps.some(command => command.runAs === 'ACCOUNT')">执行账号<input v-model="configuration.account" aria-label="执行账号" :disabled="busy" placeholder="例如 ubuntu" maxlength="32" autocomplete="username" /></label>
        <SoftwareParameterFields v-if="recipe.parameters?.length" :key="`${host.id}:${recipe.key}:${recipe.version}`" ref="parameterFields" class="custom-install-parameters" :fields="recipe.parameters" :model-value="configuration.parameters || {}" :credential-refs="configuration.parameterCredentials" :path-options="{ PYTHON_BINARY: props.knownPythonPaths || [] }" :disabled="busy" :context-name="configuration.name" @update:model-value="configuration.parameters = $event" @update:credential-refs="configuration.parameterCredentials = $event" @busy="parameterBusy = $event" @draft="touched = true" @error="emit('error', $event)" />
        <p v-if="recipeCatalogKey(recipe) === 'ytlabel'" class="install-note">ytlabel 无登录认证，请在受限网络使用。</p>
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
                {{ item.name }}
              </option>
            </select></label
          ><button class="quiet" :disabled="busy" @click="editCredential(role)">
            新建凭据
          </button>
        </div>
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
        <ul v-if="warningChecks.length" class="preflight-checks">
          <li
            v-for="check in warningChecks"
            :key="check.key"
            :class="{ warning: check.status === 'WARNING' }"
          >
            <b>{{ check.status === "WARNING" ? "!" : "✓" }}</b
            ><span>{{ check.message || "检查结果未提供说明" }}</span>
          </li>
        </ul>
        <details v-if="successfulChecks.length" class="check-details"><summary>{{ successfulChecks.length }} 项检查通过</summary>
          <ul class="preflight-checks"><li v-for="check in successfulChecks" :key="check.key"><b>✓</b><span>{{ check.message || "检查通过" }}</span></li></ul>
        </details>
        <OfficialInstallationDetails v-if="plan.official" :installation="plan.official" :commands="!installCommands.length" />
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
                  ? plan.definition.commands && !plan.definition.commands.builtin ? "按命令在线安装" : plan.source.materialId ? "平台下载后分发" : "目标服务器在线下载"
                  : "在线镜像"
                : selectedMaterial ? materialChoiceLabel(selectedMaterial) : "离线材料库"
            }}
          </dd>
          <dt>访问入口</dt>
          <dd>
            <code>{{ webPreview ? webPreview.url || webPreview.reason : serviceEndpoint(recipe, plan.config) }}</code>
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
              : "可继续原作业。已进入系统变更的作业会先核对结果。"
          }}
        </p>
        <div v-if="job?.status === 'UNKNOWN' || job?.targetAckPending || job?.status === 'QUEUED'" class="inline-actions">
          <button
            v-if="job?.status === 'UNKNOWN' && recipe.key !== 'docker' && !['node', 'npm', 'pnpm', 'nvm'].includes(recipe.key)"
            class="primary"
            :disabled="busy"
            @click="jobAction('resume')"
          >继续安装</button>
          <button
            v-if="job?.status === 'UNKNOWN' || job?.targetAckPending"
            class="secondary"
            :disabled="busy"
            @click="jobAction('reconcile')"
          >
            核对原作业</button
          ><button
            v-if="job?.status === 'QUEUED' && job.phase !== 'INSTALL_RESUMING'"
            class="quiet danger"
            :disabled="busy"
            @click="jobAction('cancel')"
          >
            取消排队</button
          >
        </div>
        <div class="inline-actions">
          <button v-if="canRetryInstallation && retryRecipe" class="primary" @click="retryFailedInstallation()">重新安装</button>
          <a v-if="installedWeb?.url" class="primary installed-web-link" :href="installedWeb.url" target="_blank" rel="noopener noreferrer">进入系统 ↗</a>
          <button class="secondary" @click="showJobLogs()">查看日志</button>
        </div>
      </section>
      </main>
      <aside v-if="mode && mode !== 'SELF' && step < 4" class="installation-overview" aria-label="安装概览">
        <div class="overview-heading"><span>安装概览</span><b>{{ mode === 'DOCKER' ? 'Docker' : '原生' }}</b></div>
        <dl class="overview-facts">
          <dt>软件</dt><dd>{{ recipe.name }}</dd>
          <template v-if="method !== 'OFFICIAL'"><dt>版本</dt><dd>{{ recipe.version }}</dd></template>
          <dt>来源</dt><dd>{{ sourceChoice === 'ONLINE' ? '在线安装' : sourceChoice === 'LIBRARY' ? '平台离线包' : '上传安装包' }}</dd>
          <dt v-if="configuration.account">执行账号</dt><dd v-if="configuration.account">{{ configuration.account }}</dd>
          <template v-if="!isTool(recipe) && !isDockerEngine(recipe) && configuration.hostPort"><dt>监听地址</dt><dd class="overview-code">{{ configuration.bindAddress }}:{{ configuration.hostPort }}</dd></template>
          <dt>{{ plan ? '安装目录' : '存储根目录' }}</dt><dd class="overview-code">{{ method === 'OFFICIAL' ? '系统标准目录' : plan?.paths.root || configuration.storageRoot || host.installRoot }}</dd>
        </dl>
        <a v-if="recipe.officialUrl" class="overview-source" :href="recipe.officialUrl" target="_blank" rel="noopener noreferrer">查看来源 ↗</a>
        <details v-if="installCommands.length" class="command-preview" :open="step === 3" aria-label="安装命令">
          <summary>执行步骤 <span>{{ installCommands.length }} 步</span></summary>
          <details v-for="(command, index) in installCommands" :key="index" class="command-step">
            <summary>{{ index + 1 }}. {{ command.name }}</summary>
            <small v-if="command.kind !== 'SYSTEM'">{{ command.runAs === 'ROOT' ? '管理员' : commandDefinition?.builtin ? '安装账号' : configuration.account || '所选账号' }}</small>
            <small v-if="command.workingDirectory">{{ command.workingDirectory }}</small>
            <RecipeSystemStepDetails v-if="command.kind === 'SYSTEM'" :recipe="plan?.definition || recipe" :step="command" :context="plan || { config: configuration, source: { type: sourceType } }" />
            <pre v-else>{{ command.script }}</pre>
          </details>
        </details>
        <details v-if="method !== 'OFFICIAL'" class="source-details"><summary>文件与来源详情</summary><InstallationMaterialDetails
          :recipe="recipe" :runtime-mode="configuration.runtimeMode"
          :material="sourceType === 'ARCHIVE' ? selectedMaterial : undefined" :online="sourceType !== 'ARCHIVE'"
        /></details>
      </aside>
      </div>
    </div>
    <ServiceInstallDialog v-if="engineInstalling && props.dockerRecipe" embedded :open="true" :recipe="props.dockerRecipe" :recipes="[props.dockerRecipe]" :official-recipes="props.dockerOfficialRecipes" :host="host" @close="resumeSoftware" @submitted="emit('submitted', $event)" @logs="showEngineLogs" />
    <section v-if="preparationOpen && host" class="inline-preparation">
      <h3>准备安装环境</h3>
      <HostPreparationPanel :key="`${host.id}:${automaticUpdate?.mode || 'UPDATE'}`" :host="readinessHost || host" :automatic-update="automaticUpdate" :update-only="!automaticUpdate || automaticUpdate.mode === 'UPDATE'" @updated="preparationChecked" />
      <button class="secondary" @click="preparationOpen = false">返回安装设置</button>
    </section>
    <template #footer
      ><div class="install-footer">
        <button class="secondary" @click="close()">
          {{ embedded ? "返回软件安装" : submitted || operationPending || engineInstalling ? "关闭" : "取消" }}</button
        ><span class="footer-spacer"></span
        ><template v-if="!engineInstalling && !preparationOpen"><button
          v-if="method !== 'SELF' && step === 3"
          class="secondary"
          :disabled="busy || submissionAttempted"
          @click="previous"
        >
          上一步</button
        ><button
          v-if="method !== 'SELF' && step < 3 && !existing"
          class="primary"
          :disabled="busy || parameterBusy || !mode || !selectedVersion || !recipe || !host || sourceChoice === 'UPLOAD' || (method === 'OFFICIAL' && !officialRecipe)"
          @click="checkInstallation"
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
        </template>
      </div></template
    >
  </component>
</template>

<style scoped src="./service-installation.css"></style>
<style scoped>
.installed-web-link { width: auto; margin: 0; padding: 9px 14px; border-radius: var(--control-radius); color: white; text-decoration: none; font-size: 12px; }
.custom-install-parameters { grid-column: 1 / -1; }
.service-install { gap: 24px; }
.install-context { padding: 0 30px 0 0; border: 0; min-height: 46px; }
.install-context strong { font-size: 21px; letter-spacing: -.4px; color: #17283c; }
.install-context small { font-size: 12px; color: #708094; }
.context-kind { margin-left: auto; background: #edf3f8; color: #506b85; border-radius: 5px; padding: 5px 8px; }
.official-steps { grid-template-columns: repeat(3, minmax(0, 1fr)); gap: 0; padding: 0 0 20px; border-bottom: 1px solid #e7ecf2; }
.install-steps li { gap: 9px; }
.install-steps li:not(:last-child)::after { content: ""; height: 1px; background: #e6ebf1; flex: 1; margin: 0 20px; }
.install-steps li > span { width: 23px; height: 23px; font-size: 11px; }
.install-steps small { font-size: 12px; white-space: nowrap; }
.installation-workspace { display: grid; grid-template-columns: minmax(0, 1fr) 310px; align-items: start; gap: 30px; min-width: 0; }
.installation-main { display: grid; gap: 22px; min-width: 0; }
.execution-workspace { grid-template-columns: minmax(0, 1fr); }
.installation-methods { display: flex; flex-wrap: wrap; gap: 0; padding: 0; border-bottom: 1px solid #dfe7f0; }
.installation-methods button { flex: 1; border: 0; border-bottom: 2px solid transparent; background: transparent; border-radius: 0; padding: 10px 8px 13px; color: #65768b; font-size: 13px; }
.installation-methods button.active { border-bottom-color: #2563eb; color: #1d4ed8; font-weight: 600; }
.installation-methods button:hover:not(:disabled) { color: #1d4ed8; background: #f7f9fc; }
.installation-selection { display: grid; grid-template-columns: repeat(auto-fit, minmax(170px, 1fr)); gap: 16px; }
.installation-selection > label { display: grid; gap: 8px; min-width: 0; color: #53657a; font-size: 12px; }
.installation-selection select { margin: 0; height: 40px; width: 100%; min-width: 0; padding: 8px 10px; }
.settings-grid { display: grid; grid-template-columns: minmax(0, 1fr); gap: 24px; min-width: 0; }
.settings-grid > section + section { border-top: 1px solid #e7ecf2; padding-top: 22px; }
.install-panel { gap: 16px; }
.install-panel h3 { font-size: 13px; letter-spacing: .1px; color: #273c53; }
.instance-panel { grid-template-columns: minmax(0, 1fr) minmax(0, 1fr); align-items: start; }
.instance-panel > h3, .instance-panel > .form-row, .instance-panel > .credential-field, .instance-panel > .credential-editor { grid-column: 1 / -1; }
.host-readiness { display: flex; flex-wrap: wrap; align-items: center; gap: 10px; font-size: 12px; color: #63778e; line-height: 1.6; }
.self-install { padding: 8px 0 20px; }
.self-install p { margin: 0; font-size: 13px; line-height: 1.9; color: var(--muted); }
.self-install .primary { justify-self: start; width: auto; }
.install-panel > a { font-size: 12px; overflow-wrap: anywhere; }
.install-error-actions { display: flex; flex-wrap: wrap; align-items: start; gap: 10px 16px; }
.install-diagnostics { min-width: 0; max-width: 100%; }
.install-diagnostics summary { cursor: pointer; }
.install-diagnostics small, .install-diagnostics code { display: block; margin-top: 6px; }
.source-summary { margin: 0; color: var(--muted); font-size: 12px; }
.installation-overview { position: sticky; top: 0; padding: 20px; background: #f5f7fa; border: 1px solid #e5eaf0; border-radius: 9px; min-width: 0; }
.overview-heading { display: flex; justify-content: space-between; align-items: center; gap: 10px; margin-bottom: 20px; font-weight: 600; font-size: 13px; color: #273c53; }
.overview-heading b { font-size: 10px; letter-spacing: .2px; font-weight: 500; padding: 4px 6px; background: #e7edf4; border-radius: 3px; color: #61748b; }
.overview-facts { display: grid; grid-template-columns: 72px minmax(0, 1fr); gap: 14px 10px; margin: 0; font-size: 12px; line-height: 1.5; }
.overview-facts dt { color: #7b899a; }
.overview-facts dd { margin: 0; color: #344b63; overflow-wrap: anywhere; }
.overview-code { font: 11px/1.7 Consolas, monospace; }
.overview-source { display: inline-block; font-size: 11px; margin-top: 18px; color: #607e9d; }
.source-details, .command-preview { border-top: 1px solid #dfe6ee; margin-top: 18px; padding-top: 16px; min-width: 0; }
.source-details summary, .command-preview > summary { color: #516c87; font-size: 12px; cursor: pointer; }
.command-preview > summary span { float: right; color: #8a97a5; font-size: 11px; }
.command-step { margin-top: 10px; }
.command-step > summary { cursor: pointer; color: #3c566d; font-size: 12px; }
.command-step small { display: block; margin-top: 6px; color: #63778a; font-size: 11px; overflow-wrap: anywhere; }
.command-step pre { margin: 9px 0 0; padding: 12px; border: 1px solid #dde5ee; background: #fff; border-radius: 5px; white-space: pre-wrap; overflow-wrap: anywhere; font: 11px/1.75 Consolas, monospace; }
.source-details :deep(.material-contents) { margin-top: 14px; padding: 0; border: 0; }
.check-details { color: var(--muted); font-size: 12px; }
.check-details > summary { cursor: pointer; }
.execution-panel { gap: 18px; }
.job-result { display: flex; align-items: center; justify-content: space-between; flex-wrap: wrap; gap: 10px; padding: 16px 18px; border-radius: 6px; }
.job-result strong { font-size: 15px; }

.inline-preparation { display: grid; gap: 16px; min-width: 0; }
@media (max-width: 850px) {
  .installation-workspace { grid-template-columns: minmax(0, 1fr); gap: 22px; }
  .installation-overview { position: static; padding: 16px; }
  .overview-facts { grid-template-columns: 72px minmax(0, 1fr) 72px minmax(0, 1fr); }
  .source-details, .command-preview { margin-top: 14px; }
}
@media (max-width: 520px) {
  .service-install { gap: 18px; }
  .install-context { gap: 10px; padding-right: 22px; }
  .install-context strong { font-size: 17px; }
  .install-context small { font-size: 10px; }
  .context-kind { display: none; }
  .install-steps li { flex-direction: row; gap: 6px; }
  .install-steps li:not(:last-child)::after { margin: 0 8px; }
  .install-steps small { font-size: 10px; }
  .install-steps li > span { width: 20px; height: 20px; }
  .installation-methods button { font-size: 12px; padding: 10px 5px; }
  .installation-selection, .instance-panel { grid-template-columns: minmax(0, 1fr); }
  .overview-facts { grid-template-columns: 72px minmax(0, 1fr); }
  .execution-output { min-height: 230px; padding: 14px; font-size: 11px; }
}
</style>
<style>
.service-install-dialog.el-dialog { display: flex; flex-direction: column; padding: 0; border-radius: 12px; overflow: hidden; max-height: calc(100dvh - 48px); box-shadow: 0 24px 80px #122b4b2b; }
.service-install-dialog.el-dialog .el-dialog__header { flex: 0 0 auto; margin: 0; padding: 22px 28px; border-bottom: 1px solid #e5ebf2; }
.service-install-dialog.el-dialog .el-dialog__headerbtn { top: 24px; right: 22px; width: 28px; height: 28px; }
.service-install-dialog.el-dialog .el-dialog__body { min-height: 0; overflow-y: auto; overflow-x: hidden; padding: 22px 28px 26px; }
.service-install-dialog.el-dialog .el-dialog__footer { flex: 0 0 auto; padding: 16px 28px; border-top: 1px solid #e5ebf2; background: #fafbfd; }
.service-install-dialog.el-card { border: 0; box-shadow: none; }
@media (max-width: 520px) {
  .service-install-dialog.el-dialog { width: calc(100vw - 16px) !important; max-height: calc(100dvh - 16px); border-radius: 9px; }
  .service-install-dialog.el-dialog .el-dialog__header { padding: 18px 16px; }
  .service-install-dialog.el-dialog .el-dialog__headerbtn { top: 20px; right: 14px; }
  .service-install-dialog.el-dialog .el-dialog__body { padding: 18px 16px; }
  .service-install-dialog.el-dialog .el-dialog__footer { padding: 13px 16px; }
}
</style>
