<script setup lang="ts">
import { ElDrawer } from "element-plus";
import { hostTime } from "./hosts";
import SoftwareIcon from "./SoftwareIcon.vue";
import ServiceNetworkFields from "./ServiceNetworkFields.vue";
import InstallationMaterialDetails from "./InstallationMaterialDetails.vue";
import { softwareJobPhaseLabels } from "./software";
import {
  materialChoiceLabel,
  serviceEndpoint,
  runtimeLabels,
  isTool,
  isDockerEngine,
  type ServiceInstallReceipt,
} from "./serviceInstallation";
import {
  useServiceInstallation,
  type ServiceInstallationProps,
} from "./useServiceInstallation";

const props = defineProps<ServiceInstallationProps>();
const emit = defineEmits<{
  close: [];
  submitted: [ServiceInstallReceipt];
  error: [unknown];
  preparation: [string];
}>();
const steps = ["安装来源", "实例配置", "网络存储", "预检确认", "执行结果"];
const {
  step,
  busy,
  configuration,
  sourceType,
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
  act,
  refreshJob,
  clearError,
  close,
} = useServiceInstallation(
  props,
  () => emit("close"),
  (receipt) => emit("submitted", receipt),
);
</script>

<template>
  <ElDrawer
    :model-value="open"
    :title="`安装 ${recipe?.name || '服务'}`"
    size="min(780px,100vw)"
    destroy-on-close
    :close-on-click-modal="!busy"
    :close-on-press-escape="!busy"
    :show-close="!busy"
    @close="close"
  >
    <div v-if="recipe && host && configuration" class="service-install">
      <header class="install-context">
        <SoftwareIcon :type="recipe.key" />
        <div>
          <strong
            >{{ recipe.name }} <span>{{ recipe.version }}</span></strong
          ><small>{{ host.name }} · {{ host.address || "本机" }}</small>
        </div>
        <span class="context-kind">{{
          runtimeLabels[configuration.runtimeMode]
        }}</span>
      </header>
      <ol class="install-steps" aria-label="安装步骤">
        <li
          v-for="(label, index) in steps"
          :key="label"
          :class="{ current: step === index, complete: step > index }"
          :aria-current="step === index ? 'step' : undefined"
        >
          <span>{{ index + 1 }}</span
          ><small>{{ label }}</small>
        </li>
      </ol>
      <div v-if="fieldError" class="install-error" role="alert">
        {{ fieldError }}
      </div>
      <div v-if="actionError" class="install-error" role="alert">
        <strong>{{ actionError.message }}</strong
        ><small>{{ actionError.code }} · {{ hostTime(actionError.at) }}</small
        ><code v-if="actionError.traceId"
          >traceId={{ actionError.traceId }}</code
        >
        <button
          v-if="
            /EXECUTOR|SUDO|DOCKER|AUTHORIZATION|TARGET_PACKAGE/.test(
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

      <section v-if="step === 0" class="install-panel" aria-label="安装来源">
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
              ><strong>在线获取 · 可选</strong
              ><small>{{ configuration.runtimeMode === 'NATIVE' ? 'Ops 下载后入库复用' : '目标联网拉取固定镜像' }}</small></span
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
              准备官方材料</button
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
              <option value="">当前版本暂无就绪材料，请在线获取或上传补充</option>
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
        <dl class="install-facts">
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
                  ? "在线官方材料"
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
        <details>
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
        class="install-panel"
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
            softwareJobPhaseLabels[job?.phase || ""] ||
            job?.phase ||
            "等待作业回执"
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
        <dl class="install-facts">
          <dt>目标主机</dt>
          <dd>{{ host.name }}</dd>
          <dt>访问入口</dt>
          <dd>
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
          <dt v-if="job?.errorCode">错误码</dt>
          <dd v-if="job?.errorCode">
            <code>{{ job.errorCode }}</code>
          </dd>
          <dt v-if="job?.traceId">追踪标识</dt>
          <dd v-if="job?.traceId">
            <code>{{ job.traceId }}</code>
          </dd>
          <dt>最近更新</dt>
          <dd>{{ hostTime(job?.updatedAt) }}</dd>
        </dl>
        <div class="inline-actions">
          <button class="secondary" :disabled="busy" @click="act(refreshJob)">
            刷新状态</button
          ><button
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
          ><button class="quiet" :disabled="busy" @click="loadLogs">
            读取执行记录
          </button>
        </div>
        <pre v-if="logs" class="install-logs">{{ logs }}</pre>
      </section>
    </div>
    <template #footer
      ><div class="install-footer">
        <button class="secondary" :disabled="busy" @click="close">
          {{ submitted ? "关闭" : "取消" }}</button
        ><span class="footer-spacer"></span
        ><button
          v-if="step > 0 && step < 4"
          class="secondary"
          :disabled="busy || submissionAttempted"
          @click="
            step -= 1;
            clearError();
          "
        >
          上一步</button
        ><button
          v-if="step < 3"
          class="primary"
          :disabled="busy || !recipe || !host"
          @click="next"
        >
          {{
            busy ? "处理中…" : step === 2 ? "检查安装条件" : "下一步"
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
</template>

<style scoped src="./service-installation.css"></style>
