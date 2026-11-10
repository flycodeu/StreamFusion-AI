import type { ObservationAttempt } from "./observation";
import type {
  InstallationPaths,
  OfficialInstallation,
  ServiceConfiguration,
  ServiceRecipe,
} from "./serviceInstallation";
export type SoftwareType =
  | "GIT"
  | "NODE"
  | "JAVA"
  | "DOCKER"
  | "MAVEN"
  | "MINIO"
  | "CODEX"
  | "CLAUDE"
  | "MEDIAMTX"
  | "MYSQL"
  | "REDIS"
  | "ZLM"
  | "PYTHON"
  | "FFMPEG"
  | "NGINX"
  | "JENKINS"
  | "SEAWEEDFS"
  | "LABEL_STUDIO"
  | "OTHER";
export type SoftwareRuntime = "SYSTEMD" | "DOCKER" | "TOOL";
export type SoftwareAction = "START" | "STOP" | "RESTART" | "RELOAD" | "PAUSE" | "UNPAUSE";

export interface SoftwareMetadata {
  versionSource?: string;
  versionStatus?: string;
  packageName?: string;
  executablePath?: string;
  serviceFile?: string;
  listeningPorts?: string[];
}

export interface SoftwareInstance {
  configurationAvailable?: boolean;
  id: string;
  name: string;
  hostId: string;
  hostName: string;
  softwareType: SoftwareType;
  softwareVersion: string;
  runtimeKind: SoftwareRuntime;
  locator: string;
  managementMode: "OBSERVE" | "MANAGED" | "RELEASED";
  desiredState: "UNCHANGED" | "RUNNING" | "STOPPED" | "INSTALLED" | "REMOVED" | "PAUSED";
  actualState:
    "UNKNOWN" | "RUNNING" | "STOPPED" | "INSTALLED" | "MISSING" | "FAILED" | "REMOVED" | "PAUSED";
  observation?: SoftwareMetadata & {
    state: SoftwareInstance["actualState"];
    identityHash: string;
    version: string;
    observedAt: string;
    errorCode: string;
    message: string;
    source: string;
  };
  latestAttempt?: ObservationAttempt;
  observationRevision?: number;
  freshness: "UNOBSERVED" | "FRESH" | "STALE";
  version: string;
  lastJobId: string;
  errorCode: string;
  dataOwnership: string;
  createdAt: string;
  updatedAt: string;
  installationStatus?:
    "" | "INSTALLING" | "READY" | "FAILED" | "UNKNOWN" | "CANCELLED" | "UNINSTALLED";
  installationJobId?: string;
  nodeEnvironmentId?: string;
  nodeDataPaths?: string[];
  installationPaths?: InstallationPaths;
  installationEvidence?: {
    observedVersion: string;
    observedAt: string;
    health: string;
    containerId: string;
    binaryPath?: string;
  };
  installation?: {
    source?: {type:string;sha256?:string;byteSize?:number};
    definition?: ServiceRecipe;
    config?: ServiceConfiguration;
    official?: OfficialInstallation;
  };
}

export interface SoftwareJob {
  id: string;
  status: string;
  phase: string;
  errorCode: string;
  traceId: string;
  updatedAt: string;
  targetAckPending: boolean;
}

export function softwareHasInstallationRecord(item: SoftwareInstance): boolean {
  return !!(item.installationJobId && item.installation?.definition && item.installation.config);
}

export function softwareIsHistoryOnly(item: SoftwareInstance): boolean {
  return item.locator === `pending:${item.id}` && item.managementMode !== "MANAGED" &&
    (item.installationStatus === "FAILED" || item.installationStatus === "CANCELLED");
}

export interface SoftwareMutationResult {
  softwareInstanceId: string;
  jobId?: string;
}

export interface SoftwareRegistrationDraft {
  name: string;
  hostId: string;
  softwareType: SoftwareType;
  runtimeKind: SoftwareRuntime;
  locator: string;
  softwareVersion: string;
}

export interface SoftwareDiscoveryCandidate extends SoftwareMetadata {
  name: string;
  softwareType: SoftwareType;
  runtimeKind: SoftwareRuntime;
  locator: string;
  state: SoftwareInstance["actualState"];
  version: string;
  ports: string[];
  configPaths: string[];
  registeredId?: string;
}

export interface SoftwareDiscoveryReport {
  hostId: string;
  hostVersion: string;
  observedAt: string;
  limited: boolean;
  candidates: SoftwareDiscoveryCandidate[];
  checks: {
    source: "SYSTEMD" | "DOCKER" | "TOOLS" | "CONFIG";
    status: "AVAILABLE" | "UNAVAILABLE";
    code: string;
  }[];
  configurations: {
    softwareType: SoftwareType;
    path: string;
    port?: number;
    bindAddress?: string;
  }[];
}

export const softwareTypes: Record<SoftwareType, string> = {
  GIT: "Git",
  NODE: "Node.js",
  DOCKER: "Docker Engine",
  JAVA: "Java",
  MAVEN: "Maven",
  MINIO: "MinIO",
  CODEX: "Codex CLI",
  CLAUDE: "Claude Code",
  MEDIAMTX: "MediaMTX",
  MYSQL: "MySQL",
  REDIS: "Redis",
  ZLM: "ZLMediaKit",
  PYTHON: "Python",
  FFMPEG: "FFmpeg",
  NGINX: "Nginx",
  JENKINS: "Jenkins",
  SEAWEEDFS: "SeaweedFS",
  LABEL_STUDIO: "Label Studio",
  OTHER: "其他服务",
};
export function softwareTypeLabel(item: SoftwareInstance) {
  const definition = item.installation?.definition;
  return definition?.commands && !definition.commands.builtin ? definition.name : softwareTypes[item.softwareType] || definition?.name || "其他软件";
}
export const softwareModes = {
  OBSERVE: "只读登记",
  MANAGED: "已接管",
  RELEASED: "已解除",
};
export const softwareRuntimeLabels: Record<SoftwareRuntime, string> = {
  SYSTEMD: "systemd 服务",
  DOCKER: "Docker 容器",
  TOOL: "程序工具",
};

export function softwareVersionLabel(
  metadata?: SoftwareMetadata,
  runtime?: SoftwareRuntime,
) {
  if (metadata?.versionSource === "TEMPLATE" || metadata?.versionStatus === "DECLARED") return "配置版本";
  if (metadata?.versionSource === "PACKAGE") return "安装包版本";
  if (metadata?.versionSource === "IMAGE_REFERENCE" || runtime === "DOCKER")
    return "镜像引用";
  return "程序版本";
}

export function softwareVersionReason(
  metadata?: SoftwareMetadata,
  runtime?: SoftwareRuntime,
) {
  if (metadata?.versionSource === "TEMPLATE" || metadata?.versionStatus === "DECLARED") return "来自安装配置";
  if (metadata?.versionStatus === "METADATA_UNAVAILABLE")
    return "版本信息不可读取";
  if (metadata?.versionStatus === "METADATA_LIMIT") return "本次未探测版本";
  if (
    runtime === "SYSTEMD" &&
    metadata?.versionStatus === "NO_PACKAGE_EVIDENCE"
  )
    return "未找到关联的软件包";
  if (runtime === "SYSTEMD") return "尚未采集版本";
  return "尚未识别";
}

export function softwareVersionText(value?: string) {
  if (!value) return "";
  return (
    value.match(/(?:openjdk version|java version)\s+"([^"]+)"/)?.[1] ||
    value.match(/^(?:Python|ffmpeg version)\s+(\S+)/)?.[1] ||
    value
  );
}
export const softwareDesiredLabels = {
  PAUSED: "暂停",
  REMOVED: "已卸载",
  UNCHANGED: "保持现状",
  INSTALLED: "已安装（按需使用）",
  RUNNING: "运行",
  STOPPED: "停止",
};
export const softwareActualLabels = {
  PAUSED: "已暂停",
  REMOVED: "已卸载",
  UNKNOWN: "未确认",
  RUNNING: "运行中",
  STOPPED: "已停止",
  INSTALLED: "已安装",
  MISSING: "未找到",
  FAILED: "运行异常",
};
export const softwareJobLabels: Record<string, string> = {
  QUEUED: "排队中",
  RUNNING: "执行中",
  SUCCEEDED: "已完成",
  FAILED: "执行失败",
  UNKNOWN: "结果未确认",
  CANCELLED: "已取消",
};
export const softwareJobErrorMessages: Record<string, string> = {
  NODE_REGISTRY_CHANGED: "镜像列表或账号配置已变化，请重新预检并确认。",
  NODE_REGISTRY_RESULT_MISMATCH: "账号镜像回读与所选地址不一致，请核对原作业。",
  NODE_REGISTRY_NPM_REQUIRED: "请先在此环境安装带有 npm 的 Node 版本。",
  NODE_REGISTRY_PATH_UNSAFE: "请检查 .npmrc 的文件归属、权限和链接。",
  NODE_REGISTRY_READ_FAILED: "npm 配置不可读，请检查 .npmrc 和 npm 程序。",
  NODE_REGISTRY_CONFIG_UNSUPPORTED: "registry 包含变量或认证地址，请在服务器核对 .npmrc。",
  NODE_REGISTRY_CONFIG_REJECTED: "npm 拒绝此配置，原文件未替换；请检查配置格式。",
  NODE_REGISTRY_OTHER_CONFIG_CHANGED: "新配置改变了其他设置，原文件未替换；请核对 .npmrc。",
  NODE_REGISTRY_RESULT_UNCONFIRMED: "镜像切换结果待确认，请核对原作业。",
  DOCKER_CONTAINER_DEPENDENCIES_UNCONFIRMED: "Docker Engine 当前不可访问，无法确认容器依赖。请检查 Engine 状态；若已停止，请先明确启动 Docker，再提交卸载。启动可能恢复原有容器，请先确认业务影响。",
};
export const softwareJobPhaseLabels: Record<string, string> = {
  SOFTWARE_RESTARTED: "软件已重启",
  SOFTWARE_RELOADED: "配置已重载",
  SOFTWARE_PAUSED: "服务已暂停",
  SOFTWARE_UNPAUSED: "服务已继续",
  SOFTWARE_OPERATION_COMPLETED: "操作已完成",
  MANAGEMENT_EXECUTING: "执行管理操作",
  MANAGEMENT_VERIFYING: "核对操作结果",
  MANAGEMENT_EXECUTE: "执行管理操作",
  MANAGEMENT_VERIFY: "核对操作结果",
  MANAGEMENT_COMPLETED: "管理操作已完成",
  MANAGEMENT_RESULT_UNCONFIRMED: "操作结果待核对",
  OPERATION_SUBMITTED: "任务已受理",
  GIT_REPOSITORY_BOUND: "仓库账号已绑定",
  GIT_REPOSITORY_UNBOUND: "仓库账号已解除",
  GIT_REPOSITORY_APPLY: "正在配置仓库账号",
  GIT_PREFLIGHT: "等待仓库配置检查",
  GIT_CONFIGURING: "正在配置仓库账号",
  DOCKER_ACCESS_UPDATED: "Docker 账号权限已更新",
  TRANSFER: "传输安装文件",
  IMPORT: "导入镜像",
  ACTIVATE: "启动服务",
  HEALTH: "检查服务健康",
  RECOVERY: "恢复原版本",
  DOCKER_MATERIAL: "准备 Docker 安装材料",
  DOCKER_PACKAGES_APPLY: "安装 Docker 软件包",
  OFFICIAL_REFRESH: "更新官方软件索引",
  OFFICIAL_PLAN_VERIFY: "复核官方软件包计划",
  INSTALL_RESUMING: "等待继续安装",
  INSTALL_TRANSFER_INTERRUPTED: "网络或材料传输中断",
  OFFICIAL_PACKAGES_APPLY: "安装官方软件包",
  SOFTWARE_PREFLIGHT: "检查软件安装条件",
  SERVICE_CONFIGURE: "写入服务配置",
  SERVICE_HEALTH: "检查服务健康",
  CONFIGURATION_STOP: "停止服务以应用配置",
  CONFIGURATION_CREATE: "应用服务配置",
  SERVICE_INSTALLED: "软件已安装",
  SERVICE_NOT_CREATED: "服务尚未创建",
  SERVICE_HEALTH_FAILED: "服务健康检查未通过",
  SERVICE_NOT_STARTED: "安装未开始",
  SERVICE_RECONCILE_REQUIRED: "安装结果待核对",
  NATIVE_RECONCILE_REQUIRED: "安装结果待核对",
  OFFICIAL_PLAN_CHANGED: "安装计划已变化",
  OFFICIAL_PREPARATION_INTERRUPTED: "安装准备已中断",
  OFFICIAL_REFRESH_FAILED: "软件源刷新失败",
  INSTALL_QUEUE_EXPIRED: "排队已超时",
  NODE_INSTALL: "安装 Node.js",
  NODE_CHANGE: "更新 Node 环境",
  NODE_ENVIRONMENT_UPDATED: "Node 环境已更新",
  SOFTWARE_UNINSTALL: "卸载软件",
  SOFTWARE_UNINSTALLED: "软件已卸载",
  SOFTWARE_UNINSTALLING: "正在卸载软件",
  PREFLIGHT: "等待执行",
  TARGET_EXECUTION: "目标执行中",
  SERVICE_IMAGE_PULL: "拉取固定镜像",
  SERVICE_IMAGE_LOAD: "导入离线镜像",
  SERVICE_IMAGE_VERIFY: "校验镜像身份",
  SERVICE_CREATE: "创建服务实例",
  SERVICE_INITIALIZING: "启动与初始化",
  SERVICE_READY: "服务检查通过",
  NATIVE_MATERIAL: "校验与准备原生材料",
  NATIVE_DOWNLOAD: "下载原生安装文件",
  NATIVE_VERIFY: "校验安装文件",
  COMMAND_INSTALL: "执行安装命令",
  COMMAND_VERIFY: "验证安装结果",
  MANAGEMENT_ROLLBACK: "恢复原配置",
  OPERATION_RESTORED: "配置未生效，已恢复原配置",
  MANAGEMENT_REBIND: "核对更新后的服务配置",
  COMMAND_UNINSTALL: "执行卸载命令",
  COMMAND_DELETE_DATA: "删除软件数据",
  NATIVE_FFMPEG_CONFIGURE: "检查 FFmpeg 构建配置",
  NATIVE_FFMPEG_BUILD: "编译 FFmpeg",
  NATIVE_PYTHON_CONFIGURE: "检查 Python 构建配置",
  NATIVE_PYTHON_BUILD: "编译 Python",
  NATIVE_PYTHON_INSTALL: "安装独立 Python 环境",
  NATIVE_REDIS_BUILD: "编译 Redis",
  NATIVE_NGINX_CONFIGURE: "检查 Nginx 构建配置",
  NATIVE_NGINX_BUILD: "编译 Nginx",
  NATIVE_ZLM_CONFIGURE: "检查 ZLM 构建配置",
  NATIVE_ZLM_BUILD: "编译 ZLM",
  NATIVE_START: "启动原生服务",
  NATIVE_NOT_STARTED: "原生安装未开始",
  SOFTWARE_PENDING: "等待软件执行",
  SOFTWARE_ADOPTED: "生命周期已接管",
  SOFTWARE_UNMANAGED: "已解除管理",
  SOFTWARE_STATE_VERIFIED: "软件状态已核对",
  SOFTWARE_RECONCILE_REQUIRED: "需要核对软件状态",
  CANCELLED: "已取消",
};

export function softwareJobStatusLabel(status?: string) {
  return softwareJobLabels[status || ""] || "状态待确认";
}

export function softwareJobPhaseLabel(phase?: string) {
  return softwareJobPhaseLabels[phase || ""] || "阶段待确认";
}

// Protocol codes remain available in diagnostics, not as normal page copy.
export function softwareObservationMessage(message?: string, errorCode?: string) {
  if (errorCode) return "检查未完成，请查看诊断信息";
  if (!message || ["READY", "SERVICE_INSTALLED", "SOFTWARE_STATE_VERIFIED"].includes(message)) return "";
  return /^[A-Z][A-Z0-9_]+$/.test(message) ? "检查结果待确认" : message;
}

export function softwareInstallationReady(item: SoftwareInstance) {
  return !item.installationStatus || item.installationStatus === "READY";
}

export function softwareUninstallAvailable(item: SoftwareInstance) {
  return item.managementMode === "MANAGED" && !!item.installation &&
    !["INSTALLING", "UNKNOWN", "UNINSTALLED"].includes(item.installationStatus || "");
}

export function softwareIsDockerEngine(item: SoftwareInstance) {
  return item.softwareType === "DOCKER" || item.installation?.definition?.key === "docker" ||
    (item.runtimeKind === "SYSTEMD" && item.locator === "docker.service");
}

export function softwareIsTool(item: SoftwareInstance) {
  return (
    item.runtimeKind === "TOOL" ||
    item.installation?.definition?.kind === "TOOL"
  );
}

export function softwareLifecycleAvailable(item: SoftwareInstance) {
  return (
    softwareInstallationReady(item) ||
    (item.installationStatus === "FAILED" &&
      item.managementMode === "MANAGED" &&
      (item.runtimeKind === "DOCKER"
        ? /^[a-f0-9]{64}$/.test(item.locator)
        : !!item.locator && !item.locator.startsWith("pending:")) &&
      /^[a-f0-9]{64}$/.test(item.observation?.identityHash || ""))
  );
}

export function softwareState(item: SoftwareInstance) {
  if (item.installationStatus === "UNINSTALLED" || item.actualState === "REMOVED")
    return { label: "已卸载", tone: "state-muted" };
  if (item.managementMode === "RELEASED")
    return { label: "已解除", tone: "state-muted" };
  if (!softwareInstallationReady(item)) {
    const labels = {
      INSTALLING: "安装中",
      FAILED: "安装失败",
      UNKNOWN: "安装待核对",
      CANCELLED: "安装已取消",
    };
    return {
      label:
        labels[item.installationStatus as keyof typeof labels] || "安装未确认",
      tone:
        item.installationStatus === "FAILED" ? "state-error" : "state-muted",
    };
  }
  if (
    item.latestAttempt?.code &&
    !["READY", "NOT_CHECKED"].includes(item.latestAttempt.code)
  )
    return item.observation?.observedAt
      ? {
          label: `上次${softwareActualLabels[item.observation.state] || "未知"}`,
          tone: "state-muted",
        }
      : { label: "待检查", tone: "state-muted" };
  if (item.freshness === "UNOBSERVED")
    return { label: "未检查", tone: "state-muted" };
  if (item.freshness === "STALE")
    return {
      label: `上次${softwareActualLabels[item.observation?.state || item.actualState] || "未知"}`,
      tone: "state-muted",
    };
  return {
    label: softwareActualLabels[item.actualState] || "未确认",
    tone: ["RUNNING", "INSTALLED"].includes(item.actualState)
      ? "state-success"
      : ["MISSING", "FAILED"].includes(item.actualState)
        ? "state-error"
        : "state-muted",
  };
}

export function softwareJobDiagnostic(status?: string, phase?: string, phases = softwareJobPhaseLabels) {
  const state = status
    ? softwareJobLabels[status] || "状态未识别"
    : "状态未提供";
  const stage = phase
    ? phases[phase] || "阶段未识别"
    : "阶段未提供";
  return `${state} · ${stage}`;
}
