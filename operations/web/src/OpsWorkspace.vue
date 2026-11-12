<script setup lang="ts">
import { ref, onMounted, onUnmounted, watch, computed } from "vue";
import { ElDialog, ElMessage, ElMessageBox } from "element-plus";
import { ApiError, request, upload } from "./api";
import { jobKindLabels as softwareJobKindLabels, type PageResult } from "./jobs";
import OpsPagination from "./OpsPagination.vue";
import { requestAllPages } from "./listPages";
import PreviewConfigForm from "./PreviewConfigForm.vue";
import { previewDefaults, type PreviewConfig } from "./preview";
import ZLMConfigForm from "./ZLMConfigForm.vue";
import { zlmDefaults, type ZLMConfig } from "./zlm";
import type { Host } from "./hosts";
import RuntimePolicyFields from "./RuntimePolicyFields.vue";
import { runtimePolicyError, runtimePolicySummary, type RuntimePolicy, type RuntimePolicyState } from "./runtimePolicy";
import { softwareJobErrorMessages, softwareJobPhaseLabels, softwareJobDiagnostic } from "./software";
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
function initialPasswordSelected(e: Event) {
  const value = (e.target as HTMLSelectElement).value;
  const [id, revision] = value ? value.split(":") : ["", ""];
  instanceDraft.value.config.initialPasswordCredentialId = id;
  instanceDraft.value.config.initialPasswordRevision = revision;
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
function credentialSelected(
  d: { credentialId: string; credentialRevision: string },
  e: Event,
) {
  const key = (e.target as HTMLSelectElement).value;
  [d.credentialId, d.credentialRevision] = key ? key.split(":") : ["", ""];
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
      <details class="dependency-section" @toggle="toggleBindings">
        <summary>外部依赖连接</summary>
        <div class="section-head">
          <h2>外部依赖</h2>
          <button
            class="secondary"
            :disabled="busy"
            @click="dialog = 'binding'"
          >
            绑定已有服务
          </button>
        </div>
        <div v-if="!bindings.length" class="empty">尚未登记外部依赖</div>
        <div v-else class="table-scroll">
          <table>
            <thead>
              <tr>
                <th>类型</th>
                <th>地址</th>
                <th>数据库</th>
                <th>检查来源</th>
                <th>操作</th>
              </tr>
            </thead>
            <tbody>
              <tr v-for="b in bindings" :key="b.id">
                <td>{{ b.kind }}</td>
                <td>{{ b.host }}:{{ b.port }}</td>
                <td>{{ b.database || "—" }}</td>
                <td>
                  <div class="probe-controls">
                    <select
                      v-model="bindingProbeTargets[b.id]"
                      :aria-label="`检查来源：${b.host}`"
                      @change="
                        bindingProbeInstances[b.id] = '';
                        delete bindingProbeResults[b.id];
                      "
                    >
                      <option value="">选择主机</option>
                      <option v-for="t in targets" :key="t.id" :value="t.id">
                        {{ t.name }} · {{ t.address }}
                      </option>
                    </select>
                    <select
                      v-model="bindingProbeInstances[b.id]"
                      :aria-label="`实例网络：${b.host}`"
                      @change="delete bindingProbeResults[b.id]"
                    >
                      <option value="">仅检查主机 TCP</option>
                      <option
                        v-for="i in instanceOptions.filter(
                          (item) =>
                            item.targetId === bindingProbeTargets[b.id] &&
                            item.currentReleaseId &&
                            !item.config.preview &&
                            !item.config.zlm,
                        )"
                        :key="i.id"
                        :value="i.id"
                      >
                        {{ i.name }} · 当前已发布版本
                      </option>
                    </select>
                  </div>
                </td>
                <td>
                  <button
                    class="quiet"
                    :disabled="busy || !bindingProbeTargets[b.id]"
                    @click="bindingProbe(b)"
                  >
                    {{
                      bindingProbeInstances[b.id] ? "检查实例连接" : "TCP 检查"
                    }}
                  </button>
                  <details
                    v-if="bindingProbeResults[b.id]"
                    class="probe-result"
                  >
                    <summary>
                      {{
                        bindingProbeResults[b.id].authentication?.authenticated
                          ? "认证通过"
                          : bindingProbeResults[b.id].authentication
                              ?.errorCode ||
                            bindingProbeResults[b.id].errorCode ||
                            "查看检查结果"
                      }}
                    </summary>
                    <dl>
                      <dt>控制器 TCP</dt>
                      <dd>
                        {{
                          bindingProbeResults[b.id].controller.reachable
                            ? "可达"
                            : "不可达"
                        }}
                      </dd>
                      <dt>目标主机 TCP</dt>
                      <dd>
                        {{
                          !bindingProbeResults[b.id].targetHost.executed
                            ? "未执行"
                            : bindingProbeResults[b.id].targetHost.reachable
                              ? "可达"
                              : "不可达"
                        }}
                      </dd>
                      <dt>实例网络</dt>
                      <dd>
                        {{
                          !bindingProbeResults[b.id].applicationNetwork
                            ? "未验证"
                            : bindingProbeResults[b.id].applicationNetwork
                                  ?.reachable
                              ? "可达"
                              : "不可达"
                        }}
                      </dd>
                      <dt>最小认证</dt>
                      <dd>
                        {{
                          !bindingProbeResults[b.id].authentication?.executed
                            ? "未验证"
                            : bindingProbeResults[b.id].authentication
                                  ?.authenticated
                              ? "通过"
                              : bindingProbeResults[b.id].authentication
                                  ?.errorCode || "未通过"
                        }}
                      </dd>
                      <template v-if="bindingProbeResults[b.id].releaseId"
                        ><dt>冻结版本</dt>
                        <dd>
                          <code>{{ bindingProbeResults[b.id].releaseId }}</code>
                        </dd></template
                      >
                      <dt>检查时间</dt>
                      <dd>{{ time(bindingProbeResults[b.id].observedAt) }}</dd>
                    </dl>
                    <small
                      v-if="
                        bindingProbeResults[b.id].authentication?.errorCode ===
                        'AUTH_TLS_REQUIRED'
                      "
                      >该 MySQL 账号需要 TLS
                      full-auth，当前探针未支持；没有发送明文密码，结果不代表密码错误或数据库不可用。</small
                    >
                    <small
                      v-else-if="
                        bindingProbeResults[b.id].errorCode ||
                        bindingProbeResults[b.id].authentication?.errorCode
                      "
                      >核对目标执行器能力、实例当前版本与原作业/ACK；认证错误请检查所选凭据修订，不要以
                      TCP 可达判断密码正确。</small
                    >
                  </details>
                </td>
              </tr>
            </tbody>
          </table>
        </div>
        <OpsPagination :page="bindingPage" :page-size="bindingPageSize" :total="bindingTotal" :disabled="bindingsLoading" label="外部依赖分页" @update:page="bindingList.changePage" @update:page-size="bindingList.changeSize" />
      </details>
    </template>
    <template v-else>
      <div class="section-head">
        <h2>作业记录</h2>
        <div class="job-filters">
          <select v-model="jobStatusFilter" aria-label="筛选作业状态">
            <option value="">全部状态</option>
            <option
              v-for="(label, code) in statusLabels"
              :key="code"
              :value="code"
            >
              {{ label }}
            </option>
          </select>
          <select v-model="jobKindFilter" aria-label="筛选作业类型">
            <option value="">全部类型</option>
            <option
              v-for="(label, code) in jobKindLabels"
              :key="code"
              :value="code"
            >
              {{ label }}
            </option>
          </select>
          <button class="secondary" :disabled="busy" @click="act(refresh)">
            刷新
          </button>
        </div>
      </div>
      <div v-if="!jobs.length" class="empty">暂无作业</div>
      <div v-else class="table-scroll">
        <table>
          <thead>
            <tr>
              <th>时间 / 作业</th>
              <th>类型</th>
              <th>阶段</th>
              <th>状态</th>
              <th>操作</th>
            </tr>
          </thead>
          <tbody>
            <tr v-for="j in jobs" :key="j.id">
              <td>
                {{ time(j.createdAt) }}<small>{{ short(j.id) }}</small>
              </td>
              <td>
                {{ jobKindLabels[j.kind] || '其他操作' }}
              </td>
              <td>
                {{ phaseLabels[j.phase] || '阶段待确认'
                }}<small v-if="j.targetAckPending">目标确认收尾中</small>
              </td>
              <td>
                <span
                  class="state-tag"
                  :class="
                    j.status === 'SUCCEEDED'
                      ? 'state-success'
                      : jobStatus(j) === 'FAILED'
                        ? 'state-error'
                        : 'state-muted'
                  "
                  >{{ statusLabels[jobStatus(j)] || '状态待确认' }}</span
                >
              </td>
              <td>
                <button
                  class="quiet"
                  @click="
                    detail = j;
                    dialog = 'job';
                  "
                >
                  详情</button
                ><button
                  v-if="
                    j.status === 'QUEUED' &&
                    j.phase !== 'UPLOADING' &&
                    !j.targetAckPending
                  "
                  class="quiet"
                  :disabled="busy"
                  @click="cancelJob(j)"
                >
                  取消排队</button
                ><button
                  v-if="j.status === 'UNKNOWN' || j.targetAckPending"
                  class="quiet"
                  :disabled="busy"
                  @click="reconcile(j)"
                >
                  核对</button
                ><button
                  v-if="
                    j.status === 'UNKNOWN' &&
                    ['PUBLISH', 'ROLLBACK'].includes(j.kind)
                  "
                  class="quiet danger"
                  :disabled="busy"
                  @click="reconcile(j, true)"
                >
                  恢复
                </button>
              </td>
            </tr>
          </tbody>
        </table>
      </div>
      <OpsPagination :page="jobPage" :page-size="jobPageSize" :has-next="!!jobNextCursor" :disabled="jobsLoading" label="作业分页" @update:page="changeJobPage" @update:page-size="changeJobSize" />
    </template>
  </section>
  <ElDialog
    :model-value="!!dialog"
    :title="
      (
        {
          binding: '绑定已有依赖',
          instance: '实例配置草稿',
          'runtime-policy': '应用运行管理',
          registration: '预览节点登记与维护',
          import: '导入制品',
          candidate: '选择候选',
          plan: '发布计划',
          history: '版本记录',
          logs: '实例日志',
          job: '作业详情',
        } as Record<string, string>
      )[dialog]
    "
    width="min(640px,94vw)"
    align-center
    @close="dialog = ''"
  >
    <form
      v-if="dialog === 'binding'"
      class="dialog-form"
      @submit.prevent="saveBinding"
    >
      <div class="form-grid">
        <label
          >类型<select
            v-model="bindingDraft.kind"
            aria-label="类型"
            @change="
              bindingDraft.port = bindingDraft.kind === 'MYSQL' ? 3306 : 6379
            "
          >
            <option>MYSQL</option>
            <option>REDIS</option>
          </select></label
        ><label
          >端口<input v-model.number="bindingDraft.port" type="number" required
        /></label>
      </div>
      <label>地址<input v-model="bindingDraft.host" required /></label>
      <div class="form-grid">
        <label>数据库<input v-model="bindingDraft.database" /></label
        ><label>用户<input v-model="bindingDraft.username" /></label>
      </div>
      <label
        >密码修订<select @change="credentialSelected(bindingDraft, $event)">
          <option value="">无密码</option>
          <option
            v-for="c in credentialOptions.filter((c) => c.kind === 'PASSWORD')"
            :key="c.id + c.revision"
            :value="c.id + ':' + c.revision"
          >
            {{ c.name }} r{{ c.revision }}
          </option>
        </select></label
      >
      <div class="dialog-footer">
        <button
          type="button"
          class="secondary"
          :disabled="busy"
          @click="dialog = ''"
        >
          取消
        </button>
        <button class="primary" :disabled="busy">登记绑定</button>
      </div>
    </form>
    <form
      v-else-if="dialog === 'instance'"
      class="dialog-form"
      @submit.prevent="saveInstance"
    >
      <label
        >部署配方<select
          v-model="recipe"
          :disabled="!!selected"
          @change="recipeChanged"
        >
          <option value="java-compose-v1">Java 服务</option>
          <option value="preview-node-v1">预览节点 · ZLM / FFmpeg</option>
          <option value="zlm-direct-v1">独立 ZLM</option>
        </select></label
      >
      <label>实例名称<input v-model="instanceDraft.name" required /></label
      ><label
        >目标<select
          v-model="instanceDraft.targetId"
          :disabled="!!selected"
          required
        >
          <option v-for="t in targets" :key="t.id" :value="t.id">
            {{ t.name }}
          </option>
        </select></label
      >
      <template v-if="instanceDraft.config.zlm">
        <div class="form-grid">
          <label
            >目标绑定 IPv4<input
              v-model="instanceDraft.config.bindAddress"
              :disabled="!!selected?.currentReleaseId"
              required
          /></label>
          <label
            >内存 MiB<input
              v-model.number="instanceDraft.config.memoryMiB"
              type="number"
              min="512"
              max="65536"
              required
          /></label>
        </div>
        <ZLMConfigForm
          v-model="instanceDraft.config.zlm"
          :credentials="credentialOptions"
          :deployed="!!selected?.currentReleaseId"
        />
      </template>
      <template v-else-if="instanceDraft.config.preview">
        <div class="form-grid">
          <label
            >目标绑定 IPv4<input
              v-model="instanceDraft.config.bindAddress"
              :disabled="!!selected?.currentReleaseId"
              required /></label
          ><label
            >内存 MiB<input
              v-model.number="instanceDraft.config.memoryMiB"
              type="number"
              min="512"
              max="65536"
              required
          /></label>
        </div>
        <PreviewConfigForm
          v-model="instanceDraft.config.preview"
          :deployed="!!selected?.currentReleaseId"
        />
      </template>
      <template v-else>
        <div class="form-grid">
          <label
            >环境标签<input
              v-model="instanceDraft.config.environment"
              required /></label
          ><label
            >Spring profile<input
              v-model="instanceDraft.config.profile" /></label
          ><label
            >绑定地址<input
              v-model="instanceDraft.config.bindAddress"
              required /></label
          ><label
            >宿主端口<input
              v-model.number="instanceDraft.config.hostPort"
              type="number"
              min="1024"
              max="65535"
              required /></label
          ><label
            >容器端口<input
              v-model.number="instanceDraft.config.containerPort"
              type="number"
              min="1024"
              max="65535"
              required /></label
          ><label
            >内存 MiB<input
              v-model.number="instanceDraft.config.memoryMiB"
              type="number"
              min="256"
              max="8192"
              required
          /></label>
        </div>
        <label
          >外部依赖<select v-model="instanceDraft.config.bindingIds" multiple>
            <option v-for="b in bindingOptions" :key="b.id" :value="b.id">
              {{ b.kind }} · {{ b.host }}:{{ b.port }}
            </option>
          </select></label
        >
        <details>
          <summary>健康与恢复</summary>
          <div class="form-grid">
            <label
              >应用初始密码修订<select
                :value="
                  instanceDraft.config.initialPasswordCredentialId
                    ? instanceDraft.config.initialPasswordCredentialId +
                      ':' +
                      instanceDraft.config.initialPasswordRevision
                    : ''
                "
                @change="initialPasswordSelected"
              >
                <option value="">不注入</option>
                <option
                  v-for="c in credentialOptions.filter(
                    (c) => c.kind === 'PASSWORD',
                  )"
                  :key="c.id + c.revision"
                  :value="c.id + ':' + c.revision"
                >
                  {{ c.name }} r{{ c.revision }}
                </option>
              </select></label
            >
            <label
              >健康路径<input
                v-model="instanceDraft.config.healthPath"
                required /></label
            ><label
              >健康超时 秒<input
                v-model.number="instanceDraft.config.healthSeconds"
                type="number"
                min="5"
                max="120"
                required /></label
            ><label
              >数据兼容标识<input
                v-model="instanceDraft.config.dataCompatibility"
                required
            /></label>
          </div>
        </details>
      </template>
      <RuntimePolicyFields v-if="!instanceDraft.config.preview && !instanceDraft.config.zlm" v-model="instanceDraft.config.runtimePolicy" kind="DOCKER" :disabled="busy" optional inherit-label="沿用当前实例策略" />
      <div class="dialog-footer">
        <button
          type="button"
          class="secondary"
          :disabled="busy"
          @click="dialog = ''"
        >
          取消
        </button>
        <button class="primary" :disabled="busy">保存草稿</button>
      </div>
    </form>
    <form v-else-if="dialog === 'runtime-policy' && selected" class="dialog-form" @submit.prevent="applyRuntimePolicy">
      <div class="runtime-actions"><strong>{{ selected.name }}</strong><span class="spacer"></span><button type="button" class="secondary" :disabled="busy || runtimePolicyDirty" @click="instanceAction(selected, 'START')">启动</button><button type="button" class="secondary" :disabled="busy || runtimePolicyDirty" @click="instanceAction(selected, 'STOP')">停止</button><button type="button" class="secondary" :disabled="busy || runtimePolicyDirty" @click="instanceAction(selected, 'RESTART')">重启</button></div>
      <p v-if="selected.observation.runtimePolicy?.held">已保持停止，修改策略后仍保持停止。</p>
      <p v-else>{{ runtimePolicySummary(selected.observation.runtimePolicy?.actual, 'DOCKER') }}</p>
      <RuntimePolicyFields v-if="runtimePolicyDraft" :model-value="runtimePolicyDraft" kind="DOCKER" :stop-timeout-editable="false" :disabled="busy || runtimePolicyAttempted || !selected.observation.runtimePolicy?.editable" @update:model-value="editRuntimePolicy" />
      <p v-else class="muted">{{ busy ? '正在读取当前策略…' : '尚未确认运行策略，请重新读取' }}</p>
      <p v-if="selected.observation.runtimePolicy && !selected.observation.runtimePolicy.editable" class="muted">当前运行对象不支持修改策略</p>
      <p v-if="runtimePolicyErrorMessage" class="error" role="alert">{{ runtimePolicyErrorMessage }}</p>
      <div class="dialog-footer"><button type="button" class="secondary" :disabled="busy" @click="dialog = ''">关闭</button><button type="button" class="secondary" :disabled="busy || runtimePolicyAttempted" @click="openRuntimePolicy(selected)">重新读取</button><button class="primary" :disabled="busy || !runtimePolicyDirty || !selected.observation.runtimePolicy?.editable">{{ runtimePolicyAttempted ? '重试原请求' : '应用策略' }}</button></div>
    </form>
    <form
      v-else-if="dialog === 'import'"
      class="dialog-form"
      @submit.prevent="importArtifact"
    >
      <label
        >来源<select
          v-model="importSource"
          aria-label="来源"
          @change="
            importRecipe =
              importSource === 'JAR' ? 'java-compose-v1' : importRecipe;
            file = undefined;
            importSHA = '';
          "
        >
          <option value="DOCKER_ARCHIVE">完整 Docker 镜像</option>
          <option value="JAR">可执行 JAR + 已安装 Java21</option>
        </select></label
      >
      <label
        >配方<select
          v-model="importRecipe"
          aria-label="配方"
          :disabled="importSource === 'JAR'"
        >
          <option value="java-compose-v1">Java 服务</option>
          <option value="preview-node-v1">预览节点</option>
          <option value="zlm-direct-v1">独立 ZLM</option>
        </select></label
      >
      <label v-if="importSource === 'JAR'"
        >Java21 运行环境<select
          v-model="importRuntime"
          aria-label="Java21 运行环境"
          required
        >
          <option value="">选择已校验就绪环境</option>
          <option
            v-for="runtime in runtimeInstallations.filter(
              (item) =>
                item.pluginKey === 'java-runtime' &&
                item.pluginVersion === '21-jre' &&
                item.status === 'READY',
            )"
            :key="runtime.id"
            :value="runtime.id"
          >
            {{
              targets.find((target) => target.id === runtime.targetId)?.name ||
              runtime.targetId
            }}
            · Java21
          </option>
        </select></label
      >
      <label>名称<input v-model="importName" required /></label
      ><label
        >{{ importSource === "JAR" ? "可执行 JAR" : "Docker save 归档"
        }}<input
          type="file"
          :accept="importSource === 'JAR' ? '.jar' : '.tar'"
          required
          @change="pickFile" /></label
      ><label
        >预期 SHA-256<input
          v-model="importSHA"
          required
          pattern="[a-fA-F0-9]{64}"
          spellcheck="false" /></label
      ><small>{{
        importSource === "JAR"
          ? "最大128MiB；验证SHA-256、Main-Class和Java21字节码边界。只添加固定JAR层，不执行上传代码；封装结果冻结运行环境，完整镜像不能替换JRE。"
          : importRecipe === "zlm-direct-v1"
            ? "选择独立 ZLM 镜像归档，使用可信 SHA-256；单镜像 linux/amd64，最大 2GiB。"
            : importRecipe === "preview-node-v1"
              ? "选择 S4 节点包内 preview-node-image.tar，使用该文件的可信 SHA-256；最大 2GiB，须支持维护封闭。"
              : "单镜像 linux/amd64，标签以 sfops/ 开头，最大512MiB。"
      }}</small>
      <div class="dialog-footer">
        <button
          type="button"
          class="secondary"
          :disabled="busy"
          @click="dialog = ''"
        >
          取消
        </button>
        <button class="primary" :disabled="busy">
          {{ uploadPhase || "上传并校验" }}
        </button>
      </div>
    </form>
    <form
      v-else-if="dialog === 'candidate'"
      class="dialog-form"
      @submit.prevent="preview()"
    >
      <p v-if="planKind === 'ROLLBACK'">回退到上一成功版本，恢复其冻结配置。</p>
      <label v-if="planKind === 'PUBLISH'"
        >候选制品<select v-model="artifactChoice" required>
          <option v-for="a in eligibleArtifacts" :key="a.id" :value="a.id">
            {{ a.name }}
          </option>
        </select></label
      >
      <template
        v-if="planInstance?.config.preview && planInstance.currentReleaseId"
      >
        <p>
          在平台“服务信息 →
          预览执行节点”停止接新预览；资源归零后获取维护许可。作业完成后，节点仍保持排空。
        </p>
        <label
          >维护许可编号<input
            v-model="maintenancePermit"
            required
            pattern="[a-f0-9-]{36}"
            autocomplete="off"
        /></label>
      </template>
      <div class="dialog-footer">
        <button
          type="button"
          class="secondary"
          :disabled="busy"
          @click="dialog = ''"
        >
          取消
        </button>
        <button class="primary" :disabled="busy || optionsLoading">生成预检计划</button>
      </div>
    </form>
    <div v-else-if="dialog === 'plan' && plan" class="dialog-form">
      <dl class="plan-detail">
        <dt>操作</dt>
        <dd>
          {{ plan.kind === "ROLLBACK" ? "回滚" : "发布" }} ·
          {{ planInstance?.name }}
        </dd>
        <dt>版本来源</dt>
        <dd>{{ artifactOptions.find((item) => item.id === plan?.candidate.artifactId)?.name || '所选制品' }}</dd>
        <dt>失败处理</dt><dd>{{ plan.baseline ? '恢复上一成功版本' : '停止未成功的候选' }}</dd>
        <template v-if="plan.candidate.runtimeEnvironment">
          <dt>冻结运行环境</dt>
          <dd>
            Java {{ plan.candidate.runtimeEnvironment.pluginVersion }}
          </dd>
        </template>
        <dt>端口</dt>
        <dd>
          {{ plan.candidate.config.bindAddress }}:{{
            plan.candidate.config.hostPort
          }}
          → {{ plan.candidate.config.containerPort }}
        </dd>
        <dt>健康检查</dt>
        <dd>
          {{ plan.candidate.config.healthPath }} ·
          {{ plan.candidate.config.healthSeconds }}秒
        </dd>
        <dt>影响</dt>
        <dd>
          {{
            plan.candidate.config.preview
              ? "只替换此预览节点；已有资源必须清零。失败恢复上一成功版本，作业完成后需到平台检查并明确启用。"
              : "替换该实例，发布期间会短暂中断；数据库不随应用回滚。"
          }}
        </dd>
        <template v-if="plan.maintenancePermit"
          ><dt>维护许可</dt>
          <dd>
            <code>{{ plan.maintenancePermit }}</code>
          </dd></template
        >
        <dt>有效期</dt>
        <dd>{{ time(plan.expiresAt) }}</dd>
      </dl>
      <details><summary>版本技术详情</summary><dl class="plan-detail"><dt>候选</dt><dd><code>{{ plan.candidate.id }}</code></dd><dt>镜像</dt><dd><code>{{ plan.candidate.imageId }}</code></dd><dt>恢复基线</dt><dd><code>{{ plan.baseline?.id || "停止未成功的候选" }}</code></dd><template v-if="plan.candidate.runtimeEnvironment"><dt>运行环境摘要</dt><dd><code>{{ plan.candidate.runtimeEnvironment.imageDigest }}</code></dd></template></dl></details>
      <div class="dialog-footer">
        <button
          type="button"
          class="secondary"
          :disabled="busy"
          @click="dialog = ''"
        >
          取消
        </button>
        <button class="primary" :disabled="busy" @click="submit">
          确认执行
        </button>
      </div>
    </div>
    <div v-else-if="dialog === 'history'" class="version-list">
      <article v-for="r in releases" :key="r.id">
        <strong>{{
          r.id === selected?.currentReleaseId
            ? "当前成功"
            : r.id === selected?.previousReleaseId
              ? "上一成功"
              : "历史候选"
        }}</strong
        ><span v-if="r.runtimeEnvironment">Java {{ r.runtimeEnvironment.pluginVersion }}</span>
        <details><summary>版本详情</summary><code>{{ r.id }}</code><small>{{ r.imageId }}</small><code v-if="r.runtimeEnvironment">{{ r.runtimeEnvironment.imageDigest }}</code></details>
        <details v-if="r.config.preview">
          <summary>冻结的节点配置</summary>
          <pre class="log-output">{{ JSON.stringify(r.config, null, 2) }}</pre>
        </details>
      </article>
      <div v-if="!releases.length" class="empty">暂无版本</div>
      <OpsPagination :page="releasePage" :page-size="releasePageSize" :total="releaseTotal" :disabled="releasesLoading" label="发布版本分页" @update:page="releaseList.changePage" @update:page-size="releaseList.changeSize" />
    </div>
    <div v-else-if="dialog === 'registration'" class="dialog-form">
      <p>
        部署就绪后，将目标机上的登记文件和机器凭据安全转交平台管理员，导入节点；浏览器预览验证与明确启用在平台进行。
      </p>
      <pre class="log-output">{{ JSON.stringify(registration, null, 2) }}</pre>
      <p>
        升级或回退：平台排空 → 获取维护许可 → Ops 预检执行 → 核对作业及版本 →
        平台检查 / 重新登记 → 解除维护 → 参与调度。
      </p>
    </div>
    <pre v-else-if="dialog === 'logs'" class="log-output">{{
      logs || "暂无日志"
    }}</pre>
    <div v-else-if="dialog === 'job' && detail" class="dialog-form">
      <div v-if="detail.status === 'UNKNOWN'" class="error">
        结果尚未确认，相关变更已锁定
      </div>
      <p v-if="detail.errorCode" class="error">{{ softwareJobErrorMessages[detail.errorCode] || '操作未完成，请核对原操作和执行日志。' }}</p>
      <dl class="plan-detail">
        <dt>状态</dt>
        <dd>
          {{ statusLabels[jobStatus(detail)] }} ·
          {{ phaseLabels[detail.phase] || '阶段待确认' }}
        </dd>
        <dt>时间</dt>
        <dd>{{ time(detail.updatedAt) }}</dd>
      </dl>
      <div class="runtime-actions">
        <button v-if="detail.status === 'UNKNOWN' || detail.targetAckPending" class="secondary" :disabled="busy" @click="reconcile(detail)">核对原操作</button>
        <button v-if="detail.status === 'QUEUED' && detail.phase !== 'UPLOADING' && !detail.targetAckPending" class="quiet" :disabled="busy" @click="cancelJob(detail)">取消排队</button>
      </div>
      <details><summary>诊断信息</summary><dl class="plan-detail">
        <dt>作业</dt><dd><code>{{ detail.id }}</code></dd>
        <dt>状态 / 阶段</dt><dd><code>{{ softwareJobDiagnostic(detail.status, detail.phase, phaseLabels) }}</code></dd>
        <dt>错误码</dt><dd>{{ detail.errorCode || "—" }}</dd>
        <dt>追踪</dt>
        <dd>
          <code>{{ detail.traceId }}</code>
        </dd>
        <dt>结果</dt>
        <dd>
          <pre>{{ JSON.stringify(detail.result, null, 2) }}</pre>
        </dd>
      </dl></details>
    </div>
  </ElDialog>
</template>

<style scoped>
.ops-content small {
  display: block;
  color: #70839a;
  font-size: 12px;
  margin-top: 6px;
}
.actions {
  min-width: 180px;
}
.actions button {
  padding: 5px;
}
.runtime-actions{display:flex;align-items:center;gap:10px;flex-wrap:wrap;}
.runtime-actions strong{overflow-wrap:anywhere;}
.runtime-actions .spacer{flex:1;}
.probe-controls {
  display: flex;
  gap: 8px;
  flex-wrap: wrap;
}
.probe-controls select {
  margin: 0;
  width: auto;
  min-width: 140px;
  height: 34px;
  padding: 4px 10px;
  font-size: 12px;
}
.job-filters {
  display: flex;
  align-items: center;
  gap: 10px;
  flex-wrap: wrap;
}
.job-filters select {
  margin: 0;
  width: 132px;
  height: 36px;
  font-size: 13px;
}
.form-grid {
  display: grid;
  grid-template-columns: 1fr 1fr;
  gap: 14px;
}
.plan-detail {
  display: grid;
  grid-template-columns: 90px minmax(0, 1fr);
  gap: 14px;
  margin: 0;
}
.plan-detail dt {
  color: #6b8095;
}
.plan-detail dd {
  margin: 0;
  overflow-wrap: anywhere;
}
.plan-detail code {
  overflow-wrap: anywhere;
}
.plan-detail pre {
  white-space: pre-wrap;
  max-height: 280px;
  overflow: auto;
  font-size: 12px;
}
.log-output {
  background: #edf3f8;
  color: #28445e;
  padding: 16px;
  border-radius: 6px;
  white-space: pre-wrap;
  overflow-wrap: anywhere;
  max-height: 60vh;
  overflow: auto;
  font:
    12px/1.6 Consolas,
    monospace;
}
.probe-result {
  max-width: 320px;
}
.dependency-section {
  margin-top: 28px;
  padding-top: 20px;
  border-top: 1px solid var(--border);
}
.dependency-section > summary {
  font-weight: 500;
  color: var(--text);
}
.probe-result dl {
  display: grid;
  grid-template-columns: auto 1fr;
  gap: 8px;
  font-size: 12px;
}
.probe-result dd {
  margin: 0;
  overflow-wrap: anywhere;
}
.version-list article {
  padding: 16px 0;
  border-bottom: 1px solid #dce5ef;
  display: grid;
  gap: 7px;
  overflow-wrap: anywhere;
}
details {
  margin-top: 8px;
}
details > .form-grid {
  margin-top: 12px;
}
summary {
  cursor: pointer;
  color: #507596;
  font-size: 13px;
}
select[multiple] {
  min-height: 65px;
}
@media (max-width: 600px) {
  .form-grid {
    grid-template-columns: 1fr;
  }
  .plan-detail {
    grid-template-columns: 75px minmax(0, 1fr);
  }
  .job-filters {
    width: 100%;
  }
  .job-filters select {
    flex: 1;
    min-width: 0;
  }
}
</style>
