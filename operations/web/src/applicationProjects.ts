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
  gitAccountId?: string;
  jenkinsCredentialId?: string;
  branch: string;
}

export interface ModuleEnvHint {
  name: string;
  defaultValue?: string;
  sensitive?: boolean;
  profile?: string;
}

export interface ModuleProxyHint {
  location: string;
  stripPrefix?: boolean;
  targetPort?: number;
}

export interface ModuleConfigTemplate {
  filename: string;
  profile?: string;
  format?: string;
  content: string;
}

export interface ModuleConfigFileMeta {
  filename: string;
  remotePath?: string;
  revision?: string;
  credentialNames?: string[];
  updatedAt?: string;
}

export interface ModuleConfigDraft {
  filename: string;
  content: string;
}

export interface ModuleVariable {
  name: string;
  value?: string;
  credentialId?: string;
  credentialRevision?: string;
}

export interface NginxProxyRule {
  location: string;
  targetModuleId?: string;
  upstreamUrl?: string;
  stripPrefix?: boolean;
  webSocket?: boolean;
  sse?: boolean;
}

export interface NginxDeployConfig {
  nginxSoftwareId?: string;
  siteName?: string;
  serverName?: string;
  listenPort?: number;
  spa?: boolean;
  staticDir?: string;
  proxyRules?: NginxProxyRule[];
}

export interface ModuleCatalog {
  kind: ModuleKind;
  description?: string;
  environment: string;
  dependencies?: string[];
  startCommand: string;
  runtimeMode: string;
  accessUrl?: string;
  checkCommand?: string;
  testCommand?: string;
  skipTests?: boolean;
  buildCommand?: string;
  artifactPath?: string;
  defaultPort?: number;
  healthPath?: string;
  profiles?: string[];
  defaultProfile?: string;
  envHints?: ModuleEnvHint[];
  proxyHints?: ModuleProxyHint[];
  configTemplates?: ModuleConfigTemplate[];
  supportsContainer?: boolean;
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
  managedInstanceId?: string;
  recordedVersion?: string;
  deployMode?: string;
  profile?: string;
  bindAddress?: string;
  servicePort?: number;
  memoryMiB?: number;
  startCommand?: string;
  healthPath?: string;
  ports?: { hostPort: number; containerPort: number; protocol?: string }[];
  variables?: ModuleVariable[];
  nginx?: NginxDeployConfig;
  dependencyIds?: string[];
  configDir?: string;
  configFiles?: ModuleConfigFileMeta[];
  configDrafts?: ModuleConfigDraft[];
  computedAccessUrl?: string;
}

export interface ProjectEnvironment {
  id: string;
  name: string;
  deployments: ProjectServiceRegistration[];
}

export interface ProjectInstanceSource {
  projectId: string;
  moduleId: string;
  serviceInstanceId?: string;
  algorithmHostEnvironmentId?: string;
}

export interface ProjectReleaseSource extends ProjectInstanceSource {
  managedInstanceId?: string;
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
  dependencies?: string[];
  buildCommand?: string;
  testCommand?: string;
  artifactPath?: string;
  recommendedRuntimeMode?: string;
  defaultProfile?: string;
  profiles?: string[];
  defaultPort?: number;
  healthPath?: string;
  envHints?: ModuleEnvHint[];
  proxyHints?: ModuleProxyHint[];
  configTemplates?: ModuleConfigTemplate[];
  supportsContainer?: boolean;
}

export interface InspectionSource {
  id: string;
  name: string;
}

export interface ProjectGitAccount {
  id: string;
  name: string;
  authMethod: string;
  sshHost: string;
  sshPort: number;
  keySource?: string;
}

export const deployModeLabels: Record<string, string> = {
  NATIVE: "原生 systemd",
  DOCKER: "Docker 容器",
  STATIC: "Nginx 静态站点",
};

export function deployModeText(mode?: string) {
  if (!mode) return "未指定";
  return deployModeLabels[mode] || mode;
}

export function computeDeploymentPreviewUrl(
  hostAddress?: string,
  cfg?: {
    deployMode?: string;
    servicePort?: number;
    hostPort?: number;
    nginx?: NginxDeployConfig;
  },
) {
  const format = (host: string, port: number) => {
    const clean = host.trim();
    if (!clean || port < 1 || port > 65535) return "";
    const wrapped = clean.includes(":") && !clean.startsWith("[") ? `[${clean}]` : clean;
    return port === 80 ? `http://${wrapped}` : `http://${wrapped}:${port}`;
  };
  if (cfg?.deployMode === "STATIC" && cfg.nginx) {
    const serverName = cfg.nginx.serverName?.trim() || "";
    const host = serverName && serverName !== "_" && !serverName.includes("*") ? serverName : hostAddress || "";
    const port = cfg.nginx.listenPort || cfg.servicePort || (host ? 80 : 0);
    return format(host, port);
  }
  const port = cfg?.servicePort || cfg?.hostPort || 0;
  return format(hostAddress || "", port);
}

const frameworkOnlyTokens = new Set(["vue", "vite", "react", "spring boot", "maven wrapper"]);

export interface DependencyCheckItem {
  name: string;
  searchTerm: string;
  hostRequired: boolean;
  satisfied: boolean;
  matches: SoftwareInstance[];
}

export function normalizeDependencySearchTerm(dep: string): string {
  const lower = dep.trim().toLowerCase();
  if (lower.startsWith("java") || lower.startsWith("jdk")) return "Java";
  if (lower.startsWith("node")) return "Node";
  if (lower.startsWith("python") || lower === "uv") return "Python";
  if (lower.startsWith("maven")) return "Maven";
  if (lower.startsWith("nginx")) return "Nginx";
  if (lower.startsWith("mysql")) return "MySQL";
  if (lower.startsWith("redis")) return "Redis";
  if (lower.startsWith("seaweed")) return "SeaweedFS";
  if (lower.startsWith("docker")) return "Docker";
  if (lower.startsWith("ffmpeg")) return "FFmpeg";
  if (lower.startsWith("zlmedia")) return "ZLMediaKit";
  if (lower.startsWith("mediamtx")) return "MediaMTX";
  return dep.trim();
}

export function softwareMatchesDependency(item: SoftwareInstance, dep: string): boolean {
  if (
    item.managementMode === "RELEASED" ||
    item.installationStatus === "UNINSTALLED" ||
    item.actualState === "REMOVED"
  ) {
    return false;
  }
  const token = normalizeDependencySearchTerm(dep).toLowerCase();
  const catalogKey = (item.installation?.definition?.catalogKey || item.installation?.definition?.key || "").toLowerCase();
  const sType = (item.softwareType || "").toLowerCase();
  const name = (item.name || "").toLowerCase();
  if (token === "java") return sType === "java" || catalogKey === "java";
  if (token === "node") return sType === "node" || ["node", "nvm", "npm", "pnpm"].includes(catalogKey);
  if (token === "python") return sType === "python" || catalogKey === "python";
  if (token === "maven") return sType === "maven" || catalogKey === "maven";
  if (token === "nginx") return sType === "nginx" || catalogKey === "nginx";
  if (token === "mysql") return sType === "mysql" || catalogKey === "mysql";
  if (token === "redis") return sType === "redis" || catalogKey === "redis";
  if (token === "seaweedfs") return sType === "seaweedfs" || catalogKey === "seaweedfs";
  if (token === "docker") return sType === "docker";
  if (token === "ffmpeg") return sType === "ffmpeg";
  if (token === "zlmediakit") return sType === "zlm" || catalogKey === "zlmediakit";
  if (token === "mediamtx") return sType === "mediamtx" || catalogKey === "mediamtx";
  return catalogKey.includes(token) || sType.includes(token) || name.includes(token);
}

export function evaluateDependencyReadiness(
  dependencies: string[] | undefined,
  hostId: string,
  software: SoftwareInstance[],
): DependencyCheckItem[] {
  const result: DependencyCheckItem[] = [];
  const seen = new Set<string>();
  for (const raw of dependencies || []) {
    const clean = raw.trim();
    const lower = clean.toLowerCase();
    if (!clean || frameworkOnlyTokens.has(lower) || ["pnpm", "npm", "yarn"].includes(lower)) continue;
    const term = normalizeDependencySearchTerm(clean);
    if (seen.has(term.toLowerCase())) continue;
    seen.add(term.toLowerCase());
    const hostRequired = ["java", "node", "python", "maven", "nginx", "docker"].includes(term.toLowerCase());
    const matches = software.filter(
      (item) => (!hostRequired || !hostId || item.hostId === hostId) && softwareMatchesDependency(item, term),
    );
    result.push({
      name: clean,
      searchTerm: term,
      hostRequired,
      satisfied: matches.length > 0,
      matches,
    });
  }
  return result;
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
    description: module?.catalog?.description ?? "",
    environment: module?.catalog?.environment ?? "",
    dependencies: [...(module?.catalog?.dependencies ?? [])],
    startCommand: module?.catalog?.startCommand ?? "",
    runtimeMode: module?.catalog?.runtimeMode ?? "",
    accessUrl: module?.catalog?.accessUrl ?? "",
    checkCommand: module?.catalog?.checkCommand ?? "",
    testCommand: module?.catalog?.testCommand ?? "",
    skipTests: module?.catalog?.skipTests ?? false,
    buildCommand: module?.catalog?.buildCommand ?? "",
    artifactPath: module?.catalog?.artifactPath ?? "",
    defaultPort: module?.catalog?.defaultPort ?? 0,
    healthPath: module?.catalog?.healthPath ?? "",
    profiles: [...(module?.catalog?.profiles ?? [])],
    defaultProfile: module?.catalog?.defaultProfile ?? "",
    envHints: [...(module?.catalog?.envHints ?? [])],
    proxyHints: [...(module?.catalog?.proxyHints ?? [])],
    configTemplates: (module?.catalog?.configTemplates ?? []).map(item => ({ ...item })),
    supportsContainer: module?.catalog?.supportsContainer ?? false,
  };
}

export function moduleEdit(draft: ModuleDraft, repositoryId = draft.repositoryId) {
  const description = draft.description?.trim();
  const dependencies = (draft.dependencies || []).map(item => item.trim()).filter(Boolean);
  const accessUrl = draft.accessUrl?.trim();
  const checkCommand = draft.checkCommand?.trim();
  const testCommand = draft.testCommand?.trim();
  const buildCommand = draft.buildCommand?.trim();
  const artifactPath = draft.artifactPath?.trim();
  const defaultPort = Number(draft.defaultPort) || 0;
  const healthPath = draft.healthPath?.trim();
  const profiles = (draft.profiles || []).map(item => item.trim()).filter(Boolean);
  const defaultProfile = draft.defaultProfile?.trim();
  const envHints = (draft.envHints || []).filter(item => item.name?.trim());
  const proxyHints = (draft.proxyHints || []).filter(item => item.location?.trim());
  const configTemplates = (draft.configTemplates || []).filter(item => item.filename?.trim());
  return {
    id: draft.id || undefined,
    name: draft.name.trim(),
    directory: moduleDirectory(draft.directory),
    repositoryId,
    catalog: {
      kind: draft.kind,
      ...(description ? { description } : {}),
      environment: draft.environment.trim(),
      ...(dependencies.length ? { dependencies } : {}),
      startCommand: draft.startCommand.trim(),
      runtimeMode: draft.runtimeMode.trim(),
      ...(accessUrl ? { accessUrl } : {}),
      ...(checkCommand ? { checkCommand } : {}),
      ...(testCommand ? { testCommand } : {}),
      ...(draft.skipTests ? { skipTests: true } : {}),
      ...(buildCommand ? { buildCommand } : {}),
      ...(artifactPath ? { artifactPath } : {}),
      ...(defaultPort > 0 ? { defaultPort } : {}),
      ...(healthPath ? { healthPath } : {}),
      ...(profiles.length ? { profiles } : {}),
      ...(defaultProfile ? { defaultProfile } : {}),
      ...(envHints.length ? { envHints } : {}),
      ...(proxyHints.length ? { proxyHints } : {}),
      ...(configTemplates.length ? { configTemplates } : {}),
      ...(draft.supportsContainer ? { supportsContainer: true } : {}),
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
