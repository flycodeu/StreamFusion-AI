<script setup lang="ts">
import { ElDialog } from "element-plus";
import ErrorHelpLink from "./ErrorHelpLink.vue";
import HostPreparationPanel from "./HostPreparationPanel.vue";
import NodeVersionDialog from "./NodeVersionDialog.vue";
import { hostTime, type Host } from "./hosts";
import type { AutomaticPreparationUpdate } from "./preparationUpdate";
import type { InstallationMaterial, ServiceRecipe } from "./serviceInstallation";
import {
  nodeTerminalCommand,
  type NodeDiscovery,
  type NodeDraft,
  type NodeEnvironment,
  type NodeObservation,
  type NodePlan,
} from "./nodeEnvironment";

defineProps<{
  draft?: NodeDraft;
  actionOpen: boolean;
  environment?: NodeEnvironment;
  choices: ServiceRecipe[];
  initialChoices: ServiceRecipe[];
  initialRecipe?: ServiceRecipe;
  initialMaterials: InstallationMaterial[];
  matchingMaterials: InstallationMaterial[];
  recipe?: ServiceRecipe;
  plan?: NodePlan;
  busy: boolean;
  attempted: boolean;
  error?: {
    message: string;
    code: string;
    traceId: string;
    at: string;
  };
  currentHost: Host;
  preparationOpen: boolean;
  automaticUpdate?: AutomaticPreparationUpdate;
  discoveryOpen: boolean;
  discoveryAccount: string;
  discoveryPath: string;
  discovery?: NodeDiscovery;
  terminalOpen: boolean;
}>();

const emit = defineEmits<{
  closeAction: [];
  "update:preparationOpen": [value: boolean];
  "update:discoveryOpen": [value: boolean];
  "update:discoveryAccount": [value: string];
  "update:discoveryPath": [value: string];
  "update:terminalOpen": [value: boolean];
  changedDraft: [];
  preflight: [];
  submit: [];
  prepare: [];
  materialReady: [item: InstallationMaterial];
  initialMaterialReady: [item: InstallationMaterial];
  materialBusy: [value: boolean];
  updatedPreparation: [];
  discover: [];
  register: [candidate: NodeObservation];
}>();
</script>

<template>
  <NodeVersionDialog
    v-if="draft && actionOpen"
    :draft="draft"
    :environment="environment"
    :choices="choices"
    :initial-choices="initialChoices"
    :initial-recipe="initialRecipe"
    :initial-materials="initialMaterials"
    :materials="matchingMaterials"
    :recipe="recipe"
    :plan="plan"
    :busy="busy"
    :attempted="attempted"
    :error="error?.message"
    :error-code="error?.code"
    :diagnostic="
      error
        ? [error.code, hostTime(error.at), error.traceId, currentHost.id]
            .filter(Boolean)
            .join(' · ')
        : ''
    "
    :host="currentHost"
    :preparing="preparationOpen"
    @close="emit('closeAction')"
    @back="emit('update:preparationOpen', false)"
    @change="emit('changedDraft')"
    @preflight="emit('preflight')"
    @submit="emit('submit')"
    @prepare="emit('prepare')"
    @material="emit('materialReady', $event)"
    @initial-material="emit('initialMaterialReady', $event)"
    @material-busy="emit('materialBusy', $event)"
  >
    <template #preparation>
      <HostPreparationPanel
        v-if="preparationOpen"
        :host="currentHost"
        :automatic-update="automaticUpdate"
        update-only
        @updated="emit('updatedPreparation')"
      />
    </template>
  </NodeVersionDialog>
  <ElDialog
    :model-value="discoveryOpen"
    title="检测已有 Node 安装"
    width="min(680px,94vw)"
    :close-on-click-modal="false"
    align-center
    @update:model-value="emit('update:discoveryOpen', $event)"
  >
    <div class="discovery-form">
      <label
        >执行账号<input
          :value="discoveryAccount"
          aria-label="检测 Node 账号"
          :disabled="busy"
          @input="emit('update:discoveryAccount', ($event.target as HTMLInputElement).value)"
      /></label>
      <label
        >指定目录（可选）<input
          :value="discoveryPath"
          aria-label="检测 Node 目录"
          placeholder="留空检测标准位置"
          :disabled="busy"
          @input="emit('update:discoveryPath', ($event.target as HTMLInputElement).value)"
      /></label>
      <button
        class="primary"
        :disabled="busy || !discoveryAccount.trim()"
        @click="emit('discover')"
      >
        {{ busy ? "检测中…" : "开始检测" }}
      </button>
      <p v-if="error" role="alert" class="error">
        {{ error.message }} <ErrorHelpLink :code="error.code" />
      </p>
      <article
        v-for="candidate in discovery?.candidates"
        :key="candidate.rootPath"
        class="candidate"
      >
        <strong>{{ candidate.managerType }} · {{ candidate.account }}</strong
        ><code>{{ candidate.rootPath }}</code>
        <p>
          {{
            candidate.versions.map((item) => item.version).join(" / ") ||
            "未发现 Node 版本"
          }}
        </p>
        <button
          class="secondary"
          :disabled="busy"
          @click="emit('register', candidate)"
        >
          登记此环境
        </button>
      </article>
      <p v-if="discovery && !discovery.candidates.length" class="empty">
        未发现可登记的 Node 环境
      </p>
    </div>
  </ElDialog>
  <ElDialog
    :model-value="terminalOpen"
    class="node-terminal-dialog"
    title="终端使用方式"
    width="min(660px,94vw)"
    align-center
    @update:model-value="emit('update:terminalOpen', $event)"
  >
    <p class="node-dialog-hint">
      使用 {{ environment?.account }} 账号执行；只作用于当前终端。
    </p>
    <details>
      <summary>当前终端临时加载</summary>
      <pre v-if="environment">{{ nodeTerminalCommand(environment) }}</pre>
    </details>
  </ElDialog>
  <ElDialog
    v-if="!draft"
    :model-value="preparationOpen"
    append-to-body
    :title="automaticUpdate ? '正在准备安装环境' : '更新接入组件'"
    width="min(720px,94vw)"
    align-center
    @update:model-value="emit('update:preparationOpen', $event)"
  >
    <HostPreparationPanel
      v-if="preparationOpen"
      :host="currentHost"
      :automatic-update="automaticUpdate"
      update-only
      @updated="emit('updatedPreparation')"
    />
  </ElDialog>
</template>
