import type { SoftwareJob } from "./software";
import type { Host } from "./hosts";
import type { RuntimePolicy } from "./runtimePolicy";
import type { CredentialReference, ManagementDefinition, ParameterDefinition } from "./softwareManagement";

export interface ServiceRecipe {
  key: string;
  catalogKey?: string;
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
  defaultMemoryMiB?: number;
  defaultBindAddress?: string;
  web?: { scheme: "http" | "https"; portKey?: string; path?: string; embed?: boolean; urlParameter?: string };
  commands?: {
    builtin?: boolean;
    flowVersion?: number;
    revision?: number;
    kind: "TOOL" | "SERVICE" | "SYSTEMD" | "DOCKER";
    locator: string;
    binaryPath?: string;
    distributions?: string[];
    steps: {
      id?: string;
      kind?: "COMMAND" | "SYSTEM";
      action?: string;
      runtimeMode?: "NATIVE" | "DOCKER";
      sourceModes?: string[];
      phase: "INSTALL" | "VERIFY" | "UNINSTALL" | "DELETE_DATA";
      name: string;
      script: string;
      runAs: "ROOT" | "ACCOUNT";
      workingDirectory?: string;
      timeoutSeconds: number;
    }[];
  };
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
  parameters?: ParameterDefinition[];
  management?: ManagementDefinition;
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
export type InstallationMethod = "OFFICIAL" | "OFFLINE" | "SELF";

export function recipeCatalogKey(recipe: ServiceRecipe) {
  return recipe.catalogKey || recipe.key;
}

export function recipeCommandSteps(commands: ServiceRecipe["commands"], runtimeMode?: RuntimeMode, sourceMode?: string) {
  return commands?.steps.filter(step =>
    (!step.runtimeMode || step.runtimeMode === runtimeMode) &&
    (!step.sourceModes?.length || step.sourceModes.includes(sourceMode || "")),
  ) || [];
}

export function recipeSystemStepDetails(
  recipe: ServiceRecipe,
  step: NonNullable<ServiceRecipe["commands"]>["steps"][number],
  context: Partial<Pick<ServiceInstallPlan, "config" | "paths" | "official" | "source">> & { runtimeMode?: RuntimeMode; sourceMode?: string; account?: string; rootPath?: string } = {},
): { label: string; value: string; url?: boolean }[] {
  if (step.kind !== "SYSTEM") return [];
  const runtime = context.config?.runtimeMode || context.runtimeMode;
  const source = context.source?.type || context.sourceMode;
  const root = context.paths?.root || "${OPS_INSTALL_ROOT}";
  const config = context.paths?.config || `${root}/config`;
  const data = context.paths?.data || "${OPS_DATA_ROOT}";
  const details: { label: string; value: string; url?: boolean }[] = [];
  const add = (label: string, value: string, url = false) => details.push({ label, value, ...(url ? { url } : {}) });
  switch (step.action) {
    case "INSTALL_MATERIAL":
      add("处理", source === "ARCHIVE" ? "接收所选离线包，核对文件大小与 SHA-256" : "下载固定版本的材料，核对文件大小与 SHA-256");
      add("材料位置", runtime === "DOCKER" ? "${OPS_ARTIFACT_PATH}" : ["node", "nvm", "npm", "pnpm"].includes(recipe.key) ? "${OPS_NODE_MATERIAL}" : "本次任务的材料暂存目录");
      for (const part of recipe.native?.parts || []) {
        add(part.name, part.url, true);
        add("文件校验", `SHA-256: ${part.sha256} · ${(part.byteSize / 1024 / 1024).toFixed(1)} MiB`);
      }
      if (runtime === "DOCKER" && recipe.image) add("镜像", recipe.image + (recipe.digest ? `@${recipe.digest}` : ""));
      break;
    case "NATIVE_PREPARE":
      add("处理", recipe.key === "docker" ? "核验 Docker 软件包与离线依赖，准备安装目录和引擎配置" : "安全解包材料，创建安装目录及所需运行账号");
      add("安装目录", root);
      add("配置目录", config);
      add("数据目录", data);
      if (recipe.key === "docker") add("引擎配置", "/etc/docker/daemon.json");
      break;
    case "NATIVE_CONFIGURE":
      add("处理", recipe.key === "docker" ? "核验 Docker 软件包、程序与服务文件，记录安装身份" : recipe.kind === "TOOL" ? "核验安装后的程序文件，保存文件身份" : "核验程序文件，生成服务配置和 systemd 单元");
      add("程序路径", recipe.key === "docker" ? "/usr/bin/dockerd" : "${OPS_BINARY}");
      if (recipe.kind !== "TOOL") { add("配置目录", config); add("服务单元", recipe.key === "docker" ? "/usr/lib/systemd/system/docker.service" : "${OPS_UNIT_PATH}"); }
      break;
    case "DOCKER_PREPARE":
      add("处理", "核对固定镜像，生成配置文件、数据挂载与容器参数");
      add("镜像", recipe.image + (recipe.digest ? `@${recipe.digest}` : ""));
      if (recipe.imageId) add("镜像标识", recipe.imageId);
      add("配置目录", config);
      add("数据目录", data);
      if (context.config?.hostPort) add("监听地址", `${context.config.bindAddress}:${context.config.hostPort}`);
      break;
    case "OFFICIAL_PLAN":
      add("处理", "复核软件包名称、版本、来源和依赖，冻结本次安装清单");
      if (context.official?.packages.length) {
        for (const pkg of context.official.packages) add(pkg.name, `${pkg.version} · ${pkg.origin}`);
      } else add("软件包", "安装检查后生成当前系统可用的软件包清单");
      if (recipe.officialUrl) add("来源", recipe.officialUrl, true);
      break;
    case "OFFICIAL_OWNERSHIP":
      add("处理", "记录本次安装的软件包版本及文件身份，用于后续检查和卸载");
      if (context.official) add("程序路径", context.official.paths.binary);
      break;
    case "NODE_PREPARE":
      add("处理", recipe.key === "node" ? "核验 Node 包元数据，保存所选账号的环境快照" : "保存所选账号的环境快照");
      add("系统账号", context.config?.account || context.account || "${OPS_ACCOUNT}");
      add("环境目录", context.rootPath || "${OPS_NODE_ROOT}");
      break;
    case "NODE_TERMINAL":
      add("处理", "按安装选项为所选账号配置终端自动加载");
      add("系统账号", context.config?.account || context.account || "${OPS_ACCOUNT}");
      break;
    case "INSTALL_CONFIRM":
      add("处理", "核对实际版本、文件身份和运行状态，保存安装结果");
      add("软件版本", `${recipe.name} ${recipe.version}`);
      break;
    case "UNINSTALL_PREPARE":
      add("处理", "核对程序归属、依赖与使用状态，确认卸载范围");
      if (context.official || recipe.native?.buildRecipe?.startsWith("SYSTEM_")) {
        add("软件包", "核对本次安装记录中的软件包清单");
        for (const path of context.official?.paths.ownedPaths || []) add("专属目录", path);
      } else add("安装目录", root);
      break;
    case "UNINSTALL_CONFIRM":
      add("处理", "核对程序移除结果，记录保留或删除的数据目录");
      if (context.official) {
        if (context.official.paths.data && context.official.paths.data !== "/") add("数据目录", context.official.paths.data);
      } else if (data !== "/") add("数据目录", data);
      break;
    default:
      add("处理", step.name);
  }
  return details;
}

export function recipeChoiceId(recipe: ServiceRecipe) {
  return JSON.stringify([recipe.key, recipe.version, recipe.revision]);
}

export function installationNeedsComponentUpdate(code: string): boolean {
  return ["EXECUTOR_UPGRADE_REQUIRED", "NODE_CAPABILITY_REQUIRED", "NATIVE_INSTALL_UPGRADE_REQUIRED",
    "SERVICE_RECIPE_UNTRUSTED", "SERVICE_INSTALL_INPUT_INVALID"].includes(code);
}

export function defaultInstallationRuntime(recipes: ServiceRecipe[], host?: Host): RuntimeMode | undefined {
  const native = recipes.some((item) => item.runtimeModes.includes("NATIVE"));
  if (!recipes.some((item) => item.runtimeModes.includes("DOCKER"))) return native ? "NATIVE" : undefined;
  if (native && recipes.some((item) => item.kind === "TOOL" || item.key === "docker")) return "NATIVE";
  const facts = host?.capabilities;
  if (!facts || Date.parse(facts.expiresAt) <= Date.now() || !Number.isFinite(Date.parse(facts.expiresAt))) return undefined;
  if (facts.docker.code === "DOCKER_NOT_INSTALLED") return native ? "NATIVE" : "DOCKER";
  return facts.docker.status === "AVAILABLE" || facts.docker.code === "DOCKER_ENGINE_UNAVAILABLE" ? "DOCKER" : undefined;
}

export interface OfficialInstallation {
  manager: string;
  os: string;
  architecture: string;
  packages: { name: string; version: string; origin: string; direct: boolean }[];
  commands: { executable: string; args: string[] }[];
  paths: { root: string; binary: string; config: string; data: string; logs: string; unit: string; ownedPaths: string[] };
}

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
  runtimePolicy?: RuntimePolicy;
  parameters?: Record<string, string>;
  parameterCredentials?: Record<string, CredentialReference>;
  name: string;
  account?: string;
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
    verification: "PENDING" | "RECIPE_SHA256" | "PINNED_IMAGE" | "UPLOAD_SHA256";
    importedAt?: string;
    fileName?: string;
  };
  sources?: MaterialSource[];
}

export interface InstallationSource {
  type: "REGISTRY" | "ONLINE" | "ARCHIVE" | "OFFICIAL";
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
  official?: OfficialInstallation;
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
  git: "配置 GitHub 或 GitLab 账号，用于拉取和推送。",
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
  jenkins: "持续集成服务，支持独立构建与自动化任务。",
  postgresql: "关系数据库，支持独立数据目录与初始账号。",
  gitlab: "代码托管与协作平台。",
  prometheus: "时序指标采集与监控。",
  nacos: "服务发现与配置管理。",
  "label-studio": "图片、文本、音视频等数据标注。",
  ytlabel: "图像与视频标注，支持 AI 辅助和 YOLO 数据集导出。",
};

export const runtimeLabels: Record<RuntimeMode, string> = {
  DOCKER: "Docker 容器",
  NATIVE: "原生安装",
};

export function isTool(recipe: ServiceRecipe) {
  return recipe.commands ? recipe.commands.kind === "TOOL" : recipe.kind === "TOOL";
}

export function requiresServicePort(recipe: ServiceRecipe) {
  return !isTool(recipe) && !isDockerEngine(recipe) && (!recipe.commands || recipe.port > 0);
}

export function isDockerEngine(recipe: ServiceRecipe) {
  return recipe.key === "docker";
}

export function initialOfficialConfiguration(recipe: ServiceRecipe): ServiceConfiguration {
  const service = !isTool(recipe) && !isDockerEngine(recipe);
  return { name: recipe.name, runtimeMode: "NATIVE", storageRoot: "", ports: {},
    bindAddress: service ? recipe.defaultBindAddress || (recipe.web || recipe.key === "nginx" ? "0.0.0.0" : "127.0.0.1") : "", hostPort: service ? recipe.port : 0, memoryMiB: 512,
    credentialId: "", credentialRevision: "", accessKeyCredentialId: "", accessKeyCredentialRevision: "",
    database: "", username: "", userCredentialId: "", userCredentialRevision: "" };
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

export function serviceBindAddressValid(recipe: ServiceRecipe, address: string) {
  return explicitIPv4(address) || !!recipe.web && address === "0.0.0.0";
}

export function initialServiceConfiguration(
  recipe: ServiceRecipe,
  address: string,
  selectedRuntime?: RuntimeMode,
): ServiceConfiguration {
  const runtimeMode = selectedRuntime || (
    recipe.runtimeModes.includes("NATIVE") && isTool(recipe)
      ? "NATIVE"
      : recipe.runtimeModes[0] || "DOCKER"
  ) as RuntimeMode;
  return {
    name: recipe.key === "java" && runtimeMode === "NATIVE" ? `Java ${recipe.version}` : recipe.name,
    ...(recipe.parameters?.length ? { parameters: Object.fromEntries(recipe.parameters.filter(field => field.type !== "CREDENTIAL").map(field => [field.key, field.default || ""])), parameterCredentials: {} } : {}),
    runtimeMode,
    storageRoot: "",
    ports: Object.fromEntries(
      visibleServicePorts(recipe, runtimeMode).map((port) => [
        port.key,
        port.required ? port.defaultPort : 0,
      ]),
    ),
    bindAddress: recipe.defaultBindAddress || (recipe.web ? "0.0.0.0" : explicitIPv4(address) ? address : "127.0.0.1"),
    hostPort: isTool(recipe) || isDockerEngine(recipe) ? 0 : recipe.port,
    memoryMiB: recipe.defaultMemoryMiB || (recipe.key === "mysql" ? 1024 : recipe.key === "nginx" ? 256 : 512),
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
  if (!requiresServicePort(recipe)) return "无监听端口";
  const address = `${config.bindAddress}:${config.hostPort}`;
  if (recipe.key === "mediamtx") return `rtsp://${address}`;
  return recipe.key === "nginx" ||
    recipe.key === "seaweedfs" ||
    recipe.key === "zlm"
    ? `http://${address}`
    : address;
}
