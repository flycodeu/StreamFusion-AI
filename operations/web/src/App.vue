<script setup lang="ts">
import { computed, KeepAlive, nextTick, onMounted, onUnmounted, ref, watch } from "vue";
import { ElDialog, ElMessage, ElMessageBox } from "element-plus";
import { ApiError, prepare, request, send, setCSRF } from "./api";
import OpsWorkspace from "./OpsWorkspace.vue";
import ApplicationProjects from "./ApplicationProjects.vue";
import type { ProjectInstanceSource } from "./applicationProjects";
import SoftwareManagement from "./SoftwareManagement.vue";
import SoftwareCommandTemplates from "./SoftwareCommandTemplates.vue";
import SoftwareDirectory from "./SoftwareDirectory.vue";
import SoftwareLogs from "./SoftwareLogs.vue";
import SoftwareLogDialog from "./SoftwareLogDialog.vue";
import type { LogSelection } from "./jobs";
import HostManagement from "./HostManagement.vue";
import PortManagement from "./PortManagement.vue";
import HostOverview from "./HostOverview.vue";
import ErrorReference from "./ErrorReference.vue";
import SystemMonitor from "./SystemMonitor.vue";
import OpsPagination from "./OpsPagination.vue";
import OpsDiagnosticBanner from "./OpsDiagnosticBanner.vue";
import OpsLoginView from "./OpsLoginView.vue";
import OpsControllerDialogs from "./OpsControllerDialogs.vue";
import { usePagination } from "./pagination";
import { errorHelp, showErrorHelp } from "./errorHelp";

interface Status {
  status: string;
  release: string;
  os: string;
  arch: string;
  listen: string;
  stateDirectory: string;
  startedAt: string;
  uptimeSeconds: number;
  heapBytes: number;
}
interface Settings {
  name: string;
  controllerId: string;
  version: string;
  createdAt: string;
}
interface Credential {
  id: string;
  revision: string;
  name: string;
  kind: string;
  createdAt: string;
  revokedAt: string;
}
interface Audit {
  id: string;
  actor: string;
  action: string;
  result: string;
  createdAt: string;
  traceId: string;
  objectId: string;
}
const nav = [
  {
    id: "overview",
    label: "运行总览",
    icon: "M3 3h7v7H3z M14 3h7v7h-7z M3 14h7v7H3z M14 14h7v7h-7z",
  },
  {
    id: "hosts",
    label: "主机管理",
    icon: "M4 5h16v14H4z M8 9h2 M14 9h2 M8 14h8",
  },
  {
    id: "ports",
    label: "端口管理",
    icon: "M4 6h16v12H4z M8 9v6 M12 9v6 M16 9v6",
  },
  {
    id: "software",
    label: "软件管理",
    icon: "M4 4h6v6H4z M14 4h6v6h-6z M4 14h6v6H4z M14 14h6v6h-6z",
  },
  {
    id: "software-install",
    label: "软件安装",
    icon: "M12 3v12 M7 10l5 5 5-5 M4 17v4h16v-4",
  },
  {
    id: "commands",
    label: "软件配置",
    icon: "M4 4h16v16H4z M7 8l3 3-3 3 M12 15h5",
  },
  {
    id: "software-directory",
    label: "软件目录",
    icon: "M4 5h16v14H4z M8 9h8 M8 13h5 M8 17h8",
  },
  { id: "application-projects", label: "项目维护", icon: "M3 5h7l2 2h9v13H3z M7 11h10 M7 15h7" },
  { id: "instances", label: "项目发布", icon: "M5 5h14v14H5z M9 9h6v6H9z" },
  { id: "managed-instances", label: "上线目标管理", icon: "M4 4h16v6H4z M4 14h16v6H4z" },
  { id: "artifacts", label: "版本制品", icon: "M4 7h16v13H4z M8 4h8v3H8z" },
  { id: "jobs", label: "发布作业", icon: "M6 3h12v4H6z M6 11h12 M6 16h8" },

  {
    id: "credentials",
    label: "连接凭据",
    icon: "M14 3a7 7 0 0 0-6.7 9L3 16v5h5v-3h3l2-2a7 7 0 1 0 1-13z M16 7h.01",
  },
  {
    id: "audit",
    label: "日志记录",
    icon: "M7 3h10v3H7z M7 5H4v16h16V5h-3 M8 11h8 M8 16h5",
  },
  {
    id: "settings",
    label: "系统设置",
    icon: "M4 7h16 M4 17h16 M8 4v6 M16 14v6",
  },
];
const title = computed(
  () => nav.find((item) => item.id === active.value)?.label,
);
const validNavIds = new Set(nav.map((item) => item.id));
function readHashPage(): string | undefined {
  if (typeof globalThis.location?.hash !== "string") return undefined;
  const id = globalThis.location.hash.replace(/^#\/?/, "");
  return validNavIds.has(id) ? id : undefined;
}
const active = ref(readHashPage() || "overview");
const instanceRegistrationSource = ref<ProjectInstanceSource>();
const maintenanceSource = ref<ProjectInstanceSource>();
const managedReturnSource = ref<ProjectInstanceSource>();
function openMaintenance(source?: ProjectInstanceSource) {
  maintenanceSource.value = source;
  active.value = "application-projects";
}
function openManagedTargets(source?: ProjectInstanceSource) {
  managedReturnSource.value = source;
  active.value = "managed-instances";
}
function openProjectInstances(projectId: string, moduleId: string) {
  instanceRegistrationSource.value = { projectId, moduleId };
  active.value = "instances";
}
const commandsVisited = ref(active.value === "commands");
function onHashChange() {
  const id = readHashPage();
  if (id && active.value !== id) active.value = id;
}
watch(active, (page) => {
  if (page !== "instances") instanceRegistrationSource.value = undefined;
  if (page !== "application-projects") maintenanceSource.value = undefined;
  if (page !== "managed-instances") managedReturnSource.value = undefined;
  if (page === "commands") commandsVisited.value = true;
  if (
    typeof globalThis.history?.replaceState === "function" &&
    typeof globalThis.location?.hash === "string"
  ) {
    const nextHash = page === "overview" && !globalThis.location.hash ? "" : `#${page}`;
    if (globalThis.location.hash !== nextHash) {
      globalThis.history.replaceState(
        null,
        "",
        nextHash || `${globalThis.location.pathname}${globalThis.location.search}`,
      );
    }
  }
});
const preparationHostId = ref("");
const dockerHostId = ref("");
const selectedSoftwareId = ref("");
const softwareInstallSearch = ref("");
watch(active, (page) => { if (!["software", "software-install"].includes(page)) dockerHostId.value = ""; });
watch(active, (page) => { if (page !== "software") selectedSoftwareId.value = ""; });
watch(active, (page) => { if (page !== "software-install") softwareInstallSearch.value = ""; });
function showJenkinsInstallation() {
  softwareInstallSearch.value = "Jenkins";
  active.value = "software-install";
}
function showHostPreparation(hostId: string) {
  preparationHostId.value = hostId;
  active.value = "hosts";
}
const navigationOpen = ref(false);
const navigationGroups = [
  { label: "工作空间", ids: ["overview", "hosts", "ports"] },
  { label: "软件中心", ids: ["software", "software-install", "commands", "software-directory"] },
  { label: "应用交付", ids: ["application-projects", "instances", "artifacts", "jobs"] },
  { label: "控制器", ids: ["credentials", "audit", "settings"] },
].map((group) => ({ ...group, items: nav.filter((item) => group.ids.includes(item.id)) }));
const logTab = ref<"jobs" | "runtime" | "audit">("jobs");
const logTabs = [
  { id: "jobs", label: "执行记录" },
  { id: "runtime", label: "运行日志" },
  { id: "audit", label: "操作记录" },
] as const;
const logJobId = ref("");
const logSoftwareId = ref("");
const logDialogSelection = ref<LogSelection>();
const controllerDataPage = (page: string) =>
  page === "credentials" || (page === "audit" && logTab.value === "audit");
function openLogs(selection: LogSelection) {
  if (selection.jobId || selection.softwareId) {
    logDialogSelection.value = selection;
  } else {
    logJobId.value = "";
    logSoftwareId.value = "";
    logTab.value = "jobs";
    active.value = "audit";
  }
}
function selectLog(id: string, source: "jobs" | "runtime") {
  if (source === "jobs") logJobId.value = id;
  else logSoftwareId.value = id;
}
const loggedIn = ref(false);
const busy = ref(false);
const connected = ref(false);
const username = ref("");
const password = ref("");
const loginError = ref("");
const diagnostic = ref<ApiError | null>(null);
const status = ref<Status>();
const settings = ref<Settings>();
const audits = ref<Audit[]>([]);
const auditPage = ref(1), auditPageSize = ref(10), auditLoading = ref(false);
const auditCursors = ref([""]), auditNextCursor = ref("");
let auditRequest = 0;
const credentials = ref<Credential[]>([]);
const { page: credentialPage, pageSize: credentialPageSize, total: credentialTotal, pageItems: pagedCredentials } = usePagination(() => credentials.value);
const credentialDialog = ref(false);
const selected = ref<Credential | null>(null);
const credentialName = ref("");
const credentialKind = ref("TOKEN");
const secret = ref("");
const passwordDialog = ref(false);
const oldPassword = ref("");
const newPassword = ref("");
const confirmPassword = ref("");
const auditDetail = ref<Audit | null>(null);
const showLoginPassword = ref(false);
const loginCapsLock = ref(false);

function focusLoginInput() {
  void nextTick(() => {
    if (loggedIn.value) return;
    const id = username.value.trim()
      ? "ops-login-password"
      : "ops-login-username";
    const el = document.getElementById(id) as HTMLInputElement | null;
    if (el && !el.disabled) el.focus();
  });
}
let timer: ReturnType<typeof setTimeout> | undefined;
const displayTime = (value?: string) =>
  value
    ? new Date(value).toLocaleString("zh-CN", {
        timeZone: "Asia/Shanghai",
        hour12: false,
      })
    : "—";
const uptime = computed(() => {
  const seconds = status.value?.uptimeSeconds || 0;
  return (
    Math.floor(seconds / 3600) + "h " + Math.floor((seconds % 3600) / 60) + "m"
  );
});
const credentialKindLabels: Record<string, string> = {
  TOKEN: "Token 密钥",
  PASSWORD: "密码凭据",
  SSH_KEY: "SSH 私钥",
};
const actionLabels: Record<string, string> = {
  ADMIN_INITIALIZED: "初始化管理员",
  LOGIN: "登录",
  LOGIN_FAILED: "登录失败",
  LOGOUT: "退出",
  PASSWORD_CHANGED: "修改密码",
  SETTINGS_UPDATED: "修改设置",
  CREDENTIAL_SAVED: "保存凭据",
  CREDENTIAL_REVOKED: "撤销凭据",
  BACKUP_CREATED: "创建备份",
  STATE_RESTORED: "恢复状态",
  HOST_PORTS_OBSERVED: "检测主机端口",
  FIREWALL_CHANGE_ACCEPTED: "提交防火墙变更",
  FIREWALL_CHANGE_RESULT: "防火墙变更结果",
  FIREWALL_JOB_CANCELLED: "取消防火墙作业",
  HOST_SAVED: "保存主机连接",
  HOST_CHECKED: "检查主机能力",
  HOST_SOFTWARE_DISCOVERED: "识别主机软件",
  HOST_MAINTENANCE_CHANGED: "修改主机维护状态",
  HOST_DELETED: "移除主机登记",
  HOST_PREPARATION_ACCEPTED: "开始主机接入",
  HOST_PREPARATION_RESULT: "主机接入结果",
  HOST_TERMINAL_INSTALL_ACCEPTED: "开始安装主机终端工具",
  HOST_TERMINAL_INSTALL_RESULT: "主机终端工具安装结果",
  HOST_PREPARATION_REPAIR_ACCEPTED: "开始修复接入权限",
  HOST_PREPARATION_REPAIR_RESULT: "接入权限修复结果",
  HOST_PREPARED: "准备主机运行依赖",
  SOFTWARE_REGISTERED: "登记已有软件",
  SOFTWARE_OBSERVED: "检查软件状态",
  SOFTWARE_OBSERVE_BATCH_ACCEPTED: "提交批量状态检测",
  SOFTWARE_OBSERVE_BATCH_RESULT: "批量状态检测结果",
  SOFTWARE_INSTALL_ACCEPTED: "提交服务安装",
  SOFTWARE_INSTALL_RESULT: "服务安装结果",
  SOFTWARE_ADOPT_ACCEPTED: "提交软件接管",
  SOFTWARE_START_ACCEPTED: "提交软件启动",
  SOFTWARE_STOP_ACCEPTED: "提交软件停止",
  SOFTWARE_UNMANAGE_ACCEPTED: "提交解除软件管理",
  SOFTWARE_ADOPT_RESULT: "软件接管结果",
  SOFTWARE_START_RESULT: "软件启动结果",
  SOFTWARE_STOP_RESULT: "软件停止结果",
  SOFTWARE_UNMANAGE_RESULT: "解除软件管理结果",
  SOFTWARE_UNMANAGED: "解除软件管理",
  ARTIFACT_UPLOADED: "上传版本制品",
  INSTANCE_SAVED: "保存应用实例",
  BINDING_SAVED: "保存外部依赖",
  DEPENDENCY_CHECKED: "检测依赖连通",
  DEPLOYMENT_CREATED: "创建发布作业",
  JOB_CANCELLED: "取消发布作业",
  EXTERNAL_BINDING_REGISTERED: "登记外部依赖",
  INSTANCE_DRAFT_SAVED: "保存应用草稿",
  ARTIFACT_IMPORTED: "导入版本制品",
  JAR_PACKAGED: "打包 Java 制品",
  PUBLISH_ACCEPTED: "提交发布",
  PUBLISH_RESULT: "发布结果",
  MONITORING_CHANGED: "修改自动检测",
  OBSERVATION_BATCH_ACCEPTED: "提交批量检测",
  OBSERVATION_BATCH_CANCELLED: "取消批量检测",
  INSTALL_MATERIAL_CREATED: "登记安装材料",
  INSTALL_MATERIAL_VERIFIED: "校验安装材料",
  INSTALL_MATERIAL_DELETED: "删除安装材料",
  INSTALL_MATERIAL_LIBRARY_IMPORTED: "导入内置安装材料",
  OFFICIAL_MATERIAL_PREPARED: "获取官方安装材料",
  SOFTWARE_INSTALL_PLAN_CREATED: "预检软件安装",
  SOFTWARE_UNINSTALL_ACCEPTED: "提交软件卸载",
  SOFTWARE_UNINSTALL_RESULT: "软件卸载结果",
  SOFTWARE_CONFIGURATION_PLAN_CREATED: "预检软件配置",
  SOFTWARE_CONFIGURATION_ACCEPTED: "提交软件配置",
  SOFTWARE_CONFIGURATION_RESULT: "软件配置结果",
  SOFTWARE_DOCKER_ACCESS_GRANT_ACCEPTED: "提交 Docker 账号授权",
  SOFTWARE_DOCKER_ACCESS_REVOKE_ACCEPTED: "提交撤销 Docker 账号授权",
  SOFTWARE_DOCKER_ACCESS_RESULT: "Docker 账号权限结果",
  SOFTWARE_DOCKER_ACCESS_NOT_STARTED: "Docker 账号授权未执行",
  NODE_ENVIRONMENT_OBSERVED: "检测 Node 环境",
  NODE_ENVIRONMENT_REGISTERED: "登记 Node 环境",
  NODE_ENVIRONMENT_RELEASED: "解除 Node 环境登记",
  NODE_ENVIRONMENT_PLAN_CREATED: "预检 Node 操作",
  NODE_ENVIRONMENT_CHANGE_ACCEPTED: "提交 Node 操作",
  NODE_ENVIRONMENT_CHANGE_RESULT: "Node 操作结果",
  APPLICATION_PROJECT_SAVED: "保存项目配置",
  APPLICATION_PROJECT_ARCHIVED: "归档项目",
  APPLICATION_PROJECT_DELETED: "删除项目",
  APPLICATION_MODULE_DELETED: "删除项目模块",
  APPLICATION_SERVICE_INSTANCE_SAVED: "保存实例登记",
  APPLICATION_SERVICE_INSTANCE_DELETED: "移除实例登记",
  JENKINS_UNLOCK_READ: "读取 Jenkins 首次解锁密码",
  SOFTWARE_LOGIN_SAVED: "保存软件登录账号",
  SOFTWARE_LOGIN_READ: "读取软件登录密码",
  COMMAND_TEMPLATE_DELETED: "删除安装方案",
  GIT_ACCOUNT_SAVED: "保存 Git 账号",
  GIT_ACCOUNT_DELETED: "删除 Git 账号",
  GIT_REPOSITORY_PLAN_CREATED: "预检仓库账号配置",
  GIT_REPOSITORY_CHANGE_ACCEPTED: "提交仓库账号配置",
  GIT_REPOSITORY_CHANGE_RESULT: "仓库账号配置结果",
  GIT_REPOSITORY_NOT_STARTED: "仓库配置未执行",
  GIT_REPOSITORY_TEST: "测试仓库读取",
  NODE_REGISTRY_CREATED: "添加 npm 镜像地址",
  NODE_REGISTRY_UPDATED: "修改 npm 镜像地址",
  NODE_REGISTRY_DELETED: "删除 npm 镜像地址",
  NODE_REGISTRY_NOT_STARTED: "npm 镜像切换未执行",
  PLUGIN_PLAN_CREATED: "预检工具安装",
  PLUGIN_INSTALL_ACCEPTED: "提交工具安装",
  PLUGIN_INSTALL_RESULT: "工具安装结果",
  PLUGIN_REMOVE_ACCEPTED: "提交工具卸载",
  PLUGIN_REMOVE_RESULT: "工具卸载结果",
  PLUGIN_VERIFIED: "校验工具环境",
  PLUGIN_JOB_CANCELLED: "取消工具作业",
  PREVIEW_HANDOFF: "登记预览服务",
  ZLM_HANDOFF: "登记媒体服务",
};
function auditResult(action: string, result: string) {
  if (action === "GIT_REPOSITORY_TEST" && result === "GIT_REMOTE_READABLE")
    return { label: "成功", tone: "state-success" };
  if (["SUCCESS", "SUCCEEDED", "READY"].includes(result) || (action === "HOST_PORTS_OBSERVED" && !result))
    return { label: "成功", tone: "state-success" };
  if (action === "HOST_SOFTWARE_DISCOVERED") {
    const discovery = /^SUCCESS candidates=\d+ unavailable=(\d+) limited=(true|false)$/.exec(result);
    if (discovery) return Number(discovery[1]) || discovery[2] === "true"
      ? { label: "部分未确认", tone: "state-muted" }
      : { label: "成功", tone: "state-success" };
  }
  if (["FAILED", "AUTH_FAILED", "SOFTWARE_LOGIN_REAUTH_FAILED"].includes(result)) return { label: "失败", tone: "state-error" };
  const labels: Record<string, string> = { UNKNOWN: "待确认", QUEUED: "排队中", RUNNING: "执行中", CANCELLED: "已取消" };
  return { label: labels[result] || "待核对", tone: "state-muted" };
}
function report(e: unknown) {
  if (e instanceof ApiError && e.code === "HOST_CHECK_BUSY") return;
  if (e instanceof ApiError) {
    diagnostic.value = e;
    if (e.code === "AUTH_REQUIRED") {
      loggedIn.value = false;
      password.value = "";
      void prepare();
    }
    return;
  }
  ElMessage.error(e instanceof Error ? e.message : "操作失败");
}
async function execute(action: () => Promise<void>) {
  if (busy.value) return;
  busy.value = true;
  try {
    await action();
  } catch (e) {
    report(e);
  } finally {
    busy.value = false;
  }
}
async function refreshStatus() {
  try {
    status.value = await request<Status>("/status");
    connected.value = true;
  } catch (e) {
    connected.value = false;
    throw e;
  }
}
async function loadAudit(page = auditPage.value, size = auditPageSize.value) {
  const revision = ++auditRequest;
  auditLoading.value = true;
  try {
    const params = new URLSearchParams({ limit: String(size), before: page === 1 ? "" : auditCursors.value[page - 1] || "" });
    const data = await request<{ items: Audit[]; nextCursor: string }>(`/audit?${params}`);
    if (revision !== auditRequest) return;
    auditPage.value = page;
    auditPageSize.value = size;
    audits.value = data.items;
    auditNextCursor.value = data.nextCursor;
    auditCursors.value = auditCursors.value.slice(0, page);
    if (data.nextCursor) auditCursors.value[page] = data.nextCursor;
  } catch (error) {
    if (revision === auditRequest) throw error;
  } finally {
    if (revision === auditRequest) auditLoading.value = false;
  }
}
async function changeAuditPage(page: number) {
  if (page >= 1 && page <= auditCursors.value.length && !auditLoading.value) {
    try { await loadAudit(page); } catch (error) { report(error); }
  }
}
async function latestAudit() {
  await loadAudit(1);
}
async function changeAuditSize(size: number) {
  try { await loadAudit(1, size); } catch (error) { report(error); }
}
async function load() {
  const [config, items] = await Promise.all([
    request<Settings>("/settings"),
    request<Credential[]>("/credentials"),
    refreshStatus(),
    loadAudit(),
  ]);
  settings.value = config;
  credentials.value = items;
}
async function login() {
  loginError.value = "";
  await execute(async () => {
    await prepare();
    try {
      const data = await request<{ username: string; csrfToken: string }>(
        "/auth/login",
        "POST",
        { username: username.value, password: password.value },
      );
      setCSRF(data.csrfToken);
      username.value = data.username;
      password.value = "";
      loggedIn.value = true;
      diagnostic.value = null;
      await load();
    } catch (e) {
      loginError.value = e instanceof Error ? e.message : "登录失败";
    }
  });
}
async function logout() {
  await execute(async () => {
    await request("/auth/logout", "POST");
    loggedIn.value = false;
    diagnostic.value = null;
    await prepare();
  });
}
function openCredential(item: Credential | null = null) {
  selected.value = item;
  credentialName.value = item?.name || "";
  credentialKind.value = item?.kind || "TOKEN";
  secret.value = "";
  credentialDialog.value = true;
}
async function saveCredential() {
  await execute(async () => {
    const item = selected.value;
    await request(
      item ? "/credentials/" + item.id + "/revisions" : "/credentials",
      "POST",
      {
        name: credentialName.value,
        kind: credentialKind.value,
        secret: secret.value,
        ...(item ? { expectedRevision: item.revision } : {}),
      },
    );
    secret.value = "";
    credentialDialog.value = false;
    await load();
    ElMessage.success("凭据已保存");
  });
}
async function revoke(item: Credential) {
  try {
    await ElMessageBox.confirm(
      "撤销后该修订将不可使用，确认继续？",
      "撤销凭据",
      { confirmButtonText: "撤销", cancelButtonText: "取消", type: "warning" },
    );
  } catch {
    return;
  }
  await execute(async () => {
    await request(
      "/credentials/" + item.id + "/revisions/" + item.revision + "/revoke",
      "POST",
    );
    await load();
  });
}
function latest(item: Credential) {
  return !credentials.value.some(
    (other) =>
      other.id === item.id && Number(other.revision) > Number(item.revision),
  );
}
async function changePassword() {
  if (newPassword.value !== confirmPassword.value) {
    ElMessage.error("两次新密码不一致");
    return;
  }
  await execute(async () => {
    await request("/auth/password", "POST", {
      currentPassword: oldPassword.value,
      newPassword: newPassword.value,
    });
    oldPassword.value = "";
    newPassword.value = "";
    confirmPassword.value = "";
    passwordDialog.value = false;
    loggedIn.value = false;
    await prepare();
    ElMessage.success("密码已修改，请重新登录");
  });
}
async function backup() {
  await execute(async () => {
    const response = await send("/backups", "POST");
    if (!response.ok) {
      const body = await response.json();
      throw new ApiError(body.msg, body.code, body.traceId, body.timestamp);
    }
    const objectURL = URL.createObjectURL(await response.blob());
    const link = document.createElement("a");
    link.href = objectURL;
    link.download = "streamfusion-ops-backup.tar.gz";
    link.click();
    setTimeout(() => URL.revokeObjectURL(objectURL), 10000);
    await loadAudit();
  });
}
function poll() {
  timer = setTimeout(async () => {
    if (loggedIn.value && !document.hidden) {
      try {
        if (controllerDataPage(active.value)) {
          await load();
        } else {
          await refreshStatus();
        }
      } catch (e) {
        report(e);
      }
    }
    poll();
  }, 15000);
}
watch(
  [title, loggedIn, () => settings.value?.name],
  () => {
    const siteName = settings.value?.name || "StreamFusion Ops";
    document.title = loggedIn.value
      ? `${title.value} · ${siteName}`
      : `控制器登录 · ${siteName}`;
  },
  { immediate: true },
);
watch(loggedIn, (value) => {
  if (!value) {
    auditRequest += 1;
    auditLoading.value = false;
    logDialogSelection.value = undefined;
    showLoginPassword.value = false;
    loginCapsLock.value = false;
    focusLoginInput();
  }
});
watch([active, logTab], ([page]) => {
  if (loggedIn.value && controllerDataPage(page)) {
    void load().catch(report);
  }
});
onMounted(async () => {
  const hashPage = readHashPage();
  if (hashPage) active.value = hashPage;
  if (typeof globalThis.addEventListener === "function") {
    globalThis.addEventListener("hashchange", onHashChange);
  }
  busy.value = true;
  try {
    await prepare();
    const me = await request<{ username: string }>("/auth/me");
    username.value = me.username;
    loggedIn.value = true;
    await load();
  } catch (e) {
    if (!(e instanceof ApiError && e.code === "AUTH_REQUIRED")) report(e);
  } finally {
    busy.value = false;
    if (!loggedIn.value) focusLoginInput();
    poll();
  }
});
onUnmounted(() => {
  auditRequest += 1;
  if (typeof globalThis.removeEventListener === "function") {
    globalThis.removeEventListener("hashchange", onHashChange);
  }
  if (timer) clearTimeout(timer);
});
</script>

<template>
  <div class="shell">
    <button
      v-if="loggedIn && navigationOpen"
      class="nav-backdrop"
      aria-label="收起导航"
      @click="navigationOpen = false"
    ></button>
    <aside
      v-if="loggedIn"
      id="ops-navigation"
      class="sidebar"
      :class="{ 'is-open': navigationOpen, 'delivery-navigation': ['application-projects', 'instances', 'managed-instances', 'artifacts', 'jobs'].includes(active) }"
    >
      <div class="brand">
        <img
          class="brand-logo"
          src="/streamfusion.svg"
          alt=""
          width="34"
          height="34"
        /><span>StreamFusion<small>运维控制台</small></span>
      </div>
      <nav aria-label="导航">
        <div
          v-for="group in navigationGroups"
          :key="group.label"
          class="nav-group"
        >
          <span class="nav-group-label">{{ group.label }}</span>
          <button
            v-for="item in group.items"
            :key="item.id"
            :class="{ active: active === item.id || (active === 'managed-instances' && item.id === 'application-projects') }"
            :aria-current="active === item.id ? 'page' : undefined"
            @click="
              active = item.id;
              navigationOpen = false;
            "
          >
            <svg class="nav-icon" viewBox="0 0 24 24" aria-hidden="true">
              <path :d="item.icon" />
            </svg>
            {{ item.label }}
          </button>
        </div>
      </nav>
      <div class="side-foot">
        {{ status?.os || "—" }} · {{ status?.arch || "—" }}<br /><span
          >v{{ status?.release || "—" }}</span
        >
      </div>
    </aside>
    <main class="main" :class="{ 'login-main': !loggedIn, 'logs-main': loggedIn && active === 'audit' }">
      <header v-if="loggedIn">
        <button
          class="navigation-toggle"
          aria-label="展开导航"
          aria-controls="ops-navigation"
          :aria-expanded="navigationOpen"
          @click="navigationOpen = !navigationOpen"
        >
          <svg viewBox="0 0 24 24" aria-hidden="true">
            <path d="M4 6h16M4 12h16M4 18h16" />
          </svg>
        </button>
        <h1>{{ title }}</h1>
        <div class="account">
          <svg class="account-icon" viewBox="0 0 24 24" aria-hidden="true">
            <circle cx="12" cy="8" r="3" />
            <path d="M5 21v-3a7 7 0 0 1 14 0v3" />
          </svg>
          <span>{{ username }}</span>
          <button class="quiet" :disabled="busy" @click="logout">退出</button>
        </div>
      </header>
      <OpsLoginView
        v-if="!loggedIn"
        v-model:username="username"
        v-model:password="password"
        v-model:show-login-password="showLoginPassword"
        v-model:login-caps-lock="loginCapsLock"
        :busy="busy"
        :login-error="loginError"
        @clear-error="loginError = ''"
        @submit="login"
      />
      <template v-else>
        <OpsDiagnosticBanner
          :diagnostic="diagnostic"
          @close="diagnostic = null"
        />
        <SoftwareCommandTemplates class="content" v-if="commandsVisited" v-show="active === 'commands'" :active="active === 'commands'" />
        <SoftwareDirectory v-if="active === 'software-directory'" @detail="(id) => { selectedSoftwareId = id; active = 'software'; }" />
        <KeepAlive>
          <SoftwareManagement
            v-if="active === 'software' || active === 'software-install'"
            :view="active === 'software-install' ? 'install' : 'manage'"
            :docker-host-id="dockerHostId"
            :install-search="softwareInstallSearch"
            :selected-software-id="selectedSoftwareId"
            @selection-opened="(id) => { if (selectedSoftwareId === id) selectedSoftwareId = ''; }"
            @error="report"
            @preparation="showHostPreparation"
            @navigate="active = $event === 'install' ? 'software-install' : 'software'"
            @logs="openLogs"
          />
        </KeepAlive>
        <HostManagement
          v-if="active === 'hosts'"
          :preparation-host-id="preparationHostId"
          @error="report"
          @preparation-opened="preparationHostId = ''"
          @credential-saved="load"
          @docker="(hostId) => { dockerHostId = hostId; active = 'software-install'; }"
        />
        <PortManagement
          v-else-if="active === 'ports'"
          @error="report"
          @preparation="showHostPreparation"
        />
        <OpsWorkspace
          v-else-if="['instances', 'managed-instances', 'artifacts', 'jobs'].includes(active)"
          :page="active"
          :application-source="instanceRegistrationSource"
          @application-source-used="instanceRegistrationSource = undefined"
          @maintain="openMaintenance($event || managedReturnSource)"
          @logs="openLogs"
          @error="report"
        />
        <ApplicationProjects
          class="content"
          v-else-if="active === 'application-projects'"
          :source="maintenanceSource"
          @source-used="maintenanceSource = undefined"
          @manage-targets="openManagedTargets"
          @error="report"
          @install-jenkins="showJenkinsInstallation"
          @view-instances="openProjectInstances"
        />
        <HostOverview
          v-else-if="active === 'overview'"
          :controller="status"
          :connected="connected"
          :uptime="uptime"
          @error="report"
        />
        <section
          v-else-if="active === 'credentials'"
          class="content panel-card"
        >
          <div class="section-head">
            <h2>凭据修订</h2>
            <div class="section-actions">
              <button
                class="secondary"
                :disabled="busy"
                @click="execute(() => load())"
              >
                刷新
              </button>
              <button
                class="primary"
                :disabled="busy"
                @click="openCredential()"
              >
                新增凭据
              </button>
            </div>
          </div>
          <p class="install-note">
            要找回 MySQL 或 Redis 的安装密码，请在<a href="#software">软件管理</a>中打开对应实例，进入「登录账号」并选择「复制密码」。
          </p>
          <div v-if="credentials.length" class="table-scroll">
            <table>
              <thead>
                <tr>
                  <th>名称</th>
                  <th>类型</th>
                  <th>修订</th>
                  <th>创建时间</th>
                  <th>状态</th>
                  <th>操作</th>
                </tr>
              </thead>
              <tbody>
                <tr v-for="item in pagedCredentials" :key="item.id + item.revision">
                  <td>{{ item.name }}</td>
                  <td>{{ credentialKindLabels[item.kind] || item.kind }}</td>
                  <td>r{{ item.revision }}</td>
                  <td>{{ displayTime(item.createdAt) }}</td>
                  <td>
                    <span
                      class="state-tag"
                      :class="item.revokedAt ? 'state-muted' : 'state-success'"
                    >
                      {{ item.revokedAt ? "已撤销" : "可用" }}
                    </span>
                  </td>
                  <td>
                    <button
                      v-if="!item.revokedAt && latest(item)"
                      class="quiet"
                      :disabled="busy"
                      @click="openCredential(item)"
                    >
                      新修订</button
                    ><button
                      v-if="!item.revokedAt"
                      class="quiet danger"
                      :disabled="busy"
                      @click="revoke(item)"
                    >
                      撤销
                    </button>
                  </td>
                </tr>
              </tbody>
            </table>
          </div>
          <div v-else class="empty">尚未保存凭据</div>
          <OpsPagination v-if="credentials.length" v-model:page="credentialPage" v-model:page-size="credentialPageSize" :total="credentialTotal" label="凭据分页" />
        </section>
        <section v-else-if="active === 'audit'" class="content panel-card logs-panel">
          <div class="log-record-tabs" role="tablist" aria-label="日志类型">
            <button v-for="entry in logTabs"
              :key="entry.id" role="tab" :aria-selected="logTab === entry.id" :class="{ active: logTab === entry.id }"
              @click="logTab = entry.id">{{ entry.label }}</button>
          </div>
          <SoftwareLogs v-if="logTab !== 'audit'" :mode="logTab" :job-id="logJobId" :software-id="logSoftwareId" @selected="selectLog" />
          <template v-else>
          <div class="section-head">
            <h2>操作记录</h2>
            <button
              class="secondary"
              :disabled="busy || auditLoading"
              @click="execute(latestAudit)"
            >
              最新记录
            </button>
          </div>
          <div v-if="audits.length" class="table-scroll">
            <table>
              <thead>
                <tr>
                  <th>时间</th>
                  <th>操作者</th>
                  <th>动作</th>
                  <th>结果</th>
                  <th>操作</th>
                </tr>
              </thead>
              <tbody>
                <tr v-for="item in audits" :key="item.id">
                  <td>{{ displayTime(item.createdAt) }}</td>
                  <td>{{ item.actor }}</td>
                  <td>{{ actionLabels[item.action] || "其他操作" }}</td>
                  <td>
                    <span
                      class="state-tag"
                      :class="auditResult(item.action, item.result).tone"
                    >
                      {{ auditResult(item.action, item.result).label }}
                    </span>
                  </td>
                  <td>
                    <button class="quiet" @click="auditDetail = item">
                      详情
                    </button>
                  </td>
                </tr>
              </tbody>
            </table>
          </div>
          <div v-else class="empty">暂无操作记录</div>
          <OpsPagination :page="auditPage" :page-size="auditPageSize" :has-next="!!auditNextCursor" :disabled="auditLoading" label="操作记录分页" @update:page="changeAuditPage" @update:page-size="changeAuditSize" />
          </template>
        </section>
        <section v-else-if="active === 'settings'" class="content settings">
          <div class="settings-meta" aria-label="服务信息">
            <article class="settings-info-card">
              <span class="settings-card-icon" aria-hidden="true"><svg viewBox="0 0 24 24"><rect x="3" y="4" width="18" height="12" rx="2"/><path d="M8 20h8M12 16v4M7 8h.01M11 8h6"/></svg></span>
              <div><span>监听地址</span>
              <code>{{ status?.listen || "—" }}</code>
              </div>
            </article>
            <article class="settings-info-card">
              <span class="settings-card-icon" aria-hidden="true"><svg viewBox="0 0 24 24"><path d="m12 3 9 5-9 5-9-5 9-5ZM3 12l9 5 9-5M3 16l9 5 9-5"/></svg></span>
              <div><span>服务版本</span>
              <code>v{{ status?.release || "—" }}</code>
              </div>
            </article>
          </div>
          <SystemMonitor @error="report" />
          <div class="section-head"><h2>设置与维护</h2></div>
          <div class="settings-actions">
          <div class="setting-action">
            <div>
              <strong>状态备份</strong>
            </div>
            <button class="secondary" :disabled="busy" @click="backup">
              下载备份
            </button>
          </div>
          <div class="setting-action">
            <div>
              <strong>管理员密码</strong>
            </div>
            <button
              class="secondary"
              :disabled="busy"
              @click="passwordDialog = true"
            >
              修改密码
            </button>
          </div>
          <div class="setting-action">
            <strong>错误说明</strong>
            <button class="secondary" @click="showErrorHelp('')">查看文档</button>
          </div>
          </div>
        </section>
      </template>
    </main>
    <SoftwareLogDialog :selection="logDialogSelection" @close="logDialogSelection = undefined" />
    <ElDialog v-model="errorHelp.open" title="错误说明" width="min(1180px, 96vw)" align-center destroy-on-close append-to-body>
      <ErrorReference :initial-code="errorHelp.code" />
    </ElDialog>
    <OpsControllerDialogs
      v-model:credential-dialog="credentialDialog"
      v-model:credential-name="credentialName"
      v-model:credential-kind="credentialKind"
      v-model:secret="secret"
      v-model:password-dialog="passwordDialog"
      v-model:old-password="oldPassword"
      v-model:new-password="newPassword"
      v-model:confirm-password="confirmPassword"
      v-model:audit-detail="auditDetail"
      :selected="selected"
      :busy="busy"
      :action-labels="actionLabels"
      :audit-result="auditResult"
      @save-credential="saveCredential"
      @change-password="changePassword"
    />
  </div>
</template>
