<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref, watch } from "vue";
import {
  ElAlert,
  ElButton,
  ElCollapse,
  ElCollapseItem,
  ElDialog,
  ElDropdown,
  ElDropdownItem,
  ElDropdownMenu,
  ElForm,
  ElFormItem,
  ElInput,
  ElInputNumber,
  ElMessage,
  ElOption,
  ElRadioButton,
  ElRadioGroup,
  ElSelect,
  ElSwitch,
  ElTag,
} from "element-plus";
import { ApiError, request } from "./api";
import type {
  ApplicationProject,
  ModuleConfigDraft,
  ModuleConfigTemplate,
  ModuleVariable,
  ProjectEnvironment,
  ProjectServiceRegistration,
} from "./applicationProjects";
import {
  evaluateDependencyReadiness,
  isProjectBuildService,
} from "./applicationProjects";
import type { Host, SSHCredential } from "./hosts";
import { requestAllPages } from "./listPages";
import type { SoftwareInstance } from "./software";
import {
  buildEnvironmentPatch,
  type EnvironmentFormVariable as EditableVariable,
  type EnvironmentFormProxyRule as EditableProxyRule,
} from "./features/project-delivery/environmentPatch";

interface EditableConfigFile {
  filename: string;
  content: string;
  baseline: string;
  existsOnServer: boolean;
  loading?: boolean;
  saved?: boolean;
}

const props = defineProps<{
  project: ApplicationProject;
  moduleId: string;
  environment?: ProjectEnvironment;
  registration?: ProjectServiceRegistration;
  addingAlgorithmHost?: boolean;
  softwareList?: SoftwareInstance[];
  visible?: boolean;
}>();
const emit = defineEmits<{
  close: [];
  "install-software": [payload: { search: string; hostId?: string }];
  saved: [ApplicationProject];
  error: [unknown];
}>();

const lifetime = new AbortController();
const projectVersion = props.project.version;
const module = computed(() =>
  props.project.modules.find((item) => item.id === props.moduleId),
);
const algorithm = computed(() => module.value?.catalog?.kind === "ALGORITHM");
const existingEnvironments =
  props.project.environments?.filter((item) =>
    item.deployments.some((row) => row.moduleId === props.moduleId),
  ) || [];
const defaultEnvName =
  props.environment?.name ||
  (existingEnvironments.length
    ? ""
    : module.value?.catalog?.defaultProfile || "test");
const environmentName = ref(defaultEnvName);
const hostId = ref(props.registration?.hostId || "");
const originalTarget = props.registration?.managedInstanceId || "";
const managedId = ref(originalTarget);
const removing = ref(false);
const originalRuntime = props.registration?.runtimeSoftwareId || "";
const runtimeId = ref(originalRuntime);

const inferredNewDeployMode = (() => {
  if (props.registration?.deployMode) return props.registration.deployMode;
  if (props.registration) return "";
  const cat = module.value?.catalog;
  if (cat?.kind !== "ALGORITHM" && (cat?.runtimeMode === "STATIC" || cat?.kind === "FRONTEND")) return "STATIC";
  if (cat?.runtimeMode === "DOCKER") return "DOCKER";
  return "NATIVE";
})();
const deployMode = ref(inferredNewDeployMode);
const canUseStaticMode = computed(() => !algorithm.value || props.registration?.deployMode === "STATIC");
const nativeJava = computed(() => deployMode.value === "NATIVE" && module.value?.catalog?.kind === "BACKEND");
const profile = ref(
  props.registration?.profile ||
    (!props.registration
      ? module.value?.catalog?.defaultProfile || defaultEnvName || ""
      : ""),
);
const bindAddress = ref(
  props.registration?.bindAddress || (!props.registration ? "0.0.0.0" : ""),
);
const servicePort = ref(
  props.registration?.servicePort ||
    (!props.registration && inferredNewDeployMode !== "STATIC"
      ? module.value?.catalog?.defaultPort || (algorithm.value ? 0 : 8080)
      : 0),
);
const containerPort = ref(
  props.registration?.ports?.[0]?.containerPort ||
    module.value?.catalog?.defaultPort ||
    8080,
);
const memoryMiB = ref(props.registration?.memoryMiB || 0);
const startCommand = ref(
  props.registration?.startCommand ||
    (!props.registration && inferredNewDeployMode !== "STATIC"
      ? module.value?.catalog?.startCommand || ""
      : ""),
);
const healthPath = ref(
  props.registration?.healthPath ||
    (!props.registration ? module.value?.catalog?.healthPath || "" : ""),
);
const dependencyIds = ref<string[]>([
  ...(props.registration?.dependencyIds || []),
]);

function toEditableVars(vars?: ModuleVariable[]): EditableVariable[] {
  return (vars || []).map((v) => ({
    name: v.name,
    mode: v.credentialId ? "CREDENTIAL" : "PLAIN",
    value: v.value || "",
    credentialRef: v.credentialId
      ? `${v.credentialId}:${v.credentialRevision || "1"}`
      : "",
  }));
}
function defaultConfigFilenameFor(envName: string, prof: string, mode: string) {
  const kind = module.value?.catalog?.kind || "";
  if (mode === "STATIC" || kind === "FRONTEND") {
    return `.env.${(envName || "production").trim()}`;
  }
  if (kind === "BACKEND") {
    const p = (prof || envName || "test").trim();
    return p ? `application-${p}.yml` : "application.yml";
  }
  return "config.yaml";
}

const pulledTemplates = ref<ModuleConfigTemplate[]>(
  (module.value?.catalog?.configTemplates || []).filter((t) => validConfigFilename(t.filename)),
);
const pullingTemplates = ref(false);
const loadingRemoteConfigs = ref(false);
const addingConfigFile = ref(false);
const newFilenameInput = ref("");

function buildInitialConfigFiles(): EditableConfigFile[] {
  const map = new Map<string, EditableConfigFile>();
  const existing = props.registration?.configFiles || [];
  for (const meta of existing) {
    if (!validConfigFilename(meta.filename)) continue;
    map.set(meta.filename, {
      filename: meta.filename,
      content: "",
      baseline: "",
      existsOnServer: true,
    });
  }
  for (const file of props.registration?.configDrafts || []) {
    const old = map.get(file.filename);
    map.set(file.filename, {
      filename: file.filename,
      content: file.content,
      baseline: file.content,
      existsOnServer: old?.existsOnServer || false,
      saved: true,
    });
  }
  if (!map.size) {
    const defName = defaultConfigFilenameFor(
      environmentName.value || "test",
      profile.value,
      deployMode.value,
    );
    map.set(defName, {
      filename: defName,
      content: "",
      baseline: "",
      existsOnServer: false,
    });
  }
  return Array.from(map.values());
}

const configFiles = ref<EditableConfigFile[]>(buildInitialConfigFiles());
const activeConfigFilename = ref(configFiles.value[0]?.filename || "");
const activeConfigFile = computed(() =>
  configFiles.value.find((f) => f.filename === activeConfigFilename.value) ||
  configFiles.value[0],
);

const variables = ref<EditableVariable[]>(
  toEditableVars(props.registration?.variables),
);

function sanitizeSiteName(raw: string) {
  return raw
    .toLowerCase()
    .replace(/[^a-z0-9._-]+/g, "-")
    .replace(/^-+|-+$/g, "")
    .slice(0, 60);
}
const streamFusionBackend = computed(() => {
  const web = module.value;
  const path = web?.directory?.replace(/\/+$/, "") || "";
  if (!web || !/(?:^|\/)platform-web$/.test(path)) return undefined;
  const apiPath = path.replace(/platform-web$/, "platform-api");
  return props.project.modules.find(
    (candidate) => candidate.repositoryId === web.repositoryId && candidate.directory === apiPath,
  );
});
function platformApiRule(moduleId: string): EditableProxyRule {
  return { location: "/api", targetType: "MODULE", targetModuleId: moduleId, upstreamUrl: "",
    stripPrefix: true, webSocket: false, sse: false };
}
const initialProxyRules = (): EditableProxyRule[] => {
  const existing = props.registration?.nginx?.proxyRules;
  if (existing?.length) {
    return existing.map((r) => ({
      location: r.location || "/api",
      targetType: r.targetModuleId ? "MODULE" : "URL",
      targetModuleId: r.targetModuleId || "",
      upstreamUrl: r.upstreamUrl || "",
      stripPrefix: !!r.stripPrefix,
      webSocket: !!r.webSocket,
      sse: !!r.sse,
    }));
  }
  if (!props.registration && streamFusionBackend.value) return [platformApiRule(streamFusionBackend.value.id)];
  if (!props.registration && module.value?.catalog?.proxyHints?.length) {
    const firstBackend = props.project.modules.find(
      (m) => m.id !== props.moduleId && m.catalog?.kind === "BACKEND",
    );
    if (firstBackend) {
      return module.value.catalog.proxyHints.map((hint) => ({
        location: hint.location || "/api",
        targetType: "MODULE",
        targetModuleId: firstBackend.id,
        upstreamUrl: "",
        stripPrefix: !!hint.stripPrefix,
        webSocket: false,
        sse: false,
      }));
    }
  }
  return [];
};
const nginxSoftwareId = ref(
  props.registration?.nginx?.nginxSoftwareId || "",
);
const nginxSiteName = ref(
  props.registration?.nginx?.siteName ||
    (!props.registration
      ? sanitizeSiteName(`${module.value?.name || "web"}-${defaultEnvName || "test"}`)
      : ""),
);
const nginxServerName = ref(
  props.registration?.nginx?.serverName || (!props.registration && !streamFusionBackend.value ? "_" : ""),
);
const nginxListenPort = ref(
  props.registration?.nginx?.listenPort || (!props.registration ? 80 : 80),
);
const nginxSpa = ref(props.registration?.nginx?.spa ?? true);
const nginxProxyRules = ref<EditableProxyRule[]>(initialProxyRules());
function setStreamFusionRoutes() {
  if (!streamFusionBackend.value) return;
  const conflicts = new Set([
    "api", "assets", "index.html", "api-docs.html", "api-docs.css", "api-docs.js", "streamfusion.svg",
    "login", "home", "profile", "change-password", "forbidden", "unavailable",
    "system", "monitor", "camera", "preview", "storage",
  ]);
  nginxSpa.value = true;
  nginxProxyRules.value = [
    platformApiRule(streamFusionBackend.value.id),
    ...nginxProxyRules.value.filter((rule) => !conflicts.has(rule.location.trim().split("/")[1] ?? "")),
  ];
}

interface ManagedTarget {
  id: string;
  name: string;
  targetId: string;
  config: { environment: string; hostPort: number };
}
const managedTargets = ref<ManagedTarget[]>([]);
const loadingTargets = ref(true),
  targetError = ref("");
const targetOptions = computed(() =>
  managedTargets.value.filter(
    (item) =>
      item.targetId === hostId.value &&
      item.config.environment.toLowerCase() ===
        environmentName.value.trim().toLowerCase(),
  ),
);
const hosts = ref<Host[]>([]);
const availableHosts = computed(() => {
  if (!algorithm.value) return hosts.value;
  const environment = props.project.environments?.find((item) =>
    item.name.toLowerCase() === environmentName.value.trim().toLowerCase(),
  );
  const occupied = new Set(environment?.deployments.filter((row) =>
    row.id !== props.registration?.id && row.moduleId === props.moduleId,
  ).map((row) => row.hostId));
  return hosts.value.filter((host) => !occupied.has(host.id));
});
const software = ref<SoftwareInstance[]>(props.softwareList || []);
const credentials = ref<SSHCredential[]>([]);
const credentialsLoaded = ref(false);
const loadingHosts = ref(true),
  loadingSoftware = ref(!props.softwareList?.length),
  saving = ref(false);
const hostError = ref(""),
  softwareError = ref(""),
  formError = ref("");

const activeSoftware = computed(() =>
  software.value.filter(
    (item) =>
      item.managementMode !== "RELEASED" &&
      item.installationStatus !== "UNINSTALLED" &&
      item.actualState !== "REMOVED",
  ),
);
const runtimeOptions = computed(() =>
  activeSoftware.value.filter(
    (item) =>
      item.hostId === hostId.value &&
      ["SYSTEMD", "DOCKER"].includes(item.runtimeKind) &&
      !isProjectBuildService(module.value?.name || "", item) &&
      !!item.observation?.identityHash,
  ),
);
function isNginxInstance(item: SoftwareInstance) {
  const key = (
    item.installation?.definition?.catalogKey ||
    item.installation?.definition?.key ||
    ""
  ).toLowerCase();
  return item.softwareType === "NGINX" || key === "nginx";
}
function inferInstancePort(item: SoftwareInstance, fallback = 0): number {
  const cfgPort = Number(
    (item.installation?.config as { port?: number } | undefined)?.port || 0,
  );
  if (cfgPort > 0 && cfgPort <= 65535) return cfgPort;
  const ports = item.observation?.listeningPorts || [];
  for (const p of ports) {
    const m = String(p).match(/:(\d+)$/) || String(p).match(/^(\d+)$/);
    if (m) {
      const n = Number(m[1]);
      if (n >= 1 && n <= 65535) return n;
    }
  }
  const mappings = item.observation?.portMappings || [];
  for (const mapping of mappings) {
    const m = String(mapping).match(/:(\d+)\s*->/);
    if (m) {
      const n = Number(m[1]);
      if (n >= 1 && n <= 65535) return n;
    }
  }
  return fallback;
}
const nginxOptions = computed(() =>
  activeSoftware.value.filter(
    (item) => item.hostId === hostId.value && isNginxInstance(item) && item.runtimeKind === "SYSTEMD",
  ),
);
const middlewareOptions = computed(() =>
  activeSoftware.value.filter((item) =>
    ["MYSQL", "REDIS", "SEAWEEDFS"].includes(item.softwareType) ||
    ["mysql", "redis", "seaweedfs"].includes(
      (item.installation?.definition?.catalogKey || "").toLowerCase(),
    ),
  ),
);
const activeSecretCredentials = computed(() =>
  credentials.value.filter(
    (c) => !c.revokedAt && (c.kind === "PASSWORD" || c.kind === "TOKEN"),
  ),
);
const dependencyChecks = computed(() => {
  if (typeof evaluateDependencyReadiness !== "function") return [];
  return evaluateDependencyReadiness(
    module.value?.catalog?.dependencies,
    hostId.value,
    activeSoftware.value,
  );
});
const missingDependencies = computed(() =>
  dependencyChecks.value.filter((d) => !d.satisfied),
);
const hostMap = computed(
  () => new Map(hosts.value.map((h) => [h.id, h])),
);
const backendModuleChoices = computed(() =>
  props.project.modules.filter((m) => m.id !== props.moduleId),
);
function resolveBackendModuleEndpoint(targetModuleId: string) {
  const envs = props.project.environments || [];
  const sameEnv = envs.find(
    (e) =>
      e.name.toLowerCase() === environmentName.value.trim().toLowerCase(),
  );
  const dep =
    sameEnv?.deployments.find((d) => d.moduleId === targetModuleId);
  if (!dep) return "";
  const host = hostMap.value.get(dep.hostId);
  const addr = host?.address || "";
  const port = dep.servicePort || dep.ports?.[0]?.hostPort || 0;
  if (!addr || !port) return "";
  return `http://${addr}:${port}`;
}
const previewAccessUrl = computed(() => {
  if (algorithm.value) return "";
  const addr = hostMap.value.get(hostId.value)?.address || "";
  if (!addr && (!nginxServerName.value || nginxServerName.value === "_")) return "";
  const wrap = (h: string) => (h.includes(":") && !h.startsWith("[") ? `[${h}]` : h);
  if (deployMode.value === "STATIC") {
    const serverName = nginxServerName.value.trim();
    const host =
      serverName && serverName !== "_" && !serverName.includes("*")
        ? serverName
        : addr;
    const port = Number(nginxListenPort.value) || 80;
    if (!host) return "";
    return port === 80 ? `http://${wrap(host)}` : `http://${wrap(host)}:${port}`;
  }
  const port = Number(servicePort.value) || 0;
  if (!addr || port < 1 || port > 65535) return "";
  return port === 80 ? `http://${wrap(addr)}` : `http://${wrap(addr)}:${port}`;
});

const originalBuildService = computed(() =>
  software.value.some(
    (item) =>
      item.id === originalRuntime &&
      isProjectBuildService(module.value?.name || "", item),
  ),
);
const sensitivePattern = /(password|passwd|secret|token|api_?key|private_?key|credential)/i;
function isSensitiveVarName(name: string) {
  return sensitivePattern.test(name.trim());
}
const hasUnsatisfiedPlainSensitiveVar = computed(() =>
  variables.value.some(
    (v) =>
      v.name.trim() &&
      ((v.mode === "PLAIN" && v.value && isSensitiveVarName(v.name)) ||
        (v.mode === "CREDENTIAL" && !v.credentialRef)),
  ),
);
const profileSuggestions = computed(() => {
  const set = new Set<string>(["dev", "test", "prod"]);
  for (const p of module.value?.catalog?.profiles || []) {
    if (p) set.add(p);
  }
  return Array.from(set);
});

watch(hostId, () => {
  runtimeId.value = "";
  managedId.value = "";
  if (deployMode.value === "STATIC") {
    const first = nginxOptions.value[0];
    nginxSoftwareId.value = first?.id || "";
    if (first) {
      const p = inferInstancePort(first, 80);
      if (p) nginxListenPort.value = p;
    }
  }
});
watch([environmentName, profile], () => {
  if (
    configFiles.value.length === 1 &&
    !configFiles.value[0].existsOnServer &&
    !configFiles.value[0].content &&
    !pulledTemplates.value.length
  ) {
    const nextName = defaultConfigFilenameFor(
      environmentName.value || "test",
      profile.value,
      deployMode.value,
    );
    configFiles.value[0].filename = nextName;
    activeConfigFilename.value = nextName;
  }
});
watch(environmentName, () => {
  managedId.value = "";
  if (!props.registration && !nginxSiteName.value) {
    nginxSiteName.value = sanitizeSiteName(
      `${module.value?.name || "web"}-${environmentName.value || "test"}`,
    );
  }
});
watch(() => props.softwareList, (items) => { if (items?.length) software.value = items; });
watch(
  nginxOptions,
  (list) => {
    if (
      !props.registration &&
      deployMode.value === "STATIC" &&
      !nginxSoftwareId.value &&
      list.length
    ) {
      nginxSoftwareId.value = list[0].id;
      const p = inferInstancePort(list[0], 80);
      if (p) nginxListenPort.value = p;
    }
  },
  { immediate: true },
);

async function ensureCredentialsLoaded() {
  if (credentialsLoaded.value) return;
  try {
    const items = await request<SSHCredential[]>(
      "/credentials",
      "GET",
      undefined,
      lifetime.signal,
    );
    if (!lifetime.signal.aborted && Array.isArray(items)) {
      credentials.value = items;
      credentialsLoaded.value = true;
    }
  } catch {
    /* Non-blocking when credentials fail or are absent in minimal harness. */
  }
}

function addVariableRow() {
  void ensureCredentialsLoaded();
  variables.value.push({
    name: "",
    mode: "PLAIN",
    value: "",
    credentialRef: "",
  });
}
function importRecognizedHints() {
  void ensureCredentialsLoaded();
  const existing = new Set(variables.value.map((v) => v.name.trim()));
  const hints = module.value?.catalog?.envHints || [];
  let added = 0;
  for (const hint of hints) {
    if (!hint.name || existing.has(hint.name)) continue;
    existing.add(hint.name);
    variables.value.push({
      name: hint.name,
      mode: hint.sensitive ? "CREDENTIAL" : "PLAIN",
      value: hint.sensitive ? "" : hint.defaultValue || "",
      credentialRef: "",
      profile: hint.profile,
    });
    added++;
  }
  if (added > 0) ElMessage.success(`已导入 ${added} 项识别的环境变量`);
}
function fillFromMiddleware(item: SoftwareInstance) {
  void ensureCredentialsLoaded();
  if (!dependencyIds.value.includes(item.id)) {
    dependencyIds.value.push(item.id);
  }
  const hostAddr = hostMap.value.get(item.hostId)?.address || "127.0.0.1";
  const key = (
    item.installation?.definition?.catalogKey ||
    item.softwareType ||
    ""
  ).toLowerCase();
  const upsertVar = (entry: EditableVariable) => {
    const idx = variables.value.findIndex((v) => v.name === entry.name);
    if (idx >= 0) {
      if (!variables.value[idx].value && !variables.value[idx].credentialRef) {
        variables.value[idx] = entry;
      }
    } else {
      variables.value.push(entry);
    }
  };
  if (key === "mysql") {
    const port = inferInstancePort(item, 3306);
    if (module.value?.catalog?.kind === "BACKEND") {
      upsertVar({
        name: "DB_URL",
        mode: "PLAIN",
        value: `jdbc:mysql://${hostAddr}:${port}/streamfusion?connectionTimeZone=Asia/Shanghai&connectTimeout=3000&socketTimeout=5000`,
        credentialRef: "",
      });
    } else {
      upsertVar({ name: "MYSQL_HOST", mode: "PLAIN", value: hostAddr, credentialRef: "" });
      upsertVar({ name: "MYSQL_PORT", mode: "PLAIN", value: String(port), credentialRef: "" });
    }
    upsertVar({ name: "DB_USERNAME", mode: "PLAIN", value: "root", credentialRef: "" });
    upsertVar({ name: "DB_PASSWORD", mode: "CREDENTIAL", value: "", credentialRef: "" });
    ElMessage.success(`已填入 ${item.name} 连接模板，请为 DB_PASSWORD 选择凭据`);
  } else if (key === "redis") {
    const port = inferInstancePort(item, 6379);
    upsertVar({ name: "REDIS_HOST", mode: "PLAIN", value: hostAddr, credentialRef: "" });
    upsertVar({ name: "REDIS_PORT", mode: "PLAIN", value: String(port), credentialRef: "" });
    upsertVar({ name: "REDIS_PASSWORD", mode: "CREDENTIAL", value: "", credentialRef: "" });
    ElMessage.success(`已填入 ${item.name} Redis 参数，请确认凭据`);
  } else if (key === "seaweedfs") {
    const port = inferInstancePort(item, 8333);
    upsertVar({ name: "S3_ENDPOINT", mode: "PLAIN", value: `http://${hostAddr}:${port}`, credentialRef: "" });
    upsertVar({ name: "S3_ACCESS_KEY", mode: "CREDENTIAL", value: "", credentialRef: "" });
    upsertVar({ name: "S3_SECRET_KEY", mode: "CREDENTIAL", value: "", credentialRef: "" });
    ElMessage.success(`已填入 ${item.name} S3 地址，请选择 Access/Secret 凭据`);
  }
}

function ensureActiveConfigFile(): EditableConfigFile {
  let file = activeConfigFile.value;
  if (!file) {
    const name = defaultConfigFilenameFor(
      environmentName.value || "test",
      profile.value,
      deployMode.value,
    );
    file = {
      filename: name,
      content: "",
      baseline: "",
      existsOnServer: false,
    };
    configFiles.value.push(file);
    activeConfigFilename.value = name;
  }
  return file;
}

function appendToActiveConfig(snippet: string) {
  const file = ensureActiveConfigFile();
  const base = file.content.replace(/\s+$/, "");
  file.content = base ? `${base}\n\n${snippet.trimStart()}` : snippet.trimStart();
}

function applyTemplateToEditor(tpl: ModuleConfigTemplate) {
  if (!validConfigFilename(tpl.filename)) {
    formError.value = "配置文件只支持 .yml、.yaml 和 .properties。";
    return;
  }
  let file = configFiles.value.find((item) => item.filename === tpl.filename);
  if (!file) {
    file = {
      filename: tpl.filename,
      content: tpl.content,
      baseline: "",
      existsOnServer: false,
    };
    configFiles.value.push(file);
  } else {
    file.content = tpl.content;
  }
  activeConfigFilename.value = file.filename;
  ElMessage.success(`已加载仓库模板 ${tpl.filename}`);
}

async function pullRepoConfigTemplates(applyDefault = true) {
  if (pullingTemplates.value || !module.value) return;
  pullingTemplates.value = true;
  formError.value = "";
  try {
    const res = await request<{
      templates?: ModuleConfigTemplate[];
      profiles?: string[];
      defaultProfile?: string;
      defaultPort?: number;
    }>(
      `/application-projects/${props.project.id}/modules/${props.moduleId}/config-templates`,
      "POST",
      {},
      lifetime.signal,
    );
    if (lifetime.signal.aborted) return;
    const tpls = (res.templates || []).filter((tpl) => validConfigFilename(tpl.filename));
    pulledTemplates.value = tpls;
    if (!tpls.length) {
      ElMessage.info?.("仓库中没有 YAML 或 properties 配置模板，可以手动填写。");
      return;
    }
    if (applyDefault) {
      const targetProf = (profile.value || environmentName.value || "").trim().toLowerCase();
      const matched =
        tpls.find((t) => (t.profile || "").toLowerCase() === targetProf) ||
        tpls.find((t) => !t.profile) || (tpls.length === 1 ? tpls[0] : undefined);
      if (!matched) ElMessage.info?.("没有匹配当前环境的模板，请手动选用。");
      else if (configFiles.value.some((f) => f.filename === matched.filename && f.content.trim())) {
        ElMessage.info?.("已获取仓库模板。当前草稿已保留，可手动选用模板。");
      } else applyTemplateToEditor(matched);
    }
  } catch (error) {
    if (!lifetime.signal.aborted) {
      formError.value = describe(error, "从仓库拉取配置模板失败");
    }
  } finally {
    pullingTemplates.value = false;
  }
}

async function readRemoteConfigFile(file: EditableConfigFile) {
  if (!hostId.value || !environmentName.value.trim()) return;
  file.loading = true;
  const targetHost = hostId.value;
  const targetEnvironment = environmentName.value.trim();
  try {
    const qs = new URLSearchParams({
      hostId: targetHost,
      environment: targetEnvironment,
    });
    const res = await request<{
      exists?: boolean;
      content?: string;
    }>(
      `/application-projects/${props.project.id}/modules/${props.moduleId}/config-files/${encodeURIComponent(file.filename)}?${qs}`,
      "GET",
      undefined,
      lifetime.signal,
    );
    if (lifetime.signal.aborted || hostId.value !== targetHost || environmentName.value.trim() !== targetEnvironment) return;
    file.existsOnServer = !!res.exists;
    if (res.exists && typeof res.content === "string" && !file.saved && file.content === file.baseline) {
      file.content = res.content;
    }
  } finally {
    file.loading = false;
  }
}

async function loadRemoteConfigFiles(showToast = false) {
  if (!hostId.value || !environmentName.value.trim() || loadingRemoteConfigs.value) return;
  loadingRemoteConfigs.value = true;
  formError.value = "";
  const targetHost = hostId.value;
  const targetEnvironment = environmentName.value.trim();
  try {
    const qs = new URLSearchParams({
      hostId: targetHost,
      environment: targetEnvironment,
    });
    const res = await request<{
      files?: { filename: string }[];
    }>(
      `/application-projects/${props.project.id}/modules/${props.moduleId}/config-files?${qs}`,
      "GET",
      undefined,
      lifetime.signal,
    );
    if (lifetime.signal.aborted || hostId.value !== targetHost || environmentName.value.trim() !== targetEnvironment) return;
    const remoteList = (res.files || []).filter((file) => validConfigFilename(file.filename));
    for (const remote of remoteList) {
      let local = configFiles.value.find((f) => f.filename === remote.filename);
      if (!local) {
        local = {
          filename: remote.filename,
          content: "",
          baseline: "",
          existsOnServer: true,
        };
        configFiles.value.push(local);
      } else {
        local.existsOnServer = true;
      }
    }
    const target =
      configFiles.value.find((f) => f.filename === activeConfigFilename.value) ||
      configFiles.value[0];
    if (target) {
      activeConfigFilename.value = target.filename;
      if (target.existsOnServer) {
        await readRemoteConfigFile(target);
      }
    }
    if (showToast) {
      ElMessage.success(
        remoteList.length
          ? `发现 ${remoteList.length} 份旧配置，选中文件可导入草稿`
          : "目标服务器暂无该环境的配置文件",
      );
    }
  } catch (error) {
    if (!lifetime.signal.aborted && showToast) {
      formError.value = describe(error, "读取服务器配置文件失败");
    }
  } finally {
    loadingRemoteConfigs.value = false;
  }
}

async function selectConfigTab(filename: string) {
  activeConfigFilename.value = filename;
  const target = configFiles.value.find((f) => f.filename === filename);
  if (target && target.existsOnServer && !target.content && !target.loading && hostId.value) {
    try {
      await readRemoteConfigFile(target);
    } catch {
      /* User can inspect error via explicit server reload. */
    }
  }
}

function validConfigFilename(filename: string) {
  return /^[A-Za-z0-9][A-Za-z0-9._-]{0,74}\.(?:yml|yaml|properties)$/.test(filename) && !filename.includes("..");
}

function confirmAddConfigFile() {
  const clean = newFilenameInput.value.trim();
  if (!clean) return;
  if (!validConfigFilename(clean)) {
    formError.value = "请输入有效的 YAML 或 properties 文件名，如 application-test.yml。";
    return;
  }
  let existing = configFiles.value.find((f) => f.filename === clean);
  if (!existing) {
    existing = {
      filename: clean,
      content: "",
      baseline: "",
      existsOnServer: false,
    };
    configFiles.value.push(existing);
  }
  activeConfigFilename.value = existing.filename;
  newFilenameInput.value = "";
  addingConfigFile.value = false;
  formError.value = "";
}

function insertCredentialPlaceholder(cred: SSHCredential) {
  const file = ensureActiveConfigFile();
  const token = `\${SFOPS_CRED:${cred.name}}`;
  if (!file.content) {
    file.content = `# 引用凭据：${cred.name}\npassword: ${token}\n`;
  } else {
    file.content = `${file.content}${file.content.endsWith("\n") ? "" : "\n"}${token}\n`;
  }
  ElMessage.success(`已插入凭据占位符 ${token}`);
}

function insertMiddlewareYamlSnippet(item: SoftwareInstance) {
  void ensureCredentialsLoaded();
  if (!dependencyIds.value.includes(item.id)) {
    dependencyIds.value.push(item.id);
  }
  const file = ensureActiveConfigFile();
  const isYaml = /\.ya?ml$/i.test(file.filename);
  const hostAddr = hostMap.value.get(item.hostId)?.address || "127.0.0.1";
  const key = (
    item.installation?.definition?.catalogKey ||
    item.softwareType ||
    ""
  ).toLowerCase();
  let snippet = "";
  if (key === "mysql") {
    const port = inferInstancePort(item, 3306);
    snippet = isYaml
      ? `spring:\n  datasource:\n    url: jdbc:mysql://${hostAddr}:${port}/streamfusion?useUnicode=true&characterEncoding=utf8&serverTimezone=Asia/Shanghai\n    username: root\n    password: \${SFOPS_CRED:请替换为MySQL凭据名称}\n`
      : `DB_URL=jdbc:mysql://${hostAddr}:${port}/streamfusion?serverTimezone=Asia/Shanghai\nDB_USERNAME=root\nDB_PASSWORD=\${SFOPS_CRED:请替换为MySQL凭据名称}\n`;
  } else if (key === "redis") {
    const port = inferInstancePort(item, 6379);
    snippet = isYaml
      ? `spring:\n  data:\n    redis:\n      host: ${hostAddr}\n      port: ${port}\n      password: \${SFOPS_CRED:请替换为Redis凭据名称}\n`
      : `REDIS_HOST=${hostAddr}\nREDIS_PORT=${port}\nREDIS_PASSWORD=\${SFOPS_CRED:请替换为Redis凭据名称}\n`;
  } else if (key === "seaweedfs") {
    const port = inferInstancePort(item, 8333);
    snippet = isYaml
      ? `storage:\n  s3:\n    endpoint: http://${hostAddr}:${port}\n    access-key: \${SFOPS_CRED:请替换为AccessKey凭据}\n    secret-key: \${SFOPS_CRED:请替换为SecretKey凭据}\n`
      : `S3_ENDPOINT=http://${hostAddr}:${port}\nS3_ACCESS_KEY=\${SFOPS_CRED:请替换为AccessKey凭据}\nS3_SECRET_KEY=\${SFOPS_CRED:请替换为SecretKey凭据}\n`;
  }
  if (snippet) {
    appendToActiveConfig(snippet);
    ElMessage.success(`已将 ${item.name} 连接配置插入 ${file.filename}，请检查凭据占位符名称`);
  }
}

function removeActiveConfigFile() {
  const file = activeConfigFile.value;
  if (!file) return;
  const idx = configFiles.value.findIndex((f) => f.filename === file.filename);
  if (idx >= 0) configFiles.value.splice(idx, 1);
  activeConfigFilename.value = configFiles.value[0]?.filename || "";
}
function addProxyRule() {
  const firstBackend = backendModuleChoices.value.find(
    (m) => m.catalog?.kind === "BACKEND",
  );
  nginxProxyRules.value.push({
    location: "/api",
    targetType: firstBackend ? "MODULE" : "URL",
    targetModuleId: firstBackend?.id || "",
    upstreamUrl: "",
    stripPrefix: true,
    webSocket: false,
    sse: false,
  });
}
function onProxyTargetChange(rule: EditableProxyRule, value: string) {
  if (value === "__URL__") {
    rule.targetType = "URL";
    rule.targetModuleId = "";
  } else {
    rule.targetType = "MODULE";
    rule.targetModuleId = value;
  }
}
function clearHistoricalBuildService() {
  runtimeId.value = "";
}

function describe(error: unknown, fallback: string) {
  if (error instanceof ApiError && error.code === "AUTH_REQUIRED")
    emit("error", error);
  return error instanceof ApiError
    ? [error.message, error.code, error.traceId].filter(Boolean).join(" · ")
    : fallback;
}

onMounted(async () => {
  const shouldLoadCredentials =
    !!module.value?.catalog ||
    !!props.registration?.deployMode ||
    !!props.registration?.variables?.length ||
    !!props.registration?.configFiles?.length;
  await Promise.all([
    request<Host[]>("/hosts", "GET", undefined, lifetime.signal)
      .then((items) => {
        hosts.value = items;
        if (!props.registration && availableHosts.value.length === 1)
          hostId.value = availableHosts.value[0].id;
      })
      .catch((error) => {
        if (!lifetime.signal.aborted)
          hostError.value = describe(error, "无法读取主机");
      })
      .finally(() => {
        loadingHosts.value = false;
      }),
    requestAllPages<ManagedTarget>("/instances", lifetime.signal)
      .then((items) => {
        managedTargets.value = items;
      })
      .catch(() => {})
      .finally(() => {
        loadingTargets.value = false;
      }),
    requestAllPages<SoftwareInstance>("/software-instances", lifetime.signal)
      .then((items) => {
        if (items.length || !software.value.length) software.value = items;
      })
      .catch((error) => {
        if (!lifetime.signal.aborted && !software.value.length)
          softwareError.value = describe(error, "无法读取已识别的软件");
      })
      .finally(() => {
        loadingSoftware.value = false;
      }),
    shouldLoadCredentials ? ensureCredentialsLoaded() : Promise.resolve(),
  ]);
});
onUnmounted(() => lifetime.abort());

async function save() {
  if (!module.value || props.project.archived || saving.value || !environmentName.value.trim())
    return;
  if (algorithm.value && !props.registration && !availableHosts.value.some((host) => host.id === hostId.value)) {
    formError.value = "请选择尚未登记此算法模块的服务器";
    return;
  }
  if (deployMode.value === "STATIC" && !canUseStaticMode.value) {
    formError.value = "算法模块只能登记原生进程或 Docker 容器";
    return;
  }
  if (deployMode.value === "STATIC" && streamFusionBackend.value && ["", "_"].includes(nginxServerName.value.trim())) {
    formError.value = "请填写用户访问时使用的站点域名或 IP 地址";
    return;
  }
  if (hasUnsatisfiedPlainSensitiveVar.value) {
    formError.value = "请为敏感变量选择「凭据引用」与对应凭据，避免明文密码或遗漏凭据";
    return;
  }
  const nextDrafts: ModuleConfigDraft[] = configFiles.value
    .filter((file) => file.filename.trim() && file.content.trim())
    .map(({ filename, content }) => ({ filename, content }));
  if (nativeJava.value && !managedId.value) {
    const encoder = new TextEncoder();
    const sizes = nextDrafts.map((file) => encoder.encode(file.content).length);
    if (nextDrafts.some((file) => !validConfigFilename(file.filename))) {
      formError.value = "配置文件只支持 .yml、.yaml 和 .properties。请移除其他文件后保存。";
      return;
    }
    if (nextDrafts.length > 12 || sizes.some((size) => size > 32 * 1024) || sizes.reduce((total, size) => total + size, 0) > 40 * 1024) {
      formError.value = "最多 12 份配置，每份不超过 32 KiB，合计不超过 40 KiB。";
      return;
    }
  }
  saving.value = true;
  formError.value = "";
  const payload = buildEnvironmentPatch({
    version: projectVersion,
    moduleId: props.moduleId,
    environment: environmentName.value,
    hostId: hostId.value,
    managedInstanceId: managedId.value,
    runtimeSoftwareId: runtimeId.value,
    deployMode: deployMode.value,
    profile: profile.value,
    bindAddress: bindAddress.value,
    servicePort: servicePort.value,
    containerPort: containerPort.value,
    memoryMiB: memoryMiB.value,
    startCommand: startCommand.value,
    healthPath: healthPath.value,
    dependencyIds: dependencyIds.value,
    variables: variables.value,
    configDrafts: nativeJava.value && !managedId.value ? nextDrafts : undefined,
    nginx: {
      nginxSoftwareId: nginxSoftwareId.value,
      siteName: nginxSiteName.value,
      serverName: nginxServerName.value,
      listenPort: nginxListenPort.value,
      spa: !!nginxSpa.value,
      proxyRules: nginxProxyRules.value,
    },
  }, props.registration);

  try {
    const saved = await request<ApplicationProject>(
      `/application-projects/${props.project.id}/service-instances`,
      "POST",
      payload,
      lifetime.signal,
    );
    if (lifetime.signal.aborted) return;
    ElMessage.success(algorithm.value ? "算法服务器登记已保存" : "部署草稿已保存");
    emit("saved", saved);
    emit("close");
  } catch (error) {
    if (!lifetime.signal.aborted)
      formError.value = describe(error, "运行配置保存失败，请重试");
  } finally {
    saving.value = false;
  }
}

async function removeEnvironment() {
  if (
    !props.registration ||
    props.project.archived ||
    originalRuntime ||
    !removing.value ||
    saving.value
  )
    return;
  saving.value = true;
  formError.value = "";
  try {
    const saved = await request<ApplicationProject>(
      `/application-projects/${props.project.id}/service-instances/${props.registration.id}`,
      "DELETE",
      { version: projectVersion },
      lifetime.signal,
    );
    if (lifetime.signal.aborted) return;
    emit("saved", saved);
    emit("close");
    ElMessage.success("实例登记已移除");
  } catch (error) {
    if (!lifetime.signal.aborted)
      formError.value = describe(error, "运行环境移除失败");
  } finally {
    saving.value = false;
  }
}
</script>

<template>
  <ElDialog
    :model-value="visible !== false"
    :title="algorithm ? (registration ? '编辑算法服务器登记' : '登记算法服务器') : '编辑部署草稿'"
    width="min(920px, 96vw)"
    append-to-body
    :close-on-click-modal="false"
    :close-on-press-escape="!saving"
    :show-close="!saving"
    @close="visible !== false && emit('close')"
  >
    <div class="binding-head">
      <div class="binding-module">
        {{ project.name }} / <span>{{ module?.name }}</span>
        <span v-if="previewAccessUrl" class="preview-url-badge">
          预计访问地址：<code>{{ previewAccessUrl }}</code>
        </span>
      </div>
    </div>

    <ElAlert
      v-if="algorithm"
      title="按服务器登记算法模块。关联本机已登记的运行软件后，可在项目交付页查看状态和日志；Jenkins 在此提供 QA。"
      type="info"
      show-icon
      :closable="false"
    />

    <ElAlert
      v-if="originalBuildService && runtimeId === originalRuntime"
      type="warning"
      show-icon
      :closable="false"
      style="margin-bottom: 12px"
    >
      <div class="ops-alert-row">
        <span>检测到历史记录曾将「构建服务 Jenkins」误关联为此模块的运行对象。</span>
        <ElButton link type="primary" size="small" @click="clearHistoricalBuildService">
          清除误绑定
        </ElButton>
      </div>
    </ElAlert>

    <ElForm label-position="top" class="binding-form" @submit.prevent="save">
      <div class="binding-grid-3">
        <ElFormItem label="部署环境" required>
          <ElSelect
            v-if="registration || addingAlgorithmHost"
            :model-value="environmentName"
            disabled
            style="width: 100%"
          >
            <ElOption :label="environmentName" :value="environmentName" />
          </ElSelect>
          <ElSelect
            v-else
            v-model="environmentName"
            filterable
            allow-create
            default-first-option
            placeholder="例如 test、dev、prod"
            :disabled="saving"
            style="width: 100%"
          >
            <ElOption
              v-for="item in project.environments"
              :key="item.id"
              :label="item.name"
              :value="item.name"
            />
            <ElOption
              v-for="p in profileSuggestions"
              :key="'p-' + p"
              :label="p"
              :value="p"
            />
          </ElSelect>
        </ElFormItem>

        <ElFormItem label="运行服务器" :required="!registration">
          <ElSelect
            v-model="hostId"
            aria-label="运行服务器"
            placeholder="选择运行服务器"
            :disabled="saving || loadingHosts || !!originalRuntime || !!hostError"
            style="width: 100%"
          >
            <ElOption
              v-if="hostId && !hosts.some((item) => item.id === hostId)"
              label="原绑定主机"
              :value="hostId"
            />
            <ElOption
              v-for="host in availableHosts"
              :key="host.id"
              :label="host.address ? `${host.name} (${host.address})` : host.name"
              :value="host.id"
            />
          </ElSelect>
        </ElFormItem>

        <ElFormItem label="部署形态">
          <ElSelect v-model="deployMode" :disabled="saving" style="width: 100%">
            <ElOption :label="module?.catalog?.kind === 'BACKEND' ? 'Java JAR（systemd）' : '原生进程（登记）'" value="NATIVE" />
            <ElOption label="Docker 容器（登记）" value="DOCKER" />
            <ElOption v-if="canUseStaticMode" :label="algorithm ? 'Nginx 静态站点（历史登记）' : 'Nginx 静态站点'" value="STATIC" />
          </ElSelect>
        </ElFormItem>
      </div>

      <ElFormItem
        v-if="algorithm || managedId || (!nativeJava && deployMode !== 'STATIC') || originalBuildService"
        :label="algorithm ? '关联已有运行服务' : '已有服务的日志来源'"
      >
        <ElSelect
          v-model="runtimeId"
          :aria-label="algorithm ? '关联已有运行服务' : '服务日志来源'"
          :disabled="saving || !hostId || loadingSoftware || !!softwareError"
          style="width: 100%"
        >
          <ElOption label="暂未设置" value="" />
          <ElOption
            v-if="originalRuntime && !runtimeOptions.some((item) => item.id === originalRuntime)"
            :label="originalBuildService ? 'Jenkins（原关联的构建服务）' : '原关联对象'"
            :value="originalRuntime"
          />
          <ElOption
            v-for="item in runtimeOptions"
            :key="item.id"
            :label="`${item.name} · ${item.locator}`"
            :value="item.id"
          />
        </ElSelect>
        <p v-if="algorithm && hostId && !loadingSoftware && !softwareError && !runtimeOptions.length" class="field-hint">
          这台服务器暂无已核对的运行服务。可先保存登记，服务注册到「软件管理」后再关联。
        </p>
      </ElFormItem>

      <div v-if="hostId && dependencyChecks.length" class="readiness-strip">
        <span class="readiness-label">依赖登记：</span>
        <ElTag
          v-for="check in dependencyChecks"
          :key="check.name"
          size="small"
          :type="check.satisfied ? 'info' : 'warning'"
        >
          {{ check.searchTerm }}{{ check.satisfied ? " 已登记" : " 未登记" }}
        </ElTag>
        <template v-if="missingDependencies.length">
          <ElButton
            v-for="miss in missingDependencies"
            :key="'go-' + miss.name"
            link
            type="primary"
            size="small"
            @click="emit('install-software', { search: miss.searchTerm, hostId })"
          >
            去安装 {{ miss.searchTerm }} ↗
          </ElButton>
        </template>
      </div>

      <template v-if="deployMode !== 'STATIC'">
        <div class="binding-grid-4">
          <ElFormItem label="服务端口">
            <ElInputNumber
              v-model="servicePort"
              :min="0"
              :max="65535"
              controls-position="right"
              style="width: 100%"
            />
          </ElFormItem>
          <ElFormItem label="监听地址">
            <ElInput v-model="bindAddress" placeholder="0.0.0.0 或 127.0.0.1" />
          </ElFormItem>
          <ElFormItem label="运行 Profile">
            <ElSelect
              v-model="profile"
              filterable
              allow-create
              clearable
              placeholder="如 test / prod"
              style="width: 100%"
            >
              <ElOption
                v-for="p in profileSuggestions"
                :key="'run-' + p"
                :label="p"
                :value="p"
              />
            </ElSelect>
          </ElFormItem>
          <ElFormItem label="健康检查路径">
            <ElInput v-model="healthPath" placeholder="/actuator/health" />
          </ElFormItem>
        </div>

        <div v-if="deployMode === 'DOCKER' || nativeJava" class="binding-fields">
          <ElFormItem v-if="deployMode === 'DOCKER'" label="容器内端口">
            <ElInputNumber
              v-model="containerPort"
              :min="1"
              :max="65535"
              controls-position="right"
              style="width: 100%"
            />
          </ElFormItem>
          <ElFormItem :label="nativeJava && !managedId ? '内存限额（MiB，0 使用默认 512）' : '内存限额（MiB，0 为不限制）'">
            <ElInputNumber
              v-model="memoryMiB"
              :min="0"
              :max="65536"
              :step="256"
              controls-position="right"
              style="width: 100%"
            />
          </ElFormItem>
        </div>

        <p v-if="nativeJava && !managedId" class="muted">发布时由 systemd 运行 Java JAR，并加载本环境的配置草稿。</p>
        <ElFormItem v-else label="启动命令（登记用途）">
          <ElInput
            v-model="startCommand"
            type="textarea"
            :rows="2"
            placeholder="例如 java -Xms256m -Xmx512m -jar app.jar --spring.profiles.active=test"
          />
        </ElFormItem>

        <div v-if="nativeJava && !managedId" class="section-box yaml-editor-box">
          <div class="section-box-head">
            <span>应用配置文件</span>
            <div class="section-box-actions">
              <ElDropdown
                v-if="pulledTemplates.length"
                trigger="click"
                @command="applyTemplateToEditor"
              >
                <ElButton size="small" :loading="pullingTemplates">
                  选用模板 ({{ pulledTemplates.length }}) ▾
                </ElButton>
                <template #dropdown>
                  <ElDropdownMenu>
                    <ElDropdownItem
                      v-for="tpl in pulledTemplates"
                      :key="tpl.filename"
                      :command="tpl"
                    >
                      {{ tpl.filename }}{{ tpl.profile ? ` (${tpl.profile})` : "" }}
                    </ElDropdownItem>
                  </ElDropdownMenu>
                </template>
              </ElDropdown>
              <ElButton
                size="small"
                :loading="pullingTemplates"
                @click="pullRepoConfigTemplates(true)"
              >
                从仓库拉取模板
              </ElButton>
              <ElButton
                size="small"
                :loading="loadingRemoteConfigs"
                :disabled="!hostId || !environmentName.trim()"
                @click="loadRemoteConfigFiles(true)"
              >
              导入旧配置
              </ElButton>
              <ElDropdown
                trigger="click"
                @visible-change="(open) => open && ensureCredentialsLoaded()"
                @command="insertCredentialPlaceholder"
              >
                <ElButton size="small">插入凭据 ▾</ElButton>
                <template #dropdown>
                  <ElDropdownMenu>
                    <ElDropdownItem
                      v-for="cred in activeSecretCredentials"
                      :key="`${cred.id}:${cred.revision}`"
                      :command="cred"
                    >
                      {{ cred.name }} (${SFOPS_CRED:{{ cred.name }}})
                    </ElDropdownItem>
                    <ElDropdownItem v-if="!activeSecretCredentials.length" disabled>
                      凭据库暂无可用密码或 Token
                    </ElDropdownItem>
                  </ElDropdownMenu>
                </template>
              </ElDropdown>
              <ElDropdown
                v-if="middlewareOptions.length"
                trigger="click"
                @command="insertMiddlewareYamlSnippet"
              >
                <ElButton size="small">参考已装中间件 ▾</ElButton>
                <template #dropdown>
                  <ElDropdownMenu>
                    <ElDropdownItem
                      v-for="mw in middlewareOptions"
                      :key="mw.id"
                      :command="mw"
                    >
                      {{ mw.name }} · {{ mw.hostName }}
                    </ElDropdownItem>
                  </ElDropdownMenu>
                </template>
              </ElDropdown>
            </div>
          </div>

          <div class="yaml-tabs-row">
            <ElTag
              v-for="file in configFiles"
              :key="file.filename"
              :type="file.filename === activeConfigFile?.filename ? 'primary' : 'info'"
              :effect="file.filename === activeConfigFile?.filename ? 'dark' : 'plain'"
              class="yaml-file-tab"
              @click="selectConfigTab(file.filename)"
            >
              {{ file.filename }}
              <span v-if="file.content !== file.baseline" class="dirty-dot">*</span>
            </ElTag>
            <template v-if="addingConfigFile">
              <ElInput
                v-model="newFilenameInput"
                size="small"
                placeholder="如 application-prod.yml"
                style="width: 185px"
                @keyup.enter="confirmAddConfigFile"
              />
              <ElButton size="small" type="primary" @click="confirmAddConfigFile">
                确定
              </ElButton>
              <ElButton size="small" @click="addingConfigFile = false; newFilenameInput = ''">
                取消
              </ElButton>
            </template>
            <ElButton
              v-else
              size="small"
              link
              type="primary"
              @click="addingConfigFile = true"
            >
              + 新增文件
            </ElButton>
            <ElButton
              v-if="activeConfigFile && (configFiles.length > 1 || activeConfigFile.existsOnServer)"
              size="small"
              link
              type="danger"
              :disabled="saving"
              style="margin-left: auto"
              @click="removeActiveConfigFile"
            >
              从草稿移除
            </ElButton>
          </div>

          <ElInput
            v-if="activeConfigFile"
            v-model="activeConfigFile.content"
            type="textarea"
            :rows="13"
            class="yaml-code-editor"
            :disabled="saving || activeConfigFile.loading"
            placeholder="可直接从仓库拉取 application-test.yml 模板或粘贴 YAML 配置；敏感密码可点击「插入凭据」使用 ${SFOPS_CRED:凭据名} 注入"
            spellcheck="false"
          />

          <div v-if="activeConfigFile" class="yaml-status-bar">
            <div class="yaml-path-info">
              <code class="yaml-remote-path">
                {{ activeConfigFile.filename }}
              </code>
            </div>
            <div class="yaml-sync-state">
              <ElTag v-if="activeConfigFile.content !== activeConfigFile.baseline" size="small" type="warning">
                草稿有改动
              </ElTag>
              <ElTag v-else-if="activeConfigFile.saved" size="small" type="info">
                已保存到草稿
              </ElTag>
              <ElTag v-else size="small" type="info">
                发布时写入目标服务器
              </ElTag>
            </div>
          </div>
        </div>
      </template>

      <template v-else>
        <div class="section-box">
          <div class="section-box-head">
            <span>Nginx 静态站点与后端反向代理</span>
            <ElButton
              v-if="hostId && !nginxOptions.length && !loadingSoftware"
              link
              type="primary"
              size="small"
              @click="emit('install-software', { search: 'Nginx', hostId })"
            >
              该服务器没有可选的原生 Nginx，去安装 ↗
            </ElButton>
          </div>
          <p v-if="!managedId" class="field-hint">发布时生成站点配置、检查语法，然后重载 Nginx。</p>
          <template v-if="streamFusionBackend && !managedId">
            <p class="field-hint">StreamFusion 页面使用 SPA 回退，<code>/api</code> 转到同环境已上线的 {{ streamFusionBackend.name }}。可先保存草稿，再发布后端和前端。</p>
            <ElButton size="small" @click="setStreamFusionRoutes">应用 StreamFusion 路由</ElButton>
            <p class="field-hint">应用后替换与 <code>/api</code>、<code>/preview</code>、<code>/storage</code> 重叠的草稿规则。</p>
          </template>
          <div class="binding-fields" style="margin-top: 8px">
            <ElFormItem label="服务器 Nginx 实例">
              <ElSelect
                v-model="nginxSoftwareId"
                placeholder="选择该服务器已安装的原生 Nginx"
                :disabled="!hostId || loadingSoftware"
                style="width: 100%"
              >
                <ElOption
                  v-for="ng in nginxOptions"
                  :key="ng.id"
                  :label="`${ng.name}（${ng.locator}）`"
                  :value="ng.id"
                />
              </ElSelect>
            </ElFormItem>
            <ElFormItem label="站点名称">
              <ElInput v-model="nginxSiteName" placeholder="例如 platform-web-test" />
            </ElFormItem>
          </div>
          <div class="binding-grid-3">
            <ElFormItem label="域名（server_name）">
              <ElInput v-model="nginxServerName" placeholder="填写实际站点域名，例如 platform.example.com" />
              <small v-if="streamFusionBackend" class="field-hint">填写用户访问的域名或 IP 地址。外层网关须保留该 Host。</small>
              <small v-else class="field-hint">填写 <code>_</code> 不会把此站点设为同端口的默认站点。</small>
            </ElFormItem>
            <ElFormItem label="Nginx 监听端口">
              <ElInputNumber
                v-model="nginxListenPort"
                :min="1"
                :max="65535"
                controls-position="right"
                style="width: 100%"
              />
            </ElFormItem>
            <ElFormItem label="SPA 单页路由回退（try_files）">
              <div style="display: flex; align-items: center; gap: 8px; min-height: 32px">
                <ElSwitch v-model="nginxSpa" />
                <span style="font-size: 13px">Vue / React History 路由支持</span>
              </div>
            </ElFormItem>
          </div>

          <div class="proxy-head">
            <span>后端接口反向代理规则（自动生成 nginx conf location）</span>
            <ElButton size="small" type="primary" plain @click="addProxyRule">
              + 添加反代规则
            </ElButton>
          </div>
          <div v-if="nginxProxyRules.length" class="var-rows">
            <div v-for="(rule, idx) in nginxProxyRules" :key="idx" class="proxy-row">
              <ElInput
                v-model="rule.location"
                placeholder="/api"
                style="width: 110px"
              />
              <ElSelect
                :model-value="rule.targetType === 'URL' ? '__URL__' : rule.targetModuleId"
                placeholder="选择同项目后端模块或自定义地址"
                style="flex: 1"
                @change="onProxyTargetChange(rule, String($event))"
              >
                <ElOption
                  v-for="bm in backendModuleChoices"
                  :key="bm.id"
                  :label="`${bm.name}${resolveBackendModuleEndpoint(bm.id) ? '（草稿 ' + resolveBackendModuleEndpoint(bm.id) + '）' : '（同环境尚未填端口）'}`"
                  :value="bm.id"
                />
                <ElOption label="自定义上游地址…" value="__URL__" />
              </ElSelect>
              <ElInput
                v-if="rule.targetType === 'URL'"
                v-model="rule.upstreamUrl"
                placeholder="http://127.0.0.1:8080"
                style="width: 190px"
              />
              <div class="proxy-switches">
                <ElSwitch v-model="rule.stripPrefix" size="small" />
                <span>去前缀</span>
                <ElSwitch v-model="rule.webSocket" size="small" />
                <span>WS</span>
                <ElSwitch v-model="rule.sse" size="small" />
                <span>SSE</span>
              </div>
              <ElButton link type="danger" size="small" @click="nginxProxyRules.splice(idx, 1)">
                删除
              </ElButton>
            </div>
          </div>
          <p v-else class="field-hint">
            暂无反向代理规则，如前端通过 /api 调用后端接口，可点击右上角添加规则并直接关联后端模块。
          </p>
        </div>
      </template>

      <ElCollapse style="margin-top: 8px">
        <ElCollapseItem title="高级运行参数（补充环境变量 / 历史目标兼容）" name="advanced">
          <div v-if="deployMode !== 'STATIC'" class="section-box" style="margin-bottom: 12px">
            <div class="section-box-head">
              <span>补充环境变量（可选，常规配置建议直接使用上方 YAML 文件）</span>
              <div class="section-box-actions">
                <ElButton
                  v-if="module?.catalog?.envHints?.length"
                  size="small"
                  @click="importRecognizedHints"
                >
                  导入环境变量提示 ({{ module.catalog.envHints.length }})
                </ElButton>
                <ElDropdown v-if="middlewareOptions.length" trigger="click">
                  <ElButton size="small">从中间件填入变量 ▾</ElButton>
                  <template #dropdown>
                    <ElDropdownMenu>
                      <ElDropdownItem
                        v-for="mw in middlewareOptions"
                        :key="mw.id"
                        @click="fillFromMiddleware(mw)"
                      >
                        {{ mw.name }} · {{ mw.hostName }}
                      </ElDropdownItem>
                    </ElDropdownMenu>
                  </template>
                </ElDropdown>
                <ElButton size="small" type="primary" plain @click="addVariableRow">
                  + 新增变量
                </ElButton>
              </div>
            </div>
            <div v-if="variables.length" class="var-rows">
              <div v-for="(v, idx) in variables" :key="idx" class="var-row">
                <ElInput
                  v-model="v.name"
                  placeholder="变量名，如 DB_URL"
                  style="width: 165px"
                />
                <ElRadioGroup v-model="v.mode" size="small" @change="v.mode === 'CREDENTIAL' && ensureCredentialsLoaded()">
                  <ElRadioButton value="PLAIN">普通值</ElRadioButton>
                  <ElRadioButton value="CREDENTIAL">凭据引用</ElRadioButton>
                </ElRadioGroup>
                <ElInput
                  v-if="v.mode === 'PLAIN'"
                  v-model="v.value"
                  :placeholder="isSensitiveVarName(v.name) ? '敏感键名请改选右侧「凭据引用」' : '输入变量值'"
                  style="flex: 1"
                />
                <ElSelect
                  v-else
                  v-model="v.credentialRef"
                  filterable
                  placeholder="选择已保存凭据"
                  style="flex: 1"
                  @visible-change="(open) => open && ensureCredentialsLoaded()"
                >
                  <ElOption
                    v-for="c in activeSecretCredentials"
                    :key="`${c.id}:${c.revision}`"
                    :label="`${c.name} · v${c.revision} (${c.kind})`"
                    :value="`${c.id}:${c.revision}`"
                  />
                </ElSelect>
                <ElButton link type="danger" size="small" @click="variables.splice(idx, 1)">
                  删除
                </ElButton>
              </div>
            </div>
            <p v-else class="field-hint">
              无补充环境变量。已在上方 YAML 配置文件中定义完整配置时，此处留空即可。
            </p>
            <p v-if="hasUnsatisfiedPlainSensitiveVar" class="error-hint">
              提示：名称含 PASSWORD / TOKEN / SECRET / KEY 的变量必须使用「凭据引用」，不可填写明文。
            </p>
          </div>
          <ElFormItem v-if="originalTarget" label="旧版上线目标">
            <ElSelect
              v-model="managedId"
              :disabled="saving || !hostId || loadingTargets || !!targetError"
              style="width: 100%"
            >
              <ElOption label="解除旧版目标关联" value="" />
              <ElOption
                v-if="originalTarget && managedId === originalTarget && !targetOptions.some((item) => item.id === originalTarget)"
                label="原上线目标（待核对）"
                :value="originalTarget"
              />
              <ElOption
                v-for="item in targetOptions"
                :key="item.id"
                :label="`${item.name} · ${item.config.hostPort}`"
                :value="item.id"
              />
            </ElSelect>
          </ElFormItem>
        </ElCollapseItem>
      </ElCollapse>

      <ElAlert
        v-if="hostError || softwareError || formError"
        type="error"
        show-icon
        :closable="false"
        :title="[hostError, softwareError, formError].filter(Boolean).join(' · ')"
        style="margin-top: 12px"
      />

      <ElAlert
        v-if="removing"
        type="warning"
        show-icon
        :closable="false"
        style="margin-top: 8px"
      >
        <div class="remove-prompt" role="group" aria-label="移除运行环境">
          <span>移除 {{ module?.name }} 的 {{ environmentName }} 运行环境？</span>
          <div class="remove-actions">
            <ElButton size="small" :disabled="saving" @click="removing = false">
              取消移除
            </ElButton>
            <ElButton type="danger" size="small" :loading="saving" @click="removeEnvironment">
              确认移除
            </ElButton>
          </div>
        </div>
      </ElAlert>
    </ElForm>

    <template #footer>
      <div class="dialog-footer">
        <ElButton
          v-if="registration && !originalRuntime && !removing"
          link
          type="danger"
          class="remove-btn"
          :disabled="saving"
          @click="removing = true"
        >
          移除登记
        </ElButton>
        <div class="footer-right">
          <ElButton :disabled="saving" @click="emit('close')">取消</ElButton>
          <ElButton
            type="primary"
            :loading="saving"
            :disabled="saving || loadingHosts || !module || (!registration && (!hostId || !!hostError))"
            @click="save"
          >
            {{ saving ? "保存中…" : algorithm ? "保存登记" : "保存草稿" }}
          </ElButton>
        </div>
      </div>
    </template>
  </ElDialog>
</template>

<style scoped>
.binding-head {
  margin-bottom: 10px;
}
.binding-module {
  margin: 0;
  font-size: 13px;
  color: var(--muted);
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 8px;
}
.binding-module span {
  color: var(--text);
  font-weight: 500;
}
.preview-url-badge {
  margin-left: auto;
  font-size: 12px;
  color: #334155;
  font-weight: 400 !important;
}
.binding-form {
  display: grid;
  gap: 4px;
}
.binding-fields {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: 12px;
}
.binding-grid-3 {
  display: grid;
  grid-template-columns: repeat(3, minmax(0, 1fr));
  gap: 12px;
}
.binding-grid-4 {
  display: grid;
  grid-template-columns: repeat(4, minmax(0, 1fr));
  gap: 12px;
}
.readiness-strip {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 6px 8px;
  padding: 8px 12px;
  margin-bottom: 10px;
  background: #f8fafc;
  border: 1px solid var(--border);
  border-radius: 6px;
  font-size: 12px;
}
.readiness-label {
  color: var(--muted);
}
.section-box {
  padding: 12px 14px;
  border: 1px solid var(--border);
  border-radius: 6px;
  background: #fafbfc;
  margin-bottom: 8px;
}
.section-box-head,
.proxy-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  flex-wrap: wrap;
  gap: 8px;
  font-size: 13px;
  color: #303133;
}
.proxy-head {
  margin: 8px 0 6px;
  padding-top: 8px;
  border-top: 1px dashed var(--border);
}
.section-box-actions {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 6px;
}
.yaml-editor-box {
  display: grid;
  gap: 10px;
}
.yaml-tabs-row {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 6px;
}
.yaml-file-tab {
  cursor: pointer;
  user-select: none;
}
.dirty-dot {
  margin-left: 3px;
  font-weight: 700;
}
.yaml-code-editor :deep(textarea) {
  font-family: var(--mono);
  font-size: 12px;
  line-height: 1.55;
  white-space: pre;
}
.yaml-status-bar {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  justify-content: space-between;
  gap: 8px;
}
.yaml-path-info {
  display: flex;
  align-items: center;
  gap: 8px;
  min-width: 0;
  flex: 1;
}
.yaml-remote-path {
  font: 11px/1.4 var(--mono);
  color: #475569;
  overflow-wrap: anywhere;
  word-break: break-word;
}
.yaml-sync-state {
  display: flex;
  align-items: center;
  gap: 6px;
}
.var-rows {
  display: grid;
  gap: 8px;
  margin-top: 10px;
}
.var-row,
.proxy-row {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 8px;
}
.proxy-switches {
  display: inline-flex;
  align-items: center;
  gap: 5px;
  font-size: 12px;
  color: #475569;
}
.field-hint {
  margin: 6px 0 0;
  font-size: 12px;
  color: var(--muted);
  line-height: 1.5;
}
.error-hint {
  margin: 8px 0 0;
  font-size: 12px;
  color: #dc2626;
}
.ops-alert-row {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
}
.remove-prompt {
  display: flex;
  align-items: center;
  justify-content: space-between;
  flex-wrap: wrap;
  gap: 8px;
  width: 100%;
}
.remove-actions {
  display: flex;
  gap: 8px;
}
.dialog-footer {
  display: flex;
  align-items: center;
  justify-content: space-between;
  width: 100%;
}
.remove-btn {
  margin-right: auto;
}
.footer-right {
  display: flex;
  gap: 8px;
  margin-left: auto;
}
@media (max-width: 720px) {
  .binding-fields,
  .binding-grid-3,
  .binding-grid-4 {
    grid-template-columns: minmax(0, 1fr);
  }
}
</style>
