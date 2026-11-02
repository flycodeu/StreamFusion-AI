<script setup lang="ts">
import { computed, nextTick, reactive, ref, watch } from "vue";
import { ElDialog, ElDrawer, ElMessageBox } from "element-plus";
import HostPreparationPanel from "./HostPreparationPanel.vue";
import ErrorHelpLink from "./ErrorHelpLink.vue";
import OfficialInstallationDetails from "./OfficialInstallationDetails.vue";
import { hostTime } from "./hosts";
import SoftwareIcon from "./SoftwareIcon.vue";
import ServiceNetworkFields from "./ServiceNetworkFields.vue";
import InstallationMaterialDetails from "./InstallationMaterialDetails.vue";
import { softwareJobPhaseLabel } from "./software";
import {
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

const props = defineProps<ServiceInstallationProps & { officialRecipes?: ServiceRecipe[] }>();
const emit = defineEmits<{
  close: [];
  submitted: [ServiceInstallReceipt];
  error: [unknown];
  preparation: [string];
  register: [hostId: string, softwareType: string];
}>();
const method = ref<InstallationMethod>("OFFICIAL");
const officialVersion = ref("");
const officialRecipe = computed(() => props.officialRecipes?.find((item) => item.version === officialVersion.value) || props.officialRecipes?.[0]);
const recipe = computed(() => method.value === "OFFICIAL" ? officialRecipe.value || props.recipe : props.recipe);
const selfDetectionAvailable = computed(() => props.recipe?.kind !== "TOOL" || ["java", "python", "ffmpeg"].includes(props.recipe?.key || ""));
const steps = computed(() => method.value === "OFFICIAL" ? ["官方安装", "预检确认", "执行结果"] : ["安装来源", "实例配置", "网络存储", "预检确认", "执行结果"]);
const flowProps = reactive({ open: computed(() => props.open), recipe, host: computed(() => props.host), method });
watch(() => props.open, (open) => { if (open) { method.value = "OFFICIAL"; officialVersion.value = props.officialRecipes?.[0]?.version || ""; } });
watch(() => props.officialRecipes, (items) => {
  if (!items?.some((item) => item.version === officialVersion.value)) officialVersion.value = items?.[0]?.version || "";
}, { immediate: true });
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
const closeBlocked = computed(() => busy.value || preparationOpen.value || (submissionAttempted.value && !receipt.value) || !!job.value && (['QUEUED', 'RUNNING', 'UNKNOWN'].includes(job.value.status) || job.value.targetAckPending));
async function close(done?: () => void) {
  if (closeBlocked.value) return;
  if (touched.value && !submitted.value) {
    try { await ElMessageBox.confirm("放弃本次安装设置？", "关闭窗口", { confirmButtonText: "放弃修改", cancelButtonText: "继续编辑" }); } catch { return; }
  }
  if (done) done(); else closeFlow();
}
const visibleStep = computed(() => method.value === "OFFICIAL" ? step.value === 4 ? 2 : step.value === 3 ? 1 : 0 : step.value);
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
function previous() { step.value = method.value === "OFFICIAL" ? 0 : step.value - 1; clearError(); }
</script>

<template>
  <ElDrawer
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
    <div v-if="recipe && host && configuration" class="service-install" @input="touched = true" @change="touched = true">
      <header class="install-context">
        <SoftwareIcon :type="recipe.key" />
        <div>
          <strong
            >{{ recipe.name }} <span>{{ method === 'OFFLINE' ? recipe.version : method === 'OFFICIAL' ? '版本以目标仓库预检为准' : '自行安装后登记' }}</span></strong
          ><small>{{ host.name }} · {{ host.address || "本机" }}</small>
        </div>
        <span class="context-kind">{{
          method === 'OFFLINE' ? runtimeLabels[configuration.runtimeMode] : method === 'OFFICIAL' ? '官方系统安装' : '只读检测登记'
        }}</span>
      </header>
      <div class="installation-methods" role="group" aria-label="安装方式">
        <button v-for="choice in [{ key: 'OFFICIAL', label: '官方安装' }, { key: 'OFFLINE', label: 'Ops 离线安装' }, { key: 'SELF', label: '自行安装' }]"
          :key="choice.key" :class="{ active: method === choice.key }" :aria-pressed="method === choice.key"
          :disabled="busy || submissionAttempted || submitted" @click="method = choice.key as InstallationMethod">{{ choice.label }}</button>
      </div>
      <ol v-if="method !== 'SELF'" class="install-steps" :class="{ 'official-steps': method === 'OFFICIAL' }" aria-label="安装步骤">
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
        <h3>在服务器自行安装，再由 Ops 检测登记</h3>
        <p>按软件官方说明在 {{ host.name }} 安装。登记后先只读查看，生命周期接管另行确认。</p>
        <a :href="props.recipe?.officialUrl" target="_blank" rel="noopener noreferrer">查看官方安装说明 ↗</a>
        <p v-if="!selfDetectionAvailable">当前工具检测支持 Java、Python 和 FFmpeg。此软件暂不支持自动工具登记，可先按官方说明在服务器使用。</p>
        <button v-if="selfDetectionAvailable" class="primary" @click="emit('register', host.id, props.recipe?.key.toUpperCase() || 'OTHER')">检测并登记</button>
      </section>
      <section v-else-if="method === 'OFFICIAL' && step === 0" class="install-panel" aria-label="官方安装配置">
        <template v-if="officialRecipe">
          <label v-if="(props.officialRecipes?.length || 0) > 1">官方配方<select v-model="officialVersion" aria-label="官方配方" :disabled="busy"><option v-for="item in props.officialRecipes" :key="item.version" :value="item.version">{{ item.name }}</option></select></label>
          <label>实例名称<input v-model="configuration.name" aria-label="实例名称" :disabled="busy" maxlength="120" /></label>
          <a :href="officialRecipe.officialUrl" target="_blank" rel="noopener noreferrer">官方安装说明 ↗</a>
        </template>
        <p v-else class="install-note">此软件暂未提供可执行的固定官方配方。请选择 Ops 离线安装，或查看官方说明自行安装。</p>
      </section>
      <section v-else-if="step === 0" class="install-panel" aria-label="安装来源">
        <h3>运行方式</h3>
        <div class="install-options">
          <label
            v-if="recipe.runtimeModes.includes('DOCKER')"
            class="option-card"
            :class="{
              selected: configuration.runtimeMode === 'DOCKER',
              unavailable: !recipe.runtimeModes.includes('DOCKER'),
            }"
            ><input
              v-model="configuration.runtimeMode"
              type="radio"
              value="DOCKER"
              :disabled="busy || !recipe.runtimeModes.includes('DOCKER')"
            /><span
              ><strong>Docker 容器</strong
              ><small>固定镜像，独立数据目录</small></span
            ></label
          ><label
            class="option-card"
            :class="{
              selected: configuration.runtimeMode === 'NATIVE',
              unavailable: !recipe.runtimeModes.includes('NATIVE'),
            }"
            ><input
              v-model="configuration.runtimeMode"
              type="radio"
              value="NATIVE"
              :disabled="busy || !recipe.runtimeModes.includes('NATIVE')"
            /><span
              ><strong>{{
                isTool(recipe) ? "原生程序" : "原生 / systemd"
              }}</strong
              ><small>{{
                recipe.runtimeModes.includes("NATIVE")
                  ? "独立目录，官方材料"
                  : "当前配方未提供"
              }}</small></span
            ></label
          >
        </div>
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
              ><strong>本地离线材料库 · 默认</strong
              ><small>优先复用随包材料，无需再次下载</small></span
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
        <InstallationMaterialDetails
          :recipe="recipe"
          :runtime-mode="configuration.runtimeMode"
          :material="sourceType === 'REGISTRY' ? undefined : selectedMaterial"
          :online="sourceType !== 'ARCHIVE'"
        />
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
            {{ selectedMaterial.errorCode || "材料尚未校验通过" }}
          </p>
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
        v-else-if="step === 1"
        class="install-panel"
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
        v-else-if="step === 2"
        class="install-panel"
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

      <section
        v-else-if="step === 3 && plan"
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
            ><span>{{ check.message || check.key }}</span>
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
    <template #footer
      ><div class="install-footer">
        <button class="secondary" :disabled="busy" @click="close()">
          {{ submitted ? "关闭" : "取消" }}</button
        ><span class="footer-spacer"></span
        ><button
          v-if="method !== 'SELF' && step > 0 && step < 4"
          class="secondary"
          :disabled="busy || submissionAttempted"
          @click="previous"
        >
          上一步</button
        ><button
          v-if="method !== 'SELF' && step < 3"
          class="primary"
          :disabled="busy || !recipe || !host || (method === 'OFFICIAL' && !officialRecipe)"
          @click="next"
        >
          {{
            busy ? "处理中…" : method === 'OFFICIAL' ? '检查官方安装条件' : step === 2 ? "检查安装条件" : "下一步"
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
  </ElDrawer>
  <ElDialog v-model="preparationOpen" :title="automaticUpdate ? '正在准备安装环境' : '更新接入组件'" width="min(680px,96vw)" :close-on-click-modal="false" :close-on-press-escape="false" destroy-on-close>
    <HostPreparationPanel v-if="props.open && preparationOpen && props.host" :host="readinessHost || props.host" :automatic-update="automaticUpdate" update-only @updated="preparationChecked" />
  </ElDialog>
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
