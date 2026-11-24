import type { JenkinsBuild } from "./projectJenkins";

export interface DeploymentJob {
  jobId: string;
  status: string;
  phase?: string;
  errorCode?: string;
  observedAt?: string;
  buildNumber?: number;
  commit?: string;
  releaseId?: string;
  action?: string;
  health?: DeploymentHealth;
  traceId?: string;
}

type DeploymentHealth = "READY" | "UNHEALTHY" | "NOT_DEPLOYED" | "UNKNOWN";

export interface DeploymentVersion {
  releaseId: string;
  buildNumber: number;
  commit: string;
  observedAt?: string;
}

export interface DeploymentStatus {
  kind?: "JAVA" | "STATIC";
  latest?: DeploymentJob;
  current?: DeploymentVersion;
  previous?: DeploymentVersion;
  health?: DeploymentHealth;
}

export interface DeploymentPlan {
  token: string;
  kind: "JAVA" | "STATIC";
  buildNumber: number;
  commit: string;
  hostId: string;
  hostName: string;
  environment: string;
  moduleName: string;
  summary: string[];
}

export function projectDeploymentPath(projectId: string, deploymentId: string) {
  return `/application-projects/${encodeURIComponent(projectId)}/deployments/${encodeURIComponent(deploymentId)}`;
}

export function jobPending(job?: DeploymentJob) {
  return !!job && ["QUEUED", "RUNNING", "RECOVERING"].includes(job.status);
}

export function isPublishableBuild(build?: JenkinsBuild) {
  return !!build?.configured && Number.isSafeInteger(build.number) &&
    (build.number ?? 0) > 0 && build.status === "SUCCESS" &&
    !build.building && !!build.artifacts?.length;
}

export function jobStatusText(status?: string) {
  return ({
    QUEUED: "排队中",
    RUNNING: "执行中",
    RECOVERING: "恢复中",
    SUCCEEDED: "成功",
    FAILED: "失败",
    UNKNOWN: "结果待核对",
    CANCELLED: "已取消",
  } as Record<string, string>)[status ?? ""] || status || "尚无作业";
}

export function deploymentHealthText(health?: DeploymentHealth) {
  return ({
    READY: "服务正常",
    UNHEALTHY: "服务异常",
    NOT_DEPLOYED: "尚未发布",
    UNKNOWN: "待检查",
  } as const)[health ?? "UNKNOWN"];
}

export function deploymentActionText(action?: string) {
  return ({
    DEPLOY: "发布",
    PUBLISH: "发布",
    VERIFY: "检查服务",
    ROLLBACK: "回退",
  } as Record<string, string>)[action ?? ""] || "部署作业";
}

export const deploymentPhaseLabels: Record<string, string> = {
  QUEUED: "等待执行",
  ARTIFACT_DOWNLOAD: "下载制品",
  TARGET_EXECUTION: "在目标服务器执行",
  OBSERVING: "检查服务",
  RECONCILING: "核对服务器状态",
  BASELINE_RETAINED: "原版本仍在运行",
  VERIFIED: "检查完成",
};

export function deploymentPhaseText(phase?: string) {
  return deploymentPhaseLabels[phase ?? ""] || phase || "";
}

export function projectDeploymentErrorText(code?: string) {
  return ({
    PROJECT_ROLLBACK_RELEASE_MISSING: "目标服务器找不到上一版本，无法回退。请先检查服务和版本记录。",
    PROJECT_STAGED_RELEASE_INVALID: "目标服务器的版本文件不完整或校验失败。请查看执行日志并检查服务。",
    PROJECT_ARTIFACT_INVALID: "制品大小或格式不符合部署要求。请核对 Jenkins 归档的文件。",
    PROJECT_RECOVERY_REQUIRED: "服务器状态仍需核对。请检查服务后再继续。",
    PROJECT_NGINX_CONF_D_REQUIRED: "目标服务器的 Nginx 未加载站点配置目录。请检查 nginx.conf 是否包含 /etc/nginx/conf.d/*.conf。",
    PROJECT_NGINX_SERVICE_REQUIRED: "目标服务器的 Nginx 未运行。请在主机的软件管理中确认安装和服务状态，启动后重试。",
    PROJECT_NGINX_EXISTING_CONFIG_INVALID: "目标服务器现有 Nginx 配置未通过检查。请在服务器上执行 nginx -t，修复提示的配置后重试。",
    PROJECT_NGINX_CHECK_FAILED: "发布时的 Nginx 配置检查失败。请查看执行日志，核对站点域名、监听端口和代理配置。",
    PROJECT_NGINX_RELOAD_FAILED: "Nginx 未能加载本次发布配置。请查看目标服务器的 nginx.service 日志，修复后检查服务。",
    PROJECT_JAVA_21_REQUIRED: "目标服务器未找到可用的 Java 21。请安装 Java 21，并确认 /usr/bin/java 指向该版本。",
    PROJECT_JAVA_START_FAILED: "应用未能启动。请查看目标服务器的 systemd 服务日志，核对应用配置和监听端口。",
    PROJECT_HEALTH_CHECK_FAILED: "服务未在规定时间内通过检查。请查看目标服务器的服务日志，核对端口和健康检查地址。",
    PROJECT_DEPENDENCY_UNREACHABLE: "目标服务器连不上登记的依赖。请从目标服务器确认依赖的地址、端口和防火墙设置。",
    PROJECT_SYSTEMD_REQUIRED: "目标服务器无法使用 systemd。请检查 systemctl 是否可用，或选择支持 systemd 的 Linux 主机。",
    PROJECT_SITE_OR_UNIT_OWNERSHIP_MISMATCH: "服务器上的站点配置或服务单元与 Ops 记录不一致。请先核对现有配置及归属，再处理冲突。",
  } as Record<string, string>)[code ?? ""] || "";
}

export function deploymentTime(value?: string) {
  if (!value) return "";
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return value;
  return date.toLocaleString("zh-CN", {
    timeZone: "Asia/Shanghai", year: "numeric", month: "2-digit", day: "2-digit",
    hour: "2-digit", minute: "2-digit", second: "2-digit",
  });
}
