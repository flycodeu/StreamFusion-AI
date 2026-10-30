import type { SoftwareJob } from "./software";

export interface ServiceRecipe {
  key: string;
  category?: string;
  name: string;
  kind: "SERVICE" | "TOOL";
  version: string;
  revision: string;
  platform: string;
  image: string;
  digest: string;
  imageId: string;
  officialUrl: string;
  publisher?: string;
  sources?: MaterialSource[];
  runtimeModes: string[];
  sourceModes: string[];
  port: number;
  native?: {
    parts: {
      name: string;
      url: string;
      sha256: string;
      byteSize: number;
      format: string;
      stripComponents: number;
    }[];
    buildRecipe?: string;
    requiredTools?: string[];
  };
  ports?: ServicePort[];
}

export interface MaterialSource {
  name: string;
  displayName?: string;
  version?: string;
  publisher?: string;
  url: string;
  sha256: string;
  byteSize: number;
  role: "MAIN" | "DEPENDENCY";
  packaging?: "OS_PACKAGE" | "ISOLATED_EXTRACTION" | "BINARY_ARCHIVE" | "DOCKER_SAVE";
  requirements?: string[];
}

export type RuntimeMode = "DOCKER" | "NATIVE";

export interface ServicePort {
  key: string;
  label: string;
  protocol: string;
  defaultPort: number;
  containerPort: number;
  required: boolean;
  nativeOnly?: boolean;
}

export interface ServiceConfiguration {
  name: string;
  runtimeMode: RuntimeMode;
  storageRoot: string;
  ports: Record<string, number>;
  bindAddress: string;
  hostPort: number;
  memoryMiB: number;
  credentialId: string;
  credentialRevision: string;
  accessKeyCredentialId: string;
  accessKeyCredentialRevision: string;
  database: string;
  username: string;
  userCredentialId: string;
  userCredentialRevision: string;
}

export interface InstallationMaterial {
  id: string;
  name: string;
  recipeKey: string;
  recipeVersion: string;
  recipeRevision: string;
  runtimeMode: RuntimeMode;
  status: string;
  available: boolean;
  sha256: string;
  byteSize: number;
  imageId: string;
  platform: string;
  errorCode: string;
  createdAt: string;
  updatedAt: string;
  recipeName?: string;
  origin?: {
    kind: "BUNDLED" | "UPLOADED" | "OFFICIAL_DOWNLOAD" | "UNKNOWN";
    verification: "PENDING" | "RECIPE_SHA256" | "PINNED_IMAGE";
    importedAt?: string;
    fileName?: string;
  };
  sources?: MaterialSource[];
}

export interface InstallationSource {
  type: "REGISTRY" | "ONLINE" | "ARCHIVE";
  materialId?: string;
  sha256?: string;
  byteSize?: number;
}

export interface ServiceInstallPlan {
  id: string;
  targetId: string;
  definition: ServiceRecipe;
  config: ServiceConfiguration;
  source: InstallationSource;
  expiresAt: string;
  checks: { key: string; status: "PASSED" | "WARNING"; message: string }[];
  paths: InstallationPaths;
  portMappings: {
    key?: string;
    address: string;
    hostPort: number;
    containerPort: number;
    protocol: string;
  }[];
}

export interface InstallationPaths {
  root: string;
  bin?: string;
  data: string;
  config: string;
  logs?: string;
}

export interface ServiceConfigurationPlan extends ServiceInstallPlan {
  previousConfig: ServiceConfiguration;
}

export interface ServiceInstallReceipt {
  jobId: string;
  softwareInstanceId: string;
}

export interface ServiceInstallJob extends SoftwareJob {
  result?: unknown;
}

export const serviceDescriptions: Record<string, string> = {
  docker: "主机容器运行引擎；安装后可运行 Docker 容器。",
  mysql: "关系数据库，支持独立数据目录与初始业务账号。",
  redis: "内存数据服务，使用独立认证与持久化配置。",
  nginx: "Web 服务与反向代理，独立站点配置和访问端口。",
  seaweedfs: "单机 S3 对象存储，持久保存文件与元数据。",
  mediamtx: "多协议媒体服务，支持 RTSP、RTMP 与 WebRTC。",
  zlm: "ZLMediaKit 流媒体服务，提供媒体接入与 API。",
  java: "Java 应用运行环境，安装后校验程序路径与版本。",
  maven: "Java 项目构建工具，提供独立 Maven 与 JDK 环境。",
  python: "Python 3.12 独立环境，验证 SSL、SQLite 与 venv。",
  ffmpeg: "媒体处理工具，包含 ffprobe 与 H.264 软件编码。",
  codex: "OpenAI 编程命令行，使用官方独立程序。",
  claude: "Anthropic 编程命令行，使用官方独立程序。",
};

export const runtimeLabels: Record<RuntimeMode, string> = {
  DOCKER: "Docker 容器",
  NATIVE: "原生安装",
};

export function isTool(recipe: ServiceRecipe) {
  return recipe.kind === "TOOL";
}

export function isDockerEngine(recipe: ServiceRecipe) {
  return recipe.key === "docker";
}

export function preferredInstallationMaterial(items: InstallationMaterial[]) {
  return items.find((item) => item.available && item.origin?.kind === "BUNDLED") ||
    items.find((item) => item.available);
}

export function materialOriginLabel(material: InstallationMaterial) {
  return material.origin?.kind === "BUNDLED" ? "随包离线材料" :
    material.origin?.kind === "OFFICIAL_DOWNLOAD" ? "在线获取后入库" :
    material.origin?.kind === "UPLOADED" ? "上传入库" : "历史材料（来源未记录）";
}

export function materialChoiceLabel(material: InstallationMaterial) {
  return `${material.recipeName || material.name} ${material.recipeVersion} · ${materialOriginLabel(material)} · ${materialSize(material.byteSize)} · ${material.available ? "已就绪" : materialStatusLabels[material.status] || "未就绪"}`;
}

export function visibleServicePorts(
  recipe: ServiceRecipe,
  runtime: RuntimeMode,
) {
  return (recipe.ports || []).filter(
    (port) => port.key !== "main" && (!port.nativeOnly || runtime === "NATIVE"),
  );
}

export function servicePortValue(
  recipe: ServiceRecipe,
  config: ServiceConfiguration,
  key: string,
) {
  if (key === "main") return config.hostPort;
  const descriptor = recipe.ports?.find((port) => port.key === key);
  return (
    config.ports?.[key] ?? (descriptor?.required ? descriptor.defaultPort : 0)
  );
}

export function servicePortAddress(
  recipe: ServiceRecipe,
  config: ServiceConfiguration,
  key: string,
) {
  const descriptor = recipe.ports?.find((port) => port.key === key);
  return (descriptor?.nativeOnly && key !== "s3Grpc") || key === "api"
    ? "127.0.0.1"
    : config.bindAddress;
}

export function storageDirectoryValid(value: string) {
  return (
    !value ||
    (/^\/(opt|srv|var\/lib|mnt|data)\/[^\s\\\x00]+$/.test(value) &&
      !value
        .slice(1)
        .split("/")
        .some((part) => part === "." || part === ".." || !part) &&
      value.length <= 240)
  );
}

export const materialStatusLabels: Record<string, string> = {
  WAITING_UPLOAD: "等待上传",
  UPLOADING: "上传中",
  UPLOADED: "待校验",
  AVAILABLE: "可用",
  FAILED: "校验失败",
  VERIFYING: "校验中",
  DOWNLOADING: "下载中",
  MISSING: "文件缺失",
};

export function materialNextAction(material: InstallationMaterial) {
  switch (material.status) {
    case "WAITING_UPLOAD":
    case "FAILED":
      return "UPLOAD";
    case "UPLOADED":
      return "CONFIRM";
    case "UPLOADING":
    case "VERIFYING":
    case "DOWNLOADING":
      return "WAIT";
    case "AVAILABLE":
      return material.available ? "READY" : "REIMPORT";
    case "MISSING":
      return "REIMPORT";
    default:
      return "REJECT";
  }
}

export function materialSize(bytes: number) {
  if (bytes >= 1024 ** 3) return `${(bytes / 1024 ** 3).toFixed(2)} GiB`;
  if (bytes >= 1024 ** 2) return `${(bytes / 1024 ** 2).toFixed(1)} MiB`;
  return `${Math.max(0, bytes)} B`;
}

export function explicitIPv4(address: string) {
  const parts = address.split(".");
  return (
    parts.length === 4 &&
    parts.every(
      (part) => /^(0|[1-9]\d{0,2})$/.test(part) && Number(part) <= 255,
    ) &&
    address !== "0.0.0.0"
  );
}

export function initialServiceConfiguration(
  recipe: ServiceRecipe,
  address: string,
): ServiceConfiguration {
  const runtimeMode = (
    recipe.runtimeModes.includes("NATIVE") && isTool(recipe)
      ? "NATIVE"
      : recipe.runtimeModes[0] || "DOCKER"
  ) as RuntimeMode;
  return {
    name: recipe.name,
    runtimeMode,
    storageRoot: "",
    ports: Object.fromEntries(
      visibleServicePorts(recipe, runtimeMode).map((port) => [
        port.key,
        port.required ? port.defaultPort : 0,
      ]),
    ),
    bindAddress: explicitIPv4(address) ? address : "127.0.0.1",
    hostPort: isTool(recipe) || isDockerEngine(recipe) ? 0 : recipe.port,
    memoryMiB:
      recipe.key === "mysql" ? 1024 : recipe.key === "nginx" ? 256 : 512,
    credentialId: "",
    credentialRevision: "",
    accessKeyCredentialId: "",
    accessKeyCredentialRevision: "",
    database: "",
    username: recipe.key === "mediamtx" ? "ops" : "",
    userCredentialId: "",
    userCredentialRevision: "",
  };
}

export function serviceEndpoint(
  recipe: ServiceRecipe,
  config: ServiceConfiguration,
) {
  if (isDockerEngine(recipe)) return "主机 Docker Engine · 本地 Unix socket";
  if (isTool(recipe))
    return config.runtimeMode === "NATIVE"
      ? "程序工具 · 无监听端口"
      : "工具镜像 · 按需执行";
  const address = `${config.bindAddress}:${config.hostPort}`;
  if (recipe.key === "mediamtx") return `rtsp://${address}`;
  return recipe.key === "nginx" ||
    recipe.key === "seaweedfs" ||
    recipe.key === "zlm"
    ? `http://${address}`
    : address;
}
