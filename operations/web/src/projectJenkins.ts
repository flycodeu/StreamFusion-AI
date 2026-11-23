import { jenkinsLink } from "./applicationProjects";

export interface JenkinsMonitor {
  configured: boolean;
  reason?: string;
  version: string;
  softwareId: string;
  username: string;
  hasToken: boolean;
  jobUrl?: string;
  instances: { id: string; name: string; hostName: string; url: string }[];
}

export function moduleJenkinsPath(projectId: string, moduleId: string, instanceId?: string) {
  const scope = instanceId ? "service-instances/" + encodeURIComponent(instanceId) : "modules/" + encodeURIComponent(moduleId);
  return `/application-projects/${encodeURIComponent(projectId)}/${scope}`;
}

export interface JenkinsInstanceContext {
  moduleId: string;
  moduleName: string;
  serviceInstanceId?: string;
  environmentId?: string;
  environmentName?: string;
  readOnly?: boolean;
  action: "qa" | "logs";
}

export interface JenkinsBuild {
  configured: boolean;
  status: string;
  queueReason?: string;
  number?: number;
  building?: boolean;
  startedAt?: string;
  durationMs?: number;
  buildUrl?: string;
  consoleUrl?: string;
  currentStage?: string;
  stagesAvailable: boolean;
  stages?: { name: string; status: string; durationMs: number }[];
  artifacts?: { name: string; path: string; url: string }[];
  observedAt: string;
}

export interface JenkinsBuildLog {
  number: number;
  offset: number;
  nextOffset: number;
  text: string;
  more: boolean;
  truncated: boolean;
  consoleUrl?: string;
}

const states: Record<string, { label: string; tone: string }> = {
  NOT_CONFIGURED: { label: "待配置", tone: "state-muted" },
  NEVER_BUILT: { label: "尚无构建", tone: "state-muted" },
  QUEUED: { label: "排队中", tone: "state-warning" },
  RUNNING: { label: "构建中", tone: "state-running" },
  IN_PROGRESS: { label: "执行中", tone: "state-running" },
  PAUSED_PENDING_INPUT: { label: "等待 Jenkins 确认", tone: "state-warning" },
  SUCCESS: { label: "成功", tone: "state-success" },
  FAILURE: { label: "失败", tone: "state-error" },
  FAILED: { label: "失败", tone: "state-error" },
  UNSTABLE: { label: "不稳定", tone: "state-warning" },
  ABORTED: { label: "已取消", tone: "state-muted" },
  NOT_EXECUTED: { label: "未执行", tone: "state-muted" },
  NOT_BUILT: { label: "未构建", tone: "state-muted" },
};

export function jenkinsState(status?: string) {
  return states[status ?? ""] ?? { label: "待确认", tone: "state-muted" };
}

export function jenkinsBuildLink(jobUrl: string, value?: string) {
  const base = jenkinsLink(jobUrl), link = jenkinsLink(value);
  if (!base || !link) return "";
  const job = new URL(base), target = new URL(link);
  return target.origin === job.origin && target.pathname.startsWith(job.pathname.replace(/\/?$/, "/")) ? target.href : "";
}

export function jenkinsElapsed(ms?: number) {
  if (ms === undefined || !Number.isFinite(ms) || ms <= 0) return "";
  if (ms < 1000) return `${ms} 毫秒`;
  const seconds = Math.floor(ms / 1000);
  return seconds < 60 ? `${seconds} 秒` : `${Math.floor(seconds / 60)} 分 ${seconds % 60} 秒`;
}
