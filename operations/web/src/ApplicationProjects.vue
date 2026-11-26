<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref, watch } from "vue";
import {
  ElAlert,
  ElButton,
  ElCheckbox,
  ElCheckboxGroup,
  ElDialog,
  ElDrawer,
  ElDropdown,
  ElDropdownItem,
  ElDropdownMenu,
  ElEmpty,
  ElForm,
  ElFormItem,
  ElInput,
  ElInputNumber,
  ElOption,
  ElPagination,
  ElRadioButton,
  ElRadioGroup,
  ElSelect,
  ElTabPane,
  ElTable,
  ElTableColumn,
  ElTabs,
  ElTag,
} from "element-plus";
import { ApiError, request } from "./api";
import type { Host } from "./hosts";
import type { LogSelection } from "./jobs";
import type { SoftwareInstance } from "./software";
import ProjectEnvironmentBinding from "./ProjectEnvironmentBinding.vue";
import ProjectJenkinsConnection from "./ProjectJenkinsConnection.vue";
import ProjectServiceInstances from "./ProjectServiceInstances.vue";
import {
  deployModeText,
  evaluateDependencyReadiness,
  jenkinsLink,
  moduleEdit,
  moduleKinds,
  newModuleDraft,
  projectListPath,
  repositoryId,
  type ApplicationProject,
  type ApplicationProjectPage,
  type InspectedModule,
  type ModuleKind,
  type ProjectEnvironment,
  type ProjectGitAccount,
  type ProjectInstanceSource,
  type ProjectModule,
  type ProjectReleaseSource,
  type ProjectServiceRegistration,
} from "./applicationProjects";
import { useProjectInspection } from "./features/project-delivery/useProjectInspection";

const props = defineProps<{ source?: ProjectInstanceSource; active?: boolean }>();
const emit = defineEmits<{
  "source-used": [];
  error: [unknown];
  "install-jenkins": [];
  "install-software": [payload: { search: string; hostId?: string }];
  "legacy-release": [ProjectReleaseSource, "publish" | "verify"];
  "legacy-tools": [page: "managed-instances" | "artifacts"];
  logs: [LogSelection];
}>();
const rootPath = "/application-projects";
const records = ref<ApplicationProject[]>([]);
const page = ref(1),
  pageSize = ref(10),
  total = ref(0);
const query = ref(""),
  queryInput = ref(""),
  archived = ref("false");
const loading = ref(false), saving = ref(false);
const listError = ref(""),
  formError = ref(""),
  archiveTarget = ref("");
const project = ref<ApplicationProject>();
const moduleDrawerOpen = ref(false);
const projectTab = ref<"modules" | "delivery">("modules");
const deliverySource = ref<ProjectInstanceSource>();
const selectedModuleId = ref("");
const primaryRepository = computed(() => project.value?.repositories[0]);
const hostNames = ref<Record<string, string>>({});
const projectHosts = ref<Host[]>([]);
const gitAccounts = ref<ProjectGitAccount[]>([]);
const softwareList = ref<SoftwareInstance[]>([]);
const moduleQuery = ref("");
const moduleEditorTab = ref<"basic" | "build">("basic");
const visibleModules = computed(() =>
  (project.value?.modules || []).filter((item) =>
    [item.name, item.directory, item.catalog?.description || "", item.catalog?.environment || ""].some(
      (text) => text.toLowerCase().includes(moduleQuery.value.trim().toLowerCase()),
    ),
  ),
);
function moduleEnvironments(moduleId: string) {
  return (project.value?.environments || []).flatMap((environment) =>
    environment.deployments
      .filter((registration) => registration.moduleId === moduleId)
      .map((registration) => ({ environment, registration })),
  );
}
const environmentBinding = ref<{
  moduleId: string;
  environment?: ProjectEnvironment;
  registration?: ProjectServiceRegistration;
  addingAlgorithmHost?: boolean;
}>();

const projectEditorOpen = ref(false),
  moduleEditorOpen = ref(false),
  projectDeleteOpen = ref(false),
  moduleDeleteOpen = ref(false);
const editingProject = ref<ApplicationProject>();
const projectDraft = ref({
  code: "",
  name: "",
  description: "",
  repositoryUrl: "",
  branch: "",
  gitAccountId: "",
  jenkinsCredentialId: "",
});
const projectDeleteTarget = ref<ApplicationProject>();
const moduleDeleteTarget = ref<ProjectModule>();
const moduleDraft = ref(newModuleDraft());
const separateRepository = "new-repository";
const newRepositoryBranch = ref("main");
const newRepositoryJenkinsCredential = ref("");
const pipelineKinds = {
  BACKEND: "后端 · Java / Maven",
  FRONTEND: "前端 · Node.js",
  ALGORITHM: "算法 · Python",
  OTHER: "其他（无内置流水线）",
};
const environmentPresets = [
  "JDK 21、Spring Boot、Maven",
  "JDK 17、Spring Boot、Maven",
  "Node.js 24、pnpm、Vue、Vite",
  "Node.js 22、npm、React",
  "Python 3.12、uv",
];
const dependencyPresets = [
  "MySQL",
  "Redis",
  "Nginx",
  "SeaweedFS",
  "Java 21",
  "Maven",
  "Node.js",
  "Python",
  "uv",
  "Docker",
  "FFmpeg",
  "ZLMediaKit",
  "MediaMTX",
];
const profilePresets = ["dev", "test", "stage", "prod"];
const runtimeModePresets = [
  { label: "原生 systemd 进程", value: "NATIVE" },
  { label: "Docker 容器", value: "DOCKER" },
  { label: "Nginx 静态站点", value: "STATIC" },
];
function runtimeModeText(mode?: string) {
  return deployModeText(mode);
}
function missingDependenciesForModule(mod: ProjectModule) {
  const envs = moduleEnvironments(mod.id);
  const hostId = envs[0]?.registration.hostId || "";
  if (!hostId || !softwareList.value.length) return [];
  return evaluateDependencyReadiness(mod.catalog?.dependencies, hostId, softwareList.value)
    .filter((item) => !item.satisfied)
    .map((item) => ({ ...item, hostId }));
}
function moduleComputedUrls(mod: ProjectModule) {
  const urls: { label: string; url: string }[] = [];
  for (const row of moduleEnvironments(mod.id)) {
    const u = row.registration.computedAccessUrl || "";
    if (u && jenkinsLink(u)) {
      urls.push({ label: row.environment.name, url: u });
    }
  }
  if (!urls.length && mod.catalog?.accessUrl && jenkinsLink(mod.catalog.accessUrl)) {
    urls.push({ label: "", url: mod.catalog.accessUrl });
  }
  return urls;
}
function moduleDeployModeDisplay(mod: ProjectModule) {
  const modes = Array.from(
    new Set(
      moduleEnvironments(mod.id)
        .map((row) => row.registration.deployMode)
        .filter((v): v is string => !!v),
    ),
  );
  if (modes.length) return modes.map((m) => runtimeModeText(m)).join(" / ");
  return runtimeModeText(mod.catalog?.runtimeMode);
}
function projectCodeDisplay(record: ApplicationProject) {
  if (!record.code || record.code.startsWith("project-")) return "—";
  return record.code;
}
function projectEnvironmentsSummary(record: ApplicationProject) {
  const seen = new Set<string>();
  const items: string[] = [];
  for (const env of record.environments || []) {
    for (const dep of env.deployments || []) {
      const host = hostNames.value[dep.hostId];
      const text = host ? `${env.name} · ${host}` : env.name;
      if (!seen.has(text)) {
        seen.add(text);
        items.push(text);
      }
    }
  }
  return items;
}

const {
  inspectionOpen,
  sourcesLoading,
  inspecting,
  inspected,
  inspectionMode,
  inspectionRepoUrl,
  inspectionBranch,
  inspectionGitAccountId,
  inspectionModuleId,
  sourceId,
  inspectionRepository,
  inspectionRepoMismatch,
  localRegistrationRepository,
  inspectionSources,
  inspectionResults,
  selectedDirectories,
  matchingModule,
  syncInspectionRepoFields,
  openInspection,
  selectNewDirectories,
  inspect,
  reviewableModule,
  selectedModuleEdits,
} = useProjectInspection(project, saving, formError, describeError);
let listController: AbortController | undefined;
let projectController: AbortController | undefined;
let searchTimer: ReturnType<typeof setTimeout> | undefined;

function describeError(error: unknown, fallback: string) {
  if (error instanceof ApiError && error.code === "AUTH_REQUIRED")
    emit("error", error);
  const message =
    error instanceof Error && /[\u4e00-\u9fff]/.test(error.message)
      ? error.message
      : fallback;
  const details =
    error instanceof ApiError
      ? [error.code, error.traceId, error.timestamp].filter(Boolean)
      : [];
  return [message, ...details].join(" · ");
}

async function load() {
  listController?.abort();
  const controller = (listController = new AbortController());
  const focusRequested = props.source;
  loading.value = true;
  listError.value = "";
  try {
    const data = await request<ApplicationProjectPage>(
      projectListPath(page.value, pageSize.value, query.value, archived.value),
      "GET",
      undefined,
      controller.signal,
    );
    if (controller.signal.aborted) return;
    const last = Math.max(1, Math.ceil(data.total / pageSize.value));
    if (page.value > last) {
      page.value = last;
      return;
    }
    records.value = data.items;
    total.value = data.total;
    if (!focusRequested && !props.source && !projectController) {
      const next =
        data.items.find((item) => item.id === project.value?.id) || data.items[0];
      if (next)
        showProject(
          next,
          next.id === project.value?.id ? selectedModuleId.value : undefined,
        );
      else project.value = undefined;
    }
  } catch (error) {
    if (!controller.signal.aborted)
      listError.value = describeError(error, "项目列表读取失败，请刷新后重试");
  } finally {
    if (listController === controller) loading.value = false;
  }
}

function searchProjects() {
  query.value = queryInput.value;
  if (page.value !== 1) page.value = 1;
  else void load();
}
function clearProjectFilters() {
  queryInput.value = "";
  query.value = "";
  archived.value = "false";
  page.value = 1;
}

function showProject(
  record: ApplicationProject,
  moduleId = record.modules[0]?.id ?? "",
) {
  if (project.value?.id !== record.id) moduleQuery.value = "";
  project.value = record;
  if (records.value.some((item) => item.id === record.id))
    records.value = records.value.map((item) =>
      item.id === record.id ? record : item,
    );
  selectedModuleId.value =
    record.modules.find((item) => item.id === moduleId)?.id ||
    record.modules[0]?.id ||
    "";
}

async function openProject(record: { id: string }, moduleId?: string) {
  projectController?.abort();
  const controller = (projectController = new AbortController());
  listError.value = "";
  try {
    const detail = await request<ApplicationProject>(
      `${rootPath}/${record.id}`,
      "GET",
      undefined,
      controller.signal,
    );
    if (controller.signal.aborted) return false;
    showProject(detail, moduleId);
    return true;
  } catch (error) {
    if (!controller.signal.aborted)
      listError.value = describeError(error, "项目详情读取失败，请重新打开");
    return false;
  } finally {
    if (controller === projectController) projectController = undefined;
  }
}

async function openProjectDrawer(record: { id: string }, moduleId?: string) {
  projectTab.value = "modules";
  moduleDrawerOpen.value = true;
  await openProject(record, moduleId);
}

function openDelivery(moduleId: string, serviceInstanceId?: string) {
  if (!project.value) return;
  deliverySource.value = { projectId: project.value.id, moduleId, serviceInstanceId };
  projectTab.value = "delivery";
}

function editEnvironment(source: ProjectInstanceSource) {
  if (source.algorithmHostEnvironmentId) {
    const environment = project.value?.environments?.find((item) => item.id === source.algorithmHostEnvironmentId);
    const module = project.value?.modules.find((item) => item.id === source.moduleId);
    if (!environment || module?.catalog?.kind !== "ALGORITHM" ||
        !environment.deployments.some((row) => row.moduleId === source.moduleId && row.hostId)) return false;
    selectedModuleId.value = source.moduleId;
    environmentBinding.value = { moduleId: source.moduleId, environment, addingAlgorithmHost: true };
    return true;
  }
  const row = moduleEnvironments(source.moduleId).find(({ registration }) => registration.id === source.serviceInstanceId);
  if (!row) return false;
  selectedModuleId.value = source.moduleId;
  environmentBinding.value = { moduleId: source.moduleId, ...row };
  return true;
}

async function useSource(source: ProjectInstanceSource) {
  moduleQuery.value = "";
  environmentBinding.value = undefined;
  const opened = await openProject({ id: source.projectId }, source.moduleId);
  if (props.active === false || props.source !== source) return;
  if (opened && project.value?.id === source.projectId) {
    projectTab.value = "modules";
    moduleDrawerOpen.value = true;
    if (
      (source.serviceInstanceId || source.algorithmHostEnvironmentId) &&
      !editEnvironment(source)
    ) {
      emit("error", new Error("该运行配置已变更，请在项目详情中重新选择。"));
    }
  }
  emit("source-used");
}

function editProject(
  record: ApplicationProject | null | undefined = project.value,
) {
  editingProject.value = record ?? undefined;
  projectDraft.value = {
    code:
      record?.code && !record.code.startsWith("project-") ? record.code : "",
    name: record?.name ?? "",
    description: record?.description ?? "",
    repositoryUrl: record?.repositories[0]?.url ?? "",
    branch: record?.repositories[0]?.branch ?? "",
    gitAccountId: record?.repositories[0]?.gitAccountId ?? "",
    jenkinsCredentialId: record?.repositories[0]?.jenkinsCredentialId ?? "",
  };
  formError.value = "";
  projectEditorOpen.value = true;
}

async function saveProject() {
  if (saving.value || !projectDraft.value.name.trim()) return;
  const { code, name, description, repositoryUrl, branch, gitAccountId, jenkinsCredentialId } = projectDraft.value;
  const record = editingProject.value;
  const primary = record?.repositories[0];
  const accountChanged = gitAccountId !== (primary?.gitAccountId || "");
  const jenkinsCredentialChanged = jenkinsCredentialId.trim() !== (primary?.jenkinsCredentialId || "");
  const repoChanged =
    repositoryUrl.trim() &&
    (repositoryUrl.trim() !== primary?.url ||
      branch.trim() !== primary?.branch ||
      accountChanged ||
      jenkinsCredentialChanged);
  const cleanCode = code.trim().toLowerCase();
  const codeChanged = !!cleanCode && cleanCode !== (record?.code || "");
  saving.value = true;
  formError.value = "";
  try {
    const saved = await request<ApplicationProject>(
      record ? `${rootPath}/${record.id}/catalog` : `${rootPath}/catalog`,
      record ? "PATCH" : "POST",
      {
        version: record?.version,
        ...(codeChanged ? { code: cleanCode } : {}),
        name: name.trim(),
        description: description.trim(),
        repositories: repoChanged
          ? [
              {
                id: primary?.id,
                name: primary?.name ?? "主仓库",
                url: repositoryUrl.trim(),
                branch: branch.trim(),
                ...(accountChanged ? { gitAccountId } : {}),
                ...(jenkinsCredentialChanged ? { jenkinsCredentialId: jenkinsCredentialId.trim() } : {}),
              },
            ]
          : undefined,
      },
    );
    showProject(saved, record ? selectedModuleId.value : undefined);
    projectEditorOpen.value = false;
    if (!record) {
      query.value = "";
      queryInput.value = "";
      archived.value = "false";
      page.value = 1;
      await load();
      showProject(saved);
    }
  } catch (error) {
    formError.value = describeError(error, "项目保存失败，请检查填写内容后重试");
  } finally {
    saving.value = false;
  }
}

async function setArchived(record: ApplicationProject, value: boolean) {
  if (saving.value) return;
  saving.value = true;
  listError.value = "";
  try {
    await request(
      value
        ? `${rootPath}/${record.id}/archive`
        : `${rootPath}/${record.id}/catalog`,
      value ? "POST" : "PATCH",
      {
        version: record.version,
        ...(value ? {} : { archived: false }),
      },
    );
    archiveTarget.value = "";
    await load();
  } catch (error) {
    listError.value = describeError(error, "归档状态更新失败，请刷新列表后重试");
  } finally {
    saving.value = false;
  }
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
    await request(`${rootPath}/${target.id}`, "DELETE", {
      version: target.version,
    });
    projectDeleteOpen.value = false;
    if (project.value?.id === target.id) moduleDrawerOpen.value = false;
    await load();
  } catch (error) {
    formError.value = describeError(error, "项目删除失败，请刷新后重试");
  } finally {
    saving.value = false;
  }
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
    const saved = await request<ApplicationProject>(
      `${rootPath}/${record.id}/modules/${target.id}`,
      "DELETE",
      { version: record.version },
    );
    const selected = saved.modules.some(
      (item) => item.id === selectedModuleId.value,
    )
      ? selectedModuleId.value
      : undefined;
    showProject(saved, selected);
    moduleDeleteOpen.value = false;
  } catch (error) {
    if (error instanceof ApiError && error.code === "APPLICATION_MODULE_IN_USE") {
      formError.value = [
        "请先在「运行配置」移除该模块的环境",
        error.code,
        error.traceId,
        error.timestamp,
      ]
        .filter(Boolean)
        .join(" · ");
    } else {
      formError.value = describeError(error, "模块删除失败，请刷新后重试");
    }
  } finally {
    saving.value = false;
  }
}

function onModuleKindChange(kind: ModuleKind) {
  if (!moduleDraft.value.id && !moduleDraft.value.runtimeMode) {
    moduleDraft.value.runtimeMode = kind === "FRONTEND" ? "STATIC" : "NATIVE";
  }
}

function editModule(item?: ProjectModule) {
  newRepositoryBranch.value = "main";
  newRepositoryJenkinsCredential.value = "";
  moduleDraft.value = newModuleDraft(primaryRepository.value?.id, item);
  moduleEditorTab.value = "basic";
  formError.value = "";
  moduleEditorOpen.value = true;
}

async function saveModules(
  modules: ReturnType<typeof moduleEdit>[],
  repositories?: { id: string; name: string; url: string; branch: string; jenkinsCredentialId?: string }[],
) {
  const record = project.value;
  if (!record || saving.value) return false;
  saving.value = true;
  formError.value = "";
  try {
    const saved = await request<ApplicationProject>(
      `${rootPath}/${record.id}/catalog`,
      "PATCH",
      { version: record.version, modules, repositories },
    );
    const newModule = saved.modules.find(
      (item) => !record.modules.some((previous) => previous.id === item.id),
    );
    showProject(saved, modules[0]?.id || newModule?.id || selectedModuleId.value);
    return true;
  } catch (error) {
    formError.value = describeError(error, "模块保存失败，请检查填写内容后重试");
    return false;
  } finally {
    saving.value = false;
  }
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
  const existing = project.value?.repositories.find((item) => item.url === url);
  const id = existing?.id ?? repositoryId();
  if (
    await saveModules(
      [moduleEdit(draft, id)],
      existing
        ? undefined
        : [
            {
              id,
              name: draft.name.trim(),
              url,
              branch: newRepositoryBranch.value.trim(),
              ...(newRepositoryJenkinsCredential.value.trim() && { jenkinsCredentialId: newRepositoryJenkinsCredential.value.trim() }),
            },
          ],
    )
  ) {
    moduleEditorOpen.value = false;
  }
}

function reviewSuggestion(suggestion: InspectedModule) {
  const existing = reviewableModule(suggestion);
  if (!existing) return;
  inspectionOpen.value = false;
  editModule(existing);
  const draft = moduleDraft.value;
  if (!existing.catalog) draft.kind = suggestion.kind;
  draft.environment ||= suggestion.environment;
  draft.startCommand ||= suggestion.startCommand;
  if (!draft.runtimeMode && suggestion.recommendedRuntimeMode)
    draft.runtimeMode = suggestion.recommendedRuntimeMode;
  if (!draft.dependencies?.length && suggestion.dependencies?.length)
    draft.dependencies = [...suggestion.dependencies];
  if (!draft.buildCommand && suggestion.buildCommand)
    draft.buildCommand = suggestion.buildCommand;
  if (!draft.testCommand && suggestion.testCommand)
    draft.testCommand = suggestion.testCommand;
  if (!draft.artifactPath && suggestion.artifactPath)
    draft.artifactPath = suggestion.artifactPath;
  if (!draft.defaultPort && suggestion.defaultPort)
    draft.defaultPort = suggestion.defaultPort;
  if (!draft.healthPath && suggestion.healthPath)
    draft.healthPath = suggestion.healthPath;
  if (!draft.profiles?.length && suggestion.profiles?.length)
    draft.profiles = [...suggestion.profiles];
  if (!draft.defaultProfile && suggestion.defaultProfile)
    draft.defaultProfile = suggestion.defaultProfile;
  if (!draft.envHints?.length && suggestion.envHints?.length)
    draft.envHints = [...suggestion.envHints];
  if (!draft.proxyHints?.length && suggestion.proxyHints?.length)
    draft.proxyHints = [...suggestion.proxyHints];
  if (!draft.configTemplates?.length && suggestion.configTemplates?.length)
    draft.configTemplates = suggestion.configTemplates.map((item) => ({ ...item }));
  if (!draft.supportsContainer && suggestion.supportsContainer)
    draft.supportsContainer = true;
}

async function addSuggestions() {
  const modules = selectedModuleEdits();
  if (modules.length && (await saveModules(modules)))
    inspectionOpen.value = false;
}

watch([page, pageSize, archived], () => void load());
watch([pageSize, archived], () => {
  page.value = 1;
}, { flush: "sync" });
watch(query, () => {
  queryInput.value = query.value;
  if (searchTimer) clearTimeout(searchTimer);
  listController?.abort();
  searchTimer = setTimeout(() => {
    if (page.value === 1) void load();
    else page.value = 1;
  }, 250);
});
const lifetime = new AbortController();
watch(() => props.source, (source) => {
  if (source) void useSource(source);
});
function loadProjectInfrastructure() {
  void request<Host[]>("/hosts", "GET", undefined, lifetime.signal)
    .then((hosts) => {
      if (!lifetime.signal.aborted) {
        projectHosts.value = hosts;
        hostNames.value = Object.fromEntries(
          hosts.map((host) => [host.id, host.name]),
        );
      }
    })
    .catch((error) => {
      if (!lifetime.signal.aborted)
        listError.value = describeError(error, "服务器名称读取失败");
    });
  void request<{ items: SoftwareInstance[] }>("/software-instances?page=1&pageSize=100", "GET", undefined, lifetime.signal)
    .then((res) => {
      if (!lifetime.signal.aborted && res?.items)
        softwareList.value = res.items;
    })
    .catch(() => {});
}
watch(() => props.active, (active) => {
  if (active) loadProjectInfrastructure();
  else if (active === false) {
    projectController?.abort();
    inspectionOpen.value = false;
  }
});
onMounted(() => {
  void load();
  if (props.source) void useSource(props.source);
  loadProjectInfrastructure();
  void request<ProjectGitAccount[]>("/git-accounts", "GET", undefined, lifetime.signal)
    .then((accounts) => {
      if (!lifetime.signal.aborted && Array.isArray(accounts))
        gitAccounts.value = accounts;
    })
    .catch(() => {});
});
onUnmounted(() => {
  lifetime.abort();
  listController?.abort();
  projectController?.abort();
  if (searchTimer) clearTimeout(searchTimer);
});
</script>

<template>
  <section class="content application-projects-page" aria-label="项目维护">
    <div class="panel-card">
      <div class="ops-el-toolbar">
        <div class="ops-el-toolbar-group">
          <ElInput
            v-model="queryInput"
            clearable
            placeholder="搜索项目名称或简称"
            style="width: 240px"
            @keyup.enter="searchProjects"
            @clear="searchProjects"
          />
          <ElSelect v-model="archived" style="width: 120px">
            <ElOption label="使用中" value="false" />
            <ElOption label="已归档" value="true" />
            <ElOption label="全部状态" value="all" />
          </ElSelect>
          <ElButton type="primary" :loading="loading" @click="searchProjects">
            搜索
          </ElButton>
          <ElButton @click="clearProjectFilters">清空</ElButton>
        </div>
        <div class="ops-el-toolbar-group">
          <span class="table-tip">双击行查看并配置项目模块</span>
          <ElButton
            type="primary"
            :disabled="saving"
            @click="editProject(null)"
          >
            + 新增项目
          </ElButton>
        </div>
      </div>

      <ElAlert
        v-if="listError"
        type="error"
        show-icon
        :closable="false"
        style="margin-top: 12px"
      >
        <div class="ops-alert-row">
          <span>{{ listError }}</span>
          <ElButton link type="primary" size="small" @click="load">
            刷新
          </ElButton>
        </div>
      </ElAlert>

      <ElTable
        :data="records"
        row-key="id"
        border
        stripe
        v-loading="loading"
        style="width: 100%; margin-top: 14px"
        @row-dblclick="(row) => openProjectDrawer(row as ApplicationProject)"
      >
        <ElTableColumn type="index" label="#" width="52" align="center" />
        <ElTableColumn label="项目简称" width="135" show-overflow-tooltip>
          <template #default="{ row }">
            <span class="mono-cell">{{ projectCodeDisplay(row as ApplicationProject) }}</span>
          </template>
        </ElTableColumn>
        <ElTableColumn prop="name" label="项目名称" min-width="180" show-overflow-tooltip />
        <ElTableColumn label="主 Git 仓库 / 分支" min-width="250" show-overflow-tooltip>
          <template #default="{ row }">
            <span v-if="row.repositories?.length">
              {{ row.repositories[0].url }}
              <span v-if="row.repositories[0].branch" class="muted" style="margin-left: 6px">
                ({{ row.repositories[0].branch }})
              </span>
            </span>
            <span v-else class="muted">未配置</span>
          </template>
        </ElTableColumn>
        <ElTableColumn label="模块数" width="82" align="center">
          <template #default="{ row }">
            <span>{{ row.modules?.length ?? 0 }}</span>
          </template>
        </ElTableColumn>
        <ElTableColumn label="已配环境 / 服务器" min-width="200">
          <template #default="{ row }">
            <div v-if="projectEnvironmentsSummary(row as ApplicationProject).length" class="env-summary-list">
              <span
                v-for="item in projectEnvironmentsSummary(row as ApplicationProject)"
                :key="item"
                class="env-summary-item"
              >
                {{ item }}
              </span>
            </div>
            <span v-else class="muted">暂未绑定</span>
          </template>
        </ElTableColumn>
        <ElTableColumn label="状态" width="90" align="center">
          <template #default="{ row }">
            <span :class="row.archived ? 'muted' : 'status-active-text'">
              {{ row.archived ? "已归档" : "使用中" }}
            </span>
          </template>
        </ElTableColumn>
        <ElTableColumn label="操作" width="185" fixed="right">
          <template #default="{ row }">
            <div class="table-actions">
              <ElButton
                link
                type="primary"
                size="small"
                @click="openProjectDrawer(row as ApplicationProject)"
              >
                模块管理
              </ElButton>
              <ElButton
                v-if="!row.archived"
                link
                type="primary"
                size="small"
                @click="editProject(row as ApplicationProject)"
              >
                编辑
              </ElButton>
              <ElDropdown trigger="click">
                <ElButton link size="small">更多 ▾</ElButton>
                <template #dropdown>
                  <ElDropdownMenu>
                    <ElDropdownItem
                      v-if="row.archived"
                      @click="setArchived(row as ApplicationProject, false)"
                    >
                      恢复使用
                    </ElDropdownItem>
                    <ElDropdownItem
                      v-else
                      @click="
                        archiveTarget = row.id;
                        openProjectDrawer(row as ApplicationProject);
                      "
                    >
                      归档项目
                    </ElDropdownItem>
                    <ElDropdownItem
                      divided
                      @click="confirmProjectDeletion(row as ApplicationProject)"
                    >
                      删除项目
                    </ElDropdownItem>
                  </ElDropdownMenu>
                </template>
              </ElDropdown>
            </div>
          </template>
        </ElTableColumn>
        <template #empty>
          <ElEmpty
            :description="loading ? '正在读取…' : '暂无项目，点击右上角「+ 新增项目」创建'"
          />
        </template>
      </ElTable>

      <div v-if="total > 0" class="ops-el-pager">
        <ElPagination
          v-model:current-page="page"
          v-model:page-size="pageSize"
          :page-sizes="[10, 20, 50]"
          :total="total"
          background
          layout="total, sizes, prev, pager, next"
        />
      </div>
    </div>

    <ElDrawer
      :model-value="moduleDrawerOpen && active !== false"
      :title="
        project
          ? `${project.name}${projectCodeDisplay(project) !== '—' ? ' (' + projectCodeDisplay(project) + ')' : ''}`
          : '项目详情'
      "
      size="min(1160px, 95vw)"
      append-to-body
      destroy-on-close
      @close="active !== false && (moduleDrawerOpen = false)"
    >
      <template v-if="project">
        <div class="drawer-meta-strip">
          <div class="meta-strip-items">
            <span>
              <label>主仓库：</label>
              <code>{{ primaryRepository?.url || "未配置" }}</code>
              <template v-if="primaryRepository?.branch">
                <span class="muted">（分支 {{ primaryRepository.branch }}）</span>
              </template>
            </span>
            <span>
              <label>状态：</label>
              <span>{{ project.archived ? "已归档" : "使用中" }}</span>
            </span>
            <ProjectJenkinsConnection
              :key="project.id"
              :project-id="project.id"
              :archived="project.archived"
              @error="emit('error', $event)"
              @install="emit('install-jenkins')"
            />
          </div>
          <div class="meta-strip-actions">
            <ElButton
              v-if="!project.archived"
              size="small"
              @click="editProject(project)"
            >
              编辑项目
            </ElButton>
          </div>
        </div>

        <ElAlert
          v-if="archiveTarget === project.id"
          type="warning"
          show-icon
          :closable="false"
          style="margin-bottom: 12px"
        >
          <div class="ops-alert-row">
            <span>归档「{{ project.name }}」后，将停止新的 QA 构建与部署操作，确定归档？</span>
            <div class="ops-alert-actions">
              <ElButton size="small" @click="archiveTarget = ''">取消</ElButton>
              <ElButton
                type="danger"
                size="small"
                :loading="saving"
                @click="setArchived(project, true)"
              >
                确认归档
              </ElButton>
            </div>
          </div>
        </ElAlert>

        <ElTabs v-model="projectTab">
          <ElTabPane label="模块与配置" name="modules" />
          <ElTabPane label="交付与运行" name="delivery" />
        </ElTabs>

        <ProjectServiceInstances
          v-if="projectTab === 'delivery'"
          :key="project.id"
          :fixed-project="project"
          :hosts="projectHosts"
          :source="deliverySource"
          @source-used="deliverySource = undefined"
          @maintain="editEnvironment"
          @release="emit('legacy-release', $event, 'publish')"
          @verify="emit('legacy-release', $event, 'verify')"
          @logs="emit('logs', $event)"
          @error="emit('error', $event)"
        />
        <div
          v-if="projectTab === 'delivery' && project.environments?.some(env => env.deployments.some(deployment => deployment.managedInstanceId))"
          class="ops-el-toolbar-group"
        >
          <ElButton link @click="emit('legacy-tools', 'managed-instances')">旧版上线目标</ElButton>
          <ElButton link @click="emit('legacy-tools', 'artifacts')">旧版制品库</ElButton>
        </div>

        <template v-if="projectTab === 'modules'">
        <div class="ops-el-toolbar" style="margin-bottom: 12px">
          <div class="ops-el-toolbar-group">
            <ElInput
              v-model="moduleQuery"
              clearable
              placeholder="筛选模块名称、目录或所需环境"
              style="width: 260px"
            />
          </div>
          <div v-if="!project.archived" class="ops-el-toolbar-group">
            <ElButton :disabled="saving" @click="openInspection()">
              识别仓库模块
            </ElButton>
            <ElButton
              type="primary"
              :disabled="saving"
              @click="editModule()"
            >
              + 添加模块
            </ElButton>
          </div>
        </div>

        <ElTable
          :data="visibleModules"
          row-key="id"
          border
          stripe
          style="width: 100%"
        >
          <ElTableColumn type="index" label="#" width="48" align="center" />
          <ElTableColumn prop="name" label="模块名称" min-width="135" show-overflow-tooltip />
          <ElTableColumn label="模块描述" min-width="140" show-overflow-tooltip>
            <template #default="{ row }">
              <span>{{ row.catalog?.description || "—" }}</span>
            </template>
          </ElTableColumn>
          <ElTableColumn label="模块类型" width="84" align="center">
            <template #default="{ row }">
              <span>{{ moduleKinds[(row as ProjectModule).catalog?.kind || "OTHER"] }}</span>
            </template>
          </ElTableColumn>
          <ElTableColumn label="源码目录" min-width="145" show-overflow-tooltip>
            <template #default="{ row }">
              <span class="mono-cell">{{ row.directory || "." }}</span>
            </template>
          </ElTableColumn>
          <ElTableColumn label="所需环境与依赖" min-width="210">
            <template #default="{ row }">
              <div class="env-req-cell">
                <span>{{ row.catalog?.environment || "未定义" }}</span>
                <span
                  v-if="row.catalog?.dependencies?.length"
                  class="muted"
                  style="font-size: 12px"
                >
                  依赖：{{ row.catalog.dependencies.join("、") }}
                </span>
                <div
                  v-if="missingDependenciesForModule(row as ProjectModule).length"
                  class="dep-missing-row"
                >
                  <ElButton
                    v-for="miss in missingDependenciesForModule(row as ProjectModule)"
                    :key="miss.name"
                    link
                    type="warning"
                    size="small"
                    @click="emit('install-software', { search: miss.searchTerm, hostId: miss.hostId })"
                  >
                    未登记 {{ miss.searchTerm }}
                  </ElButton>
                </div>
              </div>
            </template>
          </ElTableColumn>
          <ElTableColumn label="部署形态" width="130">
            <template #default="{ row }">
              <span>{{ moduleDeployModeDisplay(row as ProjectModule) }}</span>
            </template>
          </ElTableColumn>
          <ElTableColumn label="运行环境 / 服务器" min-width="195">
            <template #default="{ row }">
              <div v-if="moduleEnvironments(row.id).length" class="env-list">
                <div
                  v-for="envRow in moduleEnvironments(row.id)"
                  :key="envRow.registration.id"
                  class="env-item"
                >
                  <ElButton
                    v-if="!project.archived"
                    link
                    type="primary"
                    size="small"
                    @click="
                      selectedModuleId = row.id;
                      environmentBinding = { moduleId: row.id, ...envRow };
                    "
                  >
                    {{ envRow.environment.name }}
                  </ElButton>
                  <span v-else>{{ envRow.environment.name }}</span>
                  <span class="muted">
                    {{ hostNames[envRow.registration.hostId] || "服务器待核对" }}
                    <template v-if="envRow.registration.servicePort || envRow.registration.nginx?.listenPort">
                      · :{{ envRow.registration.nginx?.listenPort || envRow.registration.servicePort }}
                    </template>
                  </span>
                </div>
              </div>
              <ElButton
                v-else-if="!project.archived"
                link
                type="primary"
                size="small"
                @click="
                  selectedModuleId = row.id;
                  environmentBinding = { moduleId: row.id };
                "
              >
                  + 添加部署草稿
              </ElButton>
              <span v-else class="muted">—</span>
            </template>
          </ElTableColumn>
          <ElTableColumn label="模块地址（自动）" min-width="175" show-overflow-tooltip>
            <template #default="{ row }">
              <div v-if="moduleComputedUrls(row as ProjectModule).length" class="env-list">
                <a
                  v-for="entry in moduleComputedUrls(row as ProjectModule)"
                  :key="entry.label + entry.url"
                  class="table-link"
                  :href="entry.url"
                  target="_blank"
                  rel="noopener noreferrer"
                >
                  <template v-if="entry.label">{{ entry.label }} · </template>{{ entry.url }}
                </a>
              </div>
              <span v-else class="muted">待配置端口</span>
            </template>
          </ElTableColumn>
          <ElTableColumn label="操作" width="165" fixed="right">
            <template #default="{ row }">
              <div v-if="!project.archived" class="table-actions">
                <ElButton
                  link
                  type="primary"
                  size="small"
                  @click="
                    selectedModuleId = row.id;
                    editModule(row as ProjectModule);
                  "
                >
                  编辑
                </ElButton>
                <ElButton
                  link
                  type="primary"
                  size="small"
                  @click="
                    selectedModuleId = row.id;
                    environmentBinding = {
                      moduleId: row.id,
                      ...moduleEnvironments(row.id)[0],
                    };
                  "
                >
                  部署草稿
                </ElButton>
                <ElDropdown trigger="click">
                  <ElButton link size="small">更多 ▾</ElButton>
                  <template #dropdown>
                    <ElDropdownMenu>
                      <ElDropdownItem
                        v-if="moduleEnvironments(row.id).length"
                        @click="
                          selectedModuleId = row.id;
                          environmentBinding = { moduleId: row.id };
                        "
                      >
                        添加运行环境
                      </ElDropdownItem>
                      <ElDropdownItem @click="openDelivery(row.id)">
                        查看交付与运行
                      </ElDropdownItem>
                      <ElDropdownItem @click="openInspection(row as ProjectModule)">
                        识别此模块
                      </ElDropdownItem>
                      <ElDropdownItem
                        divided
                        @click="confirmModuleDeletion(row as ProjectModule)"
                      >
                        删除模块
                      </ElDropdownItem>
                    </ElDropdownMenu>
                  </template>
                </ElDropdown>
              </div>
              <span v-else class="muted">—</span>
            </template>
          </ElTableColumn>
          <template #empty>
            <ElEmpty
              :description="
                moduleQuery
                  ? '没有匹配的模块'
                  : '还没有模块，点击右上方「+ 添加模块」或「识别仓库模块」'
              "
            />
          </template>
        </ElTable>
        </template>
      </template>
    </ElDrawer>

    <ElDialog
      v-model="projectEditorOpen"
      :title="editingProject ? '编辑项目' : '新建项目'"
      width="min(560px, 94vw)"
      append-to-body
      :close-on-click-modal="false"
      :close-on-press-escape="!saving"
      :show-close="!saving"
    >
      <ElForm label-position="top" @submit.prevent="saveProject">
        <div class="form-pair">
          <ElFormItem label="项目名称" required>
            <ElInput
              v-model="projectDraft.name"
              maxlength="60"
              placeholder="请输入项目名称"
            />
          </ElFormItem>
          <ElFormItem label="项目简称（Code）">
            <ElInput
              v-model="projectDraft.code"
              maxlength="63"
              placeholder="例如 nt-camera，选填"
            />
          </ElFormItem>
        </div>
        <ElFormItem label="项目简介">
          <ElInput
            v-model="projectDraft.description"
            type="textarea"
            :rows="2"
            maxlength="1000"
            placeholder="选填"
          />
        </ElFormItem>
        <ElFormItem
          label="主仓库地址"
          :required="!!editingProject?.repositories[0]"
        >
          <ElInput
            v-model="projectDraft.repositoryUrl"
            placeholder="例如 https://git.example.com/team/project.git"
          />
        </ElFormItem>
        <div v-if="projectDraft.repositoryUrl" class="form-pair">
          <ElFormItem label="Git 分支">
            <ElInput v-model="projectDraft.branch" placeholder="例如 main" />
          </ElFormItem>
          <ElFormItem label="Ops 读取仓库的 Git 账号">
            <ElSelect
              v-model="projectDraft.gitAccountId"
              clearable
              placeholder="公开仓库可留空"
              style="width: 100%"
            >
              <ElOption label="公开仓库（无需认证）" value="" />
              <ElOption
                v-for="acc in gitAccounts"
                :key="acc.id"
                :label="`${acc.name} (${acc.authMethod})`"
                :value="acc.id"
              />
            </ElSelect>
          </ElFormItem>
        </div>
        <ElFormItem v-if="projectDraft.repositoryUrl" label="Jenkins 拉取仓库的凭据 ID">
          <ElInput v-model="projectDraft.jenkinsCredentialId" clearable placeholder="公开仓库可留空" />
          <span class="field-hint">填写 Jenkins 凭据列表中用于拉取此仓库的 ID。</span>
        </ElFormItem>
        <ElAlert
          v-if="formError"
          type="error"
          show-icon
          :closable="false"
          :title="formError"
          style="margin-bottom: 12px"
        />
        <div class="dialog-footer-actions">
          <ElButton :disabled="saving" @click="projectEditorOpen = false">
            取消
          </ElButton>
          <ElButton
            type="primary"
            native-type="submit"
            :loading="saving"
            :disabled="!projectDraft.name.trim()"
          >
            {{ saving ? "正在保存…" : "保存项目" }}
          </ElButton>
        </div>
      </ElForm>
    </ElDialog>

    <ProjectEnvironmentBinding
      v-if="environmentBinding && project"
      :visible="active !== false"
      :project="project"
      :module-id="environmentBinding.moduleId"
      :environment="environmentBinding.environment"
      :registration="environmentBinding.registration"
      :adding-algorithm-host="environmentBinding.addingAlgorithmHost"
      :software-list="softwareList"
      @install-software="emit('install-software', $event)"
      @saved="showProject($event, selectedModuleId)"
      @close="environmentBinding = undefined"
      @error="emit('error', $event)"
    />

    <ElDialog
      v-model="projectDeleteOpen"
      title="删除项目"
      width="min(440px, 94vw)"
      append-to-body
      :close-on-click-modal="false"
      :close-on-press-escape="!saving"
      :show-close="!saving"
    >
      <p class="dialog-copy">
        删除「{{ projectDeleteTarget?.name }}」的项目档案及其配置后无法恢复。
      </p>
      <ElAlert
        v-if="formError"
        type="error"
        show-icon
        :closable="false"
        :title="formError"
        style="margin-bottom: 12px"
      />
      <div class="dialog-footer-actions">
        <ElButton :disabled="saving" @click="projectDeleteOpen = false">
          取消
        </ElButton>
        <ElButton type="danger" :loading="saving" @click="deleteProject">
          {{ saving ? "正在删除…" : "确认删除" }}
        </ElButton>
      </div>
    </ElDialog>

    <ElDialog
      v-model="moduleDeleteOpen"
      title="删除模块"
      width="min(460px, 94vw)"
      append-to-body
      :close-on-click-modal="false"
      :close-on-press-escape="!saving"
      :show-close="!saving"
    >
      <p class="dialog-copy">
        删除「{{ moduleDeleteTarget?.name }}」的模块记录后无法恢复。
      </p>
      <ElAlert
        v-if="formError"
        type="error"
        show-icon
        :closable="false"
        :title="formError"
        style="margin-bottom: 12px"
      />
      <div class="dialog-footer-actions">
        <ElButton
          v-if="
            project &&
            moduleDeleteTarget &&
            formError.includes('APPLICATION_MODULE_IN_USE')
          "
          :disabled="saving"
          @click="
            moduleDeleteOpen = false;
            environmentBinding = {
              moduleId: moduleDeleteTarget.id,
              ...moduleEnvironments(moduleDeleteTarget.id)[0],
            };
          "
        >
          查看运行配置
        </ElButton>
        <ElButton :disabled="saving" @click="moduleDeleteOpen = false">
          取消
        </ElButton>
        <ElButton type="danger" :loading="saving" @click="deleteModule">
          {{ saving ? "正在删除…" : "确认删除" }}
        </ElButton>
      </div>
    </ElDialog>

    <ElDialog
      v-model="moduleEditorOpen"
      :title="moduleDraft.id ? '编辑模块' : '添加模块'"
      width="min(760px, 95vw)"
      append-to-body
      :close-on-click-modal="false"
      :close-on-press-escape="!saving"
      :show-close="!saving"
    >
      <ElForm label-position="top" @submit.prevent="saveModule">
        <ElTabs v-model="moduleEditorTab" style="margin-top: -6px">
          <ElTabPane label="基本信息与依赖" name="basic">
            <div class="form-pair">
              <ElFormItem label="模块名称" required>
                <ElInput
                  v-model="moduleDraft.name"
                  maxlength="60"
                  placeholder="请输入模块名称"
                />
              </ElFormItem>
              <ElFormItem label="模块类型">
                <ElSelect
                  v-model="moduleDraft.kind"
                  style="width: 100%"
                  @change="onModuleKindChange($event)"
                >
                  <ElOption
                    v-for="(label, kind) in pipelineKinds"
                    :key="kind"
                    :label="label"
                    :value="kind"
                  />
                </ElSelect>
              </ElFormItem>
            </div>
            <div class="form-pair">
              <ElFormItem label="代码仓库">
                <ElSelect v-model="moduleDraft.repositoryId" style="width: 100%">
                  <ElOption label="未登记仓库" value="" />
                  <ElOption
                    v-for="(repository, index) in project?.repositories"
                    :key="repository.id"
                    :label="index === 0 ? '主仓库' : repository.name"
                    :value="repository.id"
                  />
                  <ElOption label="登记其他仓库" :value="separateRepository" />
                </ElSelect>
              </ElFormItem>
              <ElFormItem label="源码目录">
                <ElInput
                  v-model="moduleDraft.directory"
                  placeholder="例如 platform-api，根目录填 ."
                />
              </ElFormItem>
            </div>

            <div
              v-if="moduleDraft.repositoryId === separateRepository"
              class="form-pair"
            >
              <ElFormItem label="仓库地址" required>
                <ElInput
                  v-model="moduleDraft.repositoryUrl"
                  placeholder="请输入 Git 仓库地址"
                />
              </ElFormItem>
              <ElFormItem label="Git 分支">
                <ElInput v-model="newRepositoryBranch" placeholder="main" />
              </ElFormItem>
            </div>
            <ElFormItem v-if="moduleDraft.repositoryId === separateRepository" label="Jenkins 拉取仓库的凭据 ID">
              <ElInput v-model="newRepositoryJenkinsCredential" clearable placeholder="公开仓库可留空" />
            </ElFormItem>

            <div class="form-pair">
              <ElFormItem label="所需构建 / 运行环境">
                <ElSelect
                  v-model="moduleDraft.environment"
                  filterable
                  allow-create
                  default-first-option
                  clearable
                  placeholder="自动识别或选择运行环境（支持手动输入）"
                  style="width: 100%"
                >
                  <ElOption
                    v-for="item in environmentPresets"
                    :key="item"
                    :label="item"
                    :value="item"
                  />
                </ElSelect>
              </ElFormItem>
              <ElFormItem label="推荐部署形态">
                <ElSelect
                  v-model="moduleDraft.runtimeMode"
                  filterable
                  allow-create
                  clearable
                  placeholder="后端/算法推荐原生 systemd，前端推荐 Nginx 静态"
                  style="width: 100%"
                >
                  <ElOption
                    v-for="item in runtimeModePresets"
                    :key="item.value"
                    :label="item.label"
                    :value="item.value"
                  />
                </ElSelect>
              </ElFormItem>
            </div>

            <ElFormItem label="依赖软件与中间件（支持搜索、多选或自填）">
              <ElSelect
                v-model="moduleDraft.dependencies"
                multiple
                filterable
                allow-create
                default-first-option
                clearable
                placeholder="例如 MySQL、Redis、Nginx、Java 21、SeaweedFS"
                style="width: 100%"
              >
                <ElOption
                  v-for="dep in dependencyPresets"
                  :key="dep"
                  :label="dep"
                  :value="dep"
                />
              </ElSelect>
            </ElFormItem>

            <ElFormItem label="模块描述">
              <ElInput
                v-model="moduleDraft.description"
                maxlength="200"
                placeholder="例如 平台后端核心服务"
              />
            </ElFormItem>
            <p class="muted" style="margin: 0 0 8px; font-size: 12px">
              模块访问地址由系统根据「运行配置」中所绑定的目标服务器及端口 / Nginx 站点自动计算，无需手填。
            </p>
          </ElTabPane>

          <ElTabPane label="构建与识别信息" name="build">
            <ElAlert
              title="Jenkins QA 按模块类型使用预设流程。下列命令和路径来自仓库识别，供核对。"
              type="info"
              show-icon
              :closable="false"
              style="margin-bottom: 12px"
            />
            <div class="form-pair">
              <ElFormItem label="构建命令">
                <ElInput
                  v-model="moduleDraft.buildCommand"
                  placeholder="未识别"
                  readonly
                />
              </ElFormItem>
              <ElFormItem label="产物输出路径">
                <ElInput
                  v-model="moduleDraft.artifactPath"
                  placeholder="未识别"
                  readonly
                />
              </ElFormItem>
            </div>

            <div class="form-pair">
              <ElFormItem label="测试命令">
                <ElInput
                  v-model="moduleDraft.testCommand"
                  placeholder="未识别"
                  readonly
                />
              </ElFormItem>
            </div>

            <div class="form-pair">
              <ElFormItem label="默认服务端口">
                <ElInputNumber
                  v-model="moduleDraft.defaultPort"
                  :min="0"
                  :max="65535"
                  controls-position="right"
                  style="width: 100%"
                />
              </ElFormItem>
              <ElFormItem label="健康检查路径">
                <ElInput
                  v-model="moduleDraft.healthPath"
                  placeholder="例如 /actuator/health（选填）"
                />
              </ElFormItem>
            </div>

            <div class="form-pair">
              <ElFormItem label="可选环境 Profile">
                <ElSelect
                  v-model="moduleDraft.profiles"
                  multiple
                  filterable
                  allow-create
                  default-first-option
                  clearable
                  placeholder="例如 dev、test、prod"
                  style="width: 100%"
                >
                  <ElOption
                    v-for="p in profilePresets"
                    :key="p"
                    :label="p"
                    :value="p"
                  />
                </ElSelect>
              </ElFormItem>
              <ElFormItem label="手工环境检测命令（选填）">
                <ElInput
                  v-model="moduleDraft.checkCommand"
                  placeholder="登记供人工参考；Jenkins 按模块类型检查环境"
                />
              </ElFormItem>
            </div>

            <ElFormItem label="启动命令参考">
              <ElInput
                v-model="moduleDraft.startCommand"
                type="textarea"
                :rows="2"
                placeholder="例如 sh mvnw spring-boot:run 或 uv run --locked python -m app"
              />
            </ElFormItem>

            <div v-if="moduleDraft.envHints?.length || moduleDraft.proxyHints?.length" class="hint-summary">
              <span v-if="moduleDraft.envHints?.length" class="muted">
                已识别 {{ moduleDraft.envHints.length }} 项外部配置变量：
                {{ moduleDraft.envHints.slice(0, 8).map((h) => h.name).join("、") }}
                <template v-if="moduleDraft.envHints.length > 8">…</template>
              </span>
              <span v-if="moduleDraft.proxyHints?.length" class="muted">
                已识别前端代理路径：
                {{ moduleDraft.proxyHints.map((p) => p.location).join("、") }}
              </span>
            </div>
          </ElTabPane>
        </ElTabs>

        <ElAlert
          v-if="formError"
          type="error"
          show-icon
          :closable="false"
          :title="formError"
          style="margin: 10px 0 12px"
        />
        <div class="dialog-footer-actions">
          <ElButton :disabled="saving" @click="moduleEditorOpen = false">
            取消
          </ElButton>
          <ElButton
            type="primary"
            native-type="submit"
            :loading="saving"
            :disabled="!moduleDraft.name.trim()"
          >
            {{ saving ? "正在保存…" : "保存模块" }}
          </ElButton>
        </div>
      </ElForm>
    </ElDialog>

    <ElDialog
      v-model="inspectionOpen"
      :title="inspectionModuleId ? '识别模块环境与参数' : '自动识别仓库模块'"
      width="min(820px, 95vw)"
      append-to-body
      :close-on-click-modal="false"
      :close-on-press-escape="!saving"
      :show-close="!saving"
    >
      <div class="project-form">
        <div class="inspection-mode-row">
          <ElRadioGroup
            v-model="inspectionMode"
            :disabled="inspecting || saving"
          >
            <ElRadioButton value="repo">项目 Git 仓库</ElRadioButton>
            <ElRadioButton value="local" :disabled="!inspectionSources.length">
              控制器本地目录{{ inspectionSources.length ? ` (${inspectionSources.length})` : "" }}
            </ElRadioButton>
          </ElRadioGroup>
          <span v-if="sourcesLoading" class="muted" style="font-size: 12px">
            正在检查本地来源…
          </span>
        </div>

        <ElForm
          v-if="inspectionMode === 'repo'"
          label-position="top"
          class="repo-inspect-form"
          @submit.prevent="inspect"
        >
            <div class="form-pair">
              <ElFormItem label="选择已登记仓库" style="margin-bottom: 10px">
                <ElSelect
                  v-model="inspectionRepository"
                  :disabled="inspecting || saving"
                  style="width: 100%"
                  @change="syncInspectionRepoFields"
                >
                  <ElOption
                    v-for="(repository, index) in project?.repositories"
                    :key="repository.id"
                    :label="index === 0 ? `主仓库 · ${repository.url}` : `${repository.name} · ${repository.url}`"
                    :value="repository.id"
                  />
                  <ElOption v-if="!project?.repositories.length" label="临时填写仓库" value="" />
                </ElSelect>
              </ElFormItem>
              <ElFormItem label="分支（选填）" style="margin-bottom: 10px">
                <ElInput
                  v-model="inspectionBranch"
                  :disabled="inspecting || saving"
                  placeholder="默认分支可留空"
                />
              </ElFormItem>
            </div>
            <div class="repo-url-bar">
              <ElFormItem label="Git 仓库地址" style="margin-bottom: 0; flex: 1">
                <ElInput
                  v-model="inspectionRepoUrl"
                  :disabled="inspecting || saving"
                  placeholder="https://github.com/org/repo.git"
                />
              </ElFormItem>
              <ElFormItem label="Git 账号" style="margin-bottom: 0; width: 210px">
                <ElSelect
                  v-model="inspectionGitAccountId"
                  clearable
                  :disabled="inspecting || saving"
                  placeholder="公开仓库可留空"
                  style="width: 100%"
                >
                  <ElOption label="公开仓库" value="" />
                  <ElOption
                    v-for="acc in gitAccounts"
                    :key="acc.id"
                    :label="`${acc.name} (${acc.authMethod})`"
                    :value="acc.id"
                  />
                </ElSelect>
              </ElFormItem>
              <ElButton
                type="primary"
                native-type="submit"
                :loading="inspecting"
                :disabled="!inspectionRepoUrl.trim() || saving"
              >
                {{ inspecting ? "正在浅克隆识别…" : "拉取并识别" }}
              </ElButton>
            </div>
        </ElForm>

        <form
          v-else
          class="source-picker"
          @submit.prevent="inspect"
        >
          <ElFormItem label="源码来源" style="margin-bottom: 0">
            <ElSelect
              v-model="sourceId"
              placeholder="选择控制器上的源码"
              :disabled="inspecting || saving"
              style="width: 100%"
            >
              <ElOption
                v-for="source in inspectionSources"
                :key="source.id"
                :label="source.name"
                :value="source.id"
              />
            </ElSelect>
          </ElFormItem>
          <ElButton
            type="primary"
            native-type="submit"
            :loading="inspecting"
            :disabled="!sourceId || saving"
          >
            {{ inspecting ? "正在识别…" : "开始识别" }}
          </ElButton>
        </form>

        <ElAlert
          v-if="formError"
          type="error"
          show-icon
          :closable="false"
          :title="formError"
        />
        <p v-if="inspected && !inspectionResults.length" class="muted">
          没有识别到支持的模块（Java / Maven / Node.js / Python），您可以关闭弹窗并手动添加模块。
        </p>
        <ElAlert
          v-if="inspectionRepoMismatch && inspectionResults.length"
          type="warning"
          show-icon
          :closable="false"
          title="当前地址与所选仓库不同。可查看识别结果；应用前请选回已登记的地址，或更新项目仓库地址后重新识别。"
        />

        <ElFormItem
          v-if="
            inspectionMode === 'local' &&
            inspectionResults.length &&
            !inspectionModuleId &&
            project?.repositories.length
          "
          label="登记到仓库"
          style="margin-bottom: 0"
        >
          <ElSelect
            v-model="localRegistrationRepository"
            :disabled="saving"
            style="width: 100%"
            @change="selectNewDirectories"
          >
            <ElOption label="未登记仓库" value="" />
            <ElOption
              v-for="(repository, index) in project.repositories"
              :key="repository.id"
              :label="index === 0 ? '主仓库' : repository.name"
              :value="repository.id"
            />
          </ElSelect>
        </ElFormItem>

        <ElCheckboxGroup v-model="selectedDirectories" class="inspection-list">
          <article
            v-for="item in inspectionResults"
            :key="item.directory"
            class="inspection-result"
          >
            <div class="project-toolbar">
              <div style="display: flex; align-items: center; gap: 8px; flex-wrap: wrap">
                <ElCheckbox
                  v-if="!inspectionRepoMismatch && !matchingModule(item.directory)"
                  :value="item.directory"
                  :disabled="saving"
                >
                  {{ item.name }}
                </ElCheckbox>
                <span v-else style="font-weight: 500">{{ item.name }}</span>
                <ElTag size="small" type="info">{{ moduleKinds[item.kind] }}</ElTag>
                <span class="mono-cell">{{ item.directory }}</span>
                <ElTag v-if="item.recommendedRuntimeMode" size="small">
                  {{ runtimeModeText(item.recommendedRuntimeMode) }}
                </ElTag>
              </div>
              <ElButton
                v-if="!inspectionRepoMismatch && matchingModule(item.directory)"
                link
                type="primary"
                size="small"
                :disabled="saving"
                @click="reviewSuggestion(item)"
              >
                填入模块空项
              </ElButton>
            </div>
            <div class="inspect-meta-lines">
              <div><strong>环境与依赖：</strong>{{ item.environment }}</div>
              <div v-if="item.dependencies?.length">
                <strong>检测组件：</strong>{{ item.dependencies.join("、") }}
              </div>
              <div v-if="item.profiles?.length || item.defaultPort">
                <strong>配置档案：</strong>
                <template v-if="item.profiles?.length">
                  Profile [{{ item.profiles.join(", ") }}]（默认 {{ item.defaultProfile || item.profiles[0] }}）
                </template>
                <template v-if="item.defaultPort"> · 默认端口 {{ item.defaultPort }}</template>
                <template v-if="item.envHints?.length"> · 识别 {{ item.envHints.length }} 个外部环境变量</template>
              </div>
              <div v-if="item.proxyHints?.length">
                <strong>前端代理：</strong>{{ item.proxyHints.map((p) => `${p.location}${p.targetPort ? ' → :' + p.targetPort : ''}`).join("；") }}
              </div>
              <div v-if="item.buildCommand">
                <strong>构建命令：</strong><code>{{ item.buildCommand }}</code>
                <span v-if="item.artifactPath" class="muted"> → {{ item.artifactPath }}</span>
              </div>
              <small v-if="item.evidence.length">
                识别依据：{{ item.evidence.join("、") }}
              </small>
            </div>
          </article>
        </ElCheckboxGroup>

        <div class="dialog-footer-actions">
          <ElButton :disabled="saving" @click="inspectionOpen = false">
            关闭
          </ElButton>
          <ElButton
            v-if="!inspectionRepoMismatch && selectedDirectories.length"
            type="primary"
            :loading="saving"
            @click="addSuggestions"
          >
            {{ saving ? "正在登记…" : `登记所选 ${selectedDirectories.length} 个模块` }}
          </ElButton>
        </div>
      </div>
    </ElDialog>
  </section>
</template>

<style scoped>
.application-projects-page {
  min-width: 0;
}
.table-tip {
  font-size: 12px;
  color: var(--muted);
}
.mono-cell {
  font: 12px/1.5 var(--mono);
  color: #475569;
}
.env-summary-list,
.env-req-cell,
.env-list {
  display: flex;
  flex-direction: column;
  gap: 2px;
}
.env-summary-item {
  font-size: 12px;
  color: #334155;
}
.env-item {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 4px 8px;
}
.status-active-text {
  color: #15803d;
}
.table-actions,
.dialog-footer-actions {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 8px;
}
.dialog-footer-actions {
  justify-content: flex-end;
  margin-top: 8px;
}
.drawer-meta-strip {
  display: flex;
  align-items: center;
  justify-content: space-between;
  flex-wrap: wrap;
  gap: 10px 16px;
  padding: 10px 14px;
  margin-bottom: 14px;
  background: #f8fafc;
  border: 1px solid var(--border);
  border-radius: 6px;
  font-size: 13px;
}
.meta-strip-items {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 8px 20px;
  min-width: 0;
}
.meta-strip-items label {
  color: var(--muted);
}
.meta-strip-items code {
  font-family: var(--mono);
  font-size: 12px;
}
.table-link {
  color: var(--el-color-primary);
  text-decoration: none;
}
.table-link:hover {
  text-decoration: underline;
}
.dialog-copy {
  margin: 0 0 12px;
  line-height: 1.6;
}
.project-form {
  display: grid;
  gap: 12px;
}
.project-toolbar {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 8px;
}
.form-pair {
  display: grid;
  gap: 0 12px;
  grid-template-columns: repeat(2, minmax(0, 1fr));
}
.source-picker {
  display: grid;
  grid-template-columns: minmax(0, 1fr) auto;
  align-items: end;
  gap: 10px;
}
.inspection-mode-row {
  display: flex;
  align-items: center;
  justify-content: space-between;
  flex-wrap: wrap;
  gap: 8px;
}
.repo-url-bar {
  display: flex;
  align-items: flex-end;
  flex-wrap: wrap;
  gap: 10px;
}
.inspection-list {
  display: grid;
  gap: 10px;
}
.inspect-meta-lines {
  display: grid;
  gap: 4px;
  font-size: 12px;
  color: #334155;
  line-height: 1.5;
}
.inspect-meta-lines strong {
  font-weight: 500;
  color: #64748b;
}
.hint-summary {
  display: grid;
  gap: 4px;
  padding: 8px 10px;
  background: #f8fafc;
  border: 1px solid var(--border);
  border-radius: 6px;
  font-size: 12px;
}
.dep-missing-row {
  display: flex;
  flex-wrap: wrap;
  gap: 2px 8px;
}
.inspection-result {
  display: grid;
  gap: 6px;
  min-width: 0;
  border: 1px solid var(--border);
  border-radius: 6px;
  padding: 12px;
}
.inspection-result small {
  color: var(--muted);
  overflow-wrap: anywhere;
}
.inspection-result .result-choice {
  display: flex;
  align-items: center;
  gap: 8px;
}
.result-choice input[type="checkbox"] {
  width: 15px;
  min-height: 0;
  height: 15px;
  margin: 0;
  padding: 0;
}
@media (max-width: 600px) {
  .form-pair,
  .source-picker {
    grid-template-columns: minmax(0, 1fr);
  }
}
</style>
