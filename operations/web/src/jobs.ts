import type { SoftwareJob } from "./software";

export interface PageResult<T> { items: T[]; nextCursor: string }
export interface JobSummary extends SoftwareJob {
  kind: string;
  targetId: string;
  instanceId?: string;
  softwareInstanceId?: string;
  subjectName?: string;
  hostName?: string;
  operationKey?: string;
  createdAt: string;
}

const kindLabels: Record<string, string> = {
  SOFTWARE_INSTALL: "软件安装", SOFTWARE_CONFIGURE: "软件配置", SOFTWARE_UNINSTALL: "软件卸载",
  SOFTWARE_START: "启动服务", SOFTWARE_STOP: "停止服务", SOFTWARE_RESTART: "重启服务",
  SOFTWARE_RELOAD: "重载配置", SOFTWARE_PAUSE: "暂停服务", SOFTWARE_UNPAUSE: "继续服务",
  SOFTWARE_ADOPT: "接管软件", SOFTWARE_UNMANAGE: "解除管理", SOFTWARE_OPERATION: "软件操作",
  SOFTWARE_DOCKER_ACCESS: "Docker 账号权限", NODE_ENVIRONMENT_CHANGE: "Node.js 环境配置",
  GIT_REPOSITORY_CHANGE: "Git 仓库操作", GIT_ACCOUNT_CHANGE: "Git 账号配置",
  PLUGIN_INSTALL: "工具安装", PLUGIN_REMOVE: "工具卸载",
};
export function jobKindLabel(kind: string) { return kindLabels[kind] || "软件操作"; }

export interface LogSelection { jobId?: string; softwareId?: string; title?: string }
