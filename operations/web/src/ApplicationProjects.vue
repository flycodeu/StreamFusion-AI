<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref, watch } from "vue";
import { ElDialog } from "element-plus";
import { ApiError, request } from "./api";
import { hostTime } from "./hosts";
import OpsPagination from "./OpsPagination.vue";
import ProjectEnvironmentBinding from "./ProjectEnvironmentBinding.vue";
import ProjectJenkinsBuild from "./ProjectJenkinsBuild.vue";
import TerminalCommandEntry from "./TerminalCommandEntry.vue";
import {
  jenkinsLink, moduleDirectory, moduleEdit, moduleKinds, newModuleDraft, projectListPath, repositoryId,
  type ApplicationProject, type ApplicationProjectPage, type InspectedModule,
  type InspectionSource, type ProjectEnvironment, type ProjectModule, type ProjectServiceRegistration,
} from "./applicationProjects";

const emit = defineEmits<{ error: [unknown]; "install-jenkins": []; "view-instances": [projectId: string, moduleId: string] }>();
const rootPath = "/application-projects";
const records = ref<ApplicationProject[]>([]);
const page = ref(1), pageSize = ref(10), total = ref(0);
const query = ref(""), archived = ref("false");
const loading = ref(false), opening = ref(false), saving = ref(false);
const listError = ref(""), formError = ref(""), archiveTarget = ref("");
const project = ref<ApplicationProject>();
const selectedModuleId = ref("");
const selectedModule = computed(() => project.value?.modules.find(item => item.id === selectedModuleId.value));
const primaryRepository = computed(() => project.value?.repositories[0]);
const selectedRepository = computed(() => project.value?.repositories.find(item => item.id === selectedModule.value?.repositoryId));
const selectedRegistrations = computed(() => (project.value?.environments || []).flatMap(environment =>
  environment.deployments.filter(registration => registration.moduleId === selectedModuleId.value).map(registration => ({ environment, registration })),
));
const environmentBinding = ref<{ moduleId: string; environment?: ProjectEnvironment; registration?: ProjectServiceRegistration }>();

const projectEditorOpen = ref(false), moduleEditorOpen = ref(false), projectDeleteOpen = ref(false), moduleDeleteOpen = ref(false);
const projectDraft = ref({ name: "", description: "", repositoryUrl: "", jenkinsUrl: "" });
const projectDeleteTarget = ref<ApplicationProject>();
const moduleDeleteTarget = ref<ProjectModule>();
const moduleDraft = ref(newModuleDraft());
const separateRepository = "new-repository";

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
  loading.value = true;
  listError.value = "";
  try {
    const data = await request<ApplicationProjectPage>(projectListPath(page.value, pageSize.value, query.value, archived.value), "GET", undefined, controller.signal);
    if (controller.signal.aborted) return;
    const last = Math.max(1, Math.ceil(data.total / pageSize.value));
    if (page.value > last) { page.value = last; return; }
    records.value = data.items;
    total.value = data.total;
  } catch (error) {
    if (!controller.signal.aborted) listError.value = describeError(error, "项目列表读取失败，请刷新后重试");
  } finally {
    if (listController === controller) loading.value = false;
  }
}

function showProject(record: ApplicationProject, moduleId = record.modules[0]?.id ?? "") {
  project.value = record;
  selectedModuleId.value = moduleId;
}

async function openProject(record: ApplicationProject) {
  projectController?.abort();
  const controller = projectController = new AbortController();
  opening.value = true;
  listError.value = "";
  try {
    const detail = await request<ApplicationProject>(`${rootPath}/${record.id}`, "GET", undefined, controller.signal);
    if (!controller.signal.aborted) showProject(detail);
  } catch (error) {
    if (!controller.signal.aborted) listError.value = describeError(error, "项目详情读取失败，请重新打开");
  } finally {
    if (controller === projectController) opening.value = false;
  }
}

function backToProjects() {
  project.value = undefined;
  formError.value = "";
  void load();
}

function editProject() {
  const record = project.value;
  projectDraft.value = {
    name: record?.name ?? "",
    description: record?.description ?? "",
    repositoryUrl: primaryRepository.value?.url ?? "",
    jenkinsUrl: record?.jenkinsUrl ?? "",
  };
  formError.value = "";
  projectEditorOpen.value = true;
}

async function saveProject() {
  if (saving.value || !projectDraft.value.name.trim()) return;
  const { name, description, repositoryUrl, jenkinsUrl } = projectDraft.value;
  if (jenkinsUrl.trim() && !jenkinsLink(jenkinsUrl.trim())) {
    formError.value = "请输入 Jenkins 作业的完整 HTTP 或 HTTPS 地址，去掉登录信息和 URL 参数";
    return;
  }
  const record = project.value;
  const primary = primaryRepository.value;
  const repoChanged = repositoryUrl.trim() && repositoryUrl.trim() !== primary?.url;
  saving.value = true;
  formError.value = "";
  try {
    const saved = await request<ApplicationProject>(record ? `${rootPath}/${record.id}/catalog` : `${rootPath}/catalog`, record ? "PATCH" : "POST", {
      version: record?.version,
      name: name.trim(),
      description: description.trim(),
      jenkinsUrl: jenkinsUrl.trim(),
      repositories: repoChanged ? [{ id: primary?.id, name: primary?.name ?? "主仓库", url: repositoryUrl.trim(), branch: primary?.branch ?? "" }] : undefined,
    });
    showProject(saved, record ? selectedModuleId.value : undefined);
    projectEditorOpen.value = false;
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
      formError.value = ["请先在「应用实例」解除该模块关联的环境或服务实例", error.code, error.traceId, error.timestamp].filter(Boolean).join(" · ");
    } else {
      formError.value = describeError(error, "模块删除失败，请刷新后重试");
    }
  } finally { saving.value = false; }
}

function editModule(item?: ProjectModule) {
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
  if (await saveModules([moduleEdit(draft, id)], existing ? undefined : [{ id, name: draft.name.trim(), url, branch: "" }])) {
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
onMounted(load);
onUnmounted(() => {
  listController?.abort();
  projectController?.abort();
  inspectionController?.abort();
  if (searchTimer) clearTimeout(searchTimer);
});
</script>

<template>
  <section class="application-projects" aria-label="项目管理">
    <template v-if="!project">
      <p v-if="listError" class="project-error" role="alert">{{ listError }}</p>
      <section class="project-list panel-card" :aria-busy="loading || opening" aria-label="项目目录">
        <header class="project-toolbar directory-toolbar">
          <div class="project-actions project-filters">
            <input v-model="query" type="search" placeholder="搜索项目名称或代码" aria-label="搜索项目" />
            <select v-model="archived" aria-label="项目状态">
              <option value="false">使用中的项目</option>
              <option value="true">已归档</option>
              <option value="all">全部项目</option>
            </select>
          </div>
          <div class="project-actions">
            <button type="button" class="secondary" :disabled="loading" @click="load">刷新</button>
            <button type="button" class="primary" :disabled="opening || saving" @click="editProject">新建项目</button>
          </div>
        </header>
        <div class="project-list-body">
          <table class="project-table" aria-label="项目列表">
            <thead><tr><th scope="col">项目</th><th scope="col">模块</th><th scope="col">更新时间</th><th scope="col">操作</th></tr></thead>
            <tbody>
              <tr v-for="record in records" :key="record.id" class="project-row">
                <th class="project-identity" scope="row">
                  <div class="project-row-title">
                    <button type="button" class="project-name" :disabled="opening || saving" @click="openProject(record)">{{ record.name }}</button>
                    <small v-if="record.archived" class="project-state">已归档</small>
                  </div>
                  <p v-if="record.description">{{ record.description }}</p>
                </th>
                <td class="project-meta">{{ record.modules.length }} 个模块</td>
                <td class="project-meta"><time :datetime="record.updatedAt">{{ hostTime(record.updatedAt) }}</time></td>
                <td>
                  <div v-if="archiveTarget === record.id" class="project-actions row-actions">
                    <button type="button" class="quiet" :disabled="saving" @click="archiveTarget = ''">取消</button>
                    <button type="button" class="quiet" :disabled="saving" @click="setArchived(record, true)">确认归档</button>
                  </div>
                  <div v-else class="project-actions row-actions">
                    <a v-if="jenkinsLink(record.jenkinsUrl)" class="project-link" :href="jenkinsLink(record.jenkinsUrl)" target="_blank" rel="noopener noreferrer">打开 Jenkins ↗</a>
                    <button type="button" class="quiet" :disabled="opening || saving" @click="openProject(record)">详情</button>
                    <button v-if="record.archived" type="button" class="quiet" :disabled="saving" @click="setArchived(record, false)">恢复使用</button>
                    <button v-else type="button" class="quiet" :disabled="saving" @click="archiveTarget = record.id">归档</button>
                    <button type="button" class="quiet danger" :disabled="opening || saving" @click="confirmProjectDeletion(record)">删除</button>
                  </div>
                </td>
              </tr>
            </tbody>
          </table>
          <p v-if="!records.length" class="project-empty" role="status">{{ loading ? "正在读取项目…" : query || archived === "true" ? "没有符合条件的项目" : "暂无项目" }}</p>
        </div>
        <footer class="project-pagination"><OpsPagination v-model:page="page" v-model:page-size="pageSize" :total="total" :disabled="loading" label="项目分页" /></footer>
      </section>
    </template>

    <template v-else>
      <button type="button" class="quiet back-link" @click="backToProjects">‹ 返回项目</button>
      <header class="project-summary panel-card">
        <div class="project-toolbar">
          <h2>{{ project.name }}<small v-if="project.archived" class="project-state">已归档</small></h2>
          <div class="project-actions">
            <button type="button" class="secondary" @click="editProject">编辑项目</button>
            <a v-if="jenkinsLink(project.jenkinsUrl)" class="primary project-link" :href="jenkinsLink(project.jenkinsUrl)" target="_blank" rel="noopener noreferrer">打开 Jenkins ↗</a>
            <button v-else type="button" class="quiet" @click="emit('install-jenkins')">安装 Jenkins</button>
          </div>
        </div>
        <p v-if="project.description" class="project-description">{{ project.description }}</p>
        <dl v-if="primaryRepository" class="project-facts">
          <div><dt>主仓库</dt><dd><code>{{ primaryRepository.url }}</code></dd></div>
          <div v-if="primaryRepository.branch"><dt>分支</dt><dd><code>{{ primaryRepository.branch }}</code></dd></div>
        </dl>
      </header>

      <section class="module-section panel-card" aria-label="项目模块">
        <div class="project-toolbar module-top">
          <h3>项目模块 <span>{{ project.modules.length }}</span></h3>
          <div class="project-actions">
            <button type="button" class="secondary" @click="openInspection()">识别模块</button>
            <button type="button" class="primary" @click="editModule()">添加模块</button>
          </div>
        </div>
        <p v-if="!selectedModule" class="project-empty">可以从源码识别模块，也可以手动填写。</p>
        <div v-else class="module-workspace">
          <div class="module-list">
            <table class="module-table" aria-label="模块列表">
              <thead><tr><th scope="col">模块</th><th scope="col">源码目录</th><th scope="col">运行方式</th></tr></thead>
              <tbody>
                <tr v-for="item in project.modules" :key="item.id" :class="{ 'current-module': item.id === selectedModule.id }" @click="selectedModuleId = item.id">
                  <th scope="row">
                    <button type="button" class="module-name" :aria-current="item.id === selectedModule.id ? 'true' : undefined" @click.stop="selectedModuleId = item.id">{{ item.name }}</button>
                    <small>{{ item.catalog?.kind ? (moduleKinds[item.catalog.kind] || "其他") : "待分类" }}</small>
                  </th>
                  <td><code>{{ item.directory || "." }}</code></td>
                  <td>{{ item.catalog?.runtimeMode || "待确认" }}</td>
                </tr>
              </tbody>
            </table>
          </div>
          <article class="module-detail" aria-label="模块详情">
            <div class="project-toolbar">
              <h4>{{ selectedModule.name }}</h4>
              <div class="project-actions">
                <button type="button" class="quiet" @click="openInspection(selectedModule)">识别此模块</button>
                <button type="button" class="secondary" @click="editModule(selectedModule)">编辑模块</button>
              </div>
            </div>
            <dl class="module-facts">
              <div><dt>源码目录</dt><dd><code>{{ selectedModule.directory || "." }}</code></dd></div>
              <div><dt>线上运行方式</dt><dd>{{ selectedModule.catalog?.runtimeMode || "待确认" }}</dd></div>
              <div v-if="selectedRepository" class="full-fact"><dt>代码仓库</dt><dd><code>{{ selectedRepository.url }}</code></dd></div>
              <div class="full-fact"><dt>开发环境</dt><dd>{{ selectedModule.catalog?.environment || "待填写" }}</dd></div>
            </dl>
            <div v-if="selectedRegistrations.length" class="project-actions bound-environments">
              <strong>环境绑定</strong>
              <button v-for="row in selectedRegistrations" :key="row.registration.id" class="secondary" type="button"
                :aria-label="'编辑环境 ' + row.environment.name"
                @click="environmentBinding = { moduleId: selectedModule.id, ...row }">{{ row.environment.name }}</button>
            </div>
            <footer class="project-actions module-actions">
              <button type="button" class="primary" :disabled="saving" @click="emit('view-instances', project.id, selectedModule.id)">打开实例工作台</button>
              <button v-if="!project.archived" type="button" class="secondary" :disabled="saving" @click="environmentBinding = { moduleId: selectedModule.id }">绑定环境</button>
              <button type="button" class="quiet danger" :disabled="saving" @click="confirmModuleDeletion(selectedModule)">删除模块</button>
            </footer>
          </article>
        </div>
      </section>

      <ProjectJenkinsBuild
        v-if="jenkinsLink(project.jenkinsUrl)"
        :key="`${project.id}:${project.jenkinsUrl}`"
        :project-id="project.id"
        :job-url="jenkinsLink(project.jenkinsUrl)"
        @error="emit('error', $event)"
      />
    </template>
  </section>

  <ElDialog v-model="projectEditorOpen" :title="project ? '编辑项目' : '新建项目'" width="min(540px, 94vw)" :close-on-click-modal="false" :close-on-press-escape="!saving" :show-close="!saving">
    <form class="project-form" @submit.prevent="saveProject">
      <label>项目名称 <input v-model="projectDraft.name" required maxlength="60" /></label>
      <label>项目简介 <textarea v-model="projectDraft.description" rows="2" maxlength="1000" placeholder="选填" /></label>
      <label>主仓库地址 <input v-model="projectDraft.repositoryUrl" :required="!!primaryRepository" placeholder="例如 https://git.example.com/team/project.git" /></label>
      <label>Jenkins 作业链接 <input v-model="projectDraft.jenkinsUrl" type="url" placeholder="例如 https://jenkins.example.com/job/project/" /></label>
      <p v-if="formError" class="project-error" role="alert">{{ formError }}</p>
      <div class="project-actions form-actions">
        <button type="button" class="secondary" :disabled="saving" @click="projectEditorOpen = false">取消</button>
        <button type="submit" class="primary" :disabled="saving || !projectDraft.name.trim()">{{ saving ? "正在保存…" : "保存项目" }}</button>
      </div>
    </form>
  </ElDialog>

  <ProjectEnvironmentBinding v-if="environmentBinding && project" :project="project" :module-id="environmentBinding.moduleId"
    :environment="environmentBinding.environment" :registration="environmentBinding.registration"
    @saved="showProject($event, selectedModuleId)" @close="environmentBinding = undefined" @error="emit('error', $event)" />

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
        <button v-if="project && moduleDeleteTarget && formError.includes('APPLICATION_MODULE_IN_USE')" type="button" class="secondary" :disabled="saving" @click="emit('view-instances', project.id, moduleDeleteTarget.id)">查看关联实例</button>
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
            <option v-for="(label, kind) in moduleKinds" :key="kind" :value="kind">{{ label }}</option>
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
      <label v-if="moduleDraft.repositoryId === separateRepository">仓库地址 <input v-model="moduleDraft.repositoryUrl" required /></label>
      <label>开发环境 <input v-model="moduleDraft.environment" placeholder="例如 Java 21、Spring Boot、Maven Wrapper" /></label>
      <label>开发启动命令 <textarea v-model="moduleDraft.startCommand" class="command-input" rows="3" spellcheck="false" placeholder="在模块目录中执行的命令" /></label>
      <label>线上运行方式 <input v-model="moduleDraft.runtimeMode" placeholder="待确认，例如 JAR、容器、静态站点" /></label>
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
      <p v-if="inspected && !inspectionResults.length" role="status">没有找到支持的源码清单，可以手动填写环境和启动命令。</p>
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
        <TerminalCommandEntry v-if="item.startCommand" label="开发启动" :command="item.startCommand" />
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
.application-projects { display: grid; gap: 16px; min-width: 0; container: project-content / inline-size; }
.application-projects :deep(.ops-pagination) { margin-top: 0; }
.project-toolbar, .project-actions { display: flex; align-items: center; flex-wrap: wrap; gap: 8px 12px; }
.project-toolbar { justify-content: space-between; }
.project-toolbar input, .project-toolbar select { width: auto; margin: 0; }
.project-list { padding: 0; overflow: hidden; }
.directory-toolbar { padding: 12px 18px; border-bottom: 1px solid var(--border); }
.project-filters input { min-width: 13rem; }
.project-list-body { max-height: max(160px, calc(100dvh - 220px)); overflow: auto; }
.project-table, .module-table { width: 100%; min-width: 0; border-collapse: separate; border-spacing: 0; text-align: left; }
.project-table { table-layout: fixed; }
.project-table thead th, .module-table thead th { position: sticky; top: 0; z-index: 1; padding-block: 11px; color: #475569; background: #f8fafc; font-size: 12px; font-weight: 600; }
.project-table th, .project-table td { padding: 10px 18px; border-bottom: 1px solid var(--border-subtle); }
.project-table tbody tr:last-child > * { border-bottom: 0; }
.project-table thead th:nth-child(2) { width: 88px; }
.project-table thead th:nth-child(3) { width: 148px; }
.project-table thead th:last-child { width: 264px; text-align: right; }
.project-table th:nth-child(2), .project-table th:nth-child(3), .project-meta { white-space: nowrap; }
.project-table tbody th, .module-table tbody th { background: transparent; color: inherit; white-space: normal; }
.project-table tbody tr:hover { background: #f8fafc; }
.project-pagination { padding: 9px 18px; border-top: 1px solid var(--border); }
.project-identity { min-width: 0; font-weight: 400; }
.project-toolbar h2, .project-toolbar h3, .project-toolbar h4 { margin: 0; overflow-wrap: anywhere; }
.project-row-title { display: flex; align-items: center; flex-wrap: wrap; gap: 4px 8px; }
.project-identity p, .project-meta, .project-state { color: var(--muted); font-size: 12px; line-height: 1.6; }
.project-identity p { margin: 3px 0 0; overflow-wrap: anywhere; display: -webkit-box; -webkit-line-clamp: 2; -webkit-box-orient: vertical; overflow: hidden; }
.project-name, .module-name { padding: 0; border: 0; background: transparent; color: inherit; font-size: 14px; font-weight: 600; line-height: 1.5; text-align: left; overflow-wrap: anywhere; }
.project-name:hover, .module-name:hover, .project-link { color: var(--accent); }
.project-link { font-size: 13px; font-weight: 500; text-decoration: none; white-space: nowrap; }
.project-link:hover { text-decoration: underline; }
.project-link.primary { display: inline-flex; align-items: center; color: white; }
.row-actions { justify-content: flex-end; gap: 2px 6px; }
.row-actions button { margin: 0; padding-inline: 6px; white-space: nowrap; }
.project-empty { margin: 0; padding: 36px 16px; text-align: center; color: var(--muted); }
.project-error { margin: 0; padding: 9px 12px; border: 1px solid #fecaca; border-radius: var(--control-radius); color: #991b1b; background: #fef2f2; overflow-wrap: anywhere; }
.back-link { justify-self: start; padding-left: 0; }
.project-summary { padding: 18px 22px; }
.project-summary h2 { font-size: 22px; line-height: 1.4; }
.project-summary .project-state { margin-left: 8px; }
.project-state { border: 1px solid var(--border); border-radius: 20px; padding: 2px 8px; background: #f8fafc; font-weight: 400; }
.project-description { margin: 8px 0 0; line-height: 1.6; white-space: pre-wrap; overflow-wrap: anywhere; }
.project-facts { display: flex; gap: 10px 22px; flex-wrap: wrap; margin: 14px 0 0; padding-top: 12px; border-top: 1px solid var(--border-subtle); }
.project-facts > div { display: flex; flex-wrap: wrap; align-items: baseline; gap: 4px 8px; min-width: 0; }
.project-facts dt { color: var(--muted); font-size: 12px; }
.project-facts dd { margin: 0; min-width: 0; overflow-wrap: anywhere; font-size: 13px; }
.module-section { padding: 0; overflow: hidden; }
.module-top { padding: 14px 20px; border-bottom: 1px solid var(--border); }
.module-top h3 { font-size: 16px; }
.module-top h3 span { margin-left: 6px; color: var(--muted); font-size: 12px; font-weight: 400; }
.module-workspace { display: grid; grid-template-columns: minmax(0, 1.15fr) minmax(270px, 1fr); }
.module-list { max-height: clamp(280px, 55dvh, 520px); min-width: 0; overflow: auto; border-right: 1px solid var(--border); }
.module-table { table-layout: fixed; }
.module-table th, .module-table td { padding: 9px 12px; overflow-wrap: anywhere; border-bottom: 1px solid var(--border-subtle); }
.module-table th:first-child { width: 40%; padding-left: 19px; }
.module-table td { color: #475569; font-size: 12px; white-space: normal; }
.module-table tbody th { font-weight: 400; }
.module-table small { display: block; margin-top: 2px; font-size: 11px; }
.module-table tbody tr { cursor: pointer; }
.module-table tbody tr:hover, .module-table tbody tr:focus-within { background: #f8fafc; }
.module-table tbody tr.current-module { background: var(--accent-soft); }
.current-module th { box-shadow: inset 3px 0 var(--accent); }
.module-name[aria-current] { color: var(--accent); }
.module-table small, .module-facts dt { color: var(--muted); }
.module-detail { min-width: 0; padding: 16px 20px; }
.module-detail h4 { font-size: 16px; }
.module-detail .project-actions { gap: 4px 6px; }
.module-actions { justify-content: space-between; margin-top: 16px; padding-top: 12px; border-top: 1px solid var(--border-subtle); }
.module-actions .danger { margin-left: auto; }
.bound-environments { margin-top: 14px; }
.bound-environments strong { font-size: 13px; }
.module-facts { display: grid; gap: 14px 18px; grid-template-columns: repeat(2, minmax(0, 1fr)); margin: 14px 0; padding: 14px 0; border-top: 1px solid var(--border-subtle); border-bottom: 1px solid var(--border-subtle); }
.module-facts .full-fact { grid-column: 1 / -1; }
.module-facts dt { font-size: 12px; }
.module-facts dd { margin: 4px 0 0; white-space: pre-wrap; overflow-wrap: anywhere; }
.project-form { display: grid; gap: 14px; min-width: 0; }
.project-form label { display: grid; gap: 6px; color: #334155; font-size: 13px; }
.project-form input:not([type="checkbox"]), .project-form select, .project-form textarea { width: 100%; min-width: 0; margin: 0; }
.project-form textarea { resize: vertical; }
.project-form .command-input { font-family: var(--mono); }
.project-form p { margin: 0; }
.form-actions { justify-content: flex-end; margin-top: 4px; }
.form-pair { display: grid; gap: 12px; grid-template-columns: repeat(2, minmax(0, 1fr)); }
.source-picker { display: grid; grid-template-columns: minmax(0, 1fr) auto; align-items: end; gap: 10px; }
.inspection-result { display: grid; gap: 8px; min-width: 0; border: 1px solid var(--border); border-radius: var(--control-radius); padding: 12px; }
.inspection-result small { color: var(--muted); overflow-wrap: anywhere; }
.inspection-result .result-choice { display: flex; align-items: center; font-weight: 600; }
.result-choice input[type="checkbox"] { width: 15px; min-height: 0; height: 15px; margin: 0; padding: 0; }

@container project-content (max-width: 730px) {
  .project-list-body { max-height: max(160px, calc(100dvh - 270px)); }
  .project-table, .project-table tbody { display: block; }
  .project-table thead { position: absolute; width: 1px; height: 1px; clip-path: inset(50%); overflow: hidden; }
  .project-row { display: grid; grid-template-columns: auto minmax(0, 1fr); gap: 6px 12px; padding: 12px 16px; }
  .project-row + .project-row { border-top: 1px solid var(--border); }
  .project-row th, .project-row td { padding: 0; border: 0; }
  .project-row .project-identity, .project-row td:last-child { grid-column: 1 / -1; }
  .project-identity { width: auto; }
  .project-meta { white-space: normal; }
  .row-actions { justify-content: flex-start; }
  .module-workspace { grid-template-columns: minmax(0, 1fr); }
  .module-list { max-height: 282px; border-right: 0; border-bottom: 1px solid var(--border); }
}
@media (max-width: 540px) {
  .directory-toolbar, .module-top, .project-summary, .module-detail, .project-pagination { padding-inline: 12px; }
  .project-summary h2 { font-size: 19px; }
  .project-filters, .project-toolbar input[type="search"] { min-width: 0; width: 100%; }
  .directory-toolbar .project-filters { display: contents; }
  .directory-toolbar input[type="search"] { flex-basis: 100%; }
  .module-table th, .module-table td { padding-inline: 8px; }
  .module-table th:first-child { padding-left: 12px; }
  .form-pair { grid-template-columns: minmax(0, 1fr); }
}
</style>
