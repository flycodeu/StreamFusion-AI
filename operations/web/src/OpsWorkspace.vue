<script setup lang="ts">
import { ref, onMounted, onUnmounted, watch, computed } from "vue";
import { ElMessage, ElMessageBox } from "element-plus";
import { ApiError, request, upload } from "./api";
import { jobKindLabels as softwareJobKindLabels, type PageResult } from "./jobs";
import OpsPagination from "./OpsPagination.vue";
import { requestAllPages } from "./listPages";
import { previewDefaults, type PreviewConfig } from "./preview";
import { zlmDefaults, type ZLMConfig } from "./zlm";
import type { Host } from "./hosts";
import { runtimePolicyError, type RuntimePolicy, type RuntimePolicyState } from "./runtimePolicy";
import { softwareJobPhaseLabels } from "./software";
import OpsExternalBindingsSection from "./OpsExternalBindingsSection.vue";
import OpsJobsSection from "./OpsJobsSection.vue";
import OpsWorkspaceDialogs from "./OpsWorkspaceDialogs.vue";
import "./ops-workspace.css";
const props = defineProps<{ page: string }>();
const emit = defineEmits<{ error: [unknown] }>();
const instanceHealthLabel = (health?: string) => ({ APPLICATION_READY: "应用就绪", APPLICATION_STOPPED: "已停止", STOPPED: "已停止", RUNNING: "运行中", UNKNOWN: "状态未确认" }[health || ""] || "状态未确认");
interface Artifact {
  recipe: string;
  id: string;
  name: string;
  imageId: string;
  archiveSha256: string;
  byteSize: number;
  available: boolean;
  runtimeEnvironment?: RuntimeEnvironment | null;
}
interface RuntimeEnvironment {
  installationId: string;
  pluginVersion: string;
  imageDigest: string;
}
interface RuntimeInstallation {
  id: string;
  targetId: string;
  pluginKey: string;
  pluginVersion: string;
  status: string;
}
interface Credential {
  id: string;
  revision: string;
  name: string;
  kind: string;
  revokedAt: string;
}
interface Binding {
  id: string;
  kind: string;
  host: string;
  port: number;
  database: string;
  username: string;
  credentialId: string;
  credentialRevision: string;
}
interface Config {
  runtimePolicy?: RuntimePolicy;
  preview?: PreviewConfig;
  zlm?: ZLMConfig;
  initialPasswordCredentialId?: string;
  initialPasswordRevision?: string;
  environment: string;
  profile: string;
  bindAddress: string;
  hostPort: number;
  containerPort: number;
  memoryMiB: number;
  healthPath: string;
  healthSeconds: number;
  dataCompatibility: string;
  bindingIds: string[];
}
interface Instance {
  pendingJob?: { id: string; status: string; kind: string; targetAckPending: boolean };
  id: string;
  name: string;
  targetId: string;
  version: string;
  draftRevision: string;
  appliedGeneration: string;
  config: Config;
  currentReleaseId: string;
  previousReleaseId: string;
  observation: {
    runtimePolicy?: RuntimePolicyState;
    health?: string;
    releaseId?: string;
    preview?: {
      state: string;
      configVersion: string;
      maxSessions: number;
      maxTranscodes: number;
    };
  };
  observedAt: string;
}
interface Release {
  runtimeEnvironment?: RuntimeEnvironment;
  id: string;
  imageId: string;
  artifactId: string;
  config: Config;
  bindings?: Binding[];
}
interface ProbeLayer {
  layer: string;
  executed: boolean;
  reachable: boolean;
  authenticated: boolean;
  errorCode?: string;
}
interface BindingProbeResult {
  controller: ProbeLayer;
  targetHost: ProbeLayer;
  applicationNetwork: ProbeLayer | null;
  authentication: ProbeLayer | null;
  errorCode: string;
  observedAt: string;
  releaseId?: string;
  credentialRevision?: string;
}
interface Plan {
  id: string;
  kind: string;
  candidate: Release;
  baseline?: Release;
  expiresAt: string;
  expectedVersion: string;
  expectedGeneration: string;
  maintenancePermit?: string;
}
interface Job {
  id: string;
  kind: string;
  status: string;
  phase: string;
  errorCode: string;
  targetAckPending: boolean;
  traceId: string;
  createdAt: string;
  updatedAt: string;
  result: Record<string, unknown>;
}
const targets = ref<Host[]>([]),
  jobs = ref<Job[]>([]),
  credentials = ref<Credential[]>([]),
  artifactOptions = ref<Artifact[]>([]),
  instanceOptions = ref<Instance[]>([]),
  bindingOptions = ref<Binding[]>([]),
  bindingsExpanded = ref(false),
  optionsLoading = ref(false);
const runtimeInstallations = ref<RuntimeInstallation[]>([]),
  importSource = ref("DOCKER_ARCHIVE"),
  importRuntime = ref("");
const recipe = ref("java-compose-v1"),
  importRecipe = ref("java-compose-v1");
const maintenancePermit = ref(""),
  planKind = ref("PUBLISH"),
  registration = ref<unknown>(null);
const eligibleArtifacts = computed(() =>
  artifactOptions.value.filter(
    (a) =>
      a.available &&
      a.recipe ===
        (planInstance.value?.config.preview
          ? "preview-node-v1"
          : planInstance.value?.config.zlm
            ? "zlm-direct-v1"
            : "java-compose-v1"),
  ),
);
function recipeChanged() {
  instanceDraft.value.config =
    recipe.value === "zlm-direct-v1"
      ? {
          environment: "zlm",
          profile: "",
          bindAddress: "",
          hostPort: 18080,
          containerPort: 8080,
          memoryMiB: 4096,
          healthPath: "/index/api/getServerConfig",
          healthSeconds: 60,
          dataCompatibility: "zlm-direct-v1",
          bindingIds: [],
          zlm: zlmDefaults(),
        }
      : recipe.value === "preview-node-v1"
        ? {
            environment: "preview",
            profile: "",
            bindAddress: "",
            hostPort: 9443,
            containerPort: 9443,
            memoryMiB: 8192,
            healthPath: "/preview-control/v1/snapshot",
            healthSeconds: 60,
            dataCompatibility: "preview-ownership-v1",
            bindingIds: [],
            preview: previewDefaults(),
          }
        : configDefault();
}
async function showRegistration(i: Instance) {
  await act(async () => {
    registration.value = await request(
      `/instances/${i.id}/preview-registration`,
    );
    dialog.value = "registration";
  });
}
const busy = ref(false),
  dialog = ref(""),
  bindingProbeTargets = ref<Record<string, string>>({}),
  bindingProbeInstances = ref<Record<string, string>>({}),
  bindingProbeResults = ref<Record<string, BindingProbeResult>>({}),
  selected = ref<Instance>(),
  plan = ref<Plan>(),
  planInstance = ref<Instance>(),
  detail = ref<Job>(),
  logs = ref(""),
  artifactChoice = ref("");
const artifactList = useListPage<Artifact>(() => "/artifacts");
const instanceList = useListPage<Instance>(() => "/instances");
const bindingList = useListPage<Binding>(() => "/bindings");
const releaseList = useListPage<Release>(() => `/instances/${selected.value?.id}/releases`);
const { items: artifacts, page: artifactPage, pageSize: artifactPageSize, total: artifactTotal, loading: artifactsLoading } = artifactList;
const { items: instances, page: instancePage, pageSize: instancePageSize, total: instanceTotal, loading: instancesLoading } = instanceList;
const { items: bindings, page: bindingPage, pageSize: bindingPageSize, total: bindingTotal, loading: bindingsLoading } = bindingList;
const { items: releases, page: releasePage, pageSize: releasePageSize, total: releaseTotal, loading: releasesLoading } = releaseList;
const configDefault = (): Config => ({
  environment: "test",
  profile: "",
  bindAddress: "127.0.0.1",
  hostPort: 18080,
  containerPort: 8080,
  memoryMiB: 512,
  healthPath: "/actuator/health",
  healthSeconds: 30,
  dataCompatibility: "schema-1",
  bindingIds: [],
});
const instanceDraft = ref({
  name: "",
  targetId: "",
  version: "",
  config: configDefault(),
});
const runtimePolicyDraft = ref<RuntimePolicy>(), runtimePolicyBaseline = ref("");
const runtimePolicyErrorMessage = ref(""), runtimePolicyAttempted = ref(false);
let runtimePolicyKey = "";
const runtimePolicyDirty = computed(() => !!runtimePolicyDraft.value && JSON.stringify(runtimePolicyDraft.value) !== runtimePolicyBaseline.value);
function editRuntimePolicy(value?: RuntimePolicy) {
  if (runtimePolicyAttempted.value) return;
  runtimePolicyDraft.value = value;
  runtimePolicyKey = ""; runtimePolicyErrorMessage.value = "";
}
async function openRuntimePolicy(instance: Instance) {
  selected.value = instance; dialog.value = "runtime-policy";
  runtimePolicyDraft.value = undefined; runtimePolicyErrorMessage.value = "";
  runtimePolicyKey = ""; runtimePolicyAttempted.value = false;
  await act(async () => {
    if (instance.pendingJob) { await readInstanceJob(instance); return; }
    await request(`/instances/${instance.id}/observations`, "POST");
    const current = await request<Instance>(`/instances/${instance.id}`);
    if (dialog.value !== "runtime-policy" || selected.value?.id !== instance.id) return;
    if (current.pendingJob) { await readInstanceJob(current); return; }
    selected.value = current;
    const policy = current.observation.runtimePolicy?.configured || current.observation.runtimePolicy?.actual;
    runtimePolicyDraft.value = policy && { ...policy };
    runtimePolicyBaseline.value = JSON.stringify(runtimePolicyDraft.value);
  });
}
async function readInstanceJob(instance: Instance) {
  if (!instance.pendingJob) return;
  detail.value = await request<Job>(`/jobs/${instance.pendingJob.id}`);
  dialog.value = "job";
}
async function showInstanceJob(instance: Instance) { await act(() => readInstanceJob(instance)); }
const instanceProgressLabel = (instance: Instance) => instance.pendingJob?.status === "UNKNOWN" ? "核对原操作" : instance.pendingJob?.targetAckPending ? "等待收尾" : "查看进度";
async function applyRuntimePolicy() {
  if (!selected.value || !runtimePolicyDraft.value || busy.value) return;
  runtimePolicyErrorMessage.value = runtimePolicyError(runtimePolicyDraft.value, "DOCKER");
  if (runtimePolicyErrorMessage.value) return;
  runtimePolicyKey ||= crypto.randomUUID();
  runtimePolicyAttempted.value = true;
  await act(async () => {
    try {
      await request(`/instances/${selected.value!.id}/runtime-policy`, "POST", { expectedVersion: selected.value!.version, idempotencyKey: runtimePolicyKey, policy: runtimePolicyDraft.value });
    } catch (cause) {
      if (cause instanceof ApiError && cause.status && cause.status < 500) { runtimePolicyAttempted.value = false; runtimePolicyKey = ""; }
      throw cause;
    }
    dialog.value = ""; await refresh(); ElMessage.success("运行策略作业已受理");
  });
}
let instanceActionIntent: { id: string; action: string; version: string; key: string } | undefined;
async function instanceAction(instance: Instance, action: "START" | "STOP" | "RESTART") {
  if (busy.value || !instance.currentReleaseId) return;
  if (action !== "START") {
    try { await ElMessageBox.confirm(action === "STOP" ? `停止“${instance.name}”并保持停止？下次启动会恢复原运行策略。` : `重启“${instance.name}”的当前版本？`, action === "STOP" ? "停止应用" : "重启应用", { confirmButtonText: "确认", cancelButtonText: "取消", type: "warning" }); }
    catch { return; }
  }
  if (!instanceActionIntent || instanceActionIntent.id !== instance.id || instanceActionIntent.action !== action || instanceActionIntent.version !== instance.version)
    instanceActionIntent = { id: instance.id, action, version: instance.version, key: crypto.randomUUID() };
  const intent = instanceActionIntent;
  await act(async () => {
    try { await request(`/instances/${instance.id}/actions`, "POST", { expectedVersion: intent.version, idempotencyKey: intent.key, action }); }
    catch (cause) { if (cause instanceof ApiError && cause.status && cause.status < 500) instanceActionIntent = undefined; throw cause; }
    instanceActionIntent = undefined; dialog.value = ""; await refresh(); ElMessage.success("应用操作已受理");
  });
}
const bindingDraft = ref({
  kind: "MYSQL",
  host: "",
  port: 3306,
  database: "",
  username: "",
  credentialId: "",
  credentialRevision: "",
});
const importName = ref(""),
  importSHA = ref(""),
  file = ref<File>(),
  uploadPhase = ref("");
let timer: ReturnType<typeof setTimeout> | undefined;
let alive = true;
const time = (v: string) =>
  v
    ? new Date(v).toLocaleString("zh-CN", {
        timeZone: "Asia/Shanghai",
        hour12: false,
      })
    : "—";
const short = (v: string) => (v ? v.slice(0, 8) : "—");
const fresh = (v: string) => v && Date.now() - new Date(v).getTime() < 30000;
const credentialOptions = computed(() =>
  credentials.value.filter((c) => !c.revokedAt),
);
const jobStatusFilter = ref("");
const jobKindFilter = ref("");
const jobPage = ref(1), jobPageSize = ref(10), jobCursors = ref([""]),
  jobNextCursor = ref(""), jobsLoading = ref(false);
let jobsController: AbortController | undefined, detailController: AbortController | undefined,
  optionsController: AbortController | undefined;
let appliedJobFilters = { status: "", kind: "" }, restoringJobFilters = false;
const jobStatus = (job: Job) => job.phase === "CANCELLED" ? "CANCELLED" : job.status;
const statusLabels: Record<string, string> = {
  QUEUED: "排队中",
  RUNNING: "执行中",
  UNKNOWN: "结果未确认",
  SUCCEEDED: "成功",
  FAILED: "失败",
  CANCELLED: "已取消",
};
const jobKindLabels: Record<string, string> = {
  ...softwareJobKindLabels,
  FIREWALL_CHANGE: "防火墙变更",
  HOST_PREPARE: "主机接入",
  IMPORT: "导入制品",
  PUBLISH: "发布",
  ROLLBACK: "回滚",
};
const phaseLabels: Record<string, string> = {
  ...softwareJobPhaseLabels,
  BASELINE_UNCHANGED: "原节点保持运行",
  PULLING: "拉取环境镜像",
  VERIFYING: "校验环境摘要",
  VERSION_CHECK: "探测工具版本",
  RECORDED: "环境已登记",
  REMOVING: "卸载环境",
  REMOVED: "环境已卸载",
  PLUGIN_ERROR: "插件异常",
  CANCELLED: "已取消",
  WAITING_UPLOAD: "等待上传",
  UPLOADING: "上传中",
  UPLOADED: "待校验",
  VERIFY: "校验归档",
  REGISTERED: "制品已登记",
  PREFLIGHT: "等待执行",
  TARGET_EXECUTION: "目标执行中",
  TRANSFER: "传输归档",
  IMPORT: "导入镜像",
  ACTIVATE: "启动候选",
  HEALTH: "检查健康",
  RECOVERY: "恢复原版本",
  NOT_STARTED: "未启动，原状态已核对",
  HEALTH_VERIFIED: "健康已验证",
  BASELINE_RESTORED: "已恢复原版本",
  EMPTY_BASELINE_RESTORED: "候选已停止",
  RECOVERY_REQUIRED: "需要恢复",
  CONNECTING: "连接目标",
  UPLOADING_EXECUTOR: "上传接入材料",
  INSTALLING_EXECUTOR: "安装接入组件",
  VERIFYING_EXECUTOR: "检查接入结果",
  PREPARATION_RESULT: "接入结果",
};
async function act(fn: () => Promise<void>) {
  if (busy.value) return;
  busy.value = true;
  try {
    await fn();
  } catch (e) {
    emit("error", e);
  } finally {
    busy.value = false;
  }
}
function useListPage<T>(path: () => string) {
  const items = ref<T[]>([]), page = ref(1), pageSize = ref(10), total = ref(0), loading = ref(false);
  let controller: AbortController | undefined;
  async function load(nextPage = page.value, nextSize = pageSize.value): Promise<void> {
    controller?.abort();
    const current = new AbortController();
    controller = current;
    loading.value = true;
    const requestedPath = path();
    try {
      const out = await request<{ items: T[]; total: number }>(`${requestedPath}?page=${nextPage}&pageSize=${nextSize}`, "GET", undefined, current.signal);
      if (current.signal.aborted || requestedPath !== path()) return;
      const last = Math.max(1, Math.ceil(out.total / nextSize));
      if (nextPage > last) {
        await load(last, nextSize);
        return;
      }
      total.value = out.total;
      items.value = out.items as typeof items.value;
      page.value = nextPage;
      pageSize.value = nextSize;
    } catch (error) {
      if (!current.signal.aborted) throw error;
    } finally {
      if (controller === current) loading.value = false;
    }
  }
  function changePage(value: number) {
    void load(value).catch(error => emit("error", error));
  }
  function changeSize(value: number) {
    void load(1, value).catch(error => emit("error", error));
  }
  onUnmounted(() => controller?.abort());
  return { items, page, pageSize, total, loading, load, changePage, changeSize };
}
async function loadJobs(nextPage = jobPage.value, nextSize = jobPageSize.value, cursors = jobCursors.value.slice()) {
  jobsController?.abort();
  const current = new AbortController();
  jobsController = current;
  jobsLoading.value = true;
  const filters = { status: jobStatusFilter.value, kind: jobKindFilter.value };
  const query = new URLSearchParams({ limit: String(nextSize) });
  if (cursors[nextPage - 1]) query.set("before", cursors[nextPage - 1]);
  if (filters.status) query.set("status", filters.status);
  if (filters.kind) query.set("kind", filters.kind);
  try {
    const out = await request<PageResult<Job>>(`/jobs?${query}`, "GET", undefined, current.signal);
    if (current.signal.aborted) return;
    jobs.value = out.items;
    jobPage.value = nextPage;
    jobPageSize.value = nextSize;
    jobNextCursor.value = out.nextCursor;
    jobCursors.value = cursors.slice(0, nextPage);
    appliedJobFilters = filters;
  } catch (error) {
    if (!current.signal.aborted) {
      if (filters.status !== appliedJobFilters.status || filters.kind !== appliedJobFilters.kind) {
        restoringJobFilters = true;
        jobStatusFilter.value = appliedJobFilters.status;
        jobKindFilter.value = appliedJobFilters.kind;
      }
      throw error;
    }
  } finally {
    if (jobsController === current) jobsLoading.value = false;
  }
}
function changeJobPage(value: number) {
  const cursors = jobCursors.value.slice();
  if (value > jobPage.value) {
    if (!jobNextCursor.value) return;
    cursors[jobPage.value] = jobNextCursor.value;
  }
  void loadJobs(value, jobPageSize.value, cursors).catch(error => emit("error", error));
}
function changeJobSize(value: number) {
  void loadJobs(1, value, [""]).catch(error => emit("error", error));
}
async function refreshJobDetail() {
  if (!detail.value || dialog.value !== "job" || detailController) return;
  const current = new AbortController(), id = detail.value.id;
  detailController = current;
  try {
    const out = await request<Job>(`/jobs/${id}`, "GET", undefined, current.signal);
    if (!current.signal.aborted && dialog.value === "job" && detail.value?.id === id) detail.value = out;
  } catch (error) {
    if (!current.signal.aborted) throw error;
  } finally {
    if (detailController === current) detailController = undefined;
  }
}
async function refresh() {
  await Promise.all([
    props.page === "artifacts" ? !artifactsLoading.value && artifactList.load()
      : props.page === "instances" ? !instancesLoading.value && instanceList.load()
      : !jobsLoading.value && loadJobs(),
    props.page === "instances" && bindingsExpanded.value && !bindingsLoading.value && bindingList.load(),
    dialog.value === "history" && !releasesLoading.value && releaseList.load(),
    refreshJobDetail(),
  ]);
}
function poll() {
  timer = setTimeout(async () => {
    if (alive && !document.hidden && !busy.value) {
      try {
        await refresh();
      } catch (e) {
        emit("error", e);
      }
    }
    if (alive) poll();
  }, 3000);
}
onMounted(async () => {
  try {
    await Promise.all([refresh(), request<Host[]>("/hosts").then(items => { targets.value = items; })]);
  } catch (e) {
    emit("error", e);
  }
  if (alive) poll();
});
onUnmounted(() => {
  alive = false;
  if (timer) clearTimeout(timer);
  jobsController?.abort();
  detailController?.abort();
  optionsController?.abort();
});
watch(
  () => props.page,
  () => {
    dialog.value = "";
    detail.value = undefined;
    void refresh().catch((e) => emit("error", e));
  },
);
watch(dialog, (value) => {
  detailController?.abort();
  detailController = undefined;
  optionsController?.abort();
  optionsLoading.value = false;
  if (!["instance", "binding", "import", "candidate"].includes(value)) return;
  const current = new AbortController();
  optionsController = current;
  optionsLoading.value = true;
  void (async () => {
    const [c, b, environments, a] = await Promise.all([
      value === "instance" || value === "binding" ? request<Credential[]>("/credentials", "GET", undefined, current.signal) : undefined,
      value === "instance" ? requestAllPages<Binding>("/bindings", current.signal) : undefined,
      value === "import" ? requestAllPages<RuntimeInstallation>("/plugin-installations", current.signal) : undefined,
      value === "candidate" && planKind.value === "PUBLISH" ? requestAllPages<Artifact>("/artifacts", current.signal) : undefined,
    ]);
    if (current.signal.aborted) return;
    if (c) credentials.value = c;
    if (b) bindingOptions.value = b;
    if (environments) runtimeInstallations.value = environments;
    if (a) {
      artifactOptions.value = a;
      if (!eligibleArtifacts.value.some(item => item.id === artifactChoice.value)) artifactChoice.value = eligibleArtifacts.value[0]?.id || "";
    }
  })().catch(error => { if (!current.signal.aborted) emit("error", error); })
    .finally(() => { if (optionsController === current) optionsLoading.value = false; });
});
watch([jobStatusFilter, jobKindFilter], () => {
  if (restoringJobFilters) { restoringJobFilters = false; return; }
  if (props.page !== "artifacts" && props.page !== "instances") void loadJobs(1, jobPageSize.value, [""]).catch(error => emit("error", error));
});
watch([bindings, targets], ([items]) => {
  for (const binding of items) {
    if (!targets.value.some(target => target.id === bindingProbeTargets.value[binding.id])) bindingProbeTargets.value[binding.id] = targets.value.length === 1 ? targets.value[0].id : "";
  }
});
async function toggleBindings(event: Event) {
  bindingsExpanded.value = (event.target as HTMLDetailsElement).open;
  if (!bindingsExpanded.value) return;
  try {
    await Promise.all([bindingList.load(), requestAllPages<Instance>("/instances").then(items => { instanceOptions.value = items; })]);
  } catch (error) { emit("error", error); }
}
function openInstance(i?: Instance) {
  selected.value = i;
  recipe.value = i?.config.preview
    ? "preview-node-v1"
    : i?.config.zlm
      ? "zlm-direct-v1"
      : "java-compose-v1";
  instanceDraft.value = i
    ? {
        name: i.name,
        targetId: i.targetId,
        version: i.version,
        config: JSON.parse(JSON.stringify(i.config)) as Config,
      }
    : {
        name: "",
        targetId: targets.value[0]?.id || "",
        version: "",
        config: configDefault(),
      };
  dialog.value = "instance";
}
async function saveInstance() {
  await act(async () => {
    if (instanceDraft.value.config.zlm) {
      const zlm = instanceDraft.value.config.zlm;
      instanceDraft.value.config.hostPort = zlm.apiPort;
    }
    await request(
      selected.value ? `/instances/${selected.value.id}` : "/instances",
      selected.value ? "PATCH" : "POST",
      instanceDraft.value,
    );
    dialog.value = "";
    await refresh();
    ElMessage.success("草稿已保存");
  });
}
async function saveBinding() {
  await act(async () => {
    await request("/bindings", "POST", { id: "", ...bindingDraft.value });
    dialog.value = "";
    await refresh();
  });
}
async function bindingProbe(b: Binding) {
  await act(async () => {
    const target = targets.value.find(
      (item) => item.id === bindingProbeTargets.value[b.id],
    );
    if (!target) throw new Error("请先选择检查来源主机");
    const instance = instanceOptions.value.find(
      (item) =>
        item.id === bindingProbeInstances.value[b.id] &&
        item.targetId === target.id,
    );
    const out = await request<BindingProbeResult>(
      "/bindings/" + b.id + "/probes",
      "POST",
      {
        targetId: target.id,
        ...(instance
          ? { instanceId: instance.id, releaseId: instance.currentReleaseId }
          : {}),
      },
    );
    bindingProbeResults.value[b.id] = out;
  });
}
async function cancelJob(j: Job) {
  await act(async () => {
    await request(`/jobs/${j.id}/cancel`, "POST");
    await refresh();
  });
}
function pickFile(e: Event) {
  file.value = (e.target as HTMLInputElement).files?.[0];
  if (file.value && !importName.value) importName.value = file.value.name;
}
async function importArtifact() {
  await act(async () => {
    if (!file.value) throw new Error("请选择镜像归档");
    const limit =
      importSource.value === "JAR"
        ? 128
        : importRecipe.value === "java-compose-v1"
          ? 512
          : 2048;
    if (file.value.size > limit * 1024 * 1024)
      throw new Error(`归档上限 ${limit} MiB`);
    uploadPhase.value = "受理导入";
    const accepted = await request<{ jobId: string }>(
      "/artifact-imports",
      "POST",
      {
        name: importName.value,
        recipe: importRecipe.value,
        sourceKind: importSource.value,
        ...(importSource.value === "JAR"
          ? { pluginInstallationId: importRuntime.value }
          : {}),
        archiveSha256: importSHA.value.trim().toLowerCase(),
        byteSize: file.value.size,
        idempotencyKey: crypto.randomUUID(),
      },
    );
    uploadPhase.value = "上传归档";
    await upload(`/jobs/${accepted.jobId}/input`, file.value);
    uploadPhase.value = "提交校验";
    await request(`/jobs/${accepted.jobId}/input/complete`, "POST");
    dialog.value = "";
    await refresh();
    ElMessage.success("上传完成，正在校验");
    uploadPhase.value = "";
  });
}
function choosePublish(i: Instance, kind = "PUBLISH") {
  planInstance.value = i;
  planKind.value = kind;
  maintenancePermit.value = "";
  artifactChoice.value = "";
  plan.value = undefined;
  dialog.value = "candidate";
}
async function preview(kind = planKind.value) {
  await act(async () => {
    if (!planInstance.value) return;
    plan.value = await request<Plan>(
      `/instances/${planInstance.value.id}/plans`,
      "POST",
      {
        kind,
        artifactId: kind === "PUBLISH" ? artifactChoice.value : "",
        maintenancePermit: maintenancePermit.value,
      },
    );
    dialog.value = "plan";
  });
}
async function submit() {
  await act(async () => {
    if (!plan.value) return;
    await request(`/plans/${plan.value.id}/submit`, "POST", {
      idempotencyKey: crypto.randomUUID(),
    });
    dialog.value = "";
    await refresh();
    ElMessage.success("发布作业已受理");
  });
}
async function observe(i: Instance) {
  await act(async () => {
    await request(`/instances/${i.id}/observations`, "POST");
    await refresh();
  });
}
async function history(i: Instance) {
  await act(async () => {
    selected.value = i;
    releasePage.value = 1;
    await releaseList.load();
    dialog.value = "history";
  });
}
async function showLogs(i: Instance) {
  await act(async () => {
    logs.value = (
      await request<{ text: string }>(`/instances/${i.id}/logs`)
    ).text;
    dialog.value = "logs";
  });
}
async function reconcile(j: Job, recover = false) {
  if (recover) {
    try {
      await ElMessageBox.confirm(
        "按本次作业冻结的基线恢复服务，确认继续？",
        "恢复服务",
        { confirmButtonText: "恢复", cancelButtonText: "取消" },
      );
    } catch {
      return;
    }
  }
  await act(async () => {
    await request(`/jobs/${j.id}/${recover ? "recover" : "reconcile"}`, "POST");
    await refresh();
  });
}
</script>

<template>
  <section class="content ops-content panel-card">
    <template v-if="page === 'artifacts'">
      <div class="section-head">
        <h2>不可变镜像制品</h2>
        <button class="secondary" :disabled="busy" @click="dialog = 'import'">
          导入制品
        </button>
      </div>
      <div v-if="!artifacts.length" class="empty">尚未导入制品</div>
      <div v-else class="table-scroll">
        <table>
          <thead>
            <tr>
              <th>名称</th>
              <th>镜像身份</th>
              <th>运行环境</th>
              <th>归档</th>
              <th>材料</th>
            </tr>
          </thead>
          <tbody>
            <tr v-for="a in artifacts" :key="a.id">
              <td>{{ a.name }}</td>
              <td>
                <code :title="a.imageId">{{ a.imageId.slice(0, 24) }}…</code>
              </td>
              <td>
                <template v-if="a.runtimeEnvironment">
                  Java {{ a.runtimeEnvironment.pluginVersion }}
                  <code :title="a.runtimeEnvironment.imageDigest"
                    >{{ a.runtimeEnvironment.imageDigest.slice(0, 19) }}…</code
                  >
                </template>
                <span v-else>镜像自带</span>
              </td>
              <td>
                {{ (a.byteSize / 1048576).toFixed(1) }} MiB
                <code :title="a.archiveSha256">{{
                  a.archiveSha256.slice(0, 12)
                }}</code>
              </td>
              <td>
                <span
                  class="state-tag"
                  :class="a.available ? 'state-success' : 'state-error'"
                  >{{ a.available ? "可用" : "缺失" }}</span
                >
              </td>
            </tr>
          </tbody>
        </table>
      </div>
      <OpsPagination :page="artifactPage" :page-size="artifactPageSize" :total="artifactTotal" :disabled="artifactsLoading" label="制品分页" @update:page="artifactList.changePage" @update:page-size="artifactList.changeSize" />
    </template>
    <template v-else-if="page === 'instances'">
      <div class="section-head">
        <h2>服务实例</h2>
        <button
          class="secondary"
          :disabled="busy || !targets.length"
          @click="openInstance()"
        >
          新增实例
        </button>
      </div>
      <div v-if="!instances.length" class="empty">尚未创建实例</div>
      <div v-else class="table-scroll">
        <table>
          <thead>
            <tr>
              <th>实例 / 环境</th>
              <th>目标 / 端口</th>
              <th>当前 / 上一成功</th>
              <th>实际观察</th>
              <th>操作</th>
            </tr>
          </thead>
          <tbody>
            <tr v-for="i in instances" :key="i.id">
              <td>
                {{ i.name
                }}<small
                  >{{ i.config.environment }} · 草稿 r{{
                    i.draftRevision
                  }}</small
                >
              </td>
              <td>
                {{ targets.find((t) => t.id === i.targetId)?.name
                }}<small
                  >{{ i.config.bindAddress }}:{{ i.config.hostPort }}</small
                >
              </td>
              <td>
                <code
                  >{{ short(i.currentReleaseId) }} /
                  {{ short(i.previousReleaseId) }}</code
                ><small>已应用 {{ i.appliedGeneration }}</small>
              </td>
              <td>
                <span
                  class="state-tag"
                  :class="
                    fresh(i.observedAt) &&
                    i.observation.health === 'APPLICATION_READY'
                      ? 'state-success'
                      : 'state-muted'
                  "
                  >{{
                    fresh(i.observedAt)
                      ? i.observation.health === "APPLICATION_READY"
                        ? "应用就绪"
                        : instanceHealthLabel(i.observation.health)
                      : "待观察"
                  }}</span
                ><small>{{ time(i.observedAt) }}</small>
                <small v-if="i.observation.preview"
                  >{{
                    i.observation.preview.state === "MAINTENANCE"
                      ? "维护封闭中"
                      : i.observation.preview.state
                  }}
                  · {{ i.observation.preview.maxSessions }}/{{
                    i.observation.preview.maxTranscodes
                  }}</small
                >
              </td>
              <td class="actions">
                <template v-if="!i.config.preview && !i.config.zlm">
                  <button class="quiet" :disabled="busy || !i.currentReleaseId" @click="i.pendingJob ? showInstanceJob(i) : openRuntimePolicy(i)">{{ i.pendingJob ? instanceProgressLabel(i) : '运行管理' }}</button>
                </template>
                <button class="quiet" :disabled="busy" @click="openInstance(i)">
                  配置</button
                ><button
                  class="quiet"
                  :disabled="busy || !!i.pendingJob"
                  @click="choosePublish(i)"
                >
                  发布</button
                ><button
                  class="quiet"
                  :disabled="busy || !!i.pendingJob || !i.previousReleaseId"
                  @click="choosePublish(i, 'ROLLBACK')"
                >
                  回滚</button
                ><button
                  class="quiet"
                  :disabled="busy || !i.currentReleaseId"
                  @click="observe(i)"
                >
                  观察</button
                ><button class="quiet" :disabled="busy" @click="history(i)">
                  版本</button
                ><button
                  class="quiet"
                  :disabled="busy || !i.currentReleaseId"
                  @click="showLogs(i)"
                >
                  日志
                </button>
                <button
                  v-if="i.config.preview"
                  class="quiet"
                  :disabled="busy || !i.currentReleaseId"
                  @click="showRegistration(i)"
                >
                  平台登记指引
                </button>
              </td>
            </tr>
          </tbody>
        </table>
      </div>
      <OpsPagination :page="instancePage" :page-size="instancePageSize" :total="instanceTotal" :disabled="instancesLoading" label="实例分页" @update:page="instanceList.changePage" @update:page-size="instanceList.changeSize" />
      <OpsExternalBindingsSection
        :busy="busy"
        :bindings="bindings"
        :targets="targets"
        :instance-options="instanceOptions"
        :binding-probe-targets="bindingProbeTargets"
        :binding-probe-instances="bindingProbeInstances"
        :binding-probe-results="bindingProbeResults"
        :binding-page="bindingPage"
        :binding-page-size="bindingPageSize"
        :binding-total="bindingTotal"
        :bindings-loading="bindingsLoading"
        :time="time"
        @toggle="toggleBindings"
        @open-create-binding="dialog = 'binding'"
        @probe="bindingProbe"
        @change-page="bindingList.changePage"
        @change-size="bindingList.changeSize"
      />
    </template>
    <OpsJobsSection
      v-else
      v-model:job-status-filter="jobStatusFilter"
      v-model:job-kind-filter="jobKindFilter"
      :busy="busy"
      :jobs="jobs"
      :status-labels="statusLabels"
      :job-kind-labels="jobKindLabels"
      :phase-labels="phaseLabels"
      :job-page="jobPage"
      :job-page-size="jobPageSize"
      :job-next-cursor="jobNextCursor"
      :jobs-loading="jobsLoading"
      :job-status="jobStatus"
      :time="time"
      :short="short"
      @refresh="act(refresh)"
      @open-detail="
        detail = $event;
        dialog = 'job';
      "
      @cancel-job="cancelJob"
      @reconcile="(job, recover) => reconcile(job, recover)"
      @change-page="changeJobPage"
      @change-size="changeJobSize"
    />
  </section>
  <OpsWorkspaceDialogs
    :dialog="dialog"
    :busy="busy"
    :options-loading="optionsLoading"
    :binding-draft="bindingDraft"
    :credential-options="credentialOptions"
    :recipe="recipe"
    :selected="selected"
    :instance-draft="instanceDraft"
    :targets="targets"
    :binding-options="bindingOptions"
    :runtime-policy-draft="runtimePolicyDraft"
    :runtime-policy-dirty="runtimePolicyDirty"
    :runtime-policy-attempted="runtimePolicyAttempted"
    :runtime-policy-error-message="runtimePolicyErrorMessage"
    :import-source="importSource"
    :import-recipe="importRecipe"
    :import-runtime="importRuntime"
    :import-name="importName"
    :import-s-h-a="importSHA"
    :upload-phase="uploadPhase"
    :runtime-installations="runtimeInstallations"
    :plan-kind="planKind"
    :artifact-choice="artifactChoice"
    :eligible-artifacts="eligibleArtifacts"
    :plan-instance="planInstance"
    :maintenance-permit="maintenancePermit"
    :plan="plan"
    :artifact-options="artifactOptions"
    :releases="releases"
    :release-page="releasePage"
    :release-page-size="releasePageSize"
    :release-total="releaseTotal"
    :releases-loading="releasesLoading"
    :registration="registration"
    :logs="logs"
    :detail="detail"
    :status-labels="statusLabels"
    :phase-labels="phaseLabels"
    @close="dialog = ''"
    @save-binding="saveBinding"
    @save-instance="saveInstance"
    @recipe-changed="recipeChanged"
    @update:recipe="recipe = $event"
    @update:import-source="
      importSource = $event;
      file = undefined;
    "
    @update:import-recipe="importRecipe = $event"
    @update:import-runtime="importRuntime = $event"
    @update:import-name="importName = $event"
    @update:import-s-h-a="importSHA = $event"
    @update:artifact-choice="artifactChoice = $event"
    @update:maintenance-permit="maintenancePermit = $event"
    @pick-file="pickFile"
    @import-artifact="importArtifact"
    @apply-runtime-policy="applyRuntimePolicy"
    @edit-runtime-policy="editRuntimePolicy"
    @open-runtime-policy="openRuntimePolicy"
    @instance-action="instanceAction"
    @preview="preview()"
    @submit="submit"
    @change-release-page="releaseList.changePage"
    @change-release-size="releaseList.changeSize"
    @reconcile="reconcile($event)"
    @cancel-job="cancelJob"
  />
</template>
