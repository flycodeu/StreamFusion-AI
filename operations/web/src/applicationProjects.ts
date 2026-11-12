import type { RuntimePolicy } from "./runtimePolicy";

export interface ProjectRepository {
  id: string;
  name: string;
  url: string;
  gitAccountId: string;
  branch: string;
}

export interface ProjectStep {
  name: string;
  command: string;
  workdir: string;
  timeoutSeconds: number;
  privilege: "ACCOUNT" | "ROOT";
}

export interface ProjectModule {
  id: string;
  name: string;
  repositoryId: string;
  directory: string;
  build: {
    kind: "DOCKERFILE" | "COMMAND" | "STATIC";
    hostId: string;
    account: string;
    softwareId: string;
    context: string;
    dockerfile: string;
    outputPath: string;
    toolchain: string;
    steps: ProjectStep[];
  };
  runtime: {
    kind: "DOCKER" | "NATIVE" | "STATIC";
    command: string;
    health: { kind: "HTTP" | "TCP" | "COMMAND" | "NONE"; path: string; port: number; command: string; timeoutSeconds: number };
  };
}

export interface ProjectVariable {
  name: string;
  value: string;
  credentialId: string;
  credentialRevision: string;
}

export interface ProjectDeployment {
  runtimePolicy?: RuntimePolicy;
  id: string;
  moduleId: string;
  hostId: string;
  runtimeSoftwareId: string;
  bindAddress: string;
  memoryMiB: number;
  ports: { hostPort: number; containerPort: number; protocol: "TCP" | "UDP" }[];
  mounts: { source: string; target: string; readOnly: boolean }[];
  variables: ProjectVariable[];
  logs: { kind: "FILE" | "DOCKER" | "JOURNAL"; directory: string; pattern: string; retentionDays: number; maxMiB: number };
}

export interface ProjectEnvironment {
  id: string;
  name: string;
  deployments: ProjectDeployment[];
}

export interface ApplicationProject {
  id: string;
  code: string;
  name: string;
  description: string;
  version: string;
  archived: boolean;
  createdAt: string;
  updatedAt: string;
  repositories: ProjectRepository[];
  modules: ProjectModule[];
  environments: ProjectEnvironment[];
}

export interface ApplicationProjectPage {
  items: ApplicationProject[];
  total: number;
  page: number;
  pageSize: number;
}

export interface ProjectCredential {
  id: string;
  name: string;
  kind: string;
  revision: string;
  revokedAt: string;
}

export function latestProjectCredentials(items: ProjectCredential[]): ProjectCredential[] {
  const selected = new Map<string, ProjectCredential>();
  for (const item of items) {
    if (item.revokedAt || !["PASSWORD", "TOKEN"].includes(item.kind) || !/^[1-9][0-9]*$/.test(item.revision)) continue;
    const previous = selected.get(item.id);
    if (!previous || BigInt(item.revision) > BigInt(previous.revision)) selected.set(item.id, item);
  }
  return [...selected.values()];
}

export function newApplicationProject(): ApplicationProject {
  return { id: "", code: "", name: "", description: "", version: "", archived: false, createdAt: "", updatedAt: "", repositories: [], modules: [], environments: [] };
}

export function newProjectRepository(): ProjectRepository {
  return { id: crypto.randomUUID(), name: "", url: "", gitAccountId: "", branch: "" };
}

export function newProjectModule(repositoryId = ""): ProjectModule {
  return {
    id: crypto.randomUUID(), name: "", repositoryId, directory: ".",
    build: { kind: "DOCKERFILE", hostId: "", account: "", softwareId: "", context: ".", dockerfile: "Dockerfile", outputPath: "", toolchain: "", steps: [] },
    runtime: { kind: "DOCKER", command: "", health: { kind: "NONE", path: "", port: 0, command: "", timeoutSeconds: 30 } },
  };
}

export function newProjectEnvironment(): ProjectEnvironment {
  return { id: crypto.randomUUID(), name: "", deployments: [] };
}

export function newProjectDeployment(moduleId: string, kind: ProjectModule["runtime"]["kind"] = "DOCKER"): ProjectDeployment {
  return {
    id: crypto.randomUUID(), moduleId, hostId: "", runtimeSoftwareId: "", bindAddress: "", memoryMiB: 0, ports: [], mounts: [], variables: [],
    logs: { kind: kind === "DOCKER" ? "DOCKER" : kind === "STATIC" ? "FILE" : "JOURNAL", directory: "", pattern: "", retentionDays: 0, maxMiB: 0 },
  };
}

// A detached draft keeps edits and failed saves from mutating the list snapshot.
export function copyApplicationProject(project: ApplicationProject): ApplicationProject {
  return JSON.parse(JSON.stringify(project));
}

export function projectListPath(page: number, pageSize: number, query = "", archived = "false") {
  const params = new URLSearchParams({ page: String(page), pageSize: String(pageSize), archived });
  if (query.trim()) params.set("q", query.trim());
  return `/application-projects?${params}`;
}

export function projectRepositoryUsage(project: ApplicationProject, repositoryId: string): string[] {
  return project.modules.filter(module => module.repositoryId === repositoryId).map(module => module.name || "未命名模块");
}

export function projectModuleUsage(project: ApplicationProject, moduleId: string): string[] {
  return project.environments.filter(environment => environment.deployments.some(deployment => deployment.moduleId === moduleId)).map(environment => environment.name || "未命名环境");
}

export function projectVariableMode(variable: ProjectVariable, mode: "VALUE" | "CREDENTIAL") {
  if (mode === "VALUE") { variable.credentialId = ""; variable.credentialRevision = ""; }
  else variable.value = "";
}

export type ProjectSection = "basics" | "repositories" | "modules" | "environments";

export function projectValidationError(project: ApplicationProject, section?: ProjectSection): { section: ProjectSection; message: string; itemId?: string } | undefined {
  if (!section || section === "basics") {
    if (!project.name.trim()) return { section: "basics", message: "请填写项目名称" };
    if (!project.code.trim()) return { section: "basics", message: "请填写项目代码" };
    if (Array.from(project.code.trim()).length > 60) return { section: "basics", message: "项目代码最多60个字符" };
  }
  for (const [itemSection, label, items] of [["repositories", "仓库", project.repositories], ["modules", "模块", project.modules], ["environments", "环境", project.environments]] as const) {
    if (section && section !== itemSection) continue;
    const names = new Set<string>();
    for (const item of items) {
      const name = item.name.trim().toLocaleLowerCase();
      if (!name) return { section: itemSection, message: `请填写${label}名称`, itemId: item.id };
      if (names.has(name)) return { section: itemSection, message: `${label}名称重复，请调整后保存`, itemId: item.id };
      names.add(name);
    }
  }
}
