<script setup lang="ts">
import { computed, nextTick, onMounted, onUnmounted, ref, watch } from "vue";
import { ElDialog, ElDropdown, ElDropdownItem, ElDropdownMenu, ElMessage, ElMessageBox } from "element-plus";
import { ApiError, request } from "./api";
import {
  isProjectBuildService, moduleKinds, type ApplicationProject, type ProjectEnvironment, type ProjectInstanceSource,
  type ProjectModule, type ProjectReleaseSource, type ProjectServiceRegistration,
} from "./applicationProjects";
import type { Host } from "./hosts";
import type { LogSelection } from "./jobs";
import { requestAllPages } from "./listPages";
import type { JenkinsInstanceContext } from "./projectJenkins";
import { softwareState, type SoftwareInstance } from "./software";
import ProjectEnvironmentBinding from "./ProjectEnvironmentBinding.vue";
import ProjectJenkinsBuild from "./ProjectJenkinsBuild.vue";

interface ModuleRow {
  module: ProjectModule;
  environment?: ProjectEnvironment;
  registration?: ProjectServiceRegistration;
}

const props = defineProps<{ hosts: Host[]; source?: ProjectInstanceSource }>();
const emit = defineEmits<{ "source-used": []; logs: [LogSelection]; release: [ProjectReleaseSource]; error: [unknown] }>();
const lifetime = new AbortController();
const projects = ref<ApplicationProject[]>([]);
const projectId = ref(""), projectSearch = ref(""), moduleSearch = ref(""), environmentFilter = ref("");
const focusedModuleId = ref("");
const listElement = ref<HTMLElement>();
const loading = ref(false), removing = ref(false), pageError = ref("");
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
    return { ...row, runtime, buildService, state: runtime && !buildService ? softwareState(runtime) : undefined };
  });
});
const runtimeRefresh = ref(0), runtimeLoading = ref(false), runtimeError = ref("");
const binding = ref<ModuleRow>();
const panel = ref<JenkinsInstanceContext>();

function describe(error: unknown, fallback: string) {
  if (error instanceof ApiError && error.code === "AUTH_REQUIRED") emit("error", error);
  return error instanceof ApiError
    ? [error.message, error.code, error.traceId, error.timestamp].filter(Boolean).join(" · ")
    : fallback;
}

function selectProject(id: string) {
  projectId.value = id;
  moduleSearch.value = "";
  environmentFilter.value = "";
  focusedModuleId.value = "";
  panel.value = undefined;
  binding.value = undefined;
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
    if (!lifetime.signal.aborted) pageError.value = describe(error, "无法读取项目实例");
  } finally { loading.value = false; }
}
function apply(saved: ApplicationProject) {
  projects.value = projects.value.map(item => item.id === saved.id ? saved : item);
}
function openServiceLog(runtime?: SoftwareInstance) {
  if (runtime) emit("logs", { softwareId: runtime.id, title: runtime.name });
}
function openBuild(row: ModuleRow, action: JenkinsInstanceContext["action"]) {
  if (!row.registration || !row.environment || !project.value || (action === "qa" && project.value.archived)) return;
  panel.value = {
    action, readOnly: project.value.archived, moduleId: row.module.id, moduleName: row.module.name, serviceInstanceId: row.registration.id,
    environmentId: row.environment.id, environmentName: row.environment.name,
  };
}
function openRelease(row: ModuleRow) {
  if (!row.registration || !row.environment || !project.value || project.value.archived) return;
  emit("release", {
    projectId: project.value.id, moduleId: row.module.id, moduleName: row.module.name,
    serviceInstanceId: row.registration.id, hostId: row.registration.hostId,
    environmentId: row.environment.id, environmentName: row.environment.name,
  });
}
async function remove(row: ModuleRow) {
  const current = project.value;
  if (!current || current.archived || !row.registration || !row.environment || row.registration.runtimeSoftwareId || removing.value) return;
  try {
    await ElMessageBox.confirm(`解除「${row.module.name}」在「${row.environment.name}」的绑定？此环境的实例登记和版本记录将移除。`, "解除环境绑定", {
      confirmButtonText: "解除绑定", cancelButtonText: "取消", type: "warning",
    });
  } catch { return; }
  if (lifetime.signal.aborted || removing.value) return;
  removing.value = true;
  pageError.value = "";
  try {
    const saved = await request<ApplicationProject>(`/application-projects/${current.id}/service-instances/${row.registration.id}`, "DELETE", { version: current.version }, lifetime.signal);
    if (!lifetime.signal.aborted) {
      apply(saved);
      ElMessage.success("环境绑定已解除");
    }
  } catch (error) {
    if (!lifetime.signal.aborted) pageError.value = describe(error, "解除环境绑定失败");
  } finally { removing.value = false; }
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
onUnmounted(() => lifetime.abort());
</script>

<template>
  <section class="project-instances" aria-label="项目实例">
    <p v-if="pageError" class="workspace-error" role="alert">{{ pageError }}</p>
    <div class="instance-workspace">
      <aside class="project-rail" aria-label="项目列表">
        <header class="rail-header"><h3>项目</h3><span class="count">{{ projects.length }}</span></header>
        <input v-if="projects.length > 6" v-model="projectSearch" type="search" aria-label="搜索项目" placeholder="搜索项目" />
        <ul v-if="projectOptions.length" class="project-nav">
          <li v-for="item in projectOptions" :key="item.id">
            <button type="button" :aria-pressed="item.id === projectId" @click="selectProject(item.id)">
              <span>{{ item.name }}<small v-if="item.archived">已归档</small></span>
            </button>
          </li>
        </ul>
        <p v-else class="empty-note">{{ loading ? "正在读取项目…" : projectSearch ? "没有匹配的项目" : "暂无项目" }}</p>
      </aside>

      <div v-if="project" ref="listElement" class="module-workspace">
        <header class="project-header">
          <div>
            <h3>{{ project.name }}</h3>
            <p>{{ project.modules.length }} 个模块<span aria-hidden="true"> / </span>{{ project.environments?.length || 0 }} 个环境<template v-if="project.archived"> · 已归档</template></p>
          </div>
          <button class="secondary refresh-button" type="button" :disabled="loading" @click="refresh">{{ loading ? "刷新中…" : "刷新" }}</button>
        </header>
        <div v-if="project.modules.length" class="module-section">
          <div class="list-toolbar">
            <h4>模块实例</h4>
            <div class="list-filters">
              <input v-model="moduleSearch" type="search" aria-label="搜索模块" placeholder="搜索模块或目录" />
              <select v-if="project.environments?.length" v-model="environmentFilter" aria-label="筛选环境">
                <option value="">全部环境</option>
                <option v-for="env in project.environments" :key="env.id" :value="env.id">{{ env.name }}</option>
              </select>
            </div>
          </div>
          <p v-if="runtimeError" class="workspace-error" role="alert">{{ runtimeError }}</p>
          <table aria-label="模块实例列表" class="module-table">
            <thead><tr><th scope="col">模块</th><th scope="col">环境 / 主机</th><th scope="col">运行对象</th><th scope="col">登记版本</th><th scope="col">操作</th></tr></thead>
            <tbody>
              <tr v-for="row in visibleRows" :key="row.registration?.id || row.module.id" :data-focused="row.module.id === focusedModuleId" :data-bound="!!row.registration">
                <th scope="row" class="module-identity">
                  <span class="module-name">{{ row.module.name }}</span>
                  <span class="cell-detail">{{ row.module.catalog?.kind ? moduleKinds[row.module.catalog.kind] : '待分类' }}<template v-if="row.module.directory && row.module.directory !== row.module.name"> · {{ row.module.directory }}</template></span>
                </th>
                <td data-label="环境 / 主机">
                  <template v-if="row.environment && row.registration">
                    <span class="env-name">{{ row.environment.name }}</span>
                    <span class="cell-detail">{{ hostsById.get(row.registration.hostId) || '主机待核对' }}</span>
                  </template>
                  <span v-else class="muted-value">未绑定环境</span>
                </td>
                <td data-label="运行对象">
                  <span v-if="row.buildService" class="binding-mismatch">Jenkins · 需重新绑定</span>
                  <template v-else-if="row.runtime">
                    <span>{{ row.runtime.name }}</span>
                    <span class="cell-detail observed-state" :class="row.state?.tone">{{ row.state?.label }}</span>
                  </template>
                  <span v-else class="muted-value">{{ row.registration?.runtimeSoftwareId ? (runtimeLoading ? '读取中…' : '对象待核对') : '—' }}</span>
                </td>
                <td data-label="登记版本" class="version-cell"><span :class="{ 'muted-value': !row.registration?.recordedVersion }">{{ row.registration?.recordedVersion || '—' }}</span></td>
                <td class="actions-cell">
                  <div v-if="row.registration" class="row-actions">
                    <button v-if="!project.archived" type="button" class="row-button qa" @click="openBuild(row, 'qa')">QA</button>
                    <button v-if="!project.archived" type="button" class="row-button" @click="openRelease(row)">上线</button>
                    <button type="button" class="row-link" @click="openBuild(row, 'logs')">构建日志</button>
                    <ElDropdown v-if="!project.archived || (row.runtime && !row.buildService)" trigger="click">
                      <button type="button" class="more-button" :aria-label="row.module.name + ' 更多操作'">•••</button>
                      <template #dropdown>
                        <ElDropdownMenu>
                          <ElDropdownItem v-if="row.runtime && !row.buildService" @click="openServiceLog(row.runtime)">{{ row.runtime.name }} 服务日志</ElDropdownItem>
                          <ElDropdownItem v-if="!project.archived" @click="binding = row">编辑环境绑定</ElDropdownItem>
                          <ElDropdownItem v-if="!project.archived" @click="binding = { module: row.module }">绑定其他环境</ElDropdownItem>
                          <ElDropdownItem v-if="!project.archived && !row.registration.runtimeSoftwareId" divided :disabled="removing" @click="remove(row)">解除环境绑定</ElDropdownItem>
                        </ElDropdownMenu>
                      </template>
                    </ElDropdown>
                  </div>
                  <button v-else-if="!project.archived" type="button" class="row-link bind-link" @click="binding = { module: row.module }">绑定环境</button>
                  <span v-else class="muted-value">—</span>
                </td>
              </tr>
            </tbody>
          </table>
          <p v-if="!visibleRows.length" class="empty-note">没有符合条件的模块。<button type="button" class="row-link" @click="moduleSearch = ''; environmentFilter = ''">清除筛选</button></p>
        </div>
        <p v-else class="empty-note">请先在项目管理添加模块。</p>
      </div>
      <div v-else-if="!loading" class="empty-note">请先在项目管理创建项目。<button type="button" class="row-link" @click="refresh">刷新</button></div>
    </div>

    <ProjectEnvironmentBinding v-if="binding && project" :project="project" :module-id="binding.module.id" :environment="binding.environment" :registration="binding.registration"
      @saved="apply" @close="binding = undefined" @error="emit('error', $event)" />

    <ElDialog :model-value="!!panel" :title="panel?.action === 'qa' ? '模块 QA' : '构建日志'" width="min(820px, 94vw)" append-to-body destroy-on-close
      :close-on-click-modal="false" @update:model-value="open => { if (!open) panel = undefined }">
      <template v-if="project && panel">
        <p class="panel-path">{{ project.name }}<span>/</span><strong>{{ panel.moduleName }}</strong><span>/</span>{{ panel.environmentName }}</p>
        <ProjectJenkinsBuild :key="panel.serviceInstanceId + panel.action" :project-id="project.id" :context="panel" @error="emit('error', $event)" />
      </template>
    </ElDialog>
  </section>
</template>

<style scoped>
.project-instances { min-width: 0; container: instances / inline-size; }
.instance-workspace { display: grid; grid-template-columns: clamp(158px, 16%, 218px) minmax(0, 1fr); min-height: 340px; border: 1px solid var(--border); border-radius: var(--card-radius); background: var(--surface); }
.project-rail { min-width: 0; padding: 18px 10px; background: #f7f9fc; border-right: 1px solid var(--border); border-radius: var(--card-radius) 0 0 var(--card-radius); }
.rail-header, .project-header, .list-toolbar, .list-filters, .row-actions, .panel-path { display: flex; align-items: center; }
.rail-header { gap: 7px; padding: 0 8px 12px; }
.rail-header h3, .list-toolbar h4 { margin: 0; font-size: 14px; font-weight: 650; color: #334155; }
.count { font: 12px var(--mono); color: var(--muted); }
.project-rail input { width: 100%; margin: 0 0 8px; }
.project-nav { display: grid; gap: 3px; max-height: 60vh; margin: 0; padding: 0; overflow: auto; list-style: none; }
.project-nav button { display: block; position: relative; width: 100%; min-height: 38px; padding: 9px 12px; border: 0; border-radius: 4px; text-align: left; color: var(--text); background: transparent; overflow-wrap: anywhere; }
.project-nav button:hover { background: #eef2f8; }
.project-nav button[aria-pressed=true] { background: #e5eefd; color: #174b9d; font-weight: 650; }
.project-nav button[aria-pressed=true]::before { position: absolute; content: ""; inset: 9px auto 9px 0; width: 3px; border-radius: 3px; background: var(--accent); }
.project-nav small { margin-left: 8px; font-weight: 400; }
.module-workspace { min-width: 0; }
.project-header { justify-content: space-between; gap: 12px; padding: 20px 22px 17px; border-bottom: 1px solid var(--border); }
.project-header h3 { margin: 0; font-size: 21px; letter-spacing: -.025em; color: #15283e; overflow-wrap: anywhere; }
.project-header p { margin: 5px 0 0; color: var(--muted); font-size: 12px; }
.project-header p span { margin: 0 5px; color: #adb8c8; }
.refresh-button { min-height: 30px; padding: 5px 12px; flex-shrink: 0; }
.list-toolbar { padding: 14px 20px; gap: 12px; justify-content: space-between; flex-wrap: wrap; }
.list-filters { gap: 8px; }
.list-filters input, .list-filters select, .project-rail input { height: 32px; min-height: 32px; margin: 0; border: 1px solid var(--border); border-radius: 5px; padding: 4px 8px; font-size: 12px; background: white; color: var(--text); }
.list-filters input { width: 166px; } .list-filters select { max-width: 150px; }
.module-table { width: 100%; min-width: 0; table-layout: fixed; border-collapse: collapse; font-size: 13px; }
.module-table thead th { text-align: left; background: #f5f7fa; color: #526379; font-size: 12px; font-weight: 550; padding: 9px 10px; border-block: 1px solid var(--border); }
.module-table thead th:first-child { width: 24%; padding-left: 20px; }
.module-table thead th:nth-child(3) { width: 17%; }
.module-table thead th:nth-child(4) { width: 12%; }
.module-table thead th:last-child { width: 199px; }
.module-table tbody td, .module-table tbody th { padding: 12px 10px; border: none; border-bottom: 1px solid var(--border-subtle); vertical-align: middle; overflow-wrap: anywhere; text-align: left; line-height: 1.5; font-weight: 400; }
.module-table tbody :is(td, th) { white-space: normal; background: transparent; }
.module-table tbody th { padding-left: 20px; }
.module-table tbody tr:last-child > * { border-bottom: 0; }
.module-table tbody tr:hover, .module-table tbody tr[data-focused=true] { background: #f5f9ff; }
.module-name { color: #142942; font-weight: 650; font-size: 14px; }
.cell-detail { display: block; margin: 3px 0 0; color: var(--muted); font-size: 12px; font-weight: 400; }
.env-name { display: inline-block; border: 1px solid #cbd9ed; padding: 0 6px; border-radius: 4px; color: #315572; background: #f4f8fd; font-size: 12px; }
.muted-value { color: var(--muted); font-size: 12px; }
.binding-mismatch { color: #8a5618; font-size: 12px; }
.version-cell { font-variant-numeric: tabular-nums; }
.observed-state { display: flex; align-items: center; gap: 5px; width: fit-content; background: none; border: 0; }
.observed-state::before { content: ""; width: 6px; height: 6px; border-radius: 50%; background: currentColor; }
.observed-state.state-success { color: #16734d; }
.observed-state.state-error { color: #b42318; }
.observed-state.state-warning { color: #965c11; }
.row-actions { gap: 6px; flex-wrap: wrap; }
.row-button, .more-button, .row-link { font-size: 12px; line-height: 18px; min-height: 28px; white-space: nowrap; }
.row-button, .more-button { border-radius: 4px; border: 1px solid #cbd5e1; background: var(--surface); color: #31445a; padding: 3px 8px; }
.row-button:hover, .more-button:hover { border-color: #869cba; background: #f3f6fa; }
.row-button.qa { color: #174a9a; background: #edf3ff; border-color: #bfcef0; font-weight: 650; }
.row-button.qa:hover { background: #dfeaff; border-color: #90abe4; }
.row-link { border: none; background: transparent; color: var(--accent); padding: 3px 2px; }
.row-link:hover { text-decoration: underline; text-underline-offset: 3px; }
.more-button { border-color: transparent; padding: 3px 4px; color: var(--muted); }
.empty-note { margin: 0; padding: 16px; color: var(--muted); font-size: 13px; }
.workspace-error { margin: 0 0 8px; padding: 9px 12px; border: 1px solid #fecaca; background: #fef2f2; border-radius: 5px; color: #991b1b; overflow-wrap: anywhere; }
.panel-path { flex-wrap: wrap; gap: 8px; margin: 0 0 14px; font-size: 13px; color: var(--muted); }
.panel-path span { color: #a6b2c1; }
.panel-path strong { font-weight: 600; color: var(--text); }
@container instances (max-width: 900px) {
  .instance-workspace { grid-template-columns: 140px minmax(0, 1fr); }
  .module-table thead th:first-child { width: 24%; }
  .module-table thead th:nth-child(3) { width: 16%; }
  .module-table thead th:nth-child(4) { width: 10%; }
}
@container instances (max-width: 740px) {
  .instance-workspace { grid-template-columns: minmax(0, 1fr); }
  .project-rail { display: flex; align-items: center; gap: 8px; padding: 8px 12px; border: none; border-bottom: 1px solid var(--border); border-radius: var(--card-radius) var(--card-radius) 0 0; flex-wrap: wrap; }
  .rail-header { padding: 0; }
  .project-nav { display: flex; min-width: 0; max-width: 100%; }
  .project-nav button { width: max-content; min-height: 30px; padding: 4px 10px; }
  .project-rail input { max-width: 160px; margin: 0; }
  .project-header { padding: 14px; }
  .list-toolbar { padding: 12px 14px; }
}
@container instances (max-width: 580px) {
  .list-toolbar { display: grid; gap: 8px; }
  .list-filters { flex-wrap: wrap; min-width: 0; }
  .list-filters input { width: min(155px, 54%); }
  .list-filters select { width: min(128px, 42%); }
  .module-table, .module-table tbody { display: block; }
  .module-table thead { position: absolute; width: 1px; height: 1px; clip-path: inset(50%); overflow: hidden; }
  .module-table tbody tr { display: grid; grid-template-columns: repeat(3, minmax(0, 1fr)); gap: 9px 12px; border-top: 1px solid var(--border); padding: 12px 14px; }
  .module-table tbody td, .module-table tbody th { display: block; border: 0; padding: 0; min-width: 0; }
  .module-table .module-identity, .module-table .actions-cell { grid-column: 1 / -1; }
  .module-identity .cell-detail { display: inline; margin-left: 8px; }
  .module-table tr[data-bound=false] { grid-template-columns: minmax(0, 1fr) auto; align-items: center; }
  .module-table tr[data-bound=false] .module-identity, .module-table tr[data-bound=false] .actions-cell { grid-column: auto; }
  .module-table tr[data-bound=false] td[data-label] { display: none; }
  .module-table td[data-label]::before { display: block; content: attr(data-label); color: var(--muted); font-size: 11px; margin-bottom: 3px; }
}
</style>
