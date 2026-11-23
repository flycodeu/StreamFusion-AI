import type { SoftwareInstance } from "./software";

export const moduleKinds = {
  FRONTEND: "前端",
  BACKEND: "后端",
  ALGORITHM: "算法",
  OTHER: "其他",
} as const;

export type ModuleKind = keyof typeof moduleKinds;

export interface ProjectRepository {
  id: string;
  name: string;
  url: string;
  branch: string;
}

export interface ModuleCatalog {
  kind: ModuleKind;
  environment: string;
  startCommand: string;
  runtimeMode: string;
}

export interface ProjectModule {
  id: string;
  name: string;
  repositoryId: string;
  directory: string;
  catalog?: ModuleCatalog;
}

export interface ProjectServiceRegistration {
  id: string;
  moduleId: string;
  hostId: string;
  runtimeSoftwareId?: string;
  recordedVersion?: string;
}

export interface ProjectEnvironment {
  id: string;
  name: string;
  deployments: ProjectServiceRegistration[];
}

export interface ProjectInstanceSource {
  projectId: string;
  moduleId: string;
}

export interface ProjectReleaseSource extends ProjectInstanceSource {
  serviceInstanceId: string;
  environmentId: string;
  environmentName: string;
  moduleName: string;
  hostId: string;
}

export function isProjectBuildService(moduleName: string, software: SoftwareInstance) {
  return moduleName.trim().toLowerCase() !== "jenkins" &&
    (software.softwareType === "JENKINS" || software.installation?.definition?.catalogKey === "jenkins");
}

export interface ApplicationProject {
  id: string;
  code: string;
  name: string;
  description: string;
  jenkinsUrl?: string;
  version: string;
  archived: boolean;
  updatedAt: string;
  repositories: ProjectRepository[];
  modules: ProjectModule[];
  environments?: ProjectEnvironment[];
}

export interface ApplicationProjectPage {
  items: ApplicationProject[];
  total: number;
}

export interface ModuleDraft extends ModuleCatalog {
  id: string;
  name: string;
  directory: string;
  repositoryId: string;
  repositoryUrl: string;
}

export interface InspectedModule {
  directory: string;
  name: string;
  kind: ModuleKind;
  environment: string;
  startCommand: string;
  evidence: string[];
}

export interface InspectionSource {
  id: string;
  name: string;
}

export function projectListPath(page: number, pageSize: number, query: string, archived: string) {
  const params = new URLSearchParams({ page: String(page), pageSize: String(pageSize), archived });
  if (query.trim()) params.set("q", query.trim());
  return `/application-projects?${params}`;
}

export function jenkinsLink(value?: string) {
  if (!value || !/^https?:\/\//.test(value) || /[\s\\?#]/.test(value)) return "";
  try {
    const url = new URL(value);
    if (["http:", "https:"].includes(url.protocol) && !url.username && !url.password && !url.search && !url.hash) return url.href;
  } catch { /* Await a complete URL. */ }
  return "";
}

export function newModuleDraft(repositoryId = "", module?: ProjectModule): ModuleDraft {
  return {
    id: module?.id ?? "",
    name: module?.name ?? "",
    directory: module?.directory ?? ".",
    repositoryId: module?.repositoryId ?? repositoryId,
    repositoryUrl: "",
    kind: module?.catalog?.kind ?? "OTHER",
    environment: module?.catalog?.environment ?? "",
    startCommand: module?.catalog?.startCommand ?? "",
    runtimeMode: module?.catalog?.runtimeMode ?? "",
  };
}

export function moduleEdit(draft: ModuleDraft, repositoryId = draft.repositoryId) {
  return {
    id: draft.id || undefined,
    name: draft.name.trim(),
    directory: moduleDirectory(draft.directory),
    repositoryId,
    catalog: {
      kind: draft.kind,
      environment: draft.environment.trim(),
      startCommand: draft.startCommand.trim(),
      runtimeMode: draft.runtimeMode.trim(),
    },
  };
}

export function moduleDirectory(value: string) {
  const directory = value.trim();
  if (directory.startsWith("/")) return directory;
  return directory.split("/").filter(part => part && part !== ".").join("/") || ".";
}

export function repositoryId() {
  const bytes = crypto.getRandomValues(new Uint8Array(16));
  bytes[6] = bytes[6] & 0x0f | 0x40;
  bytes[8] = bytes[8] & 0x3f | 0x80;
  const hex = Array.from(bytes, value => value.toString(16).padStart(2, "0")).join("");
  return hex.replace(/^(........)(....)(....)(....)(............)$/, "$1-$2-$3-$4-$5");
}
