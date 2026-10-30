<script setup lang="ts">
import { computed, onUnmounted, ref, watch } from "vue";
import { ApiError, request } from "./api";
import { hostTime, type Host } from "./hosts";
import ServiceNetworkFields from "./ServiceNetworkFields.vue";
import {
  isTool,
  isDockerEngine,
  runtimeLabels,
  serviceEndpoint,
  servicePortAddress,
  servicePortValue,
  type ServiceConfiguration,
  type ServiceConfigurationPlan,
  type ServiceInstallReceipt,
} from "./serviceInstallation";
import type { SoftwareInstance } from "./software";

const props = defineProps<{
  detail: SoftwareInstance;
  host?: Host;
  disabled: boolean;
}>();
const emit = defineEmits<{
  submitted: [ServiceInstallReceipt];
  preparation: [string];
}>();
const editing = ref(false);
const busy = ref(false);
const draft = ref<ServiceConfiguration>();
const plan = ref<ServiceConfigurationPlan>();
const submitKey = ref("");
const attempted = ref(false);
const receipt = ref<ServiceInstallReceipt>();
const error = ref<{
  message: string;
  code: string;
  traceId: string;
  at: string;
}>();
const recipe = computed(() => props.detail.installation?.definition);
const currentConfig = computed(() => props.detail.installation?.config);
const networkChanged = computed(
  () =>
    !!plan.value &&
    (plan.value.previousConfig.hostPort !== plan.value.config.hostPort ||
      plan.value.previousConfig.bindAddress !== plan.value.config.bindAddress ||
      JSON.stringify(plan.value.previousConfig.ports) !==
        JSON.stringify(plan.value.config.ports)),
);
const instanceRoot = computed(() =>
  currentConfig.value?.storageRoot
    ? `${currentConfig.value.storageRoot}/${props.detail.id}`
    : `${props.host?.installRoot || ""}/software/${props.detail.id}`,
);
const now = ref(Date.now());
const clock = setInterval(() => {
  now.value = Date.now();
}, 1000);
onUnmounted(() => clearInterval(clock));
const expired = computed(
  () => !plan.value || Date.parse(plan.value.expiresAt) <= now.value,
);
const canEdit = computed(
  () =>
    !!recipe.value &&
    !isTool(recipe.value) &&
    !isDockerEngine(recipe.value) &&
    props.detail.managementMode === "MANAGED" &&
    props.detail.installationStatus === "READY" &&
    !props.disabled,
);

function reset() {
  editing.value = false;
  plan.value = undefined;
  submitKey.value = "";
  attempted.value = false;
  receipt.value = undefined;
  error.value = undefined;
  if (currentConfig.value)
    draft.value = {
      ...JSON.parse(JSON.stringify(currentConfig.value)),
      runtimeMode: currentConfig.value.runtimeMode || "DOCKER",
      storageRoot: currentConfig.value.storageRoot || "",
      ports: { ...currentConfig.value.ports },
    };
}
watch(() => props.detail.id, reset, { immediate: true });
watch(
  () => props.detail.version,
  () => {
    if (!editing.value && !busy.value) reset();
  },
);
watch(
  draft,
  () => {
    if (!attempted.value) {
      plan.value = undefined;
      submitKey.value = "";
    }
  },
  { deep: true },
);

async function act(action: () => Promise<void>) {
  if (busy.value) return;
  busy.value = true;
  error.value = undefined;
  try {
    await action();
  } catch (cause) {
    error.value = {
      message: cause instanceof Error ? cause.message : "配置请求未确认",
      code: cause instanceof ApiError ? cause.code : "REQUEST_NOT_CONFIRMED",
      traceId: cause instanceof ApiError ? cause.traceId : "",
      at:
        cause instanceof ApiError ? cause.timestamp : new Date().toISOString(),
    };
  } finally {
    busy.value = false;
  }
}
async function check() {
  if (!draft.value || !canEdit.value) return;
  await act(async () => {
    plan.value = await request<ServiceConfigurationPlan>(
      `/software-instances/${props.detail.id}/configuration-plans`,
      "POST",
      { expectedVersion: props.detail.version, config: draft.value },
    );
    submitKey.value = crypto.randomUUID();
  });
}
async function submit() {
  if (!plan.value || (!attempted.value && expired.value)) return;
  await act(async () => {
    attempted.value = true;
    receipt.value = await request<ServiceInstallReceipt>(
      `/software-configuration-plans/${plan.value!.id}/submit`,
      "POST",
      { idempotencyKey: submitKey.value },
    );
    emit("submitted", receipt.value);
  });
}
</script>

<template>
  <div class="configuration-panel">
    <p v-if="!recipe || !currentConfig || !draft" class="install-note">
      此对象通过外部登记接入。当前可观察与维护生命周期，配置仍由原安装方式管理。
    </p>
    <template v-else>
      <dl v-if="!editing" class="configuration-facts">
        <dt>运行方式</dt>
        <dd>{{ runtimeLabels[currentConfig.runtimeMode || "DOCKER"] }}</dd>
        <template v-if="!isTool(recipe)"
          ><dt>登记入口</dt>
          <dd>
            <code>{{ serviceEndpoint(recipe, currentConfig) }}</code>
          </dd>
          <template
            v-for="port in recipe.ports?.filter(
              (item) =>
                item.key !== 'main' &&
                (!item.nativeOnly || currentConfig?.runtimeMode === 'NATIVE'),
            ) || []"
            :key="port.key"
            ><dt>{{ port.label }}</dt>
            <dd>
              <template v-if="servicePortValue(recipe, currentConfig, port.key)"
                >{{ servicePortAddress(recipe, currentConfig, port.key) }}:{{
                  servicePortValue(recipe, currentConfig, port.key)
                }}
                / {{ port.protocol }}</template
              ><span v-else>未启用</span>
            </dd></template
          ></template
        >
        <dt>实例目录</dt>
        <dd>
          <code>{{ instanceRoot }}</code>
        </dd>
        <dt>程序位置</dt>
        <dd>
          <code>{{
            currentConfig.runtimeMode === "NATIVE"
              ? detail.installationEvidence?.binaryPath || "尚未返回程序路径"
              : recipe.image
          }}</code>
        </dd>
        <dt>数据目录</dt>
        <dd>
          <code>{{ instanceRoot }}/data</code>
        </dd>
        <dt>配置目录</dt>
        <dd>
          <code>{{ instanceRoot }}/config</code>
          <small v-if="isDockerEngine(recipe)">配置副本；主机实际配置 /etc/docker/daemon.json</small>
        </dd>
        <dt>日志位置</dt>
        <dd>
          <code v-if="isDockerEngine(recipe)">docker.service 的系统日志（journalctl）</code>
          <code v-else-if="currentConfig.runtimeMode === 'NATIVE'"
            >{{ instanceRoot }}/logs</code
          ><span v-else>Docker 有界日志，可在运行日志中读取</span>
        </dd>
      </dl>
      <p
        v-if="
          !editing &&
          recipe.key === 'seaweedfs' &&
          currentConfig.runtimeMode === 'NATIVE'
        "
        class="install-note"
      >
        原生 S3 同时监听内部 gRPC（不使用 S3
        密钥鉴权），请由管理员限制该端口的访问来源。
      </p>
      <button
        v-if="!editing && !isTool(recipe) && !isDockerEngine(recipe)"
        class="secondary"
        :disabled="!canEdit"
        @click="
          reset();
          editing = true;
        "
      >
        调整配置
      </button>
      <template v-if="editing">
        <ServiceNetworkFields
          :recipe="recipe"
          :config="draft"
          :disabled="busy || attempted || disabled"
          :project-root="host?.installRoot"
        />
        <div v-if="error" class="install-error" role="alert">
          <strong>{{ error.message }}</strong
          ><small>{{ error.code }} · {{ hostTime(error.at) }}</small
          ><code v-if="error.traceId">traceId={{ error.traceId }}</code
          ><button
            v-if="/EXECUTOR|SUDO|DOCKER|AUTHORIZATION/.test(error.code)"
            class="quiet"
            @click="emit('preparation', detail.hostId)"
          >
            查看安装准备
          </button>
        </div>
        <div v-if="plan" class="configuration-plan">
          <p v-if="networkChanged" class="install-note">
            端口或地址变更后，旧防火墙规则保留。请到“端口管理”重新检测并按需调整访问规则。
          </p>
          <strong>配置预检完成</strong
          ><small>有效至 {{ hostTime(plan.expiresAt) }}</small>
          <ul class="preflight-checks">
            <li v-for="check in plan.checks" :key="check.key">
              <b>{{ check.status === "WARNING" ? "!" : "✓" }}</b
              ><span>{{ check.message || check.key }}</span>
            </li>
          </ul>
          <code
            v-for="mapping in plan.portMappings"
            :key="`${mapping.address}:${mapping.hostPort}/${mapping.protocol}`"
            >{{ mapping.address }}:{{ mapping.hostPort }} →
            {{ mapping.containerPort }}/{{ mapping.protocol }}</code
          >
        </div>
        <p v-if="attempted && !receipt" class="install-note">
          提交结果未确认，重试使用原标识；也可在执行记录查看原作业。
        </p>
        <p v-if="receipt" class="install-note">
          配置作业已提交：{{ receipt.jobId }}。执行记录会持续更新。
        </p>
        <div class="inline-actions">
          <button
            class="quiet"
            :disabled="busy || (attempted && !receipt)"
            @click="reset"
          >
            {{ receipt ? "收起" : "取消" }}</button
          ><span class="spacer"></span
          ><button
            v-if="!attempted"
            class="secondary"
            :disabled="busy || disabled"
            @click="check"
          >
            检查配置</button
          ><button
            v-if="plan && !receipt"
            class="primary"
            :disabled="busy || (!attempted && (disabled || expired))"
            @click="submit"
          >
            {{ attempted ? "核对原提交" : "确认变更" }}
          </button>
        </div>
      </template>
    </template>
  </div>
</template>

<style scoped src="./service-installation.css"></style>
<style scoped>
.configuration-panel {
  display: grid;
  gap: 20px;
  min-width: 0;
  padding-top: 12px;
}
.configuration-facts {
  display: grid;
  grid-template-columns: 100px minmax(0, 1fr);
  gap: 16px;
  margin: 0;
  font-size: 12px;
}
.configuration-facts dt {
  color: var(--muted);
}
.configuration-facts dd {
  margin: 0;
  overflow-wrap: anywhere;
}
.configuration-facts code {
  display: block;
  font: 11px/1.8 var(--mono);
}
.configuration-panel > .secondary {
  justify-self: start;
}
.configuration-plan {
  display: grid;
  gap: 12px;
  border: 1px solid var(--border);
  border-radius: 6px;
  padding: 16px;
}
.configuration-plan small {
  color: var(--muted);
}
.configuration-plan > code {
  font: 11px/1.7 var(--mono);
  overflow-wrap: anywhere;
}
</style>
