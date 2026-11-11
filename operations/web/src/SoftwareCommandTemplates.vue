<script setup lang="ts">
import { computed, nextTick, onMounted, ref } from "vue";
import { ElDialog, ElDropdown, ElDropdownItem, ElDropdownMenu, ElMessageBox } from "element-plus";
import { ApiError, request } from "./api";
import OpsPagination from "./OpsPagination.vue";
import { usePagination } from "./pagination";
import { hostTime } from "./hosts";
import { recipeCatalogKey, type ServiceRecipe } from "./serviceInstallation";
import SoftwareIcon from "./SoftwareIcon.vue";
import RecipeSystemStepDetails from "./RecipeSystemStepDetails.vue";
import SoftwareDefinitionExtensions from "./SoftwareDefinitionExtensions.vue";

type Commands = NonNullable<ServiceRecipe["commands"]>;
type CommandStep = Commands["steps"][number];
interface CommandTemplate {
  id: string;
  version: string;
  revision: number;
  draft: ServiceRecipe;
  published?: ServiceRecipe;
  updatedAt: string;
}
defineProps<{ active: boolean }>();
const emit = defineEmits<{ changed: [] }>();
const phases: Record<CommandStep["phase"], string> = { INSTALL: "安装", VERIFY: "验证", UNINSTALL: "卸载", DELETE_DATA: "删除数据" };
const operations = { INSTALL: "安装步骤", UNINSTALL: "卸载步骤" } as const;
const operationOf = (phase: CommandStep["phase"]) => phase === "INSTALL" || phase === "VERIFY" ? "INSTALL" : "UNINSTALL";
const kinds: Record<Commands["kind"], string> = { TOOL: "程序工具", SERVICE: "服务程序", SYSTEMD: "系统服务", DOCKER: "Docker 容器" };
const distributions = ["ubuntu", "debian", "centos", "rhel", "rocky", "almalinux", "fedora", "opensuse", "alpine"];
const records = ref<CommandTemplate[]>([]);
const examples = ref<ServiceRecipe[]>([]);
const catalog = ref<ServiceRecipe[]>([]);
const catalogChoices = computed(() => {
  const groups = new Map<string, { key: string; name: string }>();
  for (const item of [...catalog.value, ...examples.value, ...records.value.map(record => record.draft)]) {
    const key = recipeCatalogKey(item);
    if (!groups.has(key)) groups.set(key, { key, name: item.name });
  }
  return Array.from(groups.values());
});
const selectedExample = ref("");
const search = ref("");
const statusFilter = ref("ALL");
const selectedId = ref("");
const editingVersion = ref("");
const draft = ref<ServiceRecipe>();
const baseline = ref("");
const editorOpen = ref(false);
const editorContent = ref<HTMLElement>();
const extensions = ref<InstanceType<typeof SoftwareDefinitionExtensions>>();
const activeStep = ref(-1);
const commandsOnly = ref(false);
const activePhase = ref<keyof typeof operations>("INSTALL");
const viewingRuntime = ref("NATIVE");
const viewingSource = ref("ONLINE");

const busy = ref(false);
const preview = ref(false);
const feedback = ref("");
const error = ref<{ message: string; code?: string; traceId?: string; at?: string }>();
const selected = computed(() => records.value.find(item => item.id === selectedId.value));
const commands = computed(() => draft.value?.commands);
const builtin = computed(() => !!commands.value?.builtin);
const kindChoices = computed(() => Object.entries(kinds).filter(([key]) => builtin.value || key !== "SERVICE"));
const step = computed(() => commands.value?.steps[activeStep.value]);
const dirty = computed(() => !!draft.value && JSON.stringify(draft.value) !== baseline.value);
const clone = <T,>(value: T): T => JSON.parse(JSON.stringify(value));
const softwareNames: Record<string, string> = { java: "Java", mysql: "MySQL", nginx: "Nginx", redis: "Redis", docker: "Docker Engine", node: "Node.js", nvm: "nvm", npm: "npm", pnpm: "pnpm", python: "Python", git: "Git", maven: "Maven", ffmpeg: "FFmpeg", codex: "Codex CLI", claude: "Claude Code", mediamtx: "MediaMTX", zlm: "ZLMediaKit", seaweedfs: "SeaweedFS" };
const sourceLabels: Record<string, string> = { ONLINE: "在线发行包", REGISTRY: "在线镜像", ARCHIVE: "离线安装包", OFFICIAL: "官方在线" };
function versionLabel(value: string) {
  if (value.endsWith("ubuntu24.04-repository")) {
    const version = value.replace(/-?ubuntu24\.04-repository$/, "");
    return version ? `${version} · Ubuntu 仓库` : "Ubuntu 仓库";
  }
  return value.replace(/-(official-)?ubuntu24\.04$/, " · Ubuntu 24.04");
}
const softwareGroups = computed(() => {
  const groups = new Map<string, { key: string; name: string; records: CommandTemplate[] }>();
  const query = search.value.trim().toLowerCase();
  for (const record of records.value) {
    if (statusFilter.value === "PUBLISHED" && !record.published || statusFilter.value === "DRAFT" && templateStatus(record) === "已发布") continue;
    const key = recipeCatalogKey(record.draft), name = softwareNames[key] || record.draft.name;
    if (query && !`${name} ${record.draft.name} ${record.draft.version} ${key}`.toLowerCase().includes(query)) continue;
    if (!groups.has(key)) groups.set(key, { key, name, records: [] });
    groups.get(key)!.records.push(record);
  }
  return Array.from(groups.values()).sort((a, b) => a.name.localeCompare(b.name, "zh-CN", { numeric: true }));
});
const selectedGroupKey = computed(() => draft.value ? recipeCatalogKey(draft.value) : "");
const { page, pageSize, total, pageItems: pagedSoftwareGroups } = usePagination(
  () => softwareGroups.value, [search, statusFilter],
);
pageSize.value = 50;
const selectedGroupName = computed(() => softwareNames[selectedGroupKey.value] || draft.value?.name || "新建软件");
const groupRecords = computed(() => records.value.filter(record => recipeCatalogKey(record.draft) === selectedGroupKey.value));
const versionChoices = computed(() => Array.from(new Set(groupRecords.value.map(item => item.draft.version))));
const runtimeChoices = computed(() => Array.from(new Set(groupRecords.value.filter(item => item.draft.version === draft.value?.version).flatMap(item => item.draft.runtimeModes))));
const sourceChoices = computed(() => {
  const variants = groupRecords.value.filter(item => item.draft.version === draft.value?.version && item.draft.runtimeModes.includes(viewingRuntime.value));
  return Array.from(new Set(variants.flatMap(item => item.draft.sourceModes))).filter(source =>
    source === "ARCHIVE" || (viewingRuntime.value === "DOCKER" ? source === "REGISTRY" : source !== "REGISTRY"),
  );
});
const visibleSteps = computed(() => (commands.value?.steps || [])
  .map((item, index) => ({ item, index }))
  .filter(({ item }) => (!item.runtimeMode || item.runtimeMode === viewingRuntime.value) &&
    (!item.sourceModes?.length || item.sourceModes.includes(viewingSource.value))),
);
const variableLabels: Record<string, string> = {
  OPS_INSTALL_ROOT: "本次安装目录", OPS_DATA_ROOT: "数据目录", OPS_ARTIFACT_PATH: "已核验的离线包路径",
  OPS_SOFTWARE_ID: "软件实例标识", OPS_VERSION: "软件版本", OPS_ACCOUNT: "执行账号",
  OPS_HOST_PORT: "监听端口", OPS_BIND_ADDRESS: "监听地址", OPS_BINARY: "安装后的程序路径",
  OPS_PACKAGE_ARGUMENTS: "已确认的软件包名、版本或离线包路径", OPS_SERVICE_UNIT: "服务单元名",
  OPS_SERVICE_UNITS: "本次操作的服务与 socket", OPS_UNIT_PATH: "服务单元文件路径", OPS_OWNED_PATHS: "已确认归属的数据与配置路径",
  OPS_PLATFORM: "系统架构", OPS_IMAGE: "镜像地址", OPS_IMAGE_ID: "已核验的镜像标识", OPS_IMAGE_DIGEST: "固定镜像摘要",
  OPS_CONTAINER_ID: "本次容器标识", OPS_CONTAINER_NAME: "本次容器名称", OPS_MEMORY_MIB: "内存限额（MiB）",
  OPS_PUBLISH_ARGUMENTS: "配置的端口映射", OPS_OWNERSHIP_ARGUMENTS: "Ops 所有权标签",
  OPS_CONTAINER_ARGUMENTS: "配置挂载与容器入口参数", OPS_PROGRAM_ARGUMENTS: "容器内程序启动参数",
  OPS_BUILD_ROOT: "编译暂存目录", OPS_BUILD_JOBS: "编译并行数", OPS_NODE_ROOT: "账号的 Node 环境目录",
  OPS_NODE_VERSION: "Node 版本", OPS_NODE_ACTION: "当前环境操作", OPS_NODE_PACKAGE: "包名称",
  OPS_NODE_MATERIAL: "已核验的材料路径", OPS_NODE_JOB: "本次任务标识", OPS_NODE_DEFAULT: "默认版本或删除选项",
  OPS_NODE_CONFIGS: "独立的 npm 配置目录",
};
const availableVariables = computed(() => {
  const names = builtin.value
    ? [...new Set(visibleSteps.value.flatMap(({ item }) => `${item.script} ${item.workingDirectory || ""}`.match(/OPS_[A-Z0-9_]+/g) || []))]
    : ["OPS_INSTALL_ROOT", "OPS_DATA_ROOT", "OPS_ARTIFACT_PATH", "OPS_SOFTWARE_ID", "OPS_VERSION", "OPS_ACCOUNT", "OPS_HOST_PORT", "OPS_BIND_ADDRESS"];
  return names.map(name => ({ name, label: variableLabels[name] || (/^OPS_PART_\d+$/.test(name) ? "已核验并解包的材料路径" : "执行时由所选安装方案提供") }));
});
const stepContext = computed(() => ({ runtimeMode: viewingRuntime.value as "NATIVE" | "DOCKER", sourceMode: viewingSource.value }));
const phaseSteps = computed(() => visibleSteps.value.filter(entry => operationOf(entry.item.phase) === activePhase.value));
const displaySteps = computed(() => commandsOnly.value ? phaseSteps.value.filter(entry => entry.item.kind !== "SYSTEM") : phaseSteps.value);
const hasSystemSteps = computed(() => phaseSteps.value.some(entry => entry.item.kind === "SYSTEM"));
const phaseChoices = computed(() => Object.entries(operations) as [keyof typeof operations, string][]);
const phasePosition = computed(() => phaseSteps.value.findIndex(entry => entry.index === activeStep.value));
const totalSteps = computed(() => commands.value?.steps.length || 0);
function selectPhase(phase: CommandStep["phase"]) {
  activePhase.value = operationOf(phase);
  activeStep.value = -1;
}
async function selectGroup(key: string) {
  const group = softwareGroups.value.find(item => item.key === key);
  const record = group?.records.find(item => item.id === selectedId.value) || group?.records[0];
  if (record) await edit(record);
}
async function selectVariant(field: "version" | "runtime" | "source", value: string) {
  const candidates = groupRecords.value.filter(item => field === "version" ? item.draft.version === value : field === "runtime" ? item.draft.runtimeModes.includes(value) : item.draft.sourceModes.includes(value));
  const runtime = field === "runtime" ? value : viewingRuntime.value, source = field === "source" ? value : viewingSource.value;
  const best = candidates.find(item => (field === "version" || item.draft.version === draft.value?.version) && item.draft.runtimeModes.includes(runtime) && item.draft.sourceModes.includes(source)) || candidates.find(item => item.draft.version === draft.value?.version && item.draft.runtimeModes.includes(runtime)) || candidates[0];
  if (!best || busy.value) return;
  if (best.id !== selectedId.value) {
    if (!await canDiscard()) return;
    loadDraft(best);
  }
  if (draft.value?.runtimeModes.includes(runtime)) viewingRuntime.value = runtime;
  viewingSource.value = sourceChoices.value.includes(source) ? source : sourceChoices.value.find(item => item !== "ARCHIVE") || sourceChoices.value[0] || "ONLINE";
  selectPhase(activePhase.value);
}

function emptyStep(phase: CommandStep["phase"]): CommandStep {
  return { id: `command-${crypto.randomUUID()}`, kind: "COMMAND", phase, name: phases[phase], script: "", runAs: "ROOT", timeoutSeconds: phase === "VERIFY" ? 60 : 600, workingDirectory: "" };
}
function newDefinition(): ServiceRecipe {
  return { key: "custom-", name: "", kind: "TOOL", version: "1.0.0", revision: "1", platform: "linux/amd64", image: "", digest: "", imageId: "", officialUrl: "", runtimeModes: ["NATIVE"], sourceModes: ["ONLINE"], port: 0,
    commands: { flowVersion: 1, kind: "TOOL", locator: "", distributions: [], steps: [emptyStep("INSTALL"), emptyStep("VERIFY"), emptyStep("UNINSTALL")] } };
}
function resetView() {
  if (!draft.value) return;
  viewingRuntime.value = draft.value.runtimeModes.includes("NATIVE") ? "NATIVE" : draft.value.runtimeModes[0] || "NATIVE";
  const sources = draft.value.sourceModes.filter(source => viewingRuntime.value === "DOCKER" ? ["REGISTRY", "ARCHIVE"].includes(source) : source !== "REGISTRY");
  viewingSource.value = sources.find(source => source !== "ARCHIVE") || sources[0] || "ONLINE";
  selectPhase("INSTALL");
}
function loadDraft(record?: CommandTemplate, example = false) {
  selectedId.value = record?.id || "";
  editingVersion.value = record?.version || "";
  draft.value = record ? clone(record.draft) : newDefinition();
  if (draft.value.commands && !draft.value.commands.builtin) draft.value.commands.distributions ||= [];
  if (example) {
    Object.assign(draft.value, { key: "custom-command-demo", name: "命令示例", version: "1.0.0" });
    draft.value.commands!.locator = "${OPS_INSTALL_ROOT}/bin/demo";
    draft.value.commands!.steps = [
      { ...emptyStep("INSTALL"), script: "printf '%s\\n' '#!/bin/sh' 'printf \"demo 1.0.0\\n\"' > \"$OPS_INSTALL_ROOT/bin/demo\"\nchmod 755 \"$OPS_INSTALL_ROOT/bin/demo\"" },
      { ...emptyStep("VERIFY"), script: '"$OPS_INSTALL_ROOT/bin/demo"' },
      { ...emptyStep("UNINSTALL"), script: 'rm -f -- "$OPS_INSTALL_ROOT/bin/demo"' },
    ];
  }
  baseline.value = JSON.stringify(draft.value);
  resetView();
  commandsOnly.value = false;
  preview.value = false;
  error.value = undefined;
  feedback.value = "";
}
async function canDiscard(title = "切换配置") {
  if (!dirty.value) return true;
  try { await ElMessageBox.confirm("放弃未保存的命令配置？", title, { confirmButtonText: "放弃修改", cancelButtonText: "继续编辑" }); return true; } catch { return false; }
}
async function edit(record?: CommandTemplate, example = false) {
  if (busy.value || !await canDiscard()) return;
  loadDraft(record, example);
  editorOpen.value = true;
}
async function closeEditor(done?: () => void) {
  if (busy.value || !await canDiscard("关闭配置")) return;
  draft.value = undefined;
  error.value = undefined;
  if (done) done();
  else editorOpen.value = false;
}
async function createFromExample() {
  const example = examples.value.find(item => item.key === selectedExample.value);
  selectedExample.value = "";
  if (!example || busy.value || !await canDiscard()) return;
  loadDraft();
  draft.value = clone(example);
  if (draft.value.commands) { draft.value.commands.distributions ||= []; draft.value.commands.flowVersion = 1; }
  let suffix = 2;
  while (records.value.some(item => item.draft.key === draft.value!.key))
    draft.value.key = `${example.key.slice(0, 58)}-${suffix++}`;
  baseline.value = JSON.stringify(draft.value);
  resetView();
  editorOpen.value = true;
}
async function act(action: () => Promise<void>) {
  if (busy.value) return;
  busy.value = true;
  error.value = undefined;
  feedback.value = "";
  try { await action(); } catch (cause) {
    error.value = cause instanceof ApiError ? { message: cause.message, code: cause.code, traceId: cause.traceId, at: cause.timestamp } : { message: cause instanceof Error ? cause.message : "操作未完成，请重试" };
  } finally { busy.value = false; }
}
async function refresh() {
  let loaded = false;
  await act(async () => {
    const [templates, definitions, recipes] = await Promise.all([
      request<CommandTemplate[]>("/command-templates"), request<ServiceRecipe[]>("/command-template-examples"), request<ServiceRecipe[]>("/software-recipes"),
    ]);
    records.value = templates;
    examples.value = definitions;
    catalog.value = recipes;
    loaded = true;
  });
  return loaded;
}
async function reloadSelected() { if (await refresh()) await edit(selected.value); }
function templateStatus(item: CommandTemplate) {
  if (!item.published) return "草稿";
  const comparable = (value: ServiceRecipe) => JSON.stringify({ ...value, revision: "", commands: value.commands && { ...value.commands, revision: 0 } });
  return comparable(item.draft) === comparable(item.published) ? "已发布" : "有待发布更改";
}
function changeKind() {
  if (!draft.value || !commands.value) return;
  const docker = commands.value.kind === "DOCKER";
  draft.value.kind = commands.value.kind === "TOOL" ? "TOOL" : "SERVICE";
  draft.value.runtimeModes = [docker ? "DOCKER" : "NATIVE"];
  if (draft.value.sourceModes[0] !== "ARCHIVE") draft.value.sourceModes = [docker ? "REGISTRY" : "ONLINE"];
  if (commands.value.kind === "TOOL") draft.value.port = 0;
  changeSource();
}
function changeSource() {
  if (!draft.value || !commands.value || builtin.value) return;
  for (const item of commands.value.steps) {
    if (item.runtimeMode) item.runtimeMode = draft.value.runtimeModes[0] as "NATIVE" | "DOCKER";
    if (item.sourceModes?.length) item.sourceModes = [draft.value.sourceModes[0]!];
  }
  resetView();
}
async function addStep() {
  const items = commands.value?.steps;
  if (!items || items.length >= 80) return;
  const added = emptyStep(activePhase.value);
  added.name = "新步骤";
  added.runtimeMode = viewingRuntime.value as "NATIVE" | "DOCKER";
  added.sourceModes = [viewingSource.value];
  const last = phaseSteps.value.at(-1);
  const index = activeStep.value >= 0
    ? activeStep.value + (step.value?.action?.endsWith("_CONFIRM") ? 0 : 1)
    : last ? last.index + (last.item.action?.endsWith("_CONFIRM") ? 0 : 1) : items.length;
  items.splice(index, 0, added);
  activeStep.value = index;
  await nextTick();
  stepNameInput.value?.[0]?.focus();
}
const stepNameInput = ref<HTMLInputElement[]>();
function toggleCommandsOnly() {
  commandsOnly.value = !commandsOnly.value;
  if (commandsOnly.value && step.value?.kind === "SYSTEM") activeStep.value = -1;
}
function removeStep() {
  if (!step.value || step.value.kind === "SYSTEM") return;
  commands.value?.steps.splice(activeStep.value, 1);
  selectPhase(activePhase.value);
}
function canMoveStep(offset: number) {
  const next = phaseSteps.value[phasePosition.value + offset]?.item;
  return !!step.value && !!next && step.value.kind !== "SYSTEM" && next.action !== "UNINSTALL_PREPARE" && !next.action?.endsWith("_CONFIRM");
}
function moveStep(offset: number) {
  const items = commands.value?.steps, next = phaseSteps.value[phasePosition.value + offset]?.index;
  if (!items || next === undefined || !canMoveStep(offset)) return;
  [items[activeStep.value], items[next]] = [items[next]!, items[activeStep.value]!];
  activeStep.value = next;
}
function validate() {
  const value = draft.value, definition = commands.value;
  if (!value || !definition) return "请新建或选择配置";
  const extensionError = extensions.value?.validationError();
  if (extensionError) return extensionError;
  if (!definition.builtin && !/^custom-[a-z0-9][a-z0-9-]{0,55}$/.test(value.key)) return "软件标识须以 custom- 开头，后接小写字母、数字或连字符";
  if (value.catalogKey && !/^[a-z][a-z0-9-]{0,63}$/.test(value.catalogKey)) return "所属软件须以小写字母开头，仅含小写字母、数字或连字符";
  if (!value.name.trim() || !value.version.trim() || !definition.builtin && !definition.locator.trim()) return "填写软件名称、版本与安装对象";
  if (value.officialUrl && !/^https:\/\//i.test(value.officialUrl)) return "来源地址须为 HTTPS 地址";
  for (const phase of (definition.builtin ? [] : definition.flowVersion === 1 ? ["INSTALL", "UNINSTALL"] : ["INSTALL", "VERIFY", "UNINSTALL"]) as CommandStep["phase"][])
    if (!definition.steps.some(item => item.phase === phase)) return `至少添加一个${phases[phase]}步骤`;
  const invalid = definition.steps.findIndex(item => !item.name.trim() || item.kind !== "SYSTEM" && (!item.script.trim() || !Number.isInteger(item.timeoutSeconds) || item.timeoutSeconds < 1 || item.timeoutSeconds > 3600));
  if (invalid >= 0) {
    const item = definition.steps[invalid]!;
    if (item.runtimeMode) viewingRuntime.value = item.runtimeMode;
    if (item.sourceModes?.length && !item.sourceModes.includes(viewingSource.value)) viewingSource.value = item.sourceModes[0]!;
    activePhase.value = operationOf(item.phase);
    activeStep.value = invalid;
    return `补全第 ${invalid + 1} 步的名称、命令与超时（1–3600 秒）`;
  }
  return "";
}
function checkDraft() {
  const message = validate();
  error.value = message ? { message } : undefined;
  return !message;
}
function updateRecord(record: CommandTemplate) {
  records.value = [record, ...records.value.filter(item => item.id !== record.id)];
  selectedId.value = record.id;
  editingVersion.value = record.version;
  draft.value = clone(record.draft);
  if (draft.value.commands && !draft.value.commands.builtin) draft.value.commands.distributions ||= [];
  baseline.value = JSON.stringify(draft.value);
}
async function persistDraft() {
  const current = selected.value;
  const saved = await request<CommandTemplate>(current ? `/command-templates/${current.id}` : "/command-templates", current ? "PUT" : "POST", {
    ...(current ? { expectedVersion: editingVersion.value } : {}), definition: clone(draft.value!),
  });
  updateRecord(saved);
  return saved;
}
async function save() {
  if (!checkDraft()) return;
  await act(async () => { await persistDraft(); feedback.value = "草稿已保存"; });
}
async function previewPublish() {
  if (!checkDraft()) return;
  preview.value = true;
  await nextTick();
  editorContent.value?.parentElement?.scrollTo(0, 0);
}
async function publish() {
  if (!checkDraft()) return;
  await act(async () => {
    const current = !selected.value || dirty.value ? await persistDraft() : selected.value;
    updateRecord(await request<CommandTemplate>(`/command-templates/${current.id}/publish`, "POST", { expectedVersion: editingVersion.value }));
    preview.value = false;
    feedback.value = "已发布到软件目录";
    emit("changed");
  });
}
async function withdraw() {
  const current = selected.value;
  if (!current?.published || busy.value) return;
  try { await ElMessageBox.confirm(`从软件目录下架“${current.published.name}”？已安装软件和历史任务保留。`, "下架配置", { confirmButtonText: "下架", cancelButtonText: "取消" }); } catch { return; }
  await act(async () => {
    const result = await request<CommandTemplate>(`/command-templates/${current.id}`, "DELETE", { expectedVersion: editingVersion.value });
    records.value = records.value.map(item => item.id === result.id ? result : item);
    editingVersion.value = result.version;
    feedback.value = "已下架";
    emit("changed");
  });
}
function extraAction(command: string) {
  if (command === "withdraw") void withdraw();
  if (command === "demo") void edit(undefined, true);
  if (command === "reload") void reloadSelected();
}
onMounted(refresh);
</script>

<template>
  <section class="recipe-workspace" aria-label="软件配置">
    <div v-if="error && !editorOpen" class="template-error" role="alert">{{ error.message }}</div>
    <div class="library-tools">
      <div class="library-filters">
        <input v-model="search" type="search" aria-label="搜索软件配置" placeholder="搜索软件" />
        <select v-model="statusFilter" aria-label="筛选发布状态">
          <option value="ALL">全部状态</option>
          <option value="PUBLISHED">已发布</option>
          <option value="DRAFT">待发布</option>
        </select>
      </div>
      <div class="library-actions">
        <select v-if="examples.length" v-model="selectedExample" aria-label="从模板创建" :disabled="busy" @change="createFromExample">
          <option value="" disabled>从示例创建</option>
          <option v-for="example in examples" :key="example.key" :value="example.key">{{ example.name }} {{ example.version }} · {{ example.runtimeModes[0] === 'DOCKER' ? 'Docker' : '原生' }} · {{ example.sourceModes[0] === 'ARCHIVE' ? '离线' : '在线' }}</option>
        </select>
        <button class="secondary" aria-label="刷新软件配置" :disabled="busy" @click="refresh">刷新</button>
        <button class="primary" aria-label="新增软件" :disabled="busy" @click="edit()">新增软件</button>
      </div>
    </div>
    <div class="library-list" aria-label="软件配置列表">
      <button v-for="group in pagedSoftwareGroups" :key="group.key" class="software-option" :disabled="busy" @click="selectGroup(group.key)">
        <SoftwareIcon :type="group.key" />
        <span><strong>{{ group.name }}</strong><small>{{ group.records.length }} 个安装方案</small></span>
        <span v-if="group.records.some(item => templateStatus(item) !== '已发布')" class="draft-dot" title="有待发布配置" />
        <span class="entry-arrow" aria-hidden="true">›</span>
      </button>
      <p v-if="!softwareGroups.length" class="library-empty">{{ busy ? '正在加载配置…' : search ? '没有匹配的软件' : '暂无软件配置' }}</p>
    </div>
    <OpsPagination v-model:page="page" v-model:page-size="pageSize" :total="total" label="软件配置分页" />

    <ElDialog
      v-if="active"
      v-model="editorOpen"
      :title="preview ? '发布预览 · ' + selectedGroupName : '软件配置 · ' + selectedGroupName"
      class="command-editor"
      width="min(820px, calc(100vw - 24px))"
      align-center
      append-to-body
      destroy-on-close
      :before-close="closeEditor"
      :show-close="!busy"
      :close-on-press-escape="!busy"
      :close-on-click-modal="!busy"
    >
      <div v-if="draft && commands" ref="editorContent" class="editor-content">
    <div v-if="error" class="template-error" role="alert"><strong>{{ error.message }}</strong><button v-if="error.code === 'VERSION_CONFLICT' && selected" class="quiet" :disabled="busy" @click="reloadSelected">加载最新配置</button><details v-if="error.code"><summary>诊断信息</summary><code>{{ error.code }} · {{ hostTime(error.at) }}</code><code v-if="error.traceId">{{ error.traceId }}</code></details></div>
        <div v-if="selected" class="variant-bar" aria-label="选择安装方案">
          <label>软件版本<select aria-label="软件版本" :value="draft.version" :disabled="busy || preview" @change="selectVariant('version', ($event.target as HTMLSelectElement).value)"><option v-for="version in versionChoices" :key="version" :value="version">{{ versionLabel(version) }}</option></select></label>
          <label>运行方式<select aria-label="运行方式" :value="viewingRuntime" :disabled="busy || preview" @change="selectVariant('runtime', ($event.target as HTMLSelectElement).value)"><option v-for="runtime in runtimeChoices" :key="runtime" :value="runtime">{{ runtime === 'DOCKER' ? 'Docker' : '原生安装' }}</option></select></label>
          <label>安装来源<select aria-label="安装来源" :value="viewingSource" :disabled="busy || preview" @change="selectVariant('source', ($event.target as HTMLSelectElement).value)"><option v-for="source in sourceChoices" :key="source" :value="source">{{ sourceLabels[source] || source }}</option></select></label>
        </div>

        <details v-if="!preview" :open="!selected" class="recipe-settings"><summary><span>方案信息</span><a v-if="draft.officialUrl" :href="draft.officialUrl" target="_blank" rel="noopener noreferrer" @click.stop>来源说明 ↗</a></summary>
          <fieldset :disabled="busy || builtin" class="metadata-grid">
            <label>方案名称<input v-model="draft.name" maxlength="120" placeholder="例如 Eclipse Temurin JDK" /></label>
            <label>软件版本<input v-model="draft.version" maxlength="80" placeholder="例如 21" /></label>
            <label>所属软件<input :value="draft.catalogKey || (builtin ? draft.key : '')" list="command-catalog-groups" @input="draft.catalogKey = ($event.target as HTMLInputElement).value" maxlength="64" placeholder="选择或填写软件标识" spellcheck="false" /><datalist id="command-catalog-groups"><option v-for="item in catalogChoices" :key="item.key" :value="item.key">{{ item.name }}</option></datalist></label>
            <label>配置标识<input v-model="draft.key" :disabled="!!selected" maxlength="63" placeholder="custom-my-tool" spellcheck="false" /></label>
            <label>运行类型<select v-model="commands.kind" @change="changeKind"><option v-for="[key, label] in kindChoices" :key="key" :value="key">{{ label }}</option></select></label>
            <label v-if="builtin">支持来源<input :value="draft.sourceModes.map(source => sourceLabels[source] || source).join('、')" /></label><label v-else>安装来源<select v-model="draft.sourceModes[0]" @change="changeSource"><option :value="commands.kind === 'DOCKER' ? 'REGISTRY' : 'ONLINE'">在线</option><option value="ARCHIVE">离线包</option></select></label>
            <label class="wide-field">安装对象<input v-model="commands.locator" :placeholder="commands.kind === 'TOOL' ? '${OPS_INSTALL_ROOT}/bin/tool' : commands.kind === 'SYSTEMD' ? 'my-service-${OPS_SOFTWARE_ID}.service' : 'my-container-${OPS_SOFTWARE_ID}'" spellcheck="false" maxlength="300" /></label>
            <label>来源地址<input v-model="draft.officialUrl" type="url" placeholder="https://example.com/install" spellcheck="false" /></label>
            <label>系统架构<select v-model="draft.platform"><option value="linux/amd64">Linux amd64</option></select></label>
            <label v-if="commands.kind !== 'TOOL'">程序路径<input v-model="commands.binaryPath" placeholder="可选" spellcheck="false" maxlength="300" /></label>
            <label v-if="commands.kind !== 'TOOL'">监听端口<input v-model.number="draft.port" type="number" min="0" max="65535" placeholder="0 表示无需监听" /></label>
            <label>建议内存（MiB）<input :value="draft.defaultMemoryMiB || ''" type="number" min="128" max="16384" placeholder="默认 512" @input="draft.defaultMemoryMiB = ($event.target as HTMLInputElement).value ? Number(($event.target as HTMLInputElement).value) : undefined" /></label>
            <label v-if="commands.kind !== 'TOOL'">默认监听地址<input v-model="draft.defaultBindAddress" :placeholder="draft.web ? '留空默认 0.0.0.0' : '留空使用主机地址'" maxlength="45" spellcheck="false" /></label>
            <div class="distribution-field"><span>适用系统 <small>不选择则不限制</small></span><div class="distribution-options"><label v-for="distribution in distributions" :key="distribution"><input v-model="commands.distributions" type="checkbox" :value="distribution" />{{ distribution }}</label></div></div>
          </fieldset>
        </details>

        <section v-if="!preview" class="command-workbench" aria-label="命令步骤编辑器">
          <div class="step-toolbar">
            <nav class="phase-tabs" aria-label="命令阶段">
              <button v-for="[phase, label] in phaseChoices" :key="phase" :class="{ active: activePhase === phase }" :aria-pressed="activePhase === phase" :disabled="busy" @click="selectPhase(phase)">{{ label }}</button>
            </nav>
            <div class="step-actions">
              <button v-if="hasSystemSteps" class="quiet" :disabled="busy" :aria-pressed="commandsOnly" @click="toggleCommandsOnly">{{ commandsOnly ? '查看完整流程' : '仅看命令' }}</button>
              <button class="secondary compact-button" :disabled="busy || totalSteps >= 80" @click="addStep">添加步骤</button>
            </div>
          </div>
          <div v-for="(entry, order) in displaySteps" :key="entry.item.id || entry.index" class="step-row">
            <button
              :class="['step-option', { active: activeStep === entry.index, automatic: entry.item.kind === 'SYSTEM' }]"
              :aria-expanded="activeStep === entry.index"
              :disabled="busy"
              @click="activeStep = activeStep === entry.index ? -1 : entry.index"
            >
              <span v-if="!commandsOnly" class="step-number">{{ order + 1 }}</span>
              <strong>{{ entry.item.name || '未命名步骤' }}</strong>
              <small v-if="entry.item.kind === 'SYSTEM'">平台处理</small>
              <small v-else-if="entry.item.phase === 'DELETE_DATA'">删除数据时执行</small>
              <span class="step-arrow" aria-hidden="true">›</span>
            </button>
            <div v-if="activeStep === entry.index && step" class="step-body">
              <RecipeSystemStepDetails v-if="step.kind === 'SYSTEM'" :recipe="draft" :step="step" :context="stepContext" />
              <template v-else>
                <label>步骤名称<input ref="stepNameInput" v-model="step.name" maxlength="120" :disabled="busy" /></label>
                <label>步骤命令<textarea v-model="step.script" spellcheck="false" maxlength="16384" :disabled="busy" placeholder="输入 Bash 命令" /></label>
                <div class="step-controls">
                  <details class="step-advanced">
                    <summary>高级设置</summary>
                    <fieldset :disabled="busy" class="metadata-grid">
                      <label>执行账号<select v-model="step.runAs" aria-label="执行账号"><option value="ROOT">管理员（sudo）</option><option value="ACCOUNT">所选服务器账号</option></select></label>
                      <label>超时（秒）<input v-model.number="step.timeoutSeconds" type="number" min="1" max="3600" /></label>
                      <label v-if="activePhase === 'INSTALL'">步骤用途<select v-model="step.phase"><option value="INSTALL">执行命令</option><option value="VERIFY">检查结果</option></select></label>
                      <label v-else>执行条件<select v-model="step.phase"><option value="UNINSTALL">卸载时执行</option><option value="DELETE_DATA">勾选删除数据时执行</option></select></label>
                      <label class="wide-field">工作目录<input v-model="step.workingDirectory" placeholder="留空使用默认目录" spellcheck="false" maxlength="300" /></label>
                    </fieldset>
                  </details>
                  <div class="step-actions">
                    <template v-if="!commandsOnly">
                      <button class="quiet" :disabled="busy || !canMoveStep(-1)" @click="moveStep(-1)">上移步骤</button>
                      <button class="quiet" :disabled="busy || !canMoveStep(1)" @click="moveStep(1)">下移步骤</button>
                    </template>
                    <button class="quiet danger" :disabled="busy" @click="removeStep">删除步骤</button>
                  </div>
                </div>
              </template>
            </div>
          </div>
          <p v-if="!displaySteps.length" class="library-empty">{{ commandsOnly ? '当前阶段没有命令' : '当前阶段没有步骤' }}</p>
        </section>
        <section v-else class="publish-preview" aria-label="发布预览">
          <section v-for="(label, operation) in operations" :key="operation">
            <h4>{{ label }}</h4>
            <details v-for="({ item }, index) in visibleSteps.filter(entry => operationOf(entry.item.phase) === operation)" :key="item.id || index" class="preview-step">
              <summary>
                <strong>{{ index + 1 }}. {{ item.name }}</strong>
                <small>{{ item.kind === 'SYSTEM' ? '平台处理' : item.runAs === 'ROOT' ? '管理员' : '所选账号' }}<template v-if="item.phase === 'DELETE_DATA'"> · 删除数据时执行</template></small>
              </summary>
              <pre v-if="item.kind !== 'SYSTEM'">{{ item.script }}</pre>
              <RecipeSystemStepDetails v-else :recipe="draft" :step="item" :context="stepContext" />
            </details>
          </section>
        </section>
        <SoftwareDefinitionExtensions ref="extensions" :definition="draft" :disabled="busy" :preview="preview" />
        <details v-if="!preview && availableVariables.length" class="variables">
          <summary>可用变量</summary>
          <dl><template v-for="variable in availableVariables" :key="variable.name"><dt>{{ variable.name }}</dt><dd>{{ variable.label }}</dd></template></dl>
        </details>
      </div>
      <template #footer>
        <div class="editor-footer">
          <span class="editor-status" role="status">{{ dirty ? '未保存' : feedback || (selected ? templateStatus(selected) : '新配置') }}</span>
          <div class="step-actions">
            <template v-if="preview">
              <button class="secondary" :disabled="busy" @click="preview = false">返回编辑</button>
              <button class="primary" :disabled="busy" @click="publish">确认发布</button>
            </template>
            <template v-else>
              <ElDropdown trigger="click" @command="extraAction">
                <button class="quiet" aria-label="更多配置操作" :disabled="busy">⋯</button>
                <template #dropdown>
                  <ElDropdownMenu>
                    <ElDropdownItem command="reload" :disabled="busy || !selected">重新载入</ElDropdownItem>
                    <ElDropdownItem command="demo" :disabled="busy">创建演示工具</ElDropdownItem>
                    <ElDropdownItem v-if="selected?.published" command="withdraw" divided :disabled="busy">下架方案</ElDropdownItem>
                  </ElDropdownMenu>
                </template>
              </ElDropdown>
              <button class="secondary" :disabled="busy" @click="save">保存草稿</button>
              <button class="primary" :disabled="busy" @click="previewPublish">预览发布</button>
            </template>
          </div>
        </div>
      </template>
    </ElDialog>
  </section>
</template>

<style scoped>
.recipe-workspace, .editor-content { display: grid; gap: 16px; min-width: 0; }
.library-tools, .library-filters, .library-actions, .step-toolbar, .step-actions, .editor-footer {
  display: flex; align-items: center; flex-wrap: wrap; gap: 8px;
}
.library-tools, .step-toolbar, .editor-footer { justify-content: space-between; }
.library-filters input { width: min(280px, 100%); }
.library-tools select { width: auto; max-width: min(240px, 100%); }
.library-list { display: grid; grid-template-columns: repeat(auto-fill, minmax(min(100%, 228px), 1fr)); gap: 10px; }
.software-option {
  display: grid; grid-template-columns: auto minmax(0, 1fr) auto auto; align-items: center;
  gap: 12px; min-width: 0; text-align: left; background: white; border: 1px solid var(--border);
  border-radius: 7px; padding: 13px; color: #22374e;
}
.software-option:hover { border-color: #8eb5c6; background: #f7fbfc; }
.software-option strong, .software-option small { display: block; overflow-wrap: anywhere; }
.software-option small { margin-top: 4px; color: var(--muted); font-size: 12px; }
.entry-arrow { grid-column: 4; color: #5e8094; font-size: 20px; }
.draft-dot { width: 7px; height: 7px; background: #c98530; border-radius: 50%; }
.library-empty { margin: 0; padding: 16px; color: var(--muted); }
.editor-status { color: var(--muted); font-size: 13px; }
.variant-bar, .metadata-grid {
  display: grid; grid-template-columns: repeat(3, minmax(0, 1fr)); gap: 12px;
}
.variant-bar { padding: 12px; background: #f4f7fa; border-radius: 6px; }
.editor-content label { display: grid; gap: 5px; min-width: 0; color: #506577; font-size: 12px; }
.editor-content input, .editor-content select { min-width: 0; width: 100%; }
.editor-content fieldset { padding: 0; margin: 10px 0 0; border: 0; min-width: 0; }
.wide-field { grid-column: span 2; }
.distribution-field { grid-column: 1 / -1; font-size: 12px; }
.distribution-options { display: flex; flex-wrap: wrap; gap: 6px 12px; margin-top: 8px; }
.distribution-options label { display: flex; align-items: center; }
.distribution-options input { width: auto; }
.recipe-settings > summary, .variables > summary, .step-advanced > summary, .preview-step > summary {
  cursor: pointer; color: #425d73; font-size: 13px;
}
.recipe-settings > summary a { margin-left: 12px; }
.command-workbench { border: 1px solid var(--border); border-radius: 7px; }
.step-toolbar { padding: 8px 10px; background: #f7f9fc; border-radius: 7px 7px 0 0; }
.phase-tabs { display: flex; gap: 4px; }
.phase-tabs button { background: transparent; border: 1px solid transparent; border-radius: 4px; color: #53687d; padding: 7px 9px; }
.phase-tabs button.active { background: #e7f2f5; color: #17637a; font-weight: 600; }
.step-row { border-top: 1px solid var(--border); }
.step-option {
  display: flex; align-items: center; text-align: left; gap: 10px; width: 100%; min-height: 42px;
  border: 0; padding: 8px 12px; background: transparent; color: #263d53; border-radius: 0;
}
.step-option:hover, .step-option.active { background: #edf5f8; }
.step-option.automatic { color: #586e81; }
.step-option strong { flex: 1; min-width: 0; overflow-wrap: anywhere; font-size: 13px; font-weight: 500; }
.step-option small { font-size: 11px; color: #5e7487; }
.step-number {
  display: grid; place-items: center; width: 23px; height: 23px; flex-shrink: 0;
  background: #e9f0f5; border-radius: 4px; font: 11px var(--mono);
}
.step-arrow { color: #688599; }
.step-option.active .step-arrow { transform: rotate(90deg); }
.step-body { display: grid; min-width: 0; gap: 12px; padding: 12px; background: #f9fbfd; }
.step-body textarea {
  min-height: 136px; width: 100%; resize: vertical; padding: 12px; white-space: pre; tab-size: 2;
  background: #142638; color: #e4edf5; border: 1px solid #294359; border-radius: 5px; font: 13px/1.6 var(--mono);
}
.step-controls .step-actions { justify-content: flex-end; margin-top: 8px; }
.publish-preview { display: grid; gap: 12px; }
.publish-preview h4 { margin: 0 0 8px; }
.preview-step { border-top: 1px solid var(--border); padding: 9px 0; }
.preview-step summary small { margin-left: 8px; color: var(--muted); }
.preview-step pre, .variables dl { margin: 8px 0 0; padding: 12px; background: #f3f6f9; border-radius: 5px; overflow-wrap: anywhere; white-space: pre-wrap; }
.variables dl { display: grid; grid-template-columns: minmax(0, 1fr) minmax(0, 1fr); gap: 6px 12px; font-size: 12px; }
.variables dd { margin: 0; }
.template-error { padding: 10px; background: #fff5f3; border: 1px solid #f2d1ca; border-radius: 5px; color: #943c31; }
.template-error details { margin-top: 6px; }
.template-error code { display: block; overflow-wrap: anywhere; }
@media (max-width: 550px) {
  .variant-bar, .metadata-grid { grid-template-columns: 1fr; }
  .wide-field { grid-column: auto; }
  .library-filters, .library-actions { width: 100%; }
  .library-filters input, .library-actions select { flex: 1; min-width: 0; }
  .editor-footer .step-actions { margin-left: auto; }
}
</style>

<style>
.command-editor.el-dialog { display: flex; flex-direction: column; max-height: calc(100dvh - 24px); padding: 0; }
.command-editor > .el-dialog__header { flex: 0 0 auto; padding: 16px 48px 14px 18px; border-bottom: 1px solid var(--border); margin: 0; }
.command-editor > .el-dialog__body { min-height: 0; overflow-y: auto; overscroll-behavior: contain; padding: 16px 18px; }
.command-editor > .el-dialog__footer { flex: 0 0 auto; border-top: 1px solid var(--border); padding: 10px 18px; }
</style>
