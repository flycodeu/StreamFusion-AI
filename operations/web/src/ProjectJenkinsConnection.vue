<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref, watch } from "vue";
import {
  ElAlert,
  ElButton,
  ElDialog,
  ElEmpty,
  ElForm,
  ElFormItem,
  ElInput,
  ElMessage,
  ElOption,
  ElPopover,
  ElSelect,
  ElTag,
  ElTooltip,
} from "element-plus";
import { ApiError, request } from "./api";
import type { JenkinsMonitor } from "./projectJenkins";
import { jenkinsSecurityTokenURL } from "./webAccess";

interface JenkinsPluginCheck {
  shortName: string;
  installed: boolean;
  active: boolean;
  version?: string;
}
interface JenkinsConnectionTestResult {
  ok: boolean;
  jenkinsVersion: string;
  authenticatedUser?: { id: string; fullName: string };
  pluginsChecked: boolean;
  recommendedPlugins?: JenkinsPluginCheck[];
  checkedAt?: string;
  buildAgentLabel?: string;
  buildAgents?: { checked: boolean; matching: number; online: number };
}
interface DiagnosticMeta {
  code?: string;
  traceId?: string;
  timestamp?: string;
}

const props = defineProps<{ projectId: string; archived: boolean }>();
const emit = defineEmits<{ error: [unknown]; install: [] }>();
const lifetime = new AbortController();
const connection = ref<JenkinsMonitor>();
const open = ref(false), saving = ref(false), reading = ref(false), testing = ref(false);
const readError = ref(""), saveError = ref(""), testError = ref("");
const readErrorMeta = ref<DiagnosticMeta>();
const saveErrorMeta = ref<DiagnosticMeta>();
const testErrorMeta = ref<DiagnosticMeta>();
const testResult = ref<JenkinsConnectionTestResult>();
const softwareId = ref(""), username = ref(""), token = ref("");
const defaultBuildAgentLabel = "sfops-build";
const buildAgentLabel = ref(defaultBuildAgentLabel);
const endpoint = () => `/application-projects/${props.projectId}/jenkins-connection`;
const selected = computed(() => connection.value?.instances.find(item => item.id === connection.value?.softwareId));
const draftInstance = computed(() => connection.value?.instances.find(item => item.id === softwareId.value));
const tokenGuideURL = computed(() => jenkinsSecurityTokenURL(draftInstance.value?.url));
const reuseToken = computed(() => !!connection.value?.hasToken && connection.value.softwareId === softwareId.value && connection.value.username === username.value.trim());
const valid = computed(() => !!softwareId.value && !!username.value.trim() && (reuseToken.value || !!token.value.trim()));

watch([softwareId, username, token, buildAgentLabel], () => {
  testResult.value = undefined;
  testError.value = "";
  testErrorMeta.value = undefined;
});

function extractDiagnostic(error: unknown, fallback: string): { message: string; meta?: DiagnosticMeta } {
  if (error instanceof ApiError) {
    if (error.code === "AUTH_REQUIRED") emit("error", error);
    const meta: DiagnosticMeta | undefined =
      error.code || error.traceId || error.timestamp
        ? { code: error.code, traceId: error.traceId, timestamp: error.timestamp }
        : undefined;
    return { message: error.message || fallback, meta };
  }
  return {
    message: error instanceof Error && /[\u4e00-\u9fff]/.test(error.message) ? error.message : fallback,
  };
}
async function refresh() {
  if (reading.value || saving.value) return;
  reading.value = true;
  readError.value = "";
  readErrorMeta.value = undefined;
  try {
    const data = await request<JenkinsMonitor>(endpoint(), "GET", undefined, lifetime.signal);
    if (!lifetime.signal.aborted) connection.value = data;
  } catch (error) {
    if (!lifetime.signal.aborted) {
      const diag = extractDiagnostic(error, "无法读取项目 Jenkins 设置");
      readError.value = diag.message;
      readErrorMeta.value = diag.meta;
    }
  } finally { reading.value = false; }
}
function edit() {
  const current = connection.value;
  if (!current || props.archived || saving.value) return;
  softwareId.value = current.softwareId || (current.instances.length === 1 ? current.instances[0].id : "");
  username.value = current.username || "";
  buildAgentLabel.value = current.buildAgentLabel || defaultBuildAgentLabel;
  token.value = "";
  saveError.value = "";
  saveErrorMeta.value = undefined;
  testError.value = "";
  testErrorMeta.value = undefined;
  testResult.value = undefined;
  open.value = true;
}
async function testConnection() {
  if (!connection.value || !valid.value || saving.value || testing.value) return;
  testing.value = true;
  testError.value = "";
  testErrorMeta.value = undefined;
  testResult.value = undefined;
  const input = {
    softwareId: softwareId.value,
    username: username.value.trim(),
    token: token.value.trim(),
    buildAgentLabel: buildAgentLabel.value.trim(),
  };
  try {
    const data = await request<JenkinsConnectionTestResult>(
      `${endpoint()}/test`,
      "POST",
      input,
      lifetime.signal,
    );
    if (!lifetime.signal.aborted) {
      testResult.value = data;
    }
  } catch (error) {
    if (!lifetime.signal.aborted) {
      const diag = extractDiagnostic(error, "无法连接到 Jenkins，请检查实例、账号与 API Token");
      testError.value = diag.message;
      testErrorMeta.value = diag.meta;
    }
  } finally {
    input.token = "";
    testing.value = false;
  }
}
async function save() {
  if (!connection.value || props.archived || !valid.value || saving.value) return;
  saving.value = true;
  saveError.value = "";
  saveErrorMeta.value = undefined;
  const input = {
    expectedVersion: connection.value.version, softwareId: softwareId.value,
    username: username.value.trim(), token: token.value.trim(), buildAgentLabel: buildAgentLabel.value.trim(),
  };
  try {
    const data = await request<JenkinsMonitor>(endpoint(), "POST", input, lifetime.signal);
    if (lifetime.signal.aborted) return;
    connection.value = data;
    open.value = false;
    token.value = "";
    ElMessage.success("项目 Jenkins 已保存");
  } catch (error) {
    if (!lifetime.signal.aborted) {
      const diag = extractDiagnostic(error, "无法保存项目 Jenkins 设置");
      saveError.value = diag.message;
      saveErrorMeta.value = diag.meta;
    }
  } finally { input.token = ""; saving.value = false; }
}
onMounted(refresh);
onUnmounted(() => { lifetime.abort(); token.value = ""; });
</script>

<template>
  <section class="jenkins-setting" aria-label="项目 Jenkins">
    <span class="setting-name">Jenkins</span>
    <template v-if="readError">
      <span class="setting-error" role="alert">{{ readError }}</span>
      <ElPopover v-if="readErrorMeta" trigger="click" placement="bottom-start" :width="340">
        <div class="diag-meta-list">
          <div v-if="readErrorMeta.code"><span>错误码</span><code>{{ readErrorMeta.code }}</code></div>
          <div v-if="readErrorMeta.traceId"><span>追踪标识</span><code>{{ readErrorMeta.traceId }}</code></div>
          <div v-if="readErrorMeta.timestamp"><span>发生时间</span><code>{{ readErrorMeta.timestamp }}</code></div>
        </div>
        <template #reference>
          <ElButton link type="info" size="small">排查详情</ElButton>
        </template>
      </ElPopover>
      <ElButton link type="primary" size="small" @click="refresh">重试</ElButton>
    </template>
    <span v-else-if="!connection || reading" role="status" class="muted">正在读取…</span>
    <template v-else>
      <span v-if="connection.configured">{{ selected ? selected.name + " · " + selected.hostName : "已配置" }}</span>
      <span v-else class="setting-name">未设置</span>
      <ElButton v-if="!archived" link type="primary" size="small" @click="edit">
        {{ connection.configured ? "更改 Jenkins" : "设置 Jenkins" }}
      </ElButton>
    </template>
  </section>

  <ElDialog
    v-model="open"
    title="项目 Jenkins 连接"
    width="min(520px, 94vw)"
    append-to-body
    destroy-on-close
    :close-on-click-modal="false"
    :close-on-press-escape="!saving"
    :show-close="!saving"
    @close="token = ''; testResult = undefined; testError = ''; testErrorMeta = undefined; saveError = ''; saveErrorMeta = undefined"
  >
    <template v-if="connection">
      <p class="dialog-lead">选择此项目的 Jenkins，设置访问账号和构建节点标签。</p>
      <ElForm v-if="connection.instances.length" label-position="top" class="jenkins-form" @submit.prevent="save">
        <ElFormItem label="Jenkins 实例" required>
          <ElSelect
            v-model="softwareId"
            placeholder="选择 Jenkins 实例"
            style="width: 100%"
            :disabled="saving || testing"
          >
            <ElOption
              v-for="item in connection.instances"
              :key="item.id"
              :label="`${item.name} · ${item.hostName}`"
              :value="item.id"
            />
          </ElSelect>
        </ElFormItem>
        <ElFormItem label="Jenkins 用户名" required>
          <ElInput
            v-model="username"
            maxlength="128"
            autocomplete="off"
            placeholder="登录 Jenkins 的个人账号"
            :disabled="saving || testing"
          />
        </ElFormItem>
        <ElFormItem :required="!reuseToken">
          <template #label>
            <div class="token-label-row">
              <span>个人 API Token</span>
              <ElTooltip
                content="请使用个人访问令牌（不要使用系统级 Credentials）：登录 Jenkins → 点击右上角用户名 → Security / 安全 → API Token → Add new Token 生成。"
                placement="top"
              >
                <span class="help-badge" tabindex="0">?</span>
              </ElTooltip>
              <a
                v-if="tokenGuideURL"
                class="token-jump-link"
                :href="tokenGuideURL"
                target="_blank"
                rel="noopener noreferrer"
              >
                前往 Jenkins 生成 ↗
              </a>
            </div>
          </template>
          <ElInput
            v-model="token"
            type="password"
            show-password
            maxlength="256"
            autocomplete="new-password"
            :disabled="saving || testing"
            :placeholder="reuseToken ? '已保存，留空继续使用' : '粘贴从「个人设置 → 安全 → API Token」生成的令牌'"
          />
        </ElFormItem>

        <ElFormItem label="构建节点标签">
          <ElInput
            v-model="buildAgentLabel"
            maxlength="64"
            :placeholder="defaultBuildAgentLabel"
            :disabled="saving || testing"
          />
          <span class="muted">构建需要至少一个带此标签的在线 Linux Jenkins Agent。留空使用 {{ defaultBuildAgentLabel }}。</span>
        </ElFormItem>

        <ElAlert
          v-if="testResult"
          type="success"
          :closable="false"
          show-icon
          :title="`连接验证成功（Jenkins ${testResult.jenkinsVersion}）`"
          class="jenkins-alert"
        >
          <div class="test-user-line">
            登录账号：{{ testResult.authenticatedUser?.fullName || testResult.authenticatedUser?.id || username }}
          </div>
          <div v-if="testResult.buildAgentLabel" class="muted">构建标签：{{ testResult.buildAgentLabel }}</div>
          <div v-if="testResult.buildAgents?.checked" class="test-user-line" :class="{ 'setting-error': !testResult.buildAgents.online }">
            <template v-if="testResult.buildAgents.online">
              匹配标签且在线的构建节点：{{ testResult.buildAgents.online }} / {{ testResult.buildAgents.matching }}
            </template>
            <template v-else-if="testResult.buildAgents.matching">
              匹配节点均已离线或未配置执行器。请在 Jenkins 的节点管理中检查。
            </template>
            <template v-else>
              没有匹配的节点。请在 Jenkins 为构建节点添加标签 {{ testResult.buildAgentLabel }}。
            </template>
          </div>
          <div v-else-if="testResult.buildAgents" class="muted">无法读取构建节点，请在 Jenkins 的节点管理中确认标签和在线状态。</div>
          <div v-if="testResult.pluginsChecked && testResult.recommendedPlugins?.length" class="plugin-pill-row">
            <span class="plugin-caption">推荐流水线插件：</span>
            <ElTag
              v-for="plugin in testResult.recommendedPlugins"
              :key="plugin.shortName"
              size="small"
              :type="plugin.installed && plugin.active ? 'success' : 'warning'"
            >
              {{ plugin.shortName }} · {{ plugin.installed && plugin.active ? "已启用" : plugin.installed ? "未启用" : "未安装" }}
            </ElTag>
          </div>
          <div v-else class="muted plugin-fallback">当前账号无插件清单读取权限，已跳过插件检测；认证已通过。</div>
        </ElAlert>

        <ElAlert
          v-if="testError"
          type="error"
          :closable="false"
          show-icon
          :title="testError"
          class="jenkins-alert"
        >
          <ElPopover v-if="testErrorMeta" trigger="click" placement="top" :width="340">
            <div class="diag-meta-list">
              <div v-if="testErrorMeta.code"><span>错误码</span><code>{{ testErrorMeta.code }}</code></div>
              <div v-if="testErrorMeta.traceId"><span>追踪标识</span><code>{{ testErrorMeta.traceId }}</code></div>
              <div v-if="testErrorMeta.timestamp"><span>发生时间</span><code>{{ testErrorMeta.timestamp }}</code></div>
            </div>
            <template #reference>
              <ElButton link type="primary" size="small">排查详情</ElButton>
            </template>
          </ElPopover>
        </ElAlert>

        <ElAlert
          v-if="saveError"
          type="error"
          :closable="false"
          show-icon
          :title="saveError"
          class="jenkins-alert"
        >
          <ElPopover v-if="saveErrorMeta" trigger="click" placement="top" :width="340">
            <div class="diag-meta-list">
              <div v-if="saveErrorMeta.code"><span>错误码</span><code>{{ saveErrorMeta.code }}</code></div>
              <div v-if="saveErrorMeta.traceId"><span>追踪标识</span><code>{{ saveErrorMeta.traceId }}</code></div>
              <div v-if="saveErrorMeta.timestamp"><span>发生时间</span><code>{{ saveErrorMeta.timestamp }}</code></div>
            </div>
            <template #reference>
              <ElButton link type="primary" size="small">排查详情</ElButton>
            </template>
          </ElPopover>
        </ElAlert>
      </ElForm>

      <ElEmpty v-else description="暂无可用的 Jenkins 实例" :image-size="64">
        <ElButton type="primary" size="small" @click="open = false; emit('install')">安装 Jenkins</ElButton>
      </ElEmpty>
    </template>

    <template #footer>
      <div class="dialog-footer">
        <ElButton
          v-if="connection?.instances.length"
          :loading="testing"
          :disabled="saving || !valid"
          @click="testConnection"
        >
          {{ testing ? "正在验证…" : "测试连接" }}
        </ElButton>
        <div class="footer-right">
          <ElButton :disabled="saving" @click="open = false">取消</ElButton>
          <ElButton
            v-if="connection?.instances.length"
            type="primary"
            :loading="saving"
            :disabled="testing || !valid"
            @click="save"
          >
            {{ saving ? "保存中…" : "保存" }}
          </ElButton>
        </div>
      </div>
    </template>
  </ElDialog>
</template>

<style scoped>
.jenkins-setting { display: flex; align-items: center; flex-wrap: wrap; gap: 4px 8px; font-size: 12px; }
.setting-name, .muted { color: var(--muted); }
.setting-error { color: #a62c22; overflow-wrap: anywhere; font-size: 12px; }
.dialog-lead { margin: 0 0 14px; font-size: 13px; color: var(--muted); line-height: 1.55; }
.jenkins-form { display: grid; gap: 2px; }
.token-label-row { display: inline-flex; align-items: center; gap: 6px; width: 100%; }
.help-badge {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 16px;
  height: 16px;
  border-radius: 50%;
  background: #e2e8f0;
  color: #475569;
  font-size: 11px;
  font-weight: 600;
  cursor: help;
}
.token-jump-link { margin-left: auto; font-size: 12px; color: var(--el-color-primary); text-decoration: none; font-weight: 500; }
.token-jump-link:hover { text-decoration: underline; }
.jenkins-alert { margin-top: 8px; }
.test-user-line { font-size: 12px; margin-bottom: 6px; }
.plugin-pill-row { display: flex; flex-wrap: wrap; align-items: center; gap: 6px; }
.plugin-caption { font-size: 12px; }
.plugin-fallback { font-size: 12px; }
.diag-meta-list { display: grid; gap: 6px; font-size: 12px; }
.diag-meta-list > div { display: grid; grid-template-columns: 64px 1fr; gap: 8px; align-items: baseline; }
.diag-meta-list span { color: var(--muted); }
.diag-meta-list code { overflow-wrap: anywhere; word-break: break-all; }
.dialog-footer { display: flex; align-items: center; justify-content: space-between; gap: 8px; width: 100%; }
.footer-right { display: flex; align-items: center; gap: 8px; margin-left: auto; }
</style>
