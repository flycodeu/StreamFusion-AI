<script setup lang="ts">
import { computed, onUnmounted, ref, watch } from "vue";
import { ApiError, request } from "./api";
import SoftwareParameterFields from "./SoftwareParameterFields.vue";
import { configurationContext, configurationValues, parameterChanges, parameterDefaults, parameterError, type CredentialReference, type ManagementContext, type ManagementOperation, type ManagementTable } from "./softwareManagement";
import { softwareJobStatusLabel, type SoftwareInstance, type SoftwareJob, type SoftwareMutationResult } from "./software";

interface OperationPlan { id: string; operation: ManagementOperation; expiresAt: string; checks: { key: string; status: string; message: string }[]; resources: { key: string; type: string; value: string }[] }
interface OperationJob extends SoftwareJob { kind: string; operationKey?: string; result?: { management?: { output: string; table?: ManagementTable } } }
const props = defineProps<{ detail: SoftwareInstance; active: boolean; disabled?: boolean; presentation?: "configuration" | "migration"; initialContext?: ManagementContext }>();
const emit = defineEmits<{ submitted: [SoftwareMutationResult]; completed: []; logs: [string]; preparation: []; closeState: [{ blocked: boolean; dirty: boolean; pending?: boolean }] }>();
const context = ref<ManagementContext>(), operationKey = ref("");
const parameters = ref<Record<string, string>>({}), operationParameters = ref<Record<string, string>>({});
const credentialRefs = ref<Record<string, CredentialReference>>({}), operationCredentialRefs = ref<Record<string, CredentialReference>>({});
const sharedFields = ref<InstanceType<typeof SoftwareParameterFields>>(), actionFields = ref<InstanceType<typeof SoftwareParameterFields>>();
const account = ref(""), busy = ref(false), credentialsBusy = ref(false), sharedSecretDraft = ref(false), operationSecretDraft = ref(false), error = ref("");
const secretDraft = computed(() => sharedSecretDraft.value || operationSecretDraft.value);
const diagnostics = ref<ApiError>(), plan = ref<OperationPlan>(), job = ref<OperationJob>();
const submitKey = ref(""), submissionAttempted = ref(false), baseline = ref(""), operationBaseline = ref(""), pendingSelection = ref("");
const configurationReady = ref(false), currentValues = ref<Record<string, string>>({}), dataDirectory = ref("");
const checkedMigrationParameters = ref(""), migrationReport = ref<ManagementTable>();
const editor = computed(() => !!props.presentation);
const migrationChecked = computed(() => !!checkedMigrationParameters.value && checkedMigrationParameters.value === JSON.stringify(operationParameters.value));
const changes = computed(() => parameterChanges(sharedDefinitions.value, currentValues.value, parameters.value));
const restartImpact = computed(() => plan.value?.checks.find(check => /RESTART|IMPACT/.test(check.key) || /重启|重载|短暂停止/.test(check.message))?.message || (plan.value?.operation.rebind ? "运行中的服务将重启。" : ""));
const migrationLabels: Record<string, string> = { CURRENT_DATA_DIRECTORY: "当前目录", DATA_DIRECTORY: "目标目录", FILE_COUNT: "文件数量", DATA_BYTES: "数据大小（字节）", AVAILABLE_BYTES: "可用空间（字节）", CHECK: "检查结果" };
let disposed = false, revision = 0, timer: ReturnType<typeof setTimeout> | undefined, readAbort: AbortController | undefined;
let acknowledgementJob = "", acknowledgementPolls = 0;
const acknowledgementPaused = ref(false);
const operations = computed(() => (context.value?.definition?.management?.operations || context.value?.operations || [])
  .filter(item => !item.runtimeModes?.length || item.runtimeModes.includes(props.detail.installation?.config?.runtimeMode ||
    (props.detail.runtimeKind === "TOOL" ? props.detail.installation?.definition?.runtimeModes[0] : undefined) ||
    (props.detail.runtimeKind === "DOCKER" ? "DOCKER" : "NATIVE"))));
const operation = computed(() => operations.value.find(item => item.key === operationKey.value));
const groups = computed(() => [...new Set(operations.value.map(item => item.group || "常用操作"))]);
const sharedDefinitions = computed(() => context.value?.definition?.management?.parameters || []);
const pending = computed(() => !!job.value && (["QUEUED", "RUNNING", "UNKNOWN"].includes(job.value.status) || job.value.targetAckPending));
const blocked = computed(() => busy.value || credentialsBusy.value || submissionAttempted.value && !job.value);
const closeBlocked = computed(() => editor.value ? credentialsBusy.value || submissionAttempted.value && !job.value && !operation.value?.readOnly : blocked.value);
const operationDirty = computed(() => !!operationBaseline.value && operationBaseline.value !== JSON.stringify([operationParameters.value, operationCredentialRefs.value]));
const dirty = computed(() => secretDraft.value || operationDirty.value || !!baseline.value && baseline.value !== JSON.stringify([parameters.value, credentialRefs.value]));
const needsAccount = computed(() => [...(operation.value?.steps || []), ...(operation.value?.verifySteps || []), ...(operation.value?.rollbackSteps || [])].some(step => step.runAs === "ACCOUNT"));
watch([closeBlocked, dirty, pending], () => emit("closeState", { blocked: closeBlocked.value, dirty: dirty.value, pending: pending.value }), { immediate: true });
watch([parameters, credentialRefs, operationParameters, operationCredentialRefs, account], () => {
  if (!submissionAttempted.value) { plan.value = undefined; submitKey.value = ""; }
}, { deep: true });
function checkpoint() { sharedSecretDraft.value = false; operationSecretDraft.value = false; baseline.value = JSON.stringify([parameters.value, credentialRefs.value]); operationBaseline.value = JSON.stringify([operationParameters.value, operationCredentialRefs.value]); }
function showError(cause: unknown) {
  error.value = cause instanceof Error ? cause.message : "操作未完成，请重试";
  diagnostics.value = cause instanceof ApiError ? cause : undefined;
}
function stopReading() { if (timer) clearTimeout(timer); timer = undefined; readAbort?.abort(); }
async function load() {
  if (!props.active || disposed || context.value) return;
  const current = ++revision, abort = new AbortController(); readAbort = abort;
  try {
    const result = props.initialContext || await request<ManagementContext>(`/software-instances/${props.detail.id}/management`, "GET", undefined, abort.signal);
    if (disposed || current !== revision || abort.signal.aborted) return;
    context.value = editor.value ? configurationContext(result, props.detail) : result;
    parameters.value = parameterDefaults(context.value.definition?.management?.parameters, context.value.parameters);
    credentialRefs.value = result.credentialRefs || {};
    account.value = props.detail.installation?.config?.account || "";
    selectOperation(operations.value[0]?.key || "");
    checkpoint();
    if (editor.value) {
      if (result.lastJobId) { await readJob(result.lastJobId); if (disposed || current !== revision || pending.value) return; }
      await readConfiguration();
    } else if (result.lastJobId) void readJob(result.lastJobId, true);
  } catch (cause) { if (!disposed && !abort.signal.aborted && current === revision) showError(cause); }
}
function selectOperation(key: string, completedRead = false) {
  if (!completedRead && (blocked.value || pending.value)) return;
  operationKey.value = key;
  operationParameters.value = parameterDefaults(operations.value.find(item => item.key === key)?.parameters);
  operationCredentialRefs.value = {};
  operationSecretDraft.value = false;
  plan.value = undefined; job.value = undefined; submissionAttempted.value = false; submitKey.value = "";
  operationBaseline.value = JSON.stringify([operationParameters.value, operationCredentialRefs.value]);
  pendingSelection.value = "";
}
async function readConfiguration() {
  if (pending.value || busy.value) return;
  configurationReady.value = false;
  checkedMigrationParameters.value = ""; migrationReport.value = undefined;
  selectOperation("read-config");
  await check();
}
async function checkMigration() {
  if (!configurationReady.value || pending.value || blocked.value) return;
  const values = { ...operationParameters.value };
  selectOperation("check-data-migration");
  operationParameters.value = values;
  await check();
}
function chooseOperation(key: string) {
  if (key === operationKey.value) return;
  if (operationDirty.value || operationSecretDraft.value) pendingSelection.value = key;
  else selectOperation(key);
}
function adoptTemplate() {
  const next = context.value?.availableTemplate;
  if (!next || blocked.value || pending.value) return;
  context.value = { ...next, softwareVersion: context.value!.softwareVersion, stateVersion: context.value!.stateVersion };
  parameters.value = parameterDefaults(next.definition?.management?.parameters, parameters.value);
  credentialRefs.value = Object.fromEntries((next.definition?.management?.parameters || []).filter(field => field.type === "CREDENTIAL" && credentialRefs.value[field.key]).map(field => [field.key, credentialRefs.value[field.key]!]));
  selectOperation(operations.value[0]?.key || "");
}
async function check() {
  if (!context.value || !operation.value || blocked.value || props.disabled || pending.value) return;
  if (editor.value && !operation.value.readOnly && (!configurationReady.value || props.presentation === "migration" && !migrationChecked.value)) return;
  const requestedRevision = revision;
  if (sharedFields.value && !(await sharedFields.value.saveCredentials()) || actionFields.value && !(await actionFields.value.saveCredentials())) return;
  if (disposed || requestedRevision !== revision || !props.active || blocked.value || props.disabled || pending.value) return;
  const message = (editor.value && operationKey.value === "read-config" ? "" : parameterError(sharedDefinitions.value, parameters.value, credentialRefs.value)) || parameterError(operation.value.parameters, operationParameters.value, operationCredentialRefs.value);
  if (message) { error.value = message; return; }
  const current = revision;
  busy.value = true; error.value = ""; diagnostics.value = undefined;
  try {
    const result = await request<OperationPlan>(`/software-instances/${props.detail.id}/operation-plans`, "POST", {
      expectedVersion: context.value.softwareVersion || props.detail.version, expectedManagementVersion: context.value.stateVersion,
      templateId: context.value.templateId, templateRevision: context.value.templateRevision,
      operationKey: operation.value.key, parameters: parameters.value, credentialRefs: credentialRefs.value,
      operationParameters: operationParameters.value, operationCredentialRefs: operationCredentialRefs.value, account: account.value,
    });
    if (disposed || current !== revision) return;
    plan.value = result; submitKey.value = crypto.randomUUID();
  } catch (cause) { if (!disposed && current === revision) showError(cause); }
  finally { if (!disposed && current === revision) busy.value = false; }
  if (plan.value?.operation.readOnly && !disposed) await submit();
}
async function submit() {
  if (!plan.value || busy.value || credentialsBusy.value || pending.value || !submitKey.value) return;
  if (editor.value && !plan.value.operation.readOnly && (!configurationReady.value || props.presentation === "migration" && !migrationChecked.value)) return;
  const current = revision;
  busy.value = true; submissionAttempted.value = true; error.value = "";
  try {
    const receipt = await request<SoftwareMutationResult>(`/software-operation-plans/${plan.value.id}/submit`, "POST", { idempotencyKey: submitKey.value });
    if (disposed || current !== revision) return;
    checkpoint(); if (!editor.value || !plan.value.operation.readOnly) emit("submitted", receipt);
    if (receipt.jobId) job.value = { id: receipt.jobId, kind: "SOFTWARE_OPERATION", status: "UNKNOWN", phase: "OPERATION_SUBMITTED", errorCode: "", traceId: "", updatedAt: new Date().toISOString(), targetAckPending: false };
    if (receipt.jobId) await readJob(receipt.jobId);
  } catch (cause) {
    if (!disposed && current === revision) {
      if (cause instanceof ApiError && cause.status === 409 && ["MANAGEMENT_PLAN_STALE", "MANAGEMENT_STATE_CHANGED", "MANAGEMENT_RESOURCE_CHANGED", "MANAGEMENT_RESOURCES_CHANGED", "OPERATION_LOCKED", "MANAGEMENT_TEMPLATE_MISMATCH"].includes(cause.code)) {
        submissionAttempted.value = false; plan.value = undefined; submitKey.value = "";
      }
      showError(cause);
    }
  } finally { if (!disposed && current === revision) busy.value = false; }
}
async function readJob(id: string, restoreOperation = false) {
  if (disposed || !props.active) return;
  stopReading();
  const current = revision, abort = new AbortController(); readAbort = abort;
  try {
    const result = await request<OperationJob>(`/jobs/${id}`, "GET", undefined, abort.signal);
    if (disposed || current !== revision || abort.signal.aborted) return;
    if (restoreOperation && result.operationKey && operations.value.some(item => item.key === result.operationKey)) selectOperation(result.operationKey);
    job.value = result;
    if (acknowledgementJob !== id || !result.targetAckPending) { acknowledgementJob = id; acknowledgementPolls = 0; acknowledgementPaused.value = false; }
    if (["QUEUED", "RUNNING"].includes(result.status) || result.targetAckPending) {
      if (!result.targetAckPending || ++acknowledgementPolls <= 5) timer = setTimeout(() => void readJob(id), 2000);
      else acknowledgementPaused.value = true;
    }
    else if (result.status === "SUCCEEDED" && !result.targetAckPending) {
      const latest = await request<ManagementContext>(`/software-instances/${props.detail.id}/management`, "GET", undefined, abort.signal);
      if (!disposed && current === revision && !abort.signal.aborted) {
        context.value = editor.value ? configurationContext(latest, props.detail) : latest; submissionAttempted.value = false;
        const completedRead = plan.value?.operation.readOnly ? plan.value.operation.key : "";
        if (editor.value && completedRead === "read-config") {
          const values = configurationValues(result.result?.management?.table, sharedDefinitions.value);
          parameters.value = { ...parameters.value, ...values };
          currentValues.value = { ...values };
          dataDirectory.value = result.result?.management?.table?.rows.find(row => row.key === "DATA_DIRECTORY")?.value || "";
          configurationReady.value = true;
          selectOperation(props.presentation === "migration" ? "migrate-data" : "configure", true);
          checkpoint();
        } else if (editor.value && completedRead === "check-data-migration") {
          const values = { ...operationParameters.value };
          checkedMigrationParameters.value = JSON.stringify(values);
          migrationReport.value = result.result?.management?.table;
          selectOperation("migrate-data", true); operationParameters.value = values;
        } else if (editor.value && plan.value && !plan.value.operation.readOnly) emit("completed");
      }
    } else if (editor.value && result.status === "FAILED" && plan.value?.operation.readOnly) {
      error.value = operationKey.value === "read-config" ? "读取当前配置失败，请查看日志后重试" : "迁移检查未通过，请查看日志后调整目标目录";
    }
  } catch (cause) { if (!disposed && !abort.signal.aborted && current === revision) showError(cause); }
}
async function reconcile() {
  if (!job.value || busy.value) return;
  const current = revision, id = job.value.id;
  busy.value = true;
  try { await request(`/jobs/${id}/reconcile`, "POST", {}); if (!disposed && current === revision) await readJob(id); }
  catch (cause) { if (!disposed && current === revision) showError(cause); }
  finally { if (!disposed && current === revision) busy.value = false; }
}
watch(() => props.detail.id, () => {
  revision++; stopReading(); context.value = undefined; job.value = undefined; plan.value = undefined;
  submissionAttempted.value = false; submitKey.value = ""; operationKey.value = "";
  parameters.value = {}; operationParameters.value = {}; credentialRefs.value = {}; operationCredentialRefs.value = {};
  configurationReady.value = false; currentValues.value = {}; dataDirectory.value = "";
  checkedMigrationParameters.value = ""; migrationReport.value = undefined; baseline.value = ""; operationBaseline.value = "";
  error.value = ""; diagnostics.value = undefined; busy.value = false; if (props.active) void load();
});
watch(() => props.active, (active) => { if (active) { if (job.value) void readJob(job.value.id); else void load(); } else stopReading(); }, { immediate: true });
onUnmounted(() => { disposed = true; revision++; stopReading(); });
</script>

<template>
  <section class="software-management-panel" aria-label="软件管理操作">
    <p v-if="error" class="operation-error" role="status">{{ error }}</p>
    <button v-if="diagnostics && ['EXECUTOR_UPGRADE_REQUIRED', 'COMMAND_TRUST_REQUIRED'].includes(diagnostics.code)" class="quiet" @click="emit('preparation')">更新接入组件</button>
    <button v-if="error && !context" class="quiet" @click="load">重新加载</button>
    <template v-if="editor && context">
      <div class="configuration-heading"><strong>{{ job ? '执行结果' : plan && !plan.operation.readOnly ? '确认变更' : presentation === 'migration' ? '迁移数据' : '当前配置' }}</strong><button class="quiet" :disabled="blocked || pending || dirty" @click="readConfiguration">{{ job && !plan?.operation.readOnly && !pending ? '继续编辑' : '重新读取' }}</button></div>
      <label v-if="needsAccount">执行账号<input v-model="account" :disabled="blocked || disabled || pending" placeholder="例如 ubuntu" maxlength="32" /></label>
      <p v-if="!configurationReady" class="configuration-reading" role="status">{{ pending || busy ? '正在读取当前配置…' : '请成功读取当前配置后继续' }}</p>
      <template v-if="configurationReady">
        <div v-show="!plan && !job" class="configuration-fields">
        <label v-if="dataDirectory" class="current-data-directory">当前数据目录<code>{{ dataDirectory }}</code></label>
        <SoftwareParameterFields v-if="presentation === 'configuration'" ref="sharedFields" :fields="sharedDefinitions" v-model="parameters" v-model:credential-refs="credentialRefs" :disabled="blocked || disabled || pending || !!job" :context-name="detail.name" @busy="credentialsBusy = $event" @draft="sharedSecretDraft = $event" @error="showError" />
        <SoftwareParameterFields v-else ref="actionFields" :fields="operation?.parameters || []" v-model="operationParameters" :disabled="blocked || disabled || pending || !!job" :context-name="detail.name" @busy="credentialsBusy = $event" @error="showError" />
        </div>
        <p v-if="presentation === 'migration'" class="migration-retain">迁移完成后保留原目录。</p>
        <table v-if="migrationReport && migrationChecked" class="configuration-diff"><tbody><tr v-for="(row,index) in migrationReport.rows" :key="index"><td v-for="column in migrationReport.columns" :key="column">{{ column === 'key' ? migrationLabels[row[column]] || row[column] : row[column] }}</td></tr></tbody></table>
        <section v-if="plan && !job && !plan.operation.readOnly" class="operation-plan" aria-label="配置变更预览">
          <table v-if="presentation === 'configuration'" class="configuration-diff"><thead><tr><th>配置项</th><th>当前值</th><th>修改后</th></tr></thead><tbody><tr v-for="change in changes" :key="change.key"><th>{{ change.label }}</th><td>{{ change.before }}</td><td>{{ change.after }}</td></tr></tbody></table>
          <p v-if="restartImpact" class="configuration-impact">{{ restartImpact }}</p>
          <p v-for="check in plan.checks.filter(check => check.status !== 'PASS')" :key="check.key">{{ check.message }}</p>
          <details><summary>执行步骤</summary><details v-if="context.definition?.management?.scriptPrelude"><summary>共用脚本</summary><pre>{{ context.definition.management.scriptPrelude }}</pre></details><div v-for="step in [...plan.operation.steps, ...(plan.operation.verifySteps || [])]" :key="step.name"><strong>{{ step.name }}</strong><pre>{{ step.script }}</pre></div></details>
        </section>
        <div v-if="!pending && !job" class="operation-actions">
          <button v-if="presentation === 'migration' && !migrationChecked" class="primary" :disabled="blocked || disabled" @click="checkMigration">检查迁移</button>
          <button v-else-if="!plan" class="primary" :disabled="blocked || disabled || presentation === 'configuration' && !dirty" @click="check">{{ presentation === 'migration' ? '预览迁移' : '检查变更' }}</button>
          <template v-else><button class="secondary" :disabled="blocked" @click="plan = undefined">返回修改</button><button class="primary" :disabled="busy || credentialsBusy || disabled" @click="submit">{{ submissionAttempted && !job ? '核对提交结果' : presentation === 'migration' ? '确认迁移' : '保存配置' }}</button></template>
        </div>
      </template>
      <section v-if="job" class="operation-result" aria-label="配置执行结果"><header><strong>{{ softwareJobStatusLabel(job.status) }}</strong><button class="quiet" @click="emit('logs', job.id)">查看日志</button><button v-if="job.status === 'UNKNOWN' || job.targetAckPending" class="quiet" :disabled="busy" @click="reconcile">核对任务结果</button></header><p v-if="job.targetAckPending">{{ acknowledgementPaused ? '执行已完成，请核对任务结果。' : '执行已完成，正在确认结果…' }}</p></section>
    </template>
    <template v-else-if="context">
      <div v-if="context.availableTemplate" class="management-update"><span>有新的管理方案</span><button class="quiet" :disabled="blocked || pending" @click="adoptTemplate">查看新版</button></div>
      <nav v-for="group in groups" :key="group" class="operation-group" :aria-label="group"><strong>{{ group }}</strong><div><button v-for="item in operations.filter(item => (item.group || '常用操作') === group)" :key="item.key" :class="{ selected: item.key === operationKey }" :disabled="blocked || pending" @click="chooseOperation(item.key)">{{ item.name }}</button></div></nav>
      <div v-if="pendingSelection" class="management-update"><span>放弃本次操作输入并切换？</span><button class="quiet" @click="selectOperation(pendingSelection)">切换</button><button class="quiet" @click="pendingSelection = ''">继续编辑</button></div>
      <template v-if="operation">
        <details v-if="sharedDefinitions.length" class="shared-configuration" :open="context.stateVersion === '0'"><summary>管理连接与配置</summary><SoftwareParameterFields ref="sharedFields" :fields="sharedDefinitions" v-model="parameters" v-model:credential-refs="credentialRefs" :disabled="blocked || disabled || pending" :context-name="detail.name" @busy="credentialsBusy = $event" @draft="sharedSecretDraft = $event" @error="showError" /></details>
        <h3>{{ operation.name }}</h3>
        <SoftwareParameterFields :key="operationKey" ref="actionFields" :fields="operation.parameters || []" v-model="operationParameters" v-model:credential-refs="operationCredentialRefs" :disabled="blocked || disabled || pending" :context-name="detail.name" @busy="credentialsBusy = $event" @draft="operationSecretDraft = $event" @error="showError" />
        <label v-if="needsAccount">执行账号<input v-model="account" :disabled="blocked || disabled || pending" placeholder="例如 ubuntu" maxlength="32" /></label>
        <div v-if="!pending" class="operation-actions"><button v-if="!plan" class="primary" :disabled="blocked || disabled" @click="check">{{ operation.readOnly ? '查询' : '检查并预览' }}</button><template v-else><button class="secondary" :disabled="blocked" @click="plan = undefined">返回修改</button><button class="primary" :disabled="busy || credentialsBusy" @click="submit">{{ submissionAttempted && !job ? '核对提交结果' : '确认执行' }}</button></template></div>
        <section v-if="plan && !job" class="operation-plan"><p v-for="check in plan.checks.filter(item => item.status !== 'PASS')" :key="check.key">{{ check.message }}</p><details><summary>执行命令与资源</summary><div v-for="step in [...plan.operation.steps, ...(plan.operation.verifySteps || [])]" :key="step.name"><strong>{{ step.name }} · {{ step.runAs === 'ROOT' ? '管理员' : '指定账号' }}</strong><pre>{{ step.script }}</pre></div><dl><template v-for="resource in plan.resources" :key="resource.key"><dt>{{ resource.type }}</dt><dd><code>{{ resource.value }}</code></dd></template></dl></details></section>
      </template>
      <section v-if="job" class="operation-result" aria-label="管理操作结果"><header><strong>{{ softwareJobStatusLabel(job.status) }}</strong><button class="quiet" @click="emit('logs', job.id)">查看输出</button><button v-if="job.status === 'UNKNOWN' || job.targetAckPending" class="quiet" :disabled="busy" @click="reconcile">核对任务结果</button><button v-else-if="error" class="quiet" @click="readJob(job.id)">刷新结果</button></header><div v-if="job.result?.management?.table" class="operation-table"><table><thead><tr><th v-for="column in job.result.management.table.columns" :key="column">{{ column }}</th></tr></thead><tbody><tr v-for="(row, index) in job.result.management.table.rows" :key="index"><td v-for="column in job.result.management.table.columns" :key="column">{{ row[column] }}</td></tr></tbody></table><p v-if="job.result.management.table.total != null">共 {{ job.result.management.table.total }} 项<span v-if="job.result.management.table.total > job.result.management.table.rows.length">，显示前 {{ job.result.management.table.rows.length }} 项</span></p></div><pre v-else-if="job.result?.management?.output">{{ job.result.management.output }}</pre></section>
    </template>
    <details v-if="diagnostics" class="operation-diagnostics"><summary>诊断信息</summary><code>{{ diagnostics.code }}</code><code>{{ diagnostics.traceId }}</code></details>
  </section>
</template>

<style scoped>
.software-management-panel { display: grid; gap: 18px; min-width: 0; }
.configuration-heading { display:flex; align-items:center; justify-content:space-between; gap:12px; }
.configuration-fields { display:grid; gap:18px; min-width:0; }
.configuration-reading,.migration-retain,.configuration-impact { margin:0; font-size:13px; color:var(--muted); }
.current-data-directory { font-size:12px; color:var(--muted); }
.current-data-directory code { overflow-wrap:anywhere; color:var(--text); }
.configuration-diff { width:100%; min-width:0; border-collapse:collapse; table-layout:fixed; }
.configuration-diff th,.configuration-diff td { white-space:normal; overflow-wrap:anywhere; }
.operation-group > strong { display: block; color: var(--muted); font-size: 12px; margin-bottom: 9px; }
.operation-group > div { display: flex; flex-wrap: wrap; gap: 8px; }
.operation-group button { width: auto; background: white; border: 1px solid var(--border); border-radius: 5px; padding: 8px 12px; color: var(--text); }
.operation-group button.selected { border-color: var(--accent); color: var(--accent); background: var(--accent-soft); }
.shared-configuration { border: 1px solid var(--border); border-radius: 6px; padding: 14px; }
summary { cursor: pointer; color: var(--muted); font-size: 12px; }.shared-configuration[open] summary { margin-bottom: 18px; }
h3 { margin: 0; font-size: 15px; }
label { display: grid; gap: 8px; font-size: 13px; }
.operation-actions, .operation-result header, .management-update { display: flex; flex-wrap: wrap; align-items: center; gap: 12px; }
.operation-result header { justify-content: space-between; font-size: 13px; }.operation-error { color: var(--error); font-size: 13px; margin: 0; }
.operation-plan, .operation-result { min-width: 0; border-top: 1px solid var(--border); padding-top: 16px; }
pre { white-space: pre-wrap; overflow-wrap: anywhere; max-height: 300px; overflow: auto; padding: 12px; background: #122232; color: #dbeafe; font: 12px/1.6 var(--mono); border-radius: 6px; }
.operation-plan dl { display: grid; grid-template-columns: auto minmax(0, 1fr); gap: 8px; font-size: 12px; }.operation-plan dd { margin: 0; overflow-wrap: anywhere; }
.operation-table { overflow: auto; max-height: 400px; margin-top: 12px; }table { width: 100%; font-size: 12px; }th, td { white-space: nowrap; padding: 10px; text-align: left; border-bottom: 1px solid var(--border); }
.operation-diagnostics { font-size: 12px; }.operation-diagnostics code { display: block; margin-top: 8px; overflow-wrap: anywhere; }
</style>
