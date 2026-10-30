import type { ObservationAttempt } from "./observation";
import type {
  InstallationPaths,
  ServiceConfiguration,
  ServiceRecipe,
} from "./serviceInstallation";
export type SoftwareType =
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
  | "OTHER";
export type SoftwareRuntime = "SYSTEMD" | "DOCKER" | "TOOL";

export interface SoftwareMetadata {
  versionSource?: string;
  versionStatus?: string;
  packageName?: string;
  executablePath?: string;
  serviceFile?: string;
  listeningPorts?: string[];
}

export interface SoftwareInstance {
  id: string;
  name: string;
  hostId: string;
  hostName: string;
  softwareType: SoftwareType;
  softwareVersion: string;
  runtimeKind: SoftwareRuntime;
  locator: string;
  managementMode: "OBSERVE" | "MANAGED" | "RELEASED";
  desiredState: "UNCHANGED" | "RUNNING" | "STOPPED" | "INSTALLED" | "REMOVED";
  actualState:
    "UNKNOWN" | "RUNNING" | "STOPPED" | "INSTALLED" | "MISSING" | "FAILED" | "REMOVED";
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
  installationPaths?: InstallationPaths;
  installationEvidence?: {
    observedVersion: string;
    observedAt: string;
    health: string;
    containerId: string;
    binaryPath?: string;
  };
  installation?: {
    definition?: ServiceRecipe;
    config?: ServiceConfiguration;
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
  OTHER: "其他服务",
};
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
  if (metadata?.versionSource === "PACKAGE") return "安装包版本";
  if (metadata?.versionSource === "IMAGE_REFERENCE" || runtime === "DOCKER")
    return "镜像引用";
  return "程序版本";
}

export function softwareVersionReason(
  metadata?: SoftwareMetadata,
  runtime?: SoftwareRuntime,
) {
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
  REMOVED: "已卸载",
  UNCHANGED: "保持现状",
  INSTALLED: "已安装（按需使用）",
  RUNNING: "运行",
  STOPPED: "停止",
};
export const softwareActualLabels = {
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
  DOCKER_CONTAINER_DEPENDENCIES_UNCONFIRMED: "Docker Engine 当前不可访问，无法确认容器依赖。请检查 Engine 状态；若已停止，请先明确启动 Docker，再提交卸载。启动可能恢复原有容器，请先确认业务影响。",
};
export const softwareJobPhaseLabels: Record<string, string> = {
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
