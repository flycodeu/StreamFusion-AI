<script setup lang="ts">
import { computed, nextTick, onMounted, onUnmounted, ref, watch } from "vue";
import { ElDrawer } from "element-plus";
import { ApiError, request } from "./api";
import {
  isProjectBuildService, moduleKinds, type ApplicationProject, type ProjectEnvironment, type ProjectInstanceSource,
  type ProjectModule, type ProjectReleaseSource, type ProjectServiceRegistration,
} from "./applicationProjects";
import type { Host } from "./hosts";
import type { LogSelection } from "./jobs";
import { requestAllPages } from "./listPages";
import { moduleJenkinsPath, jenkinsState, type JenkinsBuild, type JenkinsInstanceContext, type JenkinsMonitor } from "./projectJenkins";
import { softwareState, type SoftwareInstance } from "./software";
import ProjectJenkinsBuild from "./ProjectJenkinsBuild.vue";

interface ModuleRow {
  module: ProjectModule;
  environment?: ProjectEnvironment;
  registration?: ProjectServiceRegistration;
}

const props = defineProps<{ hosts: Host[]; source?: ProjectInstanceSource }>();
const emit = defineEmits<{ "source-used": []; logs: [LogSelection]; release: [ProjectReleaseSource]; verify: [ProjectReleaseSource]; maintain: [source: ProjectInstanceSource]; error: [unknown] }>();
const lifetime = new AbortController();
const projects = ref<ApplicationProject[]>([]);
const projectId = ref(""), projectSearch = ref(""), moduleSearch = ref(""), environmentFilter = ref("");
const queryDraft = ref(""), environmentDraft = ref("");
const focusedModuleId = ref("");
const listElement = ref<HTMLElement>();
const loading = ref(false), pageError = ref("");
const project = computed(() => projects.value.find(item => item.id === projectId.value));
const projectOptions = computed(() => projects.value.filter(item =>
  item.name.toLowerCase().includes(projectSearch.value.trim().toLowerCase()),
));
const rows = computed<ModuleRow[]>(() => {
  if (!project.value) return [];
  const bindings = new Map<string, ModuleRow[]>();
  const modules = new Map(project.value.modules.map(module => [module.id, module]));
  for (const environment of project.value.environments || []) {
    for (const registration of environment.deployments) {
      const module = modules.get(registration.moduleId);
      if (!module) continue;
      const row = { module, environment, registration };
      const existing = bindings.get(module.id);
      if (existing) existing.push(row);
      else bindings.set(module.id, [row]);
    }
  }
  const bound = project.value.modules.flatMap(module => bindings.get(module.id) || []);
  return [...bound, ...project.value.modules.filter(module => !bindings.has(module.id)).map(module => ({ module }))];
});
const hostsById = computed(() => new Map(props.hosts.map(host => [host.id, host.name])));
const runtimeIds = computed(() => [...new Set(rows.value.flatMap(row =>
  row.registration?.runtimeSoftwareId ? [row.registration.runtimeSoftwareId] : [],
))]);
const runtimes = ref(new Map<string, SoftwareInstance>());
const visibleRows = computed(() => {
  const query = moduleSearch.value.trim().toLowerCase();
  return rows.value.filter(({ module, environment }) =>
    (!environmentFilter.value || environment?.id === environmentFilter.value) &&
    (!query || module.name.toLowerCase().includes(query) || module.directory.toLowerCase().includes(query)),
  ).map(row => {
    const runtime = runtimes.value.get(row.registration?.runtimeSoftwareId || "");
    const buildService = !!runtime && isProjectBuildService(row.module.name, runtime);
    return { ...row, runtime, buildService, record: records.value.get(row.registration?.id || row.module.id),
      state: runtime && !buildService ? softwareState(runtime) : undefined };
  });
});
const runtimeRefresh = ref(0), runtimeLoading = ref(false), runtimeError = ref("");
const panel = ref<{ context: JenkinsInstanceContext; monitor: JenkinsMonitor }>();
const records = ref(new Map<string, JenkinsBuild>());
const opening = ref("");
const setupNotice = ref<{ message: string; row: ModuleRow }>();
let selectionRequest: AbortController | undefined;

function searchModules() {
  moduleSearch.value = queryDraft.value;
  environmentFilter.value = environmentDraft.value;
}
function clearFilters() {
  queryDraft.value = environmentDraft.value = "";
  searchModules();
}

function describe(error: unknown, fallback: string) {
  if (error instanceof ApiError && error.code === "AUTH_REQUIRED") emit("error", error);
  return error instanceof ApiError
    ? [error.message, error.code, error.traceId, error.timestamp].filter(Boolean).join(" · ")
    : fallback;
}

function selectProject(id: string) {
  projectId.value = id;
  clearFilters();
  focusedModuleId.value = "";
  panel.value = undefined;
  selectionRequest?.abort();
  opening.value = "";
  setupNotice.value = undefined;
}
function useSource() {
  const source = props.source;
  if (!source || !projects.value.length) return;
  if (projects.value.some(item => item.id === source.projectId)) {
    selectProject(source.projectId);
    focusedModuleId.value = source.moduleId;
    void nextTick(() => listElement.value?.querySelector("[data-focused=true]")?.scrollIntoView({ block: "nearest" }));
  }
  emit("source-used");
}
async function refresh() {
  if (loading.value) return;
  loading.value = true;
  pageError.value = "";
  try {
    const data = await requestAllPages<ApplicationProject>("/application-projects?archived=all", lifetime.signal);
    if (lifetime.signal.aborted) return;
    projects.value = data;
    if (!project.value) selectProject((data.find(item => !item.archived) || data[0])?.id || "");
    useSource();
    runtimeRefresh.value++;
  } catch (error) {
    if (!lifetime.signal.aborted) pageError.value = describe(error, "无法读取项目发布清单");
  } finally { loading.value = false; }
}
function maintain(row: ModuleRow) {
  if (!project.value || project.value.archived) return;
  emit("maintain", {
    projectId: project.value.id, moduleId: row.module.id,
    ...(row.registration && { serviceInstanceId: row.registration.id }),
  });
}
function openServiceLog(runtime: SoftwareInstance) {
  emit("logs", { softwareId: runtime.id, title: runtime.name });
}
async function openBuild(row: ModuleRow, action: JenkinsInstanceContext["action"] | "record") {
  const current = project.value;
  if (!current || action === "qa" && current.archived) return;
  const id = row.registration?.id || row.module.id;
  if (opening.value === id) return;
  selectionRequest?.abort();
  const controller = selectionRequest = new AbortController();
  opening.value = id;
  setupNotice.value = undefined;
  pageError.value = "";
  try {
    const path = moduleJenkinsPath(current.id, row.module.id, row.registration?.id);
    const monitor = await request<JenkinsMonitor>(path + "/jenkins-monitor", "GET", undefined, controller.signal);
    if (controller.signal.aborted) return;
    if (!monitor.configured) {
      setupNotice.value = { row, message: monitor.reason || "请在项目维护中设置 Jenkins 连接和模块类型。" };
      return;
    }
    if (action === "record") {
      const latest = await request<JenkinsBuild>(path + "/jenkins-build", "GET", undefined, controller.signal);
      if (!controller.signal.aborted) recordBuild(id, latest);
      return;
    }
    panel.value = {
      monitor, context: {
        action, readOnly: current.archived, moduleId: row.module.id, moduleName: row.module.name,
        serviceInstanceId: row.registration?.id, environmentId: row.environment?.id, environmentName: row.environment?.name,
      },
    };
  } catch (error) {
    if (!controller.signal.aborted) pageError.value = describe(error, "读取 QA 配置失败，请重试。");
  } finally {
    if (selectionRequest === controller) opening.value = "";
  }
}
function recordBuild(id: string, build: JenkinsBuild) {
  records.value.set(id, build);
}
function openRelease(row: ModuleRow, action: "release" | "verify" = "release") {
  if (!project.value || project.value.archived) return;
  if (!row.registration?.managedInstanceId || !row.environment) {
    setupNotice.value = { row, message: "请在项目维护中选择该环境的上线目标。" };
    return;
  }
  const source: ProjectReleaseSource = {
    projectId: project.value.id, moduleId: row.module.id, moduleName: row.module.name,
    serviceInstanceId: row.registration.id, hostId: row.registration.hostId,
    environmentId: row.environment.id, environmentName: row.environment.name,
    managedInstanceId: row.registration.managedInstanceId,
  };
  if (action === "verify") emit("verify", source);
  else emit("release", source);
}
watch([runtimeIds, runtimeRefresh], async ([ids], _, cleanup) => {
  const controller = new AbortController();
  cleanup(() => controller.abort());
  runtimes.value = new Map();
  runtimeError.value = "";
  runtimeLoading.value = ids.length > 0;
  if (!ids.length) return;
  try {
    const available = await requestAllPages<SoftwareInstance>("/software-instances", controller.signal);
    if (!controller.signal.aborted) {
      const referenced = new Set(ids);
      runtimes.value = new Map(available.filter(item => referenced.has(item.id)).map(item => [item.id, item]));
    }
  } catch (error) {
    if (!controller.signal.aborted) runtimeError.value = describe(error, "无法读取关联运行对象，请刷新重试");
  } finally { if (!controller.signal.aborted) runtimeLoading.value = false; }
});
watch(() => props.source, useSource);
onMounted(refresh);
onUnmounted(() => { lifetime.abort(); selectionRequest?.abort(); });
</script>

<template>
  <section class="release-ledger" aria-label="项目发布">
    <aside class="project-picker" aria-label="项目列表">
      <header><h3>项目</h3><span>{{ projects.length }}</span></header>
      <input v-model="projectSearch" type="search" aria-label="搜索项目" placeholder="输入项目名称筛选" />
      <ul v-if="projectOptions.length">
        <li v-for="item in projectOptions" :key="item.id">
          <button type="button" :aria-pressed="item.id === projectId" @click="selectProject(item.id)">
            {{ item.name }}<small v-if="item.archived">已归档</small>
          </button>
        </li>
      </ul>
      <p v-else class="empty-state">{{ loading ? '正在读取…' : projectSearch ? '没有匹配的项目' : '暂无项目' }}</p>
    </aside>
    <div ref="listElement" class="release-main">
      <form class="filters" @submit.prevent="searchModules">
        <label>模块名称<input v-model="queryDraft" type="search" placeholder="请输入模块名称" aria-label="模块名称" /></label>
        <label>环境<select v-model="environmentDraft" aria-label="环境"><option value="">全部环境</option>
          <option v-for="env in project?.environments" :key="env.id" :value="env.id">{{ env.name }}</option>
        </select></label>
        <div class="filter-buttons">
          <button type="submit" class="search-button">搜索</button>
          <button type="button" class="plain-button" @click="clearFilters">重置</button>
        </div>
      </form>
      <div class="table-heading">
        <div><h3>{{ project?.name || '发布模块' }}</h3><span v-if="project">{{ project.modules.length }} 个模块<template v-if="project.archived"> · 已归档</template></span></div>
        <button type="button" class="plain-button" :disabled="loading" @click="refresh">{{ loading ? '刷新中…' : '刷新' }}</button>
      </div>
      <p v-if="pageError || runtimeError" class="sheet-message error-msg" role="alert">{{ pageError || runtimeError }}</p>
      <div v-if="setupNotice" class="sheet-message" role="status">
        <span><strong>{{ setupNotice.row.module.name }}</strong> · {{ setupNotice.message }}</span>
        <button v-if="!project?.archived" type="button" class="text-action" @click="maintain(setupNotice.row)">前往项目维护</button>
        <button type="button" class="dismiss" aria-label="关闭提示" @click="setupNotice = undefined">×</button>
      </div>
      <div class="table-viewport" tabindex="0" aria-label="模块发布表格">
        <table aria-label="模块发布列表" class="release-table">
          <thead>
            <tr><th scope="col">模块名称</th><th scope="col">模块类型</th><th scope="col">源码目录</th>
              <th scope="col">环境</th><th scope="col">运行服务器</th><th scope="col">QA 记录</th><th scope="col">操作</th></tr>
          </thead>
          <tbody>
            <tr v-for="row in visibleRows" :key="row.registration?.id || row.module.id" :data-focused="row.module.id === focusedModuleId">
              <th scope="row">{{ row.module.name }}<small class="mobile-kind">{{ row.module.catalog?.kind ? moduleKinds[row.module.catalog.kind] : '' }}<template v-if="row.record?.number"> · #{{ row.record.number }}</template></small></th>
              <td>{{ row.module.catalog?.kind ? moduleKinds[row.module.catalog.kind] : '—' }}</td>
              <td class="source-path" :title="row.module.directory">{{ row.module.directory || '—' }}</td>
              <td>{{ row.environment?.name || '—' }}</td>
              <td>
                <span>{{ row.registration ? hostsById.get(row.registration.hostId) || '—' : '—' }}</span><small v-if="row.environment" class="mobile-env">{{ row.environment.name }}</small>
                <small v-if="row.state" :class="row.state.tone">{{ row.state.label }}</small>
              </td>
              <td>
                <button type="button" class="text-action lookup" :disabled="!!opening" @click="openBuild(row, 'record')">
                  <template v-if="row.record"><span v-if="row.record.number">#{{ row.record.number }} · </span>{{ jenkinsState(row.record.status).label }}</template>
                  <template v-else>查记录</template>
                </button>
              </td>
              <td class="action-col">
                <div class="row-ops">
                  <button v-if="!project?.archived" type="button" class="text-action" :disabled="!!opening" @click="openBuild(row, 'qa')">QA</button>
                  <button type="button" class="text-action" :disabled="!!opening" @click="openBuild(row, 'logs')">QA 日志</button>
                  <button v-if="!project?.archived" type="button" class="text-action" @click="openRelease(row)">上线</button>
                  <button v-if="!project?.archived && row.registration?.managedInstanceId" type="button" class="text-action" @click="openRelease(row, 'verify')">验证</button>
                  <button v-if="row.runtime && !row.buildService" type="button" class="text-action" @click="openServiceLog(row.runtime)">服务日志</button>
                </div>
              </td>
            </tr>
          </tbody>
        </table>
      </div>
      <p v-if="!visibleRows.length" class="empty-state">{{ loading ? '正在读取模块…' : moduleSearch || environmentFilter ? '没有符合筛选条件的模块' : '暂无发布模块' }}</p>
    </div>
    <ElDrawer :model-value="!!panel" :title="panel?.context.action === 'qa' ? 'QA 执行' : 'QA 日志'" size="min(680px, 96vw)"
      append-to-body destroy-on-close :close-on-click-modal="false" @update:model-value="open => { if (!open) panel = undefined }">
      <template v-if="panel && project">
        <p class="run-context">{{ project.name }}<span>/</span><strong>{{ panel.context.moduleName }}</strong><template v-if="panel.context.environmentName"><span>/</span>{{ panel.context.environmentName }}</template></p>
        <ProjectJenkinsBuild :key="(panel.context.serviceInstanceId || panel.context.moduleId) + panel.context.action" :project-id="project.id" :context="panel.context" :monitor="panel.monitor"
          @observed="recordBuild(panel.context.serviceInstanceId || panel.context.moduleId, $event)" @error="emit('error', $event)" />
      </template>
    </ElDrawer>
  </section>
</template>

<style scoped>
.release-ledger {
  --line: #dfe5ed;
  --blue: #2468ba;
  display: grid;
  grid-template-columns: clamp(170px, 18%, 270px) minmax(0, 1fr);
  align-items: stretch;
  gap: 12px;
  min-width: 0;
  min-height: min(62vh, 600px);
  color: #23384e;
  container: release-ledger / inline-size;
}
.project-picker, .release-main { min-width: 0; border: 1px solid var(--line); background: #fff; border-radius: 4px; }
.project-picker { padding: 14px 10px; }
.project-picker header, .table-heading, .table-heading > div, .row-ops, .filters, .filter-buttons, .run-context { display: flex; align-items: center; }
.project-picker header { gap: 8px; margin: 0 5px 12px; }
.project-picker h3, .table-heading h3 { margin: 0; color: #20344c; font-size: 15px; font-weight: 650; }
.project-picker header span, .table-heading span { font-size: 12px; color: #62748a; }
.project-picker input, .filters input, .filters select {
  box-sizing: border-box;
  margin: 0;
  min-height: 32px;
  height: 32px;
  border: 1px solid #cdd7e3;
  border-radius: 3px;
  padding: 0 9px;
  background: white;
  color: #26384d;
  font: inherit;
}
.project-picker input { width: 100%; font-size: 12px; }
.project-picker ul { list-style: none; margin: 10px 0 0; padding: 0; max-height: 65vh; overflow: auto; }
.project-picker li + li { margin-top: 2px; }
.project-picker li button { display: block; width: 100%; border: 0; border-left: 3px solid transparent; border-radius: 0; padding: 7px 10px; background: transparent; color: #34475c; text-align: left; overflow-wrap: anywhere; font-size: 13px; }
.project-picker li button:hover { background: #f4f7fa; }
.project-picker li button[aria-pressed=true] { border-left-color: #2d76cb; background: #edf4fe; color: #1556a0; font-weight: 600; }
.project-picker small { margin-left: 6px; font-size: 11px; }
.release-main { padding: 15px 17px 20px; }
.filters { flex-wrap: wrap; gap: 12px 17px; padding-bottom: 14px; border-bottom: 1px solid #edf0f4; font-size: 13px; }
.filters label { display: inline-flex; align-items: center; gap: 8px; margin: 0; white-space: nowrap; color: #34465a; }
.filters input { width: clamp(140px, 15vw, 245px); }
.filters select { width: 115px; }
.filter-buttons { gap: 7px; }
.search-button, .plain-button { min-height: 30px; padding: 4px 13px; border: 1px solid #ccd5e0; border-radius: 3px; font-size: 13px; }
.plain-button { color: #354b61; background: #fff; }
.search-button { color: #fff; background: #2869b3; border-color: #2869b3; }
.table-heading { justify-content: space-between; gap: 8px; margin: 15px 0 10px; }
.table-heading > div { gap: 10px; }
.table-viewport { overflow-x: auto; min-width: 0; border: 1px solid var(--line); }
.release-table { width: 100%; min-width: 750px; border-collapse: collapse; font-size: 13px; text-align: left; }
.release-table tr > * { padding: 10px; border-bottom: 1px solid var(--line); border-right: 1px solid var(--line); text-align: left; overflow-wrap: anywhere; }
.release-table thead th { background: #f5f7fa; font-size: 12px; font-weight: 650; color: #36475b; white-space: nowrap; }
.release-table tbody th { font-weight: 600; background: transparent; }
.release-table tbody tr:nth-child(even) { background: #f8fafc; }
.release-table tbody tr:hover, .release-table tbody tr[data-focused=true] { background: #edf4fe; }
.release-table tbody tr:last-child > * { border-bottom: 0; }
.release-table tr > :last-child { border-right: 0; }
.release-table .source-path { max-width: 205px; color: #526375; }
.release-table td small { display: block; margin-top: 3px; color: #64758a; font-size: 11px; }
.release-table td small, .release-table th small { background: transparent; }
.release-table .mobile-kind, .release-table .mobile-env { display: none; }
.release-table td small.state-success { color: #217146; }
.release-table .action-col { width: 210px; }
.row-ops { flex-wrap: wrap; gap: 3px 11px; }
.text-action { border: 0; background: transparent; color: var(--blue); font: inherit; font-weight: 500; padding: 3px 0; white-space: nowrap; }
.text-action:hover { text-decoration: underline; }
.text-action:disabled { color: #8d9bae; }
.lookup { font-size: 12px; color: #596e87; }
.empty-state { margin: 0; padding: 18px 8px; color: #67798c; font-size: 13px; }
.sheet-message { display: flex; align-items: baseline; flex-wrap: wrap; gap: 6px 12px; margin: 0 0 11px; padding: 8px 11px; background: #fff8e8; border: 1px solid #ebd6a0; color: #6c501b; font-size: 13px; }
.sheet-message.error-msg { border-color: #f0c4bd; background: #fff5f4; color: #983c30; }
.dismiss { margin-left: auto; border: 0; background: transparent; color: inherit; }
.run-context { flex-wrap: wrap; gap: 8px; margin: 0 0 16px; font-size: 13px; color: var(--muted); }
.run-context strong { color: var(--text); }
@media (max-width: 850px) {
  .release-ledger { grid-template-columns: minmax(0, 1fr); min-height: auto; }
  .project-picker { display: grid; grid-template-columns: 1fr minmax(0, 1.2fr); align-items: center; gap: 8px; padding: 10px; }
  .project-picker header, .project-picker ul { margin: 0; }
  .project-picker ul { display: flex; gap: 4px; grid-column: 1/-1; overflow: auto; }
  .project-picker li { flex: 0 0 auto; }
  .project-picker li + li { margin: 0; }
  .release-main { padding: 12px 10px; }
  .filters { gap: 8px; }
  .filters label { gap: 5px; }
  .filters input { width: 145px; }
  .filters select { width: 100px; }
  .release-table { min-width: 650px; }
  .release-table th, .release-table td { padding: 7px 8px; }
}
@media (max-width: 540px) {
  .release-table { min-width: 0; table-layout: fixed; font-size: 12px; }
  .release-table td, .release-table th { padding: 8px 6px; white-space: normal; }
  .release-table tr > :is(:nth-child(2), :nth-child(3), :nth-child(4), :nth-child(6)) { display: none; }
  .release-table tr > :last-child { width: 34%; }
  .release-table .mobile-kind, .release-table .mobile-env { display: block; margin: 3px 0 0; color: #64758a; font-size: 11px; font-weight: 400; }
  .row-ops { gap: 1px 8px; }
}
</style>
