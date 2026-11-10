<script setup lang="ts">
import { computed, ref } from "vue";
import type { ServiceRecipe } from "./serviceInstallation";
import type { ManagementStep, ParameterDefinition } from "./softwareManagement";
import SoftwareResourceFields from "./SoftwareResourceFields.vue";
import { webDefinitionError } from "./webAccess";

const props = defineProps<{ definition: ServiceRecipe; disabled?: boolean; preview?: boolean }>();
const tab = ref("parameters"), operationIndex = ref(0);
const parameterTypes: Record<ParameterDefinition["type"], string> = { TEXT: "文本", INTEGER: "整数", BOOLEAN: "开关", SELECT: "下拉选项", PATH: "路径", PORT: "端口", CREDENTIAL: "凭据" };
const selected = computed(() => props.definition.management?.operations[operationIndex.value]);
const ensureManagement = () => props.definition.management ||= { parameters: [], operations: [], resources: [] };
function addParameter(scope: "install" | "instance" | "operation") {
  const field: ParameterDefinition = { key: "", label: "", type: "TEXT" };
  if (scope === "install") (props.definition.parameters ||= []).push(field);
  else if (scope === "instance") (ensureManagement().parameters ||= []).push(field);
  else selected.value?.parameters?.push(field);
}
function addOperation() {
  const operations = ensureManagement().operations;
  operations.push({ key: "", name: "新操作", group: "", parameters: [], steps: [newStep()], verifySteps: [], resultFormat: "TEXT" });
  operationIndex.value = operations.length - 1;
}
function newStep(): ManagementStep { return { name: "", script: "", runAs: "ROOT", timeoutSeconds: 60 }; }
const parameterSections = computed(() => [
  { key: "install" as const, title: "安装参数", fields: props.definition.parameters || [] },
  { key: "instance" as const, title: "管理配置", fields: props.definition.management?.parameters || [] },
]);
function changeParameterType(field: ParameterDefinition) {
  delete field.min; delete field.max;
  if (field.type === "SELECT") field.options ||= [];
  else delete field.options;
  if (field.type === "CREDENTIAL") delete field.default;
}
function options(field: ParameterDefinition, value: string) { field.options = value.split("\n").map(item => item.trim()).filter(Boolean); }
function deleteOperation() { props.definition.management?.operations.splice(operationIndex.value, 1); operationIndex.value = Math.max(0, operationIndex.value - 1); }
function setRuntime(mode: string) {
  if (selected.value) selected.value.runtimeModes = mode === "ALL" ? [] : [mode];
}
function setWeb(enabled: boolean) { props.definition.web = enabled ? { scheme: "http", path: "/" } : undefined; }
const urlParameters = computed(() => props.definition.parameters?.filter(field => field.type === "TEXT") || []);
function validationError() {
  const webError = webDefinitionError(props.definition);
  if (webError) return webError;
	if (props.definition.management?.scriptPrelude && (new TextEncoder().encode(props.definition.management.scriptPrelude).length > 32768 || props.definition.management.scriptPrelude.includes('\0'))) return "共用脚本须在 32 KiB 以内，不能包含空字符";
  const fields = [...(props.definition.parameters || []), ...(props.definition.management?.parameters || []), ...(props.definition.management?.operations.flatMap(item => item.parameters || []) || [])];
  for (const field of fields) {
    if (!/^[A-Z][A-Z0-9_]{0,47}$/.test(field.key) || !field.label.trim()) return "填写参数名称和大写参数键";
    if (field.type === "SELECT" && !field.options?.length) return `${field.label}至少需要一个选项`;
  }
  for (const operation of props.definition.management?.operations || []) {
    if (!/^[a-z][a-z0-9-]{0,47}$/.test(operation.key) || !operation.name.trim()) return "填写操作名称和操作键";
    if (!operation.steps.length || !operation.readOnly && !operation.verifySteps?.length) return `${operation.name}需要执行步骤，变更操作还需结果核验`;
    if (operation.rebind && (operation.readOnly || !['configure', 'migrate-data'].includes(operation.key) || !operation.rollbackSteps?.length)) return `${operation.name}需使用 configure 或 migrate-data 操作键，并填写失败恢复步骤`;
    if (operation.rollbackSteps?.length && !operation.rebind) return `${operation.name}的失败恢复步骤需要启用配置更新`;
    if ([...operation.steps, ...(operation.verifySteps || []), ...(operation.rollbackSteps || [])].some(step => !step.name.trim() || !step.script.trim() || step.timeoutSeconds < 1 || step.timeoutSeconds > 3600)) return `补全${operation.name}的步骤名称、脚本与超时`;
  }
  const resources = [...(props.definition.management?.resources || []), ...(props.definition.management?.operations.flatMap(operation => operation.resources || []) || [])];
  if (resources.some(resource => !/^[a-zA-Z][a-zA-Z0-9_-]{0,47}$/.test(resource.key) || !resource.value.trim())) return "填写资源名称及目标值";
  if (props.definition.management?.operations.some(operation => operation.readOnly && operation.resources?.some(resource => resource.release))) return "查询操作不能释放资源";
  return "";
}
defineExpose({ validationError });
</script>

<template>
  <section class="definition-extensions" aria-label="软件参数与管理操作">
    <div class="extension-tabs" role="tablist" aria-label="编辑软件能力">
      <button v-for="item in [{ key: 'parameters', name: '参数' }, { key: 'operations', name: '管理操作' }, { key: 'resources', name: '资源占用' }, { key: 'web', name: '访问入口' }]" :key="item.key" type="button" role="tab" :aria-selected="tab === item.key" :class="{ active: tab === item.key }" @click="tab = item.key">{{ item.name }}</button>
    </div>
    <fieldset :disabled="disabled || preview">
      <template v-if="tab === 'parameters'">
        <section v-for="section in parameterSections" :key="section.key" class="extension-section">
          <header><h4>{{ section.title }}</h4><button v-if="!preview" class="quiet" type="button" @click="addParameter(section.key)">添加参数</button></header>
          <details v-for="(field, index) in section.fields" :key="index" class="extension-item" open>
            <summary>{{ field.label || '新参数' }}<code v-if="field.key">OPS_PARAM_{{ field.key }}</code></summary>
            <div class="field-grid">
              <label>参数名称<input v-model="field.label" maxlength="120" /></label><label>参数键<input v-model="field.key" placeholder="例如 HTTP_PORT" maxlength="48" spellcheck="false" /></label>
              <label>类型<select v-model="field.type" @change="changeParameterType(field)"><option v-for="(label, key) in parameterTypes" :key="key" :value="key">{{ label }}</option></select></label>
              <label v-if="field.type !== 'CREDENTIAL'">默认值<input v-model="field.default" :type="['INTEGER', 'PORT'].includes(field.type) ? 'number' : 'text'" /></label>
              <label v-if="['INTEGER', 'PORT'].includes(field.type)">最小值<input v-model.number="field.min" type="number" /></label><label v-if="['INTEGER', 'PORT'].includes(field.type)">最大值<input v-model.number="field.max" type="number" /></label>
              <label v-if="field.type === 'SELECT'" class="wide">可选值<textarea :value="field.options?.join('\n')" placeholder="每行一个值" @input="options(field, ($event.target as HTMLTextAreaElement).value)"></textarea></label>
              <label class="checkbox"><input v-model="field.required" type="checkbox" />必填</label>
            </div><button v-if="!preview" class="quiet danger" type="button" @click="section.fields.splice(index, 1)">删除参数</button>
          </details>
          <p v-if="!section.fields.length" class="empty-small">暂无参数</p>
        </section>
      </template>
      <template v-else-if="tab === 'operations'">
        <details v-if="definition.management" class="extension-item"><summary>共用脚本</summary><textarea v-model="definition.management.scriptPrelude" class="script-input" aria-label="共用脚本" spellcheck="false"></textarea><p class="empty-small">安装、卸载和管理步骤执行前加载，用于维护共用函数。</p></details>
        <header><select v-model.number="operationIndex" aria-label="选择管理操作"><option v-for="(operation, index) in definition.management?.operations" :key="index" :value="index">{{ operation.name || '新操作' }}</option></select><button v-if="!preview" class="quiet" type="button" @click="addOperation">添加操作</button></header>
        <template v-if="selected">
          <div class="field-grid">
            <label>操作名称<input v-model="selected.name" maxlength="120" /></label><label>操作键<input v-model="selected.key" placeholder="例如 list-databases" spellcheck="false" maxlength="48" /></label>
            <label>分组<input v-model="selected.group" placeholder="例如 数据库" /></label><label>结果形式<select v-model="selected.resultFormat" aria-label="结果形式"><option value="TEXT">文本</option><option value="TABLE">表格</option></select></label>
            <label class="checkbox"><input v-model="selected.readOnly" type="checkbox" />查询操作</label>
            <label v-if="['configure', 'migrate-data'].includes(selected.key) && !selected.readOnly || selected.rebind" class="checkbox"><input v-model="selected.rebind" type="checkbox" />更新服务配置与绑定</label>
            <label>适用运行方式<select :value="selected.runtimeModes?.length === 1 ? selected.runtimeModes[0] : 'ALL'" aria-label="适用运行方式" @change="setRuntime(($event.target as HTMLSelectElement).value)"><option value="ALL">原生和 Docker</option><option value="NATIVE">仅原生</option><option value="DOCKER">仅 Docker</option></select></label>
          </div>
          <section class="extension-section">
            <header><h4>本次操作参数</h4><button v-if="!preview" class="quiet" type="button" @click="selected.parameters ||= []; addParameter('operation')">添加参数</button></header>
            <details v-for="(field, index) in selected.parameters" :key="index" class="extension-item" open><summary>{{ field.label || '新参数' }}</summary><div class="field-grid"><label>名称<input v-model="field.label" maxlength="120" /></label><label>参数键<input v-model="field.key" maxlength="48" spellcheck="false" /></label><label>类型<select v-model="field.type" @change="changeParameterType(field)"><option v-for="(label, key) in parameterTypes" :key="key" :value="key">{{ label }}</option></select></label><label v-if="field.type !== 'CREDENTIAL'">默认值<input v-model="field.default" /></label><label v-if="field.type === 'SELECT'" class="wide">可选值<textarea :value="field.options?.join('\n')" placeholder="每行一个值" @input="options(field, ($event.target as HTMLTextAreaElement).value)"></textarea></label><label class="checkbox"><input v-model="field.required" type="checkbox" />必填</label></div><button v-if="!preview" class="quiet danger" type="button" @click="selected.parameters!.splice(index, 1)">删除参数</button></details>
          </section>
          <SoftwareResourceFields v-model:resources="selected.resources" :preview="preview" :allow-release="!selected.readOnly"><h4>本次操作资源</h4></SoftwareResourceFields>
          <section v-for="group in [{ key: 'steps' as const, title: '执行步骤' }, { key: 'verifySteps' as const, title: '结果核验' }, ...(selected.rebind || selected.rollbackSteps?.length ? [{ key: 'rollbackSteps' as const, title: '失败恢复' }] : [])]" :key="group.key" class="extension-section">
            <header><h4>{{ group.title }}</h4><button v-if="!preview" class="quiet" type="button" @click="(selected[group.key] ||= []).push(newStep())">添加步骤</button></header>
            <details v-for="(step, index) in selected[group.key]" :key="index" class="extension-item" open><summary>{{ index + 1 }}. {{ step.name || '新步骤' }}</summary><div class="field-grid"><label>步骤名称<input v-model="step.name" maxlength="120" /></label><label>执行权限<select v-model="step.runAs"><option value="ROOT">管理员</option><option value="ACCOUNT">指定账号</option></select></label><label>超时（秒）<input v-model.number="step.timeoutSeconds" type="number" min="1" max="3600" /></label><label>工作目录<input v-model="step.workingDirectory" placeholder="默认使用实例工作目录" /></label></div><textarea v-model="step.script" class="script-input" :aria-label="group.title + '脚本'" spellcheck="false" maxlength="16384"></textarea><button v-if="!preview" class="quiet danger" type="button" @click="selected[group.key]!.splice(index, 1)">删除步骤</button></details>
          </section>
          <button v-if="!preview" class="quiet danger" type="button" @click="deleteOperation">删除此操作</button>
        </template>
        <p v-else class="empty-small">暂无管理操作</p>
      </template>
      <template v-else-if="tab === 'resources'">
        <SoftwareResourceFields :resources="definition.management?.resources" :preview="preview" @update:resources="ensureManagement().resources = $event"><h4>实例资源</h4></SoftwareResourceFields>
      </template>
      <template v-else>
        <label class="checkbox"><input type="checkbox" :checked="!!definition.web" @change="setWeb(($event.target as HTMLInputElement).checked)" />启用 Web 入口</label>
        <div v-if="definition.web" class="field-grid">
          <label>访问协议<select v-model="definition.web.scheme" aria-label="访问协议"><option value="http">HTTP</option><option value="https">HTTPS</option></select></label>
          <label>入口端口<select :value="definition.web.portKey || ''" aria-label="入口端口" @change="definition.web.portKey = ($event.target as HTMLSelectElement).value || undefined"><option value="">主端口</option><option v-for="port in definition.ports?.filter(port => port.key !== 'main' && port.protocol === 'TCP')" :key="port.key" :value="port.key">{{ port.label }}（{{ port.defaultPort }}）</option></select></label>
          <label class="wide">入口路径<input v-model="definition.web.path" placeholder="/" spellcheck="false" maxlength="512" /></label>
          <label class="wide">访问地址参数<select :value="definition.web.urlParameter || ''" aria-label="访问地址参数" @change="definition.web.urlParameter = ($event.target as HTMLSelectElement).value || undefined"><option value="">自动使用主机与安装端口</option><option v-for="field in urlParameters" :key="field.key" :value="field.key">{{ field.label }} · {{ field.key }}</option></select></label>
          <label class="checkbox"><input v-model="definition.web.embed" type="checkbox" />允许内嵌预览</label>
        </div>
      </template>
    </fieldset>
    <details class="extension-help"><summary>脚本输入与结果格式</summary><p>普通参数使用 OPS_PARAM_参数键，凭据使用 OPS_SECRET_参数键_FILE 指向的临时文件。查询的最后一步、变更的最后一个核验步骤输出表格结果：</p><pre>{"columns":["名称","状态"],"rows":[{"名称":"示例","状态":"正常"}],"total":1}</pre><p>编辑配置使用 read-config 读取当前值，输出 key、value 两列；configure 应用修改。数据迁移使用 check-data-migration 检查、migrate-data 执行。启用绑定更新时须提供失败恢复步骤；脚本按 OPS_JOB_ID 保留原配置，恢复后由执行器核对原服务身份。</p></details>
  </section>
</template>

<style scoped>
.definition-extensions { min-width: 0; border-top: 1px solid var(--border); padding-top: 18px; }
.extension-tabs { display: flex; flex-wrap: wrap; gap: 8px; margin-bottom: 16px; }
.extension-tabs button { width: auto; border: 0; padding: 8px 12px; border-radius: 5px; background: transparent; color: var(--muted); }
.extension-tabs button.active { color: var(--accent); background: var(--accent-soft); }
fieldset { min-width: 0; padding: 0; border: 0; margin: 0; }
header { display: flex; justify-content: space-between; align-items: center; gap: 12px; margin-bottom: 10px; }
header select { width: min(280px, 70%); }
h4 { margin: 0; font-size: 13px; }
.extension-section + .extension-section { margin-top: 22px; }
.extension-item { margin: 10px 0; padding: 12px; border: 1px solid var(--border); border-radius: var(--control-radius); }
summary { cursor: pointer; font-size: 13px; color: var(--text); }
summary code { margin-left: 12px; font-size: 11px; color: var(--muted); overflow-wrap: anywhere; }
.field-grid { display: grid; grid-template-columns: repeat(2, minmax(0, 1fr)); gap: 12px; margin-top: 14px; }
label { display: grid; gap: 6px; font-size: 12px; color: var(--muted); }
.checkbox { display: flex; align-items: center; gap: 8px; }.checkbox input { width: 16px; min-height: 16px; }
.wide { grid-column: 1 / -1; }
input, select, textarea { width: 100%; min-width: 0; }
.script-input { min-height: 140px; margin-top: 14px; font: 12px/1.6 var(--mono); }
.extension-help, .empty-small { margin: 16px 0 0; font-size: 12px; color: var(--muted); }
.extension-help pre { white-space: pre-wrap; overflow-wrap: anywhere; font-size: 12px; }
@media (max-width: 600px) { .field-grid { grid-template-columns: 1fr; } }
</style>
