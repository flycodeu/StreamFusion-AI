<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref, watch } from "vue";
import { ElDialog, ElDropdown, ElDropdownItem, ElDropdownMenu } from "element-plus";
import { ApiError, request } from "./api";
import type { Host } from "./hosts";
import ProjectEnvironmentBinding from "./ProjectEnvironmentBinding.vue";
import ProjectJenkinsConnection from "./ProjectJenkinsConnection.vue";
import {
  moduleDirectory, moduleEdit, moduleKinds, newModuleDraft, projectListPath, repositoryId,
  type ApplicationProject, type ApplicationProjectPage, type InspectedModule,
  type InspectionSource, type ProjectEnvironment, type ProjectInstanceSource, type ProjectModule, type ProjectServiceRegistration,
} from "./applicationProjects";

const props = defineProps<{ source?: ProjectInstanceSource }>();
const emit = defineEmits<{ "manage-targets": [source?: ProjectInstanceSource]; "source-used": []; error: [unknown]; "install-jenkins": []; "view-instances": [projectId: string, moduleId: string] }>();
const rootPath = "/application-projects";
const records = ref<ApplicationProject[]>([]);
const page = ref(1), pageSize = ref(10), total = ref(0);
const query = ref(""), archived = ref("false");
const loading = ref(false), opening = ref(false), saving = ref(false);
const listError = ref(""), formError = ref(""), archiveTarget = ref("");
const project = ref<ApplicationProject>();
const selectedModuleId = ref("");
const primaryRepository = computed(() => project.value?.repositories[0]);
const hostNames = ref<Record<string, string>>({});
const moduleQuery = ref("");
const visibleModules = computed(() => (project.value?.modules || []).filter(item =>
  [item.name, item.directory].some(text => text.toLowerCase().includes(moduleQuery.value.trim().toLowerCase())),
));
function moduleEnvironments(moduleId: string) {
  return (project.value?.environments || []).flatMap(environment => environment.deployments
    .filter(registration => registration.moduleId === moduleId)
    .map(registration => ({ environment, registration })));
}
const environmentBinding = ref<{ moduleId: string; environment?: ProjectEnvironment; registration?: ProjectServiceRegistration }>();

const projectEditorOpen = ref(false), moduleEditorOpen = ref(false), projectDeleteOpen = ref(false), moduleDeleteOpen = ref(false);
const editingProject = ref<ApplicationProject>();
const projectDraft = ref({ name: "", description: "", repositoryUrl: "", branch: "" });
const projectDeleteTarget = ref<ApplicationProject>();
const moduleDeleteTarget = ref<ProjectModule>();
const moduleDraft = ref(newModuleDraft());
const separateRepository = "new-repository";
const newRepositoryBranch = ref("main");
const pipelineKinds = { BACKEND: "后端 · Java / Maven", FRONTEND: "前端 · Node.js", ALGORITHM: "算法 · Python", OTHER: "其他（无内置流水线）" };

const inspectionOpen = ref(false), sourcesLoading = ref(false), inspecting = ref(false), inspected = ref(false);
const inspectionModuleId = ref(""), sourceId = ref(""), inspectionRepository = ref("");
const inspectionSources = ref<InspectionSource[]>([]);
const inspectionResults = ref<InspectedModule[]>([]);
const selectedDirectories = ref<string[]>([]);
let listController: AbortController | undefined;
let projectController: AbortController | undefined;
let inspectionController: AbortController | undefined;
let searchTimer: ReturnType<typeof setTimeout> | undefined;

function describeError(error: unknown, fallback: string) {
  if (error instanceof ApiError && error.code === "AUTH_REQUIRED") emit("error", error);
  const message = error instanceof Error && /[\u4e00-\u9fff]/.test(error.message) ? error.message : fallback;
  const details = error instanceof ApiError ? [error.code, error.traceId, error.timestamp].filter(Boolean) : [];
  return [message, ...details].join(" · ");
}

async function load() {
  listController?.abort();
  const controller = listController = new AbortController();
  const focusRequested = props.source;
  loading.value = true;
  listError.value = "";
  try {
    const data = await request<ApplicationProjectPage>(projectListPath(page.value, pageSize.value, query.value, archived.value), "GET", undefined, controller.signal);
    if (controller.signal.aborted) return;
    const last = Math.max(1, Math.ceil(data.total / pageSize.value));
    if (page.value > last) { page.value = last; return; }
    records.value = data.items;
    total.value = data.total;
    if (!focusRequested && !props.source && !projectController) {
      const next = data.items.find(item => item.id === project.value?.id) || data.items[0];
      if (next) showProject(next, next.id === project.value?.id ? selectedModuleId.value : undefined);
      else project.value = undefined;
    }
  } catch (error) {
    if (!controller.signal.aborted) listError.value = describeError(error, "项目列表读取失败，请刷新后重试");
  } finally {
    if (listController === controller) loading.value = false;
  }
}

function showProject(record: ApplicationProject, moduleId = record.modules[0]?.id ?? "") {
  if (project.value?.id !== record.id) moduleQuery.value = "";
  project.value = record;
  if (records.value.some(item => item.id === record.id)) records.value = records.value.map(item => item.id === record.id ? record : item);
  selectedModuleId.value = record.modules.find(item => item.id === moduleId)?.id || record.modules[0]?.id || "";
}

async function openProject(record: { id: string }, moduleId?: string) {
  projectController?.abort();
  const controller = projectController = new AbortController();
  opening.value = true;
  listError.value = "";
  try {
    const detail = await request<ApplicationProject>(`${rootPath}/${record.id}`, "GET", undefined, controller.signal);
    if (!controller.signal.aborted) showProject(detail, moduleId);
  } catch (error) {
    if (!controller.signal.aborted) listError.value = describeError(error, "项目详情读取失败，请重新打开");
  } finally {
    if (controller === projectController) { opening.value = false; projectController = undefined; }
  }
}

async function useSource(source: ProjectInstanceSource) {
  moduleQuery.value = "";
  await openProject({ id: source.projectId }, source.moduleId);
  if (props.source === source) emit("source-used");
}

function editProject(record: ApplicationProject | null | undefined = project.value) {
  editingProject.value = record ?? undefined;
  projectDraft.value = {
    name: record?.name ?? "",
    description: record?.description ?? "",
    repositoryUrl: record?.repositories[0]?.url ?? "",
    branch: record?.repositories[0]?.branch ?? "",
  };
  formError.value = "";
  projectEditorOpen.value = true;
}

async function saveProject() {
  if (saving.value || !projectDraft.value.name.trim()) return;
  const { name, description, repositoryUrl, branch } = projectDraft.value;
  const record = editingProject.value;
  const primary = record?.repositories[0];
  const repoChanged = repositoryUrl.trim() && (repositoryUrl.trim() !== primary?.url || branch.trim() !== primary?.branch);
  saving.value = true;
  formError.value = "";
  try {
    const saved = await request<ApplicationProject>(record ? `${rootPath}/${record.id}/catalog` : `${rootPath}/catalog`, record ? "PATCH" : "POST", {
      version: record?.version,
      name: name.trim(),
      description: description.trim(),
      repositories: repoChanged ? [{ id: primary?.id, name: primary?.name ?? "主仓库", url: repositoryUrl.trim(), branch: branch.trim() }] : undefined,
    });
    showProject(saved, record ? selectedModuleId.value : undefined);
    projectEditorOpen.value = false;
    if (!record) {
      query.value = ""; archived.value = "false"; page.value = 1;
      await load();
      showProject(saved);
    }
  } catch (error) {
    formError.value = describeError(error, "项目保存失败，请检查填写内容后重试");
  } finally { saving.value = false; }
}

async function setArchived(record: ApplicationProject, value: boolean) {
  if (saving.value) return;
  saving.value = true;
  listError.value = "";
  try {
    await request(value ? `${rootPath}/${record.id}/archive` : `${rootPath}/${record.id}/catalog`, value ? "POST" : "PATCH", {
      version: record.version,
      ...(value ? {} : { archived: false }),
    });
    archiveTarget.value = "";
    await load();
  } catch (error) {
    listError.value = describeError(error, "归档状态更新失败，请刷新列表后重试");
  } finally { saving.value = false; }
}

function confirmProjectDeletion(record: ApplicationProject) {
  projectDeleteTarget.value = record;
  formError.value = "";
  projectDeleteOpen.value = true;
}

async function deleteProject() {
  const target = projectDeleteTarget.value;
  if (!target || !projectDeleteOpen.value || saving.value) return;
  saving.value = true;
  formError.value = "";
  try {
    await request(`${rootPath}/${target.id}`, "DELETE", { version: target.version });
    projectDeleteOpen.value = false;
    await load();
  } catch (error) {
    formError.value = describeError(error, "项目删除失败，请刷新后重试");
  } finally { saving.value = false; }
}

function confirmModuleDeletion(item: ProjectModule) {
  moduleDeleteTarget.value = item;
  formError.value = "";
  moduleDeleteOpen.value = true;
}

async function deleteModule() {
  const record = project.value;
  const target = moduleDeleteTarget.value;
  if (!record || !target || !moduleDeleteOpen.value || saving.value) return;
  saving.value = true;
  formError.value = "";
  try {
    const saved = await request<ApplicationProject>(`${rootPath}/${record.id}/modules/${target.id}`, "DELETE", { version: record.version });
    const selected = saved.modules.some(item => item.id === selectedModuleId.value) ? selectedModuleId.value : undefined;
    showProject(saved, selected);
    moduleDeleteOpen.value = false;
  } catch (error) {
    if (error instanceof ApiError && error.code === "APPLICATION_MODULE_IN_USE") {
      formError.value = ["请先在「运行配置」移除该模块的环境", error.code, error.traceId, error.timestamp].filter(Boolean).join(" · ");
    } else {
      formError.value = describeError(error, "模块删除失败，请刷新后重试");
    }
  } finally { saving.value = false; }
}

function editModule(item?: ProjectModule) {
  newRepositoryBranch.value = "main";
  moduleDraft.value = newModuleDraft(primaryRepository.value?.id, item);
  formError.value = "";
  moduleEditorOpen.value = true;
}

async function saveModules(modules: ReturnType<typeof moduleEdit>[], repositories?: { id: string; name: string; url: string; branch: string }[]) {
  const record = project.value;
  if (!record || saving.value) return false;
  saving.value = true;
  formError.value = "";
  try {
    const saved = await request<ApplicationProject>(`${rootPath}/${record.id}/catalog`, "PATCH", { version: record.version, modules, repositories });
    const newModule = saved.modules.find(item => !record.modules.some(previous => previous.id === item.id));
    showProject(saved, modules[0]?.id || newModule?.id || selectedModuleId.value);
    return true;
  } catch (error) {
    formError.value = describeError(error, "模块保存失败，请检查填写内容后重试");
    return false;
  } finally { saving.value = false; }
}

async function saveModule() {
  const draft = moduleDraft.value;
  if (!draft.name.trim()) return;
  if (draft.repositoryId !== separateRepository) {
    if (await saveModules([moduleEdit(draft)])) moduleEditorOpen.value = false;
    return;
  }
  const url = draft.repositoryUrl.trim();
  if (!url) return;
  const existing = project.value?.repositories.find(item => item.url === url);
  const id = existing?.id ?? repositoryId();
  if (await saveModules([moduleEdit(draft, id)], existing ? undefined : [{ id, name: draft.name.trim(), url, branch: newRepositoryBranch.value.trim() }])) {
    moduleEditorOpen.value = false;
  }
}

function matchingModule(directory: string) {
  const modules = project.value?.modules ?? [];
  if (inspectionModuleId.value) return modules.find(item => item.id === inspectionModuleId.value);
  const atDirectory = modules.filter(item => moduleDirectory(item.directory) === directory);
  const known = atDirectory.find(item => item.repositoryId === inspectionRepository.value);
  return known ?? (inspectionRepository.value === primaryRepository.value?.id ? atDirectory.find(item => !item.repositoryId) : undefined);
}

async function openInspection(item?: ProjectModule) {
  inspectionController?.abort();
  const controller = inspectionController = new AbortController();
  inspectionModuleId.value = item?.id ?? "";
  inspectionRepository.value = item?.repositoryId ?? primaryRepository.value?.id ?? "";
  inspectionSources.value = [];
  sourceId.value = "";
  inspectionResults.value = [];
  selectedDirectories.value = [];
  formError.value = "";
  inspected.value = false;
  inspecting.value = false;
  sourcesLoading.value = true;
  inspectionOpen.value = true;
  try {
    const { items } = await request<{ items: InspectionSource[] }>(`${rootPath}/inspection-sources`, "GET", undefined, controller.signal);
    if (controller.signal.aborted) return;
    inspectionSources.value = items;
    if (items.length === 1) sourceId.value = items[0].id;
  } catch (error) {
    if (!controller.signal.aborted) formError.value = describeError(error, "识别来源读取失败，可以手动填写模块");
  } finally {
    if (controller === inspectionController) sourcesLoading.value = false;
  }
}

function resetInspectionResults() {
  inspectionResults.value = [];
  selectedDirectories.value = [];
  formError.value = "";
  inspected.value = false;
}

function selectNewDirectories() {
  selectedDirectories.value = inspectionResults.value.filter(item => !matchingModule(item.directory)).map(item => item.directory);
}

async function inspect() {
  if (!sourceId.value || inspecting.value || saving.value) return;
  const directory = project.value?.modules.find(item => item.id === inspectionModuleId.value)?.directory;
  inspectionController?.abort();
  const controller = inspectionController = new AbortController();
  resetInspectionResults();
  inspecting.value = true;
  try {
    const body = { sourceId: sourceId.value, directory: inspectionModuleId.value ? directory || "." : undefined };
    const { modules } = await request<{ modules: InspectedModule[] }>(`${rootPath}/inspect`, "POST", body, controller.signal);
    if (controller.signal.aborted) return;
    inspectionResults.value = modules;
    selectNewDirectories();
    inspected.value = true;
  } catch (error) {
    if (!controller.signal.aborted) formError.value = describeError(error, "环境识别失败，可以手动填写模块");
  } finally {
    if (controller === inspectionController) inspecting.value = false;
  }
}

function closeInspection() {
  inspectionController?.abort();
}

function reviewSuggestion(suggestion: InspectedModule) {
  const existing = matchingModule(suggestion.directory);
  if (!existing) return;
  inspectionOpen.value = false;
  editModule(existing);
  const draft = moduleDraft.value;
  if (!existing.catalog) draft.kind = suggestion.kind;
  draft.environment ||= suggestion.environment;
  draft.startCommand ||= suggestion.startCommand;
}

async function addSuggestions() {
  const modules = inspectionResults.value
    .filter(item => selectedDirectories.value.includes(item.directory) && !matchingModule(item.directory))
    .map(item => moduleEdit({ ...newModuleDraft(inspectionRepository.value), ...item }));
  if (modules.length && await saveModules(modules)) inspectionOpen.value = false;
}

watch([page, pageSize, archived], () => void load());
watch([pageSize, archived], () => { page.value = 1; }, { flush: "sync" });
watch(query, () => {
  if (searchTimer) clearTimeout(searchTimer);
  listController?.abort();
  searchTimer = setTimeout(() => { if (page.value === 1) void load(); else page.value = 1; }, 250);
});
const lifetime = new AbortController();
watch(() => props.source, source => { if (source) void useSource(source); });
onMounted(() => {
  void load();
  if (props.source) void useSource(props.source);
  void request<Host[]>("/hosts", "GET", undefined, lifetime.signal).then(hosts => {
    if (!lifetime.signal.aborted) hostNames.value = Object.fromEntries(hosts.map(host => [host.id, host.name]));
  }).catch(error => { if (!lifetime.signal.aborted) listError.value = describeError(error, "服务器名称读取失败"); });
});
onUnmounted(() => {
  lifetime.abort();
  listController?.abort();
  projectController?.abort();
  inspectionController?.abort();
  if (searchTimer) clearTimeout(searchTimer);
});
</script>

<template>
  <section class="application-projects" aria-label="项目维护">
    <aside class="project-nav" aria-label="项目目录">
      <header><h3>项目</h3><button class="quiet" type="button" :disabled="saving" @click="editProject(null)">＋ 新建</button></header>
      <input v-model="query" type="search" placeholder="筛选项目" aria-label="搜索项目" />
      <select v-model="archived" aria-label="项目状态"><option value="false">使用中</option><option value="true">已归档</option><option value="all">全部状态</option></select>
      <nav aria-label="项目列表">
        <button v-for="record in records" :key="record.id" type="button" class="project-option"
          :aria-current="project?.id === record.id ? 'true' : undefined" @click="openProject(record)">{{ record.name }}</button>
      </nav>
      <p v-if="!records.length" class="nav-note">{{ loading ? "正在读取…" : "没有匹配的项目" }}</p>
      <footer v-if="total > pageSize" class="page-controls">
        <button type="button" class="quiet" :disabled="loading || page === 1" aria-label="上一页项目" @click="page--">‹</button>
        <span>{{ page }} / {{ Math.ceil(total / pageSize) }}</span>
        <button type="button" class="quiet" :disabled="loading || page * pageSize >= total" aria-label="下一页项目" @click="page++">›</button>
      </footer>
      <div class="nav-tools"><button type="button" class="quiet" @click="emit('manage-targets', project && selectedModuleId ? { projectId: project.id, moduleId: selectedModuleId } : undefined)">上线目标管理</button></div>
    </aside>
    <main class="project-content" :aria-busy="opening">
      <p v-if="listError" class="project-error" role="alert">{{ listError }} <button class="quiet" @click="load">刷新</button></p>
      <template v-if="project">
        <header class="project-summary">
          <div class="project-toolbar">
            <h2>{{ project.name }} <small v-if="project.archived">已归档</small></h2>
            <div class="project-actions">
              <button v-if="!project.archived" type="button" class="secondary" @click="editProject()">编辑项目</button>
              <ElDropdown trigger="click"><button type="button" class="quiet" aria-label="更多项目操作">更多 ▾</button><template #dropdown><ElDropdownMenu>
                <ElDropdownItem v-if="project.archived" @click="setArchived(project, false)">恢复使用</ElDropdownItem>
                <ElDropdownItem v-else @click="archiveTarget = project.id">归档项目</ElDropdownItem>
                <ElDropdownItem @click="confirmProjectDeletion(project)">删除项目</ElDropdownItem>
              </ElDropdownMenu></template></ElDropdown>
            </div>
          </div>
          <p v-if="project.description" class="project-description">{{ project.description }}</p>
          <dl class="project-facts">
            <div><dt>Git 仓库</dt><dd>{{ primaryRepository?.url || "未配置" }}</dd></div>
            <div v-if="primaryRepository?.branch"><dt>分支</dt><dd>{{ primaryRepository.branch }}</dd></div>
          </dl>
          <ProjectJenkinsConnection :key="project.id" :project-id="project.id" :archived="project.archived"
            @error="emit('error', $event)" @install="emit('install-jenkins')" />
          <div v-if="archiveTarget === project.id" class="project-actions archive-confirm" role="group" aria-label="归档确认">
            <span>归档「{{ project.name }}」？</span><button type="button" class="secondary" @click="archiveTarget = ''">取消</button>
            <button type="button" class="secondary danger" :disabled="saving" @click="setArchived(project, true)">确认归档</button>
          </div>
        </header>
        <section class="module-section" aria-label="项目模块">
          <div class="module-top project-toolbar">
            <div class="project-actions"><h3>项目模块</h3><input v-model="moduleQuery" type="search" placeholder="筛选模块名称或目录" aria-label="筛选模块" /></div>
            <div v-if="!project.archived" class="project-actions">
              <button type="button" class="secondary" :disabled="saving" @click="openInspection()">识别模块</button>
              <button type="button" class="primary" :disabled="saving" @click="editModule()">添加模块</button>
            </div>
          </div>
          <div class="table-scroll">
            <table class="module-table" aria-label="模块列表">
              <thead><tr><th>模块名称</th><th>模块类型</th><th>源码目录</th><th>环境 / 运行服务器</th><th>操作</th></tr></thead>
              <tbody>
                <tr v-for="item in visibleModules" :key="item.id" :class="{ 'current-module': item.id === selectedModuleId }">
                  <th scope="row">{{ item.name }}</th><td>{{ moduleKinds[item.catalog?.kind || 'OTHER'] }}</td><td><code>{{ item.directory || "." }}</code></td>
                  <td><template v-if="moduleEnvironments(item.id).length">
                    <div v-for="row in moduleEnvironments(item.id)" :key="row.registration.id" class="env-item">
                      <button v-if="!project.archived" type="button" class="table-link" :aria-label="'编辑 ' + item.name + ' ' + row.environment.name + ' 运行配置'"
                        @click="selectedModuleId = item.id; environmentBinding = { moduleId: item.id, ...row }">{{ row.environment.name }}</button>
                      <span v-else>{{ row.environment.name }}</span><span>{{ hostNames[row.registration.hostId] || '服务器待核对' }}</span>
                    </div></template><span v-else class="muted">—</span></td>
                  <td><div v-if="!project.archived" class="table-actions">
                    <button type="button" class="table-link" @click="selectedModuleId = item.id; editModule(item)">编辑</button>
                    <button type="button" class="table-link" @click="selectedModuleId = item.id; environmentBinding = { moduleId: item.id, ...moduleEnvironments(item.id)[0] }">运行配置</button>
                    <ElDropdown trigger="click"><button type="button" class="table-link" :aria-label="item.name + ' 更多操作'">···</button><template #dropdown><ElDropdownMenu>
                      <ElDropdownItem v-if="moduleEnvironments(item.id).length" @click="selectedModuleId = item.id; environmentBinding = { moduleId: item.id }">添加运行环境</ElDropdownItem>
                      <ElDropdownItem @click="openInspection(item)">识别此模块</ElDropdownItem>
                      <ElDropdownItem @click="confirmModuleDeletion(item)">删除模块</ElDropdownItem>
                    </ElDropdownMenu></template></ElDropdown>
                  </div><span v-else class="muted">—</span></td>
                </tr>
              </tbody>
            </table>
          </div>
          <p v-if="!visibleModules.length" class="project-empty">{{ moduleQuery ? "没有匹配的模块" : "还没有模块" }}</p>
        </section>
      </template>
      <p v-else class="project-empty">{{ loading || opening ? "正在读取项目…" : "选择项目或新建项目" }}</p>
    </main>
  </section>

  <ElDialog v-model="projectEditorOpen" :title="editingProject ? '编辑项目' : '新建项目'" width="min(540px, 94vw)" :close-on-click-modal="false" :close-on-press-escape="!saving" :show-close="!saving">
    <form class="project-form" @submit.prevent="saveProject">
      <label>项目名称 <input v-model="projectDraft.name" required maxlength="60" /></label>
      <label>项目简介 <textarea v-model="projectDraft.description" rows="2" maxlength="1000" placeholder="选填" /></label>
      <label>主仓库地址 <input v-model="projectDraft.repositoryUrl" :required="!!editingProject?.repositories[0]" placeholder="例如 https://git.example.com/team/project.git" /></label>
      <label v-if="projectDraft.repositoryUrl">Git 分支 <input v-model="projectDraft.branch" placeholder="例如 main" /></label>
      <p v-if="formError" class="project-error" role="alert">{{ formError }}</p>
      <div class="project-actions form-actions">
        <button type="button" class="secondary" :disabled="saving" @click="projectEditorOpen = false">取消</button>
        <button type="submit" class="primary" :disabled="saving || !projectDraft.name.trim()">{{ saving ? "正在保存…" : "保存项目" }}</button>
      </div>
    </form>
  </ElDialog>

  <ProjectEnvironmentBinding v-if="environmentBinding && project" :project="project" :module-id="environmentBinding.moduleId"
    :environment="environmentBinding.environment" :registration="environmentBinding.registration"
    @manage-targets="environmentBinding = undefined; emit('manage-targets', $event)" @saved="showProject($event, selectedModuleId)" @close="environmentBinding = undefined" @error="emit('error', $event)" />

  <ElDialog v-model="projectDeleteOpen" title="删除项目" width="min(440px, 94vw)" :close-on-click-modal="false" :close-on-press-escape="!saving" :show-close="!saving">
    <div class="project-form">
      <p>删除「{{ projectDeleteTarget?.name }}」的项目档案及其配置后，无法恢复。</p>
      <p v-if="formError" class="project-error" role="alert">{{ formError }}</p>
      <div class="project-actions form-actions">
        <button type="button" class="secondary" :disabled="saving" @click="projectDeleteOpen = false">取消</button>
        <button type="button" class="secondary danger" :disabled="saving" @click="deleteProject">{{ saving ? "正在删除…" : "确认删除" }}</button>
      </div>
    </div>
  </ElDialog>

  <ElDialog v-model="moduleDeleteOpen" title="删除模块" width="min(460px, 94vw)" :close-on-click-modal="false" :close-on-press-escape="!saving" :show-close="!saving">
    <div class="project-form">
      <p>删除「{{ moduleDeleteTarget?.name }}」的模块记录后，无法恢复。</p>
      <p v-if="formError" class="project-error" role="alert">{{ formError }}</p>
      <div class="project-actions form-actions">
        <button v-if="project && moduleDeleteTarget && formError.includes('APPLICATION_MODULE_IN_USE')" type="button" class="secondary" :disabled="saving" @click="moduleDeleteOpen = false; environmentBinding = { moduleId: moduleDeleteTarget.id, ...moduleEnvironments(moduleDeleteTarget.id)[0] }">查看运行配置</button>
        <button type="button" class="secondary" :disabled="saving" @click="moduleDeleteOpen = false">取消</button>
        <button type="button" class="secondary danger" :disabled="saving" @click="deleteModule">{{ saving ? "正在删除…" : "确认删除" }}</button>
      </div>
    </div>
  </ElDialog>

  <ElDialog v-model="moduleEditorOpen" :title="moduleDraft.id ? '编辑模块' : '添加模块'" width="min(620px, 94vw)" :close-on-click-modal="false" :close-on-press-escape="!saving" :show-close="!saving">
    <form class="project-form" @submit.prevent="saveModule">
      <div class="form-pair">
        <label>模块名称 <input v-model="moduleDraft.name" required maxlength="60" /></label>
        <label>模块类别
          <select v-model="moduleDraft.kind">
            <option v-for="(label, kind) in pipelineKinds" :key="kind" :value="kind">{{ label }}</option>
          </select>
        </label>
      </div>
      <div class="form-pair">
        <label>代码仓库
          <select v-model="moduleDraft.repositoryId">
            <option value="">未登记仓库</option>
            <option v-for="(repository, index) in project?.repositories" :key="repository.id" :value="repository.id">{{ index === 0 ? '主仓库' : repository.name }}</option>
            <option :value="separateRepository">登记其他仓库</option>
          </select>
        </label>
        <label>源码目录 <input v-model="moduleDraft.directory" placeholder="例如 platform-api，根目录填 ." /></label>
      </div>
      <div v-if="moduleDraft.repositoryId === separateRepository" class="form-pair">
        <label>仓库地址 <input v-model="moduleDraft.repositoryUrl" required /></label>
        <label>Git 分支 <input v-model="newRepositoryBranch" placeholder="main" /></label>
      </div>
      <p v-if="formError" class="project-error" role="alert">{{ formError }}</p>
      <div class="project-actions form-actions">
        <button type="button" class="secondary" :disabled="saving" @click="moduleEditorOpen = false">取消</button>
        <button type="submit" class="primary" :disabled="saving || !moduleDraft.name.trim()">{{ saving ? "正在保存…" : "保存模块" }}</button>
      </div>
    </form>
  </ElDialog>

  <ElDialog v-model="inspectionOpen" :title="inspectionModuleId ? '识别模块环境' : '识别源码模块'" width="min(700px, 94vw)" :close-on-click-modal="false" :close-on-press-escape="!saving" :show-close="!saving" @close="closeInspection">
    <div class="project-form">
      <p v-if="sourcesLoading" role="status">正在读取识别来源…</p>
      <p v-else-if="!inspectionSources.length && !formError">控制器还没有登记可识别的源码目录，可以手动添加模块。</p>
      <form v-else-if="inspectionSources.length" class="source-picker" @submit.prevent="inspect">
        <label>源码来源
          <select v-model="sourceId" required :disabled="inspecting || saving" @change="resetInspectionResults">
            <option value="" disabled>选择控制器上的源码</option>
            <option v-for="source in inspectionSources" :key="source.id" :value="source.id">{{ source.name }}</option>
          </select>
        </label>
        <button type="submit" class="secondary" :disabled="!sourceId || inspecting || saving">{{ inspecting ? "正在识别…" : "开始识别" }}</button>
      </form>
      <p v-if="formError" class="project-error" role="alert">{{ formError }}</p>
      <p v-if="inspected && !inspectionResults.length" role="status">没有识别到支持的模块，可以手动添加。</p>
      <label v-if="inspectionResults.length && !inspectionModuleId && project?.repositories.length">登记到仓库
        <select v-model="inspectionRepository" :disabled="saving" @change="selectNewDirectories">
          <option value="">未登记仓库</option>
          <option v-for="(repository, index) in project.repositories" :key="repository.id" :value="repository.id">{{ index === 0 ? '主仓库' : repository.name }}</option>
        </select>
      </label>
      <article v-for="item in inspectionResults" :key="item.directory" class="inspection-result">
        <div class="project-toolbar">
          <label v-if="!matchingModule(item.directory)" class="result-choice">
            <input v-model="selectedDirectories" type="checkbox" :value="item.directory" :disabled="saving" />
            {{ item.name }}
          </label>
          <strong v-else>{{ item.name }}</strong>
          <button v-if="matchingModule(item.directory)" type="button" class="quiet" :disabled="saving" @click="reviewSuggestion(item)">填入空项</button>
        </div>
        <small>{{ moduleKinds[item.kind] }} · <code>{{ item.directory }}</code></small>
        <p>{{ item.environment }}</p>
        <small v-if="item.evidence.length">识别依据 {{ item.evidence.join('、') }}</small>
      </article>
      <div class="project-actions form-actions">
        <button type="button" class="secondary" :disabled="saving" @click="inspectionOpen = false">关闭</button>
        <button v-if="selectedDirectories.length" type="button" class="primary" :disabled="saving" @click="addSuggestions">{{ saving ? "正在登记…" : '登记所选模块' }}</button>
      </div>
    </div>
  </ElDialog>
</template>

<style scoped>
.application-projects { display: grid; grid-template-columns: clamp(168px, 15vw, 236px) minmax(0,1fr); align-items: start; gap: 14px; min-width: 0; }
.project-nav, .project-content { min-width: 0; background: var(--surface); border: 1px solid var(--border); border-radius: var(--control-radius); }
.project-nav { padding: 12px; }
.project-nav header, .project-toolbar, .project-actions, .table-actions, .env-item, .page-controls { display:flex; align-items:center; gap:8px; }
.project-nav header, .project-toolbar { justify-content:space-between; }
.project-nav h3, .module-top h3 { margin:0; font-size:14px; }
.project-nav input, .project-nav select { width:100%; margin:6px 0 0; }
.nav-tools { border-top:1px solid var(--border-subtle); margin-top:12px; padding-top:6px; }
.nav-tools .quiet { font-size:12px; padding-inline:0; color:var(--muted); }
.project-nav nav { display:grid; gap:2px; margin-top:10px; }
.project-option { width:100%; display:block; padding:8px 10px; border:0; border-radius:3px; font:inherit; background:transparent; color:var(--text); text-align:left; overflow-wrap:anywhere; }
.project-option:hover { background:var(--bg-page); }
.project-option[aria-current] { color:var(--accent); background:var(--accent-soft); font-weight:600; }
.page-controls { margin-top:12px; justify-content:space-between; color:var(--muted); font-size:12px; }
.nav-note,.muted,.project-facts dt,.env-item span,.project-summary small {color:var(--muted);}
.nav-note { font-size:12px; }
.project-content { overflow:hidden; }
.project-summary { padding:16px 18px 12px; border-bottom:1px solid var(--border); }
.project-summary h2 { margin:0; font-size:19px; overflow-wrap:anywhere; }
.project-summary small { font-size:12px; font-weight:400; }
.project-description { margin:7px 0; white-space:pre-wrap; overflow-wrap:anywhere; }
.project-facts { display:flex; gap:6px 20px; align-items:baseline; flex-wrap:wrap; margin:10px 0 6px; }
.project-facts > div { display:flex; gap:8px; align-items:baseline; min-width:0; }
.project-facts dt,.project-facts dd { font-size:12px; }
.project-facts dd { margin:0; overflow-wrap:anywhere; }
.project-actions, .project-toolbar { flex-wrap:wrap; }
.project-actions .primary, .project-actions .secondary { margin:0; width:auto; }
.archive-confirm { margin-top:8px; }
.module-top { padding:12px 18px; }
.module-top h3 { margin-right:8px; }
.module-top input { width:180px; margin:0; }
.table-scroll { overflow:auto; }
.module-table { width:100%; min-width:630px; border-collapse:collapse; text-align:left; }
.module-table tr th,.module-table tr td { padding:10px 12px; border-bottom:1px solid var(--border-subtle); vertical-align:middle; font-size:13px; color:var(--text); }
.module-table thead th { color:var(--muted); font-weight:600; font-size:12px; background:var(--bg-page); border-block:1px solid var(--border); white-space:nowrap; }
.module-table tbody th { background:transparent; font-weight:600; text-align:left; }
.module-table code { font-size:12px; overflow-wrap:anywhere; }
.module-table tbody tr:last-child > * { border-bottom:0; }
.module-table .current-module { background:var(--accent-soft); }
.module-table tbody tr:hover { background:var(--bg-page); }
.table-actions { white-space:nowrap; }
.table-link { border:0; padding:2px; margin:0; background:transparent; color:var(--accent); font:inherit; font-size:12px; }
.table-link:hover { text-decoration:underline; }
.env-item { flex-wrap:wrap; gap:2px 6px; }
.project-empty { padding:24px 16px; margin:0; text-align:center; color:var(--muted); }
.project-error { margin:0; padding:8px 12px; border:1px solid #fecaca; border-radius:var(--control-radius); color:#991b1b; background:#fef2f2; overflow-wrap:anywhere; }
.project-form { display: grid; gap: 14px; min-width: 0; }
.project-form label { display: grid; gap: 6px; color: #334155; font-size: 13px; }
.project-form input:not([type="checkbox"]), .project-form select, .project-form textarea { width: 100%; min-width: 0; margin: 0; }
.project-form textarea { resize: vertical; }
.project-form p { margin: 0; }
.form-actions { justify-content: flex-end; margin-top: 4px; }
.form-pair { display: grid; gap: 12px; grid-template-columns: repeat(2, minmax(0, 1fr)); }
.source-picker { display: grid; grid-template-columns: minmax(0, 1fr) auto; align-items: end; gap: 10px; }
.inspection-result { display: grid; gap: 8px; min-width: 0; border: 1px solid var(--border); border-radius: var(--control-radius); padding: 12px; }
.inspection-result small { color: var(--muted); overflow-wrap: anywhere; }
.inspection-result .result-choice { display: flex; align-items: center; font-weight: 600; }
.result-choice input[type="checkbox"] { width: 15px; min-height: 0; height: 15px; margin: 0; padding: 0; }

@media (max-width: 780px) {
  .application-projects { grid-template-columns:minmax(0,1fr); gap:8px; }
  .project-nav { display:grid; grid-template-columns:minmax(0,1fr) auto; gap:6px; }
  .project-nav header,.project-nav nav,.page-controls,.nav-tools { grid-column:1/-1; }
  .project-nav nav { max-height:140px; overflow:auto; margin:0; }
  .project-nav input,.project-nav select { margin:0; }
}
@media (max-width:540px) {
  .project-summary,.module-top { padding:10px; }
  .form-pair,.source-picker { grid-template-columns:minmax(0,1fr); }
  .module-top input { width:140px; }
}
</style>
