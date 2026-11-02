<script setup lang="ts">
import { computed } from "vue";
import { ElDialog } from "element-plus";
import type {
  InstallationMaterial,
  ServiceRecipe,
} from "./serviceInstallation";
import ErrorHelpLink from "./ErrorHelpLink.vue";
import NodeMaterialImport from "./NodeMaterialImport.vue";
import type { Host } from "./hosts";
import {
  nodeVersionRow,
  nodeActionLabels,
  nodeCommand,
  type NodeDraft,
  type NodeEnvironment,
  type NodePlan,
} from "./nodeEnvironment";
const props = defineProps<{
  draft: NodeDraft;
  environment?: NodeEnvironment;
  choices: ServiceRecipe[];
  initialChoices: ServiceRecipe[];
  initialRecipe?: ServiceRecipe;
  initialMaterials: InstallationMaterial[];
  materials: InstallationMaterial[];
  recipe?: ServiceRecipe;
  plan?: NodePlan;
  busy: boolean;
  attempted: boolean;
  error?: string;
  errorCode?: string;
  diagnostic?: string;
  host: Host;
}>();
const emit = defineEmits<{
  close: [];
  change: [];
  preflight: [];
  submit: [];
  prepare: [];
  material: [InstallationMaterial];
  initialMaterial: [InstallationMaterial];
  materialBusy: [boolean];
}>();
const isDownload = computed(() =>
  [
    "INITIALIZE",
    "INSTALL_NODE",
    "UPDATE_NPM",
    "INSTALL_PNPM",
    "UPDATE_PNPM",
    "UPDATE_NVM",
  ].includes(props.draft.action),
);
const destructive = computed(() =>
  ["UNINSTALL_NODE", "REMOVE_ENVIRONMENT", "RELEASE"].includes(
    props.draft.action,
  ),
);
const needsPlan = computed(
  () => !["UNINSTALL_NODE", "RELEASE"].includes(props.draft.action),
);
const versionRow = computed(() =>
  nodeVersionRow(props.environment, props.draft.nodeVersion),
);
const confirmation = computed(() =>
  props.draft.action === "UNINSTALL_NODE"
    ? versionRow.value?.name
    : props.environment?.name,
);
const removalPaths = computed(() =>
  props.draft.action === "UNINSTALL_NODE"
    ? (versionRow.value?.nodeDataPaths || []).filter((p) => p !== "/")
    : props.plan?.removedPaths || [],
);
const removesDefault = computed(
  () =>
    props.draft.action === "UNINSTALL_NODE" &&
    props.draft.nodeVersion === props.environment?.observedDefaultVersion,
);
const expired = computed(
  () => props.plan && Date.parse(props.plan.expiresAt) <= Date.now(),
);
const canSubmit = computed(
  () =>
    !props.busy &&
    (!needsPlan.value || (!!props.plan && !expired.value)) &&
    (!destructive.value ||
      props.draft.confirmationName === confirmation.value) &&
    (!props.draft.deleteData || removalPaths.value.length > 0) &&
    (!removesDefault.value || props.draft.clearNodeDefault),
);
</script>

<template>
  <ElDialog
    :model-value="true"
    :title="nodeActionLabels[draft.action]"
    width="min(720px,94vw)"
    align-center
    :close-on-click-modal="false"
    :close-on-press-escape="!busy && !attempted"
    :show-close="!busy && !attempted"
    @close="emit('close')"
  >
    <form
      class="node-form"
      @submit.prevent="plan || !needsPlan ? emit('submit') : emit('preflight')"
    >
      <p v-if="environment" class="context">
        {{ environment.name }} · {{ environment.account }}<br /><code>{{
          environment.rootPath
        }}</code>
      </p>
      <fieldset
        :disabled="busy || attempted"
        @input="emit('change')"
        @change="emit('change')"
      >
        <template v-if="draft.action === 'INITIALIZE'">
          <label
            >环境名称<input
              v-model="draft.name"
              aria-label="Node 环境名称"
              maxlength="120"
              required
          /></label>
          <label
            >执行账号<input
              v-model="draft.account"
              aria-label="Node 执行账号"
              placeholder="如 ubuntu"
              required
          /></label>
          <p class="hint">使用服务器已有的非 root 账号。</p>
        </template>
        <section v-if="draft.action === 'INITIALIZE'" class="plan">
          <strong>同时安装 Node.js</strong>
          <label
            >Node 版本<select
              v-model="draft.initialNodeVersion"
              aria-label="首次 Node 版本"
            >
              <option
                v-for="item in initialChoices"
                :key="item.version"
                :value="item.version"
              >
                {{ item.version }}
              </option>
            </select></label
          >
          <p>自动安装 nvm 和所选 Node 版本。</p>
          <template v-if="draft.source === 'ARCHIVE'">
            <label
              >Node 离线材料<select
                v-model="draft.initialNodeMaterialId"
                aria-label="首次 Node 离线材料"
              >
                <option value="">请选择已核验材料</option>
                <option
                  v-for="item in initialMaterials"
                  :key="item.id"
                  :value="item.id"
                >
                  {{ item.name }}
                </option>
              </select></label
            >
            <NodeMaterialImport
              v-if="initialRecipe"
              :key="initialRecipe.version"
              :recipe="initialRecipe"
              :disabled="attempted"
              @ready="emit('initialMaterial', $event)"
              @busy="emit('materialBusy', $event)"
            />
          </template>
          <details
            v-if="initialRecipe?.native?.parts.length"
            class="source-details"
          >
            <summary>Node 文件与来源</summary>
            <div v-for="part in initialRecipe.native.parts" :key="part.name">
              <strong>{{ part.name }}</strong
              ><a :href="part.url" target="_blank" rel="noopener noreferrer">{{
                part.url
              }}</a
              ><code>SHA-256 {{ part.sha256 }}</code>
              <p>{{ part.byteSize }} 字节</p>
            </div>
          </details>
        </section>
        <label v-if="draft.action === 'INSTALL_NODE'"
          >Node 版本<select
            v-model="draft.nodeVersion"
            aria-label="Node 安装版本"
          >
            <option
              v-for="item in choices"
              :key="item.version"
              :value="item.version"
            >
              {{ item.name }} · {{ item.version }}
            </option>
          </select></label
        >
        <label
          v-else-if="
            ['UPDATE_NPM', 'INSTALL_PNPM', 'UPDATE_PNPM'].includes(draft.action)
          "
          >工具版本<select
            v-model="draft.packageVersion"
            aria-label="Node 工具版本"
          >
            <option
              v-for="item in choices"
              :key="item.version"
              :value="item.version"
            >
              {{ item.name }} · {{ item.version }}
            </option>
          </select></label
        >
        <p v-if="draft.nodeVersion" class="version">
          Node {{ draft.nodeVersion }}
        </p>
        <template v-if="isDownload">
          <div class="source-choice" aria-label="Node 安装方式">
            <label
              ><input
                v-model="draft.source"
                type="radio"
                value="OFFICIAL"
              />官方在线安装</label
            >
            <label
              ><input v-model="draft.source" type="radio" value="ARCHIVE" />Ops
              离线安装</label
            >
          </div>
          <p v-if="draft.source === 'OFFICIAL'" class="hint">
            由目标服务器获取已核验的固定版本官方文件。
          </p>
          <template v-else>
            <label
              >平台材料<select
                v-model="draft.materialId"
                aria-label="Node 离线材料"
              >
                <option value="">请选择已核验材料</option>
                <option
                  v-for="item in materials"
                  :key="item.id"
                  :value="item.id"
                >
                  {{ item.name }} · {{ item.recipeVersion }}
                </option>
              </select></label
            >
            <p v-if="!materials.length" class="hint">
              暂无匹配材料，可在下方补充。
            </p>
            <NodeMaterialImport
              v-if="recipe"
              :key="recipe.key + recipe.version"
              :recipe="recipe"
              :disabled="attempted"
              @ready="emit('material', $event)"
              @busy="emit('materialBusy', $event)"
            />
          </template>
          <details v-if="recipe?.native?.parts.length" class="source-details">
            <summary>文件与官方来源</summary>
            <div v-for="part in recipe.native.parts" :key="part.name">
              <strong>{{ part.name }}</strong
              ><a :href="part.url" target="_blank" rel="noopener noreferrer">{{
                part.url
              }}</a>
              <code>SHA-256 {{ part.sha256 }}</code>
              <p>{{ part.byteSize }} 字节</p>
            </div>
          </details>
        </template>
        <label
          v-if="['INITIALIZE', 'INSTALL_NODE'].includes(draft.action)"
          class="check"
          ><input
            v-model="draft.makeDefault"
            type="checkbox"
          />安装后设为此环境默认版本</label
        >
        <template v-if="draft.action === 'UNINSTALL_NODE'">
          <p class="remove-note">
            将卸载此 Node 版本及其 npm、pnpm 和全局工具。
          </p>
          <code>{{ versionRow?.installationPaths?.root }}</code>
          <label
            v-if="
              removesDefault &&
              (environment?.observation?.versions.length || 0) === 1
            "
            class="check"
            ><input
              v-model="draft.clearNodeDefault"
              type="checkbox"
            />清除环境默认版本（这是最后一个版本）</label
          >
          <p v-else-if="removesDefault" class="remove-note">
            请先把其他版本设为默认，再卸载当前默认版本。
          </p>
          <p class="hint">
            项目目录保留。平台外脚本是否引用该版本需要自行核对。
          </p>
        </template>
        <p v-if="draft.action === 'REMOVE_ENVIRONMENT'" class="remove-note">
          只允许移除已没有 Node 版本的受管环境。
        </p>
        <p v-if="draft.action === 'RELEASE'" class="hint">
          解除该环境及其版本的管理关系，服务器上的程序和数据保留。
        </p>
        <label
          v-if="['UNINSTALL_NODE', 'REMOVE_ENVIRONMENT'].includes(draft.action)"
          class="check"
          ><input
            v-model="draft.deleteData"
            type="checkbox"
          />同时清理本次操作对应的附属数据</label
        >
        <div v-if="draft.deleteData" class="hint">
          <p>额外清理以下目录：</p>
          <code v-for="path in removalPaths" :key="path">{{ path }}<br /></code>
          <p v-if="!removalPaths.length">请先预检以确认精确范围。</p>
        </div>
        <label v-if="destructive"
          >{{
            draft.action === "UNINSTALL_NODE"
              ? "输入软件名称确认"
              : "输入环境名称确认"
          }}<input
            v-model="draft.confirmationName"
            aria-label="确认 Node 环境名称"
            :placeholder="confirmation"
            autocomplete="off"
        /></label>
      </fieldset>
      <section v-if="plan" class="plan" aria-label="Node 变更预览">
        <strong>预检完成</strong>
        <p v-if="plan.change?.context">
          账号 {{ plan.change.context.account }} · nvm
          {{ plan.change.context.nvmVersion }}
        </p>
        <code v-if="plan.change?.context">{{
          plan.change.context.rootPath
        }}</code>
        <p v-for="check in plan.checks" :key="check.key">{{ check.message }}</p>
        <code v-if="plan.paths?.root">{{ plan.paths.root }}</code>
        <details v-if="plan.commands?.length" open>
          <summary>将在目标服务器执行</summary>
          <pre v-for="(command, index) in plan.commands" :key="index">{{
            nodeCommand(command)
          }}</pre>
        </details>
        <p v-if="expired" role="alert">计划已过期，请重新检查。</p>
      </section>
      <p v-if="error" role="alert" class="error">
        {{ error }}
        <button
          v-if="
            ['NODE_CAPABILITY_REQUIRED', 'EXECUTOR_UPGRADE_REQUIRED'].includes(
              errorCode || '',
            )
          "
          type="button"
          class="quiet"
          @click="emit('prepare')"
        >
          更新接入组件
        </button>
      </p>
      <details v-if="error && diagnostic">
        <summary>诊断信息</summary>
        <ErrorHelpLink :code="errorCode || ''" />
        <code>{{ diagnostic }}</code>
      </details>
      <p v-if="attempted" class="hint">提交结果未确认，可核对原请求。</p>
      <footer>
        <button
          type="button"
          class="secondary"
          :disabled="busy || attempted"
          @click="emit('close')"
        >
          取消
        </button>
        <button
          v-if="needsPlan && (!plan || expired)"
          class="primary"
          type="button"
          :disabled="busy || attempted"
          @click="emit('preflight')"
        >
          {{ busy ? "检查中…" : "检查安装条件" }}
        </button>
        <button v-else class="primary" type="submit" :disabled="!canSubmit">
          {{ busy ? "提交中…" : attempted ? "核对提交结果" : "确认执行" }}
        </button>
      </footer>
    </form>
  </ElDialog>
</template>

<style scoped>
.node-form,
fieldset {
  display: grid;
  gap: 16px;
  min-width: 0;
}
fieldset {
  border: 0;
  padding: 0;
  margin: 0;
}
label {
  display: grid;
  gap: 7px;
  font-size: 13px;
}
input:not([type="radio"]):not([type="checkbox"]),
select {
  width: 100%;
  min-width: 0;
  padding: 9px 11px;
  border: 1px solid var(--border);
  border-radius: 5px;
  background: var(--panel, #fff);
  color: var(--text);
  box-sizing: border-box;
}
.source-choice {
  display: flex;
  gap: 18px;
  flex-wrap: wrap;
}
.source-choice label,
.check {
  display: flex;
  align-items: center;
  gap: 8px;
}
.node-form input[type="radio"],
.node-form input[type="checkbox"] {
  width: 16px;
  height: 16px;
  min-width: 16px;
  min-height: 16px;
  max-width: 16px;
  max-height: 16px;
  margin: 0;
  padding: 0;
  flex: 0 0 16px;
}
.source-choice label {
  white-space: nowrap;
}
.hint,
.context,
.plan p {
  margin: 0;
  color: var(--muted);
  font-size: 12px;
  line-height: 1.8;
}
.version {
  margin: 0;
  font-weight: 600;
}
code,
pre {
  font: 11px/1.8 var(--mono);
  overflow-wrap: anywhere;
  white-space: pre-wrap;
}
.source-details div {
  display: grid;
  gap: 6px;
  margin-top: 12px;
}
.source-details a {
  font-size: 12px;
  color: var(--accent);
  overflow-wrap: anywhere;
}
summary {
  cursor: pointer;
  font-size: 12px;
}
.plan {
  padding: 16px;
  border: 1px solid var(--border);
  border-radius: 6px;
  display: grid;
  gap: 10px;
}
.plan pre {
  padding: 10px;
  background: #f5f7fa;
  margin: 8px 0 0;
}
.remove-note,
.error {
  color: var(--danger, #b45335);
  font-size: 13px;
  margin: 0;
  line-height: 1.8;
}
footer {
  display: flex;
  justify-content: flex-end;
  gap: 10px;
  border-top: 1px solid var(--border);
  padding-top: 16px;
}
</style>
