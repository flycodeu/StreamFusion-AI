<script setup lang="ts">
import { computed, nextTick, onMounted, onUnmounted, ref, watch } from "vue";
import {
  ElAlert,
  ElButton,
  ElDrawer,
  ElEmpty,
  ElInput,
  ElOption,
  ElPagination,
  ElSelect,
  ElTable,
  ElTableColumn,
} from "element-plus";
import { ApiError, request } from "./api";
import {
  deployModeText,
  isProjectBuildService,
  jenkinsLink,
  moduleKinds,
  type ApplicationProject,
  type ProjectEnvironment,
  type ProjectInstanceSource,
  type ProjectModule,
  type ProjectReleaseSource,
  type ProjectServiceRegistration,
} from "./applicationProjects";
import type { Host } from "./hosts";
import type { LogSelection } from "./jobs";
import { requestAllPages } from "./listPages";
import {
  moduleJenkinsPath,
  jenkinsState,
  type JenkinsBuild,
  type JenkinsInstanceContext,
  type JenkinsMonitor,
} from "./projectJenkins";
import { softwareState, type SoftwareInstance } from "./software";
import ProjectJenkinsBuild from "./ProjectJenkinsBuild.vue";
import ProjectDeploymentPanel from "./ProjectDeploymentPanel.vue";
import {
  deploymentHealthText, deploymentTime, jobPending, jobStatusText, projectDeploymentPath, type DeploymentStatus,
} from "./projectDeployments";

interface ModuleRow {
  module: ProjectModule;
  environment?: ProjectEnvironment;
  registration?: ProjectServiceRegistration;
}

const props = defineProps<{ hosts: Host[]; source?: ProjectInstanceSource; fixedProject?: ApplicationProject }>();
const emit = defineEmits<{
  "source-used": [];
  logs: [LogSelection];
  release: [ProjectReleaseSource];
  verify: [ProjectReleaseSource];
  maintain: [source: ProjectInstanceSource];
  error: [unknown];
}>();
const lifetime = new AbortController();
const projects = ref<ApplicationProject[]>([]);
const projectId = ref(""),
  projectSearch = ref(""),
  moduleSearch = ref(""),
  environmentFilter = ref("");
const queryDraft = ref(""),
  environmentDraft = ref("");
const focusedModuleId = ref("");
const listElement = ref<HTMLElement>();
const loading = ref(false),
  pageError = ref("");
const rowPage = ref(1),
  rowPageSize = ref(10);
const project = computed(() =>
  props.fixedProject || projects.value.find((item) => item.id === projectId.value),
);
const projectOptions = computed(() =>
  projects.value.filter((item) =>
    item.name.toLowerCase().includes(projectSearch.value.trim().toLowerCase()),
  ),
);
const rows = computed<ModuleRow[]>(() => {
  if (!project.value) return [];
  const bindings = new Map<string, ModuleRow[]>();
  const modules = new Map(
    project.value.modules.map((module) => [module.id, module]),
  );
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
  const bound = project.value.modules.flatMap(
    (module) => bindings.get(module.id) || [],
  );
  return [
    ...bound,
    ...project.value.modules
      .filter((module) => !bindings.has(module.id))
      .map((module) => ({ module })),
  ];
});
const hostsById = computed(
  () => new Map(props.hosts.map((host) => [host.id, host.name])),
);
const runtimeIds = computed(() => [
  ...new Set(
    rows.value.flatMap((row) =>
      row.registration?.runtimeSoftwareId
        ? [row.registration.runtimeSoftwareId]
        : [],
    ),
  ),
]);
const runtimes = ref(new Map<string, SoftwareInstance>());
const visibleRows = computed(() => {
  const query = moduleSearch.value.trim().toLowerCase();
  return rows.value
    .filter(
      ({ module, environment }) =>
        (!environmentFilter.value ||
          environment?.id === environmentFilter.value) &&
        (!query ||
          [
            module.name,
            module.directory,
            module.catalog?.description || "",
            module.catalog?.kind ? moduleKinds[module.catalog.kind] : "",
          ].some((value) => value.toLowerCase().includes(query))),
    )
    .map((row) => {
      const runtime = runtimes.value.get(
        row.registration?.runtimeSoftwareId || "",
      );
      const buildService =
        !!runtime && isProjectBuildService(row.module.name, runtime);
      return {
        ...row,
        runtime,
        buildService,
        record: records.value.get(row.module.id),
        state: runtime && !buildService ? softwareState(runtime) : undefined,
      };
    });
});
const pagedRows = computed(() => {
  const start = (rowPage.value - 1) * rowPageSize.value;
  return visibleRows.value.slice(start, start + rowPageSize.value);
});
watch([projectId, moduleSearch, environmentFilter], () => {
  rowPage.value = 1;
});
const runtimeRefresh = ref(0),
  runtimeLoading = ref(false),
  runtimeError = ref("");
const panel = ref<{ context: JenkinsInstanceContext; monitor: JenkinsMonitor }>();
const deploymentPanel = ref<ModuleRow>();
const deploymentStatuses = ref(new Map<string, DeploymentStatus>());
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
  if (error instanceof ApiError && error.code === "AUTH_REQUIRED")
    emit("error", error);
  return error instanceof ApiError
    ? [error.message, error.code, error.traceId, error.timestamp]
        .filter(Boolean)
        .join(" · ")
    : fallback;
}

function selectProject(id: string) {
  projectId.value = id;
  clearFilters();
  focusedModuleId.value = "";
  panel.value = undefined;
  deploymentPanel.value = undefined;
  selectionRequest?.abort();
  opening.value = "";
  setupNotice.value = undefined;
}
function useSource() {
  const source = props.source;
  if (!source || (!props.fixedProject && !projects.value.length)) return;
  if (props.fixedProject?.id === source.projectId || projects.value.some((item) => item.id === source.projectId)) {
    if (!props.fixedProject) selectProject(source.projectId);
    focusedModuleId.value = source.moduleId;
    const environment = project.value?.environments?.find((item) =>
      item.deployments.some(({ id }) => id === source.serviceInstanceId),
    );
    if (environment) {
      environmentDraft.value = environmentFilter.value = environment.id;
      rowPage.value = 1;
    }
    void nextTick(() =>
      listElement.value
        ?.querySelector("[data-focused=true]")
        ?.scrollIntoView({ block: "nearest" }),
    );
  }
  emit("source-used");
}
async function refresh() {
  if (loading.value) return;
  if (props.fixedProject) {
    useSource();
    runtimeRefresh.value++;
    return;
  }
  loading.value = true;
  pageError.value = "";
  try {
    const data = await requestAllPages<ApplicationProject>(
      "/application-projects?archived=all",
      lifetime.signal,
    );
    if (lifetime.signal.aborted) return;
    projects.value = data;
    if (!project.value)
      selectProject((data.find((item) => !item.archived) || data[0])?.id || "");
    useSource();
    runtimeRefresh.value++;
  } catch (error) {
    if (!lifetime.signal.aborted)
      pageError.value = describe(error, "无法读取项目发布清单");
  } finally {
    loading.value = false;
  }
}
function maintain(row: ModuleRow) {
  if (!project.value || project.value.archived) return;
  emit("maintain", {
    projectId: project.value.id,
    moduleId: row.module.id,
    ...(row.registration && { serviceInstanceId: row.registration.id }),
  });
}
function openServiceLog(runtime: SoftwareInstance) {
  emit("logs", { softwareId: runtime.id, title: runtime.name });
}
async function openBuild(
  row: ModuleRow,
  action: JenkinsInstanceContext["action"] | "record",
) {
  const current = project.value;
  if (!current || (action === "qa" && current.archived)) return;
  const id = row.module.id;
  if (opening.value === id) return;
  selectionRequest?.abort();
  const controller = (selectionRequest = new AbortController());
  opening.value = id;
  setupNotice.value = undefined;
  pageError.value = "";
  try {
    const path = moduleJenkinsPath(
      current.id,
      row.module.id,
    );
    const monitor = await request<JenkinsMonitor>(
      path + "/jenkins-monitor",
      "GET",
      undefined,
      controller.signal,
    );
    if (controller.signal.aborted) return;
    if (!monitor.configured) {
      setupNotice.value = {
        row,
        message: monitor.reason || "请在项目管理中设置 Jenkins 连接和模块类型。",
      };
      return;
    }
    if (action === "record") {
      const latest = await request<JenkinsBuild>(
        path + "/jenkins-build",
        "GET",
        undefined,
        controller.signal,
      );
      if (!controller.signal.aborted) recordBuild(id, latest);
      return;
    }
    panel.value = {
      monitor,
      context: {
        action,
        readOnly: current.archived,
        moduleId: row.module.id,
        moduleName: row.module.name,
      },
    };
  } catch (error) {
    if (!controller.signal.aborted)
      pageError.value = describe(error, "读取 QA 配置失败，请重试。");
  } finally {
    if (selectionRequest === controller) opening.value = "";
  }
}
function recordBuild(id: string, build: JenkinsBuild) {
  records.value.set(id, build);
}
function openRelease(
  row: ModuleRow,
  action: "release" | "verify" = "release",
) {
  if (!project.value) return;
  if (row.registration && row.environment && (
    supportsProjectDeployment(row) || deploymentStatuses.value.get(row.registration.id)?.current
  )) {
    deploymentPanel.value = row;
    return;
  }
  if (project.value.archived) return;
  if (!row.registration?.managedInstanceId || !row.environment) {
    setupNotice.value = { row, message: "请在项目管理中选择该环境的上线目标。" };
    return;
  }
  const source: ProjectReleaseSource = {
    projectId: project.value.id,
    moduleId: row.module.id,
    moduleName: row.module.name,
    serviceInstanceId: row.registration.id,
    hostId: row.registration.hostId,
    environmentId: row.environment.id,
    environmentName: row.environment.name,
    managedInstanceId: row.registration.managedInstanceId,
  };
  if (action === "verify") emit("verify", source);
  else emit("release", source);
}

function supportsProjectDeployment(row: ModuleRow) {
  if (row.registration?.managedInstanceId) return false;
  return (row.registration?.deployMode === "NATIVE" && row.module.catalog?.kind === "BACKEND") ||
    (row.registration?.deployMode === "STATIC" && row.module.catalog?.kind === "FRONTEND");
}

function canOpenDelivery(row: ModuleRow) {
  return supportsProjectDeployment(row) || !!row.registration?.managedInstanceId ||
    !!deploymentStatuses.value.get(row.registration?.id || "")?.current;
}

function deliveryLabel(row: ModuleRow) {
  const status = deploymentStatuses.value.get(row.registration?.id || "");
  if (status?.latest && (jobPending(status.latest) || status.latest.status === "UNKNOWN" || status.latest.status === "FAILED")) {
    return jobStatusText(status.latest.status);
  }
  if (status?.health) return deploymentHealthText(status.health);
  if (status?.current) return "已发布 · 待检查";
  if (supportsProjectDeployment(row)) return "待检查";
  if (row.registration?.managedInstanceId) return "旧版目标";
  return row.registration ? "暂不支持发布" : "待配置";
}

function observeDelivery(id: string, status: DeploymentStatus) {
  deploymentStatuses.value.set(id, status);
}

watch([pagedRows, runtimeRefresh], ([pageRows], _, cleanup) => {
  const current = project.value;
  if (!current) return;
  const controller = new AbortController();
  let timer: ReturnType<typeof setTimeout> | undefined;
  cleanup(() => { controller.abort(); if (timer) clearTimeout(timer); });
  const activeRows = pageRows.filter((row) => row.registration?.hostId && supportsProjectDeployment(row));
  if (!activeRows.length) return;
  const update = async (pendingOnly = false) => {
    if (document.hidden && pendingOnly) {
      timer = setTimeout(() => void update(true), 5000);
      return;
    }
    for (let start = 0; start < activeRows.length && !controller.signal.aborted; start += 4) {
      const batch = activeRows.slice(start, start + 4);
      await Promise.allSettled(batch.map(async ({ registration }) => {
        if (!registration || (pendingOnly && !jobPending(deploymentStatuses.value.get(registration.id)?.latest))) return;
        const status = await request<DeploymentStatus>(
          projectDeploymentPath(current.id, registration.id), "GET", undefined, controller.signal,
        );
        if (!controller.signal.aborted) observeDelivery(registration.id, status);
      }));
    }
    if (!controller.signal.aborted && activeRows.some((row) => jobPending(deploymentStatuses.value.get(row.registration!.id)?.latest)))
      timer = setTimeout(() => void update(true), 5000);
  };
  void update();
}, { immediate: true });
watch([runtimeIds, runtimeRefresh], async ([ids], _, cleanup) => {
  const controller = new AbortController();
  cleanup(() => controller.abort());
  runtimes.value = new Map();
  runtimeError.value = "";
  runtimeLoading.value = ids.length > 0;
  if (!ids.length) return;
  try {
    const available = await requestAllPages<SoftwareInstance>(
      "/software-instances",
      controller.signal,
    );
    if (!controller.signal.aborted) {
      const referenced = new Set(ids);
      runtimes.value = new Map(
        available
          .filter((item) => referenced.has(item.id))
          .map((item) => [item.id, item]),
      );
    }
  } catch (error) {
    if (!controller.signal.aborted)
      runtimeError.value = describe(error, "无法读取关联运行对象，请刷新重试");
  } finally {
    if (!controller.signal.aborted) runtimeLoading.value = false;
  }
});
watch(() => props.source, useSource);
onMounted(refresh);
onUnmounted(() => {
  lifetime.abort();
  selectionRequest?.abort();
});
function buildStatusClass(status?: string) {
  if (status === "SUCCESS") return "status-ok";
  if (status === "FAILURE" || status === "ABORTED") return "status-fail";
  if (status === "RUNNING" || status === "QUEUED") return "status-run";
  return "muted";
}
</script>

<template>
  <section :class="{ 'panel-card release-shell': !fixedProject }" aria-label="交付与运行">
    <div class="release-ledger" :class="{ 'release-ledger-fixed': fixedProject }">
      <aside v-if="!fixedProject" class="project-picker" aria-label="项目列表">
        <ElInput
          v-model="projectSearch"
          clearable
          placeholder="输入关键字进行过滤"
        />
        <ul v-if="projectOptions.length" class="picker-list">
          <li v-for="item in projectOptions" :key="item.id">
            <button
              type="button"
              class="picker-item"
              :aria-pressed="item.id === projectId"
              @click="selectProject(item.id)"
            >
              <span>{{ item.name }}</span>
            </button>
          </li>
        </ul>
        <div v-else class="picker-empty">
          <ElEmpty
            :image-size="48"
            :description="
              loading
                ? '正在读取…'
                : projectSearch
                  ? '没有匹配的项目'
                  : '暂无项目'
            "
          />
        </div>
      </aside>

      <div ref="listElement" class="release-main">
        <div class="ops-el-toolbar">
          <div class="ops-el-toolbar-group">
            <ElInput
              v-model="queryDraft"
              clearable
              placeholder="输入模块名称或目录"
              style="width: 220px"
              @keyup.enter="searchModules"
              @clear="searchModules"
            />
            <ElSelect
              v-model="environmentDraft"
              clearable
              :value-on-clear="''"
              placeholder="全部环境"
              style="width: 140px"
              @change="searchModules"
            >
              <ElOption label="全部环境" value="" />
              <ElOption
                v-for="env in project?.environments"
                :key="env.id"
                :label="env.name"
                :value="env.id"
              />
            </ElSelect>
            <ElButton type="primary" @click="searchModules">搜索</ElButton>
            <ElButton @click="clearFilters">清空</ElButton>
          </div>
          <div class="ops-el-toolbar-group">
            <span v-if="project && !fixedProject" class="release-project-meta">
              <span>{{ project.name }}</span>
              <span class="muted">· {{ project.modules.length }} 个模块</span>
              <span v-if="project.archived" class="muted">（已归档）</span>
            </span>
            <ElButton :loading="loading" @click="refresh">刷新</ElButton>
          </div>
        </div>

        <ElAlert
          v-if="pageError || runtimeError"
          type="error"
          show-icon
          :closable="false"
          :title="pageError || runtimeError"
          style="margin-top: 12px"
        />
        <ElAlert
          v-if="setupNotice"
          type="warning"
          show-icon
          style="margin-top: 12px"
          @close="setupNotice = undefined"
        >
          <div class="ops-alert-row">
            <span>
              {{ setupNotice.row.module.name }} · {{ setupNotice.message }}
            </span>
            <ElButton
              v-if="!project?.archived"
              link
              type="primary"
              size="small"
              @click="maintain(setupNotice.row)"
            >
              前往项目管理配置 →
            </ElButton>
          </div>
        </ElAlert>

        <ElTable
          :data="pagedRows"
          border
          stripe
          style="width: 100%; margin-top: 14px"
        >
          <ElTableColumn type="index" label="#" width="48" align="center" />
          <ElTableColumn label="模块名称" min-width="140" show-overflow-tooltip>
            <template #default="{ row }">
              <span>{{ row.module.name }}</span>
            </template>
          </ElTableColumn>
          <ElTableColumn label="模块描述" min-width="135" show-overflow-tooltip>
            <template #default="{ row }">
              <span :class="{ muted: !row.module.catalog?.description }">
                {{ row.module.catalog?.description || "—" }}
              </span>
            </template>
          </ElTableColumn>
          <ElTableColumn label="版本" width="95">
            <template #default="{ row }">
              <span class="mono-cell">
                {{
                  row.registration?.recordedVersion ||
                  (row.record?.number ? `#${row.record.number}` : "—")
                }}
              </span>
            </template>
          </ElTableColumn>
          <ElTableColumn label="类型" width="76" align="center">
            <template #default="{ row }">
              <span>
                {{
                  row.module.catalog?.kind
                    ? moduleKinds[(row as ModuleRow).module.catalog!.kind]
                    : "—"
                }}
              </span>
            </template>
          </ElTableColumn>
          <ElTableColumn label="源码目录" min-width="145" show-overflow-tooltip>
            <template #default="{ row }">
              <span class="mono-cell">{{ row.module.directory || "—" }}</span>
            </template>
          </ElTableColumn>
          <ElTableColumn label="所需环境" min-width="170" show-overflow-tooltip>
            <template #default="{ row }">
              <div class="env-req-cell">
                <span>{{ row.module.catalog?.environment || "—" }}</span>
                <span
                  v-if="row.module.catalog?.dependencies?.length"
                  class="muted"
                  style="font-size: 12px"
                >
                  依赖：{{ row.module.catalog.dependencies.join("、") }}
                </span>
              </div>
            </template>
          </ElTableColumn>
          <ElTableColumn label="环境 / 运行服务器" min-width="200">
            <template #default="{ row }">
              <div class="runtime-cell">
                <span v-if="row.environment?.name || row.registration">
                  {{ row.environment?.name || "默认" }} ·
                  {{
                    row.registration
                      ? hostsById.get(row.registration.hostId) || "待核对"
                      : "—"
                  }}
                </span>
                <span v-else class="muted">未配置</span>
                <span
                  v-if="row.registration?.deployMode || row.registration?.profile || row.registration?.servicePort || row.registration?.nginx?.listenPort"
                  class="muted"
                  style="font-size: 12px"
                >
                  {{ deployModeText(row.registration?.deployMode || row.module.catalog?.runtimeMode) }}
                  <template v-if="row.registration?.profile"> · {{ row.registration.profile }}</template>
                  <template v-if="row.registration?.nginx?.listenPort || row.registration?.servicePort">
                    · :{{ row.registration?.nginx?.listenPort || row.registration?.servicePort }}
                  </template>
                </span>
                <span
                  v-if="row.state"
                  :class="row.state.tone === 'state-success' ? 'status-ok' : 'muted'"
                  style="font-size: 12px"
                >
                  {{ row.state.label }}
                </span>
              </div>
            </template>
          </ElTableColumn>
          <ElTableColumn label="模块地址（自动）" min-width="165" show-overflow-tooltip>
            <template #default="{ row }">
              <a
                v-if="jenkinsLink(row.registration?.computedAccessUrl || row.module.catalog?.accessUrl)"
                class="table-link"
                :href="jenkinsLink(row.registration?.computedAccessUrl || row.module.catalog?.accessUrl)"
                target="_blank"
                rel="noopener noreferrer"
              >
                {{ row.registration?.computedAccessUrl || row.module.catalog?.accessUrl }}
              </a>
              <span v-else class="muted">—</span>
            </template>
          </ElTableColumn>
          <ElTableColumn label="QA 状态" width="120">
            <template #default="{ row }">
              <ElButton
                link
                size="small"
                :disabled="!!opening"
                @click="openBuild(row as ModuleRow, 'record')"
              >
                <span
                  v-if="row.record"
                  :class="buildStatusClass(row.record.status)"
                >
                  <template v-if="row.record.number">#{{ row.record.number }} · </template>
                  {{ jenkinsState(row.record.status).label }}
                </span>
                <span v-else class="lookup-text">查记录</span>
              </ElButton>
            </template>
          </ElTableColumn>
          <ElTableColumn label="发布状态" min-width="140">
            <template #default="{ row }">
              <div class="runtime-cell">
                <span>{{ deliveryLabel(row as ModuleRow) }}</span>
                <small v-if="row.registration && deploymentStatuses.get(row.registration.id)?.current">
                  构建 #{{ deploymentStatuses.get(row.registration.id)?.current?.buildNumber }}
                </small>
                <small v-if="row.registration && deploymentStatuses.get(row.registration.id)?.latest?.observedAt">
                  {{ deploymentTime(deploymentStatuses.get(row.registration.id)?.latest?.observedAt) }}
                </small>
              </div>
            </template>
          </ElTableColumn>
          <ElTableColumn label="操作" width="230" fixed="right">
            <template #default="{ row }">
              <div class="row-ops">
                <ElButton
                  v-if="!project?.archived"
                  link
                  type="primary"
                  size="small"
                  :disabled="!!opening"
                  @click="openBuild(row as ModuleRow, 'qa')"
                >
                  QA
                </ElButton>
                <ElButton
                  link
                  type="primary"
                  size="small"
                  :disabled="!!opening"
                  @click="openBuild(row as ModuleRow, 'logs')"
                >
                  QA 日志
                </ElButton>
                <ElButton
                  v-if="canOpenDelivery(row as ModuleRow) && (!project?.archived || supportsProjectDeployment(row as ModuleRow))"
                  link
                  type="primary"
                  size="small"
                  @click="openRelease(row as ModuleRow)"
                >
                  {{ supportsProjectDeployment(row as ModuleRow) ? "发布与运行" : "旧版发布" }}
                </ElButton>
                <ElButton
                  v-if="!project?.archived && row.registration?.managedInstanceId && !supportsProjectDeployment(row as ModuleRow)"
                  link
                  type="primary"
                  size="small"
                  @click="openRelease(row as ModuleRow, 'verify')"
                >
                  验证
                </ElButton>
                <ElButton
                  v-if="row.runtime && !row.buildService && !supportsProjectDeployment(row as ModuleRow)"
                  link
                  type="primary"
                  size="small"
                  @click="openServiceLog(row.runtime)"
                >
                  日志
                </ElButton>
                <ElButton
                  v-if="!project?.archived"
                  link
                  size="small"
                  @click="maintain(row as ModuleRow)"
                >
                  {{ row.registration ? "草稿" : "配置" }}
                </ElButton>
              </div>
            </template>
          </ElTableColumn>
          <template #empty>
            <ElEmpty
              :description="
                loading
                  ? '正在读取模块…'
                  : moduleSearch || environmentFilter
                    ? '没有符合筛选条件的模块'
                    : '暂无发布模块'
              "
            />
          </template>
        </ElTable>

        <div v-if="visibleRows.length > 0" class="ops-el-pager">
          <ElPagination
            v-model:current-page="rowPage"
            v-model:page-size="rowPageSize"
            :page-sizes="[10, 20, 50]"
            :total="visibleRows.length"
            background
            layout="total, sizes, prev, pager, next"
          />
        </div>
      </div>
    </div>

    <ElDrawer
      :model-value="!!panel"
      :title="panel?.context.action === 'qa' ? 'QA 执行' : 'QA 日志'"
      size="min(680px, 96vw)"
      append-to-body
      destroy-on-close
      :close-on-click-modal="false"
      @update:model-value="
        (open) => {
          if (!open) panel = undefined;
        }
      "
    >
      <template v-if="panel && project">
        <p class="run-context">
          {{ project.name }}<span>/</span
          ><strong>{{ panel.context.moduleName }}</strong>
          <template v-if="panel.context.environmentName">
            <span>/</span>{{ panel.context.environmentName }}
          </template>
        </p>
        <ProjectJenkinsBuild
          :key="
            (panel.context.serviceInstanceId || panel.context.moduleId) +
            panel.context.action
          "
          :project-id="project.id"
          :context="panel.context"
          :monitor="panel.monitor"
          @observed="
            recordBuild(
              panel.context.serviceInstanceId || panel.context.moduleId,
              $event,
            )
          "
          @error="emit('error', $event)"
        />
      </template>
    </ElDrawer>
    <ProjectDeploymentPanel
      v-if="project && deploymentPanel?.registration && deploymentPanel.environment"
      :key="deploymentPanel.registration.id"
      :project-id="project.id"
      :module-id="deploymentPanel.module.id"
      :module-name="deploymentPanel.module.name"
      :deployment-id="deploymentPanel.registration.id"
      :environment="deploymentPanel.environment.name"
      :host-name="hostsById.get(deploymentPanel.registration.hostId) || ''"
      :archived="project.archived"
      @close="deploymentPanel = undefined; runtimeRefresh++"
      @observed="observeDelivery(deploymentPanel.registration.id, $event)"
      @build-observed="recordBuild(deploymentPanel.module.id, $event)"
      @edit-site="maintain(deploymentPanel)"
      @execution-logs="emit('logs', $event)"
      @error="emit('error', $event)"
    />
  </section>
</template>

<style scoped>
.release-shell {
  padding: 16px 18px;
}
.release-ledger.release-ledger-fixed { grid-template-columns: minmax(0, 1fr); }
.release-ledger {
  display: grid;
  grid-template-columns: 210px minmax(0, 1fr);
  align-items: start;
  gap: 16px;
  min-width: 0;
}
.project-picker {
  min-width: 0;
  padding-right: 14px;
  border-right: 1px solid var(--border);
  display: grid;
  gap: 10px;
}
.picker-list {
  list-style: none;
  margin: 0;
  padding: 0;
  display: grid;
  gap: 2px;
  max-height: 520px;
  overflow-y: auto;
}
.picker-item {
  width: 100%;
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 8px;
  padding: 8px 10px;
  border: 0;
  border-radius: 4px;
  background: transparent;
  color: #303133;
  font: inherit;
  font-size: 13px;
  text-align: left;
  overflow-wrap: anywhere;
}
.picker-item:hover {
  background: #f5f7fa;
}
.picker-item[aria-pressed="true"] {
  background: #ecf5ff;
  color: var(--el-color-primary);
}
.picker-empty {
  padding: 8px 0;
}
.release-main {
  min-width: 0;
}
.release-project-meta {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  font-size: 13px;
}
.mono-cell {
  font: 12px/1.5 var(--mono);
  color: #475569;
}
.env-req-cell,
.runtime-cell {
  display: flex;
  flex-direction: column;
  gap: 2px;
}
.table-link {
  color: var(--el-color-primary);
  text-decoration: none;
}
.table-link:hover {
  text-decoration: underline;
}
.lookup-text {
  color: var(--el-color-primary);
}
.status-ok {
  color: #15803d;
}
.status-fail {
  color: #dc2626;
}
.status-run {
  color: #2563eb;
}
.row-ops {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 4px 10px;
}
.run-context {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 8px;
  margin: 0 0 16px;
  font-size: 13px;
  color: var(--muted);
}
.run-context strong {
  color: var(--text);
}
@media (max-width: 900px) {
  .release-ledger {
    grid-template-columns: minmax(0, 1fr);
  }
  .project-picker {
    padding-right: 0;
    padding-bottom: 12px;
    border-right: 0;
    border-bottom: 1px solid var(--border);
  }
}
</style>
